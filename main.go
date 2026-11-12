package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/logging"
	vmcp "github.com/gausejakub/vimail/internal/mcp"
	"github.com/gausejakub/vimail/internal/tui"
	"github.com/gausejakub/vimail/internal/tui/components/preview"
	"github.com/gausejakub/vimail/internal/worker"
	"github.com/zalando/go-keyring"

	// Import theme package to trigger init() registrations.
	_ "github.com/gausejakub/vimail/internal/theme"
)

func main() {
	// Handle subcommands.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "setup":
			runSetup()
			return
		case "mcp":
			runMCP()
			return
		case "help", "--help", "-h":
			fmt.Println("Usage: vimail [command]")
			fmt.Println()
			fmt.Println("Commands:")
			fmt.Println("  setup    Configure account credentials (passwords, OAuth2)")
			fmt.Println("  mcp      Run a Model Context Protocol server on stdio (for AI clients)")
			fmt.Println("  help     Show this help")
			fmt.Println()
			fmt.Println("Run without arguments to start the email client.")
			return
		}
	}

	runTUI()
}

func runSetup() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vimail: failed to load config: %v\n", err)
		os.Exit(1)
	}
	if err := auth.RunSetup(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "vimail: setup failed: %v\n", err)
		os.Exit(1)
	}
}

// initCacheKey loads the body-encryption key from the OS keyring, creating it
// on first run. It never replaces an existing key and never silently writes
// plaintext next to already-encrypted mail.
func initCacheKey(store *cache.SQLiteStore) error {
	key, err := auth.GetCacheKey()
	switch {
	case err == nil:
		if len(key) != 32 {
			return fmt.Errorf("cache encryption key in the OS keyring is invalid (%d bytes, want 32); remove the %q keyring entry and the cache to start fresh", len(key), "vimail/_cache_encryption_key")
		}
		store.SetEncryptionKey(key)
		return nil
	case errors.Is(err, keyring.ErrNotFound):
		// First run — generate and store a new key.
		newKey, genErr := cache.GenerateEncryptionKey()
		if genErr == nil {
			genErr = auth.StoreCacheKey(newKey)
		}
		if genErr == nil {
			store.SetEncryptionKey(newKey)
			logging.Info("app", "cache encryption key initialized")
			return nil
		}
		err = genErr
	}
	// The keyring is unavailable (locked, no Secret Service, …). Generating a
	// new key here would orphan mail encrypted with the real one.
	if store.HasEncryptedBodies() {
		return fmt.Errorf("the OS keyring is unavailable (%w) and the cache holds encrypted mail; unlock the keyring and try again", err)
	}
	logging.Warn("app", "OS keyring unavailable — cached message bodies will be stored UNENCRYPTED", logging.Err(err))
	fmt.Fprintf(os.Stderr, "vimail: warning: OS keyring unavailable (%v); cached message bodies will be stored unencrypted\n", err)
	return nil
}

// openStore opens the SQLite cache, initializes the body-encryption key from
// the OS keyring, and seeds accounts from config. Shared bootstrap for the
// TUI and MCP entry points; the caller owns closing the returned db.
func openStore(cfg config.Config) (*cache.SQLiteStore, *sql.DB, error) {
	home, _ := os.UserHomeDir()
	dbDir := filepath.Join(home, ".local", "share", "vimail")
	if err := os.MkdirAll(dbDir, 0700); err != nil {
		return nil, nil, fmt.Errorf("failed to create data dir: %w", err)
	}
	// MkdirAll leaves an existing directory's mode alone; the cache holds
	// mail, so make sure other local users cannot enter it.
	_ = os.Chmod(dbDir, 0700)
	db, err := cache.Open(filepath.Join(dbDir, "cache.db"))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open cache: %w", err)
	}

	sqlStore := cache.NewSQLiteStore(db)

	// Initialize cache encryption key (stored in OS keyring).
	if err := initCacheKey(sqlStore); err != nil {
		db.Close()
		return nil, nil, err
	}

	// Seed accounts from config.
	for _, acct := range cfg.Accounts {
		if err := sqlStore.SeedAccount(acct.Name, acct.Email, acct.IMAPHost, acct.IMAPPort, acct.SMTPHost, acct.SMTPPort); err != nil {
			fmt.Fprintf(os.Stderr, "vimail: seed account %s: %v\n", acct.Email, err)
		}
	}

	return sqlStore, db, nil
}

