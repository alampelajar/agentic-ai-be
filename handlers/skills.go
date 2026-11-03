package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

type skillResponse struct {
	ID                   uint        `json:"id"`
	Slug                 string      `json:"slug"`
	Name                 string      `json:"name"`
	Description          string      `json:"description"`
	Author               string      `json:"author"`
	Source               string      `json:"source"`
	Status               string      `json:"status"`
	IsEnabled            bool        `json:"is_enabled"`
	CurrentVersion       string      `json:"current_version,omitempty"`
	ValidationStatus     string      `json:"validation_status,omitempty"`
	ValidationError      string      `json:"validation_error,omitempty"`
	Tools                []string    `json:"tools"`
	RequestedPermissions []string    `json:"requested_permissions"`
	CreatedAt            interface{} `json:"created_at"`
	UpdatedAt            interface{} `json:"updated_at"`
}

func skillFromModel(skill models.Skill) skillResponse {
	result := skillResponse{ID: skill.ID, Slug: skill.Slug, Name: skill.Name, Description: skill.Description, Author: skill.Author, Source: skill.Source, Status: skill.Status, IsEnabled: skill.IsEnabled, CreatedAt: skill.CreatedAt, UpdatedAt: skill.UpdatedAt, Tools: []string{}, RequestedPermissions: []string{}}
	if len(skill.Versions) == 0 {
		return result
	}
	version := skill.Versions[0]
	result.CurrentVersion = version.Version
	result.ValidationStatus = version.ValidationStatus
	result.ValidationError = version.ValidationError
	_ = json.Unmarshal([]byte(version.RequestedTools), &result.Tools)
	_ = json.Unmarshal([]byte(version.RequestedPermissions), &result.RequestedPermissions)
	return result
}

func currentSkillUser(c *gin.Context) (uint, bool) {
	id := c.GetUint("user_id")
	return id, id != 0
}

func skillManifestJSON(manifest models.SkillManifest) (string, error) {
	data, err := json.Marshal(manifest)
	return string(data), err
}

func createSkillFromPackage(userID uint, pkg services.ValidatedSkillPackage, source, sourceURL string) (models.Skill, error) {
	manifestJSON, err := skillManifestJSON(pkg.Manifest)
	if err != nil {
		return models.Skill{}, err
	}
	toolsJSON, err := json.Marshal(pkg.Manifest.Tools)
	if err != nil {
		return models.Skill{}, err
	}
	permissionsJSON, err := json.Marshal(pkg.Manifest.RequestedPermissions)
	if err != nil {
		return models.Skill{}, err
	}
	var existing models.Skill
	if err := config.DB.Where("user_id = ? AND slug = ?", userID, pkg.Manifest.ID).First(&existing).Error; err == nil {
		return models.Skill{}, fmt.Errorf("skill slug %q already exists", pkg.Manifest.ID)
	} else if err != gorm.ErrRecordNotFound {
		return models.Skill{}, err
	}
	skill := models.Skill{UserID: userID, Slug: pkg.Manifest.ID, Name: pkg.Manifest.Name, Description: pkg.Manifest.Description, Author: pkg.Manifest.Author, Source: source, Status: "installed", IsEnabled: false}
	skill.Versions = []models.SkillVersion{{Version: pkg.Manifest.Version, Manifest: manifestJSON, Instructions: pkg.Instructions, RequestedTools: string(toolsJSON), RequestedPermissions: string(permissionsJSON), PackageSHA256: pkg.SHA256, SourceURL: sourceURL, ValidationStatus: "valid"}}
	if err := config.DB.Create(&skill).Error; err != nil {
		return models.Skill{}, err
	}
	return skill, nil
}

func GetSkills(c *gin.Context) {
	userID, ok := currentSkillUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	query := strings.TrimSpace(c.Query("q"))
	status := strings.TrimSpace(c.Query("status"))
	db := config.DB.Where("user_id = ?", userID)
	if query != "" {
		like := "%" + query + "%"
		db = db.Where("name ILIKE ? OR slug ILIKE ? OR description ILIKE ?", like, like, like)
	}
	if status != "" {
		db = db.Where("status = ?", status)
	}
	var skills []models.Skill
	if err := db.Preload("Versions", func(tx *gorm.DB) *gorm.DB { return tx.Order("created_at DESC, id DESC") }).Order("updated_at DESC").Find(&skills).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal mengambil skill."})
		return
	}
	items := make([]skillResponse, 0, len(skills))
	for _, skill := range skills {
		items = append(items, skillFromModel(skill))
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "skills": items})
}

