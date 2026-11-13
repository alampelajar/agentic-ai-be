package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/httpguard"
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

// recordAudit stores an audit entry for an action that already happened. A failure to
// store it is written to the server log instead of undoing the action.
func recordAudit(c *gin.Context, action, entityType, entityID, entityName, message string) {
	entry := newAuditLog(c, action, entityType, entityID, entityName, message)
	if err := config.DB.Create(&entry).Error; err != nil {
		log.Printf("[audit] %s on %s %s was not recorded: %v", action, entityType, entityID, err)
	}
}

// securityEvent writes a security event to the server log: sign-in failures and locks,
// which are too frequent for the audit table. The address is not logged; a short hash
// of it lets repeated attempts on one account be grouped without storing who it is.
func securityEvent(c *gin.Context, event, email string) {
	account := "-"
	if email = strings.ToLower(strings.TrimSpace(email)); email != "" {
		sum := sha256.Sum256([]byte(email))
		account = hex.EncodeToString(sum[:6])
	}
	log.Printf("[security] event=%s route=%s ip=%s account=%s", event, c.FullPath(), httpguard.ClientKey(c.ClientIP()), account)
}
