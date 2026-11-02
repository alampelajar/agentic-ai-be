package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// userCanReferenceAIModel reports whether a model may be referenced by this
// user: a system model, or one the user owns. Soft-deleted models do not
// match. It is the same visibility rule the chat and model-list endpoints use.
func userCanReferenceAIModel(userID uint, modelID uint) (bool, error) {
	var count int64
	err := config.DB.Model(&models.AIModel{}).
		Where("id = ? AND (is_system = ? OR user_id = ?)", modelID, true, userID).
		Count(&count).Error
	return count > 0, err
}

// requireReferencableModel answers the request and returns false when modelID
// is set but the user may not use it. A missing model and another user's model
// get the same response so the endpoint does not reveal which IDs exist.
func requireReferencableModel(c *gin.Context, userID uint, modelID *uint) bool {
	if modelID == nil {
		return true
	}
	allowed, err := userCanReferenceAIModel(userID, *modelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal memeriksa model."})
		return false
	}
	if !allowed {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Model tidak ditemukan."})
		return false
	}
	return true
}
