package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/logging"
	"github.com/gausejakub/vimail/internal/worker"
)

type sendEmailArgs struct {
	Account string `json:"account,omitempty" jsonschema:"account email to send from; may be omitted when exactly one account is configured"`
	To      string `json:"to" jsonschema:"recipient address(es), comma-separated"`
	Subject string `json:"subject"`
	Body    string `json:"body" jsonschema:"plain-text message body"`
}

type sendEmailResult struct {
	Account   string `json:"account"`
	To        string `json:"to"`
	MessageID string `json:"message_id,omitempty"`
}

// registerSendTool registers send_email. It is only called when the user
// opted in via `[mcp] allow_send = true`: when sending is disabled the tool
// must be absent from the tool list entirely, not registered-but-erroring,
// so clients never even see an outbound-mail capability.
func (s *Server) registerSendTool() {
	addTool(s, &sdk.Tool{
		Name:        "send_email",
		Description: "Send an email via the account's SMTP server and archive it to Sent. Enabled by the [mcp] allow_send config option.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args sendEmailArgs) (*sdk.CallToolResult, sendEmailResult, error) {
		acct, err := s.resolveAccount(args.Account)
		if err != nil {
			return nil, sendEmailResult{}, err
		}
		if args.To == "" {
			return nil, sendEmailResult{}, fmt.Errorf("to is required")
		}
		if s.coord == nil {
			return nil, sendEmailResult{}, fmt.Errorf("sending is unavailable: no coordinator configured")
		}
		s.resolveCreds()

		// SendNow goes through the queued SendAndArchive path: the op is
		// claimed before executing (never a duplicate send across the TUI
		// and this process) and a failure leaves it retryable.
		// A send that failed stays queued and is retried on its own. A
		// client that answers "send failed" by calling again would put a
		// second copy in the queue, and both would be delivered.
		body := email.SanitizeText(args.Body)
		if id := s.queuedSend(acct, args.To, args.Subject, body); id != 0 {
			return nil, sendEmailResult{}, fmt.Errorf("this message (same recipients, subject and text) is already queued as operation %d and is retried automatically; it was not queued again (see list_operations)", id)
		}
		sendReq := worker.SendRequest{
			From:    acct,
			To:      args.To,
			Subject: args.Subject,
			// Cleaned like a draft saved over MCP: no escape sequences or
			// invisible characters in mail the user never typed.
			Body: body,
		}
		// The account's OpenPGP defaults hold here too: an account set to
		// encrypt must not send in the clear because the mail came through
		// MCP. A missing key fails the send.
		if ac, ok := s.cfg.Account(acct); ok && s.cfg.PGP.Enabled {
			sendReq.Sign, sendReq.Encrypt = ac.PGPSign, ac.PGPEncrypt
		}
		res := s.coord.SendNow(acct, sendReq)
		if res.Err != nil {
			logging.Warn("mcp", "send_email failed", logging.Acct(acct), logging.Err(res.Err))
			return nil, sendEmailResult{}, fmt.Errorf("send failed (the queued op remains retryable): %w", res.Err)
		}
		logging.Info("mcp", "send_email", logging.Acct(acct), logging.KV("message_id", res.MessageID))
		return nil, sendEmailResult{Account: acct, To: args.To, MessageID: res.MessageID}, nil
	})
}

// queuedSend returns the id of an unfinished send of the same message in the
// account's queue, or 0.
func (s *Server) queuedSend(acct, to, subject, body string) int64 {
	for _, op := range s.store.PendingOps() {
		if op.Type != cache.OpSend || op.Account != acct {
			continue
		}
		var p cache.SendPayload
		if json.Unmarshal(op.Payload, &p) != nil {
			continue
		}
		if p.To == to && p.Subject == subject && p.Body == body && len(p.Cc) == 0 && len(p.Bcc) == 0 {
			return op.ID
		}
	}
	return 0
}
