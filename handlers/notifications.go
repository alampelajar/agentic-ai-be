package handlers

import (
	"context"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// The e-mails a person may switch off in Settings, Notifications. E-mails about the
// account's security (codes, deletion) are always sent and are not listed here.
type notificationSettings struct {
	WebsiteReady bool `json:"website_ready"`
	NewMessages  bool `json:"new_messages"`
	// EmailReady says whether this server can send e-mail at all; the screen says so
	// when it cannot, instead of promising messages that never come.
	EmailReady bool `json:"email_ready"`
}

func emailReady() bool {
	_, _, _, ok := services.EmailOTPConfig()
	return ok
}

// GetMyNotifications answers GET /api/me/notifications.
func GetMyNotifications(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "Unauthorized"})
		return
	}
	var user models.User
	if err := config.DB.Select("id", "notify_website_ready", "notify_new_messages").First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "message": "Akun tidak ditemukan."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "notifications": notificationSettings{
		WebsiteReady: user.NotifyWebsiteReady, NewMessages: user.NotifyNewMessages, EmailReady: emailReady(),
	}})
}

// UpdateMyNotifications answers PUT /api/me/notifications.
func UpdateMyNotifications(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "Unauthorized"})
		return
	}
	var req struct {
		WebsiteReady *bool `json:"website_ready"`
		NewMessages  *bool `json:"new_messages"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.WebsiteReady == nil || req.NewMessages == nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Kirim website_ready dan new_messages."})
		return
	}
	if err := config.DB.Model(&models.User{}).Where("id = ?", userID).Updates(map[string]any{
		"notify_website_ready": *req.WebsiteReady, "notify_new_messages": *req.NewMessages,
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Pengaturan belum bisa disimpan."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "notifications": notificationSettings{
		WebsiteReady: *req.WebsiteReady, NewMessages: *req.NewMessages, EmailReady: emailReady(),
	}})
}

// appLink is an address in the person's app, or "" when FRONTEND_URL is not set.
func appLink(path string) string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("FRONTEND_URL")), "/")
	if base == "" {
		return ""
	}
	return base + path
}

// noticeEmail is a short e-mail with one sentence and, when there is an address,
// one button.
func noticeEmail(heading, body, button, link string) (htmlBody, textBody string) {
	action := ""
	if link != "" {
		action = fmt.Sprintf(`<p style="padding:12px 0"><a href="%s" style="background:#171717;color:#ffffff;padding:10px 18px;border-radius:6px;text-decoration:none">%s</a></p>`,
			html.EscapeString(link), html.EscapeString(button))
	}
	htmlBody = fmt.Sprintf(`<div style="font-family:Arial,sans-serif;max-width:520px;margin:auto;color:#171717">
<h2 style="margin-bottom:12px">%s</h2>
<p>%s</p>%s
<p style="color:#737373;font-size:13px">%s</p>
</div>`, html.EscapeString(heading), html.EscapeString(body), action, html.EscapeString(unsubscribeLine(button)))
	textBody = body
	if link != "" {
		textBody += " " + link
	}
	return htmlBody, textBody
}

// unsubscribeLine is in the language of the button it follows.
func unsubscribeLine(button string) string {
	if button == "Open my website" || button == "Read the message" {
		return "You can turn these e-mails off in Settings, Notifications."
	}
	return "E-mail ini bisa dimatikan di Pengaturan, Notifikasi."
}

// runNotice is the e-mail sent when a run ends, in the builder's language.
func runNotice(site models.Site, run models.AgentRun, completed bool) (subject, heading, body, button string) {
	name := site.Name
	english := site.UILanguage == "en"
	switch {
	case completed && run.Kind == models.AgentRunRevise && english:
		return "Your change to " + name + " is ready", "Your change is ready", "We finished the change you asked for on " + name + ". Have a look, then publish it when you like it.", "Open my website"
	case completed && run.Kind == models.AgentRunRevise:
		return "Perubahan " + name + " sudah selesai", "Perubahan Anda sudah selesai", "Perubahan yang Anda minta untuk " + name + " sudah selesai. Lihat dulu, lalu terbitkan kalau sudah cocok.", "Buka website saya"
	case completed && english:
		return name + " is ready", "Your website is ready", "Your website " + name + " is ready. Have a look, ask for changes, then publish it.", "Open my website"
	case completed:
		return name + " sudah jadi", "Website Anda sudah jadi", "Website " + name + " sudah selesai dibuat. Lihat dulu, minta perubahan kalau perlu, lalu terbitkan.", "Buka website saya"
	case english:
		return name + " is not finished yet", "Your website is not finished yet", "Something stopped us while making " + name + ". The finished parts are kept: open it and press Continue.", "Open my website"
	default:
		return name + " belum selesai", "Website Anda belum selesai", "Ada kendala saat membuat " + name + ". Bagian yang sudah jadi tetap disimpan: buka websitenya dan tekan Lanjutkan.", "Buka website saya"
	}
}

// NotifyRunFinished e-mails the owner when a run ends, if they want it and this
// server can send e-mail. A cancelled run sends nothing: the person stopped it.
func NotifyRunFinished(run models.AgentRun, site models.Site, completed bool) {
	if run.Status == models.RunCancelled {
		return
	}
	apiKey, sender, _, ok := services.EmailOTPConfig()
	if !ok {
		return
	}
	var user models.User
	if err := config.DB.Select("id", "email", "notify_website_ready").First(&user, run.UserID).Error; err != nil || !user.NotifyWebsiteReady || user.Email == "" {
		return
	}
	subject, heading, body, button := runNotice(site, run, completed)
	htmlBody, textBody := noticeEmail(heading, body, button, appLink("/sites/"+strconv.FormatUint(uint64(site.ID), 10)))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := services.SendEmail(ctx, apiKey, sender, user.Email, subject, htmlBody, textBody); err != nil {
		log.Printf("[NOTIFY] run %d: e-mail not sent: %v", run.ID, err)
	}
}
