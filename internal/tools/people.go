package tools

import (
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/neverprepared/mcp-ms-graph/internal/client"
)

func RegisterPeopleTools(s *server.MCPServer, c *client.GraphClient) {
	s.AddTool(mcp.NewTool("search_people",
		mcp.WithDescription("Search for people in the organization by name or email prefix."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Name or email prefix to search for.")),
	), wrap("search_people", func(req mcp.CallToolRequest) (string, error) {
		query := strArg(req, "query")
		if query == "" {
			return "", fmt.Errorf("query is required")
		}
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		people, err := g.SearchPeople(query)
		if err != nil {
			return "", err
		}
		return okJSON(people), nil
	}))
}
