package tools

import (
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/neverprepared/mcp-ms-graph/internal/client"
)

func RegisterCalendarTools(s *server.MCPServer, c *client.GraphClient) {
	s.AddTool(mcp.NewTool("list_events",
		mcp.WithDescription("List upcoming calendar events."),
		mcp.WithNumber("days", mcp.Description("Number of days to look ahead (default 7).")),
	), wrap("list_events", func(req mcp.CallToolRequest) (string, error) {
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		days := intDefault(req, "days", defaultDays, maxDays)
		now := time.Now()
		end := now.Add(time.Duration(days) * 24 * time.Hour)
		events, err := g.GetCalendarView(now, end)
		if err != nil {
			return "", err
		}
		type eventSummary struct {
			ID       string `json:"id"`
			Subject  string `json:"subject"`
			Start    string `json:"start"`
			End      string `json:"end"`
			Location string `json:"location,omitempty"`
			JoinURL  string `json:"join_url,omitempty"`
			IsAllDay bool   `json:"is_all_day"`
			ShowAs   string `json:"show_as"`
		}
		var summaries []eventSummary
		for _, e := range events {
			loc := ""
			if e.Location != nil {
				loc = e.Location.DisplayName
			}
			summaries = append(summaries, eventSummary{
				ID:       e.ID,
				Subject:  e.Subject,
				Start:    e.StartTime().Format(time.RFC3339),
				End:      e.EndTime().Format(time.RFC3339),
				Location: loc,
				JoinURL:  e.JoinURL(),
				IsAllDay: e.IsAllDay,
				ShowAs:   e.ShowAs,
			})
		}
		return okJSON(summaries), nil
	}))

	s.AddTool(mcp.NewTool("create_event",
		mcp.WithDescription("Create a new calendar event."),
		mcp.WithString("subject", mcp.Required(), mcp.Description("Event title.")),
		mcp.WithString("start", mcp.Required(), mcp.Description("Start time in RFC3339 format.")),
		mcp.WithString("end", mcp.Required(), mcp.Description("End time in RFC3339 format.")),
		mcp.WithString("attendees", mcp.Description("Comma-separated email addresses of attendees.")),
		mcp.WithString("location", mcp.Description("Location (optional).")),
		mcp.WithString("body", mcp.Description("Event body/notes (optional).")),
		mcp.WithBoolean("online", mcp.Description("Create as online (Teams) meeting (default true).")),
	), wrap("create_event", func(req mcp.CallToolRequest) (string, error) {
		subject := strArg(req, "subject")
		startStr := strArg(req, "start")
		endStr := strArg(req, "end")
		if subject == "" || startStr == "" || endStr == "" {
			return "", fmt.Errorf("subject, start, and end are required")
		}
		start, err := time.Parse(time.RFC3339, startStr)
		if err != nil {
			return "", fmt.Errorf("invalid start: %w", err)
		}
		end, err := time.Parse(time.RFC3339, endStr)
		if err != nil {
			return "", fmt.Errorf("invalid end: %w", err)
		}
		var attendees []string
		if att := strArg(req, "attendees"); att != "" {
			for _, a := range strings.Split(att, ",") {
				if a = strings.TrimSpace(a); a != "" {
					attendees = append(attendees, a)
				}
			}
		}
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		event, err := g.CreateEvent(subject, start, end, attendees,
			boolDefault(req, "online", true), strArg(req, "location"), strArg(req, "body"))
		if err != nil {
			return "", err
		}
		return okJSON(map[string]any{
			"status":  "created",
			"id":      event.ID,
			"subject": event.Subject,
		}), nil
	}))

	respond := func(name, desc, status string, fn func(*client.GraphClient, string) error) {
		s.AddTool(mcp.NewTool(name,
			mcp.WithDescription(desc),
			mcp.WithString("event_id", mcp.Required(), mcp.Description("The event ID.")),
		), wrap(name, func(req mcp.CallToolRequest) (string, error) {
			id := strArg(req, "event_id")
			if id == "" {
				return "", fmt.Errorf("event_id is required")
			}
			if err := fn(c, id); err != nil {
				return "", err
			}
			return okJSON(map[string]any{"status": status}), nil
		}))
	}

	respond("accept_event", "Accept a calendar event invitation.", "accepted", func(c *client.GraphClient, id string) error {
		g, err := c.Graph()
		if err != nil {
			return err
		}
		return g.AcceptEvent(id)
	})
	respond("decline_event", "Decline a calendar event invitation.", "declined", func(c *client.GraphClient, id string) error {
		g, err := c.Graph()
		if err != nil {
			return err
		}
		return g.DeclineEvent(id)
	})
	respond("tentative_event", "Tentatively accept a calendar event invitation.", "tentative", func(c *client.GraphClient, id string) error {
		g, err := c.Graph()
		if err != nil {
			return err
		}
		return g.TentativelyAcceptEvent(id)
	})

	s.AddTool(mcp.NewTool("delete_event",
		mcp.WithDescription("Delete a calendar event."),
		mcp.WithString("event_id", mcp.Required(), mcp.Description("The event ID.")),
	), wrap("delete_event", func(req mcp.CallToolRequest) (string, error) {
		id := strArg(req, "event_id")
		if id == "" {
			return "", fmt.Errorf("event_id is required")
		}
		g, err := c.Graph()
		if err != nil {
			return "", err
		}
		if err := g.DeleteEvent(id); err != nil {
			return "", err
		}
		return okJSON(map[string]any{"status": "deleted"}), nil
	}))
}
