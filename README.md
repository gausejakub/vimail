# vmail

A terminal-based email client with Vim-style modal keybindings, written in Go.

vmail brings the speed of Vim navigation to your inbox with a 3-pane layout, modal editing, and multiple color themes.

## Features

- **Vim-style modal editing** — Normal, Insert, Visual, and Command modes
- **3-pane layout** — Mailbox sidebar, message list, and preview pane
- **8 color themes** — vmail, tokyonight, catppuccin, kanagawa, gruvbox, nord, matrix, system
- **Hot-swappable themes** — Switch with `:theme <name>` at any time
- **Compose with Vim** — Full Vim keybindings in the message body editor
- **Multiple accounts** — Manage several email accounts in one view
- **Pure Go** — No CGO required, single static binary

## Requirements

- Go 1.24+
- A terminal with truecolor support (`COLORTERM=truecolor`)

## Install

### From source

```sh
git clone https://github.com/gause/vmail.git
cd vmail
go build -o vmail .
```

Move the binary somewhere on your `$PATH`:

```sh
mv vmail ~/.local/bin/
```

### Go install

```sh
go install github.com/gause/vmail@latest
```

## Usage

```sh
vimail
```

If built locally without moving to `$PATH`:

```sh
./vimail
```

vimail launches in fullscreen (alt-screen) mode. Press `q` or `:quit` to exit.

### Account Setup

1. Add accounts to `~/.config/vimail/config.toml` (see examples below)
2. Run `vimail setup` to store credentials in your OS keyring

```sh
vimail setup
```

This walks through each configured account and securely stores your password in the OS keyring. If credentials already exist, it will ask before overwriting.

## Configuration

vimail reads its config from `~/.config/vimail/config.toml`. If the file doesn't exist, defaults are used.

```toml
[general]
preview_pane = true

[theme]
name = "tokyonight"
```

### Adding a Gmail account

Gmail requires an **app password** (regular passwords don't work with IMAP):

1. Enable 2-Factor Authentication at https://myaccount.google.com/security
2. Generate an app password at https://myaccount.google.com/apppasswords
3. Copy the 16-character code
4. Add to config:

```toml
[[accounts]]
name = "Gmail"
email = "you@gmail.com"
imap_host = "imap.gmail.com"
imap_port = 993
smtp_host = "smtp.gmail.com"
smtp_port = 587
auth_method = "app-password"
tls = "tls"
```

5. Run `vimail setup` and paste the 16-character app password when prompted

### Adding other providers

Most providers work with a regular password or app password:

```toml
[[accounts]]
name = "Personal"
email = "alice@example.com"
imap_host = "imap.example.com"
imap_port = 993
smtp_host = "smtp.example.com"
smtp_port = 587
auth_method = "plain"       # "plain" | "app-password"
tls = "tls"                 # "tls" (default) | "starttls" | "none"
```

Common provider settings:

| Provider | IMAP Host | IMAP Port | SMTP Host | SMTP Port |
|----------|-----------|-----------|-----------|-----------|
| Gmail | imap.gmail.com | 993 | smtp.gmail.com | 587 |
| Outlook/Hotmail | outlook.office365.com | 993 | smtp.office365.com | 587 |
| Yahoo | imap.mail.yahoo.com | 993 | smtp.mail.yahoo.com | 587 |
| iCloud | imap.mail.me.com | 993 | smtp.mail.me.com | 587 |
| Seznam.cz | imap.seznam.cz | 993 | smtp.seznam.cz | 465 |

After adding accounts, run `vimail setup` to store credentials.

## Keybindings

### Navigation

| Key | Action |
|-----|--------|
| `j` / `k` | Move down / up in current pane |
| `10j` / `5k` | Move N lines down / up |
| `h` / `l` | Switch pane left / right |
| `gg` / `G` | Jump to top / bottom |
| `500gg` / `500G` | Jump to line N |
| `Ctrl+D` / `Ctrl+U` | Half-page scroll (preview) |
| `Tab` / `Shift+Tab` | Next / previous pane |

### Actions

| Key | Action |
|-----|--------|
| `c` | Compose new message |
| `r` | Reply to selected message |
| `f` | Forward |
| `dd` | Delete message |
| `o` | Open in browser |
| `R` | Refresh |
| `Enter` | Open draft (in Drafts folder) |
| `Ctrl+S` | Send message (in compose) |
| `Esc` | Close overlay / save draft |

### Modes

| Key | Action |
|-----|--------|
| `:` | Enter command mode |
| `v` / `V` | Enter visual mode (select range, `d` to delete) |
| `?` | Toggle help overlay |
| `q` | Quit |

### Commands

| Command | Action |
|---------|--------|
| `:quit` / `:q` | Quit vmail |
| `:theme <name>` | Switch theme |
| `:sync` | Sync mail |

### Available themes

`vmail` `tokyonight` `catppuccin` `kanagawa` `gruvbox` `nord` `matrix` `system`

## Project structure

```
main.go                          Entry point, subcommands (setup, help)
internal/
  config/                        TOML config loading
  auth/                          OS keyring, OAuth2 device flow, setup CLI
  email/                         Domain types (Account, Folder, Message), Store interface
  cache/                         SQLite schema + Store implementation
  worker/                        IMAP worker, SMTP worker, Coordinator
  mock/                          Mock data for dev mode
  theme/                         Theme engine + 8 themes
  tui/
    app.go                       Root bubbletea model
    keys/                        Mode enum + keybinding maps
    util/                        Shared cross-component message types
    layout/                      Container, split pane, overlay
    components/
      mailbox/                   Account & folder sidebar
      msglist/                   Message list with viewport + visual mode
      preview/                   Message preview with scroll + browser open
      compose/                   Compose overlay with Vim editor
      help/                      Help overlay
      status/                    Status bar (mode badge, info)
pkg/
  vimtea/                        Vim-style editor widget
```

## Running tests

```sh
go test ./...
```

## Status

vimail is functional with real IMAP/SMTP connectivity, app-password auth, SQLite message caching, and incremental sync. Falls back to mock data when no accounts are configured.

## License

MIT
