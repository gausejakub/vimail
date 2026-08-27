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

[Unreleased]: https://github.com/gausejakub/vimail/compare/v0.12.0...HEAD
[0.12.0]: https://github.com/gausejakub/vimail/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/gausejakub/vimail/compare/v0.10.4...v0.11.0

---

Entries begin with the introduction of this changelog (August 2026). Notable
earlier work, for context: IMAP connection reuse and operation timeouts, a TUI
freeze fix in the coordinator, AES-256-GCM encryption at rest for cached email
bodies, dangerous-attachment warnings and SMTP hardening, and the Go 1.26 /
`x/net` vulnerability remediation. See `git log` for the full history.
