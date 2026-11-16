# Security

vimail takes email security seriously. This document describes the security measures in place and how to report vulnerabilities.

## Reporting Vulnerabilities

If you discover a security vulnerability, please email **security@vimail.dev** instead of opening a public issue. We will respond within 48 hours.

## Security Model

### Credential Storage

- Passwords and OAuth2 tokens are stored in the **OS keyring** (macOS Keychain, Linux Secret Service, Windows Credential Manager) via [go-keyring](https://github.com/zalando/go-keyring), or fetched at startup from a password manager via `password_cmd` / `[cache] key_cmd` (kept in memory only, never written to disk)
- Passwords and tokens are **never** stored in config files, logs, or on disk in plaintext. The optional `oauth_client_secret` for a bring-your-own OAuth client does live in `config.toml` (kept at `0600`)
- OAuth2 tokens are refused over an unencrypted connection (`tls = "none"`)
- The Gmail sign-in uses PKCE and a checked `state`, on a loopback listener (`127.0.0.1` only); if its usual port is taken it moves to another free one, so another local user cannot block sign-in
- Password bytes are zeroed in memory after terminal input
- `password_cmd` / `key_cmd` use only the first line of the command's output (further lines of a password-manager entry are never sent to a server), have no stdin, and are bounded by a timeout even if a child process lingers. On failure only the command's stderr is reported, never its output, and there is no fallback to the keyring or to an unencrypted cache

### Encryption at Rest

- Message bodies and HTML, senders, recipients, subjects, attachment names, drafts and queued outgoing mail in the SQLite cache are encrypted with **AES-256-GCM**. Rows written by older versions are encrypted on the first start after upgrading
- **Not encrypted:** dates, Message-IDs, read/flag state, folder and account names, and attachment sizes — the cache needs them to sort and sync. Use full-disk encryption if that metadata matters to you
- The text of a sent message is removed from the queue as soon as it is delivered
- The encryption key is auto-generated on first run and stored in the OS keyring. An existing key is never replaced: if the keyring is unavailable, or answers but no longer holds the key, while the cache holds encrypted mail, vimail refuses to start instead of writing plaintext or generating a second key. If no keyring exists at all, vimail warns at startup and stores bodies unencrypted
- The key protects against other users, backups and a stolen disk — not against malware running as you, which can read the keyring too
- Deleted rows are overwritten (`PRAGMA secure_delete`)
- Unencrypted data from older versions is transparently handled (read as-is, encrypted on next write)

### Network Security

- All IMAP and SMTP connections enforce **TLS 1.2 minimum**
- Certificate validation is always enabled (`InsecureSkipVerify` is never set)
- STARTTLS upgrade is verified before credentials are sent
- Plaintext connections (`tls: "none"`) show a warning in the TUI at every start and in `vimail setup`; unknown TLS modes are rejected for IMAP and SMTP alike
- `vimail setup add` shows the servers it found and asks before any password is entered. Servers named by DNS SRV records alone, outside the address's own domain, come with a warning and the answer defaults to No: DNS is not authenticated
- After a rejected login, reconnects back off (30s up to 5 minutes) so queued work cannot get the account locked
- Deleting uses `UID EXPUNGE` where the server supports it, so only the messages vimail deleted are purged. On a server without it, messages another client had marked deleted are unmarked for the purge and marked again afterwards, so they are not purged along
- Deleting, moving and flagging act on message numbers (UIDs) taken from the cache. Before each of them vimail checks that the folder's `UIDVALIDITY` on the server is still the cached one; if the folder was recreated or renumbered, nothing is changed and the queued operations for that folder are dropped instead of being replayed onto other messages. A sync that finds a new `UIDVALIDITY` discards the folder's cached bodies, attachments and signature verdicts, in a full sync too
- An SMTP session has an overall deadline (8 minutes), and saving the sent copy has one too: a server that accepts a connection and then says nothing cannot hold a send until the retry delivers it a second time
- Header fetches are read with a bound per header section, like bodies, and no single IMAP response may exceed 1 GB (the connection is closed): a server cannot fill memory with an endless string in an envelope or a folder list
- TCP keepalive (30s) detects dead connections
- 30-second dial timeouts on all connections

### Local Data

- Data directory `~/.local/share/vimail` — permissions enforced to `0700`
- SQLite cache: `cache.db` and its WAL files — created `0600` from the first run
- Log files: `vimail.log`, `vimail-mcp.log` — permissions `0600`, also for a file left by an older version. Logs record accounts, folders, UIDs and errors, but not subjects, recipients, search queries or attachment names. Email addresses inside error text (a server's "550 <bob@example.com>: unknown user") are replaced by a placeholder, in the logs and in the operation queue
- Config file: `~/.config/vimail/config.toml` — permissions enforced to `0600` on load
- Saved attachments and exports: written with `0600` permissions, created exclusively (an existing file or symlink is never overwritten or followed)
- Temp files (HTML opened in the browser): in a private per-process directory, cleaned up on exit

### Email Content Safety

- Everything in a received message is treated as hostile. Terminal escape sequences, control characters and bidirectional overrides are stripped from subjects, addresses, bodies, attachment names and folder names, both when mail is cached and when it is drawn, so a message cannot rewrite the screen, set the window title or write to the clipboard (OSC 52)
- Invisible characters that can hide text are stripped as well: Unicode tag characters, variation selectors, zero-width spaces, word joiners, invisible operators, soft hyphens and blank filler characters. The zero-width joiner and non-joiner and the two emoji/text presentation selectors are kept only where writing needs them: inside an emoji sequence and between letters of a joining script (Arabic, Indic and others). Such text is unseen on screen but readable by an AI client ("ASCII smuggling"), so it is removed when mail is cached, drawn, and returned by the MCP server
- HTML entities are decoded before sanitizing, so `&#27;` or `&#x2066;` cannot reintroduce an escape or bidi control
- The Vim reader shows, and copies to the clipboard, the same sanitized text as the preview
- HTML emails are converted to plain text for TUI display (no script/iframe execution)
- Remote images are never auto-loaded. "Open in browser" (`o`) does not hand the message's HTML to the browser as-is: it is parsed and rebuilt with a Content-Security-Policy (no scripts, remote content, frames or form submission) and without the elements and attributes that navigate or fetch on their own — `<meta http-equiv="refresh">`, `<base>`, `<link>`, `<script>`, `<noscript>`, frames and plugins, event handlers, `srcset`, remote `src` / `background` / `poster`, and remote `url()` / `@import` in CSS (a style using CSS escapes is dropped). The policy alone is not enough: it cannot stop a meta refresh, and Firefox loads `srcset` images despite it. The rebuilt page declares UTF-8 itself, ahead of the policy; the message's own charset declarations and all control bytes are removed. Otherwise a browser told to read the file as ISO-2022-JP decodes it differently from the sanitizer, the policy disappears and an attribute value becomes a script. A `<style>` whose text would close the element is dropped. Links you click still work (`http`, `https`, `mailto`, `tel`). Markup that cannot be rebuilt is shown as plain text instead
- Exported ZIPs contain the same rebuilt `message.html`; attachments go into an `attachments/` folder under unique names, so they cannot replace `message.html`, `metadata.txt` or each other when extracted
- Preview rendering is bounded (size cap, cached per message), so a crafted body cannot freeze the UI
- The Vim reader and editor wrap a line in one pass, so a message that is a single enormous line (minified HTML or JSON) cannot freeze the UI either
- The code that reads or draws untrusted mail is fuzz-tested against stated invariants, not only for crashes: sanitizing, the browser rebuild and its CSS filter, MIME parsing, file naming, outgoing headers, the preview and Vim reader, the editor, and the whole TUI drawing a cache filled with hostile data. Inputs that once broke something are kept as regression cases
- Names are sanitized again when drawn (folders, accounts), so a cache written by an older version cannot put escape sequences on screen
- Drafts are sanitized when an AI client saves them through the MCP server and again when opened in the editor; the editor refuses control characters that arrive as typed or pasted text
- Replies go to the real address, not to an address embedded in the display name; outgoing headers are stripped of line breaks
- AI compose marks the quoted email, its attribution line and a forwarded message as untrusted, apart from your own text, and runs the agent in an empty directory
- Links are stripped from preview text (anti-phishing)
- Attachments are never auto-opened or auto-saved
- Dangerous file types (`.exe`, `.bat`, `.docm`, etc.) trigger a warning on save
- Double-extension tricks (e.g. `invoice.pdf.exe`) are detected
- Attachment filenames are reduced to a plain name: no directories (either slash style), no `..`, no hidden-file dots, no disguised extensions, no Windows device names (`CON.txt`)
- Saved attachments and exported ZIPs are marked as downloaded from the Internet: the Mark of the Web on Windows (so Office Protected View, macro blocking and SmartScreen apply) and the quarantine attribute on macOS (so Gatekeeper checks them). Linux has no such marker; files are written `0600`, never executable
- In the message list, a display name that is itself an address other than the real one (`"ceo@corp.com" <someone@elsewhere>`) is replaced by the real address
- An invitation is answered to the organizer it names; when that is not the sender of the message, vimail shows where the answer would go and sends it only on an explicit `:accept!`
- With OpenPGP on: GnuPG's output is bounded, so a small encrypted message that decompresses to gigabytes is refused instead of filling memory; opening a message never fetches keys from a server (`--no-auto-key-retrieve`); a message with more than one From header has no sender a signature could vouch for
- MIME parsing has depth (20) and size (25MB per part) limits
- IMAP body fetch is limited to 50MB to prevent OOM; header sections are limited to 1MB each

### MCP Server

- Mail returned to AI clients is labelled as untrusted content; tools carry read-only / destructive annotations
- Senders, recipients, subjects, bodies and attachment names are sanitized again when returned, so mail cached by an older version cannot carry escape sequences or invisible characters to the client
- Read-only by default: tools that change the mailbox exist only with `[mcp] allow_write = true`, and `send_email` only with `allow_send = true`
- `save_draft` can attach local files only from the directories listed in `[mcp] attachment_dirs` (none by default, so attaching is off); no other tool takes a file path or reads files. Permanent deletion is not exposed
- `read_message` and `read_messages` return a bounded body and say when it was cut; error text that quotes a mail server is sanitized like message fields
- `move_message` (which can target Trash or Spam) and `mark_all_read` are annotated destructive, so a client that asks before destructive calls asks for them too
- `save_draft` can update only drafts whose current version was saved over MCP. A draft you wrote, or edited after an AI client saved it, cannot be changed by one. Opening a draft an AI client wrote says so and counts its recipients; the composer shows the number of recipients in To, Cc and Bcc whenever a list is longer than its field
- `send_email` refuses a message that is already waiting in the queue for retry, so a client that repeats a failed send does not deliver it twice
- Prompt injection through received mail cannot be fully prevented by the server — see the README's MCP section

### Process Security

- Graceful shutdown on SIGTERM/SIGINT with credential cleanup
- Nothing from a message ever reaches a shell: external programs (browser, AI agent, notifier, gpg) are started directly with separate arguments. Text from a message that is passed as an argument (a notification's title and body) never starts with `-` and follows a `--`, so it cannot be read as an option; a sender named `-e` would otherwise have had its subject run by `osascript` as a script
- The built-in AI agent gets its prompt on stdin, not as an argument (arguments are visible in the process list), and runs with tools and MCP servers switched off, in an empty directory, with a time limit. A custom agent configured with `{prompt}` does receive mail text as an argument
- The only shell commands vimail runs are the ones you write in `config.toml` (`password_cmd`, `[cache] key_cmd`, via `sh -c`). Whoever can edit that file can run code as you — vimail keeps it `0600`; keep its directory yours too
- Parameterized SQL queries throughout (no string concatenation)

## Dependencies

- Dependencies are audited with `govulncheck ./...` in both Go modules (root and `pkg/vimtea`) on every push and pull request
- CI runs with read-only repository permissions and actions pinned to commit SHAs. In the release workflow the tests run in a job of their own with a read-only token; the job that can publish runs no tests and leaves no credentials in the checkout
- Release tarballs are built by CI from the tag, published with a `SHA256SUMS` file and signed build provenance (`gh attestation verify <file> -R gausejakub/vimail`)
- Checksums verified via `go mod verify`
- Minimal dependency footprint — pure Go, no CGO
