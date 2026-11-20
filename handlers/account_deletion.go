package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/sitebuilder"
	"agentic-ai-backend/models"
	"agentic-ai-backend/utils"
)

// Deleting an account is not immediate. The account is marked (users.deleted_at) and
// kept for accountDeletionGrace: it cannot be used, its websites are offline, and the
// person can only send a message to the admins asking for it back. An admin may restore
// it in that time. Afterwards PurgeExpiredAccounts removes it and everything it owns.
const (
	accountDeletionGrace = 30 * 24 * time.Hour
	// appealTokenTTL is how long the form shown after a refused sign-in stays usable.
	appealTokenTTL = time.Hour
	// maxOpenAppeals bounds the messages one account in deletion may leave unanswered.
	maxOpenAppeals       = 3
	appealMinLength      = 10
	appealMaxLength      = 2000
	accountRestoreTicket = "accountRestore"
	// purgeInterval is how often the API server looks for accounts whose time is up.
	purgeInterval = time.Hour
)

var errUserNotPendingDeletion = errors.New("the account is not waiting to be deleted")

// deletesAt is when an account marked for deletion is removed for good.
func deletesAt(user models.User) time.Time {
	return user.DeletedAt.Time.Add(accountDeletionGrace)
}

// pendingDeletion says whether the account is marked for deletion.
func pendingDeletion(user models.User) bool { return user.DeletedAt.Valid }

// markAccountForDeletion starts the waiting time, inside tx: sessions end and running
// website work stops. Nothing the person owns is removed yet.
func markAccountForDeletion(tx *gorm.DB, user *models.User, audit *models.AuditLog) error {
	if user.Role == "admin" {
		var admins int64
		if err := tx.Model(&models.User{}).Where("role = ?", "admin").Count(&admins).Error; err != nil {
			return err
		}
		if admins <= 1 {
			return errCannotDeleteLastAdmin
		}
	}
	now := time.Now()
	if err := tx.Model(&models.RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", user.ID).
		Update("revoked_at", now).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.AgentRun{}).
		Where("user_id = ? AND status IN ?", user.ID, []string{models.RunQueued, models.RunRunning}).
		Update("cancel_requested", true).Error; err != nil {
		return err
	}
	if err := tx.Create(audit).Error; err != nil {
		return err
	}
	// A soft delete: every ordinary lookup of users now skips this account.
	return tx.Delete(user).Error
}

// ---------------------------------------------------------------------------
// The sign-in answer for an account in deletion, and the message form behind it.
// ---------------------------------------------------------------------------

func appealKey() ([]byte, error) {
	secret, err := utils.AccessSecret()
	if err != nil {
		return nil, err
	}
	// A key of its own, so an appeal token can never pass as a sign-in token.
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("account-deletion-appeal"))
	return mac.Sum(nil), nil
}

func signAppeal(payload string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// newAppealToken lets the person who just proved they own the account (password or
// Google) send messages about it for a short time, without signing in.
func newAppealToken(userID uint, now time.Time) (string, error) {
	key, err := appealKey()
	if err != nil {
		return "", err
	}
	payload := fmt.Sprintf("%d.%d", userID, now.Add(appealTokenTTL).Unix())
	raw := payload + "." + signAppeal(payload, key)
	return base64.RawURLEncoding.EncodeToString([]byte(raw)), nil
}

// readAppealToken returns the account an appeal token was given for, or false.
func readAppealToken(token string, now time.Time) (uint, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return 0, false
	}
	parts := strings.Split(string(decoded), ".")
	if len(parts) != 3 {
		return 0, false
	}
	key, err := appealKey()
	if err != nil {
		return 0, false
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(signAppeal(payload, key)), []byte(parts[2])) {
		return 0, false
	}
	userID, err1 := strconv.ParseUint(parts[0], 10, 64)
	expires, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil || userID == 0 || now.Unix() > expires {
		return 0, false
	}
	return uint(userID), true
}

// refusePendingDeletion answers a correct sign-in to an account in deletion. The person
// is told when it will be removed and gets a token for the message form; no session.
func refusePendingDeletion(c *gin.Context, user models.User) {
	token, err := newAppealToken(user.ID, time.Now())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "message": "Server belum dikonfigurasi dengan benar"})
		return
	}
	c.JSON(http.StatusForbidden, gin.H{
		"ok":           false,
		"code":         "account_pending_deletion",
		"message":      "Akun ini sedang dalam proses penghapusan.",
		"deletes_at":   deletesAt(user),
		"appeal_token": token,
	})
}

// findUserByEmailAny finds an account by address, including one marked for deletion.
func findUserByEmailAny(email string) (models.User, error) {
	var user models.User
	err := config.DB.Unscoped().Where("email = ?", email).First(&user).Error
	return user, err
}

// emailTaken says whether an address belongs to an account, including one waiting to
// be deleted: its address stays reserved until it is removed for good.
func emailTaken(tx *gorm.DB, email string, exceptID uint) (bool, error) {
	var count int64
	query := tx.Unscoped().Model(&models.User{}).Where("email = ?", email)
	if exceptID != 0 {
		query = query.Where("id <> ?", exceptID)
	}
	err := query.Count(&count).Error
	return count > 0, err
}

