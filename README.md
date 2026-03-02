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
vmail
```

If built locally without moving to `$PATH`:

```sh
./vmail
```

vmail launches in fullscreen (alt-screen) mode. Press `q` or `:quit` to exit.

## Configuration

vmail reads its config from `~/.config/vmail/config.toml`. If the file doesn't exist, defaults are used.

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

[[accounts]]
name = "Work"
email = "alice@acme.corp"
imap_host = "imap.acme.corp"
imap_port = 993
smtp_host = "smtp.acme.corp"
smtp_port = 587
```

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
| `d` | Delete |
| `R` | Refresh |
| `Enter` | Open draft (in Drafts folder) |
| `Ctrl+S` | Send message (in compose) |
| `Esc` | Close overlay / save draft |

### Modes

| Key | Action |
|-----|--------|
| `:` | Enter command mode |
| `v` | Enter visual mode |
| `?` | Toggle help overlay |
| `q` | Quit |

### Commands

| Command | Action |
|---------|--------|
| `:quit` / `:q` | Quit vmail |
| `:theme <name>` | Switch theme |
| `:sync` | Sync mail (not yet implemented) |

### Available themes

`vmail` `tokyonight` `catppuccin` `kanagawa` `gruvbox` `nord` `matrix` `system`

## Project structure

```
main.go                          Entry point
internal/
  config/                        TOML config loading
  mock/                          Mock accounts, folders, messages
  theme/                         Theme engine + 8 themes
  cache/                         SQLite schema (skeleton)
  worker/                        IMAP/SMTP types (skeleton)
  tui/
    app.go                       Root bubbletea model
    keys/                        Mode enum + keybinding maps
    util/                        Shared cross-component message types
    layout/                      Container, split pane, overlay
    components/
      mailbox/                   Account & folder sidebar
      msglist/                   Message list with viewport
      preview/                   Message preview with scroll
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

vmail is in early development. The TUI shell is functional with mock data — real IMAP/SMTP connectivity is not yet implemented.

## License

MIT
