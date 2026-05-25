package tools

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/neverprepared/mcp-ms-graph/internal/client"
)

func RegisterProfileTools(s *server.MCPServer, c *client.GraphClient) {
	s.AddTool(mcp.NewTool("get_profile",
		mcp.WithDescription("Get the current user's profile information."),
	), wrap("get_profile", func(req mcp.CallToolRequest) (string, error) {
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		var me struct {
			DisplayName       string `json:"displayName"`
			Mail              string `json:"mail"`
			UserPrincipalName string `json:"userPrincipalName"`
			JobTitle          string `json:"jobTitle"`
			Department        string `json:"department"`
			OfficeLocation    string `json:"officeLocation"`
			ID                string `json:"id"`
		}
		if err := g.Get("/me", &me); err != nil {
			return "", err
		}
		return okJSON(me), nil
	}))

	s.AddTool(mcp.NewTool("get_presence",
		mcp.WithDescription("Get the current user's presence/availability status."),
	), wrap("get_presence", func(req mcp.CallToolRequest) (string, error) {
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		p, err := g.GetMyPresence()
		if err != nil {
			return "", err
		}
		return okJSON(p), nil
	}))
}
