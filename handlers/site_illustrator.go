package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/sitebuilder"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// BuilderIllustrator draws the Website Builder's product illustrations with an image
// model on the system OpenRouter provider (POST <base>/images). The models are tried in
// order; BUILDER_IMAGE_MODELS lists them, separated by commas.
type BuilderIllustrator struct{}

// defaultImageModels is free; add the paid "recraft/recraft-v4.1-flash-v2" (about $0.007
// a picture) after it in BUILDER_IMAGE_MODELS to keep drawing when the free one is busy.
const defaultImageModels = "recraft/recraft-v4.1-flash-v2:free"

// maxImageAnswerBytes bounds the JSON answer: one base64 picture of up to 3 MB.
const maxImageAnswerBytes = 5 << 20

func imageModels() []string {
	raw := strings.TrimSpace(os.Getenv("BUILDER_IMAGE_MODELS"))
	if raw == "" {
		raw = defaultImageModels
	}
	if raw == "off" {
		return nil
	}
	var list []string
	for _, model := range strings.Split(raw, ",") {
		if model = strings.TrimSpace(model); model != "" {
			list = append(list, model)
		}
	}
	return list
}

// imageProviders are the active system providers that may serve image models: those of
// type openrouter first, then others whose address is OpenRouter's (an admin may have
// pointed another provider at it with a key that OpenRouter refuses).
func imageProviders() []models.AIProvider {
	var all []models.AIProvider
	config.DB.Where("is_system = ? AND is_active = ?", true, true).Order("id ASC").Find(&all)
	var typed, other []models.AIProvider
	for _, provider := range all {
		switch {
		case provider.Type == "openrouter":
			typed = append(typed, provider)
		case strings.Contains(strings.ToLower(provider.BaseURL), "openrouter.ai"):
			other = append(other, provider)
		}
	}
	return append(typed, other...)
}

// imagesEndpoint turns a provider's base address into its image address.
func imagesEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	base = strings.TrimSuffix(base, "/chat/completions")
	return base + "/images"
}

func (BuilderIllustrator) Illustrate(ctx context.Context, userID uint, prompt, aspect string) ([]byte, string, string, error) {
	list := imageModels()
	if len(list) == 0 {
		return nil, "", "", sitebuilder.ErrNoIllustrator
	}
	if builderPlanFor(userID).OutOfTokens() {
		return nil, "", "", sitebuilder.ErrTokenLimit
	}
	providers := imageProviders()
	if len(providers) == 0 {
		return nil, "", "", sitebuilder.ErrNoIllustrator
	}
	var failures []string
	for _, provider := range providers {
		key, keyErr := resolveProviderAPIKey(provider, userID)
		if keyErr != nil {
			failures = append(failures, provider.Name+": "+keyErr.Message)
			continue
		}
		for _, model := range list {
			data, mediaType, callErr := requestImage(ctx, key, provider.BaseURL, model, prompt, aspect)
			if callErr != nil && rateLimitWait(callErr) > 0 && ctx.Err() == nil {
				// A busy free model is asked once more after its wait, then the next is tried.
				select {
				case <-ctx.Done():
					return nil, "", "", ctx.Err()
				case <-time.After(rateLimitWait(callErr)):
				}
				data, mediaType, callErr = requestImage(ctx, key, provider.BaseURL, model, prompt, aspect)
			}
			if callErr == nil {
				return data, mediaType, model, nil
			}
			failures = append(failures, provider.Name+" "+model+": "+services.RedactSecrets(callErr.Error()))
			if ctx.Err() != nil {
				return nil, "", "", ctx.Err()
			}
			if refusedAtOnce(callErr) {
				break // this provider's key or credit is refused: the next provider may work
			}
		}
	}
	if len(failures) == 0 {
		return nil, "", "", sitebuilder.ErrNoIllustrator
	}
	return nil, "", "", errors.New("every image model failed: " + strings.Join(failures, "; "))
}

type imageAnswer struct {
	Data []struct {
		B64JSON   string `json:"b64_json"`
		MediaType string `json:"media_type"`
	} `json:"data"`
}

func requestImage(ctx context.Context, key, baseURL, model, prompt, aspect string) ([]byte, string, error) {
	body, _ := json.Marshal(map[string]any{"model": model, "prompt": prompt, "n": 1, "aspect_ratio": aspect})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, imagesEndpoint(baseURL), bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := providerHTTPClient(3 * time.Minute).Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxImageAnswerBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(raw) > maxImageAnswerBytes {
		return nil, "", errors.New("the picture is too large")
	}
	if response.StatusCode != http.StatusOK {
		message := strings.TrimSpace(string(raw))
		if retry := response.Header.Get("Retry-After"); retry != "" {
			message += " retry_after=" + retry
		}
		return nil, "", &services.ProviderHTTPError{Status: response.StatusCode, Message: clipText(message, 500)}
	}
	var answer imageAnswer
	if err := json.Unmarshal(raw, &answer); err != nil || len(answer.Data) == 0 || answer.Data[0].B64JSON == "" {
		return nil, "", errors.New("the answer holds no picture")
	}
	encoded := answer.Data[0].B64JSON
	if comma := strings.Index(encoded, ","); strings.HasPrefix(encoded, "data:") && comma > 0 {
		encoded = encoded[comma+1:]
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", errors.New("the picture could not be read")
	}
	// The bytes decide the type, not what the answer claims.
	return data, http.DetectContentType(data), nil
}

func clipText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}
