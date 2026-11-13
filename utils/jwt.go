package utils

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// MinJWTSecretLength is the shortest signing secret accepted, in every environment.
// HMAC accepts any key, even an empty one, so a missing secret would otherwise let
// anyone sign their own tokens (with any role).
const MinJWTSecretLength = 32

// ErrWeakJWTSecret is returned when a signing secret is missing or too short.
var ErrWeakJWTSecret = errors.New("JWT secret is missing or too short")

// JWTSecret returns the signing secret held in the named variable, or
// ErrWeakJWTSecret when it is shorter than MinJWTSecretLength.
func JWTSecret(name string) ([]byte, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if len(value) < MinJWTSecretLength {
		return nil, fmt.Errorf("%w: %s", ErrWeakJWTSecret, name)
	}
	return []byte(value), nil
}

// AccessTokenSecret is the key access tokens are signed and checked with.
func AccessTokenSecret() ([]byte, error) { return JWTSecret("JWT_ACCESS_SECRET") }

// RefreshTokenSecret is the key refresh tokens are signed and checked with.
func RefreshTokenSecret() ([]byte, error) { return JWTSecret("JWT_REFRESH_SECRET") }

type Claims struct {
	UserID uint   `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

func GenerateAccessToken(userID uint, email string, role string) (string, error) {
	secret, err := AccessTokenSecret()
	if err != nil {
		return "", err
	}

	claims := Claims{
		UserID: userID,
		Email:  email,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(
				time.Now().Add(15 * time.Minute),
			),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(
		jwt.SigningMethodHS256,
		claims,
	)

	return token.SignedString(secret)
}

func GenerateRefreshToken(userID uint, email string, role string) (string, error) {
	secret, err := RefreshTokenSecret()
	if err != nil {
		return "", err
	}

	claims := Claims{
		UserID: userID,
		Email:  email,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(
				time.Now().Add(7 * 24 * time.Hour),
			),
			IssuedAt: jwt.NewNumericDate(time.Now()),
			// Without it, two tokens issued for the same user in the same second are
			// byte-identical, and the second one cannot be stored (its hash is unique).
			ID: newTokenID(),
		},
	}

	token := jwt.NewWithClaims(
		jwt.SigningMethodHS256,
		claims,
	)

	return token.SignedString(secret)
}

func ParseRefreshToken(tokenString string) (*Claims, error) {
	secret, err := RefreshTokenSecret()
	if err != nil {
		return nil, err
	}

	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(token *jwt.Token) (interface{}, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, jwt.ErrSignatureInvalid
			}

			return secret, nil
		},
	)

	if err != nil || !token.Valid {
		return nil, jwt.ErrTokenInvalidClaims
	}

	claims, ok := token.Claims.(*Claims)

	if !ok {
		return nil, jwt.ErrTokenInvalidClaims
	}

	return claims, nil
}

func newTokenID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
