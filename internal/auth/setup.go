package auth

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/gausejakub/vimail/internal/config"
	"golang.org/x/term"
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

		// Check if credentials already exist.
		if hasCredentials(acct.Email, method) {
			fmt.Printf("  Credentials already configured.\n")
			fmt.Printf("  Re-enter credentials? [y/N]: ")
			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer != "y" && answer != "yes" {
				fmt.Println("  Skipped.")
				continue
			}
		}

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
			raw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println() // newline after hidden input
			if err != nil {
				return fmt.Errorf("reading password: %w", err)
			}
			password := strings.TrimSpace(string(raw))
			// Clear password bytes from memory.
			for i := range raw {
				raw[i] = 0
			}
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

// hasCredentials checks whether credentials already exist in the keyring for an account.
func hasCredentials(email string, method AuthMethod) bool {
	switch method {
	case AuthOAuth2Gmail, AuthOAuth2Outlook:
		tok, err := GetPassword(email + ":oauth2-token")
		return err == nil && tok != ""
	default:
		pw, err := GetPassword(email)
		return err == nil && pw != ""
	}
}
