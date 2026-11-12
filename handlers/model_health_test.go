package handlers

import (
	"net/http"
	"testing"
	"time"
)

func TestProviderQuotaHeadersAreKeptPerProviderAndKey(t *testing.T) {
	groq := http.Header{}
	groq.Set("x-ratelimit-limit-requests", "1000")
	groq.Set("x-ratelimit-remaining-requests", "987")
	groq.Set("x-ratelimit-reset-requests", "1m26s")
	recordProviderQuota("https://api.groq.com/openai/v1/", "key-a", groq)

	quota, seen := seenProviderQuota("https://api.groq.com/openai/v1", "key-a")
	if !seen || quota.Limit != "1000" || quota.Remaining != "987" || quota.Reset != "1m26s" || quota.SeenAt.IsZero() {
		t.Fatalf("quota = %+v seen = %v", quota, seen)
	}
	if _, seen := seenProviderQuota("https://api.groq.com/openai/v1", "key-b"); seen {
		t.Fatal("another key must not see this key's quota")
	}

	openRouter := http.Header{}
	openRouter.Set("X-RateLimit-Limit", "50")
	openRouter.Set("X-RateLimit-Remaining", "0")
	recordProviderQuota("https://openrouter.ai/api/v1", "key-c", openRouter)
	if quota, _ := seenProviderQuota("https://openrouter.ai/api/v1", "key-c"); quota.Remaining != "0" || quota.Limit != "50" {
		t.Fatalf("OpenRouter quota = %+v", quota)
	}

	recordProviderQuota("https://example.test", "key-d", http.Header{})
	if _, seen := seenProviderQuota("https://example.test", "key-d"); seen {
		t.Fatal("an answer without quota headers must not record anything")
	}
	if quotaKey("https://x.test", "secret-key") == "" || len(quotaKey("https://x.test", "secret-key")) != 24 {
		t.Fatal("the key must be stored as a short hash, never as itself")
	}
}

func TestHealthStatusInOneWord(t *testing.T) {
	resting := time.Now().Add(time.Minute)
	cases := map[string]modelHealthRow{
		"resting":  {Calls: 10, Succeeded: 10, RestingUntil: &resting},
		"unused":   {},
		"unstable": {Calls: 10, Succeeded: 7, Failed: 3},
		"healthy":  {Calls: 10, Succeeded: 8, Failed: 2},
	}
	for want, row := range cases {
		if got := healthStatus(row); got != want {
			t.Errorf("%+v: got %s, want %s", row, got, want)
		}
	}
}

func TestOpenRouterIsRecognised(t *testing.T) {
	if !isOpenRouter("https://openrouter.ai/api/v1") || isOpenRouter("https://api.groq.com/openai/v1") {
		t.Fatal("only OpenRouter has GET /key")
	}
}
