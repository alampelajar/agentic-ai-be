package handlers

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"agentic-ai-backend/services"
)

// The frontend runs any <AGENT_TOOL>{...}</AGENT_TOOL> marker it finds in a
// reply, whether the model produced it as plain text or the backend built it
// from a native tool call. Only the backend knows which tools the agent's
// active skills grant, so it removes every marker that is not granted. The
// pattern matches the frontend's so both split the message the same way.
var agentToolMarker = regexp.MustCompile(`<AGENT_TOOL>([\s\S]*?)</AGENT_TOOL>`)

// The frontend strips a Markdown code fence around the JSON before parsing it.
var (
	leadingCodeFence  = regexp.MustCompile("(?i)^`{3}(?:json)?\\s*")
	trailingCodeFence = regexp.MustCompile("\\s*`{3}$")
)

func enforceAgentToolMarkers(message string, allowedTools []string) string {
	if !strings.Contains(message, "<AGENT_TOOL>") {
		return message
	}
	allowed := make(map[string]bool, len(allowedTools))
	for _, name := range allowedTools {
		allowed[services.CanonicalToolName(name)] = true
	}
	return agentToolMarker.ReplaceAllStringFunc(message, func(marker string) string {
		payload := agentToolMarker.FindStringSubmatch(marker)[1]
		action, ok := markerAction(payload)
		// Require the exact canonical name. The frontend lowercases and rewrites
		// separators, so anything that is not already canonical either maps to
		// the same tool or is rejected there; it can never become a different one.
		if ok && allowed[action] {
			return marker
		}
		return fmt.Sprintf("[Tool %q tidak diizinkan untuk agent ini dan tidak dijalankan.]", safeToolLabel(action, ok))
	})
}

// markerAction returns the tool name the frontend would select from a marker
// payload, following the same precedence as its parser.
func markerAction(payload string) (string, bool) {
	cleaned := strings.TrimSpace(payload)
	cleaned = leadingCodeFence.ReplaceAllString(cleaned, "")
	cleaned = trailingCodeFence.ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)

	var decoded any
	if json.Unmarshal([]byte(cleaned), &decoded) != nil {
		return "", false
	}
	if encoded, isString := decoded.(string); isString {
		if json.Unmarshal([]byte(encoded), &decoded) != nil {
			return "", false
		}
	}
	parsed, isObject := decoded.(map[string]any)
	if !isObject {
		return "", false
	}

	function := map[string]any{}
	switch value := parsed["function"].(type) {
	case string:
		function = map[string]any{"name": value}
	case map[string]any:
		function = value
	}

	arguments := firstPresent(parsed["arguments"], parsed["input"], function["arguments"])
	if encoded, isString := arguments.(string); isString {
		var inner any
		if json.Unmarshal([]byte(encoded), &inner) == nil {
			arguments = inner
		} else {
			arguments = nil
		}
	}
	argumentObject, _ := arguments.(map[string]any)

	selected := firstPresent(
		parsed["action"], parsed["tool"], parsed["name"], function["name"],
		argumentObject["action"], argumentObject["tool"], argumentObject["name"],
	)
	name, isString := selected.(string)
	if !isString {
		return "", false
	}
	return strings.TrimSpace(name), true
}

// firstPresent mirrors JavaScript's `a ?? b ?? c`: the first value that is not null.
func firstPresent(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

var unsafeLabelChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func safeToolLabel(name string, ok bool) string {
	if !ok || name == "" {
		return "tidak dikenali"
	}
	label := unsafeLabelChars.ReplaceAllString(name, "")
	if len(label) > 60 {
		label = label[:60]
	}
	if label == "" {
		return "tidak dikenali"
	}
	return label
}
