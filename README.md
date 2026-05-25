# mcp-ms-graph

MCP server for Microsoft Graph — Teams chat, Outlook mail, and Calendar.

Designed to work in environments where standard OAuth is blocked by Conditional
Access: an unmodified Microsoft Graph Explorer session is captured by a Chrome
extension and relayed to the server via [Ably](https://ably.com/) using
AES-256-GCM. The server stores the encrypted token at
`$XDG_CONFIG_HOME/mcp-ms-graph/token.enc` and refreshes it on demand by
publishing a `refresh_request` event back to the extension.

19 tools across 5 domains: Teams chat (3), People search (1), Outlook mail (7),
Calendar (6), Profile + Presence (2).

## Install

### Homebrew (macOS ARM64)

```bash
brew install neverprepared/tap/mcp-ms-graph
```

### Manual

Download the latest `mcp-ms-graph_darwin_arm64.tar.gz` from the [releases page](https://github.com/neverprepared/mcp-ms-graph/releases/latest), extract, and move the binary somewhere on your `$PATH`:

```bash
tar -xzf mcp-ms-graph_darwin_arm64.tar.gz
mv mcp-ms-graph /usr/local/bin/
```

## Setup

1. Download `chrome-extension.zip` from the [latest release](https://github.com/neverprepared/mcp-ms-graph/releases/latest), unzip it, then load the `chrome-extensions/microsoft/` folder as an unpacked extension in Chrome (`chrome://extensions` → **Load unpacked**).
2. Open Graph Explorer (`developer.microsoft.com/graph/graph-explorer`), sign in.
3. Click the extension icon → generate an encryption key (or paste an existing one).
4. Configure an Ably API key and channel name in the extension popup.
5. Run the setup wizard once to store credentials in the macOS Keychain:

   ```bash
   mcp-ms-graph setup
   ```

   Paste the same Ably API key, channel name, and encryption passphrase.

6. Add to your MCP host config (e.g. Claude Code):

   ```json
   {
     "mcpServers": {
       "ms-graph": {
         "command": "/opt/homebrew/bin/mcp-ms-graph"
       }
     }
   }
   ```

## Modes

| Mode  | Trigger                                | Notes                                                 |
|-------|----------------------------------------|-------------------------------------------------------|
| oauth | `MS_GRAPH_ACCESS_TOKEN` env set        | Bare bearer. No refresh, no relay. Testing only.      |
| relay | (default)                              | Reads token from `token.enc`, refreshes via Ably.     |

## Tools

| Domain     | Tools                                                                                       |
|------------|---------------------------------------------------------------------------------------------|
| Chat       | `list_chats`, `get_chat_messages`, `send_chat_message`                                      |
| People     | `search_people`                                                                             |
| Mail       | `list_emails`, `read_email`, `send_email`, `reply_to_email`, `delete_email`, `mark_email_read`, `mark_email_unread` |
| Calendar   | `list_events`, `create_event`, `accept_event`, `decline_event`, `tentative_event`, `delete_event` |
| Profile    | `get_profile`, `get_presence`                                                               |

## Build

```bash
make build      # → ./mcp-ms-graph
make vet
make test
```

## Releases

Tag-driven via GoReleaser. Push `vX.Y.Z`; the workflow on `macos-14` builds the
darwin/arm64 binary, attaches it to a GitHub release, and bumps
`neverprepared/homebrew-tap`'s `Formula/mcp-ms-graph.rb` automatically.

## License

MIT
