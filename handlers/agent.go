package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// ============================================================
// AVAILABLE MODEL RESPONSE
// ============================================================

type availableModelResponse struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	ModelID     string `json:"model_id"`
	Description string `json:"description"`
	IsSystem    bool   `json:"is_system"`
	IsActive    bool   `json:"is_active"`

	Provider struct {
		ID       uint   `json:"id"`
		Name     string `json:"name"`
		Type     string `json:"type"`
		BaseURL  string `json:"base_url"`
		IsSystem bool   `json:"is_system"`
	} `json:"provider"`
}

// ============================================================
// AGENT RESPONSE
// ============================================================

type agentResponse struct {
	ID          uint                     `json:"id"`
	Slug        string                   `json:"slug"`
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Status      string                   `json:"status"`
	IsActive    bool                     `json:"is_active"`
	Tasks       int64                    `json:"tasks"`
	Completed   int64                    `json:"completed"`
	Progress    int                      `json:"progress"`
	Models      []availableModelResponse `json:"models"`
}

// ============================================================
// GET AVAILABLE MODELS FOR AGENT
// ============================================================

func getAvailableModelsForAgent(
	userID uint,
	agentID uint,
) ([]availableModelResponse, error) {

	var aiModels []models.AIModel

	/*
		Query menggunakan nama tabel PostgreSQL secara eksplisit.

		PENTING:
		- ai_models       = tabel model AI
		- agent_models    = tabel relasi agent dengan model
		- a_iproviders    = tabel provider AI
	*/

	err := config.DB.
		Table("ai_models").
		Joins(`
			INNER JOIN agent_models
				ON agent_models.ai_model_id = ai_models.id
		`).
		Joins(`
			INNER JOIN a_iproviders
				ON a_iproviders.id = ai_models.provider_id
		`).
		Where(
			"agent_models.agent_id = ?",
			agentID,
		).
		Where(
			"ai_models.is_active = ?",
			true,
		).
		Where(
			"a_iproviders.is_active = ?",
			true,
		).
		Where(`
			ai_models.is_system = ?
			OR (ai_models.is_system = ? AND ai_models.user_id = ?)
		`,
			true,
			false,
			userID,
		).
		Order(
			"ai_models.is_system DESC",
		).
		Order(
			"ai_models.name ASC",
		).
		Find(&aiModels).
		Error

	if err != nil {
		return nil, err
	}

	result := make(
		[]availableModelResponse,
		0,
		len(aiModels),
	)

	for _, aiModel := range aiModels {

		var provider models.AIProvider

		err := config.DB.
			Table("a_iproviders").
			Where(
				"id = ?",
				aiModel.ProviderID,
			).
			First(&provider).
			Error

		if err != nil {
			return nil, err
		}

		item := availableModelResponse{
			ID:          aiModel.ID,
			Name:        aiModel.Name,
			ModelID:     aiModel.ModelID,
			Description: aiModel.Description,
			IsSystem:    aiModel.IsSystem,
			IsActive:    aiModel.IsActive,
		}

		item.Provider.ID = provider.ID
		item.Provider.Name = provider.Name
		item.Provider.Type = provider.Type
		item.Provider.BaseURL = provider.BaseURL
		item.Provider.IsSystem = provider.IsSystem

		result = append(
			result,
			item,
		)
	}

	return result, nil
}

// ============================================================
// AGENT TASK STATISTICS
// ============================================================
//
// Statistik task dihitung langsung dari tabel `tasks` berdasarkan:
// - tasks.user_id  = user yang sedang login
// - tasks.agent_id = agent yang sedang ditampilkan
//
// Active task = status `todo` atau `in progress`.
// Completed task = status `done`.
// Status `canceled` dan status lain tidak dihitung ke progress.
// ============================================================

type agentTaskStats struct {
	ActiveTasks    int64 `gorm:"column:active_tasks"`
	CompletedTasks int64 `gorm:"column:completed_tasks"`
}

