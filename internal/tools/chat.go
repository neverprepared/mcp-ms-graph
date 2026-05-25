package tools

import (
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/neverprepared/mcp-ms-graph/internal/client"
	"github.com/neverprepared/mcp-ms-graph/internal/util"
)

func RegisterChatTools(s *server.MCPServer, c *client.GraphClient) {
	s.AddTool(mcp.NewTool("list_chats",
		mcp.WithDescription("List recent Teams chats (1:1, group, meeting)."),
		mcp.WithNumber("limit", mcp.Description("Max number of chats to return (default 20).")),
	), wrap("list_chats", func(req mcp.CallToolRequest) (string, error) {
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		chats, err := g.ListChats(intDefault(req, "limit", defaultLimit, maxLimit))
		if err != nil {
			return "", err
		}
		return okJSON(chats), nil
	}))

	s.AddTool(mcp.NewTool("get_chat_messages",
		mcp.WithDescription("Get messages from a specific Teams chat."),
		mcp.WithString("chat_id", mcp.Required(), mcp.Description("The chat ID.")),
		mcp.WithNumber("limit", mcp.Description("Max messages to return (default 20).")),
	), wrap("get_chat_messages", func(req mcp.CallToolRequest) (string, error) {
		chatID := strArg(req, "chat_id")
		if chatID == "" {
			return "", fmt.Errorf("chat_id is required")
		}
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		msgs, err := g.GetChatMessages(chatID, intDefault(req, "limit", defaultLimit, maxLimit))
		if err != nil {
			return "", err
		}
		type cleanMsg struct {
			Sender  string `json:"sender"`
			Time    string `json:"time"`
			Content string `json:"content"`
		}
		var clean []cleanMsg
		for _, m := range msgs {
			sender := "system"
			if m.From != nil && m.From.User != nil {
				sender = m.From.User.DisplayName
			}
			body := util.HTMLToText(m.Body.Content)
			if body == "" {
				continue
			}
			clean = append(clean, cleanMsg{
				Sender:  sender,
				Time:    m.CreatedDateTime.Format(time.RFC3339),
				Content: body,
			})
		}
		return okJSON(clean), nil
	}))

	s.AddTool(mcp.NewTool("send_chat_message",
		mcp.WithDescription("Send a message to a Teams chat."),
		mcp.WithString("chat_id", mcp.Required(), mcp.Description("The chat ID.")),
		mcp.WithString("message", mcp.Required(), mcp.Description("Message content (HTML allowed).")),
	), wrap("send_chat_message", func(req mcp.CallToolRequest) (string, error) {
		chatID := strArg(req, "chat_id")
		message := strArg(req, "message")
		if chatID == "" || message == "" {
			return "", fmt.Errorf("chat_id and message are required")
		}
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		msg, err := g.SendChatMessage(chatID, message)
		if err != nil {
			return "", err
		}
		return okJSON(map[string]any{"status": "sent", "id": msg.ID}), nil
	}))
}
