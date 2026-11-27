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
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/middleware"
	"agentic-ai-backend/models"
)

// A suspended account cannot sign in, its open session and refresh token stop at once,
// its published websites go offline and its queued work stops; lifting the suspension
// gives the account back. Needs SKILLS_TEST_DATABASE_URL.
func TestAccountSuspension(t *testing.T) {
	dsn := os.Getenv("SKILLS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKILLS_TEST_DATABASE_URL is not configured")
	}
	t.Setenv("EMAIL_ALLOWED_DOMAINS", "example.test")
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
	t.Setenv("JWT_ACCESS_SECRET", strings.Repeat("a", 40))
	t.Setenv("JWT_REFRESH_SECRET", strings.Repeat("b", 40))

	suffix := strconv.Itoa(os.Getpid())
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct horse"), bcrypt.MinCost)
	user := models.User{Name: "Penjudi", Email: "suspended-" + suffix + "@example.test", Password: string(hash), Role: "user"}
	admin := models.User{Name: "Admin", Email: "suspender-" + suffix + "@example.test", Password: string(hash), Role: "admin"}
	for _, u := range []*models.User{&user, &admin} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	release := uint(987654)
	site := models.Site{UserID: user.ID, WorkspaceID: 1, Name: "Toko", Status: models.SiteStatusBuilding, PublishedReleaseID: &release}
	if err := db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	run := models.AgentRun{SiteID: site.ID, UserID: user.ID, Kind: "revise", Status: models.RunQueued}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	ids := []uint{user.ID, admin.ID}
	t.Cleanup(func() {
		db.Where("user_id IN ?", ids).Delete(&models.AgentRun{})
		db.Where("user_id IN ?", ids).Delete(&models.Site{})
		db.Where("user_id IN ?", ids).Delete(&models.RefreshToken{})
		db.Where("entity_type = ? AND entity_id IN ?", "user", []string{strconv.Itoa(int(user.ID)), strconv.Itoa(int(admin.ID))}).Delete(&models.AuditLog{})
		db.Unscoped().Delete(&models.User{}, ids)
	})

	gin.SetMode(gin.TestMode)
	asAdmin := func(c *gin.Context) { c.Set("user_id", admin.ID); c.Set("user_role", "admin"); c.Next() }
	router := gin.New()
	router.POST("/login", Login)
	router.POST("/refresh", Refresh)
	router.GET("/me", middleware.AuthMiddleware(), Me)
	router.GET("/admin/users", asAdmin, AdminGetUsers)
	router.PATCH("/admin/users/:id/suspend", asAdmin, AdminSuspendUser)
	call := func(method, path, body, bearer, cookie string) (int, map[string]any, *httptest.ResponseRecorder) {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "192.0.2.78:4000"
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie != "" {
			request.AddCookie(&http.Cookie{Name: "refresh_token", Value: cookie})
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		var parsed map[string]any
		_ = json.Unmarshal(recorder.Body.Bytes(), &parsed)
		return recorder.Code, parsed, recorder
	}
	login := func() (int, map[string]any, string) {
		code, body, recorder := call("POST", "/login", `{"email":"`+user.Email+`","password":"correct horse"}`, "", "")
		refresh := ""
		for _, cookie := range recorder.Result().Cookies() {
			if cookie.Name == "refresh_token" {
				refresh = cookie.Value
			}
		}
		return code, body, refresh
	}
	suspendPath := "/admin/users/" + strconv.Itoa(int(user.ID)) + "/suspend"

	code, body, refresh := login()
	access, _ := body["access_token"].(string)
	if code != http.StatusOK || access == "" || refresh == "" {
		t.Fatalf("sign-in before = %d %v", code, body)
	}
	if code, _, _ := call("GET", "/me", "", access, ""); code != http.StatusOK {
		t.Fatalf("/me before = %d", code)
	}

	// A reason is required, and an admin account cannot be suspended.
	if code, body, _ := call("PATCH", suspendPath, `{"suspended":true,"reason":"  "}`, "", ""); code != http.StatusBadRequest || body["code"] != "reason_required" {
		t.Errorf("suspend without a reason = %d %v", code, body)
	}
	if code, _, _ := call("PATCH", "/admin/users/"+strconv.Itoa(int(admin.ID))+"/suspend", `{"suspended":true,"reason":"x"}`, "", ""); code != http.StatusConflict {
		t.Errorf("suspending an admin = %d, want 409", code)
	}

	reason := "Mencoba membuat website judi online."
	code, body, _ = call("PATCH", suspendPath, `{"suspended":true,"reason":"`+reason+`"}`, "", "")
	if code != http.StatusOK || body["websites_offline"] != float64(1) || body["runs_stopped"] != float64(1) {
		t.Fatalf("suspend = %d %v", code, body)
	}

	// The token from before stops at once, the refresh token too, and sign-in says why.
	if code, body, _ := call("GET", "/me", "", access, ""); code != http.StatusForbidden || body["code"] != "account_suspended" || body["reason"] != reason {
		t.Errorf("/me after suspending = %d %v, want 403 account_suspended with the reason", code, body)
	}
	if code, _, _ := call("POST", "/refresh", "", "", refresh); code == http.StatusOK {
		t.Error("a refresh token from before still works")
	}
	if code, body, _ := login(); code != http.StatusForbidden || body["code"] != "account_suspended" || body["access_token"] != nil {
		t.Errorf("sign-in while suspended = %d %v", code, body)
	}

	// Its website is offline and its queued work stopped; nothing was deleted.
	var stored models.Site
	db.First(&stored, site.ID)
	if stored.PublishedReleaseID != nil || stored.Status != models.SiteStatusDraft {
		t.Errorf("website after suspending: release %v, status %s", stored.PublishedReleaseID, stored.Status)
	}
	var storedRun models.AgentRun
	db.First(&storedRun, run.ID)
	if storedRun.Status != models.RunCancelled {
		t.Errorf("queued work after suspending = %s, want cancelled", storedRun.Status)
	}

	// The console shows it, and the audit log has it.
	_, list, _ := call("GET", "/admin/users", "", "", "")
	found := false
	for _, entry := range list["users"].([]any) {
		u := entry.(map[string]any)
		if u["id"] == float64(user.ID) {
			found = u["status"] == "Suspended" && u["suspend_reason"] == reason && u["suspended_at"] != nil
		}
	}
	if !found {
		t.Error("the users list does not show the suspension")
	}
	var audits int64
	db.Model(&models.AuditLog{}).Where("action = ? AND entity_id = ?", "user.suspend", strconv.Itoa(int(user.ID))).Count(&audits)
	if audits != 1 {
		t.Errorf("user.suspend audited %d times, want 1", audits)
	}

	// Lifting it gives the account back.
	if code, _, _ := call("PATCH", suspendPath, `{"suspended":false}`, "", ""); code != http.StatusOK {
		t.Fatalf("unsuspend = %d", code)
	}
	if code, body, _ := login(); code != http.StatusOK || body["access_token"] == nil {
		t.Errorf("sign-in after lifting = %d %v", code, body)
	}
}
