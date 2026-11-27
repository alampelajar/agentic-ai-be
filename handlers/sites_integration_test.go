package handlers

import (
	"bytes"
	"context"
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
	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/internal/sitebuilder"
	"agentic-ai-backend/models"
)

// fakeBuilderModels answers like a model would, without calling one.
type fakeBuilderModels struct{}

func (fakeBuilderModels) Complete(_ context.Context, _ uint, _ sitebuilder.Need, messages []sitebuilder.Message) (string, string, error) {
	if strings.Contains(messages[0].Content, "ask only what is still missing") {
		return `{"kind":"food","suggested_name":"Dapur Uji","understood":[{"label":"Usaha","value":"Toko roti"}],"known_contact":{"whatsapp":"0812 1111"},"look_wish":"hijau","questions":[{"id":"pages","text":"Halaman?","multiple":true,"options":["Menu","Kontak"]}]}`, "fake", nil
	}
	return "```json\n" + `{"name":"x","tagline":"Roti hangat","summary":"Website untuk toko roti.","pages":[{"slug":"home","title":"Beranda","sections":["hero"]},{"slug":"menu","title":"Menu","sections":["daftar"]}],"forms":[{"name":"Pesan","fields":["nama"]}]}` + "\n```", "fake", nil
}

// Sites are private to their owner on every route, the flow up to a queued run works,
// and addresses are checked before publishing. Needs SKILLS_TEST_DATABASE_URL.
func TestSitesOwnershipAndFlow(t *testing.T) {
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
	storage, err := cloudworkspace.NewLocalDevelopmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	UseCloudWorkspaceStorage(storage, cloudworkspace.DefaultLimits(), nil)
	t.Setenv("SITE_DATA_ROOT", t.TempDir())
	t.Setenv("CLOUD_EXECUTION_ENABLED", "true")
	t.Setenv("SITE_PREVIEW_SECRET", strings.Repeat("p", 40))
	previous := builderModels
	builderModels = fakeBuilderModels{}
	t.Cleanup(func() { builderModels = previous })

	suffix := strconv.Itoa(os.Getpid())
	owner := models.User{Name: "Owner", Email: "site-owner-" + suffix + "@example.test"}
	other := models.User{Name: "Other", Email: "site-other-" + suffix + "@example.test"}
	for _, u := range []*models.User{&owner, &other} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		var siteIDs []uint
		db.Model(&models.Site{}).Where("user_id IN ?", []uint{owner.ID, other.ID}).Pluck("id", &siteIDs)
		db.Where("site_id IN ?", siteIDs).Delete(&models.AgentRun{})
		db.Where("site_id IN ?", siteIDs).Delete(&models.SiteRelease{})
		db.Where("site_id IN ?", siteIDs).Delete(&models.SiteVersion{})
		db.Where("user_id IN ?", []uint{owner.ID, other.ID}).Delete(&models.Site{})
		db.Where("user_id IN ?", []uint{owner.ID, other.ID}).Delete(&models.CloudWorkspace{})
		db.Where("actor_user_id IN ?", []uint{owner.ID, other.ID}).Delete(&models.AuditLog{})
		db.Delete(&owner)
		db.Delete(&other)
	}()

	router := func(as uint) *gin.Engine {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("user_id", as); c.Next() })
		r.GET("/sites", GetSites)
		r.POST("/sites", CreateSite)
		r.GET("/sites/slug-available", SiteSlugAvailable)
		r.GET("/sites/:id", GetSite)
		r.PATCH("/sites/:id", UpdateSite)
		r.DELETE("/sites/:id", DeleteSite)
		r.POST("/sites/:id/questions", SiteQuestions)
		r.POST("/sites/:id/brief", SiteBrief)
		r.DELETE("/sites/:id/images", DeleteSiteImage)
		r.POST("/sites/:id/runs", StartSiteRun)
		r.GET("/sites/:id/runs/:runId", GetSiteRun)
		r.POST("/sites/:id/runs/:runId/cancel", CancelSiteRun)
		r.POST("/sites/:id/versions/:number/restore", RestoreSiteVersion)
		r.GET("/sites/:id/versions/:number/files", SiteVersionFiles)
		r.GET("/sites/:id/versions/:number/file", SiteVersionFile)
		r.GET("/sites/:id/preview", SitePreview)
		r.POST("/sites/:id/publish", PublishSite)
		r.POST("/sites/:id/unpublish", UnpublishSite)
		return r
	}
	send := func(as uint, method, path, body string) (int, map[string]any) {
		res := httptest.NewRecorder()
		router(as).ServeHTTP(res, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
		var parsed map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &parsed)
		return res.Code, parsed
	}

	if code, _ := send(owner.ID, "POST", "/sites", `{"description":"short"}`); code != http.StatusBadRequest {
		t.Errorf("a too short description = %d, want 400", code)
	}
	// A long text pasted from elsewhere fits up to 10,000 characters, not one more.
	if code, body := send(owner.ID, "POST", "/sites", `{"description":"`+strings.Repeat("a", maxSiteTextLength+1)+`"}`); code != http.StatusBadRequest || body["code"] != "description_length" {
		t.Errorf("a description over the limit = %d %v, want 400 description_length", code, body["code"])
	}
	// The interface is in English, the description in Indonesian: the website is Indonesian.
	code, created := send(owner.ID, "POST", "/sites", `{"description":"Toko roti rumahan di Yogyakarta dengan warna hijau, pesan lewat WhatsApp.","language":"en"}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %v", code, created)
	}
	id := strconv.Itoa(int(created["site"].(map[string]any)["id"].(float64)))
	path := "/sites/" + id

	// Another person gets 404 on every route of this site.
	for _, request := range [][3]string{
		{"GET", path, ""}, {"PATCH", path, `{"name":"x"}`}, {"POST", path + "/questions", ""}, {"POST", path + "/brief", `{"direction":"warm-editorial"}`},
		{"DELETE", path + "/images?src=/uploads/x.png", ""}, {"POST", path + "/runs", `{"kind":"create"}`}, {"GET", path + "/runs/1", ""},
		{"POST", path + "/runs/1/cancel", ""}, {"POST", path + "/versions/1/restore", ""}, {"GET", path + "/versions/1/files", ""},
		{"GET", path + "/versions/1/file?path=site.json", ""}, {"GET", path + "/preview", ""},
		{"POST", path + "/publish", `{"slug":"toko-roti"}`}, {"POST", path + "/unpublish", ""}, {"DELETE", path, ""},
	} {
		if code, _ := send(other.ID, request[0], request[1], request[2]); code != http.StatusNotFound {
			t.Errorf("another person: %s %s = %d, want 404", request[0], request[1], code)
		}
	}
	if code, listed := send(other.ID, "GET", "/sites", ""); code != http.StatusOK || len(listed["sites"].([]any)) != 0 {
		t.Errorf("another person's list = %d %v", code, listed)
	}

	if code, _ := send(owner.ID, "POST", path+"/runs", `{"kind":"create"}`); code != http.StatusConflict {
		t.Errorf("a run before the brief = %d, want 409", code)
	}
	code, questions := send(owner.ID, "POST", path+"/questions", "")
	if code != http.StatusOK || len(questions["directions"].([]any)) != 3 || questions["suggested_name"] != "Dapur Uji" {
		t.Fatalf("questions = %d %v", code, questions)
	}
	if questions["language"] != "id" || len(questions["understood"].([]any)) != 1 || questions["known_contact"].(map[string]any)["whatsapp"] != "0812 1111" {
		t.Errorf("language, understood or contact not returned: %v", questions)
	}
	if code, _ := send(owner.ID, "POST", path+"/brief", `{"name":"Dapur Uji","direction":"no-such-look"}`); code != http.StatusBadRequest {
		t.Errorf("an unknown look = %d, want 400", code)
	}
	if code, _ := send(owner.ID, "POST", path+"/brief", `{"name":"Dapur Uji","direction":"warm-editorial","variant":"no-such-colour"}`); code != http.StatusBadRequest {
		t.Errorf("an unknown colour variant = %d, want 400", code)
	}
	code, brief := send(owner.ID, "POST", path+"/brief", `{"name":"Dapur Uji","answers":{"pages":["Menu"]},"contact":{"whatsapp":"0812 3456"},"direction":"warm-editorial","variant":"garden"}`)
	if code != http.StatusOK || brief["brief"].(map[string]any)["name"] != "Dapur Uji" || len(brief["brief"].(map[string]any)["pages"].([]any)) != 2 || brief["brief"].(map[string]any)["language"] != "id" {
		t.Fatalf("brief = %d %v", code, brief)
	}
	var stored models.Site
	db.First(&stored, created["site"].(map[string]any)["id"])
	if stored.Language != "id" || stored.UILanguage != "en" || stored.DesignVariant != "garden" {
		t.Errorf("stored language %q, interface %q, variant %q", stored.Language, stored.UILanguage, stored.DesignVariant)
	}
	code, run := send(owner.ID, "POST", path+"/runs", `{"kind":"create"}`)
	if code != http.StatusAccepted {
		t.Fatalf("run = %d %v", code, run)
	}
	if code, _ := send(owner.ID, "POST", path+"/runs", `{"kind":"create"}`); code != http.StatusConflict {
		t.Errorf("a second run at once = %d, want 409", code)
	}
	runID := strconv.Itoa(int(run["run"].(map[string]any)["id"].(float64)))
	if code, _ := send(owner.ID, "POST", path+"/runs/"+runID+"/cancel", ""); code != http.StatusOK {
		t.Errorf("cancel = %d", code)
	}
	if code, _ := send(owner.ID, "GET", path+"/preview", ""); code != http.StatusConflict {
		t.Errorf("a preview without a version = %d, want 409", code)
	}
	if code, _ := send(owner.ID, "POST", path+"/publish", `{"slug":"toko-roti"}`); code != http.StatusConflict {
		t.Errorf("publishing without a version = %d, want 409", code)
	}

	// Give the site a version, then check the address rules.
	siteID, _ := strconv.Atoi(id)
	version := models.SiteVersion{SiteID: uint(siteID), Number: 1, Summary: "v1"}
	db.Create(&version)
	db.Model(&models.Site{}).Where("id = ?", siteID).Update("current_version_id", version.ID)
	if code, body := send(owner.ID, "POST", path+"/runs", `{"kind":"revise","request":"`+strings.Repeat("a", maxSiteTextLength+1)+`"}`); code != http.StatusBadRequest || body["code"] != "request_length" {
		t.Errorf("a change request over the limit = %d %v, want 400 request_length", code, body["code"])
	}
	taken := "diambil-" + suffix
	otherSite := models.Site{UserID: other.ID, WorkspaceID: 0, Name: "Lain", Slug: &taken}
	db.Create(&otherSite)
	for slug, want := range map[string]string{"admin": "slug_reserved", "Ab": "slug_format", taken: "slug_taken"} {
		if code, answer := send(owner.ID, "POST", path+"/publish", `{"slug":"`+slug+`"}`); code != http.StatusConflict || answer["code"] != want {
			t.Errorf("publish at %q = %d %v, want %s", slug, code, answer["code"], want)
		}
	}
	slug := "roti-" + suffix
	if code, answer := send(owner.ID, "POST", path+"/publish", `{"slug":"`+slug+`"}`); code != http.StatusOK || !strings.Contains(answer["url"].(string), slug+".") {
		t.Errorf("publish = %d %v", code, answer)
	}
	if code, preview := send(owner.ID, "GET", path+"/preview", ""); code != http.StatusOK || !strings.Contains(preview["url"].(string), "p-"+id+"-") {
		t.Errorf("preview = %d %v", code, preview)
	}
	if code, _ := send(owner.ID, "DELETE", path, ""); code != http.StatusOK {
		t.Errorf("delete = %d", code)
	}
}
