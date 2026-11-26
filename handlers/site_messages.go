package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite" // reads the websites' own SQLite files

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/sitebuilder"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// What visitors sent through a website's forms (a booking, an RSVP, a question) is
// stored by the website itself, in the SQLite file of its data folder. The owner
// reads it here; the file is opened read-only and never changed.
const (
	siteMessagesLimit      = 200
	siteMessageValueLimit  = 5000
	messageNotifyInterval  = 2 * time.Minute
	siteDatabaseFile       = "site.db"
	siteDatabaseBusyMillis = 3000
)

type messageField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type siteMessage struct {
	ID     int64          `json:"id"`
	Form   string         `json:"form"`
	Fields []messageField `json:"fields"`
	At     string         `json:"at"`
	// Test is true for a message sent to the preview, before the website was published.
	Test bool `json:"test"`
}

func siteDatabasePath(siteID uint, kind string) string {
	return filepath.Join(sitebuilder.LoadSettings().DataDir(siteID, kind), siteDatabaseFile)
}

// openSiteDatabase opens a website's SQLite file for reading, or returns nil when the
// website has no file yet (no visitor ever sent anything).
func openSiteDatabase(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(%d)", filepath.ToSlash(path), siteDatabaseBusyMillis)
	return sql.Open("sqlite", dsn)
}

// orderedFields keeps the fields in the order the visitor filled them in.
func orderedFields(raw string) []messageField {
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.UseNumber()
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return []messageField{{Name: "message", Value: clipRunes(raw, siteMessageValueLimit)}}
	}
	fields := []messageField{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			break
		}
		var value any
		if err := decoder.Decode(&value); err != nil {
			break
		}
		text := ""
		switch typed := value.(type) {
		case string:
			text = typed
		case nil:
		default:
			encoded, _ := json.Marshal(typed)
			text = string(encoded)
		}
		fields = append(fields, messageField{Name: fmt.Sprint(key), Value: clipRunes(text, siteMessageValueLimit)})
	}
	return fields
}

// readSiteMessages returns the newest messages of one SQLite file, newest first.
func readSiteMessages(path string, test bool) ([]siteMessage, error) {
	db, err := openSiteDatabase(path)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query("SELECT id, form, data, created_at FROM submissions ORDER BY id DESC LIMIT ?", siteMessagesLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []siteMessage{}
	for rows.Next() {
		var message siteMessage
		var data string
		if err := rows.Scan(&message.ID, &message.Form, &data, &message.At); err != nil {
			return nil, err
		}
		message.Fields, message.Test = orderedFields(data), test
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

// newestSiteMessage is the id of the newest message, and how many came after `after`.
func newestSiteMessage(path string, after int64) (newest int64, count int64, err error) {
	db, err := openSiteDatabase(path)
	if err != nil || db == nil {
		return 0, 0, err
	}
	defer db.Close()
	err = db.QueryRow("SELECT COALESCE(MAX(id), 0), COUNT(CASE WHEN id > ? THEN 1 END) FROM submissions", after).Scan(&newest, &count)
	return newest, count, err
}

// GetSiteMessages answers GET /api/sites/:id/messages: what visitors sent to the
// published website. Before the first publish it shows what was sent to the
// preview, marked as a test.
func GetSiteMessages(c *gin.Context) {
	site, _, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	kind, test := "live", false
	if site.PublishedReleaseID == nil {
		kind, test = "preview", true
	}
	messages, err := readSiteMessages(siteDatabasePath(site.ID, kind), test)
	if err != nil {
		log.Printf("[MESSAGES] site %d: %v", site.ID, err)
		siteError(c, http.StatusInternalServerError, "messages", "Pesan belum bisa dibaca.")
		return
	}
	if messages == nil {
		messages = []siteMessage{}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "messages": messages, "test": test})
}

// messageNotice is the e-mail about new messages, in the builder's language.
func messageNotice(site models.Site, count int64) (subject, heading, body, button string) {
	if site.UILanguage == "en" {
		if count == 1 {
			return "A new message on " + site.Name, "You have a new message", "A visitor sent a message through " + site.Name + ".", "Read the message"
		}
		return strconv.FormatInt(count, 10) + " new messages on " + site.Name, "You have new messages", "Visitors sent " + strconv.FormatInt(count, 10) + " messages through " + site.Name + ".", "Read the message"
	}
	if count == 1 {
		return "Pesan baru di " + site.Name, "Ada pesan baru", "Seorang pengunjung mengirim pesan lewat " + site.Name + ".", "Baca pesan"
	}
	return strconv.FormatInt(count, 10) + " pesan baru di " + site.Name, "Ada pesan baru", "Pengunjung mengirim " + strconv.FormatInt(count, 10) + " pesan lewat " + site.Name + ".", "Baca pesan"
}

// CheckNewSiteMessages e-mails the owners of published websites about messages
// that arrived since the last check. The first check of a website only notes where
// it stands, so turning this on never sends a flood of old messages.
func CheckNewSiteMessages() {
	type row struct {
		models.Site
		Email string
	}
	var sites []row
	config.DB.Table("sites").Select("sites.*, users.email").
		Joins("JOIN users ON users.id = sites.user_id AND users.deleted_at IS NULL AND users.notify_new_messages").
		Where("sites.published_release_id IS NOT NULL").Find(&sites)
	apiKey, sender, _, canSend := services.EmailOTPConfig()
	for _, site := range sites {
		after := int64(-1)
		if site.MessagesNotifiedID != nil {
			after = *site.MessagesNotifiedID
		}
		newest, count, err := newestSiteMessage(siteDatabasePath(site.ID, "live"), max(after, 0))
		if err != nil {
			log.Printf("[MESSAGES] site %d: %v", site.ID, err)
			continue
		}
		if after >= 0 && count > 0 && canSend && site.Email != "" {
			subject, heading, body, button := messageNotice(site.Site, count)
			htmlBody, textBody := noticeEmail(heading, body, button, appLink("/sites/"+strconv.FormatUint(uint64(site.ID), 10)+"?messages=1"))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err := services.SendEmail(ctx, apiKey, sender, site.Email, subject, htmlBody, textBody)
			cancel()
			if err != nil {
				log.Printf("[MESSAGES] site %d: e-mail not sent: %v", site.ID, err)
				continue // try again at the next check
			}
		}
		if after < 0 || newest > after {
			config.DB.Model(&models.Site{}).Where("id = ?", site.ID).UpdateColumn("messages_notified_id", newest)
		}
	}
}

// StartMessageNotifier checks for new messages every two minutes.
func StartMessageNotifier() {
	go func() {
		ticker := time.NewTicker(messageNotifyInterval)
		defer ticker.Stop()
		for range ticker.C {
			CheckNewSiteMessages()
		}
	}()
}
