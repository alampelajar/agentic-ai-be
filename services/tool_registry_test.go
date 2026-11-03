package services

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestToolRegistryRejectsUnknownAndMissingArguments(t *testing.T) {
	if err := ValidateToolRequest("desktop.not_registered", nil); err == nil { t.Fatal("unknown tool must be rejected") }
	if err := ValidateToolRequest("desktop.type", map[string]any{}); err == nil { t.Fatal("missing text must be rejected") }
	if err := ValidateToolRequest("desktop.type", map[string]any{"text":"safe"}); err != nil { t.Fatalf("valid request rejected: %v", err) }
}

func TestToolsForSkillFiltersUnknownNames(t *testing.T) {
	tools := ToolsForSkill([]string{"desktop.type", "desktop.arbitrary"})
	if len(tools) != 1 || tools[0].Name != "desktop.type" { t.Fatalf("unexpected tools: %#v", tools) }
}

// The frontend (src/features/ai/tool-call.ts) and the runner use these exact
// names. A rename here must be mirrored there, or the tool silently stops working.
func TestDesktopToolNamesAreCanonicalSnakeCase(t *testing.T) {
	want := []string{
		"desktop.click", "desktop.double_click", "desktop.focus_window", "desktop.hotkey", "desktop.inspect",
		"desktop.screenshot", "desktop.scroll", "desktop.status", "desktop.type", "desktop.windows",
	}
	var got []string
	for name := range ToolRegistry {
		if strings.HasPrefix(name, "desktop.") {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("desktop tools changed: got %v, want %v", got, want)
	}
	canonical := regexp.MustCompile(`^desktop\.[a-z]+(_[a-z]+)*$`)
	for _, name := range got {
		if !canonical.MatchString(name) {
			t.Errorf("%q is not canonical desktop.snake_case", name)
		}
		if CanonicalToolName(ProviderToolName(name)) != name {
			t.Errorf("%q does not survive the provider-safe round trip (%q)", name, ProviderToolName(name))
		}
	}
}
