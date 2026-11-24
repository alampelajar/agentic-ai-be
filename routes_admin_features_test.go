package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func noLimit(c *gin.Context) { c.Next() }

// The agent features left the person's app (WEBSITE-BUILDER.md, section 20): none of
// them may be registered on the signed-in user's group in routes.go again.
func TestBackgroundFeaturesAreNotOpenToUsers(t *testing.T) {
	source, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	// protected.GET("/x", ...) and the multi-line form protected.GET(\n "/x",
	registration := regexp.MustCompile(`protected\.(GET|POST|PUT|PATCH|DELETE)\(\s*"([^"]+)"`)
	for _, match := range registration.FindAllStringSubmatch(string(source), -1) {
		path := match[2]
		for _, moved := range backgroundFeaturePaths {
			if path == moved || strings.HasPrefix(path, moved+"/") {
				t.Errorf("%s %s is open to every signed-in user again; it belongs on the admin group", match[1], path)
			}
		}
	}
	if !strings.Contains(string(source), "registerBackgroundFeatures(admin,") {
		t.Error("routes.go must register the background features on the admin group")
	}
}

func TestBackgroundFeaturesAreServedUnderAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerBackgroundFeatures(router.Group("/api/admin"), noLimit, noLimit, noLimit)
	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
		if !strings.HasPrefix(route.Path, "/api/admin/") {
			t.Errorf("%s is outside /api/admin", route.Path)
		}
	}
	for _, want := range []string{"GET /api/admin/agent-catalog", "GET /api/admin/skills", "POST /api/admin/chat", "GET /api/admin/memories", "GET /api/admin/workspaces", "GET /api/admin/ai/combos", "GET /api/admin/ai/models/health"} {
		if !registered[want] {
			t.Errorf("%s is missing", want)
		}
	}
	// The person's own models and providers are gone: admins use the system model routes.
	for path := range registered {
		if strings.Contains(path, "/api/admin/ai/providers") || path == "POST /api/admin/ai/models" {
			t.Errorf("%s should not be registered here", path)
		}
	}
}
