package worker

import (
	"fmt"

	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/config"
)

// AccountCheck is the result of testing an account's settings.
type AccountCheck struct {
	// Folders maps the display name of every folder to its server name
	// (roles resolved as during sync), e.g. "Sent" → "[Gmail]/Gesendet".
	Folders map[string]string
	IMAPErr error
	SMTPErr error
}

// OK reports whether both servers accepted the login.
func (c AccountCheck) OK() bool { return c.IMAPErr == nil && c.SMTPErr == nil }

// CheckAccount logs in to the account's IMAP and SMTP servers with the given
// credentials and lists its folders. Nothing is cached, synced or sent.
func CheckAccount(acct config.AccountConfig, creds *auth.Credentials) AccountCheck {
	var out AccountCheck
	if acct.IMAPHost == "" {
		out.IMAPErr = fmt.Errorf("no IMAP host")
	} else {
		w := NewIMAPWorker(acct, creds, nil)
		client, err := w.dial(false)
		if err != nil {
			out.IMAPErr = err
		} else {
			mailboxes, err := client.List("", "*", listOptions(client)).Collect()
			if err != nil {
				out.IMAPErr = fmt.Errorf("LIST: %w", err)
			} else {
				out.Folders = map[string]string{}
				for mailbox, name := range assignFolderNames(listedFrom(mailboxes), acct.Folders) {
					out.Folders[name] = mailbox
				}
			}
			client.Close()
		}
	}
	if acct.SMTPHost == "" {
		out.SMTPErr = fmt.Errorf("no SMTP host")
	} else {
		out.SMTPErr = NewSMTPWorker(acct, creds).Check()
	}
	return out
}
