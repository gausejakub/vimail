// Package setup adds an account interactively: the user enters an email
// address, the server settings are detected (internal/autoconfig), tested
// with the user's credentials, and written to the config file.
package setup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/autoconfig"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/worker"
)

// Flow is the interactive add-account flow. The function fields are the
// effects it has; tests replace them.
type Flow struct {
	In  *bufio.Reader
	Out io.Writer
	// ReadSecret reads a line without echo (password, client secret).
	ReadSecret func() (string, error)

	Resolver autoconfig.Resolver
	// Check logs in to both servers; it is the only step that sends the
	// user's credentials anywhere and runs only after the user has
	// confirmed the servers shown to them.
	Check func(config.AccountConfig, *auth.Credentials) worker.AccountCheck
	// OAuth runs the browser/device sign-in and returns usable credentials.
	OAuth         func(config.AccountConfig) (*auth.Credentials, error)
	StorePassword func(email, password string) error
	Append        func(config.AccountConfig) error
}

// NewFlow returns the flow wired to the terminal, the network, the OS
// keyring and the config file.
func NewFlow() *Flow {
	return &Flow{
		In:  bufio.NewReader(os.Stdin),
		Out: os.Stdout,
		ReadSecret: func() (string, error) {
			raw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			secret := strings.TrimSpace(string(raw))
			for i := range raw {
				raw[i] = 0
			}
			return secret, err
		},
		Resolver: autoconfig.NetResolver{},
		Check:    worker.CheckAccount,
		OAuth: func(acct config.AccountConfig) (*auth.Credentials, error) {
			r := &auth.OAuth2Resolver{}
			if _, err := r.RunFlow(acct); err != nil {
				return nil, err
			}
			return r.Resolve(acct)
		},
		StorePassword: auth.StorePassword,
		Append:        config.AppendAccount,
	}
}

// errAborted is returned when the user declines to continue.
var errAborted = errors.New("setup cancelled; nothing was saved")

