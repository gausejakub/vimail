# vimail

A terminal-based email client with Vim-style modal keybindings, written in Go.

vimail brings the speed of Vim navigation to your inbox with a 3-pane layout, modal editing, and multiple color themes.

## Features

- **Vim-style modal editing** — Normal, Insert, Visual, and Command modes
- **3-pane layout** — Mailbox sidebar, message list, and preview pane
- **8 color themes** — vimail, tokyonight, catppuccin, kanagawa, gruvbox, nord, matrix, system
- **Hot-swappable themes** — Switch with `:theme <name>` at any time
- **AI compose assistant** — `:ai` in the editor to draft or rewrite emails using any CLI agent
- **Compose with Vim** — Full Vim keybindings in the message body editor
- **Global search** — Press `/` to search across all accounts and folders
- **Multiple accounts** — Manage several email accounts in one view
- **Export to ZIP** — Press `E` to export messages with text, HTML, metadata, and attachments
- **Attachments** — View metadata in preview, save to disk with `S`
- **Visual mode batch ops** — Select messages with `v`, then delete (`d`) or mark as read (`r`)
- **HTML email rendering** — Clean text conversion via html2text, open raw HTML in browser with `o`
- **JSON auto-format** — Pretty-prints JSON bodies in the preview pane
- **Incremental sync** — Per-account IMAP sync with loading indicators
- **Offline operation queue** — Deletes, sends, and mark-read ops are queued in SQLite and retried on reconnect
- **Pure Go** — No CGO required, single static binary

## Requirements

- Go 1.24+
- A terminal with truecolor support (`COLORTERM=truecolor`)

## Install

### Download binary

Grab the latest release from [GitHub Releases](https://github.com/gausejakub/vimail/releases/latest) — no Go required.

### From source

```sh
git clone https://github.com/gausejakub/vimail.git
cd vimail
go build -o vimail .
```

Move the binary somewhere on your `$PATH`:

```sh
mv vimail ~/.local/bin/
```

### Go install

```sh
go install github.com/gausejakub/vimail@latest
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
| `S` | Save attachments to ~/Downloads |
| `E` | Export message(s) to ZIP |
| `o` | Open in browser |
| `R` | Refresh |
| `Enter` | Open draft (in Drafts folder) |
| `Ctrl+S` | Send message (in compose) |
| `Esc` | Close overlay / save draft / clear search |
| `/` | Search all accounts and folders |

### Modes

| Key | Action |
|-----|--------|
| `:` | Enter command mode |
| `v` / `V` | Enter visual mode (select range, `d` to delete, `r` to mark read) |
| `?` | Toggle help overlay |
| `q` | Quit |

### Commands

| Command | Action |
|---------|--------|
| `:quit` / `:q` | Quit vimail |
| `:theme <name>` | Switch theme |
| `:sync` | Sync mail |
| `:ai` | AI-assisted compose (default agent) |
| `:ai <name>` | AI-assisted compose with a specific agent |
| `:ops` / `:queue` | Show operation queue log |
| `:ps` / `:processes` | Show running background processes |
| `:search <query>` / `:s <query>` | Search messages across all accounts |

### Available themes

`vimail` `tokyonight` `catppuccin` `kanagawa` `gruvbox` `nord` `matrix` `system`

## AI Compose Assistant

vimail can use any CLI-based AI tool to help draft, rewrite, or reply to emails. Type `:ai` in the compose editor and the current body is sent to the AI agent — the response replaces the editor content.

### How it works

1. Open compose (`c`), reply (`r`), or a draft (`Enter`)
2. Type a prompt in the body (e.g. "write a polite decline to this meeting")
3. Press `Esc` to enter normal mode
4. Type `:ai` and press `Enter`
5. The hint line shows "Thinking..." while the agent runs
6. The response replaces the editor body — review, edit, and send with `Ctrl+S`

When replying, the quoted text (lines starting with `>`) is included as context, so the agent writes a reply to the original message.

### Default setup

Out of the box, vimail uses [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-cli). If `claude` is in your `$PATH`, no configuration is needed — just use `:ai`.

### Configuring agents

Add an `[ai]` section to `~/.config/vimail/config.toml` to define one or more agents:

```toml
[ai]
default = "claude"

[[ai.agents]]
name = "claude"
cmd = "claude"
args = ["--print", "-p", "{prompt}"]

[[ai.agents]]
name = "ollama"
cmd = "ollama"
args = ["run", "llama3.2", "{prompt}"]

[[ai.agents]]
name = "gemini"
cmd = "gemini"
args = ["-p", "{prompt}"]

[[ai.agents]]
name = "gpt"
cmd = "sgpt"
args = ["--no-md", "{prompt}"]

[[ai.agents]]
name = "local"
cmd = "llm"
args = ["-m", "mistral", "{prompt}"]
```

Each agent needs:

| Field | Description |
|-------|-------------|
| `name` | Identifier used with `:ai <name>` |
| `cmd` | Binary name or path (must be in `$PATH`) |
| `args` | Arguments passed to the binary. `{prompt}` is replaced with the full prompt |

The `{prompt}` placeholder is replaced with the system prompt (which includes To, Subject, and compose context) plus the editor body.

### Usage examples

| Command | What happens |
|---------|-------------|
| `:ai` | Uses the default agent |
| `:ai ollama` | Uses the agent named "ollama" |
| `:ai gpt` | Uses the agent named "gpt" |

### Compatible CLI tools

Any tool that accepts a prompt as an argument and prints the response to stdout will work:

- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-cli) — `claude --print -p`
- [Ollama](https://ollama.com) — `ollama run <model>`
- [llm](https://github.com/simonw/llm) — `llm -m <model>`
- [Gemini CLI](https://github.com/google-gemini/gemini-cli) — `gemini -p`
- [sgpt](https://github.com/tbckr/sgpt) — `sgpt --no-md`
- [aichat](https://github.com/sigoden/aichat) — `aichat -m <model>`
- [mods](https://github.com/charmbracelet/mods) — `mods`

## Logs

vimail writes structured JSON logs to `~/.local/share/vimail/vimail.log`. Every background operation (sync, fetch, send, delete, mark-read), user action, and error is logged with full context (account, folder, UID, duration).

Logs auto-rotate at 10 MB and are deleted after 3 days.

```sh
# Tail logs in real time
tail -f ~/.local/share/vimail/vimail.log | jq .

# Filter errors
cat ~/.local/share/vimail/vimail.log | jq 'select(.level == "error")'

# Show sync operations for a specific account
cat ~/.local/share/vimail/vimail.log | jq 'select(.op == "sync" and .account == "you@gmail.com")'
```

## Project structure

```
main.go                          Entry point, subcommands (setup, help)
internal/
  config/                        TOML config loading
  auth/                          OS keyring, OAuth2 device flow, setup CLI
  email/                         Domain types (Account, Folder, Message), Store interface
  ai/                            AI agent CLI wrapper (claude, ollama, etc.)
  logging/                       Async structured JSON logger with rotation
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

vimail is functional with real IMAP/SMTP connectivity, app-password auth, SQLite message caching, incremental sync, and an offline operation queue. Falls back to mock data when no accounts are configured.

## License

MIT
