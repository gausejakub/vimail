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

## [0.20.1] - 2026-11-20

### Fixed

- vimail no longer crashes when it checks an IMAP connection at the moment that connection is being closed (seen when marking messages read). (#97)

## [0.20.0] - 2026-11-20

### Added

- `:mcp` shows the running MCP servers: stdio ones AI clients started and the TUI's own, with PID, uptime, connected clients and whether they can write or send. `:mcp start [port]` serves MCP over HTTP from the TUI (loopback only, bearer token in `~/.local/share/vimail/mcp-token`, `[mcp] http_addr`), and `:mcp stop` or quitting stops it. (#96)

## [0.19.0] - 2026-11-16

### Changed

- The built-in `claude` AI agent now receives the prompt on stdin and runs with tools and MCP servers off (`--tools "" --strict-mcp-config --print`). An `[ai]` section in your config is not changed; see the README if yours still passes `{prompt}`. An agent that has not answered after three minutes is stopped. (#95)
- `:accept`, `:tentative` and `:decline` send nothing when the invitation's organizer is not the sender of the message; they say who the answer would go to, and `:accept!` sends it. (#95)
- An unknown `tls` value for IMAP is now an error, as it already was for SMTP, instead of silently meaning `tls`. (#95)
- MCP: `move_message` and `mark_all_read` are annotated destructive; `read_message` returns at most 200,000 characters and sets `body_truncated`. (#95)

### Fixed

- After `:ai` on a forward, the forwarded message is kept; it used to be replaced by the agent's text. A reply keeps its "On … wrote:" line. (#95)
- A folder whose `UIDVALIDITY` changed no longer shows the previous message's body, attachments and signature verdict under a new message after a full sync. (#95)

### Security

- **Open in browser / export:** a message could run JavaScript in the browser. The rebuilt page kept the message's own charset declaration; with ISO-2022-JP a browser decoded the file differently from the sanitizer, the Content-Security-Policy vanished and an attribute value became an event handler. The page now declares UTF-8 itself, first, and carries no control bytes. (#95)
- **Open in browser / export:** removing an `@import` from a `<style>` could join `</sty` and `le>` into an end tag, after which a meta refresh or any other markup was written out unchecked. Two smaller gaps in the CSS filter are closed as well. (#95)
- **Notifications (macOS):** a sender named `-e` had its subject passed to `osascript` as a line of AppleScript. Title and body now follow `--` and never start with a dash, for every notifier; markup in a sender name is not markup in the title on Linux. (#95)
- **OpenPGP:** an encrypted message of under 2 KB could decompress to hundreds of megabytes in memory. GnuPG output is bounded. Opening a message no longer lets gpg fetch keys from a server, and a message with two From headers has no sender a signature could vouch for. (#95)
- **AI compose:** a forwarded message and the attribution line of a reply were given to the agent as your own instructions. They are now in the section marked untrusted. (#95)
- **Queued deletes, moves and flags** could act on other messages after a folder was recreated or renumbered on the server. They are checked against the folder's `UIDVALIDITY` and dropped when it changed. Without `UID EXPUNGE`, deleting no longer purges messages another client had marked deleted. (#95)
- **Saved attachments and exports** carry the Mark of the Web on Windows and the quarantine attribute on macOS. Windows device names (`CON.txt`) are renamed, and the warning for risky file types covers about 60 more extensions (`.scf`, `.library-ms`, `.chm`, `.vhd`, `.xll`, …). (#95)
- **Hidden text:** zero-width characters, soft hyphens, blank fillers and stray variation selectors are removed when mail is cached and when it is returned over MCP; they were hidden in the preview but readable by an AI client. Emoji sequences and joining scripts keep the joiners they need. (#95)
- **Network:** SMTP sessions and the save of the sent copy have deadlines; header fetches are size-bounded; `vimail setup add` warns, and defaults to No, for servers named only by DNS outside your domain. (#95)
- **MCP:** error text quoting a mail server is sanitized; `send_email` bodies are sanitized like drafts. `save_draft` no longer changes a draft you wrote or edited (only drafts whose current version came over MCP), and `send_email` refuses a message already queued for retry instead of queueing a second copy. (#95)
- **Composer:** a draft written by an AI client says so when opened, a recipient list longer than its field is shown from its start, and the number of recipients in To, Cc and Bcc is always in view. (#95)
- **IMAP:** one response may be at most 1 GB; a server can no longer exhaust memory with an endless envelope string or folder list. (#95)
- **Gmail sign-in** moves to another loopback port when 53682 is taken, so another local user cannot block it. (#95)
- **Local:** email addresses in error text are replaced by a placeholder in logs and in the operation queue; an old log file is tightened to `0600`; vimail refuses to start, instead of generating a second key, when the keyring has lost the key of an encrypted cache. (#95)
- The message list shows the real address when a display name is a different address. (#95)
- Release workflow: tests run in a separate job without the publishing token; govulncheck is pinned. (#95)

## [0.18.1] - 2026-11-14

### Security

- Updated the Markdown library (goldmark 1.7.17) for GO-2026-5320, a cross-site scripting issue in how it renders links and images in the HTML version of Markdown mail. (#94)

## [0.18.0] - 2026-11-14

### Added

- `vimail setup add`: add an account by entering only its email address. vimail finds the servers (built-in providers, MX records, the domain's autoconfig, Thunderbird's database after asking, SRV records), shows them for confirmation, checks the login and appends the account to your config. (#81)
- Recipient suggestions in To, Cc and Bcc from addresses in your cached mail. People you have written to come first; MCP gets a `search_contacts` tool. (#82)
- Signatures: `signature` on an account and `signatures` per identity (a value can be `file:path`). New messages, replies and forwards get the signature of their From address, and it switches when you change From. MCP drafts are signed too (`signature: false` opts out). (#83)
- Archive with `A` (Gmail: removes the message from the folder), and `m` to move the message or the visual selection to a folder you type. (#84)
- `:search-server <query>` / `:ss` searches the selected account on the mail server as well, which also finds bodies that were never downloaded; MCP `search_messages` takes `server: true`. (#85)
- Desktop notifications for new mail: `[notifications] enabled = true`, with optional folders, accounts, `hide_subject` and a custom command. (#86)
- `:threads` groups the message list by conversation (`threads = true` under `[general]` makes it the default). (#87)
- Windows builds (amd64 and arm64) in releases. (#88)
- A draft changed in vimail and in another mail client before either synced is shown as `⚠ 2 versions` on both copies, also over MCP; nothing is merged or lost. (#89)
- Markdown: `:markdown` in the composer, or `markdown = true` on an account, sends the message with a formatted HTML version next to the text as written; MCP `save_draft` takes `markdown`. (#90)
- Calendar invitations are shown as an event block at the top of the message, and `:accept`, `:tentative` and `:decline` send the organizer your answer. (#91)
- OpenPGP through your own GnuPG (`[pgp] enabled = true`): signatures are checked and encrypted mail is decrypted when reading, and `:sign` / `:encrypt` (or `pgp_sign` / `pgp_encrypt` on an account) sign and encrypt what you send. The verdict is shown as a separate `PGP:` line that a message cannot fake. (#92)

### Changed

- Sent, Drafts, Trash, Spam and Archive are found from the roles the mail server advertises, so folders in any language work; `folders = { sent = "..." }` on an account names them by hand when a server advertises nothing. (#80)
- Searching the cache also matches Cc and Reply-To. (#85)

### Fixed

- Replying in one account to a message from another of your own accounts opened the reply with an empty To; it now goes back to that address. Reply all still leaves out the account you reply from. (#93)
- Gmail's `[Gmail]` container and old labels named like All Mail no longer appear as folders, and folders the server no longer lists are dropped from the sidebar. (#80)

### Security

- Setup by email shows exactly where your password will go before anything is sent, requires TLS, and ignores server names and login names from DNS or autoconfig that are not plain host or login names. (#81)
- Recipient suggestions show the address first and never rank someone who has only written to you above people you have written to, so a look-alike display name cannot take a colleague's place. (#82)
- Markdown mail never contains anything the text does not show: images become text, a link whose text differs from its address shows the address, and raw HTML is dropped. (#90)
- Invitation answers carry only the event's identifying lines and your own attendee line; an organizer who is not the sender is pointed out. (#91)
- OpenPGP verdicts apply only to the content that was actually signed or encrypted, which is also what attachments are saved from and what forwards and invitation answers use; the key must hold a valid user id for the sender's From address. (#92)

## [0.17.0] - 2026-11-14

### Added

- Drafts sync with the mail server: a draft saved in vimail is uploaded to the account's Drafts folder, so it shows up in Gmail and on your phone, and drafts written there appear in vimail. A draft sent or deleted elsewhere disappears from vimail too. Offline, uploads wait in the operation queue. (#65)
- Cc, Bcc and From fields in the composer, and attachments: `:attach <path>` and `:detach [n]` in the editor. (#65)
- Reply all (`a`) and forward (`F`). Forwarding keeps the original's attachments; reply all leaves your own addresses out. (#65)
- Identities: list an account's other addresses (aliases, other domains delivered to the same mailbox) as `identities`, and a reply goes out from the address the original was sent to. (#65)
- MCP: `save_draft` prepares complete drafts with several To/Cc/Bcc recipients, a From identity, replies (`reply_to_message`, with `reply_all` and `quote_original`) and forwards (`forward_message`), and file attachments from the directories you allow in `[mcp] attachment_dirs`. The new `read_draft` returns every field. MCP still cannot send a draft. (#65)
- MCP: reading mail returns Cc, Reply-To, Message-ID, In-Reply-To, References, a `thread_id` for grouping a conversation, and whether a message was already answered. `list_accounts` lists each account's identities. (#65)

### Changed

- **Breaking:** in MCP read output, `to` is a list of addresses instead of one string, and it holds every recipient instead of the first. (#65)
- Replies carry `In-Reply-To` and `References`, so they thread under the original in other mail clients, and the original is marked answered once the reply is sent (a forward marks it forwarded). (#65)
- Messages already in the cache get their Cc, Reply-To and threading headers on the next syncs; bodies are kept and no full resync is needed. (#65)

### Fixed

- Attachment names such as `=?UTF-8?Q?Digifest_2026...?=` are shown decoded, including names cached earlier. (#65)
- A failed upload of a sent message to the Sent folder is reported in the log instead of being ignored. (#65)
- A new draft could overwrite a draft from an earlier session that had been given the same id. (#65)

### Security

- Bcc recipients are sent only in the SMTP envelope; the Bcc header is kept in your Sent copy and drafts and never in the transmitted message. (#65)
- A From address must be exactly one of the account's identities; a value hiding a second mailbox is refused. (#65)
- MCP attachments are limited to configured directories, with symlinks resolved, and errors do not reveal whether a file outside them exists. (#65)

## [0.16.0] - 2026-11-13

### Added

- Your own theme: a TOML file in `~/.config/vimail/themes/` becomes a theme named after the file. Set any of the 22 colors and take the rest from a built-in theme; the file is re-read on `:theme <name>`, so a generated theme (from a desktop palette, for example) applies without a restart. (#64)

### Changed

- vimail opens faster: the screen is drawn first and passwords are looked up in the background, all accounts at once. With `password_cmd` each account is a separate command, and four accounts used to delay start-up by most of a second. (#63)
- Quitting no longer waits for mail servers to acknowledge the logout; one slow server could hold `q` up for several seconds. (#63)
- README: the credentials section now leads with where passwords are stored by default (macOS Keychain, Secret Service on Linux, Credential Manager on Windows) and lists more `password_cmd` examples. (#62)

## [0.15.6] - 2026-11-13

### Security

- A message whose body is one enormous line (minified HTML or JSON) froze the Vim reader for seconds per key press. Lines are now wrapped in a single pass, so a 500 KB line draws in about 10 ms. (#61)

### Fixed

- Accented and non-Latin characters can be typed in compose and in the reader's search: the editor used to accept only single-byte keys, so `č`, `ž`, `é` and the like were silently dropped. (#61)
- Moving, deleting, replacing, yanking and searching in text with such characters works one character at a time instead of one byte at a time, so a character can no longer be split in half. (#61)

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

[Unreleased]: https://github.com/gausejakub/vimail/compare/v0.16.0...HEAD
[0.16.0]: https://github.com/gausejakub/vimail/compare/v0.15.6...v0.16.0
[0.15.6]: https://github.com/gausejakub/vimail/compare/v0.15.5...v0.15.6
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
