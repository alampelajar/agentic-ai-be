package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// Persistent Memory endpoints. Every query is scoped by the user_id from the
// session, never by an id sent by the client, and memory text is never logged.

func memoryError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"ok": false, "error": message})
}

func memoryID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return uint(id), true
}

// likePattern escapes LIKE wildcards so a search for "100%" is literal.
func likePattern(term string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(term) + "%"
}

// memoryReferencesAreMine checks the agent and the task a memory points at.
func memoryReferencesAreMine(userID uint, agentID, taskID *uint) (string, bool) {
	if agentID != nil && !ensureAgentAccessible(userID, *agentID) {
		return "Agent tidak ditemukan.", false
	}
	if taskID != nil {
		var count int64
		config.DB.Model(&models.Task{}).Where("id = ? AND user_id = ?", *taskID, userID).Count(&count)
		if count == 0 {
			return "Task tidak ditemukan.", false
		}
	}
	return "", true
}

func GetMemories(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		memoryError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	query := config.DB.Where("user_id = ?", userID)
	if agent := strings.TrimSpace(c.Query("agent_id")); agent == "global" {
		query = query.Where("agent_id IS NULL")
	} else if agent != "" {
		agentID, err := strconv.ParseUint(agent, 10, 64)
		if err != nil {
			memoryError(c, http.StatusBadRequest, "agent_id tidak valid.")
			return
		}
		query = query.Where("agent_id = ?", agentID)
	}
	if category := strings.TrimSpace(c.Query("category")); category != "" {
		if !services.IsMemoryCategory(category) {
			memoryError(c, http.StatusBadRequest, "category tidak valid.")
			return
		}
		query = query.Where("category = ?", category)
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		if status != models.MemoryStatusActive && status != models.MemoryStatusArchived {
			memoryError(c, http.StatusBadRequest, "status tidak valid.")
			return
		}
		query = query.Where("status = ?", status)
	}
	if term := strings.TrimSpace(c.Query("q")); term != "" {
		pattern := likePattern(term)
		query = query.Where(`(title ILIKE ? ESCAPE '\' OR content ILIKE ? ESCAPE '\')`, pattern, pattern)
	}
	memories := []models.AgentMemory{}
	if err := query.Order("updated_at DESC, id DESC").Limit(services.MaxMemoriesPerUser).Find(&memories).Error; err != nil {
		memoryError(c, http.StatusInternalServerError, "Gagal mengambil memory.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "memories": memories})
}

func CreateMemory(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		memoryError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	var req struct {
		AgentID      *uint  `json:"agent_id"`
		Category     string `json:"category"`
		Title        string `json:"title"`
		Content      string `json:"content"`
		Importance   *int   `json:"importance"`
		SourceTaskID *uint  `json:"source_task_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		memoryError(c, http.StatusBadRequest, "Format memory tidak valid.")
		return
	}
	importance := models.MemoryDefaultImportance
	if req.Importance != nil {
		importance = *req.Importance
	}
	input := services.MemoryInput{Category: strings.TrimSpace(req.Category), Title: req.Title, Content: req.Content, Importance: importance}
	if err := services.ValidateMemory(input); err != nil {
		memoryError(c, http.StatusBadRequest, memoryValidationMessage(err))
		return
	}
	if message, ok := memoryReferencesAreMine(userID, req.AgentID, req.SourceTaskID); !ok {
		memoryError(c, http.StatusBadRequest, message)
		return
	}
	var count int64
	if err := config.DB.Model(&models.AgentMemory{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		memoryError(c, http.StatusInternalServerError, "Gagal menyimpan memory.")
		return
	}
	if count >= services.MaxMemoriesPerUser {
		memoryError(c, http.StatusConflict, "Jumlah memory sudah mencapai batas. Hapus memory yang tidak dipakai.")
		return
	}
	memory := models.AgentMemory{
		UserID:       userID,
		AgentID:      req.AgentID,
		Category:     input.Category,
		Title:        strings.TrimSpace(input.Title),
		Content:      strings.TrimSpace(input.Content),
		Importance:   input.Importance,
		Status:       models.MemoryStatusActive,
		SourceTaskID: req.SourceTaskID,
	}
	if err := config.DB.Create(&memory).Error; err != nil {
		memoryError(c, http.StatusInternalServerError, "Gagal menyimpan memory.")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true, "memory": memory})
}

func memoryValidationMessage(err error) string {
	if errors.Is(err, services.ErrSensitiveMemory) {
		return "Memory tampak berisi password, OTP, API key, token, atau kredensial lain. Hapus bagian itu; kredensial tidak boleh disimpan."
	}
	return err.Error()
}

// UpdateMemory changes only the fields present in the body. A null agent_id makes
// the memory global.
func UpdateMemory(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		memoryError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	id, ok := memoryID(c)
	if !ok {
		memoryError(c, http.StatusNotFound, "Memory tidak ditemukan.")
		return
	}
	var memory models.AgentMemory
	if err := config.DB.Where("id = ? AND user_id = ?", id, userID).First(&memory).Error; err != nil {
		memoryError(c, http.StatusNotFound, "Memory tidak ditemukan.")
		return
	}
	var body map[string]json.RawMessage
	if err := c.ShouldBindJSON(&body); err != nil {
		memoryError(c, http.StatusBadRequest, "Format update tidak valid.")
		return
	}

	input := services.MemoryInput{Category: memory.Category, Title: memory.Title, Content: memory.Content, Importance: memory.Importance}
	status := memory.Status
	agentID := memory.AgentID
	read := func(key string, target any) bool {
		raw, present := body[key]
		if !present {
			return true
		}
		return json.Unmarshal(raw, target) == nil
	}
	valid := read("category", &input.Category) && read("title", &input.Title) && read("content", &input.Content) &&
		read("importance", &input.Importance) && read("status", &status) && read("agent_id", &agentID)
	if !valid {
		memoryError(c, http.StatusBadRequest, "Format update tidak valid.")
		return
	}
	input.Category = strings.TrimSpace(input.Category)
	if err := services.ValidateMemory(input); err != nil {
		memoryError(c, http.StatusBadRequest, memoryValidationMessage(err))
		return
	}
	if status != models.MemoryStatusActive && status != models.MemoryStatusArchived {
		memoryError(c, http.StatusBadRequest, "status tidak valid.")
		return
	}
	if _, changed := body["agent_id"]; changed {
		if message, ok := memoryReferencesAreMine(userID, agentID, nil); !ok {
			memoryError(c, http.StatusBadRequest, message)
			return
		}
	}

	updates := map[string]interface{}{
		"category":   input.Category,
		"title":      strings.TrimSpace(input.Title),
		"content":    strings.TrimSpace(input.Content),
		"importance": input.Importance,
		"status":     status,
		"agent_id":   agentID,
	}
	if err := config.DB.Model(&memory).Where("user_id = ?", userID).Updates(updates).Error; err != nil {
		memoryError(c, http.StatusInternalServerError, "Gagal memperbarui memory.")
		return
	}
	if err := config.DB.Where("id = ? AND user_id = ?", id, userID).First(&memory).Error; err != nil {
		memoryError(c, http.StatusInternalServerError, "Gagal memperbarui memory.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "memory": memory})
}

// DeleteMemory removes the record itself; nothing is kept behind.
func DeleteMemory(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		memoryError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	id, ok := memoryID(c)
	if !ok {
		memoryError(c, http.StatusNotFound, "Memory tidak ditemukan.")
		return
	}
	result := config.DB.Unscoped().Where("id = ? AND user_id = ?", id, userID).Delete(&models.AgentMemory{})
	if result.Error != nil {
		memoryError(c, http.StatusInternalServerError, "Gagal menghapus memory.")
		return
	}
	if result.RowsAffected == 0 {
		memoryError(c, http.StatusNotFound, "Memory tidak ditemukan.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func GetMemorySettings(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		memoryError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "settings": gin.H{"enabled": services.MemoryEnabled(config.DB, userID)}})
}

func UpdateMemorySettings(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		memoryError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		memoryError(c, http.StatusBadRequest, "enabled wajib diisi.")
		return
	}
	setting := models.UserMemorySetting{UserID: userID, Enabled: *req.Enabled}
	err := config.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "updated_at"}),
	}).Create(&setting).Error
	if err != nil {
		memoryError(c, http.StatusInternalServerError, "Gagal menyimpan pengaturan memory.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "settings": gin.H{"enabled": *req.Enabled}})
}
