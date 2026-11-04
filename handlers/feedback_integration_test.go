package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// A report can be deleted only once it is finished, only by its owner (or an
// admin), and its replies go with it. Needs SKILLS_TEST_DATABASE_URL.
func TestFeedbackDeleteOnlyWhenFinished(t *testing.T) {
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

	suffix := strconv.Itoa(os.Getpid())
	owner := models.User{Name: "Owner", Email: "feedback-owner-" + suffix + "@example.test"}
	other := models.User{Name: "Other", Email: "feedback-other-" + suffix + "@example.test"}
	for _, u := range []*models.User{&owner, &other} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	open := models.Feedback{UserID: owner.ID, Category: "bug", Description: "masih terbuka", RelatedPage: "Chat", Status: "processing"}
	done := models.Feedback{UserID: owner.ID, Category: "bug", Description: "sudah selesai", RelatedPage: "Chat", Status: "completed"}
	adminDone := models.Feedback{UserID: owner.ID, Category: "bug", Description: "ditolak admin", RelatedPage: "Chat", Status: "rejected"}
	for _, f := range []*models.Feedback{&open, &done, &adminDone} {
		if err := db.Create(f).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&models.FeedbackReply{FeedbackID: done.ID, AdminID: other.ID, Message: "beres"}).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Where("user_id IN ?", []uint{owner.ID, other.ID}).Delete(&models.Feedback{})
		db.Delete(&owner)
		db.Delete(&other)
	}()

	send := func(as uint, path string, handler gin.HandlerFunc) int {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("user_id", as); c.Next() })
		router.DELETE("/feedback/:id", handler)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, path, nil))
		return res.Code
	}
	id := func(f models.Feedback) string { return "/feedback/" + strconv.FormatUint(uint64(f.ID), 10) }

	if code := send(owner.ID, id(open), DeleteMyFeedback); code != http.StatusConflict {
		t.Errorf("deleting an open report = %d, want 409", code)
	}
	if code := send(other.ID, id(done), DeleteMyFeedback); code != http.StatusNotFound {
		t.Errorf("deleting someone else's report = %d, want 404", code)
	}
	if code := send(owner.ID, id(done), DeleteMyFeedback); code != http.StatusOK {
		t.Errorf("owner deleting a completed report = %d, want 200", code)
	}
	var replies int64
	db.Model(&models.FeedbackReply{}).Where("feedback_id = ?", done.ID).Count(&replies)
	if replies != 0 {
		t.Errorf("replies left behind: %d", replies)
	}
	if code := send(other.ID, id(open), AdminDeleteFeedback); code != http.StatusConflict {
		t.Errorf("admin deleting an open report = %d, want 409", code)
	}
	if code := send(other.ID, id(adminDone), AdminDeleteFeedback); code != http.StatusOK {
		t.Errorf("admin deleting a rejected report = %d, want 200", code)
	}
	var left int64
	db.Model(&models.Feedback{}).Where("id IN ?", []uint{done.ID, adminDone.ID}).Count(&left)
	if left != 0 {
		t.Errorf("deleted reports still exist: %d", left)
	}
}
