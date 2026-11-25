package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// Top-up: a person pays by hand (a bank transfer or an e-wallet, as an admin set in
// TopUpSettings), sends a screenshot of the payment, and an admin approves it; only
// then are the credits added. The screenshot is never public.
const (
	topUpProofDir        = "uploads/topups"
	maxTopUpProofBytes   = 3 << 20
	maxPendingTopUps     = 3
	maxTopUpPackages     = 8
	maxTopUpCredits      = 10_000
	maxTopUpPaymentInfo  = 2000
	maxTopUpNote         = 300
	topUpRequestListSize = 50
)

var topUpProofTypes = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp"}

// topUpView is what a person sees of the settings.
type topUpView struct {
	Available      bool   `json:"available"`
	PricePerCredit int    `json:"price_per_credit"`
	Packages       []int  `json:"packages"`
	PaymentInfo    string `json:"payment_info"`
}

func loadTopUpSettings() models.TopUpSettings {
	var settings models.TopUpSettings
	config.DB.Order("id ASC").First(&settings)
	return settings
}

// parsePackages reads "10, 25,50" into sorted, unique, sensible numbers.
func parsePackages(raw string) []int {
	seen := map[int]bool{}
	list := []int{}
	for _, part := range strings.Split(raw, ",") {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value <= 0 || value > maxTopUpCredits || seen[value] {
			continue
		}
		seen[value] = true
		list = append(list, value)
	}
	sort.Ints(list)
	if len(list) > maxTopUpPackages {
		list = list[:maxTopUpPackages]
	}
	return list
}

func viewTopUp(settings models.TopUpSettings) topUpView {
	packages := parsePackages(settings.Packages)
	info := strings.TrimSpace(settings.PaymentInfo)
	view := topUpView{PricePerCredit: settings.PricePerCredit, Packages: packages, PaymentInfo: info}
	view.Available = settings.Enabled && settings.PricePerCredit > 0 && len(packages) > 0 && info != ""
	if !view.Available {
		view.PaymentInfo, view.Packages = "", []int{}
	}
	return view
}

// GetTopUp answers GET /api/topup: how to buy credits, and the person's requests.
func GetTopUp(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "Unauthorized"})
		return
	}
	var requests []models.TopUpRequest
	config.DB.Where("user_id = ?", userID).Order("id DESC").Limit(topUpRequestListSize).Find(&requests)
	var user models.User
	config.DB.Select("id", "email", "created_at", "promo_eligible").First(&user, userID)
	percent, until := discountFor(user, time.Now())
	discount := gin.H{"percent": percent, "until": nil}
	if percent > 0 {
		discount["until"] = until
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "topup": viewTopUp(loadTopUpSettings()), "requests": requests, "discount": discount})
}

// CreateTopUp answers POST /api/topups (multipart: credits, proof): a request for one of
// the packages, with the screenshot of the payment. It waits for an admin.
func CreateTopUp(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "Unauthorized"})
		return
	}
	view := viewTopUp(loadTopUpSettings())
	if !view.Available {
		siteError(c, http.StatusConflict, "topup_unavailable", "Top up kredit belum tersedia.")
		return
	}
	credits, err := strconv.Atoi(strings.TrimSpace(c.PostForm("credits")))
	valid := false
	for _, choice := range view.Packages {
		valid = valid || (err == nil && credits == choice)
	}
	if !valid {
		siteError(c, http.StatusBadRequest, "topup_package", "Pilih salah satu paket kredit.")
		return
	}
	var pending int64
	config.DB.Model(&models.TopUpRequest{}).Where("user_id = ? AND status = ?", userID, models.TopUpPending).Count(&pending)
	if pending >= maxPendingTopUps {
		siteError(c, http.StatusTooManyRequests, "topup_pending", "Tunggu sampai permintaan top up sebelumnya dicek.")
		return
	}
	header, err := c.FormFile("proof")
	if err != nil || header.Size <= 0 || header.Size > maxTopUpProofBytes {
		siteError(c, http.StatusBadRequest, "topup_proof", "Unggah screenshot bukti bayar sampai 3 MB.")
		return
	}
	file, err := header.Open()
	if err != nil {
		siteError(c, http.StatusBadRequest, "topup_proof", "Bukti bayar tidak dapat dibaca.")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxTopUpProofBytes+1))
	extension := topUpProofTypes[http.DetectContentType(data)]
	if err != nil || len(data) > maxTopUpProofBytes || extension == "" {
		siteError(c, http.StatusBadRequest, "topup_proof", "Bukti bayar harus gambar PNG, JPG atau WEBP sampai 3 MB.")
		return
	}
	if err := os.MkdirAll(topUpProofDir, 0o750); err != nil {
		siteError(c, http.StatusInternalServerError, "storage", "Bukti bayar gagal disimpan.")
		return
	}
	random := make([]byte, 16)
	_, _ = rand.Read(random)
	name := hex.EncodeToString(random) + extension
	if err := os.WriteFile(filepath.Join(topUpProofDir, name), data, 0o640); err != nil {
		siteError(c, http.StatusInternalServerError, "storage", "Bukti bayar gagal disimpan.")
		return
	}
	var user models.User
	config.DB.Select("id", "email", "created_at", "promo_eligible").First(&user, userID)
	percent, _ := discountFor(user, time.Now())
	request := models.TopUpRequest{UserID: userID, Credits: credits, Price: discounted(credits*view.PricePerCredit, percent), DiscountPercent: percent, Status: models.TopUpPending, ProofPath: name}
	if err := config.DB.Create(&request).Error; err != nil {
		_ = os.Remove(filepath.Join(topUpProofDir, name))
		siteError(c, http.StatusInternalServerError, "storage", "Permintaan top up gagal disimpan.")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true, "request": request})
}

