package localrunner

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// A project's test script must not see the person's keys and tokens, nor the
// runner's own token; it still needs what tools use to find themselves.
func TestCommandEnvKeepsOnlyWhatToolsNeed(t *testing.T) {
	env := strings.Join(commandEnv([]string{
		`Path=C:\Go\bin`,
		"HOME=/home/rina",
		"GOCACHE=/cache/go",
		`SystemRoot=C:\Windows`,
		"LOCAL_RUNNER_TOKEN=runner-secret",
		"AWS_SECRET_ACCESS_KEY=aws-secret",
		"GITHUB_TOKEN=gh-secret",
		"OPENAI_API_KEY=sk-secret",
		"DATABASE_URL=postgres://u:p@h/db",
		`=C:=C:\odd-windows-entry`,
	}), "\n")
	for _, want := range []string{`Path=C:\Go\bin`, "HOME=/home/rina", "GOCACHE=/cache/go", `SystemRoot=C:\Windows`, "CI=true", "NO_COLOR=1"} {
		if !strings.Contains(env, want) {
			t.Errorf("%q was dropped", want)
		}
	}
	for _, secret := range []string{"runner-secret", "aws-secret", "gh-secret", "sk-secret", "postgres://"} {
		if strings.Contains(env, secret) {
			t.Errorf("a secret reached the command: %q", secret)
		}
	}
}

// The approval dialog reads the file first to show the change: a file that does
// not exist yet must answer 404, so the app shows "new file" instead of an error.
func TestReadFileAnswersNotFoundForAMissingFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	s := &Server{cfg: Config{Workspaces: []Workspace{{ID: "w", Path: root}}}}
	router := gin.New()
	router.GET("/api/workspaces/:id/file", s.readFile)

	for path, want := range map[string]int{"missing.txt": http.StatusNotFound, "../outside.txt": http.StatusBadRequest} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/w/file?path="+path, nil))
		if rec.Code != want {
			t.Errorf("%s = %d, want %d", path, rec.Code, want)
		}
	}
}
