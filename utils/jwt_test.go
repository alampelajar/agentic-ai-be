package utils

import (
	"errors"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// HMAC accepts an empty key, so a missing secret must be refused here rather than
// silently signing (and accepting) tokens anyone could make.
func TestTokensRefuseMissingOrShortSecrets(t *testing.T) {
	for name, value := range map[string]string{"missing": "", "short": "too-short", "spaces": strings.Repeat(" ", 40)} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("JWT_ACCESS_SECRET", value)
			t.Setenv("JWT_REFRESH_SECRET", value)
			if _, err := GenerateAccessToken(1, "a@example.test", "admin"); !errors.Is(err, ErrWeakJWTSecret) {
				t.Errorf("access token signed with a %s secret: err = %v", name, err)
			}
			if _, err := GenerateRefreshToken(1, "a@example.test", "admin"); !errors.Is(err, ErrWeakJWTSecret) {
				t.Errorf("refresh token signed with a %s secret: err = %v", name, err)
			}
			forged, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{UserID: 1, Role: "admin"}).SignedString([]byte(value))
			if _, err := ParseRefreshToken(forged); err == nil {
				t.Errorf("a token signed with the %s secret was accepted", name)
			}
		})
	}
}

func TestTokensWorkWithStrongSecrets(t *testing.T) {
	t.Setenv("JWT_ACCESS_SECRET", strings.Repeat("a", 40))
	t.Setenv("JWT_REFRESH_SECRET", strings.Repeat("b", 40))
	if _, err := GenerateAccessToken(1, "a@example.test", "user"); err != nil {
		t.Fatal(err)
	}
	refresh, err := GenerateRefreshToken(7, "a@example.test", "user")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseRefreshToken(refresh)
	if err != nil || claims.UserID != 7 {
		t.Fatalf("claims = %+v, err = %v", claims, err)
	}
}
