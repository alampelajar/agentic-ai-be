package utils

import (
	"errors"
	"os"
	"strings"
)

// ErrJWTSecretMissing is returned instead of signing or checking a token with an empty key.
// An empty HS256 key is public knowledge, so anyone could make tokens for any user or role.
var ErrJWTSecretMissing = errors.New("JWT secret is not set")

// AccessSecret is JWT_ACCESS_SECRET, or an error when it is empty.
func AccessSecret() ([]byte, error) { return jwtSecret("JWT_ACCESS_SECRET") }

// RefreshSecret is JWT_REFRESH_SECRET, or an error when it is empty.
func RefreshSecret() ([]byte, error) { return jwtSecret("JWT_REFRESH_SECRET") }

func jwtSecret(name string) ([]byte, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil, ErrJWTSecretMissing
	}
	return []byte(value), nil
}
