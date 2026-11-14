package worker

import (
	"crypto/tls"
	"crypto/x509"
)

// testTLSRoots, when set by a test (export_e2e_test.go), replaces the system
// roots: it lets end-to-end tests run IMAP and SMTP over TLS
// against servers with a throwaway certificate. Always nil in production,
// where the system roots are used.
var testTLSRoots *x509.CertPool

func clientTLSConfig(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: testTLSRoots}
}
