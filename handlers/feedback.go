package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func currentFeedbackUserID(c *gin.Context) (uint, bool) {
	value, ok := c.Get("user_id")
	if !ok { return 0, false }
	id, ok := value.(uint)
	return id, ok
}

func CreateFeedback(c *gin.Context) {
	userID, ok := currentFeedbackUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "User tidak ditemukan"})
		return
	}

	if err := c.Request.ParseMultipartForm(6 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Form upload tidak valid atau terlalu besar"})
		return
	}

	category := strings.TrimSpace(c.PostForm("category"))
	subject := strings.TrimSpace(c.PostForm("subject"))
	description := strings.TrimSpace(c.PostForm("description"))
	relatedPage := strings.TrimSpace(c.PostForm("related_page"))

	if category == "" || description == "" || relatedPage == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Kategori, deskripsi, dan halaman terkait wajib diisi"})
		return
	}
	allowed := map[string]bool{"feature": true, "bug": true, "service": true, "aiExperience": true, "help": true}
	if !allowed[category] {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Kategori feedback tidak valid"})
		return
	}
	if len([]rune(description)) < 10 || len([]rune(description)) > 5000 || len([]rune(subject)) > 120 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Panjang subject atau deskripsi tidak valid"})
		return
	}

	attachmentPath := ""
	file, header, fileErr := c.Request.FormFile("attachment")
	if fileErr == nil {
		defer file.Close()
		if header.Size > 5<<20 {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Ukuran foto maksimal 5 MB"})
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, (5<<20)+1))
		if err != nil || len(data) == 0 || len(data) > 5<<20 {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "File foto tidak valid atau terlalu besar"})
			return
		}
		contentType := http.DetectContentType(data)
		extensions := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}
		ext, allowedType := extensions[contentType]
		if !allowedType {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Format foto harus PNG, JPG, atau WebP"})
			return
		}
		if _, err := mime.ExtensionsByType(contentType); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Tipe foto tidak valid"})
			return
		}
		randomName := make([]byte, 16)
		if _, err := rand.Read(randomName); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal membuat nama file"})
			return
		}
		fileName := hex.EncodeToString(randomName) + ext
		uploadDir := filepath.Join("uploads", "feedback")
		if err := os.MkdirAll(uploadDir, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal menyiapkan penyimpanan foto"})
			return
		}
		destination := filepath.Join(uploadDir, fileName)
		if err := os.WriteFile(destination, data, 0644); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal menyimpan foto"})
			return
		}
		attachmentPath = "/uploads/feedback/" + fileName
	} else if fileErr != http.ErrMissingFile {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Gagal membaca foto lampiran"})
		return
	}

	item := models.Feedback{
		UserID: userID, Category: category, Subject: subject,
		Description: description, RelatedPage: relatedPage,
		Attachment: attachmentPath, Status: "new",
	}
	if err := config.DB.Create(&item).Error; err != nil {
		if attachmentPath != "" {
			_ = os.Remove(filepath.Join("uploads", "feedback", filepath.Base(attachmentPath)))
		}
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal menyimpan feedback"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true, "feedback": item})
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
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "User tidak ditemukan"})
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "ID feedback tidak valid"})
		return
	}

	var req struct {
		Rating        int      `json:"rating"`
		RatingReasons []string `json:"rating_reasons"`
		RatingComment string   `json:"rating_comment"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Format JSON tidak valid"})
		return
	}
	if req.Rating < 1 || req.Rating > 5 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Rating harus antara 1 sampai 5"})
		return
	}

	req.RatingComment = strings.TrimSpace(req.RatingComment)
	if req.RatingComment != "" && (len([]rune(req.RatingComment)) < 10 || len([]rune(req.RatingComment)) > 1000) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Alasan manual harus 10 sampai 1000 karakter"})
		return
	}

	allowedReasons := map[string]bool{
		"admin_ramah": true,
		"pengerjaan_cepat": true,
		"solusi_tepat": true,
		"komunikasi_jelas": true,
		"responsif": true,
		"masalah_terselesaikan": true,
	}
	seen := make(map[string]bool)
	reasons := make([]string, 0, len(req.RatingReasons))
	for _, reason := range req.RatingReasons {
		reason = strings.TrimSpace(reason)
		if !allowedReasons[reason] {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Pilihan alasan rating tidak valid"})
			return
		}
		if !seen[reason] {
			seen[reason] = true
			reasons = append(reasons, reason)
		}
	}

	var item models.Feedback
	if err := config.DB.Where("id = ? AND user_id = ?", id, userID).First(&item).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "message": "Feedback tidak ditemukan"})
		return
	}
	if item.Status != "completed" {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "message": "Feedback hanya dapat diberi rating setelah status selesai"})
		return
	}

	reasonsJSON, err := json.Marshal(reasons)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal memproses alasan rating"})
		return
	}

	if err := config.DB.Model(&item).Updates(map[string]interface{}{
		"rating": req.Rating,
		"rating_reasons": gorm.Expr("?::jsonb", string(reasonsJSON)),
		"rating_comment": req.RatingComment,
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal menyimpan rating"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Rating berhasil disimpan"})
}

// feedbackDeletable reports whether a ticket is finished: only completed or
// rejected tickets can be deleted, so an open report never disappears.
func feedbackDeletable(status string) bool {
	return status == "completed" || status == "rejected"
}

// deleteFeedbackRecord removes the ticket, its replies, and its uploaded image.
// scope narrows the lookup (the owner for users, nothing for admins).
func deleteFeedbackRecord(c *gin.Context, scope func(*gorm.DB) *gorm.DB) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "ID feedback tidak valid"})
		return
	}
	var item models.Feedback
	if err := scope(config.DB.Where("id = ?", id)).First(&item).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "message": "Feedback tidak ditemukan"})
		return
	}
	if !feedbackDeletable(item.Status) {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "message": "Laporan hanya dapat dihapus setelah selesai atau ditolak"})
		return
	}
	err = config.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("feedback_id = ?", item.ID).Delete(&models.FeedbackReply{}).Error; err != nil {
			return err
		}
		return tx.Delete(&item).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal menghapus feedback"})
		return
	}
	if strings.HasPrefix(item.Attachment, "/uploads/feedback/") {
		_ = os.Remove(filepath.Join("uploads", "feedback", filepath.Base(item.Attachment)))
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Laporan berhasil dihapus"})
}

// feedbackAttachmentTypes are the image types CreateFeedback accepts, by the extension it gives them.
var feedbackAttachmentTypes = map[string]string{".jpg": "image/jpeg", ".png": "image/png", ".webp": "image/webp"}

// serveFeedbackAttachment sends a ticket's image to someone allowed to see the ticket.
// Images used to be public under /uploads; they are screenshots of people's screens.
func serveFeedbackAttachment(c *gin.Context, scope func(*gorm.DB) *gorm.DB) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "ID feedback tidak valid"})
		return
	}
	var item models.Feedback
	if err := scope(config.DB.Where("id = ?", id)).First(&item).Error; err != nil || !strings.HasPrefix(item.Attachment, "/uploads/feedback/") {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "message": "Lampiran tidak ditemukan"})
		return
	}
	name := filepath.Base(item.Attachment)
	contentType, ok := feedbackAttachmentTypes[strings.ToLower(filepath.Ext(name))]
	path := filepath.Join("uploads", "feedback", name)
	if _, statErr := os.Stat(path); !ok || statErr != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "message": "Lampiran tidak ditemukan"})
		return
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", "inline")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, max-age=3600")
	c.File(path)
}

// GetMyFeedbackAttachment sends the image of one of the signed-in user's own tickets.
func GetMyFeedbackAttachment(c *gin.Context) {
	userID, ok := currentFeedbackUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "User tidak ditemukan"})
		return
	}
	serveFeedbackAttachment(c, func(q *gorm.DB) *gorm.DB { return q.Where("user_id = ?", userID) })
}

// AdminGetFeedbackAttachment sends the image of any ticket, for the admin console.
func AdminGetFeedbackAttachment(c *gin.Context) {
	serveFeedbackAttachment(c, func(q *gorm.DB) *gorm.DB { return q })
}

func DeleteMyFeedback(c *gin.Context) {
	userID, ok := currentFeedbackUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "User tidak ditemukan"})
		return
	}
	deleteFeedbackRecord(c, func(q *gorm.DB) *gorm.DB { return q.Where("user_id = ?", userID) })
}

func AdminDeleteFeedback(c *gin.Context) {
	deleteFeedbackRecord(c, func(q *gorm.DB) *gorm.DB { return q })
	if c.Writer.Status() == http.StatusOK {
		id := c.Param("id")
		recordAudit(c, "feedback.deleted_by_admin", "feedback", id, "Feedback #"+id, "Feedback #"+id+" dihapus oleh administrator.")
	}
}

func AdminGetFeedback(c *gin.Context) {
	var items []models.Feedback
	if err := config.DB.Preload("Replies").Joins("JOIN users ON users.id = feedbacks.user_id").
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
	idText := strconv.FormatUint(id, 10)
	recordAudit(c, "feedback.status_changed", "feedback", idText, "Feedback #"+idText, "Status feedback #"+idText+" diubah menjadi "+req.Status+" oleh administrator.")
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
	recordAudit(c, "feedback.replied", "feedback", strconv.FormatUint(uint64(item.ID), 10), "Feedback #"+strconv.FormatUint(uint64(item.ID), 10), "Administrator membalas feedback #"+strconv.FormatUint(uint64(item.ID), 10)+".")
	c.JSON(http.StatusCreated, gin.H{"ok":true,"reply":reply})
}
