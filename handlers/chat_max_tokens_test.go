package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentic-ai-backend/services"
)

// A model that allows fewer answer tokens than chatMaxTokens refuses the request; the call is
// repeated once without a limit, and the answer comes back.
func TestARefusedTokenLimitIsDroppedAndTheCallRepeated(t *testing.T) {
	var limits []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request chatRequest
		_ = json.Unmarshal(body, &request)
		limits = append(limits, request.MaxTokens)
		if request.MaxTokens > 8192 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"max_tokens must be less than or equal to 8192","type":"invalid_request_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(textReply("selesai")))
	}))
	t.Cleanup(server.Close)

	request := baseRequest()
	request.MaxTokens = chatMaxTokens
	answer, err := callAIProviderWithRepair("key", server.URL, request)
	if err != nil || answer != "selesai" {
		t.Fatalf("answer = %q, err = %v", answer, err)
	}
	if len(limits) != 2 || limits[0] != chatMaxTokens || limits[1] != 0 {
		t.Fatalf("the call must be repeated once without a limit, sent %v", limits)
	}
}

func TestOnlyATokenLimitRefusalDropsTheLimit(t *testing.T) {
	refused := []error{
		&services.ProviderHTTPError{Status: 400, Message: "provider HTTP 400: max_tokens must be less than or equal to 8192"},
		&services.ProviderHTTPError{Status: 400, Message: "provider HTTP 400: max_completion_tokens is too large: 32768"},
	}
	for _, err := range refused {
		if !maxTokensRefused(err) {
			t.Errorf("%v must count as a refused limit", err)
		}
	}
	others := []error{
		nil,
		errors.New("network down"),
		&services.ProviderHTTPError{Status: 400, Message: "provider HTTP 400: messages[2].content must be a string"},
		&services.ProviderHTTPError{Status: 429, Message: "provider HTTP 429: max_tokens per minute exceeded"},
	}
	for _, err := range others {
		if maxTokensRefused(err) {
			t.Errorf("%v must not drop the limit", err)
		}
	}
	if chatMaxTokens < 32768 {
		t.Errorf("reasoning models need room to think and answer, got %d", chatMaxTokens)
	}
}
