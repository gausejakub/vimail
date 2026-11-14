// Package autoconfig finds the IMAP and SMTP settings for an email address,
// so an account can be added by entering the address alone.
//
// Sources are tried from most to least trusted and the first hit wins:
//
//  1. a built-in table of large providers, by the address's domain;
//  2. the domain's MX records, mapped to those providers (custom domains
//     hosted at Google, Microsoft, Fastmail, ...);
//  3. the domain's own published autoconfig (the file Thunderbird reads);
//  4. Mozilla's public ISP database, only when the caller allows it, because
//     it tells a third party which domain is being set up;
//  5. DNS SRV records (RFC 6186);
//  6. conventional host names (imap.<domain>, mail.<domain>) that answer
//     with a valid TLS certificate.
//
// Detection only proposes. DNS and autoconfig answers can be forged, so the
// caller must show the result to the user and get a confirmation before any
// password is sent to the proposed servers; TLS with a valid certificate is
// always required.
package autoconfig

import (
	"context"
	"encoding/xml"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// Settings are proposed server settings for an account.
type Settings struct {
	IMAPHost string
	IMAPPort int
	SMTPHost string
	SMTPPort int
	// Username is the login name; empty means the full email address.
	Username string
	// AuthMethod is a config auth_method value: "plain", "app-password",
	// "oauth2-gmail" or "oauth2-outlook".
	AuthMethod string
	// Provider is a short provider name ("Google", "Fastmail"), empty when
	// the servers were found generically.
	Provider string
	// Source says where the settings came from, for showing to the user.
	Source string
	// Note is provider-specific guidance (e.g. that an app password is needed).
	Note string
}

// Resolver is the network access detection needs; tests replace it.
type Resolver interface {
	// LookupMX returns the domain's mail exchangers, lower-cased, without
	// the trailing dot.
	LookupMX(ctx context.Context, domain string) ([]string, error)
	// LookupSRV returns host and port of the first target of
	// _service._tcp.domain, or an error when there is none.
	LookupSRV(ctx context.Context, service, domain string) (host string, port int, err error)
	// Fetch returns the body of an HTTPS URL (2xx only, size-limited).
	Fetch(ctx context.Context, url string) ([]byte, error)
	// ProbeTLS reports whether host:port accepts a TLS connection (direct
	// TLS, or STARTTLS on port 587/143) with a certificate valid for host.
	ProbeTLS(ctx context.Context, host string, port int) bool
}

// Options control detection.
type Options struct {
	// AllowISPDB permits asking Mozilla's ISP database
	// (autoconfig.thunderbird.net), which reveals the domain to Mozilla.
	AllowISPDB bool
}

// ErrNotFound means no source produced settings; the caller asks the user.
var ErrNotFound = fmt.Errorf("no mail server settings found")

var reDomain = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// ValidHost reports whether s is a plain DNS host name. Host names from DNS
// and autoconfig are untrusted; anything else (IP literals, ports, paths,
// control characters) is refused.
func ValidHost(s string) bool {
	if len(s) > 253 || !reDomain.MatchString(s) || net.ParseIP(s) != nil {
		return false
	}
	// A top-level label is never numeric; "10.0.0.1" is an address.
	tld := s[strings.LastIndex(s, ".")+1:]
	return strings.ContainsAny(tld, "abcdefghijklmnopqrstuvwxyz")
}

// SplitAddress returns the lower-cased domain of an email address.
func SplitAddress(email string) (domain string, ok bool) {
	email = strings.TrimSpace(email)
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 || strings.ContainsAny(email, " \t\r\n<>,;") {
		return "", false
	}
	domain = strings.ToLower(email[at+1:])
	return domain, ValidHost(domain)
}

// Detect proposes settings for an email address.
func Detect(ctx context.Context, email string, r Resolver, opt Options) (Settings, error) {
	domain, ok := SplitAddress(email)
	if !ok {
		return Settings{}, fmt.Errorf("%q is not an email address", email)
	}

	// 1. Built-in table.
	if p, ok := providerByDomain(domain); ok {
		return p.settings("built-in settings for " + p.name), nil
	}

	// 2. MX records.
	mx, _ := r.LookupMX(ctx, domain)
	for _, host := range mx {
		if p, ok := providerByMX(host); ok {
			return p.settings(fmt.Sprintf("%s's mail is handled by %s (MX %s)", domain, p.name, host)), nil
		}
	}

	// 3. The domain's own autoconfig.
	for _, url := range []string{
		"https://autoconfig." + domain + "/mail/config-v1.1.xml",
		"https://" + domain + "/.well-known/autoconfig/mail/config-v1.1.xml",
	} {
		if s, ok := fetchAutoconfig(ctx, r, url); ok {
			s.Source = "autoconfig published by " + domain
			return s, nil
		}
	}

	// 4. Mozilla's ISP database, for the domain and then its MX's domain.
	if opt.AllowISPDB {
		candidates := []string{domain}
		for _, host := range mx {
			if base := registrableGuess(host); base != "" && base != domain {
				candidates = append(candidates, base)
				break
			}
		}
		for _, d := range candidates {
			if s, ok := fetchAutoconfig(ctx, r, "https://autoconfig.thunderbird.net/v1.1/"+d); ok {
				s.Source = "Mozilla ISP database entry for " + d
				return s, nil
			}
		}
	}

	// 5. SRV records.
	if s, ok := fromSRV(ctx, r, domain); ok {
		return s, nil
	}

	// 6. Conventional names.
	if s, ok := guess(ctx, r, domain); ok {
		return s, nil
	}
	return Settings{}, ErrNotFound
}

// --- providers ---

type provider struct {
	name               string
	domains            []string
	mxSuffixes         []string
	imapHost, smtpHost string
	smtpPort           int
	auth               string
	note               string
}

func (p provider) settings(source string) Settings {
	return Settings{
		IMAPHost: p.imapHost, IMAPPort: 993, SMTPHost: p.smtpHost, SMTPPort: p.smtpPort,
		AuthMethod: p.auth, Provider: p.name, Source: source, Note: p.note,
	}
}

var providers = []provider{
	{
		name: "Google", domains: []string{"gmail.com", "googlemail.com"},
		mxSuffixes: []string{".google.com", ".googlemail.com"},
		imapHost:   "imap.gmail.com", smtpHost: "smtp.gmail.com", smtpPort: 587, auth: "app-password",
		note: "Google does not accept your normal password here. Create an app password at https://myaccount.google.com/apppasswords (2-step verification must be on), or use OAuth with your own Google client ID.",
	},
	{
		name: "Microsoft", domains: []string{"outlook.com", "hotmail.com", "live.com", "msn.com"},
		mxSuffixes: []string{".outlook.com"},
		imapHost:   "outlook.office365.com", smtpHost: "smtp.office365.com", smtpPort: 587, auth: "oauth2-outlook",
		note: "Microsoft only allows OAuth sign-in for IMAP and SMTP; passwords and app passwords are refused.",
	},
	{
		name: "iCloud", domains: []string{"icloud.com", "me.com", "mac.com"},
		mxSuffixes: []string{".mail.icloud.com", ".icloud.com"},
		imapHost:   "imap.mail.me.com", smtpHost: "smtp.mail.me.com", smtpPort: 587, auth: "app-password",
		note: "iCloud needs an app-specific password: https://account.apple.com → Sign-In and Security → App-Specific Passwords.",
	},
	{
		name: "Fastmail", domains: []string{"fastmail.com", "fastmail.fm"},
		mxSuffixes: []string{".messagingengine.com"},
		imapHost:   "imap.fastmail.com", smtpHost: "smtp.fastmail.com", smtpPort: 465, auth: "app-password",
		note: "Fastmail needs an app password: Settings → Privacy & Security → Integrations → New app password.",
	},
	{
		name: "Yahoo", domains: []string{"yahoo.com", "ymail.com"},
		mxSuffixes: []string{".yahoodns.net"},
		imapHost:   "imap.mail.yahoo.com", smtpHost: "smtp.mail.yahoo.com", smtpPort: 465, auth: "app-password",
		note: "Yahoo needs an app password: Account Security → Generate app password.",
	},
	{
		name: "Seznam", domains: []string{"seznam.cz", "email.cz", "post.cz", "spoluzaci.cz"},
		mxSuffixes: []string{".seznam.cz"},
		imapHost:   "imap.seznam.cz", smtpHost: "smtp.seznam.cz", smtpPort: 465, auth: "plain",
	},
}

func providerByDomain(domain string) (provider, bool) {
	for _, p := range providers {
		for _, d := range p.domains {
			if d == domain {
				return p, true
			}
		}
	}
	return provider{}, false
}

func providerByMX(host string) (provider, bool) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, p := range providers {
		for _, suffix := range p.mxSuffixes {
			if strings.HasSuffix(host, suffix) {
				return p, true
			}
		}
	}
	return provider{}, false
}

