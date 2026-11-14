package worker

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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

// splitHeader returns the header block and the body of a message or part,
// split at the first empty line (CRLF or LF line endings).
func splitHeader(raw []byte) (header, body []byte) {
	for i := 0; i < len(raw); {
		nl := bytes.IndexByte(raw[i:], '\n')
		if nl < 0 {
			return raw, nil
		}
		line := raw[i : i+nl]
		if len(bytes.TrimRight(line, "\r")) == 0 {
			return raw[:i], raw[i+nl+1:]
		}
		i += nl + 1
	}
	return raw, nil
}

// multipartParts splits a multipart body into the exact bytes of its parts,
// matching delimiter lines exactly as RFC 2046 defines them: "--" boundary,
// optional trailing whitespace, end of line ("--" appended on the closing
// delimiter). The preamble and epilogue are ignored. It reports false when
// the closing delimiter is missing.
func multipartParts(body []byte, boundary string) ([][]byte, bool) {
	if boundary == "" {
		return nil, false
	}
	delim, closing := "--"+boundary, "--"+boundary+"--"
	var parts [][]byte
	partStart := -1 // offset where the current part's content begins
	for i := 0; i < len(body); {
		nl := bytes.IndexByte(body[i:], '\n')
		lineEnd := len(body)
		next := len(body)
		if nl >= 0 {
			lineEnd, next = i+nl, i+nl+1
		}
		line := strings.TrimRight(string(bytes.TrimRight(body[i:lineEnd], "\r")), " \t")
		if line == delim || line == closing {
			if partStart >= 0 {
				// The line break before a delimiter belongs to the delimiter.
				end := i
				if end > partStart && body[end-1] == '\n' {
					end--
					if end > partStart && body[end-1] == '\r' {
						end--
					}
				}
				parts = append(parts, body[partStart:end])
			}
			if line == closing {
				return parts, true
			}
			partStart = next
		}
		i = next
	}
	return parts, false
}

// splitSigned returns the exact bytes of the signed part of a
// multipart/signed message and the signature. The message must have exactly
// two parts, the second being application/pgp-signature: what is shown to
// the user is then parsed from the very bytes that were verified.
func splitSigned(raw []byte, boundary string) (signed, signature []byte, ok bool) {
	_, body := splitHeader(raw)
	parts, closed := multipartParts(body, boundary)
	if !closed || len(parts) != 2 {
		return nil, nil, false
	}
	if mt, _ := topContentType(parts[1]); mt != "application/pgp-signature" {
		return nil, nil, false
	}
	_, signature = splitHeader(parts[1])
	if !bytes.Contains(signature, []byte("BEGIN PGP SIGNATURE")) {
		return nil, nil, false
	}
	return crlf(parts[0]), signature, true
}

// encryptedPayload returns the OpenPGP message of a multipart/encrypted
// message. The message must be exactly what RFC 3156 describes: two parts,
// application/pgp-encrypted then application/octet-stream. Anything else
// (an extra cleartext part next to the encrypted one, which other code
// could pick up as an attachment or invitation) yields nil.
func encryptedPayload(raw []byte) []byte {
	_, params := topContentType(raw)
	_, body := splitHeader(raw)
	parts, closed := multipartParts(body, params["boundary"])
	if !closed || len(parts) != 2 {
		return nil
	}
	if mt, _ := topContentType(parts[0]); mt != "application/pgp-encrypted" {
		return nil
	}
	if mt, _ := topContentType(parts[1]); mt != "application/octet-stream" {
		return nil
	}
	_, payload := splitHeader(parts[1])
	if !bytes.Contains(payload, []byte("BEGIN PGP MESSAGE")) {
		return nil
	}
	return payload
}

// maxSigner bounds the key user id shown, so a padded user id cannot push
// a warning out of sight.
const maxSigner = 60

// describeSignature words a signature check for the message header. from
// is the message's sender address. Warnings come first, in capitals, ahead
// of the signer's name, which is text from the key.
func describeSignature(sig pgp.Signature, from string) string {
	clip := func(s string) string {
		r := []rune(strings.Join(strings.Fields(email.SanitizeLine(s)), " "))
		if len(r) > maxSigner {
			r = append(r[:maxSigner-1], '…')
		}
		return string(r)
	}
	if sig.Multiple {
		return "SEVERAL SIGNATURES: not checked; vimail reports one signature per message"
	}
	switch sig.Status {
	case "good":
		// The name shown is the key's user id for the sender's address, and
		// only a user id GnuPG considers valid counts: not the key's primary
		// user id, which its owner chooses freely.
		var match *pgp.UserID
		var addrs []string
		for i, id := range sig.UserIDs {
			addrs = append(addrs, id.Address)
			if from != "" && id.Address == strings.ToLower(from) {
				match = &sig.UserIDs[i]
			}
		}
		switch {
		case from == "":
			return "SENDER UNKNOWN: good signature by " + clip(sig.Signer) + ", but the message's From address could not be read, so the key is not matched to a sender"
		case match == nil:
			verified := strings.Join(addrs, ", ")
			if verified == "" {
				verified = "no address you have verified"
			}
			return "NOT THE SENDER'S KEY: good signature by a key for " + clip(verified) + ", not for the sender " + clip(from) + " (" + clip(sig.Signer) + ")"
		case !sig.Trusted:
			return "UNVERIFIED KEY: good signature, but the key is not verified as theirs (" + clip(match.Text) + ")"
		}
		return "signed by " + clip(match.Text)
	case "bad":
		return "BAD SIGNATURE: the message was changed after signing, or the signature is forged"
	case "unknown key":
		return "signed with a key you do not have (" + email.SanitizeLine(sig.KeyID) + "); not checked"
	case "expired":
		return "EXPIRED OR REVOKED KEY (" + clip(sig.Signer) + ")"
	}
	return "signed, but the signature could not be checked"
}

