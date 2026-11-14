package email

import "strings"

// forwardMarker opens the inline copy of a forwarded message (ForwardBody).
const forwardMarker = "---------- Forwarded message ----------"

// SignatureBlock returns a signature with the conventional "-- " separator
// line, or "" when there is no signature.
func SignatureBlock(signature string) string {
	signature = strings.Trim(SanitizeText(strings.ReplaceAll(signature, "\r\n", "\n")), "\n")
	if strings.TrimSpace(signature) == "" {
		return ""
	}
	return "-- \n" + signature + "\n"
}

// InsertSignature places a signature block in a message body: above the
// quoted original of a reply or the inline copy of a forward, otherwise at
// the end. The text above it (what the user wrote, or will write) is kept.
func InsertSignature(body, block string) string {
	if block == "" {
		return body
	}
	lines := strings.Split(body, "\n")
	at := -1
	for i, line := range lines {
		if strings.HasPrefix(line, ">") || line == forwardMarker {
			at = i
			break
		}
	}
	if at < 0 {
		return strings.TrimRight(body, "\n") + "\n\n" + block
	}
	// Keep the attribution line ("On …, X wrote:") with its quote.
	if at > 0 && strings.HasSuffix(strings.TrimSpace(lines[at-1]), "wrote:") {
		at--
	}
	head := strings.TrimRight(strings.Join(lines[:at], "\n"), "\n")
	return head + "\n\n" + block + "\n" + strings.Join(lines[at:], "\n")
}

// ReplaceSignature swaps one signature block for another when the old one
// is still in the body exactly as inserted. It reports false, leaving the
// body alone, when the user has edited or removed it.
func ReplaceSignature(body, oldBlock, newBlock string) (string, bool) {
	if oldBlock == newBlock {
		return body, true
	}
	if oldBlock == "" {
		return InsertSignature(body, newBlock), true
	}
	if strings.Count(body, oldBlock) != 1 {
		return body, false
	}
	if newBlock == "" {
		// Remove the block and the blank line that separated it.
		i := strings.Index(body, oldBlock)
		head := strings.TrimRight(body[:i], "\n")
		tail := strings.TrimLeft(body[i+len(oldBlock):], "\n")
		if tail == "" {
			return head, true
		}
		return head + "\n\n" + tail, true
	}
	return strings.Replace(body, oldBlock, newBlock, 1), true
}
