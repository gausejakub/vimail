package worker

import (
	"errors"
	"io"
	"net"
	"testing"
)

// One response may not exceed the bound; a new command starts a new count.
func TestBoundedConnEndsAnOversizedResponse(t *testing.T) {
	old := maxResponseBytes
	maxResponseBytes = 1000
	defer func() { maxResponseBytes = old }()

	server, client := net.Pipe()
	defer server.Close()
	conn := &boundedConn{Conn: client}
	go func() {
		chunk := make([]byte, 400)
		for i := 0; i < 2; i++ { // 800 bytes: within the bound
			server.Write(chunk)
		}
		buf := make([]byte, 16)
		server.Read(buf) // the client's next command
		for {            // then an answer without end
			if _, err := server.Write(chunk); err != nil {
				return
			}
		}
	}()

	if _, err := io.ReadFull(conn, make([]byte, 800)); err != nil {
		t.Fatalf("a response within the bound: %v", err)
	}
	if _, err := conn.Write([]byte("a1 NOOP\r\n")); err != nil {
		t.Fatal(err)
	}
	// 800 more bytes are fine again: the count restarted with the command.
	if _, err := io.ReadFull(conn, make([]byte, 800)); err != nil {
		t.Fatalf("a second response within the bound: %v", err)
	}
	_, err := io.Copy(io.Discard, conn)
	if !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("an endless response ended with %v, want errResponseTooLarge", err)
	}
}
