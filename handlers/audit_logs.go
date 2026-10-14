package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// newAuditLog snapshots the actor identity and target details. It intentionally
// does not capture request bodies, passwords, API keys, or authorization tokens.
func newAuditLog(c *gin.Context, action, entityType, entityID, entityName, message string) models.AuditLog {
	actorID := c.GetUint("user_id")
	var actorUserID *uint
	if actorID != 0 {
		actorUserID = &actorID
	}

	actorEmail := strings.TrimSpace(c.GetString("user_email"))
	actorName := strings.TrimSpace(c.GetString("user_name"))
	if actorName == "" {
		actorName = actorEmail
	}
	if actorName == "" {
		actorName = "Unknown actor"
	}

	return models.AuditLog{
		ActorUserID: actorUserID,
		ActorName:   actorName,
		ActorEmail:  actorEmail,
		ActorRole:   strings.TrimSpace(c.GetString("user_role")),
		Action:      action,
		EntityType:  entityType,
		EntityID:    entityID,
		EntityName:  entityName,
		Message:     message,
	}
}

// AdminGetAuditLogs returns recent persisted audit events to admins only.
// Audit logs are append-only from the application's API and survive target deletion.
func AdminGetAuditLogs(c *gin.Context) {
	logs := make([]models.AuditLog, 0)
	if err := config.DB.Order("created_at DESC").Limit(500).Find(&logs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal mengambil audit logs.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":   true,
		"logs": logs,
	})
}
