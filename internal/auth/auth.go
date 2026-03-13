package auth

import (
	"fmt"

	"github.com/gausejakub/vimail/internal/config"
)

// AuthMethod identifies the authentication mechanism.
type AuthMethod int

const (
	AuthPlain         AuthMethod = iota // LOGIN with password
	AuthAppPassword                     // App-specific password (same as plain mechanically)
	AuthOAuth2Gmail                     // OAuth2 device flow for Gmail
	AuthOAuth2Outlook                   // OAuth2 device flow for Outlook/O365
)

// Credentials holds the resolved authentication data for a connection.
type Credentials struct {
	Username   string
	Password   string     // For plain/app-password
	AuthMethod AuthMethod // How to authenticate
	Token      string     // OAuth2 access token (when applicable)
}

// String returns a safe representation without secrets.
func (c *Credentials) String() string {
	return fmt.Sprintf("Credentials{User:%s, Method:%v}", c.Username, c.AuthMethod)
}

// Resolver resolves credentials for an account.
type Resolver interface {
	Resolve(acct config.AccountConfig) (*Credentials, error)
}

// ParseAuthMethod converts a config string to an AuthMethod.
func ParseAuthMethod(s string) AuthMethod {
	switch s {
	case "app-password":
		return AuthAppPassword
	case "oauth2-gmail":
		return AuthOAuth2Gmail
	case "oauth2-outlook":
		return AuthOAuth2Outlook
	default:
		return AuthPlain
	}
}
