# MCP Integration Guide

This guide covers integrating Engram with MCP clients like Claude Desktop, Claude Code, and Cursor.

## Transport Modes

Engram supports two transport modes:

1. **stdio** (default) — Local binary execution via command-line
2. **HTTP/SSE** — Remote server via streaming HTTP (requires deployed server)

## stdio Transport (Local)

Use this mode when running Engram locally on the same machine as your MCP client.

### Claude Desktop

Add to your `claude_desktop_config.json` (see [`claude_desktop_config.example.json`](../claude_desktop_config.example.json)):

```json
{
  "mcpServers": {
    "engram-memory": {
      "command": "/absolute/path/to/engram",
      "args": [],
      "env": {
        "DUCKDB_PATH": "/absolute/path/to/engram.duckdb",
        "OLLAMA_URL": "http://localhost:11434",
        "EMBEDDING_MODEL": "nomic-embed-text"
      }
    }
  }
}
```

> **Tip:** Use absolute paths. On macOS, run `realpath engram` to get the full path.

For Windows-specific setup, see [WINDOWS.md](../WINDOWS.md).

### Claude Code / Cursor

Same MCP server configuration — add it to your project or user settings.

## HTTP/SSE Transport (Remote)

Use this mode when connecting to a remote Engram server deployed via Docker/Kubernetes.

### Claude Desktop

```json
{
  "mcpServers": {
    "engram-memory": {
      "command": "npx",
      "args": [
        "-y",
        "supergateway",
        "--sse",
        "https://your-engram-server.example.com/mcp/sse"
      ]
    }
  }
}
```

### Cursor

Add to `.cursor/mcp.json` in your project or user settings:

```json
{
  "mcpServers": {
    "engram-memory": {
      "url": "https://your-engram-server.example.com/mcp/sse",
      "transport": "streamableHttp"
    }
  }
}
```

> **Note:** SSE transport requires deploying Engram in HTTP mode. See [deployment.md](deployment.md) for server setup.

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

| Parameter         | Required | Description                                        |
| ----------------- | :------: | -------------------------------------------------- |
| `query`           |          | Text to search for (embedded for semantic ranking) |
| `group_id`        |          | Filter by group                                    |
| `max_results`     |          | Limit results (default: 10)                        |
| `before`          |          | ISO 8601 timestamp upper bound                     |
| `after`           |          | ISO 8601 timestamp lower bound                     |
| `tags`            |          | Filter by tags (AND logic)                         |
| `source`          |          | Filter by source client                            |
| `include_expired` |          | Include expired episodes (default: false)          |

### `get_episodes`

Retrieve episodes by time range, source, or group.

### `update_episode`

Modify episode metadata, tags, or expiration.

### `get_status`

Health check — returns system status and version.

> **Safety:** Episodes can be marked as expired but cannot be permanently deleted via MCP tools. This prevents accidental memory loss from LLM errors.

## Troubleshooting

### "Command not found"

Use absolute paths in MCP config, not `./engram` or `~/engram`.

### "Cannot connect to Ollama"

- Verify Ollama is running: `ollama list`
- Check URL is `http://localhost:11434` (not `https`)

### "Database permission denied"

Make sure the directory for `DUCKDB_PATH` is writable.

### Search results seem chronological, not semantic

Check logs for `Generated query embedding with 768 dimensions`. If you see `Failed to generate query embedding`, Ollama may be down — Engram falls back to chronological ordering gracefully.
