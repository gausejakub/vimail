package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/gausejakub/vimail/internal/config"
	"golang.org/x/oauth2"
)

// Set at release time after registering vimail as a Microsoft public client.
// It is intentionally not populated with another application's credential.
var BuiltinOutlookClientID string

const gmailLoopbackAddress = "127.0.0.1:53682"

var (
	gmailEndpoint                 = oauth2.Endpoint{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token"}
	outlookEndpoint               = oauth2.Endpoint{AuthURL: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize", TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token", DeviceAuthURL: "https://login.microsoftonline.com/common/oauth2/v2.0/devicecode"}
	listenLoopback                = func(network, address string) (net.Listener, error) { return net.Listen(network, address) }
	openAuthURL                   = openBrowser
	setupOutput         io.Writer = os.Stdout
	getTokenPassword              = GetPassword
	storeTokenPassword            = StorePassword
	deleteTokenPassword           = DeletePassword
)

type OAuth2Resolver struct{}

func oauthConfig(acct config.AccountConfig) *oauth2.Config {
	method := ParseAuthMethod(acct.AuthMethod)
	clientID, secret := acct.OAuthClientID, acct.OAuthClientSecret
	if method == AuthOAuth2Outlook && clientID == "" {
		clientID = BuiltinOutlookClientID
	}
	cfg := &oauth2.Config{ClientID: clientID, ClientSecret: secret}
	if method == AuthOAuth2Outlook {
		cfg.Scopes = []string{"https://outlook.office365.com/IMAP.AccessAsUser.All", "https://outlook.office365.com/SMTP.Send", "offline_access"}
		cfg.Endpoint = outlookEndpoint
	} else {
		cfg.Scopes = []string{"https://mail.google.com/"}
		cfg.Endpoint = gmailEndpoint
		cfg.RedirectURL = "http://" + gmailLoopbackAddress + "/oauth/callback"
	}
	return cfg
}

func (r *OAuth2Resolver) Resolve(acct config.AccountConfig) (*Credentials, error) {
	username := acct.Username
	if username == "" {
		username = acct.Email
	}
	method := ParseAuthMethod(acct.AuthMethod)
	key := acct.Email + ":oauth2-token"
	raw, err := getTokenPassword(key)
	if err != nil || raw == "" {
		return nil, fmt.Errorf("no OAuth2 token found for %s (run 'vimail setup' to authenticate)", acct.Email)
	}
	var old oauth2.Token
	if err := json.Unmarshal([]byte(raw), &old); err != nil {
		return nil, fmt.Errorf("invalid OAuth2 token for %s (run 'vimail setup'): %w", acct.Email, err)
	}
	newTok, err := oauthConfig(acct).TokenSource(context.Background(), &old).Token()
	if err != nil {
		if isInvalidGrant(err) {
			_ = deleteTokenPassword(key)
			return nil, fmt.Errorf("OAuth2 authorization was revoked for %s; cached token cleared — run 'vimail setup': %w", acct.Email, err)
		}
		return nil, fmt.Errorf("refresh OAuth2 token for %s: %w", acct.Email, err)
	}
	if tokenChanged(&old, newTok) {
		if err := cacheOAuthToken(key, newTok); err != nil {
			return nil, fmt.Errorf("persist refreshed OAuth2 token: %w", err)
		}
	}
	return &Credentials{
		Username:   username,
		AuthMethod: method,
		Token:      newTok.AccessToken,
		Expiry:     newTok.Expiry,
		refresh:    func() (*Credentials, error) { return r.Resolve(acct) },
	}, nil
}

func (r *OAuth2Resolver) RunFlow(acct config.AccountConfig) (*oauth2.Token, error) {
	if ParseAuthMethod(acct.AuthMethod) == AuthOAuth2Gmail {
		return r.RunAuthorizationCodeFlow(acct)
	}
	return r.RunDeviceFlow(acct)
}

func (r *OAuth2Resolver) RunDeviceFlow(acct config.AccountConfig) (*oauth2.Token, error) {
	cfg := oauthConfig(acct)
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("OAuth2 client ID not configured for %s — set oauth_client_id in the account config", acct.AuthMethod)
	}
	ctx := context.Background()
	resp, err := cfg.DeviceAuth(ctx)
	if err != nil {
		return nil, fmt.Errorf("device authorization request failed: %w", err)
	}
	fmt.Fprintf(setupOutput, "\nTo authorize vimail, visit: %s\nEnter code: %s\n\nWaiting for authorization...\n", resp.VerificationURI, resp.UserCode)
	tok, err := cfg.DeviceAccessToken(ctx, resp, oauth2.AccessTypeOffline)
	if err != nil {
		return nil, fmt.Errorf("device flow failed: %w", err)
	}
	if err := cacheOAuthToken(acct.Email+":oauth2-token", tok); err != nil {
		return nil, err
	}
	return tok, nil
}

