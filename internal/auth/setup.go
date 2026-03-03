package auth

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/gausejakub/vimail/internal/config"
)

// RunSetup runs the interactive credential setup flow for all configured accounts.
func RunSetup(cfg config.Config) error {
	if len(cfg.Accounts) == 0 {
		fmt.Println("No accounts configured in config.toml")
		fmt.Println("Add accounts to ~/.config/vimail/config.toml first.")
		return nil
	}

	reader := bufio.NewReader(os.Stdin)

	for _, acct := range cfg.Accounts {
		fmt.Printf("\n── Setting up: %s (%s) ──\n", acct.Name, acct.Email)

		method := ParseAuthMethod(acct.AuthMethod)
		switch method {
		case AuthOAuth2Gmail, AuthOAuth2Outlook:
			r := &OAuth2Resolver{}
			_, err := r.RunDeviceFlow(acct)
			if err != nil {
				fmt.Printf("  OAuth2 setup failed: %v\n", err)
				fmt.Printf("  You can retry with 'vimail setup' or switch to app-password auth.\n")
				continue
			}
			fmt.Printf("  OAuth2 token stored successfully.\n")

		default: // plain or app-password
			fmt.Printf("  Auth method: %s\n", acct.AuthMethod)
			fmt.Printf("  Enter password for %s: ", acct.Email)
			password, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("reading password: %w", err)
			}
			password = strings.TrimSpace(password)
			if password == "" {
				fmt.Println("  Skipped (empty password).")
				continue
			}
			if err := StorePassword(acct.Email, password); err != nil {
				return fmt.Errorf("storing password for %s: %w", acct.Email, err)
			}
			fmt.Println("  Password stored in OS keyring.")
		}
	}

	fmt.Println("\nSetup complete!")
	return nil
}
