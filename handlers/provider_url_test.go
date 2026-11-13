package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentic-ai-backend/internal/safehttp"
)

func TestProviderBaseURLRulesDifferByEnvironment(t *testing.T) {
	always := []string{"not a url", "ftp://api.example.com", "https://", "https://user:pass@api.example.com/v1", "javascript:alert(1)", ""}
	public := []string{"https://api.openai.com/v1", "https://openrouter.ai/api/v1", "https://api.example.com:8443/v1"}
	internal := []string{"http://localhost:11434/v1", "http://127.0.0.1:1234/v1", "https://localhost/v1", "https://169.254.169.254/latest", "https://10.0.0.5/v1", "https://[::1]/v1", "http://api.example.com/v1"}

	t.Setenv("APP_ENV", "production")
	t.Setenv("ALLOW_PRIVATE_PROVIDER_URLS", "")
	for _, value := range append(append([]string{}, always...), internal...) {
		if validBaseURL(value) {
			t.Errorf("production accepted %q", value)
		}
	}
	for _, value := range public {
		if !validBaseURL(value) {
			t.Errorf("production refused %q", value)
		}
	}

	t.Setenv("APP_ENV", "")
	for _, value := range always {
		if validBaseURL(value) {
			t.Errorf("development accepted %q", value)
		}
	}
	for _, value := range append(append([]string{}, public...), internal...) {
		if !validBaseURL(value) {
			t.Errorf("development refused %q", value)
		}
	}

	// The override works both ways.
	t.Setenv("APP_ENV", "production")
	t.Setenv("ALLOW_PRIVATE_PROVIDER_URLS", "true")
	if !validBaseURL("http://localhost:11434/v1") {
		t.Error("the explicit opt-in was ignored")
	}
	t.Setenv("APP_ENV", "")
	t.Setenv("ALLOW_PRIVATE_PROVIDER_URLS", "false")
	if validBaseURL("http://localhost:11434/v1") {
		t.Error("the explicit refusal was ignored")
	}
}

func TestProductionProviderClientCannotCallThisComputerEvenWithAnAcceptedURL(t *testing.T) {
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("internal secret")) }))
	defer internal.Close()

	t.Setenv("APP_ENV", "production")
	t.Setenv("ALLOW_PRIVATE_PROVIDER_URLS", "")
	if _, err := providerHTTPClient(5 * time.Second).Get(internal.URL); !errors.Is(err, safehttp.ErrBlocked) {
		t.Fatalf("production reached an internal server: %v", err)
	}
	t.Setenv("APP_ENV", "")
	response, err := providerHTTPClient(5 * time.Second).Get(internal.URL)
	if err != nil {
		t.Fatalf("development could not reach a local model server: %v", err)
	}
	response.Body.Close()
}

func TestARefusedBaseURLSaysWhy(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_PROVIDER_URLS", "false")
	cases := map[string]string{
		"not a url":                       "tidak valid",
		"https://user:pw@api.example.com": "tidak valid",
		"ftp://api.example.com":           "http atau https",
		"http://api.example.com/v1":       "harus menggunakan https",
		"https://127.0.0.1:5432":          "jaringan lokal",
		"https://192.168.1.1":             "jaringan lokal",
		"https://localhost/v1":            "jaringan lokal",
		"https://[::1]/v1":                "jaringan lokal",
	}
	for value, want := range cases {
		if got := baseURLProblem(value); !strings.Contains(got, want) {
			t.Errorf("%s: problem = %q, want it to mention %q", value, got, want)
		}
	}
	if got := baseURLProblem("https://api.example.com/v1"); got != "" {
		t.Errorf("a public https URL was refused: %q", got)
	}
}
