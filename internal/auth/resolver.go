package auth

import "github.com/gausejakub/vimail/internal/config"

// NewResolver returns the appropriate Resolver for an account based on its auth_method.
func NewResolver(acct config.AccountConfig) Resolver {
	switch ParseAuthMethod(acct.AuthMethod) {
	case AuthOAuth2Gmail, AuthOAuth2Outlook:
		return &OAuth2Resolver{}
	default:
		return &PlainResolver{}
	}
}
