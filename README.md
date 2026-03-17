# Engram

[![Status: Active](https://img.shields.io/badge/status-active-brightgreen)](https://github.com/OscillateLabsLLC/.github/blob/main/SUPPORT_STATUS.md)
[![Build](https://github.com/OscillateLabsLLC/engram/actions/workflows/build.yml/badge.svg)](https://github.com/OscillateLabsLLC/engram/actions/workflows/build.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Documentation](https://img.shields.io/badge/docs-GitHub%20Pages-blue)](https://oscillatelabsllc.github.io/engram/)

Event-sourced memory system for AI agents. No LLM in the write path — just reliable episode storage with semantic search.

**[📖 View Documentation](https://oscillatelabsllc.github.io/engram/)**

## Why Engram?

Most AI memory systems couple write reliability to LLM availability by performing entity extraction at write time. Engram takes a different approach: store episodes reliably first, search them semantically, and defer any expensive derived structures (knowledge graphs, entity extraction) to an optional second layer.

**The result:** writes never fail, search is fast, and you get a single portable binary with no runtime dependencies.

## Features

- **Three search modes** — find memories by meaning (vector), by exact words (keyword), or both at once (hybrid)
- **Graceful fallback** — keyword search works even when the embedding service is unavailable; hybrid degrades gracefully
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

Start the server:

```bash
engram serve
```

Engram starts on port 3490 and prints the SSE endpoint URL. All MCP clients connect to this single server -- no database locking conflicts. See [docs/mcp-integration.md](docs/mcp-integration.md) for instructions on running as a background service on macOS, Linux, and Windows.

## Configuration

Configure via environment variables:

| Variable            | Description                             | Default                  |
| ------------------- | --------------------------------------- | ------------------------ |
| `DUCKDB_PATH`       | Path to DuckDB database file            | `./engram.duckdb`        |
| `OLLAMA_URL`        | Ollama API endpoint                     | `http://localhost:11434` |
| `EMBEDDING_MODEL`   | Embedding model name                    | `nomic-embed-text`       |
| `ENGRAM_PORT`       | Server port                             | `3490`                   |
| `ENGRAM_SERVER_URL` | Server URL (used by stdio proxy)        | `http://localhost:3490`  |

See [`.env.example`](.env.example) for a template.

## MCP Client Integration

Engram integrates with Claude Desktop, Claude Code, and Cursor via the Model Context Protocol (MCP).

### Quick Setup

1. **Start the server** (see [background service docs](docs/mcp-integration.md#running-as-a-background-service) for persistent setup):
   ```bash
   engram serve
   ```

2. **Connect your client.** Most clients support SSE directly:

   **Cursor** (`.cursor/mcp.json`):
   ```json
   {
     "mcpServers": {
       "engram-memory": {
         "url": "http://localhost:3490/mcp/sse"
       }
     }
   }
   ```

   **Claude Desktop** (stdio proxy, for clients that require stdio):
   ```json
   {
     "mcpServers": {
       "engram-memory": {
         "command": "/absolute/path/to/engram",
         "args": ["stdio"],
         "env": {
           "ENGRAM_SERVER_URL": "http://localhost:3490"
         }
       }
     }
   }
   ```

For detailed integration instructions, available MCP tools, and troubleshooting, see [docs/mcp-integration.md](docs/mcp-integration.md).

## Docker & Deployment

### Quick Start (Development)

```bash
# macOS/Windows
just docker-up

# Linux
just docker-up-linux
```

For detailed deployment instructions including Docker Compose, Kubernetes, and production configurations, see [docs/deployment.md](docs/deployment.md).

## Architecture

```text
engram/
├── cmd/engram/          # Entry point (serve / stdio subcommands)
├── internal/
│   ├── api/             # HTTP + MCP SSE server
│   ├── db/              # DuckDB operations + VSS
│   ├── embedding/       # Ollama client
│   ├── mcp/             # MCP tool definitions
│   ├── models/          # Data models
│   └── proxy/           # stdio-to-SSE proxy
├── scripts/             # Build and test scripts
├── .github/workflows/   # CI/CD (build + release)
└── Dockerfile           # Container image
```

- **Server-first**: `engram serve` owns DuckDB exclusively, exposes MCP over SSE + REST API
- **Thin stdio proxy**: `engram stdio` bridges stdin/stdout to the server for clients that require stdio (e.g., Claude Desktop)
- **DuckDB** with VSS extension for vector similarity search (HNSW indexing)
- **Ollama** for local embedding generation (768-dimensional, `nomic-embed-text`)

For a deeper dive into the architecture, see [`docs/architecture.md`](docs/architecture.md).

## Design Principles

1. **Writes never fail** (if the database is up)
2. **No LLM in the write path** — embeddings only, and those are retryable
3. **Episode log is source of truth** — everything else is derived
4. **Simple over clever** — vector search covers 80% of use cases
5. **Portable** — single binary, single database file

## Documentation

- [MCP Integration Guide](docs/mcp-integration.md) - Client setup, available tools, troubleshooting
- [Deployment Guide](docs/deployment.md) - Docker Compose, Kubernetes, production deployment
- [Architecture](docs/architecture.md) - Technical deep dive into system design

## Testing

The project includes unit and integration tests:

```bash
# Run all tests
just test

# Run with coverage
just test-coverage
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, code style, and how to submit pull requests.

## License

MIT
