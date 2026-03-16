# Contributing to vimail

Thanks for your interest in contributing to vimail!

## Getting Started

```bash
git clone https://github.com/gausejakub/vimail.git
cd vimail
go build -o vimail .
go test ./...
```

Requires Go 1.24+ and a terminal with truecolor support.

## Development

- **Architecture**: See the Project Structure section in [README.md](README.md)
- **TUI framework**: [Bubbletea](https://github.com/charmbracelet/bubbletea) v1.x with Elm architecture
- **Store interface**: `internal/email/store.go` — all TUI code uses this interface, never SQLite directly
- **Mock mode**: Run without config to use the built-in mock store for development

## Making Changes

1. Fork and create a feature branch from `main`
2. Make your changes
3. Run `go vet ./...` and `go test ./...`
4. Commit with a clear message (e.g. `fix: cross-folder delete`, `feat: new theme`)
5. Open a pull request

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep changes focused — one fix or feature per PR
- Add tests for new store/cache logic
- Don't add comments for self-evident code

## Reporting Bugs

Open an issue on GitHub with:
- What you expected to happen
- What actually happened
- Your OS, terminal emulator, and Go version
- Relevant log output from `~/.local/share/vimail/vimail.log`

## Security Issues

Please report security vulnerabilities privately — see [SECURITY.md](SECURITY.md).
