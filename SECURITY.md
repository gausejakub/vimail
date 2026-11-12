# Security

vimail takes email security seriously. This document describes the security measures in place and how to report vulnerabilities.

## Reporting Vulnerabilities

If you discover a security vulnerability, please email **security@vimail.dev** instead of opening a public issue. We will respond within 48 hours.

## Security Model

### Credential Storage

- Passwords and OAuth2 tokens are stored in the **OS keyring** (macOS Keychain, Linux Secret Service, Windows Credential Manager) via [go-keyring](https://github.com/zalando/go-keyring), or fetched at startup from a password manager via `password_cmd` / `[cache] key_cmd` (kept in memory only, never written to disk)
- Passwords and tokens are **never** stored in config files, logs, or on disk in plaintext. The optional `oauth_client_secret` for a bring-your-own OAuth client does live in `config.toml` (kept at `0600`)
- OAuth2 tokens are refused over an unencrypted connection (`tls = "none"`)
- Password bytes are zeroed in memory after terminal input
- `password_cmd` / `key_cmd` use only the first line of the command's output (further lines of a password-manager entry are never sent to a server), have no stdin, and are bounded by a timeout even if a child process lingers. On failure only the command's stderr is reported, never its output, and there is no fallback to the keyring or to an unencrypted cache

### Encryption at Rest

- Message bodies and HTML, senders, recipients, subjects, attachment names, drafts and queued outgoing mail in the SQLite cache are encrypted with **AES-256-GCM**. Rows written by older versions are encrypted on the first start after upgrading
- **Not encrypted:** dates, Message-IDs, read/flag state, folder and account names, and attachment sizes — the cache needs them to sort and sync. Use full-disk encryption if that metadata matters to you
- The text of a sent message is removed from the queue as soon as it is delivered
- The encryption key is auto-generated on first run and stored in the OS keyring. An existing key is never replaced: if the keyring is unavailable while the cache holds encrypted mail, vimail refuses to start instead of writing plaintext. If no keyring exists at all, vimail warns at startup and stores bodies unencrypted
- The key protects against other users, backups and a stolen disk — not against malware running as you, which can read the keyring too
- Deleted rows are overwritten (`PRAGMA secure_delete`)
- Unencrypted data from older versions is transparently handled (read as-is, encrypted on next write)

### Network Security

- All IMAP and SMTP connections enforce **TLS 1.2 minimum**
- Certificate validation is always enabled (`InsecureSkipVerify` is never set)
- STARTTLS upgrade is verified before credentials are sent
- Plaintext connections (`tls: "none"`) show a warning in the TUI at every start and in `vimail setup`; unknown TLS modes are rejected
- After a rejected login, reconnects back off (30s up to 5 minutes) so queued work cannot get the account locked
- Deleting uses `UID EXPUNGE` where the server supports it, so only the messages vimail deleted are purged
- TCP keepalive (30s) detects dead connections
- 30-second dial timeouts on all connections

### Local Data

- Data directory `~/.local/share/vimail` — permissions enforced to `0700`
- SQLite cache: `cache.db` and its WAL files — created `0600` from the first run
- Log files: `vimail.log`, `vimail-mcp.log` — permissions `0600`. Logs record accounts, folders, UIDs and errors, but not subjects, recipients, search queries or attachment names
- Config file: `~/.config/vimail/config.toml` — permissions enforced to `0600` on load
- Saved attachments and exports: written with `0600` permissions, created exclusively (an existing file or symlink is never overwritten or followed)
- Temp files (HTML opened in the browser): in a private per-process directory, cleaned up on exit

### Email Content Safety

- Everything in a received message is treated as hostile. Terminal escape sequences, control characters and bidirectional overrides are stripped from subjects, addresses, bodies, attachment names and folder names, both when mail is cached and when it is drawn, so a message cannot rewrite the screen, set the window title or write to the clipboard (OSC 52)
- Invisible characters that can hide text are stripped as well: Unicode tag characters, the variation-selector supplement, word joiners and invisible operators. Such text is unseen on screen but readable by an AI client ("ASCII smuggling"), so it is removed when mail is cached, drawn, and returned by the MCP server
- HTML entities are decoded before sanitizing, so `&#27;` or `&#x2066;` cannot reintroduce an escape or bidi control
- The Vim reader shows, and copies to the clipboard, the same sanitized text as the preview
- HTML emails are converted to plain text for TUI display (no script/iframe execution)
- Remote images are never auto-loaded. "Open in browser" (`o`) adds a Content-Security-Policy that blocks scripts, remote images and redirects
- Preview rendering is bounded (size cap, cached per message), so a crafted body cannot freeze the UI
- Replies go to the real address, not to an address embedded in the display name; outgoing headers are stripped of line breaks
- AI compose marks the quoted email as untrusted and runs the agent in an empty directory
- Links are stripped from preview text (anti-phishing)
- Attachments are never auto-opened or auto-saved
- Dangerous file types (`.exe`, `.bat`, `.docm`, etc.) trigger a warning on save
- Double-extension tricks (e.g. `invoice.pdf.exe`) are detected
- Attachment filenames are reduced to a plain name: no directories (either slash style), no `..`, no hidden-file dots, no disguised extensions
- MIME parsing has depth (20) and size (25MB per part) limits
- IMAP body fetch is limited to 50MB to prevent OOM

### MCP Server

- Mail returned to AI clients is labelled as untrusted content; tools carry read-only / destructive annotations
- Senders, recipients, subjects, bodies and attachment names are sanitized again when returned, so mail cached by an older version cannot carry escape sequences or invisible characters to the client
- Read-only by default: tools that change the mailbox exist only with `[mcp] allow_write = true`, and `send_email` only with `allow_send = true`
- No tool takes a file path or reads files; permanent deletion is not exposed
- Prompt injection through received mail cannot be fully prevented by the server — see the README's MCP section

### Process Security

- Graceful shutdown on SIGTERM/SIGINT with credential cleanup
- Nothing from a message ever reaches a command line: external programs (browser, AI agent) are started with `exec.Command` and separate arguments
- The only shell commands vimail runs are the ones you write in `config.toml` (`password_cmd`, `[cache] key_cmd`, via `sh -c`). Whoever can edit that file can run code as you — vimail keeps it `0600`; keep its directory yours too
- Parameterized SQL queries throughout (no string concatenation)

## Dependencies

- Dependencies are audited with `govulncheck ./...` in both Go modules (root and `pkg/vimtea`) on every push and pull request
- CI runs with read-only repository permissions and actions pinned to commit SHAs
- Release tarballs are built by CI from the tag, published with a `SHA256SUMS` file and signed build provenance (`gh attestation verify <file> -R gausejakub/vimail`)
- Checksums verified via `go mod verify`
- Minimal dependency footprint — pure Go, no CGO
