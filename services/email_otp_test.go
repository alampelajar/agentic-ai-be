package services

import (
	"strconv"
	"strings"
	"testing"
)

const testPepper = "a-random-test-pepper-with-more-than-32-characters"

func TestGenerateOTPIsSixDigits(t *testing.T) {
	for i := 0; i < 200; i++ {
		code, err := GenerateOTP()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 6 {
			t.Fatalf("code %q is not 6 characters", code)
		}
		if _, err := strconv.Atoi(code); err != nil {
			t.Fatalf("code %q is not numeric", code)
		}
	}
}

func TestHashOTPIsScopedToPurposeEmailAndPepper(t *testing.T) {
	base := HashOTP("register", "a@example.com", "123456", testPepper)
	if base != HashOTP("register", "a@example.com", "123456", testPepper) {
		t.Fatal("hash must be stable")
	}
	for name, other := range map[string]string{
		"purpose": HashOTP("change_email", "a@example.com", "123456", testPepper),
		"email":   HashOTP("register", "b@example.com", "123456", testPepper),
		"code":    HashOTP("register", "a@example.com", "654321", testPepper),
		"pepper":  HashOTP("register", "a@example.com", "123456", "another-test-pepper-with-more-than-32-chars"),
	} {
		if other == base {
			t.Errorf("hash must change with the %s", name)
		}
	}
	if len(base) != 64 {
		t.Errorf("hash length = %d, want 64 (fits the otp_hash column)", len(base))
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got, ok := NormalizeEmail("  User@Example.COM "); !ok || got != "user@example.com" {
		t.Fatalf("got %q, %v", got, ok)
	}
	for _, bad := range []string{"", "no-at-sign", "Name <a@b.com>", "a@b.com, c@d.com"} {
		if _, ok := NormalizeEmail(bad); ok {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestOTPPepperPrefersOTPPepperAndNeedsLength(t *testing.T) {
	t.Setenv("OTP_PEPPER", "")
	t.Setenv("PASSWORD_RESET_PEPPER", "")
	if OTPPepper() != "" {
		t.Fatal("no pepper configured must give an empty result")
	}
	t.Setenv("PASSWORD_RESET_PEPPER", "short")
	if OTPPepper() != "" {
		t.Fatal("a pepper under 32 characters must be refused")
	}
	t.Setenv("PASSWORD_RESET_PEPPER", testPepper)
	if OTPPepper() != testPepper {
		t.Fatal("PASSWORD_RESET_PEPPER must be the fallback")
	}
	other := strings.Repeat("x", 40)
	t.Setenv("OTP_PEPPER", other)
	if OTPPepper() != other {
		t.Fatal("OTP_PEPPER must win over the fallback")
	}
}

func TestOTPEmailContainsCodeAndEscapesHTML(t *testing.T) {
	for _, purpose := range []string{"register", "change_email"} {
		subject, htmlBody, textBody := OTPEmail(purpose, "123456")
		if subject == "" || !strings.Contains(htmlBody, "123456") || !strings.Contains(textBody, "123456") {
			t.Errorf("%s: subject/body must carry the code", purpose)
		}
	}
	_, htmlBody, _ := OTPEmail("register", "<script>")
	if strings.Contains(htmlBody, "<script>") {
		t.Error("the code must be HTML-escaped")
	}
}
