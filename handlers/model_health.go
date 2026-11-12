package handlers

import (
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// healthWindow is how far back the health page counts calls.
const healthWindow = 24 * time.Hour

type modelHealthRow struct {
	ID           uint       `json:"id"`
	Name         string     `json:"name"`
	ModelID      string     `json:"model_id"`
	Provider     string     `json:"provider"`
	ProviderID   uint       `json:"provider_id"`
	IsSystem     bool       `json:"is_system"`
	Calls        int        `json:"calls"`
	Succeeded    int        `json:"succeeded"`
	Failed       int        `json:"failed"`
	Busy         int        `json:"busy"`    // 5xx: the provider or model was overloaded or down
	Limited      int        `json:"limited"` // 429: a rate limit or a used-up quota
	AvgLatencyMS int64      `json:"avg_latency_ms"`
	LastFailure  *time.Time `json:"last_failure,omitempty"`
	LastStatus   int        `json:"last_failure_status,omitempty"`
	RestingUntil *time.Time `json:"resting_until,omitempty"`
	Status       string     `json:"status"` // healthy, unstable, resting or unused
}

type providerHealthRow struct {
	ID         uint           `json:"id"`
	Name       string         `json:"name"`
	Quota      *providerQuota `json:"quota,omitempty"`      // the last rate-limit headers it sent
	OpenRouter map[string]any `json:"openrouter,omitempty"` // credit and limits from GET /key
}

// healthStatus turns a model's counts into one word for the page.
func healthStatus(row modelHealthRow) string {
	switch {
	case row.RestingUntil != nil:
		return "resting"
	case row.Calls == 0:
		return "unused"
	case row.Failed*10 >= row.Calls*3: // a third or more failed
		return "unstable"
	default:
		return "healthy"
	}
}

// GetModelHealth lists the chat models the person can use with how they fared in the last day,
// and what each provider says about the quota left. System models count every user's calls
// (counts only, nothing about who); a person's own models count only theirs.
func GetModelHealth(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	var found []models.AIModel
	config.DB.Preload("Provider").
		Where("is_active = ? AND (is_system = ? OR user_id = ?)", true, true, userID).
		Order("is_system DESC").Order("id ASC").Find(&found)

	since := time.Now().Add(-healthWindow)
	var stats []struct {
		AIModelID uint
		Total     int
		Ok        int
		Failed    int
		Busy      int
		Limited   int
		Latency   float64
		LastFail  *time.Time
	}
	config.DB.Model(&models.AIModelCallLog{}).
		Select(`ai_model_id, COUNT(*) AS total,
			SUM(CASE WHEN status = 'ok' THEN 1 ELSE 0 END) AS ok,
			SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed,
			SUM(CASE WHEN http_status >= 500 THEN 1 ELSE 0 END) AS busy,
			SUM(CASE WHEN http_status = 429 THEN 1 ELSE 0 END) AS limited,
			COALESCE(AVG(CASE WHEN status = 'ok' THEN latency_ms END), 0) AS latency,
			MAX(CASE WHEN status = 'failed' THEN created_at END) AS last_fail`).
		Where("created_at > ? AND (is_system = ? OR user_id = ?)", since, true, userID).
		Group("ai_model_id").Scan(&stats)
	byModel := map[uint]int{}
	for i, stat := range stats {
		byModel[stat.AIModelID] = i
	}

	rows := make([]modelHealthRow, 0, len(found))
	providers := map[uint]models.AIProvider{}
	for _, model := range found {
		if isComboModel(model) || notChatModelError(model.ModelID) != "" {
			continue
		}
		row := modelHealthRow{ID: model.ID, Name: model.Name, ModelID: model.ModelID, Provider: model.Provider.Name, ProviderID: model.ProviderID, IsSystem: model.IsSystem}
		if index, present := byModel[model.ID]; present {
			stat := stats[index]
			row.Calls, row.Succeeded, row.Failed, row.Busy, row.Limited = stat.Total, stat.Ok, stat.Failed, stat.Busy, stat.Limited
			row.AvgLatencyMS = int64(stat.Latency)
			row.LastFailure = stat.LastFail
		}
		if row.LastFailure != nil {
			var last models.AIModelCallLog
			if config.DB.Where("ai_model_id = ? AND status = ? AND (is_system = ? OR user_id = ?)", model.ID, "failed", true, userID).
				Order("created_at DESC").First(&last).Error == nil {
				row.LastStatus = last.HTTPStatus
			}
		}
		if until := comboHealth.RestingUntil(model.ID); !until.IsZero() {
			row.RestingUntil = &until
		}
		row.Status = healthStatus(row)
		rows = append(rows, row)
		providers[model.ProviderID] = model.Provider
	}

	providerRows := make([]providerHealthRow, 0, len(providers))
	for _, provider := range providers {
		row := providerHealthRow{ID: provider.ID, Name: provider.Name}
		if key, keyErr := resolveProviderAPIKey(provider, userID); keyErr == nil {
			if quota, seen := seenProviderQuota(provider.BaseURL, key); seen {
				row.Quota = &quota
			}
			if isOpenRouter(provider.BaseURL) {
				row.OpenRouter = openRouterKey(provider.BaseURL, key)
			}
		}
		providerRows = append(providerRows, row)
	}
	sort.Slice(providerRows, func(i, j int) bool { return providerRows[i].Name < providerRows[j].Name })

	c.JSON(http.StatusOK, gin.H{"ok": true, "window_hours": int(healthWindow.Hours()), "models": rows, "providers": providerRows})
}
