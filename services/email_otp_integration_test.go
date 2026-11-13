package services

import (
	"errors"
	"os"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/internal/testdb"
	"agentic-ai-backend/models"
)

func otpTestDB(t *testing.T) *gorm.DB {
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
	if err := testdb.Migrate(db, &models.OTPChallenge{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func verify(t *testing.T, db *gorm.DB, purpose, email, code string) (*models.OTPChallenge, string) {
	t.Helper()
	var (
		challenge *models.OTPChallenge
		result    string
	)
	if err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		challenge, result, err = VerifyOTP(tx, purpose, email, code, testPepper)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return challenge, result
}

func TestOTPLifecycle(t *testing.T) {
	db := otpTestDB(t)
	email := "otp-lifecycle-" + time.Now().Format("150405.000000") + "@example.com"
	t.Cleanup(func() { db.Where("email = ?", email).Delete(&models.OTPChallenge{}) })

	payload := `{"name":"Rina"}`
	code, err := IssueOTP(db, models.OTPPurposeRegister, email, nil, &payload, testPepper)
	if err != nil {
		t.Fatal(err)
	}

	// A second code right away is throttled.
	if _, err := IssueOTP(db, models.OTPPurposeRegister, email, nil, nil, testPepper); err != ErrOTPThrottled {
		t.Fatalf("resend gap: got %v, want ErrOTPThrottled", err)
	}

	// A code for another purpose does not unlock this one.
	if _, result := verify(t, db, models.OTPPurposeChangeEmail, email, code); result != OTPInvalid {
		t.Fatalf("other purpose: got %s", result)
	}

	// A wrong code is refused, the right one works once and returns the payload.
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if _, result := verify(t, db, models.OTPPurposeRegister, email, wrong); result != OTPInvalid {
		t.Fatalf("wrong code: got %s", result)
	}
	challenge, result := verify(t, db, models.OTPPurposeRegister, email, code)
	if result != OTPVerified || challenge.Payload != payload {
		t.Fatalf("right code: got %s, payload %q", result, challenge.Payload)
	}
	if _, result := verify(t, db, models.OTPPurposeRegister, email, code); result != OTPInvalid {
		t.Fatalf("a code must work only once, got %s", result)
	}
}

func TestOTPBurnsAfterTooManyWrongGuesses(t *testing.T) {
	db := otpTestDB(t)
	email := "otp-burn-" + time.Now().Format("150405.000000") + "@example.com"
	t.Cleanup(func() { db.Where("email = ?", email).Delete(&models.OTPChallenge{}) })

	payload := "{}"
	code, err := IssueOTP(db, models.OTPPurposeRegister, email, nil, &payload, testPepper)
	if err != nil {
		t.Fatal(err)
	}
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for i := 1; i < OTPMaxTries; i++ {
		if _, result := verify(t, db, models.OTPPurposeRegister, email, wrong); result != OTPInvalid {
			t.Fatalf("guess %d: got %s", i, result)
		}
	}
	if _, result := verify(t, db, models.OTPPurposeRegister, email, wrong); result != OTPTooMany {
		t.Fatalf("last guess: got %s, want too many attempts", result)
	}
	// Even the right code is dead now.
	if _, result := verify(t, db, models.OTPPurposeRegister, email, code); result == OTPVerified {
		t.Fatal("the code must be burned after too many wrong guesses")
	}
}

func TestResendNeedsAPendingChallenge(t *testing.T) {
	db := otpTestDB(t)
	email := "otp-none-" + time.Now().Format("150405.000000") + "@example.com"
	if _, err := IssueOTP(db, models.OTPPurposeRegister, email, nil, nil, testPepper); err != ErrOTPNoPending {
		t.Fatalf("got %v, want ErrOTPNoPending", err)
	}
}

// Each code allows a few guesses, so the number of codes per address and day is
// capped too. The resend gap is skipped here by moving LastSentAt back.
func TestOTPDailyLimitPerAddress(t *testing.T) {
	db := otpTestDB(t)
	email := "otp-daily-" + time.Now().Format("150405.000000") + "@example.com"
	t.Cleanup(func() { db.Where("email = ?", email).Delete(&models.OTPChallenge{}) })
	payload := "{}"
	for i := 0; i < OTPMaxPerDay; i++ {
		if _, err := IssueOTP(db, models.OTPPurposeRegister, email, nil, &payload, testPepper); err != nil {
			t.Fatalf("code %d: %v", i+1, err)
		}
		db.Model(&models.OTPChallenge{}).Where("email = ?", email).Update("last_sent_at", time.Now().Add(-OTPResendGap-time.Second))
	}
	if _, err := IssueOTP(db, models.OTPPurposeRegister, email, nil, &payload, testPepper); !errors.Is(err, ErrOTPDailyLimit) {
		t.Fatalf("code over the limit: err = %v, want ErrOTPDailyLimit", err)
	}
	// A day later the address can get codes again.
	db.Model(&models.OTPChallenge{}).Where("email = ?", email).Update("sent_window_start", time.Now().Add(-25*time.Hour))
	if _, err := IssueOTP(db, models.OTPPurposeRegister, email, nil, &payload, testPepper); err != nil {
		t.Fatalf("after a day: %v", err)
	}
}
