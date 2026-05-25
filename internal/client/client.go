// Microsoft Graph client with two token modes:
//
//   - oauth: MS_GRAPH_ACCESS_TOKEN env var. Bare bearer, useful for testing.
//   - relay: token comes from the encrypted cache, fed by the Ably subscriber.
//     When the cached token is within 60s of expiry, RequestRefresh publishes
//     a "refresh_request" on the Ably channel; the browser extension picks it
//     up and re-publishes a fresh "token" event.
//
// Mode is auto-selected: env wins if set, else relay.
package client

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/neverprepared/mcp-ms-graph/internal/cache"
	"github.com/neverprepared/mcp-ms-graph/internal/graph"

	"golang.org/x/oauth2"
)

type RefreshRequester interface {
	RequestRefresh(context.Context) error
}

type GraphClient struct {
	mu         sync.RWMutex
	mode       string
	graph      *graph.Client
	tokenCache *cache.TokenCache
	refresher  RefreshRequester
}

func New(tokenCache *cache.TokenCache) (*GraphClient, error) {
	c := &GraphClient{tokenCache: tokenCache}

	if env := os.Getenv("MS_GRAPH_ACCESS_TOKEN"); env != "" {
		c.mode = "oauth"
		c.graph = graph.NewClient(oauth2.StaticTokenSource(&oauth2.Token{
			AccessToken: env,
			TokenType:   "Bearer",
		}))
		log.Printf("graph client initialized (mode=oauth)")
		return c, nil
	}

	if tokenCache == nil {
		return nil, fmt.Errorf("no MS_GRAPH_ACCESS_TOKEN env and no token cache; set env or run `mcp-ms-graph setup`")
	}
	c.mode = "relay"
	log.Printf("graph client initialized (mode=relay)")
	return c, nil
}

func (c *GraphClient) Mode() string { return c.mode }

// SetRefresher wires in the Ably subscriber so the client can request fresh
// tokens when the cached one is about to expire.
func (c *GraphClient) SetRefresher(r RefreshRequester) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refresher = r
}

// Graph returns the underlying Graph HTTP client, building it lazily in
// relay mode from whatever token is currently in the cache.
func (c *GraphClient) Graph() (*graph.Client, error) {
	if c.mode == "oauth" {
		c.mu.RLock()
		g := c.graph
		c.mu.RUnlock()
		return g, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	tok, err := c.tokenCache.Get()
	if err != nil {
		return nil, err
	}
	if tok == nil || tok.AccessToken == "" {
		if c.refresher != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = c.refresher.RequestRefresh(ctx)
			cancel()
		}
		return nil, fmt.Errorf("no Microsoft Graph token available yet; open Graph Explorer with the Chrome extension installed or wait for the Ably relay")
	}

	if tok.ExpiresAt > 0 && time.Until(time.Unix(tok.ExpiresAt, 0)) < 60*time.Second && c.refresher != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = c.refresher.RequestRefresh(ctx)
		cancel()
		if newTok, _ := c.tokenCache.Get(); newTok != nil && newTok.AccessToken != "" {
			tok = newTok
		}
	}

	c.graph = graph.NewClient(oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: tok.AccessToken,
		TokenType:   "Bearer",
	}))
	return c.graph, nil
}

// Invalidate forces the next Graph() call to rebuild from the cache.
// Called after the cache receives a new access token.
func (c *GraphClient) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mode == "relay" {
		c.graph = nil
	}
}
