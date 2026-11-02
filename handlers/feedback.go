package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"github.com/gin-gonic/gin"
)

type createFeedbackRequest struct {
	Category    string `json:"category"`
	Subject     string `json:"subject"`
	Description string `json:"description"`
	RelatedPage string `json:"related_page"`
	Attachment  string `json:"attachment"`
}

func currentFeedbackUserID(c *gin.Context) (uint, bool) {
	value, ok := c.Get("user_id")
	if !ok { return 0, false }
	id, ok := value.(uint)
	return id, ok
}

func CreateFeedback(c *gin.Context) {
	userID, ok := currentFeedbackUserID(c)
	if !ok { c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "User tidak ditemukan"}); return }
	var req createFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil { c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Format JSON tidak valid"}); return }
	req.Category = strings.TrimSpace(req.Category)
	req.Subject = strings.TrimSpace(req.Subject)
	req.Description = strings.TrimSpace(req.Description)
	req.RelatedPage = strings.TrimSpace(req.RelatedPage)
	req.Attachment = strings.TrimSpace(req.Attachment)
	if req.Category == "" || req.Description == "" || req.RelatedPage == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Kategori, deskripsi, dan halaman terkait wajib diisi"}); return
	}
	allowed := map[string]bool{"feature":true,"bug":true,"service":true,"aiExperience":true,"help":true}
	if !allowed[req.Category] { c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Kategori feedback tidak valid"}); return }
	if len([]rune(req.Description)) < 10 || len([]rune(req.Description)) > 5000 || len([]rune(req.Subject)) > 120 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Panjang subject atau deskripsi tidak valid"}); return
	}
	item := models.Feedback{UserID:userID, Category:req.Category, Subject:req.Subject, Description:req.Description, RelatedPage:req.RelatedPage, Attachment:req.Attachment, Status:"new"}
	if err := config.DB.Create(&item).Error; err != nil { c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal menyimpan feedback"}); return }
	c.JSON(http.StatusCreated, gin.H{"ok":true,"feedback":item})
}

func GetMyFeedback(c *gin.Context) {
	userID, ok := currentFeedbackUserID(c)
	if !ok { c.JSON(http.StatusUnauthorized, gin.H{"ok":false,"message":"User tidak ditemukan"}); return }
	var items []models.Feedback
	if err := config.DB.Preload("Replies").Where("user_id = ?", userID).Order("created_at DESC").Find(&items).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok":false,"message":"Gagal mengambil feedback"}); return
	}
	if items == nil { items = []models.Feedback{} }
	c.JSON(http.StatusOK, gin.H{"ok":true,"feedback":items})
}

func RateMyFeedback(c *gin.Context) {
	userID, ok := currentFeedbackUserID(c)
	if !ok { c.JSON(http.StatusUnauthorized, gin.H{"ok":false,"message":"User tidak ditemukan"}); return }
	id, err := strconv.ParseUint(c.Param("id"),10,64)
	if err != nil { c.JSON(http.StatusBadRequest, gin.H{"ok":false,"message":"ID feedback tidak valid"}); return }
	var req struct { Rating int `json:"rating"` }
	if err := c.ShouldBindJSON(&req); err != nil || req.Rating < 0 || req.Rating > 5 {
		c.JSON(http.StatusBadRequest, gin.H{"ok":false,"message":"Rating harus antara 0 sampai 5"}); return
	}
	result := config.DB.Model(&models.Feedback{}).Where("id = ? AND user_id = ?", id, userID).Update("rating", req.Rating)
	if result.Error != nil { c.JSON(http.StatusInternalServerError, gin.H{"ok":false,"message":"Gagal menyimpan rating"}); return }
	if result.RowsAffected == 0 { c.JSON(http.StatusNotFound, gin.H{"ok":false,"message":"Feedback tidak ditemukan"}); return }
	c.JSON(http.StatusOK, gin.H{"ok":true})
}

func AdminGetFeedback(c *gin.Context) {
	var items []models.Feedback
	if err := config.DB.Preload("Replies").Preload("Replies").Joins("JOIN users ON users.id = feedbacks.user_id").
		Select("feedbacks.*").Order("feedbacks.created_at DESC").Find(&items).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok":false,"message":"Gagal mengambil feedback"}); return
	}
	type row struct { models.Feedback; UserName string `json:"user_name"`; UserEmail string `json:"user_email"` }
	rows := make([]row,0,len(items))
	for _, item := range items {
		var user models.User
		if err := config.DB.Select("id","name","email").First(&user,item.UserID).Error; err == nil {
			rows = append(rows,row{Feedback:item,UserName:user.Name,UserEmail:user.Email})
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok":true,"feedback":rows})
}

func AdminUpdateFeedbackStatus(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"),10,64)
	if err != nil { c.JSON(http.StatusBadRequest, gin.H{"ok":false,"message":"ID feedback tidak valid"}); return }
	var req struct { Status string `json:"status"` }
	if err := c.ShouldBindJSON(&req); err != nil { c.JSON(http.StatusBadRequest, gin.H{"ok":false,"message":"Format JSON tidak valid"}); return }
	if req.Status != "new" && req.Status != "processing" && req.Status != "completed" && req.Status != "rejected" {
		c.JSON(http.StatusBadRequest, gin.H{"ok":false,"message":"Status feedback tidak valid"}); return
	}
	result := config.DB.Model(&models.Feedback{}).Where("id = ?",id).Update("status",req.Status)
	if result.Error != nil { c.JSON(http.StatusInternalServerError, gin.H{"ok":false,"message":"Gagal memperbarui status"}); return }
	if result.RowsAffected == 0 { c.JSON(http.StatusNotFound, gin.H{"ok":false,"message":"Feedback tidak ditemukan"}); return }
	c.JSON(http.StatusOK, gin.H{"ok":true})
}

func AdminReplyFeedback(c *gin.Context) {
	adminValue, ok := c.Get("user_id")
	adminID, valid := adminValue.(uint)
	if !ok || !valid { c.JSON(http.StatusUnauthorized, gin.H{"ok":false,"message":"Admin tidak ditemukan"}); return }
	id, err := strconv.ParseUint(c.Param("id"),10,64)
	if err != nil { c.JSON(http.StatusBadRequest, gin.H{"ok":false,"message":"ID feedback tidak valid"}); return }
	var req struct { Message string `json:"message"` }
	if err := c.ShouldBindJSON(&req); err != nil { c.JSON(http.StatusBadRequest, gin.H{"ok":false,"message":"Format JSON tidak valid"}); return }
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" || len([]rune(req.Message)) > 5000 { c.JSON(http.StatusBadRequest, gin.H{"ok":false,"message":"Balasan wajib diisi (maksimal 5000 karakter)"}); return }
	var item models.Feedback
	if err := config.DB.First(&item,id).Error; err != nil { c.JSON(http.StatusNotFound, gin.H{"ok":false,"message":"Feedback tidak ditemukan"}); return }
	reply := models.FeedbackReply{FeedbackID:uint(id),AdminID:adminID,Message:req.Message}
	if err := config.DB.Create(&reply).Error; err != nil { c.JSON(http.StatusInternalServerError, gin.H{"ok":false,"message":"Gagal mengirim balasan"}); return }
	if item.Status == "new" { config.DB.Model(&item).Update("status","processing") }
	c.JSON(http.StatusCreated, gin.H{"ok":true,"reply":reply})
}