// Add runs the flow once. existing is the current configuration, used to
// refuse an address that is already configured.
func (f *Flow) Add(ctx context.Context, existing config.Config) (config.AccountConfig, error) {
	var acct config.AccountConfig
	f.say("\n── Add an account ──")

	// 1. Address.
	address, err := f.ask("Email address", "")
	if err != nil {
		return acct, err
	}
	address = strings.ToLower(address)
	if _, ok := autoconfig.SplitAddress(address); !ok {
		return acct, fmt.Errorf("%q is not an email address", address)
	}
	if _, dup := existing.Account(address); dup {
		return acct, fmt.Errorf("%s is already configured", address)
	}
	acct.Email, acct.TLS = address, "tls"

	// 2. Detect, then let the user confirm or correct.
	f.say("Looking up mail servers for %s…", address)
	settings, err := autoconfig.Detect(ctx, address, f.Resolver, autoconfig.Options{})
	if errors.Is(err, autoconfig.ErrNotFound) {
		f.say("Nothing found from the domain itself.")
		if yes, aerr := f.confirm("Ask Mozilla's public ISP database? This tells Mozilla which domain you are setting up", false); aerr != nil {
			return acct, aerr
		} else if yes {
			settings, err = autoconfig.Detect(ctx, address, f.Resolver, autoconfig.Options{AllowISPDB: true})
		}
	}
	if err != nil {
		if !errors.Is(err, autoconfig.ErrNotFound) {
			return acct, err
		}
		f.say("No settings found. Enter them by hand (your provider's help page lists them).")
		if settings, err = f.askSettings(autoconfig.Settings{IMAPPort: 993, SMTPPort: 465, AuthMethod: "plain"}); err != nil {
			return acct, err
		}
	}
	for {
		f.show(settings, address)
		answer, err := f.ask("Use these servers? Your password will be sent to them. [Y]es / [e]dit / [n]o", "y")
		if err != nil {
			return acct, err
		}
		switch strings.ToLower(answer)[:1] {
		case "y":
		case "e":
			if settings, err = f.askSettings(settings); err != nil {
				return acct, err
			}
			continue
		default:
			return acct, errAborted
		}
		break
	}
	acct.IMAPHost, acct.IMAPPort = settings.IMAPHost, settings.IMAPPort
	acct.SMTPHost, acct.SMTPPort = settings.SMTPHost, settings.SMTPPort
	acct.AuthMethod = settings.AuthMethod
	if u := autoconfig.ExpandUsername(settings.Username, address); u != address {
		acct.Username = u
	}

	// 3. Sign-in method for providers that offer a choice.
	if settings.Provider == "Google" {
		f.say("\nGoogle sign-in:")
		f.say("  1. App password (simplest)")
		f.say("  2. OAuth in the browser, with your own Google Cloud client ID and secret")
		choice, err := f.ask("Choose", "1")
		if err != nil {
			return acct, err
		}
		if choice == "2" {
			acct.AuthMethod = "oauth2-gmail"
			if acct.OAuthClientID, err = f.ask("Google OAuth client ID", ""); err != nil {
				return acct, err
			}
			f.prompt("Google OAuth client secret (hidden): ")
			if acct.OAuthClientSecret, err = f.ReadSecret(); err != nil {
				return acct, err
			}
		}
	}

	// 4. Credentials and the connection test, repeated until it passes or
	// the user gives up. Nothing is saved before a successful test.
	var password string
	for {
		var creds *auth.Credentials
		switch auth.ParseAuthMethod(acct.AuthMethod) {
		case auth.AuthOAuth2Gmail, auth.AuthOAuth2Outlook:
			f.say("\nStarting the OAuth sign-in…")
			if creds, err = f.OAuth(acct); err != nil {
				f.say("OAuth sign-in failed: %v", err)
			}
		default:
			f.prompt("Password for %s (hidden): ", address)
			if password, err = f.ReadSecret(); err != nil {
				return acct, err
			}
			if password == "" {
				return acct, errAborted
			}
			username := acct.Username
			if username == "" {
				username = address
			}
			creds = &auth.Credentials{Username: username, Password: password, AuthMethod: auth.ParseAuthMethod(acct.AuthMethod)}
		}
		if creds != nil {
			f.say("Testing the connection…")
			check := f.Check(acct, creds)
			if check.OK() {
				f.say("  IMAP login OK (%d folders)", len(check.Folders))
				f.say("  SMTP login OK")
				f.reportFolders(check)
				break
			}
			if check.IMAPErr != nil {
				f.say("  IMAP: %s", email.SanitizeLine(check.IMAPErr.Error()))
			}
			if check.SMTPErr != nil {
				f.say("  SMTP: %s", email.SanitizeLine(check.SMTPErr.Error()))
			}
		}
		answer, err := f.ask("[r]etry sign-in / [e]dit servers / [q]uit", "r")
		if err != nil {
			return acct, err
		}
		switch strings.ToLower(answer)[:1] {
		case "r":
		case "e":
			if settings, err = f.askSettings(settings); err != nil {
				return acct, err
			}
			acct.IMAPHost, acct.IMAPPort = settings.IMAPHost, settings.IMAPPort
			acct.SMTPHost, acct.SMTPPort = settings.SMTPHost, settings.SMTPPort
		default:
			return acct, errAborted
		}
	}

	// 5. Save: the display name, then the keyring, then the config file.
	if acct.Name, err = f.ask("Your name, as recipients will see it (optional)", ""); err != nil && !errors.Is(err, io.EOF) {
		return acct, err
	}
	if password != "" {
		if err := f.StorePassword(address, password); err != nil {
			return acct, fmt.Errorf("storing the password in the OS keyring: %w", err)
		}
	}
	if err := f.Append(acct); err != nil {
		block, _ := config.AccountBlock(acct)
		f.say("\nCould not write the config file: %v\nAdd this block to %s yourself:\n\n%s", err, config.Path(), block)
		return acct, err
	}
	f.say("\nSaved %s to %s. Run `vimail` to start.", address, config.Path())
	return acct, nil
}