// serveTopUpProof sends the screenshot of a request someone may see.
func serveTopUpProof(c *gin.Context, scope func(*gorm.DB) *gorm.DB) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	var request models.TopUpRequest
	if err != nil || scope(config.DB.Where("id = ?", id)).First(&request).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "message": "Bukti bayar tidak ditemukan."})
		return
	}
	name := filepath.Base(request.ProofPath)
	path := filepath.Join(topUpProofDir, name)
	var contentType string
	for kind, extension := range topUpProofTypes {
		if strings.EqualFold(filepath.Ext(name), extension) {
			contentType = kind
		}
	}
	if _, statErr := os.Stat(path); contentType == "" || statErr != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "message": "Bukti bayar tidak ditemukan."})
		return
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", "inline")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, max-age=3600")
	c.File(path)
}

// GetMyTopUpProof answers GET /api/topups/:id/proof for the person who sent it.
func GetMyTopUpProof(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "Unauthorized"})
		return
	}
	serveTopUpProof(c, func(q *gorm.DB) *gorm.DB { return q.Where("user_id = ?", userID) })
}

// AdminGetTopUpProof answers GET /api/admin/topups/:id/proof.
func AdminGetTopUpProof(c *gin.Context) {
	serveTopUpProof(c, func(q *gorm.DB) *gorm.DB { return q })
}

// AdminListTopUps answers GET /api/admin/topups?status=pending|approved|rejected|all,
// newest first, with the person's name and e-mail.
func AdminListTopUps(c *gin.Context) {
	query := config.DB.Model(&models.TopUpRequest{})
	switch status := c.DefaultQuery("status", models.TopUpPending); status {
	case models.TopUpPending, models.TopUpApproved, models.TopUpRejected:
		query = query.Where("status = ?", status)
	}
	var requests []models.TopUpRequest
	query.Order("id DESC").Limit(200).Find(&requests)
	ids := make([]uint, 0, len(requests))
	for _, request := range requests {
		ids = append(ids, request.UserID)
	}
	var users []models.User
	if len(ids) > 0 {
		config.DB.Unscoped().Select("id", "name", "email", "credits").Where("id IN ?", ids).Find(&users)
	}
	people := map[uint]models.User{}
	for _, user := range users {
		people[user.ID] = user
	}
	list := make([]gin.H, 0, len(requests))
	for _, request := range requests {
		person := people[request.UserID]
		list = append(list, gin.H{"request": request, "name": person.Name, "email": person.Email, "credits_now": person.Credits})
	}
	var pending int64
	config.DB.Model(&models.TopUpRequest{}).Where("status = ?", models.TopUpPending).Count(&pending)
	c.JSON(http.StatusOK, gin.H{"ok": true, "requests": list, "pending": pending})
}

var errTopUpReviewed = errors.New("already reviewed")

