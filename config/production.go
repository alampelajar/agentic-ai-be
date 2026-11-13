package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"agentic-ai-backend/internal/httpguard"
)

// IsProduction is true when APP_ENV is "production" (or "prod"). Production turns
// the safe-by-default checks below from warnings into a refusal to start.
func IsProduction() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) {
	case "production", "prod":
		return true
	}
	return false
}

// CORSOrigins returns the browser origins allowed to call the API:
// CORS_ALLOWED_ORIGINS (comma separated, e.g. https://app.example.com). Outside
// production, an empty list falls back to the local Vite dev server so development
// needs no setup. In production an empty list allows no browser origin at all.
func CORSOrigins() []string {
	origins := httpguard.ParseOrigins(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if len(origins) == 0 && !IsProduction() {
		return httpguard.DevOrigins()
	}
	return origins
}

// TrustedProxies returns the addresses of reverse proxies whose X-Forwarded-For
// the server may believe (TRUSTED_PROXIES, comma separated IPs or CIDRs). The
// default is none: the client address is then the TCP peer, which cannot be
// forged. Behind a proxy or load balancer this must be set, or every request
// appears to come from the proxy and per-address limits apply to everyone at once.
func TrustedProxies() []string {
	var proxies []string
	for _, item := range strings.Split(os.Getenv("TRUSTED_PROXIES"), ",") {
		if item = strings.TrimSpace(item); item != "" {
			proxies = append(proxies, item)
		}
	}
	return proxies
}

// AllowPrivateProviderURLs says whether an AI provider's base URL may point at this
// computer or a private network (a local Ollama, say). It is allowed in development
// and refused in production, where such a URL would let a user make the server call
// its own internal network. ALLOW_PRIVATE_PROVIDER_URLS=true/false overrides it.
func AllowPrivateProviderURLs() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ALLOW_PRIVATE_PROVIDER_URLS"))) {
	case "true":
		return true
	case "false":
		return false
	}
	return !IsProduction()
}

// DatabaseSSLMode is DB_SSLMODE, "disable" when unset (local development).
func DatabaseSSLMode() string {
	if mode := strings.TrimSpace(os.Getenv("DB_SSLMODE")); mode != "" {
		return mode
	}
	return "disable"
}

var validSSLModes = map[string]bool{"disable": true, "allow": true, "prefer": true, "require": true, "verify-ca": true, "verify-full": true}

// AlwaysRequiredProblems lists what must be set before the server starts in any mode,
// development included. An empty JWT key lets anyone sign a token for any user or role,
// and a development server is often reachable from the rest of the local network.
func AlwaysRequiredProblems() []string {
	var problems []string
	access := strings.TrimSpace(os.Getenv("JWT_ACCESS_SECRET"))
	refresh := strings.TrimSpace(os.Getenv("JWT_REFRESH_SECRET"))
	if access == "" {
		problems = append(problems, "JWT_ACCESS_SECRET must be set (a long random value, see .env.example)")
	}
	if refresh == "" {
		problems = append(problems, "JWT_REFRESH_SECRET must be set (a long random value, see .env.example)")
	}
	if access != "" && access == refresh {
		problems = append(problems, "JWT_ACCESS_SECRET and JWT_REFRESH_SECRET must be different")
	}
	return problems
}

// ProductionProblems lists what is unsafe about the configuration for a public
// deployment. It names variables, never their values.
func ProductionProblems() []string {
	var problems []string
	secret := func(name string, minLength int) string {
		value := strings.TrimSpace(os.Getenv(name))
		if len(value) < minLength {
			problems = append(problems, fmt.Sprintf("%s must be set to a random value of at least %d characters", name, minLength))
		}
		return value
	}
	access := secret("JWT_ACCESS_SECRET", 32)
	refresh := secret("JWT_REFRESH_SECRET", 32)
	if access != "" && access == refresh {
		problems = append(problems, "JWT_ACCESS_SECRET and JWT_REFRESH_SECRET must be different")
	}
	secret("PASSWORD_RESET_PEPPER", 32)
	secret("AI_ENCRYPTION_KEY", 32)

	origins := httpguard.ParseOrigins(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if len(origins) == 0 {
		problems = append(problems, "CORS_ALLOWED_ORIGINS must list the https origin of the web app")
	}
	for _, origin := range origins {
		if parsed, err := url.Parse(origin); err == nil && parsed.Scheme != "https" && parsed.Hostname() != "localhost" {
			problems = append(problems, "CORS_ALLOWED_ORIGINS must use https: "+origin)
		}
	}
	switch DatabaseSSLMode() {
	case "require", "verify-ca", "verify-full":
	default:
		// Plaintext is acceptable only when the database is on the same private
		// network as the API (the bundled compose stack), and only if said so.
		if strings.TrimSpace(os.Getenv("ALLOW_PLAINTEXT_DB")) != "true" {
			problems = append(problems, "DB_SSLMODE must be require, verify-ca or verify-full (or ALLOW_PLAINTEXT_DB=true for a database on the same private network)")
		}
	}
	if os.Getenv("CLOUD_EXECUTION_ENABLED") == "true" && strings.TrimSpace(os.Getenv("ALLOW_PUBLIC_CODE_EXECUTION")) != "true" {
		problems = append(problems, "CLOUD_EXECUTION_ENABLED=true in production also needs ALLOW_PUBLIC_CODE_EXECUTION=true: the sandbox is plain Docker and not yet meant for strangers' code (see docs/phase7-public-readiness.md)")
	}
	return problems
}