// registrableGuess returns the last two labels of a host name. It is only
// used to pick a second ISPDB lookup key, so a wrong guess for multi-label
// public suffixes just means no match.
func registrableGuess(host string) string {
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	if len(labels) < 2 {
		return ""
	}
	return strings.Join(labels[len(labels)-2:], ".")
}

// --- autoconfig XML ---

type xmlConfig struct {
	Providers []struct {
		Incoming []xmlServer `xml:"incomingServer"`
		Outgoing []xmlServer `xml:"outgoingServer"`
	} `xml:"emailProvider"`
}

type xmlServer struct {
	Type       string `xml:"type,attr"`
	Hostname   string `xml:"hostname"`
	Port       int    `xml:"port"`
	SocketType string `xml:"socketType"`
	Username   string `xml:"username"`
}

func fetchAutoconfig(ctx context.Context, r Resolver, url string) (Settings, bool) {
	body, err := r.Fetch(ctx, url)
	if err != nil {
		return Settings{}, false
	}
	return parseAutoconfig(body)
}

// parseAutoconfig reads a Thunderbird autoconfig document. Only servers
// with SSL or STARTTLS and a plain host name are used; a document offering
// nothing but unencrypted servers yields no settings.
func parseAutoconfig(body []byte) (Settings, bool) {
	var doc xmlConfig
	if err := xml.Unmarshal(body, &doc); err != nil {
		return Settings{}, false
	}
	var s Settings
	usable := func(srv xmlServer, typ string) bool {
		socket := strings.ToUpper(strings.TrimSpace(srv.SocketType))
		return strings.EqualFold(srv.Type, typ) && (socket == "SSL" || socket == "STARTTLS") &&
			ValidHost(strings.ToLower(strings.TrimSpace(srv.Hostname))) && srv.Port > 0 && srv.Port < 65536
	}
	for _, p := range doc.Providers {
		for _, in := range p.Incoming {
			// vimail uses one TLS mode per account: implicit TLS for IMAP.
			if s.IMAPHost == "" && usable(in, "imap") && strings.EqualFold(strings.TrimSpace(in.SocketType), "SSL") {
				s.IMAPHost, s.IMAPPort = strings.ToLower(strings.TrimSpace(in.Hostname)), in.Port
				if u := strings.TrimSpace(in.Username); u != "" && u != "%EMAILADDRESS%" {
					s.Username = u // may hold %EMAILLOCALPART%; see ExpandUsername
				}
			}
		}
		for _, out := range p.Outgoing {
			// STARTTLS submission is only supported on port 587.
			starttls := strings.EqualFold(strings.TrimSpace(out.SocketType), "STARTTLS")
			if s.SMTPHost == "" && usable(out, "smtp") && (!starttls || out.Port == 587) {
				s.SMTPHost, s.SMTPPort = strings.ToLower(strings.TrimSpace(out.Hostname)), out.Port
			}
		}
	}
	if s.IMAPHost == "" || s.SMTPHost == "" {
		return Settings{}, false
	}
	s.AuthMethod = "plain"
	return s, true
}

