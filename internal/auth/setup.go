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
		if strings.EqualFold(acct.TLS, "none") {
			fmt.Println("  WARNING: tls = \"none\" — the password and all mail for this account")
			fmt.Println("  travel unencrypted. Use \"tls\" or \"starttls\" unless the server is local.")
		}

		method := ParseAuthMethod(acct.AuthMethod)

		if acct.PasswordCmd != "" {
			if method == AuthOAuth2Gmail || method == AuthOAuth2Outlook {
				fmt.Println("  Note: password_cmd is ignored for OAuth2 accounts; tokens are kept in the OS keyring.")
			} else {
				fmt.Println("  Uses password_cmd — nothing is stored. Testing it…")
				if _, err := RunSecretCmd(acct.PasswordCmd); err != nil {
					fmt.Printf("  password_cmd failed: %v\n", err)
				} else {
					fmt.Println("  password_cmd OK.")
				}
				continue
			}
		}

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
		case AuthOAuth2Gmail:
			if acct.OAuthClientID == "" {
				fmt.Println("  Gmail OAuth requires your own Google Desktop OAuth client:")
				fmt.Println("  1. Create a Google Cloud project and enable the Gmail API.")
				fmt.Println("  2. Configure and publish the OAuth consent screen (Testing tokens expire after 7 days).")
				fmt.Println("  3. Create an OAuth client of type Desktop app.")
				fmt.Println("  4. Set oauth_client_id and oauth_client_secret on this account in config.toml.")
			}
			fallthrough
		case AuthOAuth2Outlook:
			r := &OAuth2Resolver{}
			_, err := r.RunFlow(acct)
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

	if cfg.Cache.KeyCmd != "" {
		fmt.Println("\n── Cache encryption key ──")
		if _, err := CacheKeyFromCmd(cfg.Cache.KeyCmd); err != nil {
			fmt.Printf("  [cache] key_cmd failed: %v\n", err)
		} else {
			fmt.Println("  [cache] key_cmd OK (not using the OS keyring).")
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
