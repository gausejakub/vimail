package worker

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// ComposeRFC5322 builds a properly formatted RFC 5322 email message.
// Returns the message ID and the raw message bytes.
func ComposeRFC5322(from, to, subject, body string) (string, []byte) {
	msgID := fmt.Sprintf("<%d.%s@vimail>", time.Now().UnixNano(), sanitizeForMsgID(from))

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("From: %s\r\n", from))
	sb.WriteString(fmt.Sprintf("To: %s\r\n", to))
	sb.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	sb.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	sb.WriteString(fmt.Sprintf("Message-ID: %s\r\n", msgID))
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	sb.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)

	return msgID, []byte(sb.String())
}

func sanitizeForMsgID(s string) string {
	// Extract just the email part for a clean Message-ID.
	if idx := strings.Index(s, "@"); idx >= 0 {
		// Find the domain.
		domain := s[idx+1:]
		if end := strings.IndexAny(domain, "> "); end >= 0 {
			domain = domain[:end]
		}
		return domain
	}
	return "localhost"
}

func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
