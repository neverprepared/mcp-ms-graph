package server

import (
	"log"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/server"
	mably "github.com/neverprepared/mcp-ms-graph/internal/ably"
	"github.com/neverprepared/mcp-ms-graph/internal/cache"
	"github.com/neverprepared/mcp-ms-graph/internal/client"
	"github.com/neverprepared/mcp-ms-graph/internal/tools"
)

type Server struct {
	MCP        *server.MCPServer
	Client     *client.GraphClient
	Subscriber *mably.Subscriber
}

func New(version string) (*Server, error) {
	mcp := server.NewMCPServer("mcp-ms-graph", version,
		server.WithToolCapabilities(true),
	)

	tc := &cache.TokenCache{}
	c, err := client.New(tc)
	if err != nil {
		return nil, err
	}

	tools.RegisterChatTools(mcp, c)
	tools.RegisterPeopleTools(mcp, c)
	tools.RegisterMailTools(mcp, c)
	tools.RegisterCalendarTools(mcp, c)
	tools.RegisterProfileTools(mcp, c)

	var sub *mably.Subscriber
	if c.Mode() == "relay" {
		cfg, err := cache.LoadConfig()
		if err != nil {
			log.Printf("warn: could not load config: %v", err)
			cfg = map[string]any{}
		}
		channel, _ := cfg["ably_channel"].(string)
		if channel == "" {
			channel = os.Getenv("ABLY_CHANNEL")
		}
		if channel == "" {
			log.Printf("warn: relay mode but no ably_channel configured — tokens will only come from the on-disk cache; run `mcp-ms-graph setup`")
		} else {
			sub = mably.New(channel,
				func(accessToken string) {
					if err := tc.SaveAccessToken(accessToken); err != nil {
						log.Printf("token cache save error: %v", err)
						return
					}
					c.Invalidate()
				},
				func(refreshToken string) {
					if err := tc.MergeRefreshToken(refreshToken); err != nil {
						log.Printf("refresh token save error: %v", err)
					}
				},
			)
			sub.Start()
			c.SetRefresher(sub)
			if !sub.WaitReady(5 * time.Second) {
				log.Printf("warn: ably subscriber did not become ready within 5s; proceeding with cached token if available")
			}
		}
	}

	log.Printf("mcp-ms-graph ready (mode=%s)", c.Mode())
	return &Server{MCP: mcp, Client: c, Subscriber: sub}, nil
}

func (s *Server) Stop() {
	if s.Subscriber != nil {
		s.Subscriber.Stop()
	}
}
