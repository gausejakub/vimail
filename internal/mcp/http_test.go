package mcp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/worker"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStartHTTPRejectsNonLoopback(t *testing.T) {
	s := newHTTPTestServer(t)
	for _, addr := range []string{"0.0.0.0:0", ":0", "192.168.1.5:0"} {
		if h, err := s.StartHTTP(addr, "tok"); err == nil {
			h.Stop()
			t.Errorf("StartHTTP(%q) succeeded, want refusal", addr)
		}
	}
}

func TestHTTPRequiresTokenAndRegisters(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newHTTPTestServer(t)
	h, err := s.StartHTTP("127.0.0.1:0", "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Stop()

	resp, err := http.Post(h.URL, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status %d, want 401", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "1"}, nil)
	sess, err := client.Connect(ctx, &sdk.StreamableClientTransport{
		Endpoint:   h.URL,
		HTTPClient: &http.Client{Transport: bearer{"secret-token"}},
	}, nil)
	if err != nil {
		t.Fatalf("connect with token: %v", err)
	}
	defer sess.Close()
	if _, err := sess.ListTools(ctx, nil); err != nil {
		t.Fatalf("list tools: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		insts := ListInstances(RegistryDir())
		if len(insts) == 1 && insts[0].Transport == "http" && len(insts[0].Clients) == 1 && insts[0].Clients[0] == "test-client" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("registry = %+v, want one http instance with test-client", insts)
		}
		time.Sleep(20 * time.Millisecond)
	}

	sess.Close()
	h.Stop()
	if insts := ListInstances(RegistryDir()); len(insts) != 0 {
		t.Fatalf("after Stop registry = %+v, want empty", insts)
	}
}

func newHTTPTestServer(t *testing.T) *Server {
	store := seededStore(t)
	cfg := config.Config{Accounts: []config.AccountConfig{{Email: "me@example.com"}}}
	return New(cfg, store, worker.NewCoordinator(cfg, store))
}

type bearer struct{ tok string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return http.DefaultTransport.RoundTrip(r)
}
