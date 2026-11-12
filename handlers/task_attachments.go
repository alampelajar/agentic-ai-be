package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// Files attached to a chat message are kept under CHAT_ATTACHMENT_ROOT/<user>/<task>/, named by a
// random id (never by the name the person gave), so the conversation can show them after a reload.

func chatAttachmentRoot() string {
	if root := strings.TrimSpace(os.Getenv("CHAT_ATTACHMENT_ROOT")); root != "" {
		return root
	}
	return filepath.Join("data", "chat-attachments")
}

func taskAttachmentDir(userID, taskID uint) string {
	return filepath.Join(chatAttachmentRoot(), strconv.FormatUint(uint64(userID), 10), strconv.FormatUint(uint64(taskID), 10))
}

// checkTaskAttachments refuses what a chat message could not carry either: too many files, a file
// over 10 MB, or a type the chat does not accept.
func checkTaskAttachments(files []*multipart.FileHeader) error {
	if len(files) > chatMaxFiles {
		return fmt.Errorf("maksimal %d lampiran per pesan", chatMaxFiles)
	}
	for _, file := range files {
		if file.Size > chatMaxFileBytes {
			return fmt.Errorf("lampiran %s lebih besar dari 10MB", file.Filename)
		}
		if !isAllowedChatImage(file.Filename) && !chatFileSupported(file.Filename) {
			return fmt.Errorf("lampiran %s tidak didukung", file.Filename)
		}
	}
	return nil
}

func attachmentName(raw string) string {
	name := strings.TrimSpace(filepath.Base(strings.ReplaceAll(raw, "\\", "/")))
	if name == "" || name == "." || name == "/" {
		name = "lampiran"
	}
	if runes := []rune(name); len(runes) > 200 {
		name = string(runes[:200])
	}
	return name
}

// saveTaskAttachments writes the files and their rows. On any failure nothing is kept.
func saveTaskAttachments(userID, taskID, messageID uint, files []*multipart.FileHeader) ([]models.TaskMessageAttachment, error) {
	dir := taskAttachmentDir(userID, taskID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, errors.New("folder lampiran tidak dapat dibuat")
	}
	saved := make([]models.TaskMessageAttachment, 0, len(files))
	cleanup := func() {
		for _, attachment := range saved {
			_ = os.Remove(filepath.Join(dir, attachment.StorageKey))
		}
	}
	for _, header := range files {
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			cleanup()
			return nil, errors.New("lampiran tidak dapat disimpan")
		}
		name := attachmentName(header.Filename)
		key := hex.EncodeToString(random) + strings.ToLower(filepath.Ext(name))
		if err := copyUploaded(header, filepath.Join(dir, key)); err != nil {
			cleanup()
			return nil, fmt.Errorf("lampiran %s tidak dapat disimpan", name)
		}
		isImage := isAllowedChatImage(name)
		mimeType := "application/octet-stream"
		if isImage {
			mimeType = detectImageMimeType(name)
		}
		saved = append(saved, models.TaskMessageAttachment{
			TaskMessageID: messageID, UserID: userID, Name: name, MimeType: mimeType, Size: header.Size, IsImage: isImage, StorageKey: key,
		})
	}
	if len(saved) > 0 {
		if err := config.DB.Create(&saved).Error; err != nil {
			cleanup()
			return nil, errors.New("lampiran tidak dapat disimpan")
		}
	}
	return saved, nil
}

func copyUploaded(header *multipart.FileHeader, target string) error {
	source, err := header.Open()
	if err != nil {
		return err
	}
	defer source.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(source, chatMaxFileBytes+1)); err != nil {
		out.Close()
		_ = os.Remove(target)
		return err
	}
	return out.Close()
}

// removeTaskAttachmentFiles deletes a task's attachment files; the rows go with the messages.
func removeTaskAttachmentFiles(userID, taskID uint) {
	_ = os.RemoveAll(taskAttachmentDir(userID, taskID))
}

func removeUserAttachmentFiles(userID uint) {
	_ = os.RemoveAll(filepath.Join(chatAttachmentRoot(), strconv.FormatUint(uint64(userID), 10)))
}

// GetTaskAttachment sends one attachment of the person's own task. Images are shown inline; every
// other file is a download, so nothing uploaded can run as a page of this site.
func GetTaskAttachment(c *gin.Context) {
	task, ok := getOwnedTask(c)
	if !ok {
		return
	}
	attachmentID, err := strconv.ParseUint(strings.TrimSpace(c.Param("attachmentId")), 10, 64)
	if err != nil || attachmentID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "ID lampiran tidak valid."})
		return
	}
	var attachment models.TaskMessageAttachment
	err = config.DB.
		Joins("JOIN task_messages ON task_messages.id = task_message_attachments.task_message_id").
		Where("task_message_attachments.id = ? AND task_messages.task_id = ? AND task_message_attachments.user_id = ?", attachmentID, task.ID, task.UserID).
		First(&attachment).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Lampiran tidak ditemukan."})
		return
	}
	path := filepath.Join(taskAttachmentDir(task.UserID, task.ID), filepath.Base(attachment.StorageKey))
	if _, err := os.Stat(path); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "File lampiran tidak ada lagi."})
		return
	}
	disposition := "attachment"
	if attachment.IsImage {
		disposition = "inline"
	}
	c.Header("Content-Type", attachment.MimeType)
	c.Header("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disposition, url.PathEscape(attachment.Name)))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, max-age=3600")
	c.File(path)
}
