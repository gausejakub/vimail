# Changelog

All notable changes to vimail are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and entries are added by the merge flow (see `.claude/skills/merge-changes/`):
every merged PR lands here in the same pass that merges it.

## [Unreleased]

### Added

### Changed

### Fixed

### Security

## [0.15.5] - 2026-11-13

Security and stability release following a fourth audit, which fuzz-tested everything that reads or draws mail. Upgrading is recommended for everyone.

### Security

- A message could crash vimail every time it was selected, and so on every launch if it was the newest: an HTML-only mail whose text version comes out empty (an image-only newsletter, for example) hit a broken pattern in the preview. (#60)
- "Open in browser" and export no longer crash on a message whose CSS contains certain non-ASCII characters (a regression in 0.15.4). (#60)
- Drafts saved by an AI client through the MCP server are sanitized when saved and again when opened, so they cannot put escape sequences on the terminal. (#60)
- Folder and account names are sanitized when drawn, not only when fetched; the editor refuses control characters in typed or pasted text. (#60)

### Fixed

- Compose no longer crashes in a very small terminal window. (#60)
- Editor: a very long numeric prefix (a stuck digit key) can no longer freeze vimail or exhaust memory, and screen motions (`H`, `M`, `L`) in a tiny pane no longer break the next command. (#60)

## [0.15.4] - 2026-11-12

Security release following a third audit. Upgrading is recommended for everyone who opens mail in the browser (`o`) or exports messages.

### Security

- "Open in browser" could be redirected: a message containing `<meta http-equiv="refresh">` sent the browser tab to a sender-chosen address as soon as it opened, because a Content-Security-Policy does not cover it. The message's HTML is now parsed and rebuilt before the browser sees it, without anything that navigates or loads on its own; HTML that cannot be rebuilt is shown as plain text. (#58)
- Tracking pixels written with `srcset` were loaded by Firefox despite the policy. The rebuild removes remote images, `srcset`, remote CSS `url()` / `@import`, forms, frames and event handlers itself instead of relying on the browser. Links you click still work. (#58)
- Exported ZIPs contain the same rebuilt `message.html` instead of the raw markup, and attachments are written to an `attachments/` folder under unique names, so an attachment can no longer replace `message.html`, `metadata.txt` or another attachment when the archive is extracted. (#58)
- The MCP `list_operations` tool sanitizes folder names and error text; AI compose treats a reply's subject as context, not instructions. (#58)

### Fixed

- Searching for text that contains a backslash finds it. (#58)

## [0.15.3] - 2026-11-12

Security release following a second audit, covering everything added since 0.13.1. Upgrading is recommended, especially if you use the MCP server.

### Security

- Invisible Unicode characters that can hide text (tag characters, the variation-selector supplement, word joiners and invisible operators) are stripped from mail. They were unseen in vimail but readable by an AI client through the MCP server — a hidden prompt injection. The MCP server also sanitizes sender, recipient, subject, body and attachment names again on output, so mail already in the cache is covered. (#57)
- HTML entities in a body are decoded before sanitizing, so entity-encoded bidirectional controls can no longer reach the preview, the Vim reader or copied text. (#57)

### Changed

- `password_cmd` and `[cache] key_cmd` use only the first line of the command's output, so multi-line password-manager entries (password, then notes) work and the notes are never sent to a server. (#57)

### Fixed

- The 2-minute timeout of `password_cmd` / `key_cmd` now holds even when the command leaves a child process running. (#57)

## [0.15.2] - 2026-11-12

### Fixed

- The Vim reader opens with the cursor where you were reading (the first body line, or the top of the scrolled preview) instead of on the `From:` line, and `v` / `V` from the preview no longer start a selection at that hidden spot: move to where it should start, then press `v`. (#56)

## [0.15.1] - 2026-11-12

### Fixed

- Vim reader is easier to reach: in the focused preview pane, `v`, `V`, `/` and other Vim reading keys open the reader and act right away (they used to start the message-list selection or the global search), and `Enter` on a message in the list opens it in the reader. (#55)

## [0.15.0] - 2026-11-12

Read and copy from a message the Vim way.

### Added

- Vim reader: press `Enter` in the focused preview pane to open the message (headers, body, attachment list) in a read-only Vim buffer. Move with Vim motions, search with `/`, select with `v` / `V` and copy with `y` (or `yy`, `yiw`, …) straight to the system clipboard (`wl-copy`, `xclip`/`xsel`, `pbcopy`, or OSC 52 over SSH). `q` or `Esc` closes it. (#54)
- `pkg/vimtea` v0.3.0: `WithReadOnly` viewer mode that refuses every edit and reports yanks as `YankMsg`. (#53)

## [0.14.1] - 2026-11-12

Fixes for scrolling quickly through a large folder.

### Fixed

- Scrolling through messages no longer floods the server with one mark-read per message: selections are collected for 1.5 s and sent as one batch per folder, so an account sync or delete is no longer stuck behind them (a sync could hit its 5-minute timeout before starting). (#50)
- While scrolling, the preview loads the message you stopped on instead of every message passed on the way; skipped fetches are no longer logged as errors. Bodies over 50 MB or interrupted reads no longer break the fetch connection ("imapwire: cannot decode while a literal is open"), and a fetch that hits its deadline now says it timed out. (#51)
- An account sync that takes longer than 5 minutes keeps running in the background and refreshes the view when it finishes, instead of being reported as timed out and making the next sync fail with "account sync is locked by another process". (#52)

## [0.14.0] - 2026-11-12

Easier install and no-keyring setups.

### Added

- Nix flake: try vimail with `nix run github:gausejakub/vimail`, install it with `nix profile install github:gausejakub/vimail`, or add it as a flake input for NixOS / home-manager. (#47)
- `password_cmd` per account and `[cache] key_cmd` read the password and the cache encryption key from a password manager (e.g. 1Password `op read`, `pass`) instead of the OS keyring. Secrets stay in memory only; a failing command never falls back to the keyring or to an unencrypted cache. `vimail setup` tests the commands instead of prompting. (#49)

## [0.13.1] - 2026-11-12

Security release following a full audit of the mail-handling, local storage, network and MCP code. Upgrading is recommended for everyone.

### Changed

- **Breaking (MCP):** the MCP server is read-only by default. Tools that mark, flag, move, delete, restore or save drafts are exposed only with `[mcp] allow_write = true`. Reads, search, `sync` and `verify` are always available. Add that line to keep the previous behavior. (#46)
- Senders, recipients, subjects, attachment names, drafts and queued outgoing mail are now encrypted in the cache, not only bodies. Existing rows are encrypted once on the first start. An older vimail version reading the upgraded cache shows encrypted text, so downgrading needs a cache rebuild. (#46)
- "Open in browser" no longer runs scripts or loads remote images from the message. (#46)
- Logs no longer contain subjects, recipients, search queries or attachment names. (#46)
- AI compose marks the quoted email as untrusted, runs the agent in an empty directory, and pipes the prompt on stdin for agents whose args have no `{prompt}` placeholder. (#46)
- Outgoing non-ASCII subjects are RFC 2047 encoded, and a comma-separated To list now reaches every recipient. (#46)
- Release tarballs are built by CI and ship with `SHA256SUMS` and signed build provenance (`gh attestation verify`). (#46)

### Fixed

- A message with a very long unbroken line or deeply nested brackets no longer freezes the preview. (#46)
- `go install github.com/gausejakub/vimail@latest` works again. (#46)
- Deleting purges only the messages vimail deleted (`UID EXPUNGE`), not ones another client had marked. (#46)
- After a rejected login, reconnects back off instead of retrying immediately, and a password fixed with `vimail setup` is picked up without a restart. (#46)
- The MCP process no longer rotates or deletes the TUI's log file. (#46)
- `tls = "none"` now warns on screen and in `vimail setup`, not only in the log. (#46)

### Security

- A received email could inject terminal escape sequences through its subject, sender, body (when replying), attachment names or folder names — enough to rewrite the screen or, in terminals that allow it, overwrite the clipboard with no user action. All message-derived text is now stripped of escape sequences, control characters and bidirectional overrides when cached and when displayed. (#46)
- Line breaks in To/Subject (reachable through the MCP `send_email` tool) could inject extra headers into outgoing mail. (#46)
- The cache database was world-readable during the first session; it and its WAL files are now created owner-only, and the data directory is forced to `0700`. Deleted rows are overwritten. (#46)
- The cache encryption key is never regenerated when the keyring is merely unavailable, which could orphan encrypted mail or silently fall back to plaintext. (#46)
- Attachment and export names can no longer be `..`, contain backslash paths or disguise their extension; files are created exclusively and never follow symlinks. More executable types trigger the warning. (#46)
- "Open in browser" used a predictable shared temp path; it now uses a private per-process directory. (#46)
- A reply could be redirected by an address hidden in the sender's display name. (#46)
- MCP tools are annotated as read-only or destructive, and message content is labelled untrusted, to reduce prompt-injection risk. (#46)
- OAuth2 tokens are refused over `tls = "none"`; token refresh has a timeout. The text of sent mail is removed from the queue after delivery. (#46)
- CI uses read-only permissions and SHA-pinned actions. (#46)

## [0.13.0] - 2026-11-12

### Added

- Gmail OAuth2 sign-in using Authorization Code + PKCE with a local browser callback. You bring your own Google "Desktop app" client ID; `vimail setup` prints the URL, so it also works over SSH. (#45)
- Outlook OAuth2 with a per-account `oauth_client_id`, using XOAUTH2 for both IMAP and SMTP. (#45)
- Cache verification: `:verify [folder]`, `:verify full`, the MCP `verify` tool, and a periodic pass (`[sync] verify_interval`, default `6h`, `"0"` disables). It reconciles cached UIDs and flags with the server, checks SQLite integrity, test-decrypts a sample of cached bodies, and sweeps orphaned rows. Queued writes are never reverted. (#45)
- Flag/unflag (`f`), mark unread (`U`) and `:move <folder>` in the TUI, plus the MCP tools `flag_message`, `unflag_message`, `mark_unread` and `move_message`. Each one updates the cache right away and delivers to the server through the offline operation queue. (#45)
- A subtle motion layer: sync spinner, progress bar, status messages that clear on their own, and a brief highlight on newly arrived messages. Set `general.motion = false` for static rendering. (#45)

### Changed

- `f` now toggles the flag/star. It was previously listed as "Forward", which was never implemented. (#45)
- MCP `move_message` returns the source `folder` and UIDs plus a `destination` field. Re-list the destination folder to get the new UIDs. (#45)

### Fixed

- Long-running sessions keep working after OAuth access tokens expire: tokens are refreshed before each reconnect, and rotated refresh tokens are saved. (#45)
- Queued operations replay in the order they were made, so marking a message read and then unread while offline ends up unread on the server. (#45)
- Sync no longer piles up periodic timers when an account reports completion twice. (#45)
- Message lists sort correctly when dates have different timezone offsets. (#45)

### Security

- Upgraded `golang.org/x/net` to v0.62.0 (GO-2026-6860). CI now builds with the latest Go 1.26 patch release (GO-2026-6859). (#45)
- A stray or forged request to the OAuth callback port with the wrong `state` is rejected without aborting the real sign-in. (#45)

## [0.12.1] - 2026-08-27

### Fixed

- Vim motions now work in the mailbox (account/folder) pane: counted `j`/`k` such as `5j`, `gg`, `G`, and counted line jumps; the cursor stays visible while moving through long lists, and quickly typed commands are no longer swallowed when the terminal delivers several keys at once. The help overlay reflects this. (#44)

## [0.12.0] - 2026-08-27

### Added

- MCP `restore_messages` tool: move one or many messages back from Trash to Inbox (or another folder). Restore is server-first — Trash stays unchanged in the cache until the server confirms, then the restored messages are reconciled with their new UIDs; offline restores are queued and retried. (#43)
- MCP `mark_all_read` tool: mark every message in a folder, an account, or all accounts read with one call. It uses a whole-folder IMAP operation, so messages that were never cached are covered too, and Spam and Trash are included by default. (#43)
- MCP `list_operations` tool: see whether queued mark-read, delete, restore, and send operations were delivered, are still retrying, or failed — the MCP counterpart of the TUI's `:ops` view. (#43)
- MCP `sync` accepts `full: true` to rebuild cached headers from the server in one transaction after server-side moves or deletes made the incremental cache stale, keeping already-downloaded bodies. (#43)

### Changed

- Marking a whole folder read in the TUI (select all in visual mode, then `r`) now sends one whole-folder operation instead of one queued operation per message. (#43)

## [0.11.0] - 2026-08-26

### Added

- `vimail mcp` — a Model Context Protocol server on stdio, so local AI clients (Claude Code, Claude Desktop) can work with your mail. Read-only tools `list_accounts`, `list_folders`, `list_messages`, `read_message`, and `search_messages` are served from the local cache and never open IMAP connections; the MCP process logs to its own `vimail-mcp.log`. (#31)
- MCP write tools `save_draft`, `delete_draft`, `mark_read`, and `delete_message` (moves to Trash only — Trash and Drafts are refused), plus an explicit `sync` tool that refreshes an account or folder and delivers queued writes. Writes update the cache immediately and go through the same offline queue as the TUI. (#32)
- MCP `send_email` tool, available only with `allow_send = true` in a new `[mcp]` config section. Sending is off by default and the tool is not registered at all until you opt in. (#33)
- `mark_read` and `delete_message` accept a `uids` batch: one queue row per batch instead of one per message, with IMAP commands chunked at 500 UIDs. (#39)
- `list_recent_messages` and `read_messages` MCP tools for one-call mail review: scan a time window across every account (optionally syncing first, collapsing Gmail label copies), then read the selected bodies as one batch — fetching missing bodies without marking anything read. (#41)
- `cliamp` theme (Winamp-inspired black/green palette) and `omarchy` theme, which follows the current Omarchy palette from `~/.local/state/omarchy/current/theme/colors.toml` and falls back to your terminal's ANSI colors. Every theme now has to pass a WCAG contrast check. (#26)
- Compose editor: count prefixes work on editing commands — `2dd`, `3x`, `3J`, `2p`, `>>`, `<<`, and more — and on the `w`/`b` motions, as a single undoable step. (#23)

### Changed

- Failed queued operations are retried with exponential backoff (5 seconds up to 15 minutes, 8 attempts); a standalone MCP process retries due operations every minute, and an explicit sync always attempts delivery. (#37)
- Search deduplicates by `Message-ID`, so results now return exactly `limit` unique messages; MCP search results include a `truncated` flag. (#38)
- CI runs and enforces the standalone `pkg/vimtea` editor test suite (build, vet, test, `go mod verify`, govulncheck) alongside the root module. (#25)

### Fixed

- Vim operators in the compose editor now follow real Vim motion classes: `dw`, `db`, `d0`, `cw`, `yw`, `dF`, `dT` and friends produce the same result as Vim, `cw` on a word acts like `ce`, and `u` after `c{motion}` plus typed text restores the buffer in one step. (#21)
- `V` then `d` deletes whole lines including their line breaks and yanks them linewise, so `p`/`P` paste whole lines; the cursor lands on the first non-blank of the following line. (#42)
- `/` and `?` search wrap correctly within the cursor's own line, `?` finds a match that ends under the cursor, and `n`/`N` repeat in the original search direction. (#24)
- Log rotation and the 3-day retention are enforced while vimail is running, not only at startup; a long session can no longer keep a week of log entries. (#20)
- The async logger no longer races between `SetLevel`, logging, and shutdown, and can never send on a closed channel while quitting. (#19)
- Failed offline operations stay retryable on reconnect instead of disappearing from the queue, a mark-read that fails on the server is no longer reported as done, and batched mark-read retries are settled per folder. (#28)
- A burst of "UID not found" errors in one folder now triggers a single recovery sync instead of one overlapping sync per message. (#29)
- Two processes sharing the cache (TUI and `vimail mcp`) can no longer execute the same queued operation twice — a queued send could previously go out as a duplicate email. Operations are claimed with a 10-minute lease and account syncs take a cross-process lock. Existing databases are migrated automatically. (#30)
- MCP writes connect to IMAP on demand instead of failing with "no IMAP worker" when no TUI is running, and a sync blocked by another process's lock is reported as an error instead of a fake success. (#37)
- Distinct messages received in the same second no longer collapse into one search result, label copies return a real folder/UID handle, and encrypted cached bodies are searched correctly. (#38)
- The TUI refreshes its folder counts and open message list when another process changes the cache; MCP deletes use the server's real Trash mailbox (e.g. Seznam) on first use; stale IMAP connections are replaced before batched writes; and large deletes get a deadline per 500-UID chunk instead of one three-minute limit for the whole batch. (#40)

### Security

- Go 1.26.6 and `golang.org/x/net` v0.58.0 in both modules, clearing all reachable govulncheck advisories (including GO-2026-5028/5029/5030); `SECURITY.md` now states the current scan. (#17)
- Outbound mail over MCP is opt-in (`[mcp] allow_send`), because any connected MCP client can act as you. (#33)

[Unreleased]: https://github.com/gausejakub/vimail/compare/v0.15.5...HEAD
[0.15.5]: https://github.com/gausejakub/vimail/compare/v0.15.4...v0.15.5
[0.15.4]: https://github.com/gausejakub/vimail/compare/v0.15.3...v0.15.4
[0.15.3]: https://github.com/gausejakub/vimail/compare/v0.15.2...v0.15.3
[0.15.2]: https://github.com/gausejakub/vimail/compare/v0.15.1...v0.15.2
[0.15.1]: https://github.com/gausejakub/vimail/compare/v0.15.0...v0.15.1
[0.15.0]: https://github.com/gausejakub/vimail/compare/v0.14.1...v0.15.0
[0.14.1]: https://github.com/gausejakub/vimail/compare/v0.14.0...v0.14.1
[0.14.0]: https://github.com/gausejakub/vimail/compare/v0.13.1...v0.14.0
[0.13.1]: https://github.com/gausejakub/vimail/compare/v0.13.0...v0.13.1
[0.13.0]: https://github.com/gausejakub/vimail/compare/v0.12.1...v0.13.0
[0.12.1]: https://github.com/gausejakub/vimail/compare/v0.12.0...v0.12.1
[0.12.0]: https://github.com/gausejakub/vimail/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/gausejakub/vimail/compare/v0.10.4...v0.11.0

---

Entries begin with the introduction of this changelog (August 2026). Notable
earlier work, for context: IMAP connection reuse and operation timeouts, a TUI
freeze fix in the coordinator, AES-256-GCM encryption at rest for cached email
bodies, dangerous-attachment warnings and SMTP hardening, and the Go 1.26 /
`x/net` vulnerability remediation. See `git log` for the full history.