// ParseBodyPGP is ParseBody for accounts with PGP enabled: an encrypted
// message is decrypted, a signature is checked, and the result says so in
// Security.
func ParseBodyPGP(data []byte, c *pgp.Client) (BodyResult, error) {
	if c == nil {
		return ParseBody(data)
	}
	return parsePGPEntity(data, c, messageSender(data), 0)
}

// messageSender returns the bare address of a message's From header, or ""
// when there is none or it does not parse. Only the outer message's From is
// ever used: the content of an encrypted message is chosen by whoever
// encrypted it and must not be able to replace the sender.
func messageSender(data []byte) string {
	mr, err := mail.CreateReader(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	defer mr.Close()
	list, err := mr.Header.AddressList("From")
	if err != nil || len(list) != 1 {
		return ""
	}
	return strings.ToLower(list[0].Address)
}

// maxPGPDepth bounds how deeply encrypted and signed layers are opened.
const maxPGPDepth = 3

// parsePGPEntity opens the OpenPGP layers of an entity; from is the outer
// message's sender for every layer.
func parsePGPEntity(data []byte, c *pgp.Client, from string, depth int) (BodyResult, error) {
	mediaType, params := topContentType(data)
	if depth >= maxPGPDepth && (mediaType == "multipart/encrypted" || mediaType == "multipart/signed") {
		return BodyResult{Text: "(too many nested encryption or signature layers)", Security: "not checked: too many nested layers"}, nil
	}
	switch {
	case mediaType == "multipart/encrypted" && strings.EqualFold(params["protocol"], "application/pgp-encrypted"):
		cipher := encryptedPayload(data)
		if cipher == nil {
			return BodyResult{Text: "(malformed encrypted message: not shown)", Security: "encrypted; not decrypted: malformed message"}, nil
		}
		plain, sig, err := c.Decrypt(cipher, from)
		if err != nil {
			reason := email.SanitizeLine(err.Error())
			return BodyResult{Text: "(encrypted message: " + reason + ")", Security: "encrypted; not decrypted: " + reason}, nil
		}
		// The plaintext is a MIME entity; it may itself be signed.
		res, err := parsePGPEntity(plain, c, from, depth+1)
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
		signed, signature, ok := splitSigned(data, params["boundary"])
		if !ok {
			// Not a well-formed two-part signed message: show it, claim nothing.
			res, err := ParseBody(data)
			res.Security = "signed, but the signature could not be read"
			return res, err
		}
		// What is shown is parsed from the very bytes the signature covers,
		// never from a second reading of the whole message.
		res, err := ParseBody(signed)
		if err != nil {
			return res, err
		}
		for i := range res.Attachments {
			res.Attachments[i].PartNum = "1." + res.Attachments[i].PartNum // inside the signed part
		}
		sig, verr := c.Verify(signed, signature, from)
		if verr != nil {
			res.Security = "signed; not checked: " + email.SanitizeLine(verr.Error())
			return res, nil
		}
		res.Security = describeSignature(sig, from)
		return res, nil
	}
	return ParseBody(data)
}

// effectiveContent returns the part of a message that is shown to the user:
// the decrypted plaintext of an encrypted message and the signed part of a
// signed one, layer by layer. Saving attachments, forwarding and answering
// invitations must use it, never the raw message, or a cleartext part added
// next to the protected content would be acted on in its place.
func effectiveContent(data []byte, c *pgp.Client) ([]byte, error) {
	if c == nil {
		return data, nil
	}
	from := messageSender(data)
	for depth := 0; ; depth++ {
		mediaType, params := topContentType(data)
		encrypted := mediaType == "multipart/encrypted" && strings.EqualFold(params["protocol"], "application/pgp-encrypted")
		signed := mediaType == "multipart/signed" && strings.EqualFold(params["protocol"], "application/pgp-signature")
		if !encrypted && !signed {
			return data, nil
		}
		if depth >= maxPGPDepth {
			return nil, fmt.Errorf("too many nested encryption or signature layers")
		}
		if encrypted {
			cipher := encryptedPayload(data)
			if cipher == nil {
				return nil, fmt.Errorf("malformed encrypted message")
			}
			plain, _, err := c.Decrypt(cipher, from)
			if err != nil {
				return nil, err
			}
			data = plain
			continue
		}
		part, _, ok := splitSigned(data, params["boundary"])
		if !ok {
			return data, nil // shown whole, with no verdict (see parsePGPEntity)
		}
		data = part
	}
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
// Connected workers switch to it as well.
func (c *Coordinator) SetPGP(client *pgp.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pgp = client
	// Workers already connected use it too.
	for _, w := range c.imap {
		w.pgp = client
	}
	for _, w := range c.smtp {
		w.pgp = client
	}
}

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
