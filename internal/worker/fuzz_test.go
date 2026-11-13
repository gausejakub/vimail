package worker

import (
	"strings"
	"testing"
	"time"
)

func FuzzParseBody(f *testing.F) {
	f.Add([]byte("Subject: x\r\nContent-Type: text/plain\r\n\r\nhello"))
	f.Add([]byte("Content-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\nContent-Type: text/html\r\n\r\n<p>x</p>\r\n--B\r\nContent-Disposition: attachment; filename=\"../a.exe\"\r\n\r\ndata\r\n--B--\r\n"))
	f.Add([]byte("Content-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\nContent-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\n"))
	f.Add([]byte("Content-Type: text/plain; charset=utf-7\r\nContent-Transfer-Encoding: base64\r\n\r\n////"))
	f.Add([]byte("<html><body><table><tr><td>x"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<16 {
			return
		}
		start := time.Now()
		res, _ := ParseBody(data)
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("ParseBody took %v for %d bytes", d, len(data))
		}
		for _, r := range res.Text {
			if r != '\n' && r != '\t' && (r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || (r >= 0x202a && r <= 0x202e)) {
				t.Fatalf("control/bidi rune %U in parsed text", r)
			}
		}
		for _, a := range res.Attachments {
			if strings.ContainsAny(a.Filename, "\x1b\r\n") {
				t.Fatalf("attachment name carries control characters: %q", a.Filename)
			}
		}
		_, _ = ExtractAttachmentData(data)
	})
}

func FuzzSafeFilename(f *testing.F) {
	for _, s := range []string{"a.pdf", "../../etc/passwd", `..\..\x`, "...", ".", "", "a/b\\c", "invoice\u202efdp.exe", " .hidden ", strings.Repeat("x", 500) + ".pdf", "a\x00b", "CON", "x:y"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		name := safeFilename(s)
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00\n\r") || strings.HasPrefix(name, ".") {
			t.Fatalf("safeFilename(%q) = %q", s, name)
		}
		if len([]rune(name)) > 200 {
			t.Fatalf("safeFilename(%q) is %d runes", s, len([]rune(name)))
		}
	})
}

func FuzzComposeRFC5322(f *testing.F) {
	f.Add("me@example.com", "you@example.com", "hi", "body")
	f.Add("me@example.com\r\nBcc: x@y", "a@b\nCc: c@d", "s\r\n\r\ninjected body", "b")
	f.Add("a@b\u2028Bcc: x", "x\x0bx", "s\u0085t", "")
	f.Fuzz(func(t *testing.T, from, to, subject, body string) {
		_, raw := ComposeRFC5322(from, to, subject, body)
		head, _, ok := strings.Cut(string(raw), "\r\n\r\n")
		if !ok {
			t.Fatalf("no header/body separator")
		}
		lines := strings.Split(head, "\r\n")
		want := []string{"From:", "To:", "Subject:", "Date:", "Message-ID:", "MIME-Version:", "Content-Type:", "Content-Transfer-Encoding:"}
		if len(lines) != len(want) {
			t.Fatalf("%d header lines, want %d:\n%q", len(lines), len(want), head)
		}
		for i, prefix := range want {
			if !strings.HasPrefix(lines[i], prefix) || strings.ContainsAny(lines[i], "\r\n") {
				t.Fatalf("header line %d = %q, want %s…", i, lines[i], prefix)
			}
		}
	})
}
