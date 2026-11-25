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

func TestRunNoticeSpeaksTheBuildersLanguage(t *testing.T) {
	site := models.Site{Name: "Roti Senja", UILanguage: "id"}
	if subject, _, _, button := runNotice(site, models.AgentRun{Kind: models.AgentRunCreate}, true); subject != "Roti Senja sudah jadi" || button != "Buka website saya" {
		t.Errorf("Indonesian, made: %q %q", subject, button)
	}
	site.UILanguage = "en"
	if subject, _, _, _ := runNotice(site, models.AgentRun{Kind: models.AgentRunRevise}, true); !strings.Contains(subject, "change") {
		t.Errorf("English, change: %q", subject)
	}
	if _, _, body, _ := runNotice(site, models.AgentRun{Kind: models.AgentRunCreate}, false); !strings.Contains(body, "Continue") {
		t.Errorf("English, stopped: %q", body)
	}
}

// A person reads and saves the two e-mails they may switch off. Needs
// SKILLS_TEST_DATABASE_URL.
func TestNotificationSettingsAreSaved(t *testing.T) {
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
	user := models.User{Name: "Notify", Email: "notify-" + strconv.Itoa(os.Getpid()) + "@example.test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(&user) })

	call := func(method, body string) (int, map[string]any) {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("user_id", user.ID); c.Next() })
		r.GET("/me/notifications", GetMyNotifications)
		r.PUT("/me/notifications", UpdateMyNotifications)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, httptest.NewRequest(method, "/me/notifications", bytes.NewBufferString(body)))
		var parsed map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &parsed)
		return res.Code, parsed
	}

	// Both e-mails are on for a new account.
	code, body := call("GET", "")
	settings, _ := body["notifications"].(map[string]any)
	if code != http.StatusOK || settings["website_ready"] != true || settings["new_messages"] != true {
		t.Fatalf("new account = %d %v", code, body)
	}
	if code, _ := call("PUT", `{"website_ready":false}`); code != http.StatusBadRequest {
		t.Errorf("half a request = %d, want 400", code)
	}
	if code, _ := call("PUT", `{"website_ready":false,"new_messages":true}`); code != http.StatusOK {
		t.Fatalf("save = %d", code)
	}
	var saved models.User
	db.First(&saved, user.ID)
	if saved.NotifyWebsiteReady || !saved.NotifyNewMessages {
		t.Errorf("saved = website %v, messages %v", saved.NotifyWebsiteReady, saved.NotifyNewMessages)
	}
}
