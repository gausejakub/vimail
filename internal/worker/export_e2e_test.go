package worker

import (
	"crypto/x509"

	"github.com/gausejakub/vimail/internal/auth"
)

// SetTestTLSRoots makes IMAP/SMTP trust pool (tests only); call the returned
// function to restore the system roots.
func SetTestTLSRoots(pool *x509.CertPool) func() {
	testTLSRoots = pool
	return func() { testTLSRoots = nil }
}

// SetTestCredentials caches credentials so tests never consult the keyring.
func (c *Coordinator) SetTestCredentials(account string, creds *auth.Credentials) {
	c.mu.Lock()
	c.creds[account] = creds
	c.mu.Unlock()
}
