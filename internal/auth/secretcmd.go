package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// secretCmdTimeout bounds password_cmd / key_cmd. Generous, because a password
// manager may wait for the user to unlock it (biometrics, master password).
var secretCmdTimeout = 2 * time.Minute

// RunSecretCmd runs a user-configured command (e.g. `op read op://…`, `pass show …`)
// through `sh -c` and returns its stdout with trailing line endings removed.
// stdin is not connected, so the command cannot steal keystrokes from the TUI;
// stderr is reported on failure, stdout (the secret) never is.
func RunSecretCmd(command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), secretCmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("command timed out after %s", secretCmdTimeout)
	}
	if err != nil {
		if msg := firstLine(stderr.String()); msg != "" {
			return "", fmt.Errorf("command failed: %w: %s", err, msg)
		}
		return "", fmt.Errorf("command failed: %w", err)
	}

	secret := strings.TrimRight(stdout.String(), "\r\n")
	if secret == "" {
		return "", errors.New("command printed nothing")
	}
	return secret, nil
}

// CacheKeyFromCmd runs [cache] key_cmd and decodes its output: a base64-encoded
// 32-byte key, the same format vimail stores in the OS keyring
// (generate one with `openssl rand -base64 32`).
func CacheKeyFromCmd(command string) ([]byte, error) {
	encoded, err := RunSecretCmd(command)
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, errors.New("output is not valid base64")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("key is %d bytes, want 32 (generate one with: openssl rand -base64 32)", len(key))
	}
	return key, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const max = 200
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
