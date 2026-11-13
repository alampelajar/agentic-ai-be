package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func githubCallbackRequest(state, cookie string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/integrations/github/callback", GitHubCallback)
	request := httptest.NewRequest(http.MethodGet, "/api/integrations/github/callback?code=abc&state="+state, nil)
	if cookie != "" {
		request.AddCookie(&http.Cookie{Name: githubStateCookie, Value: cookie})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

// A link made by someone else carries their state, but the victim's browser has no
// matching cookie: the callback must stop before the state is used (no database here,
// so reaching it would panic) and before any code is exchanged.
func TestGitHubCallbackRefusesStateFromAnotherBrowser(t *testing.T) {
	state := strings.Repeat("a", 64)
	for name, cookie := range map[string]string{"no cookie": "", "other state": strings.Repeat("b", 64), "short cookie": "a"} {
		t.Run(name, func(t *testing.T) {
			response := githubCallbackRequest(state, cookie)
			if response.Code != http.StatusFound || !strings.Contains(response.Header().Get("Location"), "github=error") {
				t.Fatalf("status %d, location %q", response.Code, response.Header().Get("Location"))
			}
		})
	}
}

func TestGitHubStateCookieIsScopedAndHidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	setGitHubStateCookie(c, strings.Repeat("a", 64), 600)
	header := response.Header().Get("Set-Cookie")
	for _, want := range []string{githubStateCookie + "=", "Path=" + githubStateCookiePath, "HttpOnly", "SameSite=Lax", "Max-Age=600"} {
		if !strings.Contains(header, want) {
			t.Errorf("Set-Cookie %q lacks %q", header, want)
		}
	}
}
