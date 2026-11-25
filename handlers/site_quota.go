package handlers

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// Website Builder limits per person: credits. A new account starts with 10 (the
// column default on users.credits); a new website uses 2 and a change uses 1, only when
// the work succeeded. More credits come from an admin (a top-up). The owners' own
// accounts (BUILDER_UNLIMITED_EMAILS) use none. A premium account (User.IsPremium) still
// lets the builder use every active system model instead of the default one only.
const (
	welcomeCredits     = 10 // keep equal to the default of models.User.Credits
	createCredits      = 2
	reviseCredits      = 1
	siteSafetyLimit    = 50 // a safety bound on websites per person, not a product limit
	builderQuotaZoneTZ = "Asia/Jakarta"
	// BUILDER_UNLIMITED_EMAILS lists the owners' own accounts (comma-separated e-mail
	// addresses): no credits used and no daily system-model quota.
	builderUnlimitedEnv = "BUILDER_UNLIMITED_EMAILS"
)

// builderPlan is what one person may use of the Website Builder right now.
type builderPlan struct {
	// Credits is the balance; Available is what is left once the work already started
	// (queued or running) is paid for.
	Credits    int `json:"credits"`
	Available  int `json:"available"`
	CreateCost int `json:"create_cost"`
	ReviseCost int `json:"revise_cost"`
	SiteLimit  int `json:"site_limit"`
	SitesUsed  int `json:"sites_used"`
	// Unlimited is one of the owners' own accounts (BUILDER_UNLIMITED_EMAILS).
	Unlimited bool `json:"unlimited"`
}

// unlimitedEmail says whether an e-mail address is listed in BUILDER_UNLIMITED_EMAILS.
func unlimitedEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	for _, listed := range strings.Split(os.Getenv(builderUnlimitedEnv), ",") {
		if strings.ToLower(strings.TrimSpace(listed)) == email {
			return true
		}
	}
	return false
}

// premiumActive says whether a premium account counts now: switched on, and not past its end.
func premiumActive(user models.User, now time.Time) bool {
	return user.IsPremium && (user.PremiumUntil == nil || now.Before(*user.PremiumUntil))
}

// CanAfford says whether the person may start work that uses cost credits.
func (p builderPlan) CanAfford(cost int) bool {
	return p.Unlimited || p.Available >= cost
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

func loadPremium(userID uint) (models.User, bool) {
	var user models.User
	if err := config.DB.Select("id", "email", "is_premium", "premium_until", "credits").First(&user, userID).Error; err != nil {
		return user, false
	}
	return user, premiumActive(user, time.Now()) || unlimitedEmail(user.Email)
}

// isUnlimitedUser says whether the person's account has no Website Builder limits.
func isUnlimitedUser(userID uint) bool {
	user, _ := loadPremium(userID)
	return unlimitedEmail(user.Email)
}

func isPremiumUser(userID uint) bool {
	_, active := loadPremium(userID)
	return active
}

// runCredits is what a run of this kind uses when it succeeds.
func runCredits(kind string) int {
	if kind == models.AgentRunCreate {
		return createCredits
	}
	return reviseCredits
}

// reservedCredits is what the person's queued and running work will use.
func reservedCredits(userID uint) int {
	var runs []models.AgentRun
	config.DB.Select("kind").Where("user_id = ? AND status IN ?", userID, []string{models.RunQueued, models.RunRunning}).Find(&runs)
	total := 0
	for _, run := range runs {
		total += runCredits(run.Kind)
	}
	return total
}

func builderPlanFor(userID uint) builderPlan {
	user, _ := loadPremium(userID)
	plan := builderPlan{Credits: user.Credits, CreateCost: createCredits, ReviseCost: reviseCredits, SiteLimit: siteSafetyLimit}
	var sites int64
	config.DB.Model(&models.Site{}).Where("user_id = ?", userID).Count(&sites)
	plan.SitesUsed = int(sites)
	plan.Unlimited = unlimitedEmail(user.Email)
	plan.Available = user.Credits - reservedCredits(userID)
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
