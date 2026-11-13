package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type GeneralConfig struct {
	PreviewPane bool `toml:"preview_pane"`
	Motion      bool `toml:"motion"`
}

type AccountConfig struct {
	Name              string   `toml:"name"`
	Email             string   `toml:"email"`
	IMAPHost          string   `toml:"imap_host"`
	IMAPPort          int      `toml:"imap_port"`
	SMTPHost          string   `toml:"smtp_host"`
	SMTPPort          int      `toml:"smtp_port"`
	AuthMethod        string   `toml:"auth_method"` // "plain" | "app-password" | "oauth2-gmail" | "oauth2-outlook"
	Username          string   `toml:"username"`    // defaults to email if empty
	TLS               string   `toml:"tls"`         // "tls" (default) | "starttls" | "none"
	OAuthClientID     string   `toml:"oauth_client_id"`
	OAuthClientSecret string   `toml:"oauth_client_secret"`
	SkipFolders       []string `toml:"skip_folders"` // folders to skip syncing (auto-detected for Gmail)
	PasswordCmd       string   `toml:"password_cmd"` // plain/app-password: command printing the password, used instead of the OS keyring
}

// CacheConfig controls the local mail cache.
type CacheConfig struct {
	// KeyCmd prints the base64 cache encryption key; when set, the OS keyring is not used for it.
	KeyCmd string `toml:"key_cmd"`
}

type SyncConfig struct {
	VerifyInterval string `toml:"verify_interval"`
}

func (s SyncConfig) VerificationInterval() (time.Duration, error) {
	value := strings.TrimSpace(s.VerifyInterval)
	if value == "" {
		return 6 * time.Hour, nil
	}
	if value == "0" {
		return 0, nil
	}
	return time.ParseDuration(value)
}

// IsGmail returns true if this account uses Gmail IMAP.
func (a AccountConfig) IsGmail() bool {
	return a.IMAPHost == "imap.gmail.com"
}

// DefaultGmailSkipFolders returns folders that are redundant for Gmail accounts.
var DefaultGmailSkipFolders = []string{"All Mail", "Important", "[Gmail]/Všechny zprávy"}

// ShouldSkipFolder returns true if the given display folder name should be skipped during sync.
func (a AccountConfig) ShouldSkipFolder(folder string) bool {
	skipList := a.SkipFolders
	if len(skipList) == 0 && a.IsGmail() {
		skipList = DefaultGmailSkipFolders
	}
	for _, s := range skipList {
		if s == folder {
			return true
		}
	}
	return false
}

type ThemeConfig struct {
	Name string `toml:"name"`
}

type AIAgentConfig struct {
	Name string   `toml:"name"`
	Cmd  string   `toml:"cmd"`
	Args []string `toml:"args"`
}

type AIConfig struct {
	Default string          `toml:"default"`
	Agents  []AIAgentConfig `toml:"agents"`
}

// MCPConfig controls the `vimail mcp` server. Sending is opt-in: any
// connected MCP client can act as the user, so outbound mail requires an
// explicit allow_send = true. When false (the default, and when the section
// is absent) the send_email tool is not registered at all.
type MCPConfig struct {
	AllowSend bool `toml:"allow_send"`
	// AllowWrite exposes the tools that change mailbox state (mark, flag,
	// move, delete, restore, drafts). Off by default: mail is untrusted input
	// to the AI client, so a prompt-injected client should not be able to
	// alter or hide mail unless the user opted in.
	AllowWrite bool `toml:"allow_write"`
}

type Config struct {
	General  GeneralConfig   `toml:"general"`
	Accounts []AccountConfig `toml:"accounts"`
	Theme    ThemeConfig     `toml:"theme"`
	AI       AIConfig        `toml:"ai"`
	MCP      MCPConfig       `toml:"mcp"`
	Sync     SyncConfig      `toml:"sync"`
	Cache    CacheConfig     `toml:"cache"`
}

func DefaultConfig() Config {
	return Config{
		General: GeneralConfig{
			PreviewPane: true,
			Motion:      true,
		},
		Theme: ThemeConfig{
			Name: "vimail",
		},
		AI: AIConfig{
			Default: "claude",
			Agents: []AIAgentConfig{
				{Name: "claude", Cmd: "claude", Args: []string{"--print", "-p", "{prompt}"}},
			},
		},
	}
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "vimail", "config.toml")
}

// ThemesDir is where user theme files (<name>.toml) are read from.
func ThemesDir() string {
	return filepath.Join(filepath.Dir(configPath()), "themes")
}

func Load() (Config, error) {
	cfg := DefaultConfig()

	path := configPath()

	// Ensure config file has restrictive permissions (owner-only).
	if info, err := os.Stat(path); err == nil {
		perm := info.Mode().Perm()
		if perm&0077 != 0 {
			os.Chmod(path, 0600)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}
