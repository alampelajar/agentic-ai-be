package services

import "testing"

func TestToolRegistryRejectsUnknownAndMissingArguments(t *testing.T) {
	if err := ValidateToolRequest("desktop.not_registered", nil); err == nil { t.Fatal("unknown tool must be rejected") }
	if err := ValidateToolRequest("desktop.type", map[string]any{}); err == nil { t.Fatal("missing text must be rejected") }
	if err := ValidateToolRequest("desktop.type", map[string]any{"text":"safe"}); err != nil { t.Fatalf("valid request rejected: %v", err) }
}

func TestToolsForSkillFiltersUnknownNames(t *testing.T) {
	tools := ToolsForSkill([]string{"desktop.type", "desktop.arbitrary"})
	if len(tools) != 1 || tools[0].Name != "desktop.type" { t.Fatalf("unexpected tools: %#v", tools) }
}
