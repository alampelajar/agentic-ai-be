package handlers

import (
	"errors"
	"strings"
	"testing"

	"agentic-ai-backend/services"
)

func TestImageUnsupportedMessage(t *testing.T) {
	openRouter := &services.ProviderHTTPError{Status: 404, Message: "provider HTTP 404: No endpoints found that support image input; code=404"}
	groq := &services.ProviderHTTPError{Status: 400, Message: "provider HTTP 400: messages[9].content must be a string; type=invalid_request_error"}
	for _, err := range []error{openRouter, groq} {
		if msg := imageUnsupportedMessage(err, true, "cohere/north-mini-code:free"); !strings.Contains(msg, "tidak bisa membaca gambar") || !strings.Contains(msg, "cohere/north-mini-code:free") {
			t.Errorf("%v: got %q", err, msg)
		}
	}
	if imageUnsupportedMessage(openRouter, false, "m") != "" {
		t.Error("without images the error is about something else")
	}
	if imageUnsupportedMessage(&services.ProviderHTTPError{Status: 503, Message: "image input overloaded"}, true, "m") != "" {
		t.Error("a busy provider is not a model that cannot read images")
	}
	if imageUnsupportedMessage(&services.ProviderHTTPError{Status: 400, Message: "max_tokens too large"}, true, "m") != "" {
		t.Error("other bad requests keep their own message")
	}
	if imageUnsupportedMessage(errors.New("network down"), true, "m") != "" {
		t.Error("only provider replies are read")
	}
}
