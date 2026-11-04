package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

type agentSkillResponse struct {
	ID        uint          `json:"id"`
	AgentID   uint          `json:"agent_id"`
	SkillID   uint          `json:"skill_id"`
	IsEnabled bool          `json:"is_enabled"`
	Skill     skillResponse `json:"skill"`
}

func parseAgentID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id == 0 || id > uint64(^uint(0)) {
		return 0, false
	}
	return uint(id), true
}

func ensureAgentAccessible(userID, agentID uint) bool {
	var agent models.Agent
	return config.DB.Select("id").Where("id = ? AND is_active = ? AND (user_id IS NULL OR user_id = ?)", agentID, true, userID).First(&agent).Error == nil
}

func GetAgentSkills(c *gin.Context) {
	userID, ok := currentSkillUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	agentID, ok := parseAgentID(c)
	if !ok || !ensureAgentAccessible(userID, agentID) {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Agent tidak ditemukan."})
		return
	}
	var assignments []models.AgentSkill
	if err := config.DB.Where("user_id = ? AND agent_id = ?", userID, agentID).Preload("Skill.Versions", func(tx *gorm.DB) *gorm.DB { return tx.Order("created_at DESC, id DESC") }).Order("created_at ASC").Find(&assignments).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal mengambil assignment skill."})
		return
	}
	items := make([]agentSkillResponse, 0, len(assignments))
	for _, assignment := range assignments {
		items = append(items, agentSkillResponse{ID: assignment.ID, AgentID: assignment.AgentID, SkillID: assignment.SkillID, IsEnabled: assignment.IsEnabled, Skill: skillFromModel(assignment.Skill)})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "agent_id": agentID, "skills": items})
}

func AssignSkillToAgent(c *gin.Context) {
	userID, ok := currentSkillUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	agentID, ok := parseAgentID(c)
	if !ok || !ensureAgentAccessible(userID, agentID) {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Agent tidak ditemukan."})
		return
	}
	var req struct {
		SkillID uint `json:"skill_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.SkillID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "skill_id wajib diisi."})
		return
	}
	var skill models.Skill
	if err := config.DB.Where("id = ? AND user_id = ? AND status IN ?", req.SkillID, userID, []string{"installed", "enabled", "disabled"}).First(&skill).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Skill tidak ditemukan atau tidak dimiliki user."})
		return
	}
	assignment := models.AgentSkill{UserID: userID, AgentID: agentID, SkillID: skill.ID, IsEnabled: true}
	if err := config.DB.Where("user_id = ? AND agent_id = ? AND skill_id = ?", userID, agentID, skill.ID).FirstOrCreate(&assignment).Error; err != nil {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "Skill sudah terpasang atau assignment gagal."})
		return
	}
	config.DB.Preload("Skill.Versions", func(tx *gorm.DB) *gorm.DB { return tx.Order("created_at DESC").Limit(1) }).First(&assignment, assignment.ID)
	c.JSON(http.StatusCreated, gin.H{"ok": true, "assignment": agentSkillResponse{ID: assignment.ID, AgentID: assignment.AgentID, SkillID: assignment.SkillID, IsEnabled: assignment.IsEnabled, Skill: skillFromModel(assignment.Skill)}})
}

func UpdateAgentSkill(c *gin.Context) {
	userID, ok := currentSkillUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	agentID, ok := parseAgentID(c)
	if !ok || !ensureAgentAccessible(userID, agentID) {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Agent tidak ditemukan."})
		return
	}
	skillID, err := strconv.ParseUint(strings.TrimSpace(c.Param("skill_id")), 10, 64)
	if err != nil || skillID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "skill_id tidak valid."})
		return
	}
	var req struct {
		IsEnabled *bool `json:"is_enabled" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.IsEnabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "is_enabled wajib diisi."})
		return
	}
	var assignment models.AgentSkill
	if err := config.DB.Where("user_id = ? AND agent_id = ? AND skill_id = ?", userID, agentID, skillID).First(&assignment).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Assignment skill tidak ditemukan."})
		return
	}
	if err := config.DB.Model(&assignment).Update("is_enabled", *req.IsEnabled).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal memperbarui assignment."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "assignment": gin.H{"id": assignment.ID, "agent_id": agentID, "skill_id": skillID, "is_enabled": *req.IsEnabled}})
}

func RemoveSkillFromAgent(c *gin.Context) {
	userID, ok := currentSkillUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	agentID, ok := parseAgentID(c)
	if !ok || !ensureAgentAccessible(userID, agentID) {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Agent tidak ditemukan."})
		return
	}
	skillID, err := strconv.ParseUint(strings.TrimSpace(c.Param("skill_id")), 10, 64)
	if err != nil || skillID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "skill_id tidak valid."})
		return
	}
	result := config.DB.Where("user_id = ? AND agent_id = ? AND skill_id = ?", userID, agentID, skillID).Delete(&models.AgentSkill{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal melepas skill."})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Assignment skill tidak ditemukan."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
