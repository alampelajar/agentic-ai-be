package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/utils"
)

// ============================================================
// REQUEST
// ============================================================

type createAIModelRequest struct {
	ProviderID   uint   `json:"provider_id"`
	ProviderName string `json:"provider_name"`
	ProviderType string `json:"provider_type"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	Name         string `json:"name"`
	ModelID      string `json:"model_id"`
	Description  string `json:"description"`
	AgentIDs     []uint `json:"agent_ids"`
}

type updateAIModelRequest struct {
	Name        *string `json:"name"`
	ModelID     *string `json:"model_id"`
	Description *string `json:"description"`
	IsActive    *bool   `json:"is_active"`
	AgentIDs    *[]uint `json:"agent_ids"`
}

type removeModelFromAgentRequest struct {
	AgentID uint `json:"agent_id"`
}

// ============================================================
// GET ALL AI MODELS
// ============================================================

func GetAIModels(c *gin.Context) {
	userID, ok := getCurrentUserID(c)

	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	var aiModels []models.AIModel

	err := config.DB.
		Preload("Provider").
		Preload("Agents").
		Where(`
			is_system = ?
			OR user_id = ?
		`, true, userID).
		Where("deleted_at IS NULL").
		Order("is_system DESC, name ASC").
		Find(&aiModels).
		Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal mengambil model AI.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"models": aiModels,
	})
}

// ============================================================
// CREATE AI MODEL
// ============================================================

func CreateAIModel(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}

	var req createAIModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Data model tidak valid."})
		return
	}

	req.ProviderName = strings.TrimSpace(req.ProviderName)
	req.ProviderType = strings.TrimSpace(req.ProviderType)
	req.BaseURL = strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.Name = strings.TrimSpace(req.Name)
	req.ModelID = strings.TrimSpace(req.ModelID)
	req.Description = strings.TrimSpace(req.Description)

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Nama model wajib diisi."})
		return
	}
	if req.ModelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Model ID wajib diisi."})
		return
	}
	if req.APIKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "API key wajib diisi."})
		return
	}
	if len(req.AgentIDs) != 1 || req.AgentIDs[0] == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Model harus ditambahkan ke tepat satu Agent."})
		return
	}

	var provider models.AIProvider

	if req.ProviderID == 0 {
		if req.ProviderName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Nama provider wajib diisi."})
			return
		}
		if req.ProviderType == "" {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Tipe provider wajib diisi."})
			return
		}
		if req.BaseURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Base URL wajib diisi."})
			return
		}

		baseSlug := strings.ToLower(req.ProviderName)
		baseSlug = strings.NewReplacer(" ", "-", "_", "-", "/", "-", "\\", "-", ".", "-").Replace(baseSlug)
		baseSlug = strings.Trim(baseSlug, "-")
		if baseSlug == "" {
			baseSlug = "custom-provider"
		}
		slug := baseSlug + "-user-" + strconv.FormatUint(uint64(userID), 10)

		var existingProvider models.AIProvider
		findErr := config.DB.Where("user_id = ? AND slug = ? AND is_system = ?", userID, slug, false).First(&existingProvider).Error
		if findErr != nil && findErr != gorm.ErrRecordNotFound {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal memeriksa provider custom."})
			return
		}

		encryptedAPIKey, encryptErr := utils.EncryptString(req.APIKey)
		if encryptErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal mengenkripsi API key."})
			return
		}

		if findErr == gorm.ErrRecordNotFound {
			provider = models.AIProvider{
				UserID: &userID, Name: req.ProviderName, Slug: slug,
				BaseURL: req.BaseURL, Type: req.ProviderType,
				APIKeyEncrypted: encryptedAPIKey, IsSystem: false, IsActive: true,
			}
			if err := config.DB.Create(&provider).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal membuat provider custom."})
				return
			}
		} else {
			provider = existingProvider
			if provider.UserID == nil || *provider.UserID != userID {
				c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "Provider bukan milik user ini."})
				return
			}
			if err := config.DB.Model(&provider).Updates(map[string]interface{}{
				"name": req.ProviderName, "base_url": req.BaseURL, "type": req.ProviderType,
				"api_key_encrypted": encryptedAPIKey, "is_active": true,
			}).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal memperbarui provider custom."})
				return
			}
		}
	} else {
		err := config.DB.Where(`id = ? AND is_active = ? AND (is_system = ? OR user_id = ?)`,
			req.ProviderID, true, true, userID).First(&provider).Error
		if err != nil {
			c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "Provider tidak ditemukan atau tidak dapat digunakan."})
			return
		}

		encryptedAPIKey, encryptErr := utils.EncryptString(req.APIKey)
		if encryptErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal mengenkripsi API key."})
			return
		}

		if provider.IsSystem {
			baseSlug := strings.TrimSpace(provider.Slug)
			if baseSlug == "" {
				baseSlug = "provider"
			}
			userSlug := baseSlug + "-user-" + strconv.FormatUint(uint64(userID), 10)
			var userProvider models.AIProvider
			findErr := config.DB.Where("user_id = ? AND slug = ? AND is_system = ?", userID, userSlug, false).First(&userProvider).Error
			if findErr != nil && findErr != gorm.ErrRecordNotFound {
				c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal membaca provider milik user."})
				return
			}
			if findErr == gorm.ErrRecordNotFound {
				userProvider = models.AIProvider{
					UserID: &userID, Name: provider.Name, Slug: userSlug,
					BaseURL: provider.BaseURL, Type: provider.Type,
					APIKeyEncrypted: encryptedAPIKey, IsSystem: false, IsActive: true,
				}
				if err := config.DB.Create(&userProvider).Error; err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal membuat provider milik user."})
					return
				}
			} else if err := config.DB.Model(&userProvider).Updates(map[string]interface{}{
				"api_key_encrypted": encryptedAPIKey, "base_url": provider.BaseURL,
				"type": provider.Type, "name": provider.Name, "is_active": true,
			}).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal memperbarui provider milik user."})
				return
			}
			provider = userProvider
		} else {
			if provider.UserID == nil || *provider.UserID != userID {
				c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "Provider bukan milik user ini."})
				return
			}
			if err := config.DB.Model(&provider).Update("api_key_encrypted", encryptedAPIKey).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal menyimpan API key provider."})
				return
			}
		}
	}

	agentID := req.AgentIDs[0]
	var agent models.Agent
	if err := config.DB.Where("id = ? AND user_id = ? AND is_active = ?", agentID, userID, true).First(&agent).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Agent yang dipilih tidak ditemukan atau tidak aktif."})
		return
	}

	var duplicate int64
	err := config.DB.Model(&models.AIModel{}).Where(`provider_id = ? AND model_id = ? AND deleted_at IS NULL`,
		provider.ID, req.ModelID).Count(&duplicate).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal memeriksa model."})
		return
	}
	if duplicate > 0 {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "Model ID tersebut sudah ada pada provider ini."})
		return
	}

	aiModel := models.AIModel{
		ProviderID: provider.ID, UserID: &userID, Name: req.Name,
		ModelID: req.ModelID, Description: req.Description,
		IsSystem: false, IsActive: true,
	}

	tx := config.DB.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal memulai transaksi."})
		return
	}
	if err := tx.Create(&aiModel).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal membuat model."})
		return
	}

	link := models.AgentModel{AgentID: agent.ID, AIModelID: aiModel.ID, IsDefault: false}
	if err := tx.Create(&link).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal menghubungkan model dengan Agent."})
		return
	}
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal menyimpan model."})
		return
	}

	config.DB.Preload("Provider").Preload("Agents").First(&aiModel, aiModel.ID)
	c.JSON(http.StatusCreated, gin.H{"ok": true, "model": aiModel})
}

// ============================================================
// UPDATE AI MODEL
// ============================================================

func UpdateAIModel(c *gin.Context) {
	userID, ok := getCurrentUserID(c)

	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	var aiModel models.AIModel

	err := config.DB.
		Where(
			"id = ? AND user_id = ? AND is_system = ?",
			c.Param("id"),
			userID,
			false,
		).
		First(&aiModel).
		Error

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "Model tidak ditemukan.",
		})
		return
	}

	var req updateAIModelRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Data model tidak valid.",
		})
		return
	}

	// ============================================================
	// UPDATE BASIC DATA
	// ============================================================

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)

		if name == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Nama model tidak boleh kosong.",
			})
			return
		}

		aiModel.Name = name
	}

	if req.ModelID != nil {
		modelID := strings.TrimSpace(*req.ModelID)

		if modelID == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Model ID tidak boleh kosong.",
			})
			return
		}

		// Check duplicate if changed.
		var duplicate int64

		err := config.DB.
			Model(&models.AIModel{}).
			Where(`
				provider_id = ?
				AND model_id = ?
				AND id <> ?
				AND deleted_at IS NULL
			`,
				aiModel.ProviderID,
				modelID,
				aiModel.ID,
			).
			Count(&duplicate).
			Error

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "Gagal memeriksa Model ID.",
			})
			return
		}

		if duplicate > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"ok":    false,
				"error": "Model ID tersebut sudah digunakan.",
			})
			return
		}

		aiModel.ModelID = modelID
	}

	if req.Description != nil {
		aiModel.Description = strings.TrimSpace(
			*req.Description,
		)
	}

	if req.IsActive != nil {
		aiModel.IsActive = *req.IsActive
	}

	// ============================================================
	// TRANSACTION
	// ============================================================

	tx := config.DB.Begin()

	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal memulai transaksi.",
		})
		return
	}

	if err := tx.Save(&aiModel).Error; err != nil {
		tx.Rollback()

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal memperbarui model.",
		})
		return
	}

	// ============================================================
	// UPDATE AGENT RELATIONS
	// ============================================================

	if req.AgentIDs != nil {
		if len(*req.AgentIDs) != 1 || (*req.AgentIDs)[0] == 0 {
			tx.Rollback()
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Model harus terhubung ke tepat satu Agent.",
			})
			return
		}

		var targetAgent models.Agent
		if err := tx.Where("id = ? AND is_active = ?", (*req.AgentIDs)[0], true).First(&targetAgent).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusNotFound, gin.H{
				"ok":    false,
				"error": "Agent yang dipilih tidak ditemukan atau tidak aktif.",
			})
			return
		}

		if err := tx.
			Where(
				"ai_model_id = ?",
				aiModel.ID,
			).
			Delete(&models.AgentModel{}).
			Error; err != nil {

			tx.Rollback()

			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "Gagal memperbarui hubungan agent.",
			})
			return
		}

		link := models.AgentModel{
			AgentID:   targetAgent.ID,
			AIModelID: aiModel.ID,
			IsDefault: false,
		}

		if err := tx.Create(&link).Error; err != nil {
			tx.Rollback()

			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "Gagal menghubungkan model dengan agent.",
			})
			return
		}
	}

	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal menyimpan perubahan model.",
		})
		return
	}

	// ============================================================
	// LOAD UPDATED MODEL
	// ============================================================

	config.DB.
		Preload("Provider").
		Preload("Agents").
		First(&aiModel, aiModel.ID)

	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"model": aiModel,
	})
}

// ============================================================
// REMOVE MODEL FROM ONE AGENT
// ============================================================

func RemoveModelFromAgent(c *gin.Context) {
	userID, ok := getCurrentUserID(c)

	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	var req removeModelFromAgentRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Format request tidak valid.",
		})
		return
	}

	if req.AgentID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Agent ID wajib diisi.",
		})
		return
	}

	// ============================================================
	// GET MODEL
	// ============================================================

	var aiModel models.AIModel

	err := config.DB.
		Where(
			"id = ? AND user_id = ? AND is_system = ?",
			c.Param("id"),
			userID,
			false,
		).
		First(&aiModel).
		Error

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "Model tidak ditemukan.",
		})
		return
	}

	// ============================================================
	// GET AGENT
	// ============================================================

	var agent models.Agent

	err = config.DB.
		Where(
			"id = ? AND is_active = ?",
			req.AgentID,
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

	// ============================================================
	// REMOVE ONLY THIS RELATION
	// ============================================================

	result := config.DB.
		Where(
			"agent_id = ? AND ai_model_id = ?",
			req.AgentID,
			aiModel.ID,
		).
		Delete(&models.AgentModel{})

	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal melepas model dari agent.",
		})
		return
	}

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "Model tersebut tidak terhubung ke agent ini.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Model berhasil dilepas dari agent.",
	})
}

// ============================================================
// DELETE AI MODEL PERMANENTLY
// ============================================================

func DeleteAIModel(c *gin.Context) {
	userID, ok := getCurrentUserID(c)

	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	var aiModel models.AIModel

	err := config.DB.
		Where(
			"id = ? AND user_id = ? AND is_system = ?",
			c.Param("id"),
			userID,
			false,
		).
		First(&aiModel).
		Error

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "Model tidak ditemukan.",
		})
		return
	}

	// ============================================================
	// TRANSACTION
	// ============================================================

	tx := config.DB.Begin()

	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal memulai transaksi.",
		})
		return
	}

	// ============================================================
	// DELETE ALL RELATIONS
	// ============================================================

	if err := tx.
		Where(
			"ai_model_id = ?",
			aiModel.ID,
		).
		Delete(&models.AgentModel{}).
		Error; err != nil {

		tx.Rollback()

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal menghapus hubungan model.",
		})
		return
	}

	// ============================================================
	// DELETE MODEL
	// ============================================================

	if err := tx.Delete(&aiModel).Error; err != nil {
		tx.Rollback()

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal menghapus model.",
		})
		return
	}

	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal menyimpan penghapusan model.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Model berhasil dihapus.",
	})
}
