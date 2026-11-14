package worker

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/emersion/go-message/mail"

	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/pgp"
)

// PGP/MIME (RFC 3156).
//
// Sending: the message's content (its MIME entity, without the message
// headers) is built first, then wrapped: multipart/signed carries it in the
// clear next to a detached signature; multipart/encrypted carries it inside
// an OpenPGP message, signed inside the encryption when both are asked for.
// The Subject and the other message headers are not encrypted.
//
// Reading: a signature is checked over the exact bytes of the signed part,
// and the verdict is kept apart from the message text (BodyResult.Security),
// so a message cannot fake it by writing similar words into its body.

// pgpBody is the wrapped content of a message: what follows the headers.
type pgpBody struct {
	contentType string
	params      map[string]string
	body        []byte
}

// crlf normalizes line endings to CRLF.
func crlf(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	return bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n"))
}

func newBoundary() string {
	var b [16]byte
	rand.Read(b[:])
	return "vimail-" + hex.EncodeToString(b[:])
}

// pgpWrap signs and/or encrypts a MIME entity for req. draftCopy encrypts to
// the sender alone: a draft must be readable only by its author, whoever it
// is addressed to.
func pgpWrap(c *pgp.Client, entity []byte, req SendRequest, draftCopy bool) (pgpBody, error) {
	if c == nil {
		return pgpBody{}, fmt.Errorf("signing or encrypting needs [pgp] enabled = true in the config")
	}
	self, ok := email.SingleAddress(req.From)
	if !ok {
		return pgpBody{}, fmt.Errorf("no sender address to sign or encrypt with")
	}
	// The signed bytes are the entity up to, not including, the line break
	// that precedes the next boundary.
	entity = bytes.TrimRight(crlf(entity), "\r\n")
	boundary := newBoundary()
	var out bytes.Buffer

	if req.Encrypt {
		recipients, hidden := []string{self}, []string(nil)
		if !draftCopy {
			for _, group := range [][]string{req.allTo(), req.Cc} {
				for _, entry := range group {
					recipients = append(recipients, recipientAddresses(entry)...)
				}
			}
			for _, entry := range req.Bcc {
				// Not named in the encrypted message, or every recipient
				// could see who was blind-copied.
				hidden = append(hidden, recipientAddresses(entry)...)
			}
		}
		signer := ""
		if req.Sign {
			signer = self
		}
		cipher, err := c.Encrypt(entity, recipients, hidden, signer)
		if err != nil {
			return pgpBody{}, err
		}
		fmt.Fprintf(&out, "--%s\r\nContent-Type: application/pgp-encrypted\r\nContent-Description: PGP/MIME version identification\r\n\r\nVersion: 1\r\n\r\n", boundary)
		fmt.Fprintf(&out, "--%s\r\nContent-Type: application/octet-stream; name=\"encrypted.asc\"\r\nContent-Description: OpenPGP encrypted message\r\nContent-Disposition: inline; filename=\"encrypted.asc\"\r\n\r\n", boundary)
		out.Write(bytes.TrimRight(crlf(cipher), "\r\n"))
		fmt.Fprintf(&out, "\r\n--%s--\r\n", boundary)
		return pgpBody{
			contentType: "multipart/encrypted",
			params:      map[string]string{"protocol": "application/pgp-encrypted", "boundary": boundary},
			body:        out.Bytes(),
		}, nil
	}

	sig, err := c.Sign(entity, self)
	if err != nil {
		return pgpBody{}, err
	}
	fmt.Fprintf(&out, "--%s\r\n", boundary)
	out.Write(entity)
	fmt.Fprintf(&out, "\r\n--%s\r\nContent-Type: application/pgp-signature; name=\"signature.asc\"\r\nContent-Description: OpenPGP digital signature\r\nContent-Disposition: attachment; filename=\"signature.asc\"\r\n\r\n", boundary)
	out.Write(bytes.TrimRight(crlf(sig), "\r\n"))
	fmt.Fprintf(&out, "\r\n--%s--\r\n", boundary)
	return pgpBody{
		contentType: "multipart/signed",
		params:      map[string]string{"micalg": "pgp-sha256", "protocol": "application/pgp-signature", "boundary": boundary},
		body:        out.Bytes(),
	}, nil
}

