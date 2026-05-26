package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
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

	root.AddCommand(&cobra.Command{
		Use:   "metrics",
		Short: "Print a JSON snapshot of key metrics (mail counts, upcoming meetings, presence)",
		RunE:  runMetrics,
	})

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

type metricsOutput struct {
	Mail     *graph.InboxStats `json:"mail"`
	Calendar struct {
		CurrentlyInMeeting bool        `json:"currently_in_meeting"`
		RemainingToday     int         `json:"remaining_today"`
		Next               *nextEvent  `json:"next,omitempty"`
	} `json:"calendar"`
	Chat struct {
		Unread int `json:"unread"`
	} `json:"chat"`
	Presence    *graph.Presence `json:"presence"`
	CollectedAt string          `json:"collected_at"`
	Errors      []string        `json:"errors,omitempty"`
}

func runMetrics(_ *cobra.Command, _ []string) error {
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

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
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
