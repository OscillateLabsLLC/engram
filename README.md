# Engram

[![Build](https://github.com/OscillateLabsLLC/engram/actions/workflows/build.yml/badge.svg)](https://github.com/OscillateLabsLLC/engram/actions/workflows/build.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev)

Event-sourced memory system for AI agents. No LLM in the write path — just reliable episode storage with semantic search.

## Why Engram?

Most AI memory systems couple write reliability to LLM availability by performing entity extraction at write time. Engram takes a different approach: store episodes reliably first, search them semantically, and defer any expensive derived structures (knowledge graphs, entity extraction) to an optional second layer.

**The result:** writes never fail, search is fast, and you get a single portable binary with no runtime dependencies.

## Features

- **Semantic search** — results ranked by relevance using vector similarity, not recency
- **Graceful fallback** — works even when the embedding service is unavailable
- **Fast queries** — DuckDB HNSW indexing for sub-100ms vector search
- **Zero external APIs** — all embeddings generated locally via Ollama
- **Single binary** — portable across Linux, macOS, and Windows
- **MCP native** — integrates directly with Claude Desktop, Claude Code, and Cursor

## Quick Start

### Prerequisites

- [Ollama](https://ollama.ai) running locally (or remotely) with an embedding model
- Go 1.25+ (only if building from source)

### Install

### Option A: Download a pre-built binary

Download from the [releases page](https://github.com/OscillateLabsLLC/engram/releases) for your platform:

| Platform              | Binary                     |
| --------------------- | -------------------------- |
| macOS (Apple Silicon) | `engram-darwin-arm64`      |
| macOS (Intel)         | `engram-darwin-amd64`      |
| Linux (x86_64)        | `engram-linux-amd64`       |
| Linux (ARM64)         | `engram-linux-arm64`       |
| Windows               | `engram-windows-amd64.exe` |

```bash
# macOS/Linux: make it executable
chmod +x engram-*
mv engram-* engram
```

### Option B: Build from source

```bash
git clone https://github.com/OscillateLabsLLC/engram
cd engram

# Using just (recommended — install from https://github.com/casey/just)
just setup    # install deps, pull embedding model, build

# Or manually
go build -o engram ./cmd/engram/main.go
```

### Pull the embedding model

```bash
ollama pull nomic-embed-text
```

### Run

```bash
export DUCKDB_PATH="./engram.duckdb"
export OLLAMA_URL="http://localhost:11434"
export EMBEDDING_MODEL="nomic-embed-text"

./engram
```

## Configuration

Configure via environment variables:

| Variable          | Description                  | Default                  |
| ----------------- | ---------------------------- | ------------------------ |
| `DUCKDB_PATH`     | Path to DuckDB database file | `./engram.duckdb`        |
| `OLLAMA_URL`      | Ollama API endpoint          | `http://localhost:11434` |
| `EMBEDDING_MODEL` | Embedding model name         | `nomic-embed-text`       |

See [`.env.example`](.env.example) for a template.

## MCP Client Integration

### Claude Desktop

Add to your `claude_desktop_config.json` (see [`claude_desktop_config.example.json`](claude_desktop_config.example.json)):

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

For Windows-specific setup, see [WINDOWS.md](WINDOWS.md).

### Claude Code / Cursor

Same MCP server configuration — add it to your project or user settings.

### Verify It Works

Once configured, restart your MCP client and try:

1. **Store a memory:** "Remember that I prefer dark mode for all UIs"
2. **Retrieve it:** "What are my UI preferences?"

To verify semantic search is working (not just returning recent results):

1. Store a few memories about different topics
2. Search for something semantically related to an _older_ memory
3. If the older, relevant memory ranks higher than recent unrelated ones, VSS is active

## MCP Tools

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

## Docker

```bash
docker build -t engram .
docker run -e OLLAMA_URL=http://host.docker.internal:11434 \
           -v $(pwd)/data:/data \
           -e DUCKDB_PATH=/data/engram.duckdb \
           engram
```

## Architecture

```text
engram/
├── cmd/engram/          # Entry point
├── internal/
│   ├── db/              # DuckDB operations + VSS
│   ├── embedding/       # Ollama client
│   ├── mcp/             # MCP server implementation
│   └── models/          # Data models
├── scripts/             # Build and test scripts
├── .github/workflows/   # CI/CD (build + release)
└── Dockerfile           # Container image
```

- **Go** service using the official MCP SDK ([`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go))
- **DuckDB** with VSS extension for vector similarity search (HNSW indexing)
- **Ollama** for local embedding generation (768-dimensional, `nomic-embed-text`)
- **stdio** transport for MCP client integration

For a deeper dive into the architecture, see [`docs/architecture.md`](docs/architecture.md).

## Design Principles

1. **Writes never fail** (if the database is up)
2. **No LLM in the write path** — embeddings only, and those are retryable
3. **Episode log is source of truth** — everything else is derived
4. **Simple over clever** — vector search covers 80% of use cases
5. **Portable** — single binary, single database file

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

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, code style, and how to submit pull requests.

## License

MIT
