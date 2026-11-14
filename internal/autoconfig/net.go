package autoconfig

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// maxAutoconfigBytes bounds an autoconfig document; real ones are a few KB.
const maxAutoconfigBytes = 256 * 1024

// NetResolver is the Resolver that uses the network.
type NetResolver struct {
	// Timeout bounds each lookup, fetch and probe. Zero means 6 seconds.
	Timeout time.Duration
}

func (n NetResolver) timeout() time.Duration {
	if n.Timeout > 0 {
		return n.Timeout
	}
	return 6 * time.Second
}

func (n NetResolver) LookupMX(ctx context.Context, domain string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, n.timeout())
	defer cancel()
	records, err := net.DefaultResolver.LookupMX(ctx, domain)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range records {
		out = append(out, strings.ToLower(strings.TrimSuffix(r.Host, ".")))
	}
	return out, nil
}

func (n NetResolver) LookupSRV(ctx context.Context, service, domain string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, n.timeout())
	defer cancel()
	_, records, err := net.DefaultResolver.LookupSRV(ctx, service, "tcp", domain)
	if err != nil {
		return "", 0, err
	}
	for _, r := range records {
		host := strings.ToLower(strings.TrimSuffix(r.Target, "."))
		if host != "" && r.Port != 0 { // target "." means "service not offered"
			return host, int(r.Port), nil
		}
	}
	return "", 0, fmt.Errorf("no %s service for %s", service, domain)
}

// Fetch gets an HTTPS URL. Redirects are followed only to other HTTPS URLs,
// and the body is limited, since the server is whoever controls the domain.
func (n NetResolver) Fetch(ctx context.Context, url string) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("refusing non-HTTPS URL")
	}
	ctx, cancel := context.WithTimeout(ctx, n.timeout())
	defer cancel()
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "vimail-setup")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxAutoconfigBytes))
}

// ProbeTLS reports whether host:port speaks TLS with a certificate valid
// for host: implicit TLS, or STARTTLS on the submission port 587. Nothing
// but the handshake is exchanged; no credentials are involved.
func (n NetResolver) ProbeTLS(ctx context.Context, host string, port int) bool {
	if !ValidHost(host) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, n.timeout())
	defer cancel()
	addr := JoinHostPort(host, port)
	cfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{}
	if port != 587 {
		conn, err := (&tls.Dialer{NetDialer: dialer, Config: cfg}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	r := bufio.NewReader(conn)
	readReply := func() bool { // reads one (possibly multi-line) SMTP reply
		for {
			line, err := r.ReadString('\n')
			if err != nil || len(line) < 4 {
				return false
			}
			if line[3] == ' ' {
				return line[0] == '2'
			}
		}
	}
	if !readReply() {
		return false
	}
	fmt.Fprintf(conn, "EHLO vimail-setup\r\n")
	if !readReply() {
		return false
	}
	fmt.Fprintf(conn, "STARTTLS\r\n")
	if !readReply() {
		return false
	}
	return tls.Client(conn, cfg).HandshakeContext(ctx) == nil
}
