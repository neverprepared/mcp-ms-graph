package tools

import (
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/neverprepared/mcp-ms-graph/internal/client"
	"github.com/neverprepared/mcp-ms-graph/internal/util"
)

func RegisterMailTools(s *server.MCPServer, c *client.GraphClient) {
	s.AddTool(mcp.NewTool("list_emails",
		mcp.WithDescription("List recent emails from inbox."),
		mcp.WithNumber("limit", mcp.Description("Max emails to return (default 20).")),
	), wrap("list_emails", func(req mcp.CallToolRequest) (string, error) {
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		msgs, err := g.ListInboxMessages(intDefault(req, "limit", defaultLimit, maxLimit), "", false)
		if err != nil {
			return "", err
		}
		type emailSummary struct {
			ID          string `json:"id"`
			From        string `json:"from"`
			Subject     string `json:"subject"`
			Preview     string `json:"preview"`
			Date        string `json:"date"`
			IsRead      bool   `json:"is_read"`
			Attachments bool   `json:"has_attachments"`
		}
		var summaries []emailSummary
		for _, m := range msgs {
			from := ""
			if m.From != nil {
				from = m.From.EmailAddress.Name
				if from == "" {
					from = m.From.EmailAddress.Address
				}
			}
			summaries = append(summaries, emailSummary{
				ID:          m.ID,
				From:        from,
				Subject:     m.Subject,
				Preview:     m.BodyPreview,
				Date:        m.ReceivedDateTime.Format(time.RFC3339),
				IsRead:      m.IsRead,
				Attachments: m.HasAttachments,
			})
		}
		return okJSON(summaries), nil
	}))

	s.AddTool(mcp.NewTool("read_email",
		mcp.WithDescription("Read a specific email message in full."),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("The email message ID.")),
	), wrap("read_email", func(req mcp.CallToolRequest) (string, error) {
		id := strArg(req, "message_id")
		if id == "" {
			return "", fmt.Errorf("message_id is required")
		}
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		msg, err := g.GetMailMessage(id)
		if err != nil {
			return "", err
		}
		body := msg.Body.Content
		if msg.Body.ContentType == "html" {
			body = util.HTMLToText(body)
		}
		from := ""
		if msg.From != nil {
			from = fmt.Sprintf("%s <%s>", msg.From.EmailAddress.Name, msg.From.EmailAddress.Address)
		}
		var tos []string
		for _, r := range msg.ToRecipients {
			tos = append(tos, fmt.Sprintf("%s <%s>", r.EmailAddress.Name, r.EmailAddress.Address))
		}
		return okJSON(map[string]any{
			"subject": msg.Subject,
			"from":    from,
			"to":      tos,
			"date":    msg.ReceivedDateTime.Format(time.RFC3339),
			"body":    body,
		}), nil
	}))

	s.AddTool(mcp.NewTool("send_email",
		mcp.WithDescription("Send a new email."),
		mcp.WithString("to", mcp.Required(), mcp.Description("Recipient email address.")),
		mcp.WithString("subject", mcp.Required(), mcp.Description("Email subject.")),
		mcp.WithString("body", mcp.Required(), mcp.Description("Email body (plain text).")),
	), wrap("send_email", func(req mcp.CallToolRequest) (string, error) {
		to := strArg(req, "to")
		subject := strArg(req, "subject")
		body := strArg(req, "body")
		if to == "" || subject == "" || body == "" {
			return "", fmt.Errorf("to, subject, and body are required")
		}
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		if err := g.SendMail(to, subject, body); err != nil {
			return "", err
		}
		return okJSON(map[string]any{"status": "sent"}), nil
	}))

	s.AddTool(mcp.NewTool("reply_to_email",
		mcp.WithDescription("Reply to an email."),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("The email message ID to reply to.")),
		mcp.WithString("body", mcp.Required(), mcp.Description("Reply body (plain text).")),
	), wrap("reply_to_email", func(req mcp.CallToolRequest) (string, error) {
		id := strArg(req, "message_id")
		body := strArg(req, "body")
		if id == "" || body == "" {
			return "", fmt.Errorf("message_id and body are required")
		}
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		if err := g.ReplyToMessage(id, body); err != nil {
			return "", err
		}
		return okJSON(map[string]any{"status": "sent"}), nil
	}))

	s.AddTool(mcp.NewTool("delete_email",
		mcp.WithDescription("Delete an email message."),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("The email message ID to delete.")),
	), wrap("delete_email", func(req mcp.CallToolRequest) (string, error) {
		id := strArg(req, "message_id")
		if id == "" {
			return "", fmt.Errorf("message_id is required")
		}
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		if err := g.DeleteMessage(id); err != nil {
			return "", err
		}
		return okJSON(map[string]any{"status": "deleted"}), nil
	}))

	s.AddTool(mcp.NewTool("mark_email_read",
		mcp.WithDescription("Mark an email as read."),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("The email message ID.")),
	), wrap("mark_email_read", func(req mcp.CallToolRequest) (string, error) {
		id := strArg(req, "message_id")
		if id == "" {
			return "", fmt.Errorf("message_id is required")
		}
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		if err := g.MarkMessageRead(id, true); err != nil {
			return "", err
		}
		return okJSON(map[string]any{"status": "read"}), nil
	}))

	s.AddTool(mcp.NewTool("mark_email_unread",
		mcp.WithDescription("Mark an email as unread."),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("The email message ID.")),
	), wrap("mark_email_unread", func(req mcp.CallToolRequest) (string, error) {
		id := strArg(req, "message_id")
		if id == "" {
			return "", fmt.Errorf("message_id is required")
		}
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		if err := g.MarkMessageRead(id, false); err != nil {
			return "", err
		}
		return okJSON(map[string]any{"status": "unread"}), nil
	}))
}
