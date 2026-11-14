package email

import (
	"strings"
	"testing"
)

func TestSignatureInsertAndReplace(t *testing.T) {
	work := SignatureBlock("Jakub Gause\r\nLettr\n\n")
	if work != "-- \nJakub Gause\nLettr\n" {
		t.Fatalf("block = %q", work)
	}
	if SignatureBlock("  \n ") != "" || InsertSignature("hello", "") != "hello" {
		t.Fatal("empty signature produced a block")
	}
	if strings.Contains(SignatureBlock("x\x1b[2J"), "\x1b") {
		t.Fatal("signature not sanitized")
	}

	// New message: at the end, with room to write above.
	if got := InsertSignature("", work); got != "\n\n"+work {
		t.Fatalf("new = %q", got)
	}
	if got := InsertSignature("Hi Petr,\n", work); got != "Hi Petr,\n\n"+work {
		t.Fatalf("with text = %q", got)
	}

	// Reply: above the attribution line and the quote.
	quote := "On Thu, 12 Nov 2026 at 10:00, Petr <p@x.test> wrote:\n> Are you coming?\n>> older\n"
	got := InsertSignature("Yes.\n\n"+quote, work)
	if got != "Yes.\n\n"+work+"\n"+quote {
		t.Fatalf("reply = %q", got)
	}
	// Forward: above the inline copy.
	fwd := forwardMarker + "\nFrom: a@x.test\n\nbody"
	if got := InsertSignature("FYI\n\n"+fwd, work); got != "FYI\n\n"+work+"\n"+fwd {
		t.Fatalf("forward = %q", got)
	}

	// Switching identity swaps an untouched signature.
	personal := SignatureBlock("Jakub")
	swapped, ok := ReplaceSignature(got, work, personal)
	if !ok || !strings.Contains(swapped, personal) || strings.Contains(swapped, "Lettr") || !strings.Contains(swapped, quote) {
		t.Fatalf("swap = %q %v", swapped, ok)
	}
	// An edited signature is left alone.
	edited := strings.Replace(got, "Lettr", "Lettr s.r.o.", 1)
	if out, ok := ReplaceSignature(edited, work, personal); ok || out != edited {
		t.Fatalf("edited signature replaced: %q", out)
	}
	// To or from "no signature".
	if out, ok := ReplaceSignature("Yes.\n\n"+work+"\n"+quote, work, ""); !ok || out != "Yes.\n\n"+quote {
		t.Fatalf("removed = %q %v", out, ok)
	}
	if out, ok := ReplaceSignature("Yes.\n\n"+quote, "", work); !ok || out != "Yes.\n\n"+work+"\n"+quote {
		t.Fatalf("added = %q %v", out, ok)
	}
}
