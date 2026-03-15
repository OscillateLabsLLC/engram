# MCP Integration Guide

This guide covers integrating Engram with MCP clients like Claude Desktop, Claude Code, and Cursor.

## Architecture

Engram uses a server-based architecture. `engram serve` is a persistent process that owns the database and handles all memory operations. MCP clients connect to the server in one of two ways:

1. **SSE (recommended)** — Clients that support SSE connect directly to the server
2. **stdio proxy** — Clients that only support stdio spawn `engram stdio`, a thin bridge that proxies to the running server

This architecture allows multiple MCP clients (Cursor, Claude Desktop, Claude Code) to share the same memory store simultaneously without database locking conflicts.

## Quick Start

### 1. Start the server

Engram needs to run as a background service so it's always available when your MCP clients connect. You only run it once -- all your MCP clients share the same server.

#### macOS (launchd)

Create `~/Library/LaunchAgents/com.engram.server.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.engram.server</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/engram</string>
        <string>serve</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>DUCKDB_PATH</key>
        <string>/Users/YOUR_USERNAME/Library/Application Support/Engram/memory.duckdb</string>
        <key>OLLAMA_URL</key>
        <string>http://localhost:11434</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardErrorPath</key>
    <string>/usr/local/var/log/engram.log</string>
</dict>
</plist>
```

Then load it:

```bash
mkdir -p ~/Library/Application\ Support/Engram
launchctl load ~/Library/LaunchAgents/com.engram.server.plist
```

Engram will now start automatically on login. To stop: `launchctl unload ~/Library/LaunchAgents/com.engram.server.plist`

#### Linux (systemd)

Create `~/.config/systemd/user/engram.service`:

```ini
[Unit]
Description=Engram Memory Server
After=network.target

[Service]
ExecStart=/usr/local/bin/engram serve
Environment=DUCKDB_PATH=%h/.local/share/engram/memory.duckdb
Environment=OLLAMA_URL=http://localhost:11434
Restart=on-failure

[Install]
WantedBy=default.target
```

Then enable it:

```bash
mkdir -p ~/.local/share/engram
systemctl --user daemon-reload
systemctl --user enable --now engram
```

Check status: `systemctl --user status engram`

#### Windows

See [WINDOWS.md](../WINDOWS.md) for detailed Windows setup including running as a startup task.

#### Manual (any platform)

For testing, just run in a terminal:

```bash
engram serve
```

The server starts on port 3490 by default and prints:

```
MCP SSE endpoint: http://localhost:3490/mcp/sse
Health check:     http://localhost:3490/health
```

### 2. Configure your MCP client

#### Cursor

Add to `.cursor/mcp.json` in your project root or `~/.cursor/mcp.json` for global access:

```json
{
  "mcpServers": {
    "engram": {
      "url": "http://localhost:3490/mcp/sse"
    }
  }
}
```

#### Claude Code

```bash
claude mcp add engram --transport sse http://localhost:3490/mcp/sse
```

#### Claude Desktop

Claude Desktop doesn't support SSE directly, so it uses `engram stdio` -- a thin proxy that bridges stdio to the running server:

```json
{
  "mcpServers": {
    "engram": {
      "command": "/absolute/path/to/engram",
      "args": ["stdio"]
    }
  }
}
```

> **Tip:** On macOS, run `which engram` to get the full path. The `ENGRAM_SERVER_URL` env var defaults to `http://localhost:3490` -- only set it if your server runs on a different host or port.

#### Other MCP clients

Any client that supports SSE can connect to `http://localhost:3490/mcp/sse`. Clients that only support stdio can spawn `engram stdio` as a proxy to the running server.

## Configuration

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `DUCKDB_PATH` | `./engram.duckdb` | Path to the DuckDB database file |
| `OLLAMA_URL` | `http://localhost:11434` | Ollama server endpoint |
| `EMBEDDING_MODEL` | `nomic-embed-text` | Embedding model name |
| `ENGRAM_PORT` | `3490` | Server port |
| `ENGRAM_SERVER_URL` | `http://localhost:3490` | Server URL (used by stdio proxy) |

### CLI Usage

```
engram [serve]              # Start HTTP/SSE server (default)
engram serve --port=3490    # Explicit serve with port override
engram stdio                # Stdio proxy to running server
```

## Verifying the Integration

Once configured, restart your MCP client and try:

1. **Store a memory:** "Remember that I prefer dark mode for all UIs"
2. **Retrieve it:** "What are my UI preferences?"

