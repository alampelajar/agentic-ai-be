package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// What providers say about how much is left, kept in memory per provider and key. Groq and
// OpenAI-style APIs send x-ratelimit-* headers on every answer; OpenRouter sends X-RateLimit-*
// when it limits, and tells more on GET /key.

type providerQuota struct {
	Limit     string    `json:"limit,omitempty"`
	Remaining string    `json:"remaining,omitempty"`
	Reset     string    `json:"reset,omitempty"`
	SeenAt    time.Time `json:"seen_at"`
}

var (
	quotaMu   sync.Mutex
	quotaSeen = map[string]providerQuota{}
)

// quotaKey names a provider and the key used with it, without keeping the key itself.
func quotaKey(baseURL, apiKey string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimRight(strings.TrimSpace(baseURL), "/")) + "|" + apiKey))
	return hex.EncodeToString(sum[:12])
}

// recordProviderQuota keeps the request quota a provider reported in its answer headers.
func recordProviderQuota(baseURL, apiKey string, header http.Header) {
	pick := func(names ...string) string {
		for _, name := range names {
			if value := strings.TrimSpace(header.Get(name)); value != "" {
				return value
			}
		}
		return ""
	}
	quota := providerQuota{
		Limit:     pick("x-ratelimit-limit-requests", "X-RateLimit-Limit"),
		Remaining: pick("x-ratelimit-remaining-requests", "X-RateLimit-Remaining"),
		Reset:     pick("x-ratelimit-reset-requests", "X-RateLimit-Reset"),
	}
	if quota.Limit == "" && quota.Remaining == "" {
		return
	}
	quota.SeenAt = time.Now()
	quotaMu.Lock()
	quotaSeen[quotaKey(baseURL, apiKey)] = quota
	quotaMu.Unlock()
}

func seenProviderQuota(baseURL, apiKey string) (providerQuota, bool) {
	quotaMu.Lock()
	defer quotaMu.Unlock()
	quota, ok := quotaSeen[quotaKey(baseURL, apiKey)]
	return quota, ok
}

// OpenRouter tells a key's credit and limits on GET /key; the answer is kept for a minute.
type openRouterKeyInfo struct {
	fetched time.Time
	data    map[string]any
}

var (
	openRouterMu    sync.Mutex
	openRouterCache = map[string]openRouterKeyInfo{}
)

// openRouterKeyFields are the parts of GET /key shown to the person; the key's label is not.
var openRouterKeyFields = []string{"limit", "limit_remaining", "limit_reset", "usage", "usage_daily", "is_free_tier", "rate_limit"}

func isOpenRouter(baseURL string) bool {
	return strings.Contains(strings.ToLower(baseURL), "openrouter.ai")
}

func openRouterKey(baseURL, apiKey string) map[string]any {
	cacheKey := quotaKey(baseURL, apiKey)
	openRouterMu.Lock()
	cached, ok := openRouterCache[cacheKey]
	openRouterMu.Unlock()
	if ok && time.Since(cached.fetched) < time.Minute {
		return cached.data
	}
	endpoint := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(baseURL), "/"), "/chat/completions") + "/key"
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	response, err := providerHTTPClient(8 * time.Second).Do(request)
	if err != nil {
		log.Printf("[HEALTH] OpenRouter /key tidak dapat dibaca: %v", err)
		return nil
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &envelope) != nil || envelope.Data == nil {
		return nil
	}
	data := map[string]any{}
	for _, field := range openRouterKeyFields {
		if value, present := envelope.Data[field]; present {
			data[field] = value
		}
	}
	openRouterMu.Lock()
	openRouterCache[cacheKey] = openRouterKeyInfo{fetched: time.Now(), data: data}
	openRouterMu.Unlock()
	return data
}

// logModelCall writes one call of a single model (combos log their own attempts) and keeps the
// model's health up to date, so automatic choice and the health page see every call.
func logModelCall(userID uint, model models.AIModel, err error, latency time.Duration) {
	entry := models.AIModelCallLog{UserID: userID, AIModelID: model.ID, IsSystem: model.IsSystem, Attempt: 1, Status: "ok", LatencyMS: latency.Milliseconds()}
	if err != nil {
		entry.Status = "failed"
		class := services.ClassifyProviderError(err)
		entry.ErrorClass = string(class)
		var providerErr *services.ProviderHTTPError
		if errors.As(err, &providerErr) {
			entry.HTTPStatus = providerErr.Status
		}
		if class == services.ErrorRetryable {
			comboHealth.Failure(model.ID)
		}
	} else {
		comboHealth.Success(model.ID)
	}
	if config.DB != nil {
		if dbErr := config.DB.Create(&entry).Error; dbErr != nil {
			log.Printf("[CHAT] gagal mencatat panggilan model: %v", dbErr)
		}
	}
}
