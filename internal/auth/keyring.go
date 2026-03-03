package auth

import "github.com/zalando/go-keyring"

const serviceName = "vimail"

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
