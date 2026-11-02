package handlers

import (
	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
)

func redactActivity(v any) any {
	secret := func(key string) bool {
		key = strings.ToLower(key)
		for _, part := range []string{"token", "password", "secret", "api_key", "otp", "credential", "clipboard"} {
			if strings.Contains(key, part) {
				return true
			}
		}
		return false
	}
	var walk func(any) any
	walk = func(value any) any {
		switch item := value.(type) {
		case map[string]any:
			out := map[string]any{}
			for key, child := range item {
				if secret(key) {
					out[key] = "[REDACTED]"
				} else {
					out[key] = walk(child)
				}
			}
			return out
		case []any:
			out := make([]any, len(item))
			for i, child := range item {
				out[i] = walk(child)
			}
			return out
		default:
			return item
		}
	}
	return walk(v)
}
func CreateToolActivity(c *gin.Context) {
	uid, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	var req struct {
		AgentID           *uint  `json:"agent_id"`
		SkillID           *uint  `json:"skill_id"`
		ToolName          string `json:"tool_name" binding:"required"`
		Arguments         any    `json:"arguments"`
		TargetApplication string `json:"target_application"`
		ApprovalStatus    string `json:"approval_status"`
		ExecutionStatus   string `json:"execution_status"`
		Error             string `json:"error"`
		DurationMS        int64  `json:"duration_ms"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.ToolName) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "tool_name wajib diisi."})
		return
	}
	if req.AgentID != nil {
		var agent models.Agent
		if err := config.DB.Where("id = ? AND is_active = ? AND (user_id IS NULL OR user_id = ?)", *req.AgentID, true, uid).First(&agent).Error; err != nil {
			c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "Agent tidak dimiliki user."})
			return
		}
	}
	if req.SkillID != nil {
		var skill models.Skill
		if err := config.DB.Where("id = ? AND user_id = ?", *req.SkillID, uid).First(&skill).Error; err != nil {
			c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "Skill tidak dimiliki user."})
			return
		}
	}
	args, _ := json.Marshal(redactActivity(req.Arguments))
	row := models.ToolActivity{UserID: uid, AgentID: req.AgentID, SkillID: req.SkillID, ToolName: req.ToolName, ArgumentsJSON: string(args), TargetApplication: req.TargetApplication, ApprovalStatus: req.ApprovalStatus, ExecutionStatus: req.ExecutionStatus, Error: req.Error, DurationMS: req.DurationMS}
	if err := config.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal menyimpan aktivitas tool."})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true, "id": row.ID})
}

func GetToolActivities(c *gin.Context) {
	uid, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	var rows []models.ToolActivity
	if err := config.DB.Where("user_id = ?", uid).Order("created_at DESC").Limit(100).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal mengambil aktivitas tool."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "activities": rows})
}
