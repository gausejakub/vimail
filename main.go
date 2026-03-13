package main

import (
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
	"github.com/gausejakub/vimail/internal/tui"
	"github.com/gausejakub/vimail/internal/worker"

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
		case "help", "--help", "-h":
			fmt.Println("Usage: vimail [command]")
			fmt.Println()
			fmt.Println("Commands:")
			fmt.Println("  setup    Configure account credentials (passwords, OAuth2)")
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
		dbDir := filepath.Join(home, ".local", "share", "vimail")
		if err := os.MkdirAll(dbDir, 0700); err != nil {
			fmt.Fprintf(os.Stderr, "vimail: failed to create data dir: %v\n", err)
			os.Exit(1)
		}
		dbPath := filepath.Join(dbDir, "cache.db")
		db, err := cache.Open(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vimail: failed to open cache: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		sqlStore := cache.NewSQLiteStore(db)

		// Initialize cache encryption key (stored in OS keyring).
		if encKey, err := auth.GetCacheKey(); err == nil {
			sqlStore.SetEncryptionKey(encKey)
		} else {
			// First run — generate and store a new key.
			if newKey, err := cache.GenerateEncryptionKey(); err == nil {
				if err := auth.StoreCacheKey(newKey); err == nil {
					sqlStore.SetEncryptionKey(newKey)
					logging.Info("app", "cache encryption key initialized")
				} else {
					logging.Warn("app", "failed to store cache encryption key — bodies stored unencrypted", logging.Err(err))
				}
			}
		}

		// Seed accounts from config.
		for _, acct := range cfg.Accounts {
			if err := sqlStore.SeedAccount(acct.Name, acct.Email, acct.IMAPHost, acct.IMAPPort, acct.SMTPHost, acct.SMTPPort); err != nil {
				fmt.Fprintf(os.Stderr, "vimail: seed account %s: %v\n", acct.Email, err)
			}
		}

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

	// Clean up temp files.
	tmpDir := filepath.Join(os.TempDir(), "vimail")
	os.RemoveAll(tmpDir)
}
