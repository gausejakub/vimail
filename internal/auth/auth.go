package auth

import (
	"fmt"
	"sync"
	"time"

	"github.com/gausejakub/vimail/internal/config"
)

// AuthMethod identifies the authentication mechanism.
type AuthMethod int

const (
	AuthPlain         AuthMethod = iota // LOGIN with password
	AuthAppPassword                     // App-specific password (same as plain mechanically)
	AuthOAuth2Gmail                     // OAuth2 authorization-code + PKCE for Gmail
	AuthOAuth2Outlook                   // OAuth2 device flow for Outlook/O365
)

// Credentials holds the resolved authentication data for a connection.
type Credentials struct {
	Username   string
	Password   string     // For plain/app-password
	AuthMethod AuthMethod // How to authenticate
	Token      string     // OAuth2 access token (when applicable)
	Expiry     time.Time  // OAuth2 access-token expiry; zero means unknown/never

	// refresh re-resolves OAuth2 credentials. Long-running processes call it
	// through AccessToken so reconnects never reuse an expired access token.
	refresh func() (*Credentials, error)
	mu      sync.Mutex
}

// oauthRefreshMargin refreshes slightly early so a token cannot expire
// between resolution and the AUTHENTICATE exchange.
const oauthRefreshMargin = 2 * time.Minute

// AccessToken returns a currently valid OAuth2 access token, refreshing it via
// the resolver when it is close to expiry. Call it right before each
// authentication instead of reading Token directly.
func (c *Credentials) AccessToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refresh == nil || c.Expiry.IsZero() || time.Until(c.Expiry) > oauthRefreshMargin {
		return c.Token, nil
	}
	fresh, err := c.refresh()
	if err != nil {
		return "", err
	}
	c.Token, c.Expiry = fresh.Token, fresh.Expiry
	return c.Token, nil
}

func OAuthSASLMechanism(method AuthMethod) string {
	if method == AuthOAuth2Outlook {
		return "XOAUTH2"
	}
	return "OAUTHBEARER"
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
