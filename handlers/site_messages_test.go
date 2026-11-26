package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// writeSiteDatabase makes a website's SQLite file the way its server does.
func writeSiteDatabase(t *testing.T, path string, rows ...[2]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS submissions (id INTEGER PRIMARY KEY AUTOINCREMENT, form TEXT NOT NULL, data TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT (datetime('now')))`); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if _, err := db.Exec("INSERT INTO submissions (form, data) VALUES (?, ?)", row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOrderedFieldsKeepTheVisitorsOrder(t *testing.T) {
	fields := orderedFields(`{"name":"Rina","attend":true,"guests":2,"message":"Selamat!"}`)
	want := []messageField{{"name", "Rina"}, {"attend", "true"}, {"guests", "2"}, {"message", "Selamat!"}}
	if len(fields) != len(want) {
		t.Fatalf("fields = %v", fields)
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Errorf("field %d = %v, want %v", i, fields[i], want[i])
		}
	}
	if got := orderedFields("not json"); len(got) != 1 || got[0].Value != "not json" {
		t.Errorf("text that is not JSON = %v", got)
	}
}

// The owner reads what visitors sent, newest first; another person cannot; before
// publishing the preview's messages are shown as tests. Needs SKILLS_TEST_DATABASE_URL.
func TestOwnerReadsTheWebsitesMessages(t *testing.T) {
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
	t.Setenv("RESEND_API_KEY", "") // no e-mail leaves a test

	suffix := strconv.Itoa(os.Getpid())
	owner := models.User{Name: "Owner", Email: "messages-owner-" + suffix + "@example.test", NotifyNewMessages: true}
	stranger := models.User{Name: "Stranger", Email: "messages-stranger-" + suffix + "@example.test"}
	for _, u := range []*models.User{&owner, &stranger} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	site := models.Site{UserID: owner.ID, Name: "Dimas dan Ayu", Status: models.SiteStatusReady}
	if err := db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Delete(&site)
		db.Unscoped().Delete(&owner)
		db.Unscoped().Delete(&stranger)
	})

	get := func(as uint) (int, map[string]any) {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("user_id", as); c.Next() })
		r.GET("/sites/:id/messages", GetSiteMessages)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, httptest.NewRequest("GET", "/sites/"+strconv.Itoa(int(site.ID))+"/messages", nil))
		var body map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &body)
		return res.Code, body
	}

	// No visitor yet: an empty list, not an error.
	if code, body := get(owner.ID); code != http.StatusOK || len(body["messages"].([]any)) != 0 {
		t.Fatalf("no messages yet = %d %v", code, body)
	}

	writeSiteDatabase(t, siteDatabasePath(site.ID, "preview"), [2]string{"rsvp", `{"name":"Tes","message":"coba"}`})
	writeSiteDatabase(t, siteDatabasePath(site.ID, "live"),
		[2]string{"rsvp", `{"name":"Rina","attend":"hadir"}`},
		[2]string{"rsvp", `{"name":"Budi","attend":"tidak"}`})

	code, body := get(owner.ID)
	messages := body["messages"].([]any)
	if code != http.StatusOK || len(messages) != 1 || body["test"] != true {
		t.Fatalf("before publishing = %d %v, want the preview's one test message", code, body)
	}

	release := uint(1)
	db.Model(&site).Update("published_release_id", release)
	code, body = get(owner.ID)
	messages = body["messages"].([]any)
	if code != http.StatusOK || len(messages) != 2 || body["test"] != false {
		t.Fatalf("published = %d %v", code, body)
	}
	first := messages[0].(map[string]any)["fields"].([]any)[0].(map[string]any)
	if first["value"] != "Budi" {
		t.Errorf("newest first: first message = %v", first)
	}
	if code, _ := get(stranger.ID); code != http.StatusNotFound {
		t.Errorf("another person reading the messages = %d, want 404", code)
	}

	// The first check only notes where the website stands; a later one sees new messages.
	CheckNewSiteMessages()
	var seen models.Site
	db.First(&seen, site.ID)
	if seen.MessagesNotifiedID == nil || *seen.MessagesNotifiedID != 2 {
		t.Fatalf("after the first check, notified up to %v, want 2", seen.MessagesNotifiedID)
	}
	if _, count, err := newestSiteMessage(siteDatabasePath(site.ID, "live"), 2); err != nil || count != 0 {
		t.Errorf("new since the check = %d %v", count, err)
	}
	writeSiteDatabase(t, siteDatabasePath(site.ID, "live"), [2]string{"rsvp", `{"name":"Sari"}`})
	if newest, count, err := newestSiteMessage(siteDatabasePath(site.ID, "live"), 2); err != nil || newest != 3 || count != 1 {
		t.Errorf("a new message: newest %d, count %d, err %v", newest, count, err)
	}
}