// SendDeletionAppeal takes a message from someone whose account is in deletion, for the
// admins. Public route: the appeal token from the refused sign-in proves the account.
func SendDeletionAppeal(c *gin.Context) {
	var req struct {
		Token   string `json:"appeal_token"`
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "code": "invalid", "message": "Format permintaan tidak valid."})
		return
	}
	userID, ok := readAppealToken(req.Token, time.Now())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "code": "appeal_expired", "message": "Masuk lagi untuk mengirim pesan."})
		return
	}
	message := strings.TrimSpace(req.Message)
	if length := len([]rune(message)); length < appealMinLength || length > appealMaxLength {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "code": "appeal_length", "message": "Pesan harus 10 sampai 2000 karakter."})
		return
	}
	var user models.User
	if err := config.DB.Unscoped().First(&user, userID).Error; err != nil || !pendingDeletion(user) {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "code": "not_pending_deletion", "message": "Akun ini tidak sedang dihapus."})
		return
	}
	var open int64
	config.DB.Model(&models.Feedback{}).
		Where("user_id = ? AND category = ? AND status IN ?", user.ID, accountRestoreTicket, []string{"new", "processing"}).
		Count(&open)
	if open >= maxOpenAppeals {
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "code": "appeal_limit", "message": "Tunggu balasan admin untuk pesan sebelumnya."})
		return
	}
	item := models.Feedback{
		UserID: user.ID, Category: accountRestoreTicket, Subject: "Permintaan pemulihan akun",
		Description: message, RelatedPage: "/sign-in", Status: "new",
	}
	if err := config.DB.Create(&item).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "code": "appeal_failed", "message": "Pesan gagal dikirim."})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true})
}

// ---------------------------------------------------------------------------
// Admin: give an account back.
// ---------------------------------------------------------------------------

// AdminRestoreUser ends the waiting time of an account marked for deletion: the person
// can sign in again and their websites come back online. Open restore messages are closed.
// An account is restored once at most (User.RestoredAt).
func AdminRestoreUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "ID user tidak valid"})
		return
	}
	var user models.User
	if err := config.DB.Unscoped().First(&user, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "message": "User tidak ditemukan"})
		return
	}
	err = config.DB.Transaction(func(tx *gorm.DB) error {
		// restored_at makes this the only restore: a later deletion is final at once.
		result := tx.Unscoped().Model(&models.User{}).
			Where("id = ? AND deleted_at IS NOT NULL AND restored_at IS NULL", user.ID).
			Updates(map[string]any{"deleted_at": nil, "restored_at": time.Now()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errUserNotPendingDeletion
		}
		if err := tx.Model(&models.Feedback{}).
			Where("user_id = ? AND category = ? AND status IN ?", user.ID, accountRestoreTicket, []string{"new", "processing"}).
			Update("status", "completed").Error; err != nil {
			return err
		}
		audit := newAuditLog(c, "user.restored_by_admin", "user", strconv.FormatUint(id, 10), user.Name,
			"Akun \""+user.Email+"\" dipulihkan oleh administrator sebelum dihapus permanen.")
		return tx.Create(&audit).Error
	})
	if errors.Is(err, errUserNotPendingDeletion) {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "message": "Akun ini tidak sedang dalam proses penghapusan"})
		return
	}
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal memulihkan akun"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Akun berhasil dipulihkan"})
}

// ---------------------------------------------------------------------------
// Removing for good what is past its time.
// ---------------------------------------------------------------------------

// userSiteIDs lists the person's websites; their folders are removed after the commit.
func userSiteIDs(userID uint) []uint {
	var ids []uint
	config.DB.Model(&models.Site{}).Where("user_id = ?", userID).Pluck("id", &ids)
	return ids
}

// removeSiteFolders deletes the folders of removed websites. The site runner stops their
// containers within seconds of the rows going; the folders can be removed after that.
func removeSiteFolders(ids []uint) {
	if len(ids) == 0 {
		return
	}
	settings := sitebuilder.LoadSettings()
	go func() {
		for attempt := 0; attempt < 10; attempt++ {
			time.Sleep(6 * time.Second)
			left := false
			for _, id := range ids {
				if os.RemoveAll(settings.SiteDir(id)) != nil {
					left = true
				}
			}
			if !left {
				return
			}
		}
	}()
}

// purgeAccountForGood removes an account and everything it owns, with its files.
func purgeAccountForGood(user models.User, audit models.AuditLog) error {
	workspaceRefs := userWorkspaceRefs(user.ID)
	siteIDs := userSiteIDs(user.ID)
	if err := config.DB.Transaction(func(tx *gorm.DB) error {
		return purgeUserAccount(tx, &user, &audit)
	}); err != nil {
		return err
	}
	removeWorkspaceFiles(user.ID, workspaceRefs)
	removeSiteFolders(siteIDs)
	return nil
}

// PurgeExpiredAccounts removes every account whose waiting time has passed. Returns how
// many were removed.
func PurgeExpiredAccounts(now time.Time) int {
	var expired []models.User
	if err := config.DB.Unscoped().
		Where("deleted_at IS NOT NULL AND deleted_at <= ?", now.Add(-accountDeletionGrace)).
		Find(&expired).Error; err != nil {
		log.Printf("[ACCOUNTS] could not list accounts to remove: %v", err)
		return 0
	}
	removed := 0
	for _, user := range expired {
		audit := models.AuditLog{
			ActorName: "Sistem", ActorRole: "system",
			Action: "user.purged", EntityType: "user", EntityID: strconv.FormatUint(uint64(user.ID), 10),
			EntityName: user.Name,
			Message:    "Akun \"" + user.Email + "\" dihapus permanen setelah 30 hari dalam proses penghapusan.",
			CreatedAt:  now,
		}
		if err := purgeAccountForGood(user, audit); err != nil {
			log.Printf("[ACCOUNTS] account %d could not be removed: %v", user.ID, err)
			continue
		}
		removed++
	}
	return removed
}

// StartAccountPurger checks for expired accounts now and every purgeInterval.
func StartAccountPurger() {
	go func() {
		for {
			if removed := PurgeExpiredAccounts(time.Now()); removed > 0 {
				log.Printf("[ACCOUNTS] %d account(s) removed for good after the waiting time", removed)
			}
			time.Sleep(purgeInterval)
		}
	}()
}
