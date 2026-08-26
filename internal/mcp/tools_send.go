package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

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
	sdk.AddTool(s.srv, &sdk.Tool{
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
		res := s.coord.SendNow(acct, worker.SendRequest{
			From:    acct,
			To:      args.To,
			Subject: args.Subject,
			Body:    args.Body,
		})
		if res.Err != nil {
			logging.Warn("mcp", "send_email failed", logging.Acct(acct), logging.Err(res.Err))
			return nil, sendEmailResult{}, fmt.Errorf("send failed (the queued op remains retryable): %w", res.Err)
		}
		logging.Info("mcp", "send_email", logging.Acct(acct), logging.KV("to", args.To), logging.KV("message_id", res.MessageID))
		return nil, sendEmailResult{Account: acct, To: args.To, MessageID: res.MessageID}, nil
	})
}
