package handlers

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

const (
	creditHistoryLimit = 50
	maxCreditTopUp     = 10_000
	creditNoteLimit    = 200
)

// ChargeRun takes the credits of a run that succeeded, once: a run is charged at most
// one time (CreditEntry.AgentRunID is unique), and an owner's unlimited account is
// never charged. The agent worker calls it when a run is completed.
func ChargeRun(run models.AgentRun) {
	if run.ID == 0 || isUnlimitedUser(run.UserID) {
		return
	}
	cost := runCredits(run.Kind)
	reason := models.CreditReasonRevise
	if run.Kind == models.AgentRunCreate {
		reason = models.CreditReasonCreate
	}
	err := config.DB.Transaction(func(tx *gorm.DB) error {
		var charged int64
		tx.Model(&models.CreditEntry{}).Where("agent_run_id = ?", run.ID).Count(&charged)
		if charged > 0 {
			return nil
		}
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "credits").First(&user, run.UserID).Error; err != nil {
			return err
		}
		balance := user.Credits - cost
		if err := tx.Model(&models.User{}).Where("id = ?", user.ID).UpdateColumn("credits", balance).Error; err != nil {
			return err
		}
		runID, siteID := run.ID, run.SiteID
		return tx.Create(&models.CreditEntry{UserID: user.ID, Amount: -cost, Balance: balance, Reason: reason, SiteID: &siteID, AgentRunID: &runID}).Error
	})
	if err != nil {
		log.Printf("[CREDITS] run %d: not charged: %v", run.ID, err)
	}
}

type creditEntryView struct {
	models.CreditEntry
	SiteName string `json:"site_name"`
}

// GetMyCredits answers GET /api/me/credits: the balance, what one website and one
// change use, and the latest changes of the balance.
func GetMyCredits(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "Unauthorized"})
		return
	}
	var entries []models.CreditEntry
	config.DB.Where("user_id = ?", userID).Order("id DESC").Limit(creditHistoryLimit).Find(&entries)
	names := map[uint]string{}
	ids := make([]uint, 0, len(entries))
	for _, entry := range entries {
		if entry.SiteID != nil {
			ids = append(ids, *entry.SiteID)
		}
	}
	if len(ids) > 0 {
		var sites []models.Site
		config.DB.Select("id", "name").Where("id IN ?", ids).Find(&sites)
		for _, site := range sites {
			names[site.ID] = site.Name
		}
	}
	history := make([]creditEntryView, 0, len(entries))
	for _, entry := range entries {
		view := creditEntryView{CreditEntry: entry}
		if entry.SiteID != nil {
			view.SiteName = names[*entry.SiteID]
		}
		history = append(history, view)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "plan": builderPlanFor(userID), "history": history, "welcome": welcomeCredits})
}

// AdminChangeCredits answers PATCH /api/admin/users/:id/credits: an admin adds credits
// (a top-up) or takes some away. Body: {"amount": 20, "note": "Transfer 24 Nov"}.
// The balance never goes below zero. Recorded in the credit history and the audit log.
func AdminChangeCredits(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "ID user tidak valid."})
		return
	}
	var req struct {
		Amount *int   `json:"amount"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Amount == nil || *req.Amount == 0 || *req.Amount > maxCreditTopUp || *req.Amount < -maxCreditTopUp {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Kirim amount: jumlah kredit yang ditambah (positif) atau dikurangi (negatif), selain 0."})
		return
	}
	note := strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(note) > creditNoteLimit {
		note = string([]rune(note)[:creditNoteLimit])
	}
	adminID, _ := getCurrentUserID(c)
	var user models.User
	var balance int
	err = config.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "email", "credits").First(&user, uint(id)).Error; err != nil {
			return err
		}
		balance = max(user.Credits+*req.Amount, 0)
		if err := tx.Model(&models.User{}).Where("id = ?", user.ID).UpdateColumn("credits", balance).Error; err != nil {
			return err
		}
		entry := models.CreditEntry{UserID: user.ID, Amount: balance - user.Credits, Balance: balance, Reason: models.CreditReasonAdmin, Note: note}
		if adminID != 0 {
			entry.AdminID = &adminID
		}
		return tx.Create(&entry).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Kredit gagal disimpan."})
		return
	}
	detail := "Kredit diubah " + strconv.Itoa(balance-user.Credits) + " menjadi " + strconv.Itoa(balance) + " oleh administrator."
	if note != "" {
		detail += " Catatan: " + note
	}
	recordAudit(c, "user.credits", "user", strconv.FormatUint(id, 10), user.Email, detail)
	c.JSON(http.StatusOK, gin.H{"ok": true, "credits": balance})
}
