package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"

	"github.com/gin-gonic/gin"
)

// An admin may suspend an account, for example after it tried to make a forbidden
// website (sites_forbidden.go, "site.refused" on the Logs page). A suspended account:
//
//   - cannot sign in (password or Google), and every refresh is refused;
//   - is refused on every request at once (middleware.AuthMiddleware reads it from
//     the database, so a token issued before still stops working);
//   - has its sessions ended and its published websites taken offline, and its
//     queued or running website work stopped.
//
// Its websites and data are kept, so lifting the suspension gives the account back;
// the websites stay offline until the owner publishes them again. Both actions are
// in the audit log ("user.suspend", "user.unsuspend").

// maxSuspendReason bounds the reason the person is shown.
const maxSuspendReason = 300

func suspended(user models.User) bool { return user.SuspendedAt != nil }

// suspendedStatus is the status the admin console shows for a suspended account.
func suspendedStatus(user models.User) string {
	if suspended(user) {
		return "Suspended"
	}
	return ""
}

// refuseSuspended answers a suspended account with the reason it was given.
func refuseSuspended(c *gin.Context, user models.User) {
	c.JSON(http.StatusForbidden, gin.H{
		"ok":      false,
		"code":    "account_suspended",
		"message": "Akun Anda ditangguhkan oleh admin.",
		"reason":  user.SuspendReason,
	})
}

// AdminSuspendUser suspends an account: PATCH /api/admin/users/:id/suspend with
// {"suspended": true, "reason": "..."} or {"suspended": false}.
func AdminSuspendUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	var req struct {
		Suspended *bool  `json:"suspended"`
		Reason    string `json:"reason"`
	}
	if err != nil || c.ShouldBindJSON(&req) != nil || req.Suspended == nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Kirim suspended true atau false."})
		return
	}
	reason := strings.Join(strings.Fields(req.Reason), " ")
	if *req.Suspended && (reason == "" || utf8.RuneCountInString(reason) > maxSuspendReason) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "code": "reason_required", "error": "Tulis alasan penangguhan, paling banyak 300 karakter."})
		return
	}
	var user models.User
	if err := config.DB.First(&user, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "User tidak ditemukan."})
		return
	}
	if *req.Suspended && strings.EqualFold(strings.TrimSpace(user.Role), "admin") {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "code": "admin_account", "error": "Akun admin tidak bisa ditangguhkan. Ubah dulu perannya."})
		return
	}
	userID := strconv.FormatUint(id, 10)

	if !*req.Suspended {
		if err := config.DB.Model(&user).Updates(map[string]any{"suspended_at": nil, "suspend_reason": ""}).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Akun gagal diaktifkan."})
			return
		}
		recordAudit(c, "user.unsuspend", "user", userID, user.Email, "Penangguhan akun dicabut oleh administrator.")
		c.JSON(http.StatusOK, gin.H{"ok": true, "suspended": false})
		return
	}

	now := time.Now()
	if err := config.DB.Model(&user).Updates(map[string]any{"suspended_at": now, "suspend_reason": reason}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Akun gagal ditangguhkan."})
		return
	}
	// Every session ends now; the middleware already refuses the access token.
	config.DB.Model(&models.RefreshToken{}).Where("user_id = ? AND revoked_at IS NULL", user.ID).Update("revoked_at", now)
	offline, stopped := stopUserWebsites(user.ID, now)
	detail := "Akun ditangguhkan: " + reason
	if offline > 0 {
		detail += " (" + strconv.Itoa(offline) + " website dihentikan)"
	}
	recordAudit(c, "user.suspend", "user", userID, user.Email, detail)
	c.JSON(http.StatusOK, gin.H{"ok": true, "suspended": true, "websites_offline": offline, "runs_stopped": stopped})
}

// stopUserWebsites takes every published website of an account offline and stops its
// website work, returning how many of each. The site runner stops the containers on
// its next pass.
func stopUserWebsites(userID uint, now time.Time) (offline, stopped int) {
	var sites []models.Site
	config.DB.Where("user_id = ?", userID).Find(&sites)
	for index := range sites {
		site := &sites[index]
		if site.PublishedReleaseID != nil {
			unpublishSiteRelease(site)
			offline++
		}
	}
	var runs []models.AgentRun
	config.DB.Where("user_id = ? AND status IN ?", userID, []string{models.RunQueued, models.RunRunning}).Find(&runs)
	for _, run := range runs {
		updates := map[string]any{"cancel_requested": true}
		if run.Status == models.RunQueued {
			updates["status"], updates["finished_at"] = models.RunCancelled, now
			// Never started: the website goes back to where it was, as CancelSiteRun does.
			for index := range sites {
				if site := &sites[index]; site.ID == run.SiteID {
					status := models.SiteStatusDraft
					if site.CurrentVersionID != nil {
						status = models.SiteStatusReady
					}
					config.DB.Model(site).Update("status", status)
				}
			}
		}
		config.DB.Model(&models.AgentRun{}).Where("id = ?", run.ID).Updates(updates)
		stopped++
	}
	return offline, stopped
}
