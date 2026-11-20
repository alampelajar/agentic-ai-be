package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// Admins publish, move, rename, take offline and delete any website, each recorded,
// and the Logs list shows only websites made, published and deleted, including the
// making of a website that was never recorded. Needs SKILLS_TEST_DATABASE_URL.
func TestAdminManagesWebsites(t *testing.T) {
	dsn := os.Getenv("SKILLS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKILLS_TEST_DATABASE_URL is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	config.DB = db
	if err := migrateAll(db); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SITE_DATA_ROOT", t.TempDir())

	suffix := strconv.Itoa(os.Getpid())
	owner := models.User{Name: "Pemilik", Email: "admin-manage-" + suffix + "@example.test"}
	admin := models.User{Name: "Admin", Email: "admin-manage-admin-" + suffix + "@example.test", Role: "admin"}
	for _, u := range []*models.User{&owner, &admin} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	site := models.Site{UserID: owner.ID, Name: "Kedai Kopi", Status: models.SiteStatusReady}
	draft := models.Site{UserID: owner.ID, Name: "Belum jadi", Status: models.SiteStatusDraft}
	for _, s := range []*models.Site{&site, &draft} {
		if err := db.Create(s).Error; err != nil {
			t.Fatal(err)
		}
	}
	version := models.SiteVersion{SiteID: site.ID, Number: 1}
	db.Create(&version)
	db.Model(&site).Update("current_version_id", version.ID)
	ids := []string{strconv.FormatUint(uint64(site.ID), 10), strconv.FormatUint(uint64(draft.ID), 10)}
	t.Cleanup(func() {
		db.Where("site_id IN ?", []uint{site.ID, draft.ID}).Delete(&models.SiteRelease{})
		db.Where("site_id IN ?", []uint{site.ID, draft.ID}).Delete(&models.SiteVersion{})
		db.Where("id IN ?", []uint{site.ID, draft.ID}).Delete(&models.Site{})
		db.Where("entity_type = ? AND entity_id IN ?", "site", ids).Delete(&models.AuditLog{})
		db.Unscoped().Delete(&owner)
		db.Unscoped().Delete(&admin)
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", admin.ID)
		c.Set("user_email", admin.Email)
		c.Set("user_name", admin.Name)
		c.Set("user_role", "admin")
	})
	router.GET("/sites", AdminGetSites)
	router.PATCH("/sites/:id", AdminUpdateSite)
	router.POST("/sites/:id/publish", AdminPublishSite)
	router.POST("/sites/:id/unpublish", AdminUnpublishSite)
	router.DELETE("/sites/:id", AdminDeleteSite)
	router.GET("/site-logs", AdminGetSiteLogs)
	call := func(method, path, body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	path := "/sites/" + ids[0]
	slug := "kopi-" + suffix
	readSite := func() models.Site {
		var s models.Site
		db.First(&s, site.ID)
		return s
	}

	// Publish: refused without a version, refused at a reserved address, then live.
	if code, _ := call(http.MethodPost, "/sites/"+ids[1]+"/publish", `{"slug":"belum-jadi-`+suffix+`"}`); code != http.StatusConflict {
		t.Fatalf("publish without a version = %d", code)
	}
	if code, body := call(http.MethodPost, path+"/publish", `{"slug":"admin"}`); code != http.StatusConflict || body["code"] != "slug_reserved" {
		t.Fatalf("reserved address = %d %v", code, body)
	}
	if code, body := call(http.MethodPost, path+"/publish", `{"slug":"`+slug+`"}`); code != http.StatusOK || !strings.Contains(body["url"].(string), slug) {
		t.Fatalf("publish = %d %v", code, body)
	}
	if readSite().PublishedReleaseID == nil {
		t.Fatal("the website is not published")
	}

	// Publish again without an address keeps the address and makes release 2.
	if code, body := call(http.MethodPost, path+"/publish", `{}`); code != http.StatusOK || body["release"].(map[string]any)["number"].(float64) != 2 {
		t.Fatalf("publish again = %d %v", code, body)
	}

	// The list shows the address and the published version.
	_, body := call(http.MethodGet, "/sites", "")
	for _, raw := range body["sites"].([]any) {
		entry := raw.(map[string]any)
		if uint(entry["id"].(float64)) == site.ID {
			if entry["slug"] != slug || entry["published_version"].(float64) != 1 || entry["published_at"] == nil {
				t.Fatalf("listed website is wrong: %v", entry)
			}
		}
	}

	// Rename: empty refused, a name stored and recorded.
	if code, _ := call(http.MethodPatch, path, `{"name":"  "}`); code != http.StatusBadRequest {
		t.Fatalf("empty name = %d", code)
	}
	if code, _ := call(http.MethodPatch, path, `{"name":"Kedai Kopi Senja"}`); code != http.StatusOK || readSite().Name != "Kedai Kopi Senja" {
		t.Fatalf("rename = %d", code)
	}
	if auditCount(t, "site.rename", ids[0]) != 1 {
		t.Fatal("renaming was not recorded")
	}

	// Take offline, then refuse to take it offline twice.
	if code, _ := call(http.MethodPost, path+"/unpublish", ""); code != http.StatusOK || readSite().PublishedReleaseID != nil {
		t.Fatalf("unpublish = %d", code)
	}
	if code, _ := call(http.MethodPost, path+"/unpublish", ""); code != http.StatusConflict {
		t.Fatalf("unpublish twice = %d", code)
	}
	if auditCount(t, "site.unpublish", ids[0]) != 1 {
		t.Fatal("taking offline was not recorded")
	}

	// Logs: made (from the site, never recorded), published twice; no rename or unpublish.
	time.Sleep(10 * time.Millisecond)
	_, body = call(http.MethodGet, "/site-logs", "")
	actions := map[string]int{}
	for _, raw := range body["logs"].([]any) {
		entry := raw.(map[string]any)
		if entry["site_id"] != ids[0] {
			continue
		}
		actions[entry["action"].(string)]++
		if entry["action"] == "site.create" && entry["actor"].(map[string]any)["email"] != owner.Email {
			t.Fatalf("the maker should be the owner: %v", entry)
		}
		if entry["action"] == "site.publish" && entry["actor"].(map[string]any)["email"] != admin.Email {
			t.Fatalf("the publisher should be the admin: %v", entry)
		}
	}
	if actions["site.create"] != 1 || actions["site.publish"] != 2 || len(actions) != 2 {
		t.Fatalf("logged actions are wrong: %v", actions)
	}

	// Delete: the website is gone and the deletion is logged.
	if code, _ := call(http.MethodDelete, path, ""); code != http.StatusOK {
		t.Fatalf("delete = %d", code)
	}
	var left int64
	db.Model(&models.Site{}).Where("id = ?", site.ID).Count(&left)
	if left != 0 {
		t.Fatal("the website was not deleted")
	}
	if code, _ := call(http.MethodDelete, path, ""); code != http.StatusNotFound {
		t.Fatalf("delete twice = %d", code)
	}
	_, body = call(http.MethodGet, "/site-logs", "")
	deleted := false
	for _, raw := range body["logs"].([]any) {
		entry := raw.(map[string]any)
		if entry["site_id"] == ids[0] && entry["action"] == "site.delete" {
			deleted = true
		}
	}
	if !deleted {
		t.Fatal("the deletion is missing from the logs")
	}
}
