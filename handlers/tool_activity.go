package handlers

import (
	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http"
	"regexp"
	"strings"
)

const (
	maxActivityErrorLength  = 500
	maxActivityTargetLength = 200
	maxActivityArgsBytes    = 8192
	redactedValue           = "[REDACTED]"
)

// The only outcomes a client can report. Anything else is rejected rather than
// stored, so the activity list only ever holds values the UI knows how to show.
var (
	activityApprovalStatuses  = map[string]bool{"not_required": true, "approved_once": true, "approved_session": true, "denied": true, "expired": true}
	activityExecutionStatuses = map[string]bool{"completed": true, "failed": true, "cancelled": true}
)

// Keys whose values are never stored. "text" and "content" cover typed or
// written free text, which the server cannot tell apart from a secret.
var activitySecretKeyParts = []string{"token", "password", "passwd", "secret", "api_key", "apikey", "otp", "credential", "clipboard", "authorization", "cookie", "passphrase", "text", "content"}

// Value patterns catch secrets that sit under an innocuous key or inside free
// text such as sidecar error messages and window titles.
var activitySecretValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`),
	regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\b(?:ghp|gho|ghs|ghu|github_pat)_[A-Za-z0-9_]{16,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
}

var activitySecretAssignment = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|otp|authorization)\b(\s*[=:]\s*)("[^"]*"|'[^']*'|\S+)`)

func redactActivityString(s string) string {
	for _, pattern := range activitySecretValuePatterns {
		s = pattern.ReplaceAllString(s, redactedValue)
	}
	return activitySecretAssignment.ReplaceAllString(s, "${1}${2}"+redactedValue)
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

func redactActivity(v any) any {
	secret := func(key string) bool {
		key = strings.ToLower(key)
		for _, part := range activitySecretKeyParts {
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
		case string:
			return redactActivityString(item)
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
	if len(req.ToolName) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "tool_name terlalu panjang."})
		return
	}
	if !activityApprovalStatuses[req.ApprovalStatus] || !activityExecutionStatuses[req.ExecutionStatus] {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "approval_status atau execution_status tidak valid."})
		return
	}
	args, _ := json.Marshal(redactActivity(req.Arguments))
	if len(args) > maxActivityArgsBytes {
		args = []byte(`{"truncated":true}`)
	}
	row := models.ToolActivity{UserID: uid, AgentID: req.AgentID, SkillID: req.SkillID, ToolName: req.ToolName, ArgumentsJSON: string(args), TargetApplication: truncateRunes(redactActivityString(req.TargetApplication), maxActivityTargetLength), ApprovalStatus: req.ApprovalStatus, ExecutionStatus: req.ExecutionStatus, Error: truncateRunes(redactActivityString(req.Error), maxActivityErrorLength), DurationMS: req.DurationMS}
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