// runMCP serves the Model Context Protocol on stdio for local AI clients.
// It logs to its own file (vimail-mcp.log) so it never contends with the
// TUI over log rotation, and v1 never opens IMAP connections.
func runMCP() {
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".local", "share", "vimail")
	if err := logging.InitFile(logDir, "vimail-mcp.log", logging.LevelInfo); err != nil {
		fmt.Fprintf(os.Stderr, "vimail: failed to init logger: %v\n", err)
	}
	defer logging.Close()

	// Stdout carries JSON-RPC; route stdlib logging into the file logger.
	log.SetOutput(logging.StdLogWriter{})
	log.SetFlags(0)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vimail: failed to load config: %v\n", err)
		os.Exit(1)
	}
	if len(cfg.Accounts) == 0 || cfg.Accounts[0].IMAPHost == "" {
		fmt.Fprintln(os.Stderr, "vimail: no accounts configured — run `vimail setup` first")
		os.Exit(1)
	}

	store, db, err := openStore(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vimail: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// The MCP process gets its own coordinator so writes and syncs work with
	// no TUI running; op claiming and the sync lock keep it safe when the
	// TUI runs alongside. Credentials are resolved lazily on first sync.
	coord := worker.NewCoordinator(cfg, store)
	defer coord.DisconnectAll()

	logging.Info("mcp", "vimail mcp server starting", logging.KV("accounts", len(cfg.Accounts)))
	srv := vmcp.New(cfg, store, coord)
	if err := srv.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
		logging.Error("mcp", "server exited with error", logging.Err(err))
		fmt.Fprintf(os.Stderr, "vimail: mcp server: %v\n", err)
		os.Exit(1)
	}
	logging.Info("mcp", "vimail mcp server shutting down")
}

func runTUI() {
	// Initialize structured logger.
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".local", "share", "vimail")
	if err := logging.Init(logDir, logging.LevelDebug); err != nil {
		fmt.Fprintf(os.Stderr, "vimail: failed to init logger: %v\n", err)
	}
	defer logging.Close()

	// Redirect stdlib log into structured logger so existing log.Printf calls are captured.
	log.SetOutput(logging.StdLogWriter{})
	log.SetFlags(0) // timestamps handled by structured logger

	// Check for truecolor support.
	ct := os.Getenv("COLORTERM")
	if ct != "truecolor" && ct != "24bit" {
		fmt.Fprintln(os.Stderr, "vimail: $COLORTERM not set to truecolor — colors may be degraded")
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vimail: failed to load config: %v\n", err)
		os.Exit(1)
	}

	logging.Info("app", "vimail starting", logging.KV("accounts", len(cfg.Accounts)))

	// If real accounts are configured, use SQLiteStore + Coordinator.
	// Otherwise fall back to MockStore for development.
	var store email.Store
	var coord *worker.Coordinator

	if len(cfg.Accounts) > 0 && cfg.Accounts[0].IMAPHost != "" {
		sqlStore, db, err := openStore(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vimail: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		store = sqlStore
		coord = worker.NewCoordinator(cfg, sqlStore)

		// Resolve credentials (non-fatal errors just log).
		if errs := coord.ResolveCredentials(); len(errs) > 0 {
			for _, e := range errs {
				logging.Error("auth", "credential resolution failed", logging.Err(e))
				fmt.Fprintf(os.Stderr, "vimail: auth warning: %v\n", e)
			}
		}
	} else {
		store = email.NewMockStore()
		logging.Info("app", "no accounts configured, using mock store")
	}

	m := tui.New(cfg, store)
	if coord != nil {
		m = tui.WithCoordinator(m, coord)
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	if coord != nil {
		coord.SetProgram(p)
	}

	// Handle OS signals for graceful shutdown.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		p.Kill() // Tell bubbletea to exit.
	}()

	if _, err := p.Run(); err != nil {
		logging.Error("app", "bubbletea error", logging.Err(err))
		fmt.Fprintf(os.Stderr, "vimail: %v\n", err)
		os.Exit(1)
	}

	logging.Info("app", "vimail shutting down")

	// Clean up.
	if coord != nil {
		coord.DisconnectAll()
	}

	// Clean up temp files (decrypted HTML opened in the browser), including
	// the fixed path used by older versions.
	preview.CleanupTemp()
	os.RemoveAll(filepath.Join(os.TempDir(), "vimail"))
}