// splitHeader returns the header block and the body of a message or part.
func splitHeader(raw []byte) (header, body []byte) {
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return raw[:i], raw[i+4:]
	}
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		return raw[:i], raw[i+2:]
	}
	return raw, nil
}

// topContentType returns the media type and parameters of a message.
func topContentType(raw []byte) (string, map[string]string) {
	header, _ := splitHeader(raw)
	// Unfold, then find the Content-Type field.
	unfolded := strings.NewReplacer("\r\n ", " ", "\r\n\t", " ", "\n ", " ", "\n\t", " ").Replace(string(header))
	for _, line := range strings.Split(strings.ReplaceAll(unfolded, "\r\n", "\n"), "\n") {
		if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
			if mt, params, err := mime.ParseMediaType(strings.TrimSpace(value)); err == nil {
				return mt, params
			}
		}
	}
	return "", nil
}

// splitSigned returns the exact bytes of the first part of a
// multipart/signed message (what the signature covers) and the signature.
func splitSigned(raw []byte, boundary string) (signed, signature []byte, ok bool) {
	if boundary == "" {
		return nil, nil, false
	}
	_, body := splitHeader(raw)
	delim := []byte("--" + boundary)
	// First delimiter line.
	start := bytes.Index(body, delim)
	if start < 0 || (start > 0 && body[start-1] != '\n') {
		return nil, nil, false
	}
	rest := body[start+len(delim):]
	nl := bytes.IndexByte(rest, '\n')
	if nl < 0 {
		return nil, nil, false
	}
	rest = rest[nl+1:]
	// The signed part ends before the line break that precedes the next
	// delimiter.
	end := bytes.Index(rest, append([]byte("\n"), delim...))
	if end < 0 {
		return nil, nil, false
	}
	signed = rest[:end]
	signed = bytes.TrimSuffix(signed, []byte("\r"))
	sigPart := rest[end+1+len(delim):]
	if nl = bytes.IndexByte(sigPart, '\n'); nl < 0 {
		return nil, nil, false
	}
	sigPart = sigPart[nl+1:]
	if close := bytes.Index(sigPart, append([]byte("\n"), delim...)); close >= 0 {
		sigPart = sigPart[:close]
	}
	_, signature = splitHeader(sigPart)
	if !bytes.Contains(signature, []byte("BEGIN PGP SIGNATURE")) {
		return nil, nil, false
	}
	return crlf(signed), signature, true
}

// encryptedPayload returns the OpenPGP message of a multipart/encrypted
// message (its second part).
func encryptedPayload(raw []byte) []byte {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	for {
		p, err := mr.NextPart()
		if err != nil {
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(p.Body, maxPartSize))
		if err == nil && bytes.Contains(data, []byte("BEGIN PGP MESSAGE")) {
			return data
		}
	}
}

// describeSignature words a signature check for the message header. from
// is the message's sender address.
func describeSignature(sig pgp.Signature, from string) string {
	signer := email.SanitizeLine(sig.Signer)
	switch sig.Status {
	case "good":
		s := "signed by " + signer
		if !sig.Trusted {
			s += " (good signature, but the key is not verified as theirs)"
		}
		if from != "" && !strings.Contains(strings.ToLower(signer), strings.ToLower(from)) {
			s += "; the key's address differs from the sender " + from
		}
		return s
	case "bad":
		return "BAD SIGNATURE: the message was changed after signing, or the signature is forged"
	case "unknown key":
		return "signed with a key you do not have (" + email.SanitizeLine(sig.KeyID) + "); not checked"
	case "expired":
		return "signed by " + signer + " with an expired or revoked key"
	}
	return "signed, but the signature could not be checked"
}

// ParseBodyPGP is ParseBody for accounts with PGP enabled: an encrypted
// message is decrypted, a signature is checked, and the result says so in
// Security.
func ParseBodyPGP(data []byte, c *pgp.Client) (BodyResult, error) {
	return parseBodyPGP(data, c, "")
}

