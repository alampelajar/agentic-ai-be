package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/utils"
)

type createSystemAIModelRequest struct {
	ProviderName string `json:"provider_name" binding:"required"`
	ProviderType string `json:"provider_type" binding:"required"`
	BaseURL      string `json:"base_url" binding:"required"`
	APIKey       string `json:"api_key" binding:"required"`
	Name         string `json:"name" binding:"required"`
	ModelID      string `json:"model_id" binding:"required"`
	Description  string `json:"description"`
}

type updateSystemAIModelRequest struct {
	Name        *string `json:"name"`
	ModelID     *string `json:"model_id"`
	Description *string `json:"description"`
	IsActive    *bool   `json:"is_active"`
}

func systemProviderSlug(name string) string {
	value := strings.ToLower(strings.TrimSpace(name))
	value = strings.NewReplacer(
		" ", "-", "_", "-", "/", "-", "\\", "-", ".", "-", ":", "-",
	).Replace(value)
	value = strings.Trim(value, "-")
	if value == "" {
		return "system-provider"
	}
	return value
}

// AdminGetSystemAIModels returns only system-managed models. API keys are never
// serialized because AIProvider.APIKeyEncrypted has a json:"-" tag.
func AdminGetSystemAIModels(c *gin.Context) {
	var aiModels []models.AIModel
	if err := config.DB.
		Preload("Provider").
		Preload("Agents").
		Where("ai_models.is_system = ?", true).
		Order("ai_models.created_at DESC").
		Find(&aiModels).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "error": "Gagal mengambil katalog model sistem.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "models": aiModels})
}

// AdminCreateSystemAIModel adds a model to the shared system catalog.
// The model and provider are system-owned; user-created models remain private.
func AdminCreateSystemAIModel(c *gin.Context) {
	var req createSystemAIModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "error": "Lengkapi provider, Base URL, API key, nama model, dan Model ID.",
		})
		return
	}

	req.ProviderName = strings.TrimSpace(req.ProviderName)
	req.ProviderType = normalizeProviderType(req.ProviderType)
	req.BaseURL = strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.Name = strings.TrimSpace(req.Name)
	req.ModelID = strings.TrimSpace(req.ModelID)
	req.Description = strings.TrimSpace(req.Description)

	if req.ProviderName == "" || req.BaseURL == "" || req.APIKey == "" || req.Name == "" || req.ModelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "error": "Semua field wajib diisi kecuali deskripsi.",
		})
		return
	}

	parsedURL, err := url.ParseRequestURI(req.BaseURL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "error": "Base URL harus berupa URL HTTP/HTTPS yang valid.",
		})
		return
	}

	encryptedAPIKey, err := utils.EncryptString(req.APIKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "error": "Gagal mengenkripsi API key. Periksa AI_ENCRYPTION_KEY pada backend.",
		})
		return
	}

	tx := config.DB.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "error": "Gagal memulai transaksi database.",
		})
		return
	}
	defer tx.Rollback()

	slug := systemProviderSlug(req.ProviderName)
	var provider models.AIProvider
	providerErr := tx.
		Where("slug = ? AND is_system = ? AND user_id IS NULL", slug, true).
		First(&provider).Error

	switch {
	case providerErr == nil:
		if err := tx.Model(&provider).Updates(map[string]interface{}{
			"name": req.ProviderName,
			"base_url": req.BaseURL,
			"type": req.ProviderType,
			"api_key_encrypted": encryptedAPIKey,
			"is_active": true,
		}).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok": false, "error": "Gagal memperbarui konfigurasi provider sistem.",
			})
			return
		}
		provider.Name = req.ProviderName
		provider.BaseURL = req.BaseURL
		provider.Type = req.ProviderType
		provider.APIKeyEncrypted = encryptedAPIKey
		provider.IsActive = true

	case errors.Is(providerErr, gorm.ErrRecordNotFound):
		provider = models.AIProvider{
			UserID: nil,
			Name: req.ProviderName,
			Slug: slug,
			BaseURL: req.BaseURL,
			Type: req.ProviderType,
			APIKeyEncrypted: encryptedAPIKey,
			IsSystem: true,
			IsActive: true,
		}
		if err := tx.Create(&provider).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok": false, "error": "Gagal membuat provider sistem.",
			})
			return
		}

	default:
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "error": "Gagal memeriksa provider sistem.",
		})
		return
	}

	var duplicate int64
	if err := tx.Model(&models.AIModel{}).
		Where("provider_id = ? AND model_id = ? AND is_system = ? AND deleted_at IS NULL",
			provider.ID, req.ModelID, true).
		Count(&duplicate).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "error": "Gagal memeriksa duplikasi model.",
		})
		return
	}
	if duplicate > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"ok": false, "error": "Model ID tersebut sudah terdaftar pada provider sistem ini.",
		})
		return
	}

	aiModel := models.AIModel{
		ProviderID: provider.ID,
		UserID: nil,
		Name: req.Name,
		ModelID: req.ModelID,
		Description: req.Description,
		IsSystem: true,
		IsActive: true,
	}
	if err := tx.Create(&aiModel).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "error": "Gagal menyimpan model global.",
		})
		return
	}

	var agents []models.Agent
	if err := tx.Where("is_active = ?", true).Find(&agents).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "error": "Gagal mengambil daftar agent.",
		})
		return
	}
	for _, agent := range agents {
		link := models.AgentModel{
			AgentID: agent.ID,
			AIModelID: aiModel.ID,
			IsDefault: false,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&link).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok": false, "error": "Gagal menghubungkan model ke agent.",
			})
			return
		}
	}

	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "error": "Gagal menyimpan transaksi model global.",
		})
		return
	}

	if err := config.DB.Preload("Provider").Preload("Agents").First(&aiModel, aiModel.ID).Error; err != nil {
		c.JSON(http.StatusCreated, gin.H{
			"ok": true, "message": "Model global berhasil ditambahkan.", "model": aiModel,
		})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"ok": true, "message": "Model global berhasil ditambahkan dan tersedia untuk semua akun.", "model": aiModel,
	})
}

