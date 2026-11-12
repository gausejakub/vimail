package auth

import (
	"fmt"

	"github.com/gausejakub/vimail/internal/config"
)

// PlainResolver resolves credentials for plain/app-password authentication:
// from the account's password_cmd when set, otherwise from the OS keyring.
type PlainResolver struct{}

func (r *PlainResolver) Resolve(acct config.AccountConfig) (*Credentials, error) {
	username := acct.Username
	if username == "" {
		username = acct.Email
	}

	var password string
	if acct.PasswordCmd != "" {
		pw, err := RunSecretCmd(acct.PasswordCmd)
		if err != nil {
			return nil, fmt.Errorf("password_cmd for %s: %w", acct.Email, err)
		}
		password = pw
	} else {
		pw, err := GetPassword(acct.Email)
		if err != nil {
			return nil, fmt.Errorf("no password found in keyring for %s: %w (run 'vimail setup' to configure, or set password_cmd)", acct.Email, err)
		}
		password = pw
	}

	return &Credentials{
		Username:   username,
		Password:   password,
		AuthMethod: ParseAuthMethod(acct.AuthMethod),
	}, nil
}
