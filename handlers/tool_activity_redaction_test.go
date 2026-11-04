package handlers

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactActivityHidesSecretKeysAndFreeText(t *testing.T) {
	input := map[string]any{
		"window":   "131406",
		"text":     "FAKE-TYPED-TEXT",
		"content":  "FAKE-CONTENT",
		"password": "FAKE-PASSWORD",
		"nested": map[string]any{
			"Authorization": "FAKE-AUTH",
			"cookie":        "FAKE-COOKIE",
			"list":          []any{map[string]any{"clipboard_text": "FAKE-CLIP", "keep": "visible"}},
		},
	}
	raw, _ := json.Marshal(redactActivity(input))
	out := string(raw)
	for _, leaked := range []string{"FAKE-TYPED-TEXT", "FAKE-CONTENT", "FAKE-PASSWORD", "FAKE-AUTH", "FAKE-COOKIE", "FAKE-CLIP"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("%q leaked into %s", leaked, out)
		}
	}
	for _, kept := range []string{"131406", "visible"} {
		if !strings.Contains(out, kept) {
			t.Fatalf("non-secret value %q was removed from %s", kept, out)
		}
	}
}

func TestRedactActivityStringCatchesSecretsInFreeText(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJ1c2VyX2lkIjoyM30.c2lnbmF0dXJl"
	cases := map[string]string{
		"Authorization: Bearer abcdef1234567890":    "abcdef1234567890",
		"request failed token=FAKE-TOKEN-VALUE now": "FAKE-TOKEN-VALUE",
		"password: \"hunter two\" rejected":         "hunter two",
		"api_key=FAKE-KEY-123":                      "FAKE-KEY-123",
		"jwt " + jwt:                                jwt,
		"key sk-abcdefghijklmnopqrstuvwx leaked":    "sk-abcdefghijklmnopqrstuvwx",
		"gh ghp_abcdefghijklmnopqrstuvwxyz0123":     "ghp_abcdefghijklmnopqrstuvwxyz0123",
		"aws AKIAABCDEFGHIJKLMNOP end":              "AKIAABCDEFGHIJKLMNOP",
		"Inbox - OTP: 482913 - Mail":                "482913",
		"window 'Login' password=FAKE-PW; title ok": "FAKE-PW",
	}
	for input, secret := range cases {
		got := redactActivityString(input)
		if strings.Contains(got, secret) {
			t.Errorf("secret %q survived in %q -> %q", secret, input, got)
		}
		if !strings.Contains(got, redactedValue) {
			t.Errorf("no redaction marker for %q -> %q", input, got)
		}
	}
}

func TestRedactActivityStringKeepsOrdinaryText(t *testing.T) {
	for _, text := range []string{
		"Untitled - Notepad",
		"window handle is invalid",
		"token expired, please sign in again",
		"desktop runtime stopped",
	} {
		if got := redactActivityString(text); got != text {
			t.Errorf("ordinary text changed: %q -> %q", text, got)
		}
	}
}

func TestRedactActivityRedactsStringLeavesUnderInnocentKeys(t *testing.T) {
	raw, _ := json.Marshal(redactActivity(map[string]any{"note": "login with password=FAKE-PW please"}))
	if strings.Contains(string(raw), "FAKE-PW") {
		t.Fatalf("secret under an innocent key leaked: %s", raw)
	}
}

func TestTruncateRunesBoundsLengthWithoutSplittingCharacters(t *testing.T) {
	if got := truncateRunes("abc", 5); got != "abc" {
		t.Fatalf("short string changed: %q", got)
	}
	got := truncateRunes(strings.Repeat("é", 10), 4)
	if got != "éééé…" {
		t.Fatalf("truncation = %q", got)
	}
}
