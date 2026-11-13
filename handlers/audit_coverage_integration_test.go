package handlers

import (
	"bytes"
	"log"
	"os"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

func auditCount(t *testing.T, action, entityID string) int64 {
	t.Helper()
	var n int64
	if err := config.DB.Model(&models.AuditLog{}).Where("action = ? AND entity_id = ?", action, entityID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAdminSignInIsAudited(t *testing.T) {
	f := newGuardFixture(t)
	id := strconv.Itoa(int(f.admin.ID))
	t.Cleanup(func() { config.DB.Where("entity_id = ? AND action = ?", id, "admin.login").Delete(&models.AuditLog{}) })
	before := auditCount(t, "admin.login", id)
	if rec := f.post("/admin/login", f.admin.Email, "correct horse", "198.51.100.60"); rec.Code != http.StatusOK {
		t.Fatalf("admin login = %d %s", rec.Code, rec.Body.String())
	}
	if auditCount(t, "admin.login", id) != before+1 {
		t.Fatal("an admin sign-in left no audit entry")
	}
}

func TestAdminCreatedUserIsAudited(t *testing.T) {
	f := newGuardFixture(t)
	f.asAdmin()
	email := "guard-created-" + f.suffix + "@example.test"
	t.Cleanup(func() {
		var created models.User
		if config.DB.Where("email = ?", email).First(&created).Error == nil {
			config.DB.Where("entity_id = ? AND action = ?", strconv.Itoa(int(created.ID)), "user.created_by_admin").Delete(&models.AuditLog{})
			config.DB.Unscoped().Delete(&created)
		}
	})
	if rec := f.send(http.MethodPost, "/admin/users", `{"name":"Created","email":"`+email+`","password":"correct horse","role":"admin"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	var created models.User
	config.DB.Where("email = ?", email).First(&created)
	if auditCount(t, "user.created_by_admin", strconv.Itoa(int(created.ID))) != 1 {
		t.Fatal("creating a user (an admin here) left no audit entry")
	}
}

func TestMaintenanceChangeIsAudited(t *testing.T) {
	f := newGuardFixture(t)
	f.router.PUT("/admin/settings/maintenance", func(c *gin.Context) {
		c.Set("user_id", f.admin.ID)
		c.Set("user_email", f.admin.Email)
	}, UpdateMaintenanceStatus)
	before := auditCount(t, "settings.maintenance_changed", "maintenance_mode")
	t.Cleanup(func() {
		config.DB.Exec("UPDATE system_settings SET maintenance_mode = false")
	})
	if rec := f.send(http.MethodPut, "/admin/settings/maintenance", `{"maintenance":false}`); rec.Code != http.StatusOK {
		t.Fatalf("maintenance = %d %s", rec.Code, rec.Body.String())
	}
	if auditCount(t, "settings.maintenance_changed", "maintenance_mode") != before+1 {
		t.Fatal("a maintenance change left no audit entry")
	}
}

func TestFailedSignInIsLoggedWithoutTheAddress(t *testing.T) {
	f := newGuardFixture(t)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	f.post("/login", f.user.Email, "wrong", "198.51.100.61")
	out := buf.String()
	if !strings.Contains(out, "event=login_failed") {
		t.Fatalf("no security event for a failed sign-in: %q", out)
	}
	if strings.Contains(strings.ToLower(out), strings.ToLower(f.user.Email)) {
		t.Fatalf("the address reached the log: %q", out)
	}
}

func TestReusedRefreshTokenIsAudited(t *testing.T) {
	f := newGuardFixture(t)
	f.router.POST("/refresh", Refresh)
	id := strconv.Itoa(int(f.user.ID))
	t.Cleanup(func() { config.DB.Where("entity_id = ? AND action = ?", id, "security.refresh_token_reused").Delete(&models.AuditLog{}) })
	cookie := cookieNamed(f.post("/login", f.user.Email, "correct horse", "10.2.0.9"), "refresh_token")
	if cookie == nil {
		t.Fatal("login set no refresh cookie")
	}
	past := time.Now().Add(-refreshReuseGrace - time.Second)
	config.DB.Model(&models.RefreshToken{}).Where("token_hash = ?", hashToken(cookie.Value)).
		Updates(map[string]any{"revoked_at": past, "replaced_at": past})
	before := auditCount(t, "security.refresh_token_reused", id)
	if rec := f.refresh("/refresh", cookie); rec.Code != http.StatusUnauthorized {
		t.Fatalf("reuse = %d, want 401", rec.Code)
	}
	if auditCount(t, "security.refresh_token_reused", id) != before+1 {
		t.Fatal("a reused refresh token left no audit entry")
	}
}
