package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// ============================================================
// GET /api/admin/tasks
// ============================================================

type AdminTaskResponse struct {
	ID          uint   `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Label       string `json:"label"`
	Priority    string `json:"priority"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`

	User *struct {
		ID    uint   `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"user,omitempty"`

	Agent *struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"agent,omitempty"`
}

func AdminGetTasks(c *gin.Context) {
	var tasks []models.Task

	err := config.DB.
		Preload("User").
		Preload("Agent").
		Order("created_at DESC").
		Find(&tasks).
		Error

	if err != nil {
		c.Error(err)

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal mengambil data tasks",
		})

		return
	}

	result := make(
		[]AdminTaskResponse,
		0,
		len(tasks),
	)

	for _, task := range tasks {
		item := AdminTaskResponse{
			ID:          task.ID,
			Title:       task.Title,
			Description: task.Description,
			Status:      task.Status,
			Label:       task.Label,
			Priority:    task.Priority,
			CreatedAt:   task.CreatedAt.Format("2006-01-02 15:04:05"),
			UpdatedAt:   task.UpdatedAt.Format("2006-01-02 15:04:05"),
		}

		// ========================================================
		// USER
		// ========================================================

		if task.User.ID != 0 {
			item.User = &struct {
				ID    uint   `json:"id"`
				Name  string `json:"name"`
				Email string `json:"email"`
			}{
				ID:    task.User.ID,
				Name:  task.User.Name,
				Email: task.User.Email,
			}
		}

		// ========================================================
		// AGENT
		// ========================================================

		if task.Agent != nil {
			item.Agent = &struct {
				ID   uint   `json:"id"`
				Name string `json:"name"`
				Slug string `json:"slug"`
			}{
				ID:   task.Agent.ID,
				Name: task.Agent.Name,
				Slug: task.Agent.Slug,
			}
		}

		result = append(
			result,
			item,
		)
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"tasks": result,
	})
}