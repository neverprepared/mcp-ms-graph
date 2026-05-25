package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/server"
	"github.com/neverprepared/mcp-ms-graph/internal/cache"
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