func getAgentTaskStats(userID uint, agentID uint) (agentTaskStats, int, error) {
	var stats agentTaskStats

	err := config.DB.
		Table("tasks").
		Select(`
			COUNT(*) FILTER (
				WHERE status IN ('todo', 'in progress')
			) AS active_tasks,
			COUNT(*) FILTER (
				WHERE status = 'done'
			) AS completed_tasks
		`).
		Where(
			"user_id = ? AND agent_id = ?",
			userID,
			agentID,
		).
		Scan(&stats).
		Error

	if err != nil {
		return stats, 0, err
	}

	total := stats.ActiveTasks + stats.CompletedTasks
	progress := 0

	if total > 0 {
		progress = int((stats.CompletedTasks * 100) / total)
	}

	return stats, progress, nil
}

// ============================================================
// GET ALL AGENTS
// ============================================================

func GetAgents(c *gin.Context) {

	userID, ok := getCurrentUserID(c)

	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	var agents []models.Agent

	err := config.DB.
		Where(
			"is_active = ?",
			true,
		).
		Order(
			"id ASC",
		).
		Find(&agents).
		Error

	if err != nil {

		c.Error(err)

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal mengambil agent.",
		})
		return
	}

	result := make(
		[]agentResponse,
		0,
		len(agents),
	)

	for _, agent := range agents {

		availableModels, err :=
			getAvailableModelsForAgent(
				userID,
				agent.ID,
			)

		if err != nil {

			c.Error(err)

			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "Gagal mengambil model untuk agent.",
			})
			return
		}

		taskStats, progress, err :=
			getAgentTaskStats(
				userID,
				agent.ID,
			)

		if err != nil {

			c.Error(err)

			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "Gagal mengambil statistik task agent.",
			})
			return
		}

		result = append(
			result,
			agentResponse{
				ID:          agent.ID,
				Slug:        agent.Slug,
				Name:        agent.Name,
				Description: agent.Description,
				Status:      agent.Status,
				IsActive:    agent.IsActive,
				Tasks:       taskStats.ActiveTasks,
				Completed:   taskStats.CompletedTasks,
				Progress:    progress,
				Models:      availableModels,
			},
		)
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"agents": result,
	})
}

// ============================================================
// GET SINGLE AGENT
// ============================================================

func GetAgent(c *gin.Context) {

	userID, ok := getCurrentUserID(c)

	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	agentIDParam := strings.TrimSpace(
		c.Param("id"),
	)

	if agentIDParam == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "ID agent wajib diisi.",
		})
		return
	}

	var agent models.Agent

	// ========================================================
	// COBA CARI BERDASARKAN ID
	// ========================================================

	if numericID, err := strconv.ParseUint(
		agentIDParam,
		10,
		64,
	); err == nil {

		err = config.DB.
			Where(
				"id = ? AND is_active = ?",
				numericID,
				true,
			).
			First(&agent).
			Error

		if err != nil {

			c.JSON(http.StatusNotFound, gin.H{
				"ok":    false,
				"error": "Agent tidak ditemukan.",
			})
			return
		}

	} else {

		// ====================================================
		// KALAU BUKAN ID ANGKA, CARI BERDASARKAN SLUG
		// ====================================================

		err := config.DB.
			Where(
				"slug = ? AND is_active = ?",
				agentIDParam,
				true,
			).
			First(&agent).
			Error

		if err != nil {

			c.JSON(http.StatusNotFound, gin.H{
				"ok":    false,
				"error": "Agent tidak ditemukan.",
			})
			return
		}
	}

	// ========================================================
	// AMBIL MODEL AGENT
	// ========================================================

	availableModels, err :=
		getAvailableModelsForAgent(
			userID,
			agent.ID,
		)

	if err != nil {

		c.Error(err)

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal mengambil model agent.",
		})
		return
	}

	taskStats, progress, err :=
		getAgentTaskStats(
			userID,
			agent.ID,
		)

	if err != nil {

		c.Error(err)

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal mengambil statistik task agent.",
		})
		return
	}

	// ========================================================
	// RESPONSE
	// ========================================================

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"agent": agentResponse{
			ID:          agent.ID,
			Slug:        agent.Slug,
			Name:        agent.Name,
			Description: agent.Description,
			Status:      agent.Status,
			IsActive:    agent.IsActive,
			Tasks:       taskStats.ActiveTasks,
			Completed:   taskStats.CompletedTasks,
			Progress:    progress,
			Models:      availableModels,
		},
	})
}

