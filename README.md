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

After adding accounts to `config.toml`, run the setup command to store credentials:

```sh
vimail setup
```

This walks through each configured account and stores credentials securely in your OS keyring.

- **plain / app-password** — prompts for your password and stores it in the OS keyring
- **oauth2-gmail** — runs the OAuth2 device flow for Gmail (opens browser for authorization)
- **oauth2-outlook** — runs the OAuth2 device flow for Outlook/Microsoft 365

If no accounts are configured, `vimail setup` will remind you to add them to `config.toml` first.

## Configuration

vimail reads its config from `~/.config/vimail/config.toml`. If the file doesn't exist, defaults are used.

```toml
[general]
preview_pane = true

[theme]
name = "tokyonight"

[[accounts]]
name = "Personal"
email = "alice@example.com"
imap_host = "imap.example.com"
imap_port = 993
smtp_host = "smtp.example.com"
smtp_port = 587
auth_method = "plain"       # "plain" | "app-password" | "oauth2-gmail" | "oauth2-outlook"
tls = "tls"                 # "tls" (default) | "starttls" | "none"

[[accounts]]
name = "Gmail"
email = "alice@gmail.com"
imap_host = "imap.gmail.com"
imap_port = 993
smtp_host = "smtp.gmail.com"
smtp_port = 587
auth_method = "oauth2-gmail"
tls = "tls"
```

After adding accounts, run `vimail setup` to store credentials (see [Account Setup](#account-setup)).

## Keybindings

### Navigation

| Key | Action |
|-----|--------|
| `j` / `k` | Move down / up in current pane |
| `h` / `l` | Switch pane left / right |
| `g` / `G` | Jump to top / bottom |
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

vimail is functional with real IMAP/SMTP connectivity, OAuth2 and app-password auth, SQLite message caching, and incremental sync. Falls back to mock data when no accounts are configured.

## License

MIT
