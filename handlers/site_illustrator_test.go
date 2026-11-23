package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentic-ai-backend/services"
)

func TestImagesEndpoint(t *testing.T) {
	for base, want := range map[string]string{
		"https://openrouter.ai/api/v1":                   "https://openrouter.ai/api/v1/images",
		"https://openrouter.ai/api/v1/":                  "https://openrouter.ai/api/v1/images",
		"https://openrouter.ai/api/v1/chat/completions": "https://openrouter.ai/api/v1/images",
	} {
		if got := imagesEndpoint(base); got != want {
			t.Fatalf("imagesEndpoint(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestRequestImageReadsThePicture(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_PROVIDER_URLS", "true")
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	var asked map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/images" || r.Header.Get("Authorization") != "Bearer key" {
			http.Error(w, "wrong request", http.StatusBadRequest)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&asked)
		// The answer claims JPEG; the bytes decide.
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(png), "media_type": "image/jpeg"}}})
	}))
	defer server.Close()

	data, mediaType, err := requestImage(context.Background(), "key", server.URL+"/api/v1", "recraft/x:free", "a cup", "4:3")
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "image/png" || len(data) != len(png) {
		t.Fatalf("got %s, %d bytes", mediaType, len(data))
	}
	if asked["model"] != "recraft/x:free" || asked["aspect_ratio"] != "4:3" || asked["prompt"] != "a cup" {
		t.Fatalf("asked %v", asked)
	}
}

func TestRequestImageReportsARateLimit(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_PROVIDER_URLS", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
	}))
	defer server.Close()
	_, _, err := requestImage(context.Background(), "key", server.URL, "m", "p", "1:1")
	var httpErr *services.ProviderHTTPError
	if err == nil || !errors.As(err, &httpErr) || httpErr.Status != http.StatusTooManyRequests {
		t.Fatalf("err = %v", err)
	}
	if wait := rateLimitWait(err); wait.Seconds() != 4 {
		t.Fatalf("wait = %s, want 4s", wait)
	}
}

func TestImageModelsFromTheEnvironment(t *testing.T) {
	t.Setenv("BUILDER_IMAGE_MODELS", "")
	if got := imageModels(); len(got) != 1 || got[0] != defaultImageModels {
		t.Fatalf("default = %v", got)
	}
	t.Setenv("BUILDER_IMAGE_MODELS", " a:free , b ,")
	if got := imageModels(); len(got) != 2 || got[0] != "a:free" || got[1] != "b" {
		t.Fatalf("list = %v", got)
	}
	t.Setenv("BUILDER_IMAGE_MODELS", "off")
	if got := imageModels(); got != nil {
		t.Fatalf("off = %v", got)
	}
}
