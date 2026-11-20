package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Every running MCP server (a stdio one an AI client spawned, or the TUI's
// HTTP one) keeps a small JSON file in the registry directory and rewrites it
// on a heartbeat. A file that has not been rewritten for staleAfter belongs
// to a process that died without cleaning up, and is ignored and removed.
// Heartbeats work the same on every platform, unlike probing PIDs.
const (
	heartbeatInterval = 15 * time.Second
	staleAfter        = 3 * heartbeatInterval
)

// Instance describes one running MCP server.
type Instance struct {
	PID        int       `json:"pid"`
	Transport  string    `json:"transport"`      // "stdio" or "http"
	Addr       string    `json:"addr,omitempty"` // HTTP endpoint URL
	Clients    []string  `json:"clients,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	AllowWrite bool      `json:"allow_write"`
	AllowSend  bool      `json:"allow_send"`
}

// RegistryDir is where running servers announce themselves.
func RegistryDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "vimail", "mcp")
}

// Registration is a live entry in the registry, kept fresh until Close.
type Registration struct {
	path string

	mu   sync.Mutex
	inst Instance

	stop chan struct{}
	done chan struct{}
}

// Register writes inst to the registry in dir and starts its heartbeat.
func Register(dir string, inst Instance) (*Registration, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	r := &Registration{
		path: filepath.Join(dir, fmt.Sprintf("%d-%s.json", inst.PID, inst.Transport)),
		inst: inst,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	if err := r.write(); err != nil {
		return nil, err
	}
	go r.heartbeat()
	return r, nil
}

// AddClient records the name of a client that connected.
func (r *Registration) AddClient(name string) {
	if r == nil || name == "" {
		return
	}
	r.mu.Lock()
	r.inst.Clients = append(r.inst.Clients, name)
	r.mu.Unlock()
	_ = r.write()
}

// Close stops the heartbeat and removes the entry.
func (r *Registration) Close() {
	if r == nil {
		return
	}
	close(r.stop)
	<-r.done
	_ = os.Remove(r.path)
}

func (r *Registration) heartbeat() {
	defer close(r.done)
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			_ = r.write()
		}
	}
}

// write replaces the file atomically so readers never see half of it.
func (r *Registration) write() error {
	r.mu.Lock()
	data, err := json.Marshal(r.inst)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// ListInstances returns the running servers in dir, oldest first, and
// removes entries whose heartbeat stopped.
func ListInstances(dir string) []Instance {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Instance
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > staleAfter {
			_ = os.Remove(path)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var inst Instance
		if json.Unmarshal(data, &inst) != nil {
			continue
		}
		out = append(out, inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}
