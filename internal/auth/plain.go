package auth

import (
	"fmt"

	"github.com/gausejakub/vimail/internal/config"
)

// PlainResolver resolves credentials for plain/app-password authentication
// by retrieving the password from the OS keyring.
type PlainResolver struct{}

func (r *PlainResolver) Resolve(acct config.AccountConfig) (*Credentials, error) {
	username := acct.Username
	if username == "" {
		username = acct.Email
	}

	password, err := GetPassword(acct.Email)
	if err != nil {
		return nil, fmt.Errorf("no password found in keyring for %s: %w (run 'vimail setup' to configure)", acct.Email, err)
	}

	return &Credentials{
		Username:   username,
		Password:   password,
		AuthMethod: ParseAuthMethod(acct.AuthMethod),
	}, nil
}
