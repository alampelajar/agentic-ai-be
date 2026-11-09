package services

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math/big"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"agentic-ai-backend/models"
)

const (
	OTPCodeTTL   = 5 * time.Minute
	OTPResendGap = time.Minute
	OTPMaxTries  = 5
)

// Results of VerifyOTP. They are values, not errors, so that a wrong guess
// (which must persist its attempt counter) can still commit its transaction.
const (
	OTPVerified = "verified"
	OTPInvalid  = "invalid"
	OTPTooMany  = "too_many_attempts"
)

var (
	// ErrOTPThrottled means a code was sent less than OTPResendGap ago.
	ErrOTPThrottled = errors.New("otp: resend too soon")
	// ErrOTPNoPending means a resend was asked for but nothing is waiting.
	ErrOTPNoPending = errors.New("otp: no pending verification")
)

// NormalizeEmail lower-cases and validates an address.
func NormalizeEmail(value string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(value))
	parsed, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(parsed.Address, email) || email == "" {
		return "", false
	}
	return email, true
}

// OTPPepper is the server-side secret mixed into every code hash. OTP_PEPPER is
// preferred; PASSWORD_RESET_PEPPER is accepted so one secret can serve both.
func OTPPepper() string {
	for _, name := range []string{"OTP_PEPPER", "PASSWORD_RESET_PEPPER"} {
		if value := strings.TrimSpace(os.Getenv(name)); len(value) >= 32 {
			return value
		}
	}
	return ""
}

// EmailOTPConfig returns the settings needed to send codes, and whether all of
// them are present.
func EmailOTPConfig() (apiKey, sender, pepper string, ok bool) {
	apiKey = strings.TrimSpace(os.Getenv("RESEND_API_KEY"))
	sender = strings.TrimSpace(os.Getenv("RESEND_FROM_EMAIL"))
	pepper = OTPPepper()
	return apiKey, sender, pepper, apiKey != "" && sender != "" && pepper != ""
}

func GenerateOTP() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

// HashOTP scopes a code to its purpose and address, so a code issued for one
// flow or mailbox is useless for another.
func HashOTP(purpose, email, code, pepper string) string {
	mac := hmac.New(sha256.New, []byte(pepper))
	_, _ = mac.Write([]byte(purpose + ":" + email + ":" + code))
	return hex.EncodeToString(mac.Sum(nil))
}

