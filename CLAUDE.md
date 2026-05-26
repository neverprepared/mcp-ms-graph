# CLAUDE.md

Guidance for Claude Code sessions working in this repo.

## Commands

```bash
make build      # → ./mcp-ms-graph (darwin/arm64)
make vet
make test
make setup      # interactive: writes Ably key, channel, passphrase to OS keychain
./mcp-ms-graph  # default: serve MCP on stdio (relay mode reads token.enc)
```

Release: `git tag vX.Y.Z && git push --tags` triggers `.github/workflows/release.yaml` on `macos-14`. GoReleaser builds, releases, and bumps `neverprepared/homebrew-tap`'s `Formula/mcp-ms-graph.rb`. Requires `HOMEBREW_TAP_TOKEN` org secret (already configured).

## Architecture

Single-account MCP server exposing 21 Microsoft Graph tools (Teams chat, Outlook mail, Calendar, profile, presence) over stdio JSON-RPC via [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go).

### Token flow

The Microsoft tenant blocks standard OAuth via Conditional Access. Workaround:

```
Graph Explorer (browser) ──capture──► chrome-extensions/microsoft (service worker)
                                              │
                                              ├─ "token"          (encrypted access_token)
                                              ├─ "refresh_token"  (encrypted refresh_token)
                                              ▼
                                       Ably channel
                                              │
                              ┌───────────────┴───────────────┐
                              ▼                               ▼
                     internal/ably.Subscriber       publishes "refresh_request"
                       (history + live)              when token near expiry
                              │                               ▲
                              ▼                               │
                  cache.SaveAccessToken / .MergeRefreshToken  │
                              │                               │
                              ▼                               │
            $XDG_CONFIG_HOME/mcp-ms-graph/token.enc           │
              (encrypted JSON {access_token, refresh_token,   │
               expires_at, updated_at}, 0600)                 │
                              │                               │
                              ▼                               │
                       client.GraphClient ───────refresh_request publish via Subscriber
                              │
                              ▼
                       graph.Client (Graph v1.0 HTTP)
                              │
                              ▼
                       internal/tools/*.go (21 tools)
```

Crucial invariants:

- `internal/crypto/crypto.go` (Go) and `chrome-extensions/microsoft/crypto.js` are byte-compatible: PBKDF2-HMAC-SHA256, 100 000 iters, AES-256-GCM, wire format `base64(salt[32] || nonce[12] || ct+tag)`. **If you change one, change both.**
- The extension publishes **bare strings** as the data field of each Ably event — not JSON. `Subscriber.handle` dispatches on `msg.Name` (`token` vs `refresh_token`) and decrypts directly. The Slack equivalent uses JSON; do not assume parity.
- Ably API key and encryption passphrase live in the OS keychain (`internal/secrets`, service `mcp-ms-graph`), never on disk.
- `config.json` only holds the (non-secret) `ably_channel`.
- The `expires_at` field is parsed from the access token's JWT `exp` claim in `cache.parseJWTExp` — never trust an `expires_in` from elsewhere.

### Token modes (selected automatically)

| Mode  | Trigger                          | Behavior                                                  |
|-------|----------------------------------|-----------------------------------------------------------|
| oauth | `MS_GRAPH_ACCESS_TOKEN` env set  | Bare bearer, no refresh, no relay. Testing only.          |
| relay | (default)                        | Token via cache; refresh by publishing `refresh_request`. |

`client.GraphClient.Graph()` rebuilds the underlying `graph.Client` whenever `Invalidate()` is called (on every new `token` event). It is the single chokepoint — all tool handlers go through it. Do not cache `*graph.Client` in tool closures.

### Tool registration

Each tool module exports `RegisterXTools(s *server.MCPServer, c *client.GraphClient)`. `server.New` calls all five. Errors are surfaced as JSON via `tools.wrap`, never as MCP-level errors — keeps the LLM client experience consistent.

### Layers

```
cmd/mcp-ms-graph/main.go          # cobra: default serve, `setup` subcommand
internal/server/server.go         # New() wires cache → subscriber → client → tools
internal/client/client.go         # token modes, lazy build, refresh trigger
internal/cache/cache.go           # encrypted token.enc + plaintext config.json
internal/ably/subscriber.go       # history + live, two callbacks, refresh_request publisher
internal/crypto/crypto.go         # AES-256-GCM + PBKDF2 (byte-compatible with extension)
internal/secrets/secrets.go       # keyring wrapper, service "mcp-ms-graph"
internal/graph/                   # raw Graph HTTP client (lifted from msghub)
internal/tools/                   # 19 MCP tool definitions + handlers
internal/util/html.go             # HTMLToText for chat/email rendering
chrome-extensions/microsoft/      # MV3 service worker that captures + relays tokens
```

## phantom-ink output contract

The `metrics` and `calendar` subcommands emit `[]TimelineEntry` conforming to the phantom-ink contract:

**Schema:** https://github.com/neverprepared/phantom-ink/blob/main/contracts/timeline-entry.schema.json

Key rules:
- `id` must be stable across runs (graph event ID for events; fixed slug like `"mail-unread"` for metrics)
- `kind`: `"event"` or `"metric"`
- `start_at` / `end_at`: Unix epoch **milliseconds** (not seconds)
- `status`: `"upcoming"` / `"active"` / `"done"` / `"failed"` (cancelled → failed)
- `value`: always a **string** for metrics (stringify numbers)
- `metadata`: arbitrary map — put source-specific fields here, not at top level
- `actions`: `open_url` / `copy` / `dispatch` / `prompt`

## Conventions

- Tool arg numbers come through as `float64` over JSON — use `intDefault` not direct cast.
- All tool responses go through `okJSON(...)` / `errJSON(...)` so the wire shape is uniform.
- Skipped intentionally (parity with msghub's MCP surface): OneDrive `/me/drive/*` tools. The HTTP layer is there in `internal/graph` if you want to add them.
- When the cache has no token, `client.Graph()` issues a best-effort `RequestRefresh` and returns a clear error rather than blocking. The LLM retries on its own cadence.
