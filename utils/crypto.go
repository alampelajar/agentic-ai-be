package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func getEncryptionKey() ([]byte, error) {
	value := strings.TrimSpace(os.Getenv("AI_ENCRYPTION_KEY"))

	if value == "" {
		return nil, errors.New("AI_ENCRYPTION_KEY belum diatur")
	}

	// Format utama:
	// 64 karakter hex = 32 byte.
	if len(value) == 64 {
		key, err := hex.DecodeString(value)
		if err == nil && len(key) == 32 {
			return key, nil
		}
	}

	// Opsional: menerima base64 32 byte.
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err == nil && len(decoded) == 32 {
		return decoded, nil
	}

	return nil, errors.New(
		"AI_ENCRYPTION_KEY harus berupa 64 karakter hex atau base64 yang menghasilkan 32 byte",
	)
}

func EncryptString(plainText string) (string, error) {
	if plainText == "" {
		return "", nil
	}

	key, err := getEncryptionKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())

	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	cipherText := gcm.Seal(
		nonce,
		nonce,
		[]byte(plainText),
		nil,
	)

	return base64.StdEncoding.EncodeToString(cipherText), nil
}

func DecryptString(encryptedText string) (string, error) {
	if encryptedText == "" {
		return "", nil
	}

	key, err := getEncryptionKey()
	if err != nil {
		return "", err
	}

	data, err := base64.StdEncoding.DecodeString(encryptedText)
	if err != nil {
		return "", fmt.Errorf("gagal decode API key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()

	if len(data) < nonceSize {
		return "", errors.New("data API key terenkripsi tidak valid")
	}

	nonce := data[:nonceSize]
	cipherText := data[nonceSize:]

	plainText, err := gcm.Open(
		nil,
		nonce,
		cipherText,
		nil,
	)

	if err != nil {
		return "", err
	}

	return string(plainText), nil
}
