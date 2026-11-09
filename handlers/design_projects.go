package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// Design Studio projects. Every query is scoped by the user_id from the session.

const defaultDesignProjectName = "Untitled design"

type designProjectSummary struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Revision  int       `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type designProjectPayload struct {
	Name     *string         `json:"name"`
	Document json.RawMessage `json:"document"`
	Revision *int            `json:"revision"`
}

func designProjectError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"ok": false, "message": message})
}

func designProjectName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if name == "" || len([]rune(name)) > models.DesignProjectMaxNameLength {
		return "", false
	}
	return name, true
}

// designDocumentValid accepts a JSON object within the size limit.
func designDocumentValid(document json.RawMessage) bool {
	trimmed := bytes.TrimSpace(document)
	return len(trimmed) > 1 && len(trimmed) <= models.DesignProjectMaxDocumentLen && trimmed[0] == '{' && json.Valid(trimmed)
}

func designProjectResponse(project models.DesignProject) gin.H {
	return gin.H{
		"id":         project.ID,
		"name":       project.Name,
		"revision":   project.Revision,
		"document":   json.RawMessage(project.Document),
		"created_at": project.CreatedAt,
		"updated_at": project.UpdatedAt,
	}
}

func GetDesignProjects(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		designProjectError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	projects := []designProjectSummary{}
	if err := config.DB.Model(&models.DesignProject{}).
		Select("id", "name", "revision", "created_at", "updated_at").
		Where("user_id = ?", userID).
		Order("updated_at DESC, id DESC").
		Find(&projects).Error; err != nil {
		designProjectError(c, http.StatusInternalServerError, "Gagal mengambil project desain.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "projects": projects})
}

func CreateDesignProject(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		designProjectError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	var req designProjectPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		designProjectError(c, http.StatusBadRequest, "Format JSON tidak valid atau desain terlalu besar.")
		return
	}
	name := defaultDesignProjectName
	if req.Name != nil {
		valid, nameOK := designProjectName(*req.Name)
		if !nameOK {
			designProjectError(c, http.StatusBadRequest, "Nama project wajib diisi (maksimal 120 karakter).")
			return
		}
		name = valid
	}
	document := json.RawMessage("{}")
	if len(req.Document) > 0 {
		if !designDocumentValid(req.Document) {
			designProjectError(c, http.StatusBadRequest, "Dokumen desain tidak valid atau terlalu besar.")
			return
		}
		document = req.Document
	}
	var count int64
	if err := config.DB.Model(&models.DesignProject{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		designProjectError(c, http.StatusInternalServerError, "Gagal membuat project desain.")
		return
	}
	if count >= models.DesignProjectMaxPerUser {
		designProjectError(c, http.StatusConflict, "Jumlah project desain sudah mencapai batas. Hapus project yang tidak dipakai.")
		return
	}
	project := models.DesignProject{UserID: userID, Name: name, Document: string(document), Revision: 1}
	if err := config.DB.Create(&project).Error; err != nil {
		designProjectError(c, http.StatusInternalServerError, "Gagal membuat project desain.")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true, "project": designProjectResponse(project)})
}

func GetDesignProject(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		designProjectError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	id, ok := memoryID(c)
	if !ok {
		designProjectError(c, http.StatusNotFound, "Project desain tidak ditemukan.")
		return
	}
	var project models.DesignProject
	if err := config.DB.Where("id = ? AND user_id = ?", id, userID).First(&project).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			designProjectError(c, http.StatusNotFound, "Project desain tidak ditemukan.")
			return
		}
		designProjectError(c, http.StatusInternalServerError, "Gagal mengambil project desain.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "project": designProjectResponse(project)})
}

// UpdateDesignProject saves a new version. The client sends the revision it
// loaded; a different stored revision means another tab saved first (409).
func UpdateDesignProject(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		designProjectError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	id, ok := memoryID(c)
	if !ok {
		designProjectError(c, http.StatusNotFound, "Project desain tidak ditemukan.")
		return
	}
	var req designProjectPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		designProjectError(c, http.StatusBadRequest, "Format JSON tidak valid atau desain terlalu besar.")
		return
	}
	if req.Revision == nil {
		designProjectError(c, http.StatusBadRequest, "Revision wajib dikirim.")
		return
	}
	updates := map[string]interface{}{"revision": *req.Revision + 1, "updated_at": time.Now()}
	if req.Name != nil {
		name, nameOK := designProjectName(*req.Name)
		if !nameOK {
			designProjectError(c, http.StatusBadRequest, "Nama project wajib diisi (maksimal 120 karakter).")
			return
		}
		updates["name"] = name
	}
	if len(req.Document) > 0 {
		if !designDocumentValid(req.Document) {
			designProjectError(c, http.StatusBadRequest, "Dokumen desain tidak valid atau terlalu besar.")
			return
		}
		updates["document"] = string(req.Document)
	}
	result := config.DB.Model(&models.DesignProject{}).
		Where("id = ? AND user_id = ? AND revision = ?", id, userID, *req.Revision).
		Updates(updates)
	if result.Error != nil {
		designProjectError(c, http.StatusInternalServerError, "Gagal menyimpan project desain.")
		return
	}
	if result.RowsAffected == 0 {
		var current models.DesignProject
		if err := config.DB.Select("id", "revision").Where("id = ? AND user_id = ?", id, userID).First(&current).Error; err != nil {
			designProjectError(c, http.StatusNotFound, "Project desain tidak ditemukan.")
			return
		}
		c.JSON(http.StatusConflict, gin.H{"ok": false, "message": "Project ini sudah diubah di tab atau perangkat lain.", "revision": current.Revision})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "revision": *req.Revision + 1})
}

func DeleteDesignProject(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		designProjectError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	id, ok := memoryID(c)
	if !ok {
		designProjectError(c, http.StatusNotFound, "Project desain tidak ditemukan.")
		return
	}
	result := config.DB.Where("id = ? AND user_id = ?", id, userID).Delete(&models.DesignProject{})
	if result.Error != nil {
		designProjectError(c, http.StatusInternalServerError, "Gagal menghapus project desain.")
		return
	}
	if result.RowsAffected == 0 {
		designProjectError(c, http.StatusNotFound, "Project desain tidak ditemukan.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
