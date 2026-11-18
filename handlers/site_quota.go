package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// Website Builder limits per person. Everyone gets a monthly token allowance. A free account
// also gets a small number of websites and the model(s) the admin marked as the builder's
// default; a premium account (User.IsPremium) gets a larger allowance, many websites and
// every active system model.
const (
	freeSiteLimit          = 2
	freeMonthlyTokens      = 200_000
	premiumSiteLimit       = 50 // a safety bound, not a product limit
	premiumMonthlyTokens   = 1_000_000
	builderQuotaZoneTZ     = "Asia/Jakarta"
	builderQuotaEnvName    = "BUILDER_MONTHLY_TOKENS"
	builderPremiumQuotaEnv = "BUILDER_PREMIUM_MONTHLY_TOKENS"
)

// builderPlan is what one person may use of the Website Builder right now.
type builderPlan struct {
	Premium    bool `json:"premium"`
	SiteLimit  int  `json:"site_limit"`
	SitesUsed  int  `json:"sites_used"`
	TokenLimit int  `json:"token_limit"` // 0: no limit
	TokensUsed int  `json:"tokens_used"`
	// ResetsAt is when the monthly allowance starts again.
	ResetsAt time.Time `json:"resets_at"`
	// PremiumUntil is when the premium account ends; nil when it does not end or there is none.
	PremiumUntil *time.Time `json:"premium_until"`
}

// premiumActive says whether a premium account counts now: switched on, and not past its end.
func premiumActive(user models.User, now time.Time) bool {
	return user.IsPremium && (user.PremiumUntil == nil || now.Before(*user.PremiumUntil))
}

// OutOfTokens says whether the monthly allowance is used up.
func (p builderPlan) OutOfTokens() bool {
	return p.TokenLimit > 0 && p.TokensUsed >= p.TokenLimit
}

// AtSiteLimit says whether the person may not make another website.
func (p builderPlan) AtSiteLimit() bool {
	return p.SitesUsed >= p.SiteLimit
}

func quotaZone() *time.Location {
	if zone, err := time.LoadLocation(builderQuotaZoneTZ); err == nil {
		return zone
	}
	return time.FixedZone("WIB", 7*60*60)
}

// monthStart is the first moment of the current month in Indonesian time, when the
// allowance is counted from.
func monthStart(now time.Time) time.Time {
	local := now.In(quotaZone())
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, local.Location())
}

func loadPremium(userID uint) (models.User, bool) {
	var user models.User
	if err := config.DB.Select("id", "is_premium", "premium_until").First(&user, userID).Error; err != nil {
		return user, false
	}
	return user, premiumActive(user, time.Now())
}

func isPremiumUser(userID uint) bool {
	_, active := loadPremium(userID)
	return active
}

// tokensThisMonth adds up the tokens of the person's model calls since the month began.
func tokensThisMonth(userID uint) int {
	var total int64
	config.DB.Model(&models.AIModelCallLog{}).
		Where("user_id = ? AND created_at >= ?", userID, monthStart(time.Now())).
		Select("COALESCE(SUM(tokens), 0)").Scan(&total)
	return int(total)
}

func builderPlanFor(userID uint) builderPlan {
	start := monthStart(time.Now())
	user, premium := loadPremium(userID)
	plan := builderPlan{Premium: premium, ResetsAt: start.AddDate(0, 1, 0)}
	if premium {
		plan.PremiumUntil = user.PremiumUntil
	}
	var sites int64
	config.DB.Model(&models.Site{}).Where("user_id = ?", userID).Count(&sites)
	plan.SitesUsed = int(sites)
	plan.TokensUsed = tokensThisMonth(userID)
	if plan.Premium {
		plan.SiteLimit = premiumSiteLimit
		plan.TokenLimit = envInt(builderPremiumQuotaEnv, premiumMonthlyTokens)
		return plan
	}
	plan.SiteLimit = freeSiteLimit
	plan.TokenLimit = envInt(builderQuotaEnvName, freeMonthlyTokens)
	return plan
}

// AdminSetUserPremium gives a person a premium account, for a time or without end, or takes
// it away. Body: {"is_premium": true, "premium_until": "2026-12-31T23:59:59+07:00"} (null or
// left out: no end), or {"is_premium": false}. Recorded in the audit log.
func AdminSetUserPremium(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "ID user tidak valid."})
		return
	}
	var req struct {
		IsPremium    *bool      `json:"is_premium"`
		PremiumUntil *time.Time `json:"premium_until"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.IsPremium == nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Kirim is_premium true atau false, dan premium_until bila ada batas waktunya."})
		return
	}
	if *req.IsPremium && req.PremiumUntil != nil && !req.PremiumUntil.After(time.Now()) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Masa premium harus berakhir di waktu yang akan datang."})
		return
	}
	var user models.User
	if err := config.DB.First(&user, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	until := req.PremiumUntil
	if !*req.IsPremium {
		until = nil
	}
	if err := config.DB.Model(&user).Updates(map[string]any{"is_premium": *req.IsPremium, "premium_until": until}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal menyimpan status premium."})
		return
	}
	detail := "Akun premium dimatikan oleh administrator."
	if *req.IsPremium {
		detail = "Akun premium diaktifkan oleh administrator, tanpa batas waktu."
		if until != nil {
			detail = "Akun premium diaktifkan oleh administrator sampai " + until.In(quotaZone()).Format("02-01-2006 15:04") + " WIB."
		}
	}
	recordAudit(c, "user.premium", "user", strconv.FormatUint(id, 10), user.Email, detail)
	c.JSON(http.StatusOK, gin.H{"ok": true, "is_premium": *req.IsPremium, "premium_until": until})
}
