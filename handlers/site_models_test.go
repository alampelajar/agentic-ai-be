package handlers

import (
	"errors"
	"fmt"
	"testing"

	"agentic-ai-backend/services"
)

func TestKeyAndCreditRefusalsDoNotUseUpTries(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&services.ProviderHTTPError{Status: 401, Message: "Invalid API Key"}, true},
		{fmt.Errorf("wrapped: %w", &services.ProviderHTTPError{Status: 402, Message: "Insufficient credits"}), true},
		{&services.ProviderHTTPError{Status: 403, Message: "forbidden"}, true},
		{&services.ProviderHTTPError{Status: 429, Message: "rate limited"}, false},
		{&services.ProviderHTTPError{Status: 503, Message: "busy"}, false},
		{errors.New("context deadline exceeded"), false},
	}
	for _, c := range cases {
		if got := refusedAtOnce(c.err); got != c.want {
			t.Errorf("refusedAtOnce(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
