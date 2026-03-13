package cache

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
)

const encPrefix = "enc:"

// encrypt encrypts plaintext using AES-256-GCM and returns a base64-encoded
// string prefixed with "enc:" for storage in TEXT columns.
// Returns the original string if key is nil (encryption disabled).
func encrypt(key []byte, plaintext string) string {
	if len(key) == 0 || plaintext == "" {
		return plaintext
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return plaintext
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return plaintext
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return plaintext
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ciphertext)
}

// decrypt decrypts a value produced by encrypt. If the value doesn't have
// the "enc:" prefix, it's returned as-is (plaintext/not-yet-encrypted data).
// Returns the original string if key is nil (encryption disabled).
func decrypt(key []byte, value string) string {
	if len(key) == 0 || !strings.HasPrefix(value, encPrefix) {
		return value
	}

	data, err := base64.StdEncoding.DecodeString(value[len(encPrefix):])
	if err != nil {
		return value
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return value
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return value
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return value
	}

	plaintext, err := gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return value
	}

	return string(plaintext)
}

// GenerateEncryptionKey creates a random 256-bit key for AES-256-GCM.
func GenerateEncryptionKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, errors.New("failed to generate encryption key")
	}
	return key, nil
}
