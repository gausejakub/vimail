package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type GeneralConfig struct {
	PreviewPane bool `toml:"preview_pane"`
}

type AccountConfig struct {
	Name       string `toml:"name"`
	Email      string `toml:"email"`
	IMAPHost   string `toml:"imap_host"`
	IMAPPort   int    `toml:"imap_port"`
	SMTPHost   string `toml:"smtp_host"`
	SMTPPort   int    `toml:"smtp_port"`
	AuthMethod string `toml:"auth_method"` // "plain" | "app-password" | "oauth2-gmail" | "oauth2-outlook"
	Username   string `toml:"username"`    // defaults to email if empty
	TLS        string `toml:"tls"`         // "tls" (default) | "starttls" | "none"
}

type ThemeConfig struct {
	Name string `toml:"name"`
}

type Config struct {
	General  GeneralConfig   `toml:"general"`
	Accounts []AccountConfig `toml:"accounts"`
	Theme    ThemeConfig     `toml:"theme"`
}

func DefaultConfig() Config {
	return Config{
		General: GeneralConfig{
			PreviewPane: true,
		},
		Theme: ThemeConfig{
			Name: "vimail",
		},
	}
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "vimail", "config.toml")
}

func Load() (Config, error) {
	cfg := DefaultConfig()

	path := configPath()
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
