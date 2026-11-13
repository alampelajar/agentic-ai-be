package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/middleware"
	"agentic-ai-backend/models"
)

func accessTokenFrom(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.AccessToken == "" {
		t.Fatalf("no access token in %d %s", rec.Code, rec.Body.String())
	}
	return body.AccessToken
}

func getWithToken(router *gin.Engine, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)
	return rec
}

// The role is read from the database on each request: an admin made an ordinary
// user loses admin access at once, with a token that still says "admin".
func TestDemotedAdminLosesAdminAccessAtOnce(t *testing.T) {
	f := newGuardFixture(t)
	f.router.GET("/admin/ping", middleware.AuthMiddleware(), middleware.AdminMiddleware(), func(c *gin.Context) { c.Status(http.StatusOK) })
	token := accessTokenFrom(t, f.post("/admin/login", f.admin.Email, "correct horse", "10.2.0.1"))

	if rec := getWithToken(f.router, "/admin/ping", token); rec.Code != http.StatusOK {
		t.Fatalf("admin before demotion = %d %s", rec.Code, rec.Body.String())
	}
	config.DB.Model(&models.User{}).Where("id = ?", f.admin.ID).Update("role", "user")
	if rec := getWithToken(f.router, "/admin/ping", token); rec.Code != http.StatusForbidden {
		t.Fatalf("demoted admin with an old admin token = %d, want 403", rec.Code)
	}
}

// Two tabs may refresh with the same cookie at once, so a just-rotated token still
// works for a short while. Used again later, it was copied: every session ends.
func TestReusedRefreshTokenEndsEverySession(t *testing.T) {
	f := newGuardFixture(t)
	f.router.POST("/refresh", Refresh)
	first := cookieNamed(f.post("/login", f.user.Email, "correct horse", "10.2.0.2"), "refresh_token")
	if first == nil {
		t.Fatal("login set no refresh cookie")
	}
	rotated := f.refresh("/refresh", first)
	second := cookieNamed(rotated, "refresh_token")
	if rotated.Code != http.StatusOK || second == nil {
		t.Fatalf("first refresh = %d %s", rotated.Code, rotated.Body.String())
	}
	// Another tab, at the same moment, with the cookie it read before the rotation.
	concurrent := f.refresh("/refresh", first)
	if concurrent.Code != http.StatusOK {
		t.Fatalf("refresh within the grace period = %d, want 200", concurrent.Code)
	}

	// Later, the old token comes back: someone kept a copy.
	past := time.Now().Add(-refreshReuseGrace - time.Second)
	config.DB.Model(&models.RefreshToken{}).Where("token_hash = ?", hashToken(first.Value)).
		Updates(map[string]any{"revoked_at": past, "replaced_at": past})
	if rec := f.refresh("/refresh", first); rec.Code != http.StatusUnauthorized {
		t.Fatalf("reuse after the grace period = %d, want 401", rec.Code)
	}
	if rec := f.refresh("/refresh", second); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a session issued before the reuse still works: %d", rec.Code)
	}
	var live int64
	config.DB.Model(&models.RefreshToken{}).Where("user_id = ? AND revoked_at IS NULL", f.user.ID).Count(&live)
	if live != 0 {
		t.Fatalf("%d sessions still live after reuse", live)
	}
}

// Feedback screenshots are only sent to the ticket's owner and to admins.
func TestFeedbackAttachmentNeedsOwnerOrAdmin(t *testing.T) {
	f := newGuardFixture(t)
	if err := config.DB.AutoMigrate(&models.Feedback{}, &models.FeedbackReply{}); err != nil {
		t.Fatal(err)
	}
	name := "test-" + f.suffix + ".png"
	if err := os.MkdirAll(filepath.Join("uploads", "feedback"), 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("uploads", "feedback", name)
	png := []byte("\x89PNG\r\n\x1a\n0000")
	if err := os.WriteFile(path, png, 0o640); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	ticket := models.Feedback{UserID: f.user.ID, Category: "bug", Subject: "s", Description: "d", Status: "new", Attachment: "/uploads/feedback/" + name}
	if err := config.DB.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { config.DB.Unscoped().Delete(&ticket) })

	auth := []gin.HandlerFunc{middleware.AuthMiddleware()}
	f.router.GET("/feedback/:id/attachment", append(auth, GetMyFeedbackAttachment)...)
	f.router.GET("/admin/feedback/:id/attachment", append(auth, middleware.AdminMiddleware(), AdminGetFeedbackAttachment)...)
	id := "/" + strconv.FormatUint(uint64(ticket.ID), 10) + "/attachment"

	owner := accessTokenFrom(t, f.post("/login", f.user.Email, "correct horse", "10.2.0.3"))
	if rec := getWithToken(f.router, "/feedback"+id, owner); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || rec.Body.String() != string(png) {
		t.Fatalf("owner = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	// Another signed-in user cannot read it through the user route.
	other := accessTokenFrom(t, f.post("/login", f.admin.Email, "correct horse", "10.2.0.4"))
	if rec := getWithToken(f.router, "/feedback"+id, other); rec.Code != http.StatusNotFound {
		t.Fatalf("another user's ticket = %d, want 404", rec.Code)
	}
	if rec := getWithToken(f.router, "/admin/feedback"+id, other); rec.Code != http.StatusOK {
		t.Fatalf("admin = %d", rec.Code)
	}
	if rec := getWithToken(f.router, "/admin/feedback"+id, owner); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin on the admin route = %d, want 403", rec.Code)
	}
}
