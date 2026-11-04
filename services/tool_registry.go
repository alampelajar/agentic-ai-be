package services

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ToolDefinition struct {
	Name, Description  string
	RequiredPermission string
	TimeoutSeconds     int
	InputSchema        map[string]any
}

var ToolRegistry = map[string]ToolDefinition{
	"list-files":           {Name: "list-files", Description: "List files in the selected workspace", RequiredPermission: "workspace.read", TimeoutSeconds: 10, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "description": "Directory path relative to the workspace root."}}}},
	"read-file":            {Name: "read-file", Description: "Read a file from the selected workspace", RequiredPermission: "workspace.read", TimeoutSeconds: 10, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "description": "File path relative to the workspace root."}}, "required": []string{"path"}}},
	"desktop.status":       {"desktop.status", "Check desktop runtime availability", "desktop.read", 10, map[string]any{"type": "object", "properties": map[string]any{}}},
	"desktop.screenshot":   {"desktop.screenshot", "Take one desktop screenshot", "desktop.read", 10, map[string]any{"type": "object", "properties": map[string]any{}}},
	"desktop.windows":      {"desktop.windows", "List visible windows", "desktop.read", 10, map[string]any{"type": "object", "properties": map[string]any{}}},
	"desktop.inspect":      {"desktop.inspect", "Inspect UI elements in a window", "desktop.read", 10, map[string]any{"type": "object", "properties": map[string]any{"window": windowProperty}, "required": []string{"window"}}},
	"desktop.click":        {"desktop.click", "Click a validated UI target", "desktop.interact", 15, map[string]any{"type": "object", "properties": map[string]any{"window": windowProperty, "target": targetProperty}}},
	"desktop.double_click": {"desktop.double_click", "Double-click a validated UI target", "desktop.interact", 15, map[string]any{"type": "object", "properties": map[string]any{"window": windowProperty, "target": targetProperty}}},
	"desktop.scroll":       {"desktop.scroll", "Scroll a validated window", "desktop.interact", 15, map[string]any{"type": "object", "properties": map[string]any{"window": windowProperty}}},
	"desktop.type":         {"desktop.type", "Type non-sensitive text", "desktop.interact", 15, map[string]any{"type": "object", "properties": map[string]any{"window": windowProperty, "text": map[string]any{"type": "string", "description": "Non-sensitive text to type."}}, "required": []string{"text"}}},
	"desktop.hotkey":       {"desktop.hotkey", "Send an allowlisted shortcut", "desktop.interact", 15, map[string]any{"type": "object", "properties": map[string]any{"window": windowProperty, "keys": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Allowlisted key combination."}}, "required": []string{"keys"}}},
	"desktop.focus_window": {"desktop.focus_window", "Focus a validated window", "desktop.interact", 15, map[string]any{"type": "object", "properties": map[string]any{"window": windowProperty}, "required": []string{"window"}}},
}

var windowProperty = map[string]any{"type": "string", "description": "Window handle (numeric string) or exact window title."}
var targetProperty = map[string]any{"type": "object", "description": "UI element locator, for example automation_id or name."}

// ProviderToolName returns the function name sent to model providers, which
// only accept [a-zA-Z0-9_-]. It is the inverse of CanonicalToolName.
func ProviderToolName(canonical string) string {
	return strings.ReplaceAll(canonical, ".", "_")
}

func providerToCanonical(name string) (string, bool) {
	for canonical := range ToolRegistry {
		if ProviderToolName(canonical) == name {
			return canonical, true
		}
	}
	return "", false
}

// CanonicalToolName keeps skill manifests compatible with the runtime registry
// while exposing one stable name to providers.
func CanonicalToolName(name string) string {
	switch strings.TrimSpace(name) {
	case "desktop.list_windows":
		return "desktop.windows"
	case "desktop.activate_window":
		return "desktop.focus_window"
	default:
		trimmed := strings.TrimSpace(name)
		if _, registered := ToolRegistry[trimmed]; !registered {
			if canonical, ok := providerToCanonical(trimmed); ok {
				return canonical
			}
		}
		return trimmed
	}
}

func ValidateToolRequest(name string, args map[string]any) error {
	name = CanonicalToolName(name)
	def, ok := ToolRegistry[name]
	if !ok {
		return fmt.Errorf("tool %q is not registered", name)
	}
	if args == nil {
		args = map[string]any{}
	}
	if required, ok := def.InputSchema["required"].([]string); ok {
		for _, key := range required {
			if _, exists := args[key]; !exists {
				return fmt.Errorf("tool %q requires %s", name, key)
			}
		}
	}
	if _, err := json.Marshal(args); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}

func ToolsForSkill(names []string) []ToolDefinition {
	out := make([]ToolDefinition, 0, len(names))
	for _, n := range names {
		canonical := CanonicalToolName(n)
		if d, ok := ToolRegistry[canonical]; ok {
			out = append(out, d)
		}
	}
	return out
}