// parseBodyPGP does the work; sender is the outer message's From address,
// which a decrypted inner entity does not repeat.
func parseBodyPGP(data []byte, c *pgp.Client, sender string) (BodyResult, error) {
	if c == nil {
		return ParseBody(data)
	}
	mediaType, params := topContentType(data)
	from := sender
	if mr, err := mail.CreateReader(bytes.NewReader(data)); err == nil {
		if list, err := mr.Header.AddressList("From"); err == nil && len(list) > 0 {
			from = strings.ToLower(list[0].Address)
		}
		mr.Close()
	}
	switch {
	case mediaType == "multipart/encrypted" && strings.EqualFold(params["protocol"], "application/pgp-encrypted"):
		cipher := encryptedPayload(data)
		if cipher == nil {
			return BodyResult{Text: "(encrypted message without a readable OpenPGP part)", Security: "encrypted; not decrypted"}, nil
		}
		plain, sig, err := c.Decrypt(cipher)
		if err != nil {
			reason := email.SanitizeLine(err.Error())
			return BodyResult{Text: "(encrypted message: " + reason + ")", Security: "encrypted; not decrypted: " + reason}, nil
		}
		// The plaintext is a MIME entity; it may itself be signed.
		res, err := parseBodyPGP(plain, c, from)
		if err != nil {
			return res, err
		}
		res.Decrypted = true
		switch {
		case sig != nil:
			res.Security = "encrypted; " + describeSignature(*sig, from)
		case res.Security != "":
			res.Security = "encrypted; " + res.Security
		default:
			res.Security = "encrypted; not signed"
		}
		return res, nil

	case mediaType == "multipart/signed" && strings.EqualFold(params["protocol"], "application/pgp-signature"):
		res, err := ParseBody(data)
		if err != nil {
			return res, err
		}
		// The signature is not an attachment of the message.
		kept := res.Attachments[:0]
		for _, a := range res.Attachments {
			if a.ContentType != "application/pgp-signature" {
				kept = append(kept, a)
			}
		}
		res.Attachments = kept
		signed, signature, ok := splitSigned(data, params["boundary"])
		if !ok {
			res.Security = "signed, but the signature could not be read"
			return res, nil
		}
		sig, verr := c.Verify(signed, signature)
		if verr != nil {
			res.Security = "signed; not checked: " + email.SanitizeLine(verr.Error())
			return res, nil
		}
		res.Security = describeSignature(sig, from)
		return res, nil
	}
	return ParseBody(data)
}

// pgpClient returns the OpenPGP client for a configuration, or nil when
// PGP is off.
func pgpClient(cfg config.Config) *pgp.Client {
	if !cfg.PGP.Enabled {
		return nil
	}
	return &pgp.Client{Bin: cfg.PGP.GPG}
}

// PGP returns the coordinator's OpenPGP client (nil when PGP is off).
func (c *Coordinator) PGP() *pgp.Client { return c.pgp }

// SetPGP replaces the OpenPGP client (tests use a throwaway keyring).
// Workers created afterwards use it.
func (c *Coordinator) SetPGP(client *pgp.Client) { c.pgp = client }

// parseBody parses a fetched message, with OpenPGP when it is enabled.
func (w *IMAPWorker) parseBody(data []byte) (BodyResult, error) {
	return ParseBodyPGP(data, w.pgp)
}

// cacheBody stores a fetched body. The plaintext of an encrypted message
// is written to the cache only when the cache itself is encrypted;
// otherwise the cache keeps a placeholder and the text lives in memory for
// as long as the message is open.
func (w *IMAPWorker) cacheBody(folder string, uid uint32, result BodyResult) {
	text, html, attachments := result.Text, result.HTML, result.Attachments
	if result.Decrypted && !w.store.EncryptionEnabled() {
		text, html = "(encrypted message; decrypted when opened, not kept in the unencrypted cache)", ""
	}
	w.store.UpdateMessageBody(w.acct.Email, folder, uid, text, html, attachments)
	w.store.SetMessageSecurity(w.acct.Email, folder, uid, result.Security)
}

// draftPGPHeader carries a draft's sign/encrypt choice through the server
// Drafts folder. It is on the draft copy only, outside the encryption.
const draftPGPHeader = "X-Vimail-PGP"

func draftPGPFlags(d email.Draft) string {
	var flags []string
	if d.Sign {
		flags = append(flags, "sign")
	}
	if d.Encrypt {
		flags = append(flags, "encrypt")
	}
	return strings.Join(flags, ",")
}
