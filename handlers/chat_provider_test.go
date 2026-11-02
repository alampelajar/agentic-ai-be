package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"agentic-ai-backend/services"
)

func TestDecodeProviderContent(t *testing.T) {
	if got, err := decodeProviderContent(json.RawMessage(`"hello"`)); err != nil || got != "hello" {
		t.Fatalf("string content: %q %v", got, err)
	}
	if got, err := decodeProviderContent(json.RawMessage(`[{"type":"text","text":"hello"},{"type":"text","text":"world"}]`)); err != nil || got != "hello\nworld" {
		t.Fatalf("array content: %q %v", got, err)
	}
	if got, err := decodeProviderContent(json.RawMessage(`{"unexpected":true}`)); err == nil || got != "" {
		t.Fatalf("invalid content should fail: %q %v", got, err)
	}
}

func TestProviderRateLimitDiagnosticClassifiesSourceAndSanitizesHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", nil)
	req.Header.Set("X-RateLimit-Limit", "20")
	req.Header.Set("X-RateLimit-Remaining", "0")
	req.Header.Set("X-RateLimit-Reset", "2026-11-02T13:00:00Z")
	req.Header.Set("Retry-After", "7")
	response := &http.Response{StatusCode: http.StatusTooManyRequests, Header: req.Header}
	diagnostic := providerRateLimitDiagnostic(response, []byte(`{"error":{"metadata":{"error_type":"provider_rate_limit","provider_code":"XING_429","api_key":"must-not-appear"}}}`))
	if !strings.Contains(diagnostic, "upstream provider rate limit") || !strings.Contains(diagnostic, "provider_code=XING_429") || !strings.Contains(diagnostic, "remaining=0") {
		t.Fatalf("unexpected diagnostic: %s", diagnostic)
	}
	if strings.Contains(diagnostic, "must-not-appear") {
		t.Fatalf("secret leaked: %s", diagnostic)
	}
	if rateLimitRetryAllowed(response) {
		t.Fatal("must not retry when remaining is zero")
	}
}

func TestProviderErrorsPreserveMetadataWithoutSecrets(t *testing.T) {
	err := formatProviderHTTPError(400, []byte(`{"error":{"message":"invalid model","type":"invalid_request_error","code":"model_not_found","param":"model"}}`))
	if !strings.Contains(err.Error(), "invalid model") || !strings.Contains(err.Error(), "model_not_found") {
		t.Fatalf("metadata missing: %v", err)
	}
	if strings.Contains(err.Error(), "AI tidak mengembalikan choices") {
		t.Fatalf("generic choices error leaked: %v", err)
	}
	body := sanitizeProviderBody([]byte(`{"error":{"message":"bad","metadata":{"token":"secret-value"}}}`))
	if strings.Contains(body, "secret-value") {
		t.Fatalf("secret leaked: %s", body)
	}
}

func TestProviderResponseWithoutChoicesIsDiagnostic(t *testing.T) {
	var response chatResponse
	if err := json.Unmarshal([]byte(`{"id":"req-1","model":"xingchen-agi/xing4.0-29b-a4b"}`), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 0 {
		t.Fatal("expected no choices")
	}
	if !strings.Contains(sanitizeProviderBody([]byte(`{"id":"req-1"}`)), "req-1") {
		t.Fatal("request metadata missing")
	}
}

func TestNormalizeNativeToolCall(t *testing.T) {
	call := providerToolCall{Type: "function"}
	call.Function.Name = "desktop.windows"
	call.Function.Arguments = json.RawMessage(`{}`)
	got, err := normalizeNativeToolCall(call)
	if err != nil || !strings.Contains(got, `<AGENT_TOOL>`) || !strings.Contains(got, `desktop.windows`) {
		t.Fatalf("native tool normalization failed: %q %v", got, err)
	}
}

func TestNormalizeNativeToolCallRejectsUnknownAndInvalid(t *testing.T) {
	unknown := providerToolCall{Type: "function"}
	unknown.Function.Name = "desktop.unknown"
	unknown.Function.Arguments = json.RawMessage(`{}`)
	if _, err := normalizeNativeToolCall(unknown); err == nil {
		t.Fatal("unknown tool must be rejected")
	}
	invalid := providerToolCall{Type: "function"}
	invalid.Function.Name = "desktop.type"
	invalid.Function.Arguments = json.RawMessage(`{"unexpected":true}`)
	if _, err := normalizeNativeToolCall(invalid); err == nil {
		t.Fatal("invalid schema must be rejected")
	}
}

func TestResponseMetadataDetectsReasoningAndRefusalWithoutExposingContent(t *testing.T) {
	var response chatResponse
	if err := json.Unmarshal([]byte(`{"model":"cohere/north-mini-code","usage":{"completion_tokens":12},"choices":[{"finish_reason":"stop","message":{"content":null,"reasoning_details":[{"text":"private"}],"refusal":"blocked"}}]}`), &response); err != nil {
		t.Fatal(err)
	}
	choice := response.Choices[0]
	if choice.FinishReason != "stop" || response.Usage.CompletionTokens != 12 || !hasRawValue(choice.Message.ReasoningDetails) || !hasRawValue(choice.Message.Refusal) {
		t.Fatalf("metadata not retained: %#v", response)
	}
}

func TestProviderToolsForNamesCanonicalizesDesktopAliases(t *testing.T) {
	tools := providerToolsForNames([]string{"list-files", "desktop.list_windows", "desktop.screenshot", "desktop.activate_window", "desktop.click"})
	seen := map[string]bool{}
	for _, tool := range tools {
		seen[tool.Function.Name] = true
		if tool.Type != "function" || tool.Function.Parameters == nil {
			t.Fatalf("tool schema incomplete: %#v", tool)
		}
	}
	for _, name := range []string{"list-files", "desktop_windows", "desktop_screenshot", "desktop_focus_window", "desktop_click"} {
		if !seen[name] {
			t.Fatalf("expected provider-safe tool %q, got %#v", name, seen)
		}
	}
}

func TestProviderToolsUseSafeNamesAndObjectSchemas(t *testing.T) {
	safe := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	var names []string
	for name := range services.ToolRegistry {
		names = append(names, name)
	}
	for _, tool := range providerToolsForNames(names) {
		if !safe.MatchString(tool.Function.Name) {
			t.Fatalf("provider tool name %q is not provider-safe", tool.Function.Name)
		}
		params := tool.Function.Parameters
		props, hasProps := params["properties"].(map[string]any)
		if !hasProps {
			t.Fatalf("tool %q schema lacks properties: %#v", tool.Function.Name, params)
		}
		if required, ok := params["required"].([]string); ok {
			for _, key := range required {
				if _, declared := props[key]; !declared {
					t.Fatalf("tool %q requires undeclared property %q", tool.Function.Name, key)
				}
			}
		}
	}
}

func TestNormalizeNativeToolCallMapsProviderNameToCanonical(t *testing.T) {
	call := providerToolCall{Type: "function"}
	call.Function.Name = "desktop_status"
	call.Function.Arguments = json.RawMessage(`{}`)
	got, err := normalizeNativeToolCall(call)
	if err != nil || !strings.Contains(got, `"action":"desktop.status"`) {
		t.Fatalf("provider name not mapped to canonical: %q %v", got, err)
	}
}

func TestProviderToolsForNamesRejectsUnknown(t *testing.T) {
	if tools := providerToolsForNames([]string{"desktop.unknown"}); len(tools) != 0 {
		t.Fatalf("unknown tool must not be exposed: %#v", tools)
	}
}