type authCodeResult struct {
	code string
	err  error
}

func (r *OAuth2Resolver) RunAuthorizationCodeFlow(acct config.AccountConfig) (*oauth2.Token, error) {
	cfg := oauthConfig(acct)
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("Gmail OAuth2 requires oauth_client_id in the account config (see README)")
	}
	listener, err := listenLoopback("tcp", gmailLoopbackAddress)
	if err != nil {
		return nil, fmt.Errorf("start OAuth callback listener on %s: %w", gmailLoopbackAddress, err)
	}
	defer listener.Close()
	cfg.RedirectURL = "http://" + listener.Addr().String() + "/oauth/callback"
	verifier, state := oauth2.GenerateVerifier(), oauth2.GenerateVerifier()
	result := make(chan authCodeResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/callback", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("state") != state {
			// Reject but keep waiting: any local page can hit the loopback
			// port, and a forged request must not abort the real sign-in.
			http.Error(w, "OAuth state mismatch", http.StatusBadRequest)
			return
		}
		if oauthErr := req.URL.Query().Get("error"); oauthErr != "" {
			http.Error(w, "OAuth authorization failed", http.StatusBadRequest)
			select {
			case result <- authCodeResult{err: fmt.Errorf("OAuth authorization failed: %s", oauthErr)}:
			default:
			}
			return
		}
		code := req.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Missing authorization code", http.StatusBadRequest)
			select {
			case result <- authCodeResult{err: errors.New("OAuth callback missing code")}:
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = template.Must(template.New("ok").Parse(`<!doctype html><meta name="color-scheme" content="dark"><style>body{background:#111;color:#eee;font:18px system-ui;display:grid;place-items:center;height:100vh;margin:0}</style><p>Authenticated — you can close this tab.</p><script>window.close()</script>`)).Execute(w, nil)
		select {
		case result <- authCodeResult{code: code}:
		default:
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		<-serveDone
	}()
	url := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"), oauth2.S256ChallengeOption(verifier))
	fmt.Fprintf(setupOutput, "\nOpen this URL to authorize vimail:\n%s\n\nWaiting for browser callback on %s...\n", url, cfg.RedirectURL)
	_ = openAuthURL(url)
	select {
	case got := <-result:
		if got.err != nil {
			return nil, got.err
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		tok, err := cfg.Exchange(ctx, got.code, oauth2.VerifierOption(verifier))
		if err != nil {
			return nil, fmt.Errorf("exchange OAuth authorization code (PKCE): %w", err)
		}
		if err := cacheOAuthToken(acct.Email+":oauth2-token", tok); err != nil {
			return nil, err
		}
		return tok, nil
	case <-time.After(5 * time.Minute):
		return nil, errors.New("OAuth callback timed out after 5 minutes")
	}
}

func cacheOAuthToken(key string, tok *oauth2.Token) error {
	data, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	return storeTokenPassword(key, string(data))
}
func tokenChanged(a, b *oauth2.Token) bool {
	return a.AccessToken != b.AccessToken || a.RefreshToken != b.RefreshToken || !a.Expiry.Equal(b.Expiry)
}
func isInvalidGrant(err error) bool {
	var retrieve *oauth2.RetrieveError
	return (errors.As(err, &retrieve) && retrieve.ErrorCode == "invalid_grant") || strings.Contains(strings.ToLower(err.Error()), "invalid_grant")
}
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
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