// ============================================================
// AGENT PLAN REQUEST
// ============================================================

type agentPlanRequest struct {
	Message  string            `json:"message"`
	History  []chatHistoryItem `json:"history"`
	HasImage bool              `json:"has_image"`
}

// ============================================================
// AGENT PLAN RESPONSE
// ============================================================

type agentPlanResponse struct {
	Intent     string `json:"intent"`
	Reason     string `json:"reason"`
	NeedsImage bool   `json:"needs_image"`
}

// ============================================================
// AGENT PLAN
// ============================================================

func AgentPlan(c *gin.Context) {

	// ========================================================
	// VALIDASI USER
	// ========================================================

	_, ok := getCurrentUserID(c)

	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	// ========================================================
	// PARSE REQUEST
	// ========================================================

	var req agentPlanRequest

	if err := c.ShouldBindJSON(&req); err != nil {

		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Format request tidak valid.",
		})
		return
	}

	// ========================================================
	// VALIDASI MESSAGE
	// ========================================================

	message := strings.TrimSpace(
		req.Message,
	)

	if message == "" {

		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Message wajib diisi.",
		})
		return
	}

	// ========================================================
	// DEFAULT PLAN
	// ========================================================

	intent := "chat"

	reason :=
		"Permintaan merupakan percakapan atau pertanyaan biasa."

	needsImage := req.HasImage

	lowerMessage :=
		strings.ToLower(message)

	// ========================================================
	// KEYWORD GENERATOR
	// ========================================================

	generatorKeywords := []string{

		"buat website",
		"buat web",
		"buat situs",

		"buat landing page",
		"buat halaman website",
		"buat halaman web",

		"generate website",
		"generate web",
		"generate landing page",

		"create website",
		"create web",
		"create landing page",

		"build website",
		"build web",

		"bangun website",
		"bangun web",

		"bikin website",
		"bikin web",

		"ubah website",
		"ubah web",

		"modifikasi website",
		"modifikasi web",

		"edit website",
		"edit web",
	}

	// ========================================================
	// CEK GENERATOR
	// ========================================================

	for _, keyword := range generatorKeywords {

		if strings.Contains(
			lowerMessage,
			keyword,
		) {

			intent = "generator"

			reason =
				"Pengguna meminta membuat atau memodifikasi website."

			break
		}
	}

	// ========================================================
	// KEYWORD GAMBAR
	// ========================================================

	imageKeywords := []string{

		"gambar",
		"foto",
		"image",
		"screenshot",

		"lihat ini",

		"jelaskan gambar",
		"analisis gambar",
		"analisa gambar",
	}

	// ========================================================
	// CEK GAMBAR
	// ========================================================

	for _, keyword := range imageKeywords {

		if strings.Contains(
			lowerMessage,
			keyword,
		) {

			needsImage = true

			break
		}
	}

	// ========================================================
	// GAMBAR + GENERATOR
	// ========================================================

	if req.HasImage &&
		intent == "generator" {

		reason =
			"Pengguna meminta membuat atau memodifikasi website berdasarkan gambar yang diberikan."

		needsImage = true
	}

	// ========================================================
	// GAMBAR + CHAT
	// ========================================================

	if req.HasImage &&
		intent != "generator" {

		intent = "chat"

		reason =
			"Pengguna meminta analisis atau pembahasan berdasarkan gambar."
	}

	// ========================================================
	// RESPONSE
	// ========================================================

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"plan": agentPlanResponse{
			Intent:     intent,
			Reason:     reason,
			NeedsImage: needsImage,
		},
	})
}
