package mcp

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gausejakub/vimail/internal/logging"
)

// DefaultHTTPAddr is where `:mcp start` listens when no port is given. A
// fixed port keeps the URL in the client's config valid across restarts.
const DefaultHTTPAddr = "127.0.0.1:8737"

// maxRequestBody caps one JSON-RPC request; save_draft bodies fit easily.
const maxRequestBody = 4 << 20

// HTTPServer serves MCP over Streamable HTTP on a loopback address.
type HTTPServer struct {
	URL   string // endpoint, e.g. http://127.0.0.1:8737/mcp
	Token string // bearer token clients must send

	httpSrv *http.Server
	reg     *Registration
	done    chan error

	stopOnce sync.Once
	stopErr  error
}

// StartHTTP serves s over HTTP on addr until Stop. Only loopback addresses
// are accepted, and every request must carry the bearer token: any local
// process (another user's, or a web page through DNS rebinding) could
// otherwise read the user's mail.
func (s *Server) StartHTTP(addr, token string) (*HTTPServer, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("address %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("address %q is not a loopback address", addr)
	}
	if token == "" {
		return nil, errors.New("no access token")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s.srv },
		&sdk.StreamableHTTPOptions{
			MaxRequestBodyBytes:   maxRequestBody,
			CrossOriginProtection: http.NewCrossOriginProtection(),
		})
	mux := http.NewServeMux()
	mux.Handle("/mcp", requireToken(token, handler))

	h := &HTTPServer{
		URL:   "http://" + ln.Addr().String() + "/mcp",
		Token: token,
		httpSrv: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		},
		done: make(chan error, 1),
	}
	if reg, err := Register(RegistryDir(), s.instance("http", h.URL)); err == nil {
		h.reg = reg
		s.reg.Store(reg)
	}
	go func() {
		err := h.httpSrv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		h.done <- err
	}()
	logging.Info("mcp", "http server started", logging.KV("url", h.URL))
	return h, nil
}

// Stop closes the listener and every open client stream. It is safe to
// call more than once.
func (h *HTTPServer) Stop() error {
	h.stopOnce.Do(func() {
		h.stopErr = h.httpSrv.Close()
		if e := <-h.done; h.stopErr == nil {
			h.stopErr = e
		}
		h.reg.Close()
		logging.Info("mcp", "http server stopped", logging.KV("url", h.URL))
	})
	return h.stopErr
}

func requireToken(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="vimail"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LoadOrCreateToken returns the HTTP access token kept in dir, creating it
// on first use. It persists so a client's configured header keeps working.
func LoadOrCreateToken(dir string) (string, error) {
	path := filepath.Join(dir, "mcp-token")
	if data, err := os.ReadFile(path); err == nil {
		if tok := strings.TrimSpace(string(data)); len(tok) >= 32 {
			return tok, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}
