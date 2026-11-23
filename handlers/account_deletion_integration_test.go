package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// Deleting an account starts 30 days in which it cannot be used, its owner can only write
// to the admins, and an admin can give it back. After that it is removed for good, with
// its websites. Needs SKILLS_TEST_DATABASE_URL.
func TestAccountDeletionWaitsThirtyDays(t *testing.T) {
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
	t.Setenv("JWT_ACCESS_SECRET", strings.Repeat("a", 40))
	t.Setenv("JWT_REFRESH_SECRET", strings.Repeat("b", 40))
	t.Setenv("SITE_DATA_ROOT", t.TempDir())

	suffix := strconv.Itoa(os.Getpid())
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct horse"), bcrypt.MinCost)
	user := models.User{Name: "Leaving", Email: "leaving-" + suffix + "@example.test", Password: string(hash), Role: "user"}
	admin := models.User{Name: "Restorer", Email: "restorer-" + suffix + "@example.test", Password: string(hash), Role: "admin"}
	for _, u := range []*models.User{&user, &admin} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	site := models.Site{UserID: user.ID, WorkspaceID: 1, Name: "Toko", Status: models.SiteStatusReady}
	if err := db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	ids := []uint{user.ID, admin.ID}
	t.Cleanup(func() {
		db.Where("user_id IN ?", ids).Delete(&models.Feedback{})
		db.Where("user_id IN ?", ids).Delete(&models.Site{})
		db.Where("user_id IN ?", ids).Delete(&models.RefreshToken{})
		db.Where("entity_type = ? AND entity_id IN ?", "user", []string{strconv.Itoa(int(user.ID)), strconv.Itoa(int(admin.ID))}).Delete(&models.AuditLog{})
		db.Unscoped().Delete(&models.User{}, ids)
	})

	gin.SetMode(gin.TestMode)
	as := func(id uint) gin.HandlerFunc {
		return func(c *gin.Context) { c.Set("user_id", id); c.Set("user_role", "admin"); c.Next() }
	}
	router := gin.New()
	router.POST("/login", Login)
	router.POST("/register", Register)
	router.POST("/appeal", SendDeletionAppeal)
	router.DELETE("/me", as(user.ID), DeleteMe)
	router.GET("/admin/users", as(admin.ID), AdminGetUsers)
	router.GET("/admin/feedback", as(admin.ID), AdminGetFeedback)
	router.PATCH("/admin/users/:id/restore", as(admin.ID), AdminRestoreUser)
	call := func(method, path, body string) (int, map[string]any) {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "192.0.2.77:4000"
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		var parsed map[string]any
		_ = json.Unmarshal(recorder.Body.Bytes(), &parsed)
		return recorder.Code, parsed
	}
	login := func(password string) (int, map[string]any) {
		return call("POST", "/login", `{"email":"`+user.Email+`","password":"`+password+`"}`)
	}

	// The owner deletes the account: it waits, it is not removed.
	if code, _ := login("correct horse"); code != http.StatusOK {
		t.Fatalf("sign-in before deleting = %d", code)
	}
	if code, body := call("DELETE", "/me", `{"password":"correct horse"}`); code != http.StatusOK {
		t.Fatalf("delete = %d %v", code, body)
	}
	var marked models.User
	if err := db.Unscoped().First(&marked, user.ID).Error; err != nil || !marked.DeletedAt.Valid {
		t.Fatalf("the account must stay, marked for deletion: %v %+v", err, marked.DeletedAt)
	}
	var liveTokens int64
	db.Model(&models.RefreshToken{}).Where("user_id = ? AND revoked_at IS NULL", user.ID).Count(&liveTokens)
	if liveTokens != 0 {
		t.Errorf("%d sessions still open after deleting", liveTokens)
	}
	var kept int64
	db.Model(&models.Site{}).Where("user_id = ?", user.ID).Count(&kept)
	if kept != 1 {
		t.Errorf("the website must be kept while the account waits, found %d", kept)
	}
	var offline int64
	db.Model(&models.Site{}).Where("id = ?", site.ID).Where(ownerNotInDeletion).Count(&offline)
	if offline != 0 {
		t.Error("a website of an account in deletion must leave the gallery")
	}

	// Signing in: a wrong password is still just wrong; the right one gets the notice.
	if code, body := login("wrong horse"); code != http.StatusUnauthorized || body["code"] == "account_pending_deletion" {
		t.Errorf("wrong password = %d %v, want the usual refusal", code, body)
	}
	code, body := login("correct horse")
	if code != http.StatusForbidden || body["code"] != "account_pending_deletion" || body["appeal_token"] == "" || body["deletes_at"] == nil {
		t.Fatalf("sign-in to an account in deletion = %d %v", code, body)
	}
	if body["access_token"] != nil {
		t.Fatal("an account in deletion must not get a session")
	}
	token, _ := body["appeal_token"].(string)

	// Its address stays taken.
	if code, _ := call("POST", "/register", `{"name":"New","email":"`+user.Email+`","password":"another long pass"}`); code != http.StatusConflict && code != http.StatusGone {
		t.Errorf("register with the address of an account in deletion = %d", code)
	}

	// Messages to the admins: only with the token, of a sensible length, three at most.
	if code, _ := call("POST", "/appeal", `{"appeal_token":"forged","message":"Tolong kembalikan akun saya."}`); code != http.StatusUnauthorized {
		t.Errorf("forged token = %d, want 401", code)
	}
	if code, _ := call("POST", "/appeal", `{"appeal_token":"`+token+`","message":"pendek"}`); code != http.StatusBadRequest {
		t.Errorf("short message = %d, want 400", code)
	}
	for i := 0; i < maxOpenAppeals; i++ {
		if code, body := call("POST", "/appeal", `{"appeal_token":"`+token+`","message":"Tolong kembalikan akun saya, saya salah tekan."}`); code != http.StatusCreated {
			t.Fatalf("appeal %d = %d %v", i+1, code, body)
		}
	}
	if code, _ := call("POST", "/appeal", `{"appeal_token":"`+token+`","message":"Tolong kembalikan akun saya, saya salah tekan."}`); code != http.StatusTooManyRequests {
		t.Errorf("a fourth open appeal = %d, want 429", code)
	}
	if _, ok := readAppealToken(token, time.Now().Add(appealTokenTTL+time.Minute)); ok {
		t.Error("an appeal token must expire")
	}

	// The admin sees the account and its messages, with when it will be removed.
	_, listed := call("GET", "/admin/users", "")
	found := false
	for _, item := range listed["users"].([]any) {
		row := item.(map[string]any)
		if row["id"] == float64(user.ID) {
			found = row["deletes_at"] != nil && row["deletion_requested_at"] != nil
		}
	}
	if !found {
		t.Error("the admin user list must show the account in deletion with its date")
	}
	_, feedback := call("GET", "/admin/feedback", "")
	appeals := 0
	for _, item := range feedback["feedback"].([]any) {
		row := item.(map[string]any)
		if row["user_id"] == float64(user.ID) && row["category"] == accountRestoreTicket && row["user_deletes_at"] != nil {
			appeals++
		}
	}
	if appeals != maxOpenAppeals {
		t.Errorf("the admin sees %d restore messages, want %d", appeals, maxOpenAppeals)
	}

	// The admin restores it: sign-in works again, the messages are closed.
	if code, body := call("PATCH", "/admin/users/"+strconv.Itoa(int(user.ID))+"/restore", ""); code != http.StatusOK {
		t.Fatalf("restore = %d %v", code, body)
	}
	if code, _ := login("correct horse"); code != http.StatusOK {
		t.Errorf("sign-in after restore = %d", code)
	}
	var open int64
	db.Model(&models.Feedback{}).Where("user_id = ? AND status IN ?", user.ID, []string{"new", "processing"}).Count(&open)
	if open != 0 {
		t.Errorf("%d restore messages left open after restoring", open)
	}
	if code, _ := call("PATCH", "/admin/users/"+strconv.Itoa(int(user.ID))+"/restore", ""); code != http.StatusConflict {
		t.Errorf("restoring an active account = %d, want 409", code)
	}

	// A restore happens once: deleting the account again removes it at once, with the website.
	code, body = call("DELETE", "/me", `{"password":"correct horse"}`)
	if code != http.StatusOK || body["final"] != true {
		t.Fatalf("second delete = %d %v, want a final deletion", code, body)
	}
	assertAccountGone(t, db, user.ID)
}

// Without a restore, an account is removed when its 30 days have passed. An admin's delete
// is final at once, even for an account never deleted before.
func TestAccountRemovedAfterThirtyDaysOrByAdmin(t *testing.T) {
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
	t.Setenv("SITE_DATA_ROOT", t.TempDir())

	suffix := strconv.Itoa(os.Getpid())
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct horse"), bcrypt.MinCost)
	waiting := models.User{Name: "Waiting", Email: "waiting-" + suffix + "@example.test", Password: string(hash), Role: "user"}
	target := models.User{Name: "Target", Email: "target-" + suffix + "@example.test", Password: string(hash), Role: "user"}
	admin := models.User{Name: "Remover", Email: "remover-" + suffix + "@example.test", Password: string(hash), Role: "admin"}
	for _, u := range []*models.User{&waiting, &target, &admin} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, owner := range []uint{waiting.ID, target.ID} {
		db.Create(&models.Site{UserID: owner, WorkspaceID: 1, Name: "Toko", Status: models.SiteStatusReady})
		db.Create(&models.Feedback{UserID: owner, Category: "help", Description: "Tolong bantu saya.", RelatedPage: "/", Status: "new"})
	}
	ids := []uint{waiting.ID, target.ID, admin.ID}
	t.Cleanup(func() {
		db.Where("user_id IN ?", ids).Delete(&models.Feedback{})
		db.Where("user_id IN ?", ids).Delete(&models.Site{})
		db.Where("entity_type = ? AND entity_id IN ?", "user", []string{strconv.Itoa(int(waiting.ID)), strconv.Itoa(int(target.ID)), strconv.Itoa(int(admin.ID))}).Delete(&models.AuditLog{})
		db.Unscoped().Delete(&models.User{}, ids)
	})

	// Deleted by its owner: kept for 30 days, then removed.
	if err := db.Delete(&waiting).Error; err != nil {
		t.Fatal(err)
	}
	if removed := PurgeExpiredAccounts(time.Now()); removed != 0 {
		t.Errorf("an account deleted just now was removed (%d)", removed)
	}
	db.Unscoped().Model(&models.User{}).Where("id = ?", waiting.ID).Update("deleted_at", time.Now().Add(-accountDeletionGrace-time.Hour))
	if removed := PurgeExpiredAccounts(time.Now()); removed < 1 {
		t.Fatal("an account past its 30 days was not removed")
	}
	assertAccountGone(t, db, waiting.ID)

	// Deleted by an admin: gone at once, with no waiting time and no restore.
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/admin/users/:id", func(c *gin.Context) { c.Set("user_id", admin.ID); c.Next() }, AdminDeleteUser)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/admin/users/"+strconv.Itoa(int(target.ID)), nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin delete = %d %s", recorder.Code, recorder.Body.String())
	}
	assertAccountGone(t, db, target.ID)
}

// assertAccountGone checks that an account, its websites and its messages are removed.
func assertAccountGone(t *testing.T, db *gorm.DB, userID uint) {
	t.Helper()
	var left int64
	db.Unscoped().Model(&models.User{}).Where("id = ?", userID).Count(&left)
	if left != 0 {
		t.Errorf("account %d is still there", userID)
	}
	db.Model(&models.Site{}).Where("user_id = ?", userID).Count(&left)
	if left != 0 {
		t.Errorf("the websites of account %d must be removed with it", userID)
	}
	db.Model(&models.Feedback{}).Where("user_id = ?", userID).Count(&left)
	if left != 0 {
		t.Errorf("the messages of account %d must be removed with it", userID)
	}
}

// passWaitingTime acts as if the 30 days after deleting an account had passed, and runs
// the removal, for tests that check what a deleted account leaves behind.
func passWaitingTime(t *testing.T, userID uint) {
	t.Helper()
	config.DB.Unscoped().Model(&models.User{}).Where("id = ? AND deleted_at IS NOT NULL", userID).
		Update("deleted_at", time.Now().Add(-accountDeletionGrace-time.Hour))
	PurgeExpiredAccounts(time.Now())
	var left int64
	config.DB.Unscoped().Model(&models.User{}).Where("id = ?", userID).Count(&left)
	if left != 0 {
		t.Fatalf("account %d was not removed after its waiting time", userID)
	}
}

func TestDeletesAtIsThirtyDaysLater(t *testing.T) {
	if deletesAt(models.User{DeletedAt: gorm.DeletedAt{Time: time.Unix(0, 0), Valid: true}}) != time.Unix(0, 0).Add(accountDeletionGrace) {
		t.Error("deletesAt must be thirty days after the request")
	}
}
