// Background Ably subscriber for the Microsoft Graph token relay.
//
// The chrome-extensions/microsoft extension publishes two events on the channel:
//
//	name="token"          data=base64(encrypted(access_token bare string))
//	name="refresh_token"  data=base64(encrypted(refresh_token bare string))
//
// It also listens for "refresh_request" events and runs its own token refresh
// against login.microsoftonline.com when it sees one, then re-publishes "token".
//
// History-on-start fetches the most recent of each event so a freshly started
// server has a token immediately if one has been published recently.
package ably

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/ably/ably-go/ably"
	mcrypto "github.com/neverprepared/mcp-ms-graph/internal/crypto"
	"github.com/neverprepared/mcp-ms-graph/internal/secrets"
)

type Callback func(plaintext string)

type Subscriber struct {
	channelName     string
	onAccessToken   Callback
	onRefreshToken  Callback
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	ready           chan struct{}
	readyOnce       sync.Once
	mu              sync.Mutex
	realtime        *ably.Realtime
}

func New(channelName string, onAccessToken, onRefreshToken Callback) *Subscriber {
	return &Subscriber{
		channelName:    channelName,
		onAccessToken:  onAccessToken,
		onRefreshToken: onRefreshToken,
		ready:          make(chan struct{}),
	}
}

func (s *Subscriber) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run(ctx)
	}()
}

func (s *Subscriber) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	s.mu.Lock()
	if s.realtime != nil {
		s.realtime.Close()
		s.realtime = nil
	}
	s.mu.Unlock()
}

// WaitReady blocks until the initial history fetch completes (or timeout).
func (s *Subscriber) WaitReady(timeout time.Duration) bool {
	select {
	case <-s.ready:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (s *Subscriber) markReady() {
	s.readyOnce.Do(func() { close(s.ready) })
}

func (s *Subscriber) run(ctx context.Context) {
	apiKey, err := secrets.GetAblyKey()
	if err != nil {
		log.Printf("ably: %v", err)
		s.markReady()
		return
	}
	passphrase, err := secrets.GetPassphrase()
	if err != nil {
		log.Printf("ably: %v", err)
		s.markReady()
		return
	}

	s.fetchHistory(ctx, apiKey, passphrase)
	s.markReady()

	if ctx.Err() != nil {
		return
	}
	s.subscribe(ctx, apiKey, passphrase)
}

func (s *Subscriber) fetchHistory(ctx context.Context, apiKey, passphrase string) {
	rest, err := ably.NewREST(ably.WithKey(apiKey))
	if err != nil {
		log.Printf("ably history: create REST client: %v", err)
		return
	}
	ch := rest.Channels.Get(s.channelName)
	pages, err := ch.History().Pages(ctx)
	if err != nil {
		log.Printf("ably history: %v", err)
		return
	}
	// Walk pages until we find one of each event (or run out).
	seenAccess, seenRefresh := false, false
	for pages.Next(ctx) {
		for _, item := range pages.Items() {
			switch item.Name {
			case "token":
				if !seenAccess {
					seenAccess = true
					s.handle(item.Name, item.Data, passphrase)
				}
			case "refresh_token":
				if !seenRefresh {
					seenRefresh = true
					s.handle(item.Name, item.Data, passphrase)
				}
			}
			if seenAccess && seenRefresh {
				return
			}
		}
	}
	if !seenAccess && !seenRefresh {
		log.Printf("ably history: no recent tokens on channel %q", s.channelName)
	}
}

func (s *Subscriber) subscribe(ctx context.Context, apiKey, passphrase string) {
	rt, err := ably.NewRealtime(ably.WithKey(apiKey))
	if err != nil {
		log.Printf("ably subscribe: create realtime client: %v", err)
		return
	}
	s.mu.Lock()
	s.realtime = rt
	s.mu.Unlock()

	ch := rt.Channels.Get(s.channelName)
	_, err = ch.SubscribeAll(ctx, func(msg *ably.Message) {
		s.handle(msg.Name, msg.Data, passphrase)
	})
	if err != nil {
		log.Printf("ably subscribe: %v", err)
		return
	}
	log.Printf("ably subscribed to %q", s.channelName)
	<-ctx.Done()
}

func (s *Subscriber) handle(name string, data any, passphrase string) {
	str, ok := data.(string)
	if !ok {
		return
	}
	switch name {
	case "token":
		plaintext, err := mcrypto.Decrypt(str, passphrase)
		if err != nil {
			log.Printf("ably token: decrypt failed: %v", err)
			return
		}
		if s.onAccessToken != nil {
			s.onAccessToken(plaintext)
		}
	case "refresh_token":
		plaintext, err := mcrypto.Decrypt(str, passphrase)
		if err != nil {
			log.Printf("ably refresh_token: decrypt failed: %v", err)
			return
		}
		if s.onRefreshToken != nil {
			s.onRefreshToken(plaintext)
		}
	}
}

// RequestRefresh publishes a refresh_request on the channel. The browser
// extension is alarmed every ~10s checking for these and will run its own
// OAuth refresh and re-publish a new "token" event when it sees one.
func (s *Subscriber) RequestRefresh(ctx context.Context) error {
	s.mu.Lock()
	rt := s.realtime
	s.mu.Unlock()
	if rt == nil {
		return nil // not connected yet
	}
	ch := rt.Channels.Get(s.channelName)
	return ch.Publish(ctx, "refresh_request", "1")
}
