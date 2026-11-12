package handlers

import (
	"log"
	"time"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// autoModelLimit is how many ranked models one message may try before it gives up.
const autoModelLimit = 3

// chooseAutoModels ranks the chat models this agent may use for what the message needs, healthy
// ones first. It returns the models to try, in order, and why the first was chosen; when nothing
// fits it returns a message for the person instead.
func chooseAutoModels(userID, agentID uint, hasImages, workspaceOpen, hasDocuments bool, messageLength int) ([]models.AIModel, string, string) {
	query := config.DB.Model(&models.AIModel{}).Select("ai_models.*").Preload("Provider").
		Joins("INNER JOIN a_iproviders ON a_iproviders.id = ai_models.provider_id").
		Where("ai_models.is_active = ? AND a_iproviders.is_active = ?", true, true).
		Where("(ai_models.is_system = ? OR ai_models.user_id = ?)", true, userID).
		Order("ai_models.is_system DESC").Order("ai_models.id ASC")
	if agentID != 0 {
		query = query.Joins("INNER JOIN agent_models ON agent_models.ai_model_id = ai_models.id").Where("agent_models.agent_id = ?", agentID)
	}
	var found []models.AIModel
	if err := query.Find(&found).Error; err != nil || len(found) == 0 {
		if err != nil {
			log.Printf("[CHAT] auto: model tidak dapat dibaca: %v", err)
		}
		return nil, "", "Tidak ada model AI yang tersedia untuk agent ini."
	}

	failRates := recentFailRates(userID, 6*time.Hour)
	byID := map[uint]models.AIModel{}
	candidates := make([]services.RouteCandidate, 0, len(found))
	for _, model := range found {
		// A combo chooses for itself, and an embedding model cannot chat.
		if isComboModel(model) || notChatModelError(model.ModelID) != "" {
			continue
		}
		byID[model.ID] = model
		candidates = append(candidates, services.RouteCandidate{
			ModelDBID: model.ID, ModelID: model.ModelID, Name: model.Name, Provider: model.Provider.Name,
			Resting: comboHealth.Resting(model.ID), FailRate: failRates[model.ID],
		})
	}
	need := services.NeedFor(hasImages, workspaceOpen, hasDocuments, messageLength)
	ranked := services.RankModels(candidates, need)
	if len(ranked) == 0 {
		if need == services.NeedVision {
			return nil, "", "Pilihan otomatis tidak menemukan model yang bisa membaca gambar untuk agent ini. Tambahkan model vision (misalnya Gemini atau nvidia/nemotron-3.5-super-vl:free) atau pilih model secara manual."
		}
		return nil, "", "Tidak ada model chat yang tersedia untuk agent ini."
	}
	chosen := make([]models.AIModel, 0, autoModelLimit)
	for _, choice := range ranked {
		if len(chosen) == autoModelLimit {
			break
		}
		chosen = append(chosen, byID[choice.ModelDBID])
	}
	return chosen, ranked[0].Reason, ""
}

// recentFailRates is the share of failed calls per model since `window` ago. A model needs a few
// calls before its rate counts; system models count every user's calls, a person's own only theirs.
func recentFailRates(userID uint, window time.Duration) map[uint]float64 {
	var rows []struct {
		AIModelID uint
		Total     int
		Failed    int
	}
	config.DB.Model(&models.AIModelCallLog{}).
		Select("ai_model_id, COUNT(*) AS total, SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed").
		Where("created_at > ? AND (is_system = ? OR user_id = ?)", time.Now().Add(-window), true, userID).
		Group("ai_model_id").Scan(&rows)
	rates := map[uint]float64{}
	for _, row := range rows {
		if row.Total >= 3 {
			rates[row.AIModelID] = float64(row.Failed) / float64(row.Total)
		}
	}
	return rates
}
