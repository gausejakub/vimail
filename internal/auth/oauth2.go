package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gausejakub/vimail/internal/config"
	"golang.org/x/oauth2"
)

// OAuth2 client IDs. In a production app these would be your registered app credentials.
// Users need to register their own or use app passwords as an alternative.
var (
	gmailOAuth2Config = &oauth2.Config{
		Scopes: []string{
			"https://mail.google.com/",
		},
		Endpoint: oauth2.Endpoint{
			AuthURL:       "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL:      "https://oauth2.googleapis.com/token",
			DeviceAuthURL: "https://oauth2.googleapis.com/device/code",
		},
	}

	outlookOAuth2Config = &oauth2.Config{
		Scopes: []string{
			"https://outlook.office365.com/IMAP.AccessAsUser.All",
			"https://outlook.office365.com/SMTP.Send",
			"offline_access",
		},
		Endpoint: oauth2.Endpoint{
			AuthURL:       "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
			TokenURL:      "https://login.microsoftonline.com/common/oauth2/v2.0/token",
			DeviceAuthURL: "https://login.microsoftonline.com/common/oauth2/v2.0/devicecode",
		},
	}
)

// OAuth2Resolver resolves credentials via OAuth2 device authorization grant.
// Tokens are cached in the OS keyring as JSON.
type OAuth2Resolver struct{}

func (r *OAuth2Resolver) Resolve(acct config.AccountConfig) (*Credentials, error) {
	username := acct.Username
	if username == "" {
		username = acct.Email
	}

	method := ParseAuthMethod(acct.AuthMethod)

	// Try to load a cached token from keyring.
	tokenKey := acct.Email + ":oauth2-token"
	tokenJSON, err := GetPassword(tokenKey)
	if err == nil && tokenJSON != "" {
		var tok oauth2.Token
		if err := json.Unmarshal([]byte(tokenJSON), &tok); err == nil {
			cfg := r.oauthConfig(method)
			src := cfg.TokenSource(context.Background(), &tok)
			newTok, err := src.Token()
			if err == nil {
				// Cache refreshed token if it changed.
				if newTok.AccessToken != tok.AccessToken {
					r.cacheToken(tokenKey, newTok)
				}
				return &Credentials{
					Username:   username,
					AuthMethod: method,
					Token:      newTok.AccessToken,
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("no OAuth2 token found for %s (run 'vimail setup' to authenticate)", acct.Email)
}

// RunDeviceFlow runs the OAuth2 device authorization grant flow interactively.
// It prints instructions for the user and blocks until authorization completes.
func (r *OAuth2Resolver) RunDeviceFlow(acct config.AccountConfig) (*oauth2.Token, error) {
	method := ParseAuthMethod(acct.AuthMethod)
	cfg := r.oauthConfig(method)

	if cfg.ClientID == "" {
		return nil, fmt.Errorf("OAuth2 client ID not configured for %s — set client_id in config or use app-password auth instead", acct.AuthMethod)
	}

	ctx := context.Background()
	resp, err := cfg.DeviceAuth(ctx)
	if err != nil {
		return nil, fmt.Errorf("device authorization request failed: %w", err)
	}

	fmt.Printf("\nTo authorize vimail, visit: %s\n", resp.VerificationURI)
	fmt.Printf("Enter code: %s\n\n", resp.UserCode)
	fmt.Println("Waiting for authorization...")

	// Poll until user authorizes or timeout.
	tok, err := cfg.DeviceAccessToken(ctx, resp, oauth2.AccessTypeOffline)
	if err != nil {
		return nil, fmt.Errorf("device flow failed: %w", err)
	}

	// Cache the token.
	tokenKey := acct.Email + ":oauth2-token"
	r.cacheToken(tokenKey, tok)

	return tok, nil
}

func (r *OAuth2Resolver) oauthConfig(method AuthMethod) *oauth2.Config {
	switch method {
	case AuthOAuth2Outlook:
		return outlookOAuth2Config
	default:
		return gmailOAuth2Config
	}
}

func (r *OAuth2Resolver) cacheToken(key string, tok *oauth2.Token) {
	data, err := json.Marshal(tok)
	if err != nil {
		return
	}
	_ = StorePassword(key, string(data))
}

// SetClientCredentials allows configuring OAuth2 client credentials.
// Must be called before RunDeviceFlow.
func SetGmailClientCredentials(clientID, clientSecret string) {
	gmailOAuth2Config.ClientID = clientID
	gmailOAuth2Config.ClientSecret = clientSecret
}

func SetOutlookClientCredentials(clientID, clientSecret string) {
	outlookOAuth2Config.ClientID = clientID
	outlookOAuth2Config.ClientSecret = clientSecret
}

// tokenExpiry returns the remaining validity of a token in human-readable form.
func tokenExpiry(tok *oauth2.Token) string {
	if tok.Expiry.IsZero() {
		return "no expiry"
	}
	remaining := time.Until(tok.Expiry)
	if remaining < 0 {
		return "expired"
	}
	return fmt.Sprintf("expires in %s", remaining.Round(time.Minute))
}
