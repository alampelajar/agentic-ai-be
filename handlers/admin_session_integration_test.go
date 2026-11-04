package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func (f *guardFixture) refresh(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
	request.AddCookie(cookie)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, request)
	return rec
}

// The admin console and the user app share a host, and a cookie is shared across
// ports, so the two sessions must not use the same cookie.
func TestAdminAndUserSessionsUseSeparateCookies(t *testing.T) {
	f := newGuardFixture(t)
	f.router.POST("/refresh", Refresh)
	f.router.POST("/admin/refresh", AdminRefresh)
	f.router.POST("/admin/logout", AdminLogout)

	adminLogin := f.post("/admin/login", f.admin.Email, "correct horse", "10.1.0.1")
	if adminLogin.Code != http.StatusOK {
		t.Fatalf("admin login: %d %s", adminLogin.Code, adminLogin.Body.String())
	}
	if cookieNamed(adminLogin, "refresh_token") != nil {
		t.Fatal("admin login must not set the user app's refresh cookie")
	}
	adminCookie := cookieNamed(adminLogin, "admin_refresh_token")
	if adminCookie == nil || adminCookie.Path != "/api/admin" || !adminCookie.HttpOnly {
		t.Fatalf("admin cookie = %+v, want an HttpOnly cookie for /api/admin", adminCookie)
	}

	userLogin := f.post("/login", f.user.Email, "correct horse", "10.1.0.2")
	if userLogin.Code != http.StatusOK {
		t.Fatalf("user login: %d %s", userLogin.Code, userLogin.Body.String())
	}
	userCookie := cookieNamed(userLogin, "refresh_token")
	if userCookie == nil {
		t.Fatal("user login must set refresh_token")
	}
	if cookieNamed(userLogin, "admin_refresh_token") != nil {
		t.Fatal("user login must not touch the admin cookie")
	}

	// The admin session refreshes on its own cookie, whatever the user app did since.
	if rec := f.refresh("/admin/refresh", adminCookie); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "access_token") {
		t.Fatalf("admin refresh: %d %s", rec.Code, rec.Body.String())
	}

	// Each endpoint ignores the other app's cookie.
	userCookieAsAdmin := &http.Cookie{Name: "admin_refresh_token", Value: userCookie.Value}
	if rec := f.refresh("/admin/refresh", userCookieAsAdmin); rec.Code != http.StatusForbidden {
		t.Fatalf("a user's token on the admin refresh got %d, want 403", rec.Code)
	}
	if rec := f.refresh("/refresh", &http.Cookie{Name: "admin_refresh_token", Value: adminCookie.Value}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the admin cookie on the user refresh got %d, want 401", rec.Code)
	}

	// Admin logout clears only the admin cookie.
	logout := f.refresh("/admin/logout", adminCookie)
	cleared := cookieNamed(logout, "admin_refresh_token")
	if logout.Code != http.StatusOK || cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("admin logout: %d, cookie %+v", logout.Code, cleared)
	}
	if cookieNamed(logout, "refresh_token") != nil {
		t.Fatal("admin logout must leave the user app's session alone")
	}
}
