// Encrypted on-disk token cache and plaintext config store.
//
// Layout under $XDG_CONFIG_HOME/mcp-ms-graph (default ~/.config/mcp-ms-graph):
//
//	token.enc   — encrypted JSON {access_token, refresh_token, expires_at, updated_at}
//	config.json — plaintext non-secret config (ably_channel, …)
//
// The browser extension publishes "token" and "refresh_token" events as separate
// bare strings; the subscriber merges them into this single record via
// SaveAccessToken and MergeRefreshToken.
package cache

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/neverprepared/mcp-ms-graph/internal/crypto"
	"github.com/neverprepared/mcp-ms-graph/internal/secrets"
)

const (
	dirname    = "mcp-ms-graph"
	tokenFile  = "token.enc"
	configFile = "config.json"
)

type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"` // unix seconds
	UpdatedAt    int64  `json:"updated_at"`
}

func ConfigDir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, dirname)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return dir, nil
}

func tokenPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, tokenFile), nil
}

func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFile), nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// TokenCache is a thread-safe encrypted token store.
type TokenCache struct {
	mu    sync.RWMutex
	token *Token
}

func (c *TokenCache) Get() (*Token, error) {
	c.mu.RLock()
	if c.token != nil {
		t := *c.token
		c.mu.RUnlock()
		return &t, nil
	}
	c.mu.RUnlock()
	return c.Load()
}

func (c *TokenCache) Load() (*Token, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	path, err := tokenPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		c.token = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pass, err := secrets.GetPassphrase()
	if err != nil {
		return nil, err
	}
	plaintext, err := crypto.Decrypt(string(data), pass)
	if err != nil {
		return nil, fmt.Errorf("token cache decrypt failed: %w", err)
	}
	var tok Token
	if err := json.Unmarshal([]byte(plaintext), &tok); err != nil {
		return nil, err
	}
	c.token = &tok
	t := tok
	return &t, nil
}

func (c *TokenCache) save(tok *Token) error {
	tok.UpdatedAt = time.Now().Unix()
	pass, err := secrets.GetPassphrase()
	if err != nil {
		return err
	}
	plaintext, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	encrypted, err := crypto.Encrypt(string(plaintext), pass)
	if err != nil {
		return err
	}
	path, err := tokenPath()
	if err != nil {
		return err
	}
	if err := atomicWrite(path, []byte(encrypted), 0600); err != nil {
		return err
	}
	c.token = tok
	return nil
}

// SaveAccessToken replaces the access token (and recomputes expiry from the JWT).
// Refresh token is preserved if already cached.
func (c *TokenCache) SaveAccessToken(accessToken string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tok := &Token{}
	if c.token != nil {
		*tok = *c.token
	}
	tok.AccessToken = accessToken
	tok.ExpiresAt = parseJWTExp(accessToken)
	if err := c.save(tok); err != nil {
		return err
	}
	log.Printf("token cache: access token updated (expires in %ds)", tok.ExpiresAt-time.Now().Unix())
	return nil
}

// MergeRefreshToken updates only the refresh token; access token is preserved.
func (c *TokenCache) MergeRefreshToken(refreshToken string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tok := &Token{}
	if c.token != nil {
		*tok = *c.token
	}
	tok.RefreshToken = refreshToken
	if err := c.save(tok); err != nil {
		return err
	}
	log.Printf("token cache: refresh token updated")
	return nil
}

func (c *TokenCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	path, _ := tokenPath()
	os.Remove(path)
	c.token = nil
}

// parseJWTExp extracts the "exp" claim (unix seconds) from a JWT, or 0 on failure.
func parseJWTExp(jwt string) int64 {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return 0
	}
	// JWTs use URL-safe base64 without padding.
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0
	}
	return claims.Exp
}

func LoadConfig() (map[string]any, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func SaveConfig(cfg map[string]any) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data, 0600)
}
