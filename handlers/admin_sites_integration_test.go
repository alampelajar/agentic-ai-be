package handlers

import (
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

// Admins see the runs in progress with their owner and latest steps, every website
// with its address, and can open a preview of a website with a version.
// Needs SKILLS_TEST_DATABASE_URL.
func TestAdminSiteViews(t *testing.T) {
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
	t.Setenv("SITE_PREVIEW_SECRET", strings.Repeat("p", 40))

	suffix := strconv.Itoa(os.Getpid())
	owner := models.User{Name: "Builder", Email: "admin-sites-" + suffix + "@example.test"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	slug := "admin-sites-" + suffix
	building := models.Site{UserID: owner.ID, Name: "Toko Roti", Status: models.SiteStatusBuilding}
	published := models.Site{UserID: owner.ID, Name: "Portofolio", Status: models.SiteStatusReady, Slug: &slug}
	for _, s := range []*models.Site{&building, &published} {
		if err := db.Create(s).Error; err != nil {
			t.Fatal(err)
		}
	}
	version := models.SiteVersion{SiteID: published.ID, Number: 1}
	db.Create(&version)
	release := models.SiteRelease{SiteID: published.ID, SiteVersionID: version.ID, Number: 1, Slug: slug, Status: models.ReleaseLive}
	db.Create(&release)
	db.Model(&published).Updates(map[string]any{"current_version_id": version.ID, "published_release_id": release.ID})

	run := models.AgentRun{SiteID: building.ID, UserID: owner.ID, Kind: models.AgentRunCreate, Status: models.RunRunning}
	background := models.AgentRun{SiteID: building.ID, UserID: owner.ID, Kind: models.AgentRunCreate, Status: models.RunQueued, Background: true}
	db.Create(&run)
	db.Create(&background)
	for i, summary := range []string{"Menyusun halaman", "Menulis beranda", "Memeriksa hasil"} {
		db.Create(&models.AgentRunStep{AgentRunID: run.ID, Index: i, Kind: models.StepModel, Summary: summary, Status: models.StepDone})
	}
	t.Cleanup(func() {
		ids := []uint{building.ID, published.ID}
		db.Where("agent_run_id IN ?", []uint{run.ID, background.ID}).Delete(&models.AgentRunStep{})
		db.Where("site_id IN ?", ids).Delete(&models.AgentRun{})
		db.Where("site_id IN ?", ids).Delete(&models.SiteRelease{})
		db.Where("site_id IN ?", ids).Delete(&models.SiteVersion{})
		db.Where("id IN ?", ids).Delete(&models.Site{})
		db.Where("entity_type = ? AND entity_id = ?", "site", strconv.FormatUint(uint64(published.ID), 10)).Delete(&models.AuditLog{})
		db.Unscoped().Delete(&owner)
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", owner.ID) })
	router.GET("/active", AdminGetActiveSiteRuns)
	router.GET("/sites", AdminGetSites)
	router.POST("/sites/:id/preview", AdminSitePreview)
	call := func(method, path string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	// Active runs: the person's run, not the background one, with the newest step first.
	code, body := call(http.MethodGet, "/active")
	if code != http.StatusOK {
		t.Fatalf("active runs: %d %v", code, body)
	}
	var mine map[string]any
	for _, r := range body["runs"].([]any) {
		entry := r.(map[string]any)
		switch uint(entry["id"].(float64)) {
		case run.ID:
			mine = entry
		case background.ID:
			t.Fatal("a background run must not be listed")
		}
	}
	if mine == nil {
		t.Fatal("the running build is missing")
	}
	if mine["user"].(map[string]any)["email"] != owner.Email || mine["site"].(map[string]any)["name"] != "Toko Roti" {
		t.Fatalf("owner or site is wrong: %v", mine)
	}
	steps := mine["steps"].([]any)
	if mine["step_count"].(float64) != 3 || steps[0].(map[string]any)["summary"] != "Memeriksa hasil" {
		t.Fatalf("steps are wrong: %v", mine)
	}

	// History: both sites, the published one with its address.
	code, body = call(http.MethodGet, "/sites")
	if code != http.StatusOK {
		t.Fatalf("sites: %d %v", code, body)
	}
	found := 0
	for _, s := range body["sites"].([]any) {
		entry := s.(map[string]any)
		switch uint(entry["id"].(float64)) {
		case building.ID:
			found++
			if entry["live_url"] != "" {
				t.Fatalf("an unpublished site has an address: %v", entry)
			}
		case published.ID:
			found++
			if !strings.Contains(entry["live_url"].(string), slug) || entry["versions"].(float64) != 1 {
				t.Fatalf("published site is wrong: %v", entry)
			}
		}
	}
	if found != 2 {
		t.Fatalf("expected both sites, found %d", found)
	}

	// Preview: refused without a version, given with one, and recorded.
	if code, _ = call(http.MethodPost, "/sites/"+strconv.FormatUint(uint64(building.ID), 10)+"/preview"); code != http.StatusConflict {
		t.Fatalf("preview without a version: %d", code)
	}
	code, body = call(http.MethodPost, "/sites/"+strconv.FormatUint(uint64(published.ID), 10)+"/preview")
	if code != http.StatusOK || body["url"] == "" {
		t.Fatalf("preview: %d %v", code, body)
	}
	if auditCount(t, "site.preview", strconv.FormatUint(uint64(published.ID), 10)) != 1 {
		t.Fatal("opening a preview was not recorded")
	}
}
