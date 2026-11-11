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

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestDesignDocumentValid(t *testing.T) {
	for document, want := range map[string]bool{`{"nodes":[]}`: true, ` {} `: true, `[]`: false, `{`: false, `"x"`: false, ``: false} {
		if got := designDocumentValid(json.RawMessage(document)); got != want {
			t.Errorf("%q = %v, want %v", document, got, want)
		}
	}
	tooBig := `{"a":"` + strings.Repeat("x", models.DesignProjectMaxDocumentLen) + `"}`
	if designDocumentValid(json.RawMessage(tooBig)) {
		t.Error("an oversized document was accepted")
	}
}

// Projects are private to their owner, and a stale revision cannot overwrite a
// newer save. Needs SKILLS_TEST_DATABASE_URL.
func TestDesignProjectsOwnershipAndRevision(t *testing.T) {
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
	suffix := strconv.Itoa(os.Getpid())
	owner := models.User{Name: "Owner", Email: "design-owner-" + suffix + "@example.test"}
	other := models.User{Name: "Other", Email: "design-other-" + suffix + "@example.test"}
	for _, u := range []*models.User{&owner, &other} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		db.Where("user_id IN ?", []uint{owner.ID, other.ID}).Delete(&models.DesignProject{})
		db.Delete(&owner)
		db.Delete(&other)
	}()

	send := func(as uint, method, path, body string) (int, map[string]any) {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("user_id", as); c.Next() })
		router.GET("/design-projects", GetDesignProjects)
		router.POST("/design-projects", CreateDesignProject)
		router.GET("/design-projects/:id", GetDesignProject)
		router.PUT("/design-projects/:id", UpdateDesignProject)
		router.DELETE("/design-projects/:id", DeleteDesignProject)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
		var parsed map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &parsed)
		return res.Code, parsed
	}

	code, created := send(owner.ID, "POST", "/design-projects", `{"name":"Landing","document":{"nodes":[]}}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %v", code, created)
	}
	id := strconv.Itoa(int(created["project"].(map[string]any)["id"].(float64)))
	path := "/design-projects/" + id

	if code, _ := send(other.ID, "GET", path, ""); code != http.StatusNotFound {
		t.Errorf("another user reading = %d, want 404", code)
	}
	if code, _ := send(other.ID, "PUT", path, `{"revision":1,"document":{}}`); code != http.StatusNotFound {
		t.Errorf("another user saving = %d, want 404", code)
	}
	if code, _ := send(other.ID, "DELETE", path, ""); code != http.StatusNotFound {
		t.Errorf("another user deleting = %d, want 404", code)
	}
	if code, saved := send(owner.ID, "PUT", path, `{"revision":1,"document":{"nodes":[1]}}`); code != http.StatusOK || saved["revision"].(float64) != 2 {
		t.Errorf("save = %d %v", code, saved)
	}
	if code, stale := send(owner.ID, "PUT", path, `{"revision":1,"document":{"nodes":[2]}}`); code != http.StatusConflict || stale["revision"].(float64) != 2 {
		t.Errorf("stale save = %d %v", code, stale)
	}
	_, loaded := send(owner.ID, "GET", path, "")
	document, _ := json.Marshal(loaded["project"].(map[string]any)["document"])
	if string(document) != `{"nodes":[1]}` {
		t.Errorf("stored document = %s", document)
	}
	if code, listed := send(owner.ID, "GET", "/design-projects", ""); code != http.StatusOK || len(listed["projects"].([]any)) != 1 {
		t.Errorf("list = %d %v", code, listed)
	}
	if code, _ := send(owner.ID, "DELETE", path, ""); code != http.StatusOK {
		t.Errorf("delete = %d", code)
	}
}
