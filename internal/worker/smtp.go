package worker

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"

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
	addr := fmt.Sprintf("%s:%d", host, port)

	// Build RFC 5322 message.
	msgID, rawMsg := ComposeRFC5322(req.From, req.To, req.Subject, req.Body)

	tlsMode := w.acct.TLS
	if tlsMode == "" {
		tlsMode = "starttls" // SMTP typically uses STARTTLS on port 587.
	}

	var conn net.Conn
	var err error

	switch tlsMode {
	case "tls":
		conn, err = tls.Dial("tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	default:
		conn, err = net.Dial("tcp", addr)
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
	if err := client.Rcpt(req.To); err != nil {
		return "", nil, fmt.Errorf("SMTP RCPT TO: %w", err)
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
	case auth.AuthOAuth2Gmail, auth.AuthOAuth2Outlook:
		saslClient := sasl.NewOAuthBearerClient(&sasl.OAuthBearerOptions{
			Username: w.creds.Username,
			Token:    w.creds.Token,
		})
		return smtpAuthSASL(client, saslClient)
	default:
		auth := smtp.PlainAuth("", w.creds.Username, w.creds.Password, w.acct.SMTPHost)
		return client.Auth(auth)
	}
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