func (f *Flow) say(format string, args ...any) {
	fmt.Fprintf(f.Out, format+"\n", args...)
}

func (f *Flow) prompt(format string, args ...any) {
	fmt.Fprintf(f.Out, format, args...)
}

// ask prints a question and reads one line; an empty answer gives def.
func (f *Flow) ask(question, def string) (string, error) {
	if def != "" {
		f.prompt("%s [%s]: ", question, def)
	} else {
		f.prompt("%s: ", question)
	}
	line, err := f.In.ReadString('\n')
	line = strings.TrimSpace(email.SanitizeLine(line))
	if line == "" {
		if err != nil && def == "" {
			return "", err
		}
		return def, nil
	}
	return line, nil
}

func (f *Flow) confirm(question string, def bool) (bool, error) {
	d := "n"
	if def {
		d = "y"
	}
	answer, err := f.ask(question+" (y/n)", d)
	if err != nil {
		return false, err
	}
	return strings.HasPrefix(strings.ToLower(answer), "y"), nil
}

func (f *Flow) show(s autoconfig.Settings, address string) {
	f.say("")
	if s.Source != "" {
		f.say("Found: %s", s.Source)
	}
	f.say("  IMAP   %s", autoconfig.JoinHostPort(s.IMAPHost, s.IMAPPort))
	f.say("  SMTP   %s", autoconfig.JoinHostPort(s.SMTPHost, s.SMTPPort))
	f.say("  Login  %s", autoconfig.ExpandUsername(s.Username, address))
	if s.Note != "" {
		f.say("\n%s\n", s.Note)
	}
}

// askSettings lets the user type the servers. Hand-typed hosts may be
// anything the user can reach (an IP, localhost for a bridge); they still
// must not contain whitespace or control characters.
func (f *Flow) askSettings(s autoconfig.Settings) (autoconfig.Settings, error) {
	var err error
	host := func(question, def string) (string, error) {
		for {
			v, err := f.ask(question, def)
			if err != nil {
				return "", err
			}
			if v != "" && !strings.ContainsAny(v, " \t/\\@") {
				return strings.ToLower(v), nil
			}
			f.say("  Enter a host name, e.g. imap.example.com")
		}
	}
	port := func(question string, def int) (int, error) {
		for {
			v, err := f.ask(question, strconv.Itoa(def))
			if err != nil {
				return 0, err
			}
			if n, perr := strconv.Atoi(v); perr == nil && n > 0 && n < 65536 {
				return n, nil
			}
			f.say("  Enter a port number")
		}
	}
	if s.IMAPHost, err = host("IMAP host", s.IMAPHost); err != nil {
		return s, err
	}
	if s.IMAPPort, err = port("IMAP port (TLS)", s.IMAPPort); err != nil {
		return s, err
	}
	if s.SMTPHost, err = host("SMTP host", s.SMTPHost); err != nil {
		return s, err
	}
	if s.SMTPPort, err = port("SMTP port (465 TLS, or 587 STARTTLS)", s.SMTPPort); err != nil {
		return s, err
	}
	s.Source = "entered by hand"
	return s, nil
}

// reportFolders tells the user when a special folder was not found, since
// sending, drafts and delete depend on them.
func (f *Flow) reportFolders(check worker.AccountCheck) {
	var missing []string
	for _, role := range []string{"Sent", "Drafts", "Trash"} {
		if _, ok := check.Folders[role]; !ok {
			missing = append(missing, role)
		}
	}
	if len(missing) > 0 {
		f.say("  Note: no %s folder was recognised. Name it under `folders` in the account config (see README, Special folders).",
			strings.Join(missing, ", "))
	}
}
