package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/utils"
)

type createProviderRequest struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

type updateProviderRequest struct {
	Name     *string `json:"name"`
	Type     *string `json:"type"`
	BaseURL  *string `json:"base_url"`
	APIKey   *string `json:"api_key"`
	IsActive *bool   `json:"is_active"`
}

type testProviderRequest struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	ModelID string `json:"model_id"`
}

type providerResponse struct {
	ID        uint              `json:"id"`
	Name      string            `json:"name"`
	Slug      string            `json:"slug"`
	Type      string            `json:"type"`
	BaseURL   string            `json:"base_url"`
	IsSystem  bool              `json:"is_system"`
	IsActive  bool              `json:"is_active"`
	HasAPIKey bool              `json:"has_api_key"`
	Models    []models.AIModel `json:"models,omitempty"`
}

func getCurrentUserID(c *gin.Context) (uint, bool) {
	value, exists := c.Get("user_id")
	if !exists {
		return 0, false
	}

	switch id := value.(type) {
	case uint:
		if id == 0 {
			return 0, false
		}
		return id, true

	case int:
		if id <= 0 {
			return 0, false
		}
		return uint(id), true

	case int64:
		if id <= 0 {
			return 0, false
		}
		return uint(id), true

	case float64:
		if id <= 0 {
			return 0, false
		}
		return uint(id), true
	}

	return 0, false
}

func providerToResponse(provider models.AIProvider) providerResponse {
	hasAPIKey := provider.APIKeyEncrypted != ""

	// Provider sistem NaraRouter memakai API key dari environment.
	if provider.IsSystem && provider.Type == "nararouter" {
		hasAPIKey = true
	}

	return providerResponse{
		ID:        provider.ID,
		Name:      provider.Name,
		Slug:      provider.Slug,
		Type:      provider.Type,
		BaseURL:   provider.BaseURL,
		IsSystem:  provider.IsSystem,
		IsActive:  provider.IsActive,
		HasAPIKey: hasAPIKey,
		Models:    provider.Models,
	}
}

func normalizeProviderType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))

	switch value {
	case "openai":
		return "openai"

	case "google":
		return "google"

	case "gemini":
		return "google"

	case "openrouter":
		return "openrouter"

	case "deepseek":
		return "deepseek"

	case "nararouter":
		return "nararouter"

	case "custom":
		return "custom"

	default:
		return "custom"
	}
}

func makeSlug(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))

	var builder strings.Builder
	lastDash := false

	for _, char := range value {
		if (char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') {

			builder.WriteRune(char)
			lastDash = false
			continue
		}

		if !lastDash && builder.Len() > 0 {
			builder.WriteRune('-')
			lastDash = true
		}
	}

	result := strings.Trim(builder.String(), "-")

	if result == "" {
		return "provider"
	}

	return result
}

func validBaseURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	if err != nil {
		return false
	}

	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func normalizeChatEndpoint(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")

	if strings.HasSuffix(baseURL, "/chat/completions") {
		return baseURL
	}

	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/chat/completions"
	}

	if strings.HasSuffix(baseURL, "/v1/") {
		return baseURL + "chat/completions"
	}

	return baseURL + "/chat/completions"
}

func TestAIProviderConnection(c *gin.Context) {
	_, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	var req testProviderRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Data test connection tidak valid.",
		})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Type = normalizeProviderType(req.Type)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.ModelID = strings.TrimSpace(req.ModelID)

	if req.BaseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Base URL wajib diisi.",
		})
		return
	}

	if !validBaseURL(req.BaseURL) {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Base URL harus menggunakan http atau https.",
		})
		return
	}

	if req.APIKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "API key wajib diisi.",
		})
		return
	}

	if req.ModelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Model ID wajib diisi.",
		})
		return
	}

	// Saat ini test connection menggunakan format OpenAI-compatible.
	// Ini cocok untuk OpenAI, OpenRouter, DeepSeek API, NaraRouter,
	// dan provider custom yang menyediakan /chat/completions.
	endpoint := normalizeChatEndpoint(req.BaseURL)

	payload := map[string]interface{}{
		"model": req.ModelID,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": "Reply with exactly: CONNECTION_OK",
			},
		},
		"temperature": 0,
		"max_tokens": 20,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal membuat request test connection.",
		})
		return
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	httpReq, err := http.NewRequest(
		http.MethodPost,
		endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Endpoint provider tidak valid.",
		})
		return
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"ok":    false,
			"error": "Gagal terhubung ke provider: " + err.Error(),
		})
		return
	}

	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"ok":    false,
			"error": "Gagal membaca response provider.",
		})
		return
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errorMessage := strings.TrimSpace(string(responseBody))

		if errorMessage == "" {
			errorMessage = resp.Status
		}

		c.JSON(http.StatusBadGateway, gin.H{
			"ok":          false,
			"error":       "Provider menolak request.",
			"status_code": resp.StatusCode,
			"details":     errorMessage,
		})
		return
	}

	// Cek response agar memastikan server benar-benar memberikan
	// response chat yang valid.
	var responseJSON map[string]interface{}

	if err := json.Unmarshal(responseBody, &responseJSON); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"ok":    false,
			"error": "Provider memberikan response yang bukan JSON valid.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"message":     "Connection berhasil.",
		"provider":    req.Name,
		"provider_type": req.Type,
		"model_id":    req.ModelID,
		"endpoint":    endpoint,
		"status_code": resp.StatusCode,
	})
}

