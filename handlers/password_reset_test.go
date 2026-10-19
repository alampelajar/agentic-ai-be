package handlers

import (
	"strconv"
	"testing"
)

func TestGeneratePasswordResetOTP(t *testing.T) {
	for i := 0; i < 100; i++ {
		otp, err := generatePasswordResetOTP()
		if err != nil {
			t.Fatalf("generatePasswordResetOTP() error = %v", err)
		}
		if len(otp) != 6 {
			t.Fatalf("OTP length = %d, want 6", len(otp))
		}
		if _, err := strconv.Atoi(otp); err != nil {
			t.Fatalf("OTP %q is not numeric: %v", otp, err)
		}
	}
}

func TestHashPasswordResetOTPIsScopedToEmail(t *testing.T) {
	const pepper = "a-random-test-pepper-with-more-than-32-characters"
	first := hashPasswordResetOTP("user@example.com", "123456", pepper)
	if first != hashPasswordResetOTP("user@example.com", "123456", pepper) {
		t.Fatal("same email, code, and pepper must produce a stable hash")
	}
	if first == hashPasswordResetOTP("other@example.com", "123456", pepper) {
		t.Fatal("OTP hash must be scoped to the email")
	}
	if first == hashPasswordResetOTP("user@example.com", "123456", "another-test-pepper-with-more-than-32-chars") {
		t.Fatal("OTP hash must be scoped to the server-side pepper")
	}
}
