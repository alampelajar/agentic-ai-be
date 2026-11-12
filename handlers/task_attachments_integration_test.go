package handlers

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// uploadMessage posts a chat message with files to AddTaskMessage, as the browser does.
func uploadMessage(t *testing.T, userID, taskID uint, role string, files map[string]string) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("role", role)
	_ = writer.WriteField("content", "lihat lampiran ini")
	for name, content := range files {
		part, _ := writer.CreateFormFile("attachments", name)
		_, _ = part.Write([]byte(content))
	}
	writer.Close()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(int(taskID))}}
	c.Set("user_id", userID)
	c.Set("userID", userID)
	AddTaskMessage(c)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func fetchAttachment(userID, taskID uint, attachmentID any) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(int(taskID))}, {Key: "attachmentId", Value: jsonNumber(attachmentID)}}
	c.Set("user_id", userID)
	c.Set("userID", userID)
	GetTaskAttachment(c)
	return rec
}

func jsonNumber(value any) string {
	if number, ok := value.(float64); ok {
		return strconv.FormatInt(int64(number), 10)
	}
	return ""
}

func TestAttachmentsAreKeptWithTheMessageAndOnlyForTheirOwner(t *testing.T) {
	dsn := os.Getenv("SKILLS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKILLS_TEST_DATABASE_URL is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	config.DB = db
	if err := migrateAll(db); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	t.Setenv("CHAT_ATTACHMENT_ROOT", t.TempDir())

	suffix := strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	owner := models.User{Name: "Owner", Email: "attach-owner-" + suffix + "@example.test"}
	other := models.User{Name: "Other", Email: "attach-other-" + suffix + "@example.test"}
	for _, u := range []*models.User{&owner, &other} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	task := models.Task{UserID: owner.ID, Title: "Lampiran", Status: "running"}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}

	png := "\x89PNG\r\n\x1a\nfake image"
	code, out := uploadMessage(t, owner.ID, task.ID, "user", map[string]string{"foto.png": png, "catatan.md": "# Catatan"})
	if code != http.StatusCreated {
		t.Fatalf("upload = %d %v", code, out)
	}

	// The history lists the attachments with the message.
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(int(task.ID))}}
	c.Set("user_id", owner.ID)
	c.Set("userID", owner.ID)
	GetTaskMessages(c)
	var history struct {
		Messages []struct {
			Attachments []map[string]any `json:"attachments"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &history)
	if len(history.Messages) != 1 || len(history.Messages[0].Attachments) != 2 {
		t.Fatalf("history = %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "storage_key") || strings.Contains(rec.Body.String(), "StorageKey") {
		t.Fatalf("the storage path must not be shown: %s", rec.Body.String())
	}
	var image, document map[string]any
	for _, attachment := range history.Messages[0].Attachments {
		if attachment["is_image"] == true {
			image = attachment
		} else {
			document = attachment
		}
	}
	if image == nil || document == nil || image["name"] != "foto.png" || image["mime_type"] != "image/png" {
		t.Fatalf("attachments = %v", history.Messages[0].Attachments)
	}

	// The owner gets the image inline and the document as a download; nobody else gets either.
	got := fetchAttachment(owner.ID, task.ID, image["id"])
	if got.Code != http.StatusOK || got.Body.String() != png || got.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(got.Header().Get("Content-Disposition"), "inline") {
		t.Fatalf("image = %d %q %v", got.Code, got.Body.String(), got.Header())
	}
	doc := fetchAttachment(owner.ID, task.ID, document["id"])
	if doc.Code != http.StatusOK || doc.Body.String() != "# Catatan" || doc.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(doc.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("document = %d %v", doc.Code, doc.Header())
	}
	if fetchAttachment(other.ID, task.ID, image["id"]).Code != http.StatusNotFound {
		t.Fatal("another user must not get the attachment")
	}

	// What a chat could not carry is refused, and so are files on an assistant message.
	if code, _ := uploadMessage(t, owner.ID, task.ID, "user", map[string]string{"virus.exe": "MZ"}); code != http.StatusBadRequest {
		t.Fatalf("an unsupported file must be refused, got %d", code)
	}
	if code, _ := uploadMessage(t, owner.ID, task.ID, "assistant", map[string]string{"a.txt": "x"}); code != http.StatusBadRequest {
		t.Fatalf("an assistant message must not carry files, got %d", code)
	}

	// Deleting the task removes its files.
	dir := taskAttachmentDir(owner.ID, task.ID)
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("want 2 stored files, got %d", len(entries))
	}
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(int(task.ID))}}
	c.Set("user_id", owner.ID)
	c.Set("userID", owner.ID)
	DeleteTask(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the task's files must be removed with it")
	}
	var left int64
	db.Model(&models.TaskMessageAttachment{}).Where("user_id = ?", owner.ID).Count(&left)
	if left != 0 {
		t.Fatalf("attachment rows left: %d", left)
	}
}
