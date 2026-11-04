package handlers

import (
	"net/http"
	"time"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/safehttp"
)

// providerHTTPClient is the client for every request to an AI provider's base URL,
// which a user typed in. It refuses internal addresses unless config allows them
// (development): see safehttp.
func providerHTTPClient(timeout time.Duration) *http.Client {
	return safehttp.NewClient(timeout, config.AllowPrivateProviderURLs())
}