func GetSkill(c *gin.Context) {
	userID, ok := currentSkillUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	var skill models.Skill
	if err := config.DB.Where("id = ? AND user_id = ?", c.Param("id"), userID).Preload("Versions", func(tx *gorm.DB) *gorm.DB { return tx.Order("created_at DESC").Limit(1) }).First(&skill).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Skill tidak ditemukan."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "skill": skillFromModel(skill)})
}

func CreateSkill(c *gin.Context) {
	userID, ok := currentSkillUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	var req models.SkillCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Manifest skill tidak valid."})
		return
	}
	if strings.TrimSpace(req.Instructions) == "" {
		req.Instructions = req.Manifest.Instructions
	}
	if err := services.ValidateSkillManifest(req.Manifest, req.Instructions); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	pkg := services.ValidatedSkillPackage{Manifest: req.Manifest, Instructions: req.Instructions, SHA256: "created-in-editor"}
	skill, err := createSkillFromPackage(userID, pkg, "created", "")
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true, "skill": skillFromModel(skill)})
}

func ImportSkillZIP(c *gin.Context) {
	userID, ok := currentSkillUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "File ZIP wajib diunggah pada field file."})
		return
	}
	defer file.Close()
	if header.Size > services.MaxSkillArchiveBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"ok": false, "error": "Ukuran ZIP maksimal 10 MiB."})
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, services.MaxSkillArchiveBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "ZIP tidak dapat dibaca."})
		return
	}
	if len(data) > services.MaxSkillArchiveBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"ok": false, "error": "Ukuran ZIP maksimal 10 MiB."})
		return
	}
	pkg, err := services.ValidateSkillArchive(data)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	skill, err := createSkillFromPackage(userID, pkg, "zip", header.Filename)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true, "skill": skillFromModel(skill)})
}

func ImportSkillGitHub(c *gin.Context) {
	userID, ok := currentSkillUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	var req struct {
		URL string `json:"url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "URL GitHub wajib diisi."})
		return
	}
	data, sourceURL, err := services.DownloadPublicGitHubArchive(req.URL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	pkg, err := services.ValidateSkillArchive(data)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	skill, err := createSkillFromPackage(userID, pkg, "github", sourceURL)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true, "skill": skillFromModel(skill)})
}

func UpdateSkill(c *gin.Context) {
	userID, ok := currentSkillUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	var skill models.Skill
	if err := config.DB.Where("id = ? AND user_id = ?", c.Param("id"), userID).First(&skill).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Skill tidak ditemukan."})
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		IsEnabled   *bool   `json:"is_enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Format update tidak valid."})
		return
	}
	updates := map[string]interface{}{}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Nama skill tidak boleh kosong."})
			return
		}
		updates["name"] = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.IsEnabled != nil {
		updates["is_enabled"] = *req.IsEnabled
		if *req.IsEnabled {
			updates["status"] = "enabled"
		} else {
			updates["status"] = "disabled"
		}
	}
	if len(updates) > 0 && config.DB.Model(&skill).Updates(updates).Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal memperbarui skill."})
		return
	}
	config.DB.Preload("Versions", func(tx *gorm.DB) *gorm.DB { return tx.Order("created_at DESC").Limit(1) }).First(&skill, skill.ID)
	c.JSON(http.StatusOK, gin.H{"ok": true, "skill": skillFromModel(skill)})
}

func DeleteSkill(c *gin.Context) {
	userID, ok := currentSkillUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	result := config.DB.Where("id = ? AND user_id = ?", c.Param("id"), userID).Delete(&models.Skill{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal menghapus skill."})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Skill tidak ditemukan."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func ValidateSkill(c *gin.Context) {
	userID, ok := currentSkillUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	var skill models.Skill
	if err := config.DB.Where("id = ? AND user_id = ?", c.Param("id"), userID).Preload("Versions", func(tx *gorm.DB) *gorm.DB { return tx.Order("created_at DESC").Limit(1) }).First(&skill).Error; err != nil || len(skill.Versions) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Skill atau versi tidak ditemukan."})
		return
	}
	version := skill.Versions[0]
	var manifest models.SkillManifest
	if err := json.Unmarshal([]byte(version.Manifest), &manifest); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": true, "valid": false, "error": "Manifest tersimpan tidak valid."})
		return
	}
	err := services.ValidateSkillManifest(manifest, version.Instructions)
	updates := map[string]interface{}{"validation_status": "valid", "validation_error": ""}
	if err != nil {
		updates["validation_status"] = "error"
		updates["validation_error"] = err.Error()
	}
	config.DB.Model(&version).Updates(updates)
	c.JSON(http.StatusOK, gin.H{"ok": true, "valid": err == nil, "error": errorText(err)})
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
