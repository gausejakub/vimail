package auth

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gausejakub/vimail/internal/config"
	"golang.org/x/oauth2"
)

func TestOAuthConfigUsesPerAccountCredentialsAndProviderFlow(t *testing.T) {
	gmail := oauthConfig(config.AccountConfig{AuthMethod: "oauth2-gmail", OAuthClientID: "google-id", OAuthClientSecret: "secret"})
	if gmail.ClientID != "google-id" || gmail.RedirectURL != "http://"+gmailLoopbackAddress+"/oauth/callback" || gmail.Endpoint.DeviceAuthURL != "" {
		t.Fatalf("gmail config=%+v", gmail)
	}
	outlook := oauthConfig(config.AccountConfig{AuthMethod: "oauth2-outlook", OAuthClientID: "microsoft-id"})
	if outlook.ClientID != "microsoft-id" || outlook.Endpoint.DeviceAuthURL == "" {
		t.Fatalf("outlook config=%+v", outlook)
	}
}

func TestInvalidGrantDetection(t *testing.T) {
	if !isInvalidGrant(&oauth2.RetrieveError{ErrorCode: "invalid_grant"}) || !isInvalidGrant(errors.New("token: invalid_grant")) {
		t.Fatal("invalid_grant not recognized")
	}
}

func TestOAuthSASLMechanism(t *testing.T) {
	if OAuthSASLMechanism(AuthOAuth2Outlook) != "XOAUTH2" || OAuthSASLMechanism(AuthOAuth2Gmail) != "OAUTHBEARER" {
		t.Fatal("wrong provider SASL mechanism")
	}
}

func TestGmailPKCELoopbackPersistsTokenAndPrintsURL(t *testing.T) {
	oldEndpoint, oldListen, oldOpen, oldStore, oldOutput := gmailEndpoint, listenLoopback, openAuthURL, storeTokenPassword, setupOutput
	t.Cleanup(func() {
		gmailEndpoint = oldEndpoint
		listenLoopback = oldListen
		openAuthURL = oldOpen
		storeTokenPassword = oldStore
		setupOutput = oldOutput
	})
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code_verifier") == "" {
			t.Error("PKCE verifier missing")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"access","refresh_token":"refresh","token_type":"Bearer","expires_in":3600}`)
	}))
	defer tokenServer.Close()
	gmailEndpoint = oauth2.Endpoint{AuthURL: "https://accounts.example/auth", TokenURL: tokenServer.URL}
	var callback string
	listenLoopback = func(network, address string) (net.Listener, error) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			callback = "http://" + l.Addr().String() + "/oauth/callback"
		}
		return l, err
	}
	var output bytes.Buffer
	setupOutput = &output
	var stored string
	storeTokenPassword = func(key, value string) error { stored = value; return nil }
	openAuthURL = func(authURL string) error {
		parsed, _ := url.Parse(authURL)
		state := parsed.Query().Get("state")
		if parsed.Query().Get("code_challenge") == "" {
			t.Error("PKCE challenge missing")
		}
		go http.Get(callback + "?state=" + url.QueryEscape(state) + "&code=ok")
		return errors.New("headless")
	}
	tok, err := (&OAuth2Resolver{}).RunAuthorizationCodeFlow(config.AccountConfig{Email: "alice@gmail.com", AuthMethod: "oauth2-gmail", OAuthClientID: "client"})
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "access" || !strings.Contains(stored, "refresh") || !strings.Contains(output.String(), "https://accounts.example/auth") {
		t.Fatalf("token=%+v stored=%q output=%q", tok, stored, output.String())
	}
}

func TestGmailLoopbackRejectsStateMismatch(t *testing.T) {
	oldListen, oldOpen := listenLoopback, openAuthURL
	t.Cleanup(func() { listenLoopback = oldListen; openAuthURL = oldOpen })
	var callback string
	listenLoopback = func(network, address string) (net.Listener, error) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			callback = "http://" + l.Addr().String() + "/oauth/callback"
		}
		return l, err
	}
	mismatchStatus := make(chan int, 1)
	openAuthURL = func(authURL string) error {
		go func() {
			// A forged callback is rejected without ending the flow...
			resp, err := http.Get(callback + "?state=wrong&code=ok")
			if err != nil {
				mismatchStatus <- 0
				return
			}
			resp.Body.Close()
			mismatchStatus <- resp.StatusCode
			// ...and the genuine callback (here: a user denial) still lands.
			parsed, _ := url.Parse(authURL)
			resp, err = http.Get(callback + "?state=" + url.QueryEscape(parsed.Query().Get("state")) + "&error=access_denied")
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	_, err := (&OAuth2Resolver{}).RunAuthorizationCodeFlow(config.AccountConfig{Email: "alice@gmail.com", AuthMethod: "oauth2-gmail", OAuthClientID: "client"})
	if status := <-mismatchStatus; status != http.StatusBadRequest {
		t.Fatalf("state-mismatch status = %d, want 400", status)
	}
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("error=%v, want the genuine callback's access_denied", err)
	}
}

func TestResolvePersistsRotationAndClearsInvalidGrant(t *testing.T) {
	oldEndpoint, oldGet, oldStore, oldDelete := outlookEndpoint, getTokenPassword, storeTokenPassword, deleteTokenPassword
	t.Cleanup(func() {
		outlookEndpoint = oldEndpoint
		getTokenPassword = oldGet
		storeTokenPassword = oldStore
		deleteTokenPassword = oldDelete
	})
	expired := `{"access_token":"old","refresh_token":"refresh","token_type":"Bearer","expiry":"2000-01-01T00:00:00Z"}`
	getTokenPassword = func(string) (string, error) { return expired, nil }
	var stored, deleted bool
	storeTokenPassword = func(string, string) error { stored = true; return nil }
	deleteTokenPassword = func(string) error { deleted = true; return nil }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"new","refresh_token":"rotated","token_type":"Bearer","expires_in":3600}`)
	}))
	defer server.Close()
	outlookEndpoint.TokenURL = server.URL
	_, err := (&OAuth2Resolver{}).Resolve(config.AccountConfig{Email: "alice@outlook.com", AuthMethod: "oauth2-outlook", OAuthClientID: "client"})
	if err != nil || !stored {
		t.Fatalf("rotation err=%v stored=%v", err, stored)
	}
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		io.WriteString(w, `{"error":"invalid_grant"}`)
	})
	_, err = (&OAuth2Resolver{}).Resolve(config.AccountConfig{Email: "alice@outlook.com", AuthMethod: "oauth2-outlook", OAuthClientID: "client"})
	if err == nil || !deleted || !strings.Contains(err.Error(), "run 'vimail setup'") {
		t.Fatalf("invalid grant err=%v deleted=%v", err, deleted)
	}
}