### Testing Semantic Search

To verify semantic search is working (not just returning recent results):

1. Store a few memories about different topics
2. Search for something semantically related to an _older_ memory
3. If the older, relevant memory ranks higher than recent unrelated ones, vector search is active

Check logs for `Generated query embedding with 768 dimensions`. If you see `Failed to generate query embedding`, Ollama may be down — Engram falls back to chronological ordering gracefully.

## Available MCP Tools

### `add_memory`

Store a new episode in memory.

| Parameter            | Required | Description                                           |
| -------------------- | :------: | ----------------------------------------------------- |
| `content`            |   Yes    | Episode content                                       |
| `source`             |   Yes    | Source client (e.g., `claude-desktop`, `cursor`)      |
| `name`               |          | Human-readable label                                  |
| `source_model`       |          | Model identifier (e.g., `claude-4.6-sonnet`)          |
| `source_description` |          | Freeform context about the episode                    |
| `group_id`           |          | Multi-tenant group (default: `default`)               |
| `tags`               |          | Array of tags for categorization                      |
| `valid_at`           |          | ISO 8601 timestamp — when the information became true |
| `metadata`           |          | JSON string with additional data                      |

### `search`

Search episodes using semantic similarity and filters.

| Parameter         | Required | Description                                                                                        |
| ----------------- | :------: | -------------------------------------------------------------------------------------------------- |
| `query`           |          | Text to search for (embedded for semantic ranking)                                                 |
| `group_id`        |          | Filter by group                                                                                    |
| `max_results`     |          | Limit results (default: 10)                                                                        |
| `before`          |          | ISO 8601 timestamp upper bound                                                                     |
| `after`           |          | ISO 8601 timestamp lower bound                                                                     |
| `tags`            |          | Filter by tags (AND logic)                                                                         |
| `source`          |          | Filter by source client                                                                            |
| `include_expired` |          | Include expired episodes (default: false)                                                          |
| `min_similarity`  |          | Minimum similarity score to include (0.0–1.0). Only applies in vector mode. |
| `search_mode`     |          | How to search: `vector` (by meaning, default), `keyword` (by exact words), or `hybrid` (both combined). The default will change to `hybrid` in the next major version. |
| `search_alpha`    |          | In hybrid mode, how much to favor meaning vs. exact words. Higher = more meaning-based, lower = more word-based (default: 0.7). For pure word search, use `search_mode=keyword` instead. |

**Which mode should I use?**
- **`vector`** (default) — Best when you want conceptually similar results. "What are my deployment preferences?" will find memories about CI/CD pipelines, hosting, etc. even if they don't contain the word "deployment."
- **`keyword`** — Best when you know the exact words. Useful when Ollama is down, or when you need precise term matching.
- **`hybrid`** — Best of both worlds. Finds results that are both semantically relevant and contain the right words. Will become the default in a future version.

Search results include a `similarity` score (0.0–1.0) in vector and hybrid modes. Keyword mode does not return similarity scores.

### `get_episodes`

Retrieve episodes by time range, source, or group.

### `update_episode`

Modify episode metadata, tags, or expiration.

### `get_status`

Health check — returns system status and version.

> **Safety:** Episodes can be marked as expired but cannot be permanently deleted via MCP tools. This prevents accidental memory loss from LLM errors.

## Migration from v1.x

If you're upgrading from v1.x:

1. **Start `engram serve`** as a persistent process (launchd, systemd, or Docker)
2. **Update MCP client configs** to use either the SSE URL directly or `"args": ["stdio"]`
3. **Remove `npx supergateway`** from any configs — it's no longer needed
4. Your existing DuckDB file is unchanged — point `DUCKDB_PATH` to the same file

## Troubleshooting

### "Cannot connect to engram server"

The stdio proxy prints this when it can't reach the server. Make sure `engram serve` is running:

```bash
curl http://localhost:3490/health
```

### "Command not found"

Use absolute paths in MCP config, not `./engram` or `~/engram`.

### "Cannot connect to Ollama"

- Verify Ollama is running: `ollama list`
- Check URL is `http://localhost:11434` (not `https`)

### "Database permission denied"

Make sure the directory for `DUCKDB_PATH` is writable.

### Search results seem chronological, not semantic

Check logs for `Generated query embedding with 768 dimensions`. If you see `Failed to generate query embedding`, Ollama may be down — Engram falls back to chronological ordering gracefully.
