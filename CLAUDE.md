# CLAUDE.md

Guidance for Claude Code sessions working in this repo.

## Overview

`mcp-ms-graph` is a single-account **MCP server** (Go) that exposes **22 Microsoft Graph tools**
(Teams chat, Outlook mail, Calendar, people search, profile, presence) over **stdio JSON-RPC**
using [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go). It also ships two CLI
subcommands (`metrics`, `calendar`) that emit the phantom-ink timeline-entry contract.

Only tools are exposed — no MCP resources and no MCP prompts are registered.

## Commands

```bash
make build       # go build -trimpath -ldflags "-s -w" -o mcp-ms-graph ./cmd/mcp-ms-graph (host arch)
make vet         # go vet ./...
make test        # go test ./...   (NOTE: there are currently no *_test.go files in the repo)
make tidy        # go mod tidy
make run         # build, then run the server
make setup       # build, then run the interactive keychain setup wizard
make clean       # rm -f mcp-ms-graph; rm -rf dist/
```

There is no lint target and no golangci-lint config; `go vet` is the lint gate (it is what CI runs).

Binary entry points (cobra, `cmd/mcp-ms-graph/main.go`):

```bash
./mcp-ms-graph                    # default: same as `serve`
./mcp-ms-graph serve              # MCP server on stdio
./mcp-ms-graph setup              # interactive: Ably key + channel + passphrase → OS keychain
./mcp-ms-graph metrics [filter]   # JSON snapshot: mail counts, upcoming meetings, presence
./mcp-ms-graph calendar [filter]  # calendar events (--days/-d, default 1 = today)
```

`metrics` and `calendar` accept an optional dotted-path filter (jq-style, leading `.` optional),
plus `--raw/-r` (unquoted scalars) and `--type int|float|string|bool`. Missing paths return `null`.

CI: `.github/workflows/ci.yaml` runs on `macos-14` for pushes to `main` and all PRs —
`go vet ./...`, `go build ./...`, `go test ./...`.

Release: `git tag vX.Y.Z && git push --tags` triggers `.github/workflows/release.yaml` on `macos-14`.
GoReleaser builds **darwin/arm64 only** (`CGO_ENABLED=1`), zips `chrome-extensions/` as a release
asset, and bumps `neverprepared/homebrew-tap`'s `Formula/mcp-ms-graph.rb`. Requires the
`HOMEBREW_TAP_TOKEN` org secret (already configured).

## Architecture

### Layers

```
cmd/mcp-ms-graph/main.go          # cobra: serve (default), setup, metrics, calendar
internal/server/server.go         # New() wires cache → subscriber → client → tools
internal/client/client.go         # token modes, lazy build, refresh trigger
internal/cache/cache.go           # encrypted token.enc + plaintext config.json
internal/ably/subscriber.go       # history + live, two callbacks, refresh_request publisher
internal/crypto/crypto.go         # AES-256-GCM + PBKDF2 (byte-compatible with the extension)
internal/secrets/secrets.go       # keyring wrapper, service "mcp-ms-graph"
internal/graph/                   # raw Graph v1.0 HTTP client (chat, mail, calendar, people, presence)
internal/tools/                   # 22 MCP tool definitions + handlers
internal/util/html.go             # HTMLToText for chat/email rendering
chrome-extensions/microsoft/      # MV3 service worker that captures + relays tokens
```

### MCP tools (22)

| Module | Tools |
|---|---|
| `tools/chat.go` (3) | `list_chats`, `get_chat_messages`, `send_chat_message` |
| `tools/people.go` (1) | `search_people` |
| `tools/mail.go` (10) | `list_emails`, `read_email`, `read_emails_batch`, `send_email`, `reply_to_email`, `delete_email`, `mark_email_read`, `mark_email_unread`, `list_mail_folders`, `move_email` |
| `tools/calendar.go` (6) | `list_events`, `create_event`, `accept_event`, `decline_event`, `tentative_event`, `delete_event` |
| `tools/profile.go` (2) | `get_profile`, `get_presence` |

Skipped intentionally (parity with msghub's MCP surface): OneDrive `/me/drive/*` tools.

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
                       internal/tools/*.go (22 tools)
```

Crucial invariants:

- `internal/crypto/crypto.go` (Go) and `chrome-extensions/microsoft/crypto.js` are byte-compatible:
  PBKDF2-HMAC-SHA256, 100 000 iters, AES-256-GCM, wire format
  `base64(salt[32] || nonce[12] || ct+tag)`. **If you change one, change both.**
- The extension publishes **bare strings** as the data field of each Ably event — not JSON.
  `Subscriber.handle` dispatches on `msg.Name` (`token` vs `refresh_token`) and decrypts directly.
  The Slack equivalent uses JSON; do not assume parity.
- Ably API key and encryption passphrase live in the OS keychain (`internal/secrets`, service
  `mcp-ms-graph`), never on disk.
- `config.json` (under `$XDG_CONFIG_HOME/mcp-ms-graph`, default `~/.config/mcp-ms-graph`) only
  holds the non-secret `ably_channel`. `ABLY_CHANNEL` is the env fallback.
- `expires_at` is parsed from the access token's JWT `exp` claim in `cache.parseJWTExp` — never
  trust an `expires_in` from elsewhere.

### Token modes (selected automatically in `client.New`)

| Mode  | Trigger                          | Behavior                                                  |
|-------|----------------------------------|-----------------------------------------------------------|
| oauth | `MS_GRAPH_ACCESS_TOKEN` env set  | Bare bearer, no refresh, no relay. Testing only.          |
| relay | (default)                        | Token via cache; refresh by publishing `refresh_request`. |

`client.GraphClient.Graph()` rebuilds the underlying `graph.Client` whenever `Invalidate()` is
called (on every new `token` event). It is the single chokepoint — all tool handlers go through it.
Do not cache `*graph.Client` in tool closures.

In relay mode `server.New` only starts the Ably subscriber when a channel is configured; without
one it logs a warning and runs off the on-disk cache alone.

## phantom-ink output contract

The `metrics` and `calendar` subcommands emit `[]TimelineEntry` conforming to the phantom-ink
contract (types defined in `cmd/mcp-ms-graph/main.go`).

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

- Tool arg numbers come through as `float64` over JSON — use `intDefault`, not a direct cast.
- All tool responses go through `okJSON(...)` / `errJSON(...)` so the wire shape is uniform.
- Errors are surfaced as JSON via `tools.wrap`, never as MCP-level errors — keeps the LLM client
  experience consistent.
- Each tool module exports `RegisterXTools(s *server.MCPServer, c *client.GraphClient)`;
  `server.New` calls all five.
- When the cache has no token, `client.Graph()` issues a best-effort `RequestRefresh` and returns a
  clear error rather than blocking. The LLM retries on its own cadence.
- Target is macOS/Apple Silicon: CI, release, and the OS-keychain dependency all assume darwin.
