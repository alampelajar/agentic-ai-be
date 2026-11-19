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

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// The gallery shows only published websites an admin chose, without the owner's
// description, and a new website may copy only a website that is in it.
// Needs SKILLS_TEST_DATABASE_URL.
func TestSiteShowcase(t *testing.T) {
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
	owner := models.User{Name: "Owner", Email: "showcase-" + suffix + "@example.test"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	slug := "showcase-" + suffix
	brief := `{"name":"Raka & Nadia","pages":[{"slug":"beranda","title":"Beranda"},{"slug":"acara","title":"Acara"}]}`
	published := models.Site{UserID: owner.ID, Name: "Raka & Nadia", Description: "Nomor HP saya 0812", Status: models.SiteStatusReady,
		Slug: &slug, DesignDirection: "romantic-soft", BriefJSON: brief}
	draft := models.Site{UserID: owner.ID, Name: "Belum terbit", Status: models.SiteStatusReady}
	for _, s := range []*models.Site{&published, &draft} {
		if err := db.Create(s).Error; err != nil {
			t.Fatal(err)
		}
	}
	version := models.SiteVersion{SiteID: published.ID, Number: 1}
	db.Create(&version)
	release := models.SiteRelease{SiteID: published.ID, SiteVersionID: version.ID, Number: 1, Slug: slug, Status: models.ReleaseLive}
	db.Create(&release)
	db.Model(&published).Updates(map[string]any{"current_version_id": version.ID, "published_release_id": release.ID})
	t.Cleanup(func() {
		ids := []uint{published.ID, draft.ID}
		db.Where("site_id IN ?", ids).Delete(&models.SiteRelease{})
		db.Where("site_id IN ?", ids).Delete(&models.SiteVersion{})
		db.Where("id IN ?", ids).Delete(&models.Site{})
		for _, id := range ids {
			db.Where("entity_type = ? AND entity_id = ?", "site", strconv.FormatUint(uint64(id), 10)).Delete(&models.AuditLog{})
		}
		db.Unscoped().Delete(&owner)
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", owner.ID) })
	router.GET("/showcase", GetSiteShowcase)
	router.PATCH("/admin/sites/:id/showcase", AdminSetSiteShowcase)
	call := func(method, path, body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
		var parsed map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
		return rec.Code, parsed
	}
	listed := func() map[string]any {
		code, body := call(http.MethodGet, "/showcase?lang=id", "")
		if code != http.StatusOK {
			t.Fatalf("showcase = %d %v", code, body)
		}
		for _, s := range body["sites"].([]any) {
			if entry := s.(map[string]any); uint(entry["id"].(float64)) == published.ID {
				return entry
			}
		}
		return nil
	}

	if listed() != nil {
		t.Fatal("a website no admin chose is in the gallery")
	}
	if _, found := showcasedSite(published.ID); found {
		t.Fatal("a website no admin chose can be copied")
	}
	if code, _ := call(http.MethodPatch, "/admin/sites/"+strconv.Itoa(int(draft.ID))+"/showcase", `{"showcased":true}`); code != http.StatusConflict {
		t.Errorf("an unpublished website into the gallery = %d, want 409", code)
	}
	if code, body := call(http.MethodPatch, "/admin/sites/"+strconv.Itoa(int(published.ID))+"/showcase", `{"showcased":true}`); code != http.StatusOK {
		t.Fatalf("showcase = %d %v", code, body)
	}
	entry := listed()
	if entry == nil {
		t.Fatal("the chosen website is missing from the gallery")
	}
	if _, has := entry["description"]; has || strings.Contains(entry["live_url"].(string), slug) == false {
		t.Errorf("gallery entry = %v, want an address and no description", entry)
	}
	if pages := entry["pages"].([]any); len(pages) != 2 || pages[0] != "Beranda" {
		t.Errorf("pages = %v", pages)
	}
	if _, found := showcasedSite(published.ID); !found {
		t.Error("a website in the gallery cannot be copied")
	}
	var audit int64
	db.Model(&models.AuditLog{}).Where("action = ? AND entity_id = ?", "site.showcase", strconv.Itoa(int(published.ID))).Count(&audit)
	if audit != 1 {
		t.Errorf("audit entries = %d, want 1", audit)
	}

	// Taken offline: it leaves the gallery even though the admin's choice stays.
	db.Model(&published).Update("published_release_id", nil)
	if listed() != nil {
		t.Error("an unpublished website stays in the gallery")
	}
	db.Model(&published).Update("published_release_id", release.ID)
	if code, _ := call(http.MethodPatch, "/admin/sites/"+strconv.Itoa(int(published.ID))+"/showcase", `{"showcased":false}`); code != http.StatusOK || listed() != nil {
		t.Error("taking a website out of the gallery did not work")
	}
}
