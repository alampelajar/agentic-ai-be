package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

var validTaskStatuses = map[string]bool{
	"todo":         true,
	"in progress": true,
	"done":         true,
	"canceled":     true,
	"backlog":      true,
}

var validTaskLabels = map[string]bool{
	"bug":           true,
	"feature":       true,
	"documentation": true,
}

var validTaskPriorities = map[string]bool{
	"low":      true,
	"medium":   true,
	"high":     true,
	"critical": true,
}

type taskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Label       string `json:"label"`
	Priority    string `json:"priority"`
	AgentID     *uint  `json:"agent_id"`
	ModelID     *uint  `json:"model_id"`
}

type taskPatchRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
	Label       *string `json:"label"`
	Priority    *string `json:"priority"`
	AgentID     *uint   `json:"agent_id"`
	ModelID     *uint   `json:"model_id"`
}

func normalizeTaskRequest(req *taskRequest) error {
	req.Title = strings.TrimSpace(req.Title)
	req.Description = strings.TrimSpace(req.Description)
	req.Status = strings.TrimSpace(strings.ToLower(req.Status))
	req.Label = strings.TrimSpace(strings.ToLower(req.Label))
	req.Priority = strings.TrimSpace(strings.ToLower(req.Priority))

	if req.Status == "" {
		req.Status = "todo"
	}
	if req.Label == "" {
		req.Label = "feature"
	}
	if req.Priority == "" {
		req.Priority = "medium"
	}

	if req.Title == "" {
		return gin.Error{Err: strconv.ErrSyntax}
	}
	if !validTaskStatuses[req.Status] ||
		!validTaskLabels[req.Label] ||
		!validTaskPriorities[req.Priority] {
		return gin.Error{Err: strconv.ErrSyntax}
	}

	return nil
}

// ============================================================
// GET ALL TASKS
// ============================================================

func GetTasks(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	var tasks []models.Task

	if err := config.DB.
		Preload("Agent").
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&tasks).
		Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal mengambil task.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"tasks": tasks,
	})
}

// ============================================================
// GET SINGLE TASK
// GET /api/tasks/:id
// ============================================================

func GetTask(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	idText := strings.TrimSpace(c.Param("id"))
	idText = strings.Trim(idText, "\"'")

	taskID, err := strconv.ParseUint(idText, 10, 64)
	if err != nil || taskID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "ID task tidak valid.",
		})
		return
	}

	var task models.Task

	err = config.DB.
		Preload("Agent").
		Where(
			"id = ? AND user_id = ?",
			taskID,
			userID,
		).
		First(&task).
		Error

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "Task tidak ditemukan.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":   true,
		"task": task,
	})
}

// ============================================================
// CREATE TASK
// ============================================================

func CreateTask(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	var req taskRequest

	if err := c.ShouldBindJSON(&req); err != nil ||
		normalizeTaskRequest(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Data task tidak valid.",
		})
		return
	}

	if req.AgentID != nil {
		var agent models.Agent

		if err := config.DB.
			Where(
				"id = ? AND is_active = ?",
				*req.AgentID,
				true,
			).
			First(&agent).
			Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Agent tidak ditemukan.",
			})
			return
		}
	}

	task := models.Task{
		UserID:      userID,
		AgentID:     req.AgentID,
		ModelID:     req.ModelID,
		Title:       req.Title,
		Description: req.Description,
		Status:      req.Status,
		Label:       req.Label,
		Priority:    req.Priority,
	}

	if err := config.DB.Create(&task).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal membuat task.",
		})
		return
	}

	config.DB.
		Preload("Agent").
		First(&task, task.ID)

	c.JSON(http.StatusCreated, gin.H{
		"ok":   true,
		"task": task,
	})
}

// ============================================================
// UPDATE TASK
// ============================================================

func UpdateTask(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	var task models.Task

	if err := config.DB.
		Where(
			"id = ? AND user_id = ?",
			c.Param("id"),
			userID,
		).
		First(&task).
		Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "Task tidak ditemukan.",
		})
		return
	}

	var req taskPatchRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Data task tidak valid.",
		})
		return
	}

	if req.Title != nil {
		v := strings.TrimSpace(*req.Title)

		if v == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Judul task wajib diisi.",
			})
			return
		}

		task.Title = v
	}

	if req.Description != nil {
		task.Description = strings.TrimSpace(*req.Description)
	}

	if req.Status != nil {
		v := strings.ToLower(strings.TrimSpace(*req.Status))

		if !validTaskStatuses[v] {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Status task tidak valid.",
			})
			return
		}

		task.Status = v
	}

	if req.Label != nil {
		v := strings.ToLower(strings.TrimSpace(*req.Label))

		if !validTaskLabels[v] {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Label task tidak valid.",
			})
			return
		}

		task.Label = v
	}

	if req.Priority != nil {
		v := strings.ToLower(strings.TrimSpace(*req.Priority))

		if !validTaskPriorities[v] {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Prioritas task tidak valid.",
			})
			return
		}

		task.Priority = v
	}

	if req.AgentID != nil {
		var agent models.Agent

		if err := config.DB.
			Where(
				"id = ? AND is_active = ?",
				*req.AgentID,
				true,
			).
			First(&agent).
			Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Agent tidak ditemukan.",
			})
			return
		}

		task.AgentID = req.AgentID
	}

	if req.ModelID != nil {
		task.ModelID = req.ModelID
	}

	if err := config.DB.Save(&task).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal memperbarui task.",
		})
		return
	}

	config.DB.
		Preload("Agent").
		First(&task, task.ID)

	c.JSON(http.StatusOK, gin.H{
		"ok":   true,
		"task": task,
	})
}

// ============================================================
// DELETE TASK
// ============================================================

func DeleteTask(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	result := config.DB.
		Where(
			"id = ? AND user_id = ?",
			c.Param("id"),
			userID,
		).
		Delete(&models.Task{})

	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal menghapus task.",
		})
		return
	}

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "Task tidak ditemukan.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Task berhasil dihapus.",
	})
}