func GetAIProviders(c *gin.Context) {
	userID, ok := getCurrentUserID(c)

	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	var providers []models.AIProvider

	err := config.DB.
		Preload("Models", "is_active = ?", true).
		Where(
			"is_system = ? OR user_id = ?",
			true,
			userID,
		).
		Order("is_system DESC, name ASC").
		Find(&providers).
		Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal mengambil provider AI.",
		})
		return
	}

	responses := make([]providerResponse, 0, len(providers))

	for _, provider := range providers {
		responses = append(
			responses,
			providerToResponse(provider),
		)
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"providers": responses,
	})
}

func CreateAIProvider(c *gin.Context) {
	userID, ok := getCurrentUserID(c)

	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	var req createProviderRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Data provider tidak valid.",
		})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Type = normalizeProviderType(req.Type)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.APIKey = strings.TrimSpace(req.APIKey)

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Nama provider wajib diisi.",
		})
		return
	}

	if req.BaseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Base URL wajib diisi.",
		})
		return
	}

	if !validBaseURL(req.BaseURL) {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Base URL harus menggunakan http atau https.",
		})
		return
	}

	if req.APIKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "API key wajib diisi.",
		})
		return
	}

	encryptedAPIKey, err := utils.EncryptString(req.APIKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal mengenkripsi API key.",
		})
		return
	}

	providerType := normalizeProviderType(req.Type)

	provider := models.AIProvider{
		UserID:          &userID,
		Name:            req.Name,
		Slug:            makeSlug(req.Name),
		BaseURL:         strings.TrimRight(req.BaseURL, "/"),
		APIKeyEncrypted: encryptedAPIKey,
		Type:            providerType,
		IsSystem:        false,
		IsActive:        true,
	}

	if err := config.DB.Create(&provider).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal membuat provider.",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"ok":       true,
		"provider": providerToResponse(provider),
	})
}

func UpdateAIProvider(c *gin.Context) {
	userID, ok := getCurrentUserID(c)

	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	var provider models.AIProvider

	if err := config.DB.
		Where(
			"id = ? AND user_id = ? AND is_system = ?",
			c.Param("id"),
			userID,
			false,
		).
		First(&provider).
		Error; err != nil {

		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "Provider tidak ditemukan.",
		})
		return
	}

	var req updateProviderRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Data provider tidak valid.",
		})
		return
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)

		if name == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Nama provider tidak boleh kosong.",
			})
			return
		}

		provider.Name = name
		provider.Slug = makeSlug(name)
	}

	if req.Type != nil {
		provider.Type = normalizeProviderType(*req.Type)
	}

	if req.BaseURL != nil {
		baseURL := strings.TrimSpace(*req.BaseURL)

		if !validBaseURL(baseURL) {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Base URL harus menggunakan http atau https.",
			})
			return
		}

		provider.BaseURL = strings.TrimRight(baseURL, "/")
	}

	if req.APIKey != nil {
		apiKey := strings.TrimSpace(*req.APIKey)

		if apiKey != "" {
			encryptedAPIKey, err := utils.EncryptString(apiKey)

			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"ok":    false,
					"error": "Gagal mengenkripsi API key.",
				})
				return
			}

			provider.APIKeyEncrypted = encryptedAPIKey
		}
	}

	if req.IsActive != nil {
		provider.IsActive = *req.IsActive
	}

	if err := config.DB.Save(&provider).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal memperbarui provider.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"provider": providerToResponse(provider),
	})
}

func DeleteAIProvider(c *gin.Context) {
	userID, ok := getCurrentUserID(c)

	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	var provider models.AIProvider

	if err := config.DB.
		Where(
			"id = ? AND user_id = ? AND is_system = ?",
			c.Param("id"),
			userID,
			false,
		).
		First(&provider).
		Error; err != nil {

		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "Provider tidak ditemukan.",
		})
		return
	}

	tx := config.DB.Begin()

	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal memulai transaksi database.",
		})
		return
	}

	var modelsList []models.AIModel

	if err := tx.
		Where("provider_id = ?", provider.ID).
		Find(&modelsList).
		Error; err != nil {

		tx.Rollback()

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal membaca model provider.",
		})
		return
	}

	for _, aiModel := range modelsList {
		if err := tx.
			Where("ai_model_id = ?", aiModel.ID).
			Delete(&models.AgentModel{}).
			Error; err != nil {

			tx.Rollback()

			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "Gagal menghapus relasi model.",
			})
			return
		}
	}

	if err := tx.
		Where("provider_id = ?", provider.ID).
		Delete(&models.AIModel{}).
		Error; err != nil {

		tx.Rollback()

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal menghapus model provider.",
		})
		return
	}

	if err := tx.Delete(&provider).Error; err != nil {
		tx.Rollback()

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal menghapus provider.",
		})
		return
	}

	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Gagal menyimpan perubahan database.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Provider berhasil dihapus.",
	})
}

// Dipakai oleh handler lain apabila membutuhkan endpoint generic
// dan supaya compiler tidak menganggap fmt tidak digunakan.
func providerDebugURL(baseURL string) string {
	if strings.TrimSpace(baseURL) == "" {
		return ""
	}

	return fmt.Sprintf("%s", strings.TrimRight(baseURL, "/"))
}