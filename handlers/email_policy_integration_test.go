package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/emailpolicy"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

func emailPolicyRouter(t *testing.T) *gin.Engine {
	t.Helper()
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
	// No e-mail service in tests: the legacy route creates accounts directly, and the
	// code request stops at "unavailable" once every check has passed.
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("SMTP_HOST", "")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/register/request", RequestRegisterOTP)
	router.POST("/register", Register)
	return router
}

func uniqueLocal(prefix string) string {
	return prefix + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func codeOf(t *testing.T, body []byte) string {
	t.Helper()
	var parsed struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &parsed)
	return parsed.Code
}

func cleanupAddress(t *testing.T, email string) {
	t.Cleanup(func() {
		config.DB.Where("key_hash = ?", emailpolicy.SignupKey(email, emailpolicy.Secret())).Delete(&models.EmailSignup{})
		config.DB.Unscoped().Where("canonical_email = ?", emailpolicy.Canonical(email)).Delete(&models.User{})
	})
}

// One mailbox, one account: a Gmail alias (dots, +tag) of a taken address is refused.
func TestAliasOfATakenMailboxIsRefused(t *testing.T) {
	router := emailPolicyRouter(t)
	local := uniqueLocal("rinacheck")
	owner := models.User{Name: "Rina", Email: local + "@gmail.com", Password: "x", Role: "user"}
	if err := config.DB.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	cleanupAddress(t, owner.Email)
	if owner.CanonicalEmail != local+"@gmail.com" {
		t.Fatalf("canonical address not filled on create: %q", owner.CanonicalEmail)
	}

	alias := local[:3] + "." + local[3:] + "+second@googlemail.com"
	for _, path := range []string{"/register/request", "/register"} {
		rec := postJSON(router, path, `{"name":"Rina","email":"`+alias+`","password":"a-long-and-good-password-77"}`)
		if rec.Code != http.StatusConflict || codeOf(t, rec.Body.Bytes()) != services.EmailTakenCode {
			t.Errorf("%s with an alias = %d %s, want 409 email_taken", path, rec.Code, rec.Body.String())
		}
	}
}

// An address that already opened MaxAccountsPerAddress accounts (deleted since, say)
// cannot open another; one below the limit still can.
func TestAddressThatOpenedTooManyAccountsIsRefused(t *testing.T) {
	router := emailPolicyRouter(t)
	email := uniqueLocal("reuse") + "@gmail.com"
	cleanupAddress(t, email)

	for i := 1; i < services.MaxAccountsPerAddress; i++ {
		if err := services.RecordSignup(config.DB, email, 0); err != nil {
			t.Fatal(err)
		}
	}
	rec := postJSON(router, "/register/request", `{"name":"Rina","email":"`+email+`","password":"a-long-and-good-password-77"}`)
	if codeOf(t, rec.Body.Bytes()) == services.EmailReusedCode {
		t.Fatalf("below the limit the address was refused: %s", rec.Body.String())
	}

	if err := services.RecordSignup(config.DB, email, 0); err != nil {
		t.Fatal(err)
	}
	// Written differently, it is still the same mailbox.
	spelled := email[:2] + "." + email[2:]
	rec = postJSON(router, "/register/request", `{"name":"Rina","email":"`+spelled+`","password":"a-long-and-good-password-77"}`)
	if rec.Code != http.StatusBadRequest || codeOf(t, rec.Body.Bytes()) != services.EmailReusedCode {
		t.Fatalf("at the limit = %d %s, want 400 email_reused", rec.Code, rec.Body.String())
	}

	// Records older than the window no longer count.
	config.DB.Model(&models.EmailSignup{}).
		Where("key_hash = ?", emailpolicy.SignupKey(email, emailpolicy.Secret())).
		Update("created_at", time.Now().Add(-services.SignupHistoryWindow-time.Hour))
	rec = postJSON(router, "/register/request", `{"name":"Rina","email":"`+email+`","password":"a-long-and-good-password-77"}`)
	if codeOf(t, rec.Body.Bytes()) == services.EmailReusedCode {
		t.Fatalf("old records still counted: %s", rec.Body.String())
	}
}

// Every account that is opened is recorded for its mailbox.
func TestOpeningAnAccountIsRecorded(t *testing.T) {
	router := emailPolicyRouter(t)
	email := uniqueLocal("record") + "+tag@gmail.com"
	cleanupAddress(t, email)

	rec := postJSON(router, "/register", `{"name":"Rina","email":"`+email+`","password":"a-long-and-good-password-77"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register = %d %s", rec.Code, rec.Body.String())
	}
	var records int64
	config.DB.Model(&models.EmailSignup{}).Where("key_hash = ?", emailpolicy.SignupKey(email, emailpolicy.Secret())).Count(&records)
	if records != 1 {
		t.Fatalf("%d signup records, want 1", records)
	}
	var user models.User
	if err := config.DB.Where("email = ?", email).First(&user).Error; err != nil || user.CanonicalEmail != emailpolicy.Canonical(email) {
		t.Fatalf("user %+v, err %v", user, err)
	}
}
