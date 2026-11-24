package main

import (
	"os"
	"regexp"
	"testing"
)

// A visitor who has not signed in may look at the gallery, and only at the
// gallery: every other website route stays behind sign-in.
func TestOnlyTheGalleryIsOpenWithoutSignIn(t *testing.T) {
	source, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	public := regexp.MustCompile(`api\.(GET|POST|PUT|PATCH|DELETE)\(\s*"(/sites[^"]*)"`)
	found := public.FindAllStringSubmatch(string(source), -1)
	if len(found) != 1 || found[0][1] != "GET" || found[0][2] != "/sites/showcase" {
		t.Fatalf("public website routes = %v, want only GET /sites/showcase", found)
	}
	if regexp.MustCompile(`protected\.GET\(\s*"/sites/showcase"`).Match(source) {
		t.Error("GET /sites/showcase is registered twice; keep only the public one")
	}
}
