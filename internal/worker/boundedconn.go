package worker

import (
	"crypto/tls"
	"errors"
	"net"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
)

// maxResponseBytes bounds what an IMAP server may send in answer to one
// command. Bodies are read with their own, smaller bound; this one is for
// everything the client library reads whole into memory: envelope strings,
// mailbox lists, search results. A server, or whoever sits in its place,
// could otherwise answer a header fetch with a string of any size. One
// gigabyte is far above a real answer (the headers of a folder with a
// million messages are about half of that) and still an end to it.
var maxResponseBytes int64 = 1 << 30

var errResponseTooLarge = errors.New("the IMAP server sent more than a response can hold; connection closed")

// boundedConn counts the bytes received since the client last wrote (every
// command is a write) and closes the connection when one response passes
// maxResponseBytes. It sits under TLS, so it works the same for implicit
// TLS, STARTTLS and plaintext connections.
type boundedConn struct {
	net.Conn
	received atomic.Int64
}

func (c *boundedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if c.received.Add(int64(n)) > maxResponseBytes {
		c.Conn.Close()
		return 0, errResponseTooLarge
	}
	return n, err
}

func (c *boundedConn) Write(p []byte) (int, error) {
	c.received.Store(0)
	return c.Conn.Write(p)
}

// dialIMAP connects like imapclient.DialTLS, DialStartTLS and DialInsecure,
// with the connection wrapped in a boundedConn. mode is "tls", "starttls"
// or "none"; opts carries the TLS configuration and the dialer.
func dialIMAP(addr, mode string, opts *imapclient.Options) (*imapclient.Client, error) {
	dialer := opts.Dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 30 * time.Second}
	}
	raw, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	conn := &boundedConn{Conn: raw}
	switch mode {
	case "tls":
		cfg := opts.TLSConfig.Clone()
		if cfg.NextProtos == nil {
			cfg.NextProtos = []string{"imap"}
		}
		tlsConn := tls.Client(conn, cfg)
		// The dial timeout covered the handshake in tls.DialWithDialer.
		raw.SetDeadline(time.Now().Add(30 * time.Second))
		if err := tlsConn.Handshake(); err != nil {
			raw.Close()
			return nil, err
		}
		raw.SetDeadline(time.Time{})
		return imapclient.New(tlsConn, opts), nil
	case "starttls":
		// NewStartTLS upgrades before anything else is sent, refuses
		// PREAUTH, and closes conn on failure.
		return imapclient.NewStartTLS(conn, opts)
	default:
		return imapclient.New(conn, opts), nil
	}
}
