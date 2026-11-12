package worker

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestUniqueNameKeepsZipEntriesApart(t *testing.T) {
	used := map[string]bool{}
	got := []string{
		uniqueName(used, "invoice.pdf"),
		uniqueName(used, "invoice.pdf"),
		uniqueName(used, "INVOICE.PDF"), // same file on case-insensitive file systems
		uniqueName(used, "notes"),
		uniqueName(used, "notes"),
	}
	want := []string{"invoice.pdf", "invoice (1).pdf", "INVOICE (2).PDF", "notes", "notes (1)"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %q, want %q", got, want)
		}
	}
}

func TestMessageIDCannotCarryHeaderInjection(t *testing.T) {
	_, raw := ComposeRFC5322("me@example.com\r\nBcc: evil@example.com", "you@example.com", "hi", "body")
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("injected header line %q in:\n%s", line, head)
		}
	}
	if n := strings.Count(head, "\r\n"); n != 7 {
		t.Fatalf("header has %d line breaks, want 7:\n%s", n, head)
	}
}

// What an exported message looks like inside the ZIP.
func TestExportEntriesAreRebuiltAndCannotCollide(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeExportEntries(zw, "Subject_7", "From: a\n", BodyResult{
		Text: "hello",
		HTML: `<html><head><meta http-equiv="refresh" content="0;url=https://evil.example/"></head><body><img src="https://evil.example/p.gif"><p>Hello</p></body></html>`,
	}, []AttachmentData{
		{Filename: "message.html", Data: []byte("<script>evil()</script>")},
		{Filename: "metadata.txt", Data: []byte("forged")},
		{Filename: "../../escape.txt", Data: []byte("x")},
		{Filename: "invoice.pdf", Data: []byte("one")},
		{Filename: "Invoice.PDF", Data: []byte("two")},
	})
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		if _, dup := files[strings.ToLower(f.Name)]; dup {
			t.Errorf("duplicate entry %q", f.Name)
		}
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		files[strings.ToLower(f.Name)] = string(data)
		if !strings.HasPrefix(f.Name, "Subject_7/") || strings.Contains(f.Name, "..") {
			t.Errorf("entry %q escapes the message folder", f.Name)
		}
	}
	html := files["subject_7/message.html"]
	if !strings.Contains(html, "Content-Security-Policy") || !strings.Contains(html, "<p>Hello</p>") || strings.Contains(html, "evil") {
		t.Errorf("message.html is not the rebuilt message: %s", html)
	}
	if files["subject_7/metadata.txt"] != "From: a\n" {
		t.Errorf("metadata.txt = %q", files["subject_7/metadata.txt"])
	}
	for name, want := range map[string]string{
		"subject_7/attachments/message.html":    "<script>evil()</script>",
		"subject_7/attachments/metadata.txt":    "forged",
		"subject_7/attachments/escape.txt":      "x",
		"subject_7/attachments/invoice.pdf":     "one",
		"subject_7/attachments/invoice (1).pdf": "two",
	} {
		if files[name] != want {
			t.Errorf("%s = %q, want %q (entries: %d)", name, files[name], want, len(files))
		}
	}
}
