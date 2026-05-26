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

type nextEvent struct {
	Subject  string `json:"subject"`
	Start    string `json:"start"`
	End      string `json:"end"`
	JoinURL  string `json:"join_url,omitempty"`
	Location string `json:"location,omitempty"`
}

type accountInfo struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
}

type metricsOutput struct {
	Account  *accountInfo      `json:"account"`
	Mail     *graph.InboxStats `json:"mail"`
	Calendar struct {
		CurrentlyInMeeting bool       `json:"currently_in_meeting"`
		RemainingToday     int        `json:"remaining_today"`
		Next               *nextEvent `json:"next,omitempty"`
	} `json:"calendar"`
	Chat struct {
		Unread int `json:"unread"`
	} `json:"chat"`
	Presence    *graph.Presence `json:"presence"`
	CollectedAt string          `json:"collected_at"`
	Errors      []string        `json:"errors,omitempty"`
}

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
		mu  sync.Mutex
		wg  sync.WaitGroup
		out metricsOutput
	)
	out.CollectedAt = time.Now().UTC().Format(time.RFC3339)

	addErr := func(e error) {
		mu.Lock()
		out.Errors = append(out.Errors, e.Error())
		mu.Unlock()
	}

	// account (/me)
	wg.Add(1)
	go func() {
		defer wg.Done()
		var me struct {
			Mail              string `json:"mail"`
			UserPrincipalName string `json:"userPrincipalName"`
			DisplayName       string `json:"displayName"`
		}
		if err := g.Get("/me?$select=mail,userPrincipalName,displayName", &me); err != nil {
			addErr(fmt.Errorf("account: %w", err))
			return
		}
		email := me.Mail
		if email == "" {
			email = me.UserPrincipalName
		}
		mu.Lock()
		out.Account = &accountInfo{Email: email, DisplayName: me.DisplayName}
		mu.Unlock()
	}()

	// inbox stats
	wg.Add(1)
	go func() {
		defer wg.Done()
		stats, err := g.GetInboxStats()
		if err != nil {
			addErr(fmt.Errorf("mail: %w", err))
			return
		}
		mu.Lock()
		out.Mail = stats
		mu.Unlock()
	}()

	// calendar: all events today so we can detect in-progress meetings
	wg.Add(1)
	go func() {
		defer wg.Done()
		now := time.Now()
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
		events, err := g.GetCalendarView(startOfDay, endOfDay)
		if err != nil {
			addErr(fmt.Errorf("calendar: %w", err))
			return
		}
		mu.Lock()
		var remaining []graph.Event
		for _, e := range events {
			start := e.StartTime()
			end := e.EndTime()
			if end.After(now) {
				remaining = append(remaining, e)
			}
			if !start.After(now) && end.After(now) {
				out.Calendar.CurrentlyInMeeting = true
			}
		}
		out.Calendar.RemainingToday = len(remaining)
		if len(remaining) > 0 {
			e := remaining[0]
			ne := &nextEvent{
				Subject: e.Subject,
				Start:   e.StartTime().UTC().Format(time.RFC3339),
				End:     e.EndTime().UTC().Format(time.RFC3339),
				JoinURL: e.JoinURL(),
			}
			if e.Location != nil {
				ne.Location = e.Location.DisplayName
			}
			out.Calendar.Next = ne
		}
		mu.Unlock()
	}()

	// unread chat count
	wg.Add(1)
	go func() {
		defer wg.Done()
		count, err := g.GetUnreadChatCount()
		if err != nil {
			addErr(fmt.Errorf("chat: %w", err))
			return
		}
		mu.Lock()
		out.Chat.Unread = count
		mu.Unlock()
	}()

	// presence
	wg.Add(1)
	go func() {
		defer wg.Done()
		p, err := g.GetMyPresence()
		if err != nil {
			addErr(fmt.Errorf("presence: %w", err))
			return
		}
		mu.Lock()
		out.Presence = p
		mu.Unlock()
	}()

	wg.Wait()

	// Round-trip through map[string]any so the filter can walk a uniform shape.
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}

	if len(args) == 1 && args[0] != "" && args[0] != "." {
		doc = applyFilter(doc, args[0])
	}

	raw2, _ := cmd.Flags().GetBool("raw")
	typeName, _ := cmd.Flags().GetString("type")
	if typeName != "" {
		doc = coerceType(doc, typeName)
	}
	if raw2 || typeName != "" {
		if s, ok := formatRaw(doc); ok {
			fmt.Fprint(os.Stdout, s)
			return nil
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
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

	type eventOut struct {
		ID              string   `json:"id"`
		Subject         string   `json:"subject"`
		Start           string   `json:"start"`
		End             string   `json:"end"`
		AllDay          bool     `json:"all_day,omitempty"`
		Location        string   `json:"location,omitempty"`
		JoinURL         string   `json:"join_url,omitempty"`
		Organizer       string   `json:"organizer,omitempty"`
		Response        string   `json:"response,omitempty"`
		ShowAs          string   `json:"show_as,omitempty"`
		Attendees       []string `json:"attendees,omitempty"`
		IsOnlineMeeting bool     `json:"is_online_meeting,omitempty"`
	}

	out := make([]eventOut, 0, len(events))
	for _, e := range events {
		ev := eventOut{
			ID:              e.ID,
			Subject:         e.Subject,
			Start:           e.StartTime().Local().Format(time.RFC3339),
			End:             e.EndTime().Local().Format(time.RFC3339),
			AllDay:          e.IsAllDay,
			JoinURL:         e.JoinURL(),
			ShowAs:          e.ShowAs,
			IsOnlineMeeting: e.IsOnlineMeeting,
		}
		if e.Location != nil && e.Location.DisplayName != "" {
			ev.Location = e.Location.DisplayName
		}
		if e.Organizer != nil {
			ev.Organizer = e.Organizer.EmailAddress.Name
			if ev.Organizer == "" {
				ev.Organizer = e.Organizer.EmailAddress.Address
			}
		}
		if e.ResponseStatus != nil {
			ev.Response = e.ResponseStatus.Response
		}
		for _, a := range e.Attendees {
			name := a.EmailAddress.Name
			if name == "" {
				name = a.EmailAddress.Address
			}
			ev.Attendees = append(ev.Attendees, name)
		}
		out = append(out, ev)
	}

	raw, _ := json.Marshal(out)
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
