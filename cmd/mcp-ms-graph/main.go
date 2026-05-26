package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/neverprepared/mcp-ms-graph/internal/cache"
	"github.com/neverprepared/mcp-ms-graph/internal/client"
	"github.com/neverprepared/mcp-ms-graph/internal/graph"
	"github.com/neverprepared/mcp-ms-graph/internal/secrets"
	mcpserver "github.com/neverprepared/mcp-ms-graph/internal/server"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var version = "dev"

func main() {
	root := &cobra.Command{
		Use:          "mcp-ms-graph",
		Short:        "MCP server for Microsoft Graph (Teams, Mail, Calendar)",
		Version:      version,
		RunE:         runServe,
		SilenceUsage: true,
	}

	root.AddCommand(&cobra.Command{
		Use:          "serve",
		Short:        "Start the MCP server on stdio (default when no subcommand given)",
		RunE:         runServe,
		SilenceUsage: true,
	})

	root.AddCommand(&cobra.Command{
		Use:   "setup",
		Short: "Interactive setup: store Ably key, channel, and passphrase in the OS keychain",
		RunE:  runSetup,
	})

	metricsCmd := &cobra.Command{
		Use:   "metrics [filter]",
		Short: "Print a JSON snapshot of key metrics (mail counts, upcoming meetings, presence)",
		Long: `Print a JSON snapshot of key metrics.

An optional filter argument selects a subset of the output using a dotted path
(jq-style, no jq required). The leading dot is optional. Examples:

  mcp-ms-graph metrics                         # full document
  mcp-ms-graph metrics .mail                   # just the mail object
  mcp-ms-graph metrics .mail.unread            # scalar (JSON number)
  mcp-ms-graph metrics .mail.unread -r         # raw: 96
  mcp-ms-graph metrics .mail.unread --type int # coerce to integer
  mcp-ms-graph metrics .presence.availability -r  # raw: Available

Missing paths return JSON null.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runMetrics,
	}
	metricsCmd.Flags().BoolP("raw", "r", false, "Output scalars without JSON encoding (no quotes on strings)")
	metricsCmd.Flags().String("type", "", "Coerce scalar output to type: int, float, string, bool")
	root.AddCommand(metricsCmd)

	calendarCmd := &cobra.Command{
		Use:   "calendar [filter]",
		Short: "List calendar events (default: today)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runCalendar,
	}
	calendarCmd.Flags().IntP("days", "d", 1, "Number of days to show (default 1 = today only)")
	calendarCmd.Flags().BoolP("raw", "r", false, "Output scalars without JSON encoding")
	calendarCmd.Flags().String("type", "", "Coerce scalar output to type: int, float, string, bool")
	root.AddCommand(calendarCmd)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func runServe(_ *cobra.Command, _ []string) error {
	s, err := mcpserver.New(version)
	if err != nil {
		return err
	}
	defer s.Stop()
	return server.ServeStdio(s.MCP)
}

// TimelineAction and TimelineEntry implement the phantom-ink output contract.
// Schema: https://github.com/neverprepared/phantom-ink/blob/main/contracts/timeline-entry.schema.json
type TimelineAction struct {
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	URL      string `json:"url,omitempty"`
	Value    string `json:"value,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	Template string `json:"template,omitempty"`
}

type TimelineEntry struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Title       string            `json:"title"`
	Description *string           `json:"description,omitempty"`
	Value       *string           `json:"value,omitempty"`
	URL         *string           `json:"url,omitempty"`
	StartAt     *int64            `json:"start_at,omitempty"`
	EndAt       *int64            `json:"end_at,omitempty"`
	Status      string            `json:"status,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]any    `json:"metadata,omitempty"`
	Actions     []TimelineAction  `json:"actions,omitempty"`
}

func strPtr(s string) *string { return &s }
func i64Ptr(i int64) *int64   { return &i }

func runMetrics(cmd *cobra.Command, args []string) error {
	log.SetOutput(io.Discard)
	c, err := client.New(&cache.TokenCache{})
	if err != nil {
		return err
	}
	g, err := c.Graph()
	if err != nil {
		return err
	}

	var (
		mu          sync.Mutex
		wg          sync.WaitGroup
		mailStats   *graph.InboxStats
		calRemain   int
		calInMeet   bool
		chatUnread  int
		presence    *graph.Presence
	)

	addErr := func(_ error) {} // swallow — omit failed metrics rather than poisoning output

	wg.Add(1)
	go func() {
		defer wg.Done()
		stats, err := g.GetInboxStats()
		if err != nil { addErr(err); return }
		mu.Lock(); mailStats = stats; mu.Unlock()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		now := time.Now()
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
		events, err := g.GetCalendarView(startOfDay, endOfDay)
		if err != nil { addErr(err); return }
		mu.Lock()
		for _, e := range events {
			end := e.EndTime()
			if end.After(now) {
				calRemain++
			}
			if !e.StartTime().After(now) && end.After(now) {
				calInMeet = true
			}
		}
		mu.Unlock()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		n, err := g.GetUnreadChatCount()
		if err != nil { addErr(err); return }
		mu.Lock(); chatUnread = n; mu.Unlock()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		p, err := g.GetMyPresence()
		if err != nil { addErr(err); return }
		mu.Lock(); presence = p; mu.Unlock()
	}()

	wg.Wait()

	var entries []TimelineEntry

	if mailStats != nil {
		entries = append(entries, TimelineEntry{
			ID:     "mail-unread",
			Kind:   "metric",
			Title:  "Unread Mail",
			Value:  strPtr(strconv.Itoa(mailStats.Unread)),
			Status: "active",
			Tags:   []string{"mail", "ms-graph", "outlook"},
			Metadata: map[string]any{"total": mailStats.Total},
		})
	}

	entries = append(entries, TimelineEntry{
		ID:     "chat-unread",
		Kind:   "metric",
		Title:  "Unread Chats",
		Value:  strPtr(strconv.Itoa(chatUnread)),
		Status: "active",
		Tags:   []string{"chat", "teams", "ms-graph"},
	})

	entries = append(entries, TimelineEntry{
		ID:     "calendar-remaining-today",
		Kind:   "metric",
		Title:  "Remaining Meetings Today",
		Value:  strPtr(strconv.Itoa(calRemain)),
		Status: "active",
		Tags:   []string{"calendar", "ms-graph"},
	})

	inMeetVal := "false"
	if calInMeet {
		inMeetVal = "true"
	}
	entries = append(entries, TimelineEntry{
		ID:     "calendar-in-meeting",
		Kind:   "metric",
		Title:  "In Meeting",
		Value:  strPtr(inMeetVal),
		Status: "active",
		Tags:   []string{"calendar", "ms-graph"},
	})

	if presence != nil {
		entries = append(entries, TimelineEntry{
			ID:     "presence",
			Kind:   "metric",
			Title:  "Presence",
			Value:  strPtr(presence.Availability),
			Status: "active",
			Tags:   []string{"presence", "teams", "ms-graph"},
			Metadata: map[string]any{"activity": presence.Activity},
		})
	}

	return outputEntries(cmd, args, entries)
}

// coerceType converts a scalar JSON value to the requested Go type.
func coerceType(v any, t string) any {
	switch t {
	case "int":
		switch n := v.(type) {
		case float64:
			return int64(n)
		case string:
			if i, err := strconv.ParseInt(n, 10, 64); err == nil {
				return i
			}
		case bool:
			if n {
				return int64(1)
			}
			return int64(0)
		}
	case "float":
		switch n := v.(type) {
		case float64:
			return n
		case string:
			if f, err := strconv.ParseFloat(n, 64); err == nil {
				return f
			}
		case bool:
			if n {
				return float64(1)
			}
			return float64(0)
		}
	case "string":
		switch n := v.(type) {
		case float64:
			return strconv.FormatFloat(n, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(n)
		case int64:
			return strconv.FormatInt(n, 10)
		}
	case "bool":
		switch n := v.(type) {
		case float64:
			return n != 0
		case string:
			b, err := strconv.ParseBool(n)
			if err == nil {
				return b
			}
		}
	}
	return v
}

// formatRaw renders a scalar without JSON encoding. Returns false for
// objects/arrays so the caller falls back to JSON output.
func formatRaw(v any) (string, bool) {
	switch n := v.(type) {
	case string:
		return n, true
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64), true
	case int64:
		return strconv.FormatInt(n, 10), true
	case bool:
		return strconv.FormatBool(n), true
	case nil:
		return "null", true
	}
	return "", false
}

// applyFilter walks a dotted path through a JSON value (object/array/scalar).
// Leading "." is optional. Numeric segments index into arrays. Missing keys
// or out-of-range indices return nil (rendered as JSON null).
func applyFilter(doc any, path string) any {
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return doc
	}
	cur := doc
	for _, seg := range strings.Split(path, ".") {
		if seg == "" {
			continue
		}
		switch v := cur.(type) {
		case map[string]any:
			cur = v[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(v) {
				return nil
			}
			cur = v[i]
		default:
			return nil
		}
	}
	return cur
}

func runCalendar(cmd *cobra.Command, args []string) error {
	log.SetOutput(io.Discard)
	days, _ := cmd.Flags().GetInt("days")
	if days < 1 {
		days = 1
	}

	c, err := client.New(&cache.TokenCache{})
	if err != nil {
		return err
	}
	g, err := c.Graph()
	if err != nil {
		return err
	}

	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	end := start.AddDate(0, 0, days).Add(-time.Second)

	events, err := g.GetCalendarView(start, end)
	if err != nil {
		return err
	}

	entries := make([]TimelineEntry, 0, len(events))
	for _, e := range events {
		startMs := e.StartTime().UnixMilli()
		endMs := e.EndTime().UnixMilli()

		status := "upcoming"
		switch {
		case e.IsCancelled:
			status = "failed"
		case e.EndTime().Before(now):
			status = "done"
		case !e.StartTime().After(now):
			status = "active"
		}

		tags := []string{"calendar", "ms-graph"}
		if e.IsOnlineMeeting {
			tags = append(tags, "online-meeting")
		}
		if e.Recurrence != nil {
			tags = append(tags, "recurring")
		}
		if e.IsCancelled {
			tags = append(tags, "cancelled")
		}
		if e.IsAllDay {
			tags = append(tags, "all-day")
		}

		meta := map[string]any{
			"show_as":     e.ShowAs,
			"sensitivity": e.Sensitivity,
			"importance":  e.Importance,
			"all_day":     e.IsAllDay,
			"recurring":   e.Recurrence != nil,
			"online_meeting_provider": e.OnlineMeetingProvider,
		}
		if e.SeriesMasterID != "" {
			meta["series_master_id"] = e.SeriesMasterID
		}
		if e.Location != nil && e.Location.DisplayName != "" {
			meta["location"] = e.Location.DisplayName
		}
		if e.Organizer != nil {
			name := e.Organizer.EmailAddress.Name
			if name == "" {
				name = e.Organizer.EmailAddress.Address
			}
			meta["organizer"] = name
		}
		if e.ResponseStatus != nil {
			meta["response"] = e.ResponseStatus.Response
		}
		var attendees []string
		for _, a := range e.Attendees {
			name := a.EmailAddress.Name
			if name == "" {
				name = a.EmailAddress.Address
			}
			attendees = append(attendees, name)
		}
		if len(attendees) > 0 {
			meta["attendees"] = attendees
			meta["attendee_count"] = len(attendees)
		}

		entry := TimelineEntry{
			ID:      e.ID,
			Kind:    "event",
			Title:   e.Subject,
			StartAt: i64Ptr(startMs),
			EndAt:   i64Ptr(endMs),
			Status:  status,
			Tags:    tags,
			Metadata: meta,
		}
		if e.BodyPreview != "" {
			entry.Description = strPtr(e.BodyPreview)
		}
		if joinURL := e.JoinURL(); joinURL != "" {
			entry.URL = strPtr(joinURL)
			entry.Actions = []TimelineAction{{Label: "Join", Kind: "open_url", URL: joinURL}}
		}

		entries = append(entries, entry)
	}

	return outputEntries(cmd, args, entries)
}

// outputEntries marshals entries through the filter/raw/type pipeline and writes to stdout.
func outputEntries(cmd *cobra.Command, args []string, entries []TimelineEntry) error {
	raw, _ := json.Marshal(entries)
	var doc any
	_ = json.Unmarshal(raw, &doc)

	if len(args) == 1 && args[0] != "" && args[0] != "." {
		doc = applyFilter(doc, args[0])
	}

	rawFlag, _ := cmd.Flags().GetBool("raw")
	typeName, _ := cmd.Flags().GetString("type")
	if typeName != "" {
		doc = coerceType(doc, typeName)
	}
	if rawFlag || typeName != "" {
		if s, ok := formatRaw(doc); ok {
			fmt.Fprint(os.Stdout, s)
			return nil
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func runSetup(_ *cobra.Command, _ []string) error {
	dir, err := cache.ConfigDir()
	if err != nil {
		return err
	}
	fmt.Printf("Config dir: %s\n\n", dir)

	cfg, err := cache.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	fmt.Println("== Ably ==")
	existingKey, _ := secrets.GetAblyKey()
	ablyKey, err := promptSecret("Ably API key (kept in OS keychain)", existingKey)
	if err != nil {
		return err
	}
	if ablyKey == "" {
		return fmt.Errorf("Ably API key is required")
	}
	if err := secrets.SetAblyKey(ablyKey); err != nil {
		return fmt.Errorf("save Ably key: %w", err)
	}

	existingChannel, _ := cfg["ably_channel"].(string)
	channel, err := prompt("Ably channel name", existingChannel)
	if err != nil {
		return err
	}
	if channel == "" {
		return fmt.Errorf("channel name is required")
	}
	cfg["ably_channel"] = channel
	if err := cache.SaveConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Println()
	fmt.Println("== Encryption passphrase ==")
	fmt.Println("This must match the key shown in the Chrome extension popup.")
	existingPass, _ := secrets.GetPassphrase()
	passphrase, err := promptSecret("Passphrase (kept in OS keychain)", existingPass)
	if err != nil {
		return err
	}
	if passphrase == "" {
		return fmt.Errorf("passphrase is required")
	}
	if err := secrets.SetPassphrase(passphrase); err != nil {
		return fmt.Errorf("save passphrase: %w", err)
	}

	cfgPath, _ := cache.ConfigPath()
	fmt.Println()
	fmt.Println("Done.")
	fmt.Printf("  config: %s\n", cfgPath)
	fmt.Printf("  keychain service: %s\n", secrets.Service)
	fmt.Println()
	fmt.Println("Restart any running MCP host (Claude Code) for changes to take effect.")
	return nil
}

func prompt(label, current string) (string, error) {
	if current != "" {
		fmt.Printf("%s [%s]: ", label, current)
	} else {
		fmt.Printf("%s: ", label)
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return "", scanner.Err()
	}
	v := strings.TrimSpace(scanner.Text())
	if v == "" {
		return current, nil
	}
	return v, nil
}

func promptSecret(label, current string) (string, error) {
	if current != "" {
		fmt.Printf("%s (press Enter to keep existing): ", label)
	} else {
		fmt.Printf("%s: ", label)
	}
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return current, nil
	}
	return v, nil
}
