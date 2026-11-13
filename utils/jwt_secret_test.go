package utils

import (
	"errors"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestTokensAreNeverSignedOrCheckedWithAnEmptyKey(t *testing.T) {
	t.Setenv("JWT_ACCESS_SECRET", "")
	t.Setenv("JWT_REFRESH_SECRET", " ")
	if _, err := GenerateAccessToken(1, "a@example.com", "admin"); !errors.Is(err, ErrJWTSecretMissing) {
		t.Fatalf("access token with an empty key: err = %v", err)
	}
	if _, err := GenerateRefreshToken(1, "a@example.com", "admin"); !errors.Is(err, ErrJWTSecretMissing) {
		t.Fatalf("refresh token with an empty key: err = %v", err)
	}
	forged, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{UserID: 1, Role: "admin"}).SignedString([]byte(""))
	if _, err := ParseRefreshToken(forged); err == nil {
		t.Fatal("a token signed with an empty key was accepted")
	}
}
