package auth

import (
	"encoding/base64"

	"github.com/zalando/go-keyring"
)

const serviceName = "vimail"
const cacheKeyUser = "_cache_encryption_key"

// StorePassword stores a password in the OS keyring.
func StorePassword(email, password string) error {
	return keyring.Set(serviceName, email, password)
}

// GetPassword retrieves a password from the OS keyring.
func GetPassword(email string) (string, error) {
	return keyring.Get(serviceName, email)
}

// DeletePassword removes a password from the OS keyring.
func DeletePassword(email string) error {
	return keyring.Delete(serviceName, email)
}

// GetCacheKey retrieves the cache encryption key from the OS keyring.
// Returns nil if no key is stored (encryption not yet initialized).
func GetCacheKey() ([]byte, error) {
	encoded, err := keyring.Get(serviceName, cacheKeyUser)
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(encoded)
}

// StoreCacheKey stores a cache encryption key in the OS keyring.
func StoreCacheKey(key []byte) error {
	encoded := base64.StdEncoding.EncodeToString(key)
	return keyring.Set(serviceName, cacheKeyUser, encoded)
}