// ExpandUsername resolves the placeholders autoconfig allows in a login
// name. An empty or unresolvable template means the full address.
func ExpandUsername(template, email string) string {
	if template == "" {
		return email
	}
	local, domain, _ := strings.Cut(email, "@")
	u := strings.NewReplacer("%EMAILADDRESS%", email, "%EMAILLOCALPART%", local, "%EMAILDOMAIN%", domain).Replace(template)
	if strings.Contains(u, "%") || strings.ContainsAny(u, " \t\r\n") {
		return email
	}
	return u
}

// --- SRV and guessing ---

func fromSRV(ctx context.Context, r Resolver, domain string) (Settings, bool) {
	imapHost, imapPort, err := r.LookupSRV(ctx, "imaps", domain)
	if err != nil || !ValidHost(imapHost) {
		return Settings{}, false
	}
	smtpHost, smtpPort, err := r.LookupSRV(ctx, "submissions", domain)
	if err != nil || !ValidHost(smtpHost) {
		smtpHost, smtpPort, err = r.LookupSRV(ctx, "submission", domain)
		if err != nil || !ValidHost(smtpHost) || smtpPort != 587 { // STARTTLS only on 587
			return Settings{}, false
		}
	}
	return Settings{
		IMAPHost: imapHost, IMAPPort: imapPort, SMTPHost: smtpHost, SMTPPort: smtpPort,
		AuthMethod: "plain", Source: "DNS SRV records of " + domain,
	}, true
}

func guess(ctx context.Context, r Resolver, domain string) (Settings, bool) {
	var s Settings
	for _, host := range []string{"imap." + domain, "mail." + domain} {
		if r.ProbeTLS(ctx, host, 993) {
			s.IMAPHost, s.IMAPPort = host, 993
			break
		}
	}
	if s.IMAPHost == "" {
		return Settings{}, false
	}
	for _, host := range []string{"smtp." + domain, "mail." + domain} {
		for _, port := range []int{465, 587} {
			if r.ProbeTLS(ctx, host, port) {
				s.SMTPHost, s.SMTPPort = host, port
				break
			}
		}
		if s.SMTPHost != "" {
			break
		}
	}
	if s.SMTPHost == "" {
		return Settings{}, false
	}
	s.AuthMethod = "plain"
	s.Source = "servers found at the usual names for " + domain
	return s, true
}

// JoinHostPort formats host:port for display.
func JoinHostPort(host string, port int) string {
	return net.JoinHostPort(host, fmt.Sprintf("%d", port))
}