// AdminReviewTopUp answers PATCH /api/admin/topups/:id {"action": "approve"|"reject",
// "note": "..."}: approving adds the credits once; rejecting needs a reason the person
// sees. Recorded in the credit history and the audit log.
func AdminReviewTopUp(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	var req struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if err != nil || c.ShouldBindJSON(&req) != nil || (req.Action != "approve" && req.Action != "reject") {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Kirim action approve atau reject."})
		return
	}
	note := strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(note) > maxTopUpNote {
		note = string([]rune(note)[:maxTopUpNote])
	}
	if req.Action == "reject" && note == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Tulis alasan penolakan; orangnya akan membacanya."})
		return
	}
	adminID, _ := getCurrentUserID(c)
	now := time.Now()
	var request models.TopUpRequest
	var user models.User
	err = config.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&request, id).Error; err != nil {
			return err
		}
		if request.Status != models.TopUpPending {
			return errTopUpReviewed
		}
		updates := map[string]any{"status": models.TopUpRejected, "admin_note": note, "reviewed_at": now}
		if adminID != 0 {
			updates["reviewed_by"] = adminID
		}
		if req.Action == "approve" {
			updates["status"] = models.TopUpApproved
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "email", "credits").First(&user, request.UserID).Error; err != nil {
				return err
			}
			balance := user.Credits + request.Credits
			if err := tx.Model(&models.User{}).Where("id = ?", user.ID).UpdateColumn("credits", balance).Error; err != nil {
				return err
			}
			entry := models.CreditEntry{UserID: user.ID, Amount: request.Credits, Balance: balance, Reason: models.CreditReasonTopUp, Note: "Top up #" + strconv.FormatUint(uint64(request.ID), 10)}
			if adminID != 0 {
				entry.AdminID = &adminID
			}
			if err := tx.Create(&entry).Error; err != nil {
				return err
			}
		} else {
			tx.Select("id", "email").First(&user, request.UserID)
		}
		return tx.Model(&request).Updates(updates).Error
	})
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Permintaan top up tidak ditemukan."})
		return
	case errors.Is(err, errTopUpReviewed):
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "Permintaan ini sudah dicek."})
		return
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Permintaan top up gagal disimpan."})
		return
	}
	detail := "Top up #" + strconv.FormatUint(uint64(request.ID), 10) + " ditolak: " + note
	if req.Action == "approve" {
		detail = "Top up #" + strconv.FormatUint(uint64(request.ID), 10) + " disetujui: " + strconv.Itoa(request.Credits) + " kredit."
	}
	recordAudit(c, "user.topup", "user", strconv.FormatUint(uint64(request.UserID), 10), user.Email, detail)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// AdminGetTopUpSettings answers GET /api/admin/topup-settings.
func AdminGetTopUpSettings(c *gin.Context) {
	settings := loadTopUpSettings()
	c.JSON(http.StatusOK, gin.H{"ok": true, "settings": settings, "available": viewTopUp(settings).Available})
}

// AdminSaveTopUpSettings answers PUT /api/admin/topup-settings.
func AdminSaveTopUpSettings(c *gin.Context) {
	var req struct {
		Enabled        bool   `json:"enabled"`
		PricePerCredit int    `json:"price_per_credit"`
		Packages       string `json:"packages"`
		PaymentInfo    string `json:"payment_info"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.PricePerCredit < 0 || req.PricePerCredit > 100_000_000 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Isi harga per kredit (Rupiah), paket, dan cara bayar."})
		return
	}
	info := strings.TrimSpace(req.PaymentInfo)
	if utf8.RuneCountInString(info) > maxTopUpPaymentInfo {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Cara bayar terlalu panjang."})
		return
	}
	packages := parsePackages(req.Packages)
	text := make([]string, 0, len(packages))
	for _, value := range packages {
		text = append(text, strconv.Itoa(value))
	}
	settings := loadTopUpSettings()
	settings.Enabled, settings.PricePerCredit, settings.Packages, settings.PaymentInfo = req.Enabled, req.PricePerCredit, strings.Join(text, ","), info
	if err := config.DB.Save(&settings).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Pengaturan gagal disimpan."})
		return
	}
	recordAudit(c, "topup.settings", "topup", "1", "", "Pengaturan top up diubah.")
	c.JSON(http.StatusOK, gin.H{"ok": true, "settings": settings, "available": viewTopUp(settings).Available})
}
