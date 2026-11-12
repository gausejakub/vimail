package worker

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/config"
)

// SMTPWorker handles sending email via SMTP.
type SMTPWorker struct {
	acct  config.AccountConfig
	creds *auth.Credentials
}

// NewSMTPWorker creates a new SMTP worker.
func NewSMTPWorker(acct config.AccountConfig, creds *auth.Credentials) *SMTPWorker {
	return &SMTPWorker{
		acct:  acct,
		creds: creds,
	}
}

// Send sends an email and returns the message ID and the raw message bytes
// (for IMAP APPEND to Sent folder).
func (w *SMTPWorker) Send(req SendRequest) (string, []byte, error) {
	host := w.acct.SMTPHost
	port := w.acct.SMTPPort
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	// Build RFC 5322 message.
	msgID, rawMsg := ComposeRFC5322(req.From, req.To, req.Subject, req.Body)

	tlsMode := w.acct.TLS
	if tlsMode == "" || (tlsMode == "tls" && port == 587) {
		tlsMode = "starttls" // SMTP typically uses STARTTLS on port 587.
	}

	var conn net.Conn
	var err error

	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	switch tlsMode {
	case "tls":
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	case "starttls":
		conn, err = dialer.Dial("tcp", addr) // Plaintext initially; upgraded to TLS below.
	default:
		return "", nil, fmt.Errorf("SMTP TLS mode %q not supported — use \"tls\" or \"starttls\"", tlsMode)
	}
	if err != nil {
		return "", nil, fmt.Errorf("SMTP connect to %s: %w", addr, err)
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return "", nil, fmt.Errorf("SMTP client: %w", err)
	}
	defer client.Close()

	// STARTTLS if needed.
	if tlsMode == "starttls" {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return "", nil, fmt.Errorf("SMTP STARTTLS: %w", err)
		}
	}

	// Authenticate.
	if err := w.authenticateSMTP(client); err != nil {
		return "", nil, fmt.Errorf("SMTP auth: %w", err)
	}

	// Send.
	if err := client.Mail(req.From); err != nil {
		return "", nil, fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	for _, rcpt := range recipientAddresses(req.To) {
		if err := client.Rcpt(rcpt); err != nil {
			return "", nil, fmt.Errorf("SMTP RCPT TO: %w", err)
		}
	}
	wc, err := client.Data()
	if err != nil {
		return "", nil, fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := wc.Write(rawMsg); err != nil {
		wc.Close()
		return "", nil, fmt.Errorf("SMTP write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return "", nil, fmt.Errorf("SMTP close data: %w", err)
	}

	if err := client.Quit(); err != nil {
		// Non-fatal — message was already sent.
	}

	return msgID, rawMsg, nil
}

func (w *SMTPWorker) authenticateSMTP(client *smtp.Client) error {
	switch w.creds.AuthMethod {
	case auth.AuthOAuth2Outlook:
		token, err := w.creds.AccessToken()
		if err != nil {
			return err
		}
		return smtpAuthSASL(client, newXOAuth2Client(w.creds.Username, token))
	case auth.AuthOAuth2Gmail:
		token, err := w.creds.AccessToken()
		if err != nil {
			return err
		}
		saslClient := sasl.NewOAuthBearerClient(&sasl.OAuthBearerOptions{
			Username: w.creds.Username,
			Token:    token,
		})
		return smtpAuthSASL(client, saslClient)
	default:
		auth := smtp.PlainAuth("", w.creds.Username, w.creds.Password, w.acct.SMTPHost)
		return client.Auth(auth)
	}
}

type xOAuth2Client struct {
	username, token string
	started         bool
}

func newXOAuth2Client(username, token string) sasl.Client {
	return &xOAuth2Client{username: username, token: token}
}
func (c *xOAuth2Client) Start() (string, []byte, error) {
	c.started = true
	return "XOAUTH2", []byte("user=" + c.username + "\x01auth=Bearer " + c.token + "\x01\x01"), nil
}
func (c *xOAuth2Client) Next([]byte) ([]byte, error) {
	if !c.started {
		return nil, errors.New("XOAUTH2 exchange not started")
	}
	return nil, nil
}

// smtpAuthSASL adapts go-sasl to net/smtp.
func smtpAuthSASL(client *smtp.Client, saslClient sasl.Client) error {
	mech, ir, err := saslClient.Start()
	if err != nil {
		return err
	}
	// Use SMTP AUTH command directly.
	// net/smtp doesn't natively support XOAUTH2, so we use the lower-level approach.
	code, msg, err := sendCommand(client, fmt.Sprintf("AUTH %s %s", mech, encodeBase64(ir)))
	if err != nil {
		return err
	}
	if code == 235 {
		return nil // Auth successful.
	}
	return fmt.Errorf("SMTP AUTH failed: %d %s", code, msg)
}

func sendCommand(client *smtp.Client, cmd string) (int, string, error) {
	// Use the text proto writer from the smtp client.
	// This is a simplified approach — in practice we'd use client.Text.
	id, err := client.Text.Cmd("%s", cmd)
	if err != nil {
		return 0, "", err
	}
	client.Text.StartResponse(id)
	defer client.Text.EndResponse(id)
	code, msg, err := client.Text.ReadResponse(235)
	return code, msg, err
}

// recipientAddresses turns a To value ("a@x, Name <b@y>") into bare envelope
// addresses, one RCPT each. An unparseable value is passed through unchanged
// so the server reports the error.
func recipientAddresses(to string) []string {
	list, err := mail.ParseAddressList(to)
	if err != nil || len(list) == 0 {
		return []string{strings.TrimSpace(to)}
	}
	out := make([]string, 0, len(list))
	for _, addr := range list {
		out = append(out, addr.Address)
	}
	return out
}
