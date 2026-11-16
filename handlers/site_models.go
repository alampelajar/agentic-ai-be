package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/sitebuilder"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// BuilderModels lets the Website Builder call the person's usable models: the
// system models (within the daily system quota) and their own. Models are ranked
// like the chat's Auto choice; when one fails, the next is tried.
type BuilderModels struct{}

const builderModelTries = 3

// A rate-limited model is asked again after the wait it names, a few times: a site
// is built in the background, so waiting beats falling back to a slower model.
const (
	builderRateLimitTries = 4
	builderMaxWait        = 60 * time.Second
)

var (
	retryAfterHint = regexp.MustCompile(`retry_after=([0-9]+)`)
	// Groq: "... tokens per minute (TPM): Limit 8000, Requested 12345 ..."
	tokenLimitHint = regexp.MustCompile(`Limit ([0-9]+), Requested ([0-9]+)`)
)

// smallerAnswer returns a lower answer limit that fits a provider's per-request token
// limit, or 0 when the error is not that or the answer would become too short.
func smallerAnswer(err error, current int) int {
	var httpErr *services.ProviderHTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusRequestEntityTooLarge {
		return 0
	}
	match := tokenLimitHint.FindStringSubmatch(err.Error())
	if match == nil {
		return 0
	}
	limit, _ := strconv.Atoi(match[1])
	requested, _ := strconv.Atoi(match[2])
	next := current - (requested - limit) - 256
	if next < 2500 || next >= current {
		return 0
	}
	return next
}

// rateLimitWait says how long to wait before asking a rate-limited model again, or 0
// when the error is not a rate limit.
func rateLimitWait(err error) time.Duration {
	var httpErr *services.ProviderHTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusTooManyRequests {
		return 0
	}
	wait := 20 * time.Second
	if match := retryAfterHint.FindStringSubmatch(err.Error()); match != nil {
		if seconds, convErr := strconv.Atoi(match[1]); convErr == nil {
			wait = time.Duration(seconds+1) * time.Second
		}
	}
	if wait > builderMaxWait {
		wait = builderMaxWait
	}
	return wait
}

func builderCandidates(userID uint, need sitebuilder.Need) ([]models.AIModel, error) {
	query := config.DB.Model(&models.AIModel{}).Select("ai_models.*").Preload("Provider").
		Joins("INNER JOIN a_iproviders ON a_iproviders.id = ai_models.provider_id").
		Where("ai_models.is_active = ? AND a_iproviders.is_active = ?", true, true).
		Where("(ai_models.is_system = ? OR ai_models.user_id = ?)", true, userID).
		Order("ai_models.is_system DESC").Order("ai_models.id ASC")
	var found []models.AIModel
	if err := query.Find(&found).Error; err != nil {
		return nil, err
	}
	overQuota := systemCallsToday(userID) >= int64(comboSystemDaily())
	failRates := recentFailRates(userID, 6*time.Hour)
	byID := map[uint]models.AIModel{}
	candidates := make([]services.RouteCandidate, 0, len(found))
	for _, model := range found {
		if isComboModel(model) || notChatModelError(model.ModelID) != "" || (overQuota && model.IsSystem) {
			continue
		}
		byID[model.ID] = model
		candidates = append(candidates, services.RouteCandidate{
			ModelDBID: model.ID, ModelID: model.ModelID, Name: model.Name, Provider: model.Provider.Name,
			Resting: comboHealth.Resting(model.ID), FailRate: failRates[model.ID],
		})
	}
	routeNeed := services.NeedCoding
	if need == sitebuilder.NeedFast {
		routeNeed = services.NeedFast
	}
	ranked := services.RankModels(candidates, routeNeed)
	chosen := make([]models.AIModel, 0, builderModelTries)
	for _, choice := range ranked {
		if len(chosen) == builderModelTries {
			break
		}
		chosen = append(chosen, byID[choice.ModelDBID])
	}
	if len(chosen) == 0 {
		return nil, errors.New("no usable AI model")
	}
	return chosen, nil
}

// Complete asks the best model and falls back to the next ones on failure. Every
// attempt is logged, so it counts toward the daily system quota and model health.
func (BuilderModels) Complete(ctx context.Context, userID uint, need sitebuilder.Need, messages []sitebuilder.Message) (string, string, error) {
	chosen, err := builderCandidates(userID, need)
	if err != nil {
		return "", "", err
	}
	request := chatRequest{Temperature: 0.4, MaxTokens: 16000, Timeout: 4 * time.Minute}
	if need == sitebuilder.NeedFast {
		request.MaxTokens, request.Timeout = 4096, 90*time.Second
	}
	for _, message := range messages {
		request.Messages = append(request.Messages, chatMessage{Role: message.Role, Content: message.Content})
	}
	var failures []string
	for index, model := range chosen {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		key, keyErr := resolveProviderAPIKey(model.Provider, userID)
		if keyErr != nil {
			failures = append(failures, model.Name+": "+keyErr.Message)
			continue
		}
		attempt := request
		attempt.Model = model.ModelID
		if strings.Contains(strings.ToLower(model.ModelID), "gpt-oss") {
			attempt.ReasoningEffort = "low" // its thinking counts against the answer limit
		}
		started := time.Now()
		answer, callErr := callAIProviderWithRepair(key, model.Provider.BaseURL, attempt)
		for try := 1; try < builderRateLimitTries && callErr != nil; try++ {
			if smaller := smallerAnswer(callErr, attempt.MaxTokens); smaller > 0 {
				log.Printf("[BUILDER] %s: request over its token limit; asking again with an answer limit of %d", model.Name, smaller)
				attempt.MaxTokens = smaller
				answer, callErr = callAIProviderWithRepair(key, model.Provider.BaseURL, attempt)
				continue
			}
			wait := rateLimitWait(callErr)
			if wait == 0 {
				break
			}
			log.Printf("[BUILDER] %s is rate limited; asking again in %s", model.Name, wait)
			select {
			case <-ctx.Done():
				return "", "", ctx.Err()
			case <-time.After(wait):
			}
			answer, callErr = callAIProviderWithRepair(key, model.Provider.BaseURL, attempt)
		}
		entry := models.AIModelCallLog{UserID: userID, AIModelID: model.ID, IsSystem: model.IsSystem, Attempt: index + 1, Status: "ok", LatencyMS: time.Since(started).Milliseconds()}
		if callErr == nil && strings.TrimSpace(answer) == "" {
			callErr = errors.New("empty answer")
		}
		if callErr != nil {
			entry.Status, entry.ErrorClass = "failed", string(services.ClassifyProviderError(callErr))
			if services.ClassifyProviderError(callErr) == services.ErrorRetryable {
				comboHealth.Failure(model.ID)
			}
			failures = append(failures, model.Name+": "+services.RedactSecrets(callErr.Error()))
		} else {
			comboHealth.Success(model.ID)
		}
		if logErr := config.DB.Create(&entry).Error; logErr != nil {
			log.Printf("[BUILDER] could not log the model call: %v", logErr)
		}
		if callErr == nil {
			return answer, model.Name, nil
		}
	}
	return "", "", fmt.Errorf("every model failed: %s", strings.Join(failures, "; "))
}
