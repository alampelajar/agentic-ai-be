package handlers

import (
	"log"
	"net/http"
	"os"
	"strings"

	"agentic-ai-backend/models"
	"agentic-ai-backend/utils"
)

// providerKeyError is why an API key could not be produced, with the status and
// message Chat answers with.
type providerKeyError struct {
	Status  int
	Message string
}

// resolveProviderAPIKey returns the API key to call a model's provider with.
// System providers use the key the admin stored encrypted (or the legacy
// environment variable); a user's provider is used only by its owner.
func resolveProviderAPIKey(provider models.AIProvider, userID uint) (string, *providerKeyError) {
	var apiKey string

	if provider.IsSystem {
		if strings.TrimSpace(provider.APIKeyEncrypted) != "" {
			decryptedKey, err := utils.DecryptString(provider.APIKeyEncrypted)
			if err != nil {
				log.Printf("[CHAT] ERROR decrypt system provider API key: %v", err)
				return "", &providerKeyError{http.StatusInternalServerError, "Gagal membaca API key provider sistem."}
			}
			apiKey = strings.TrimSpace(decryptedKey)
		} else {
			switch strings.ToLower(strings.TrimSpace(provider.Type)) {
			case "nararouter":
				apiKey = strings.TrimSpace(os.Getenv("NARAROUTER_API_KEY"))
			case "openrouter":
				apiKey = strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
			case "openai":
				apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
			case "google":
				apiKey = strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
				if apiKey == "" {
					apiKey = strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
				}
			case "deepseek":
				apiKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
			}
		}
	} else {
		// User-owned provider: never use another user's provider.
		if provider.UserID == nil || *provider.UserID != userID {
			return "", &providerKeyError{http.StatusForbidden, "Provider bukan milik user."}
		}
		if strings.TrimSpace(provider.APIKeyEncrypted) == "" {
			return "", &providerKeyError{http.StatusInternalServerError, "API key provider belum dikonfigurasi."}
		}
		decryptedKey, err := utils.DecryptString(provider.APIKeyEncrypted)
		if err != nil {
			log.Printf("[CHAT] ERROR decrypt user provider API key: %v", err)
			return "", &providerKeyError{http.StatusInternalServerError, "Gagal membaca API key provider."}
		}
		apiKey = strings.TrimSpace(decryptedKey)
	}

	if apiKey == "" {
		return "", &providerKeyError{http.StatusInternalServerError, "API key provider belum dikonfigurasi."}
	}
	return apiKey, nil
}
