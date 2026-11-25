package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// The new-account promo: the welcome credits and 50% off every top-up in the first
// 30 days. One person should get it once. An account made from the browser another
// account was made from gets neither; so does one made from an address that already
// made two accounts in 30 days (an office, a boarding house or a mobile network often
// share one address, so one or two more accounts from it are allowed). An admin can
// change it in Users.
const (
	newAccountDiscountPercent = 50
	newAccountDiscountDays    = 30
	sharedAddressWindow       = 30 * 24 * time.Hour
	sharedAddressAllowed      = 2 // accounts from one address in the window that still get the promo
	deviceHeader              = "X-Device-ID"
)

var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{16,80}$`)

// signalKey keys the hashes, so a stored hash says nothing without the server's secret.
func signalKey() []byte {
	sum := sha256.Sum256([]byte("signup-signal:" + os.Getenv("JWT_ACCESS_SECRET")))
	return sum[:]
}

func signalHash(value string) string {
	if value == "" {
		return ""
	}
	mac := hmac.New(sha256.New, signalKey())
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

// applySignupPromo stores the sign-up signals of a new account and takes the promo away
// when the same device, or an address used by many accounts, made one before. Call it
// in the transaction that creates the account.
func applySignupPromo(tx *gorm.DB, c *gin.Context, userID uint) error {
	device := strings.TrimSpace(c.GetHeader(deviceHeader))
	if !deviceIDPattern.MatchString(device) {
		device = ""
	}
	deviceHash, addressHash := signalHash(device), signalHash(c.ClientIP())
	updates := map[string]any{"signup_ip": addressHash, "signup_device": deviceHash}
	note := ""
	if deviceHash != "" {
		var same int64
		tx.Unscoped().Model(&models.User{}).Where("id <> ? AND signup_device = ?", userID, deviceHash).Count(&same)
		if same > 0 {
			note = "Perangkat yang sama dengan akun lain."
		}
	}
	if note == "" && addressHash != "" {
		var same int64
		tx.Unscoped().Model(&models.User{}).
			Where("id <> ? AND signup_ip = ? AND created_at > ?", userID, addressHash, time.Now().Add(-sharedAddressWindow)).Count(&same)
		if same >= sharedAddressAllowed {
			note = "Alamat yang sama dengan " + strconv.FormatInt(same, 10) + " akun lain dalam 30 hari."
		}
	}
	if note != "" {
		updates["promo_eligible"], updates["promo_note"], updates["credits"] = false, note, 0
		log.Printf("[PROMO] user %d: no new-account promo (%s)", userID, note)
	}
	return tx.Model(&models.User{}).Where("id = ?", userID).Updates(updates).Error
}

// discountFor is the new-account discount the person gets on a top-up now, in percent.
func discountFor(user models.User, now time.Time) (percent int, until time.Time) {
	until = user.CreatedAt.Add(newAccountDiscountDays * 24 * time.Hour)
	if !user.PromoEligible || !now.Before(until) || unlimitedEmail(user.Email) {
		return 0, time.Time{}
	}
	return newAccountDiscountPercent, until
}

// discounted is the price after the discount, rounded down to whole Rupiah.
func discounted(price, percent int) int {
	return price * (100 - percent) / 100
}

// AdminSetPromo answers PATCH /api/admin/users/:id/promo {"eligible": true}: give back
// or take away the new-account promo (the discount; free credits are added separately
// with "Add credits"). Recorded in the audit log.
func AdminSetPromo(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	var req struct {
		Eligible *bool `json:"eligible"`
	}
	if err != nil || c.ShouldBindJSON(&req) != nil || req.Eligible == nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Kirim eligible true atau false."})
		return
	}
	var user models.User
	if err := config.DB.Select("id", "email").First(&user, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	note := "Promo dimatikan oleh administrator."
	if *req.Eligible {
		note = ""
	}
	if err := config.DB.Model(&user).Updates(map[string]any{"promo_eligible": *req.Eligible, "promo_note": note}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Promo gagal disimpan."})
		return
	}
	detail := "Promo akun baru dimatikan oleh administrator."
	if *req.Eligible {
		detail = "Promo akun baru diberikan oleh administrator."
	}
	recordAudit(c, "user.promo", "user", strconv.FormatUint(id, 10), user.Email, detail)
	c.JSON(http.StatusOK, gin.H{"ok": true, "eligible": *req.Eligible})
}

type usageMonth struct {
	Month         string `json:"month"`
	Tokens        int64  `json:"tokens"`
	ModelCalls    int64  `json:"model_calls"`
	Websites      int64  `json:"websites"`
	Changes       int64  `json:"changes"`
	CreditsUsed   int64  `json:"credits_used"`
	CreditsBought int64  `json:"credits_bought"`
	Revenue       int64  `json:"revenue"`
	NewAccounts   int64  `json:"new_accounts"`
	NoPromo       int64  `json:"no_promo"`
}

// AdminUsage answers GET /api/admin/usage: for each of the last six months (Indonesian
// time), the AI tokens used, the websites and changes paid for, the credits used and
// bought, the money from approved top-ups, and new accounts (and how many got no
// promo). Divide a provider's bill by credits_used to see what one credit costs.
func AdminUsage(c *gin.Context) {
	zone := quotaZone().String()
	months := map[string]*usageMonth{}
	order := make([]string, 0, 6)
	now := time.Now().In(quotaZone())
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, -5, 0)
	for i := 0; i < 6; i++ {
		key := start.AddDate(0, i, 0).Format("2006-01")
		months[key] = &usageMonth{Month: key}
		order = append(order, key)
	}
	type row struct {
		Month string
		A, B  int64
	}
	monthOf := "to_char(created_at AT TIME ZONE '" + zone + "', 'YYYY-MM')"
	collect := func(query *gorm.DB, apply func(*usageMonth, row)) {
		var rows []row
		if err := query.Where("created_at >= ?", start).Group("month").Scan(&rows).Error; err != nil {
			log.Printf("[USAGE] %v", err)
		}
		for _, r := range rows {
			if month, ok := months[r.Month]; ok {
				apply(month, r)
			}
		}
	}
	collect(config.DB.Model(&models.AIModelCallLog{}).Select(monthOf+" AS month, COALESCE(SUM(tokens),0) AS a, COUNT(*) FILTER (WHERE status = 'ok') AS b"),
		func(m *usageMonth, r row) { m.Tokens, m.ModelCalls = r.A, r.B })
	collect(config.DB.Model(&models.CreditEntry{}).Select(monthOf+" AS month, COUNT(*) FILTER (WHERE reason = ?) AS a, COUNT(*) FILTER (WHERE reason = ?) AS b", models.CreditReasonCreate, models.CreditReasonRevise),
		func(m *usageMonth, r row) { m.Websites, m.Changes = r.A, r.B })
	collect(config.DB.Model(&models.CreditEntry{}).Select(monthOf+" AS month, COALESCE(-SUM(amount) FILTER (WHERE reason IN ?),0) AS a, COALESCE(SUM(amount) FILTER (WHERE reason = ?),0) AS b", []string{models.CreditReasonCreate, models.CreditReasonRevise}, models.CreditReasonTopUp),
		func(m *usageMonth, r row) { m.CreditsUsed, m.CreditsBought = r.A, r.B })
	collect(config.DB.Model(&models.TopUpRequest{}).Select(monthOf+" AS month, COALESCE(SUM(price) FILTER (WHERE status = ?),0) AS a, 0 AS b", models.TopUpApproved),
		func(m *usageMonth, r row) { m.Revenue = r.A })
	collect(config.DB.Unscoped().Model(&models.User{}).Select(monthOf+" AS month, COUNT(*) AS a, COUNT(*) FILTER (WHERE NOT promo_eligible) AS b"),
		func(m *usageMonth, r row) { m.NewAccounts, m.NoPromo = r.A, r.B })
	list := make([]usageMonth, 0, len(order))
	for i := len(order) - 1; i >= 0; i-- {
		list = append(list, *months[order[i]])
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "months": list})
}