// AdminUpdateSystemAIModel edits safe catalog fields or toggles availability.
// Disabling keeps the record and agent links intact for easy reactivation.
func AdminUpdateSystemAIModel(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "ID model tidak valid."})
		return
	}

	var aiModel models.AIModel
	if err := config.DB.Where("id = ? AND is_system = ?", id, true).First(&aiModel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Model global tidak ditemukan."})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal mengambil model global."})
		return
	}

	var req updateSystemAIModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Data perubahan tidak valid."})
		return
	}

	updates := map[string]interface{}{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Nama model tidak boleh kosong."})
			return
		}
		updates["name"] = name
	}
	if req.ModelID != nil {
		modelID := strings.TrimSpace(*req.ModelID)
		if modelID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Model ID tidak boleh kosong."})
			return
		}

		var duplicate int64
		if err := config.DB.Model(&models.AIModel{}).
			Where("provider_id = ? AND model_id = ? AND id <> ? AND is_system = ? AND deleted_at IS NULL",
				aiModel.ProviderID, modelID, aiModel.ID, true).
			Count(&duplicate).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal memeriksa duplikasi Model ID."})
			return
		}
		if duplicate > 0 {
			c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "Model ID tersebut sudah digunakan oleh model lain pada provider ini."})
			return
		}
		updates["model_id"] = modelID
	}
	if req.Description != nil {
		updates["description"] = strings.TrimSpace(*req.Description)
	}
	if req.IsActive != nil {
		updates["is_active"] = *req.IsActive
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Tidak ada perubahan yang dikirim."})
		return
	}

	if err := config.DB.Model(&aiModel).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal memperbarui model global."})
		return
	}
	config.DB.Preload("Provider").Preload("Agents").First(&aiModel, aiModel.ID)
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Model global berhasil diperbarui.", "model": aiModel})
}

// AdminDeleteSystemAIModel removes one model from the shared system catalog.
// It does not delete the provider because other models may use that provider.
func AdminDeleteSystemAIModel(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "ID model tidak valid."})
		return
	}

	tx := config.DB.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal memulai transaksi penghapusan."})
		return
	}
	defer tx.Rollback()

	var aiModel models.AIModel
	if err := tx.Where("id = ? AND is_system = ?", id, true).First(&aiModel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Model global tidak ditemukan."})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal mengambil model global."})
		return
	}

	if err := tx.Where("ai_model_id = ?", aiModel.ID).Delete(&models.AgentModel{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal menghapus hubungan model dengan agent."})
		return
	}

	if err := tx.Delete(&aiModel).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal menghapus model global."})
		return
	}

	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal menyimpan penghapusan model."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Model global berhasil dihapus."})
}
