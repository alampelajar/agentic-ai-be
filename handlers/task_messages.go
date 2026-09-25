package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

var validTaskMessageRoles = map[string]bool{
	"user":      true,
	"assistant": true,
}

func getOwnedTask(c *gin.Context) (*models.Task, bool) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return nil, false
	}

	taskID, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || taskID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "ID task tidak valid."})
		return nil, false
	}

	var task models.Task
	if err := config.DB.Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Task tidak ditemukan."})
		return nil, false
	}
	return &task, true
}

func GetTaskMessages(c *gin.Context) {
	task, ok := getOwnedTask(c)
	if !ok {
		return
	}

	var messages []models.TaskMessage
	if err := config.DB.Where("task_id = ?", task.ID).Order("created_at ASC").Order("id ASC").Find(&messages).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal mengambil riwayat chat."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "messages": messages})
}

type taskMessageRequest struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	Model    string `json:"model"`
	ModelID  *uint  `json:"model_id"`
	Provider string `json:"provider"`
}

func AddTaskMessage(c *gin.Context) {
	task, ok := getOwnedTask(c)
	if !ok {
		return
	}

	var req taskMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Data pesan tidak valid."})
		return
	}

	req.Role = strings.ToLower(strings.TrimSpace(req.Role))
	req.Content = strings.TrimSpace(req.Content)
	if !validTaskMessageRoles[req.Role] || req.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Role atau isi pesan tidak valid."})
		return
	}

	message := models.TaskMessage{
		TaskID:   task.ID,
		Role:     req.Role,
		Content:  req.Content,
		Model:    strings.TrimSpace(req.Model),
		ModelID:  req.ModelID,
		Provider: strings.TrimSpace(req.Provider),
	}

	if err := config.DB.Create(&message).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal menyimpan pesan."})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"ok": true, "message": message})
}
