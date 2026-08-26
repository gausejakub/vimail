# Security

vimail takes email security seriously. This document describes the security measures in place and how to report vulnerabilities.

## Reporting Vulnerabilities

If you discover a security vulnerability, please email **security@vimail.dev** instead of opening a public issue. We will respond within 48 hours.

## Security Model

### Credential Storage

- Passwords and OAuth2 tokens are stored in the **OS keyring** (macOS Keychain, Linux Secret Service, Windows Credential Manager) via [go-keyring](https://github.com/zalando/go-keyring)
- Credentials are **never** stored in config files, logs, or on disk in plaintext
- Password bytes are zeroed in memory after terminal input

### Encryption at Rest

- Email bodies and HTML content in the SQLite cache are encrypted with **AES-256-GCM**
- The encryption key is auto-generated on first run and stored in the OS keyring
- Unencrypted data from older versions is transparently handled (read as-is, encrypted on next write)

### Network Security

- All IMAP and SMTP connections enforce **TLS 1.2 minimum**
- Certificate validation is always enabled (`InsecureSkipVerify` is never set)
- STARTTLS upgrade is verified before credentials are sent
- Plaintext connections (`tls: "none"`) log a warning; unknown TLS modes are rejected
- TCP keepalive (30s) detects dead connections
- 30-second dial timeouts on all connections

### Local Data

- SQLite cache: `~/.local/share/vimail/cache.db` — permissions `0600`
- Log files: `~/.local/share/vimail/vimail.log` — permissions `0600`
- Config file: `~/.config/vimail/config.toml` — permissions enforced to `0600` on load
- Saved attachments: written with `0600` permissions
- Temp files: cleaned up on exit

### Email Content Safety

- HTML emails are converted to plain text for TUI display (no script/iframe execution)
- Remote images are never auto-loaded
- Links are stripped from preview text (anti-phishing)
- Attachments are never auto-opened or auto-saved
- Dangerous file types (`.exe`, `.bat`, `.docm`, etc.) trigger a warning on save
- Double-extension tricks (e.g. `invoice.pdf.exe`) are detected
- Attachment filenames are sanitized against path traversal
- MIME parsing has depth (20) and size (25MB per part) limits
- IMAP body fetch is limited to 50MB to prevent OOM

### Process Security

- Graceful shutdown on SIGTERM/SIGINT with credential cleanup
- No shell injection — `exec.Command` used with separate arguments
- Parameterized SQL queries throughout (no string concatenation)

## Dependencies

- Dependencies are audited with `govulncheck ./...` in both Go modules (root and `pkg/vimtea`); last clean scan 2026-08-24 on Go 1.26.6 with `golang.org/x/net` v0.58.0
- Checksums verified via `go mod verify`
- Minimal dependency footprint — pure Go, no CGO