// IssueOTP creates or refreshes the challenge for (purpose, email) and returns
// the plaintext code to e-mail. payload nil keeps the stored payload (resend);
// it is then an error if nothing is pending. It never sends the e-mail.
func IssueOTP(db *gorm.DB, purpose, email string, userID *uint, payload *string, pepper string) (string, error) {
	code, err := GenerateOTP()
	if err != nil {
		return "", err
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		var challenge models.OTPChallenge
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("purpose = ? AND email = ?", purpose, email).
			First(&challenge)

		switch {
		case errors.Is(result.Error, gorm.ErrRecordNotFound):
			if payload == nil {
				return ErrOTPNoPending
			}
			challenge = models.OTPChallenge{Purpose: purpose, Email: email}
		case result.Error != nil:
			return result.Error
		default:
			if now.Sub(challenge.LastSentAt) < OTPResendGap {
				return ErrOTPThrottled
			}
			if payload == nil && challenge.Payload == "" {
				return ErrOTPNoPending
			}
		}

		challenge.UserID = userID
		challenge.OTPHash = HashOTP(purpose, email, code, pepper)
		challenge.OTPExpiresAt = now.Add(OTPCodeTTL)
		challenge.Attempts = 0
		challenge.LastSentAt = now
		// A new code invalidates any reset token issued from the previous one.
		challenge.VerifiedTokenHash = ""
		challenge.VerifiedTokenExpiresAt = nil
		if payload != nil {
			challenge.Payload = *payload
		}
		return tx.Save(&challenge).Error
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

// ClearOTP invalidates the pending code (used when delivery failed).
func ClearOTP(db *gorm.DB, purpose, email string) {
	db.Model(&models.OTPChallenge{}).
		Where("purpose = ? AND email = ?", purpose, email).
		Updates(map[string]any{"otp_hash": "", "otp_expires_at": time.Now(), "last_sent_at": time.Time{}})
}

// VerifyOTP checks a code inside tx. On success the code is consumed (it can
// never be used twice) and the challenge, with its payload, is returned. A
// wrong code counts an attempt; after OTPMaxTries the code is burned. The
// caller must commit tx for the attempt counter to persist, so it should return
// nil from its transaction function for OTPInvalid and OTPTooMany.
func VerifyOTP(tx *gorm.DB, purpose, email, code, pepper string) (*models.OTPChallenge, string, error) {
	var challenge models.OTPChallenge
	result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("purpose = ? AND email = ?", purpose, email).
		First(&challenge)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, OTPInvalid, nil
	}
	if result.Error != nil {
		return nil, "", result.Error
	}

	now := time.Now()
	// A burned code keeps reporting "too many attempts" until a new one is issued.
	if challenge.Attempts >= OTPMaxTries {
		return nil, OTPTooMany, nil
	}
	if challenge.OTPHash == "" || !now.Before(challenge.OTPExpiresAt) {
		return nil, OTPInvalid, nil
	}

	expected := HashOTP(purpose, email, strings.TrimSpace(code), pepper)
	if !hmac.Equal([]byte(challenge.OTPHash), []byte(expected)) {
		challenge.Attempts++
		if challenge.Attempts >= OTPMaxTries {
			challenge.OTPHash = ""
			challenge.OTPExpiresAt = now
		}
		if err := tx.Save(&challenge).Error; err != nil {
			return nil, "", err
		}
		if challenge.Attempts >= OTPMaxTries {
			return nil, OTPTooMany, nil
		}
		return nil, OTPInvalid, nil
	}

	consumed := challenge
	challenge.OTPHash = ""
	challenge.OTPExpiresAt = now
	challenge.Attempts = 0
	challenge.Payload = ""
	if err := tx.Save(&challenge).Error; err != nil {
		return nil, "", err
	}
	return &consumed, OTPVerified, nil
}

// SendEmail delivers a message through Resend. The key, recipient and provider
// response body are never included in the returned error.
func SendEmail(ctx context.Context, apiKey, sender, recipient, subject, htmlBody, textBody string) error {
	message := struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		HTML    string   `json:"html"`
		Text    string   `json:"text"`
	}{sender, []string{recipient}, subject, htmlBody, textBody}

	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal Resend payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("create Resend request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("send Resend request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Resend returned HTTP status %d", response.StatusCode)
	}
	return nil
}

// OTPEmail builds the subject and bodies for a purpose.
func OTPEmail(purpose, code string) (subject, htmlBody, textBody string) {
	heading, intro := "Verify your Agentic AI email", "Use this code to verify your email address and finish creating your account."
	subject = "Your Agentic AI verification code"
	if purpose == models.OTPPurposePasswordReset {
		heading, intro = "Reset your Agentic AI password", "Use this verification code to continue resetting your password."
		subject = "Your Agentic AI password reset code"
	}
	if purpose == models.OTPPurposeChangeEmail {
		heading, intro = "Confirm your new email", "Use this code to confirm this address as the new email for your Agentic AI account."
		subject = "Confirm your new Agentic AI email"
	}
	htmlBody = fmt.Sprintf(`<div style="font-family:Arial,sans-serif;max-width:520px;margin:auto;color:#171717">
<h2 style="margin-bottom:12px">%s</h2>
<p>%s</p>
<p style="font-size:32px;font-weight:700;letter-spacing:8px;padding:16px 0">%s</p>
<p>This code expires in 5 minutes and can be used only once.</p>
<p style="color:#737373;font-size:13px">If you did not request this, you can safely ignore this email.</p>
</div>`, html.EscapeString(heading), html.EscapeString(intro), html.EscapeString(code))
	textBody = fmt.Sprintf("%s Your code is %s. It expires in 5 minutes and can be used only once. If you did not request this, ignore this email.", intro, code)
	return subject, htmlBody, textBody
}
