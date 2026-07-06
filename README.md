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
- **Bring your own embeddings** — works with any OpenAI-compatible endpoint: LM Studio, Ollama, llama.cpp, or hosted providers
- **Single binary** — portable across Linux, macOS, and Windows
- **MCP native** — integrates directly with Claude Desktop, Claude Code, and Cursor

## Quick Start

### Prerequisites

- An OpenAI-compatible embeddings server with a 768-dimensional embedding model (e.g., `nomic-embed-text`). [LM Studio](https://lmstudio.ai) and [Ollama](https://ollama.ai) both work out of the box, locally or remotely.
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

### Get the embedding model

```bash
# LM Studio
lms get text-embedding-nomic-embed-text-v1.5

# Ollama
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

| Variable            | Description                                             | Default                  |
| ------------------- | ------------------------------------------------------- | ------------------------ |
| `DUCKDB_PATH`       | Path to DuckDB database file                            | `./engram.duckdb`        |
| `EMBEDDING_URL`     | OpenAI-compatible embeddings endpoint                   | `http://localhost:11434` |
| `EMBEDDING_MODEL`   | Embedding model name                                    | `nomic-embed-text`       |
| `EMBEDDING_API_KEY` | Bearer token for the embeddings endpoint (if required)  | _(none)_                 |
| `ENGRAM_PORT`       | Server port                                             | `3490`                   |
| `ENGRAM_SERVER_URL` | Server URL (used by stdio proxy)                        | `http://localhost:3490`  |
| `ENGRAM_LLM_ADAPTER` | Dreamer LLM adapter: `openai` or `claude-cli`          | `openai`                 |
| `ENGRAM_LLM_ENDPOINT` | OpenAI-compatible chat-completions endpoint (`openai` adapter) | `http://localhost:11434/v1` |
| `ENGRAM_LLM_MODEL`  | Chat model for knowledge extraction (`openai` adapter)  | `qwen3:8b`               |
| `ENGRAM_LLM_API_KEY` | Bearer token for the chat endpoint (if required)       | _(none)_                 |
| `ENGRAM_LLM_TIMEOUT` | Per-episode extraction timeout (Go duration)           | `60s`                    |
| `ENGRAM_CLAUDE_BIN` | Claude CLI binary (`claude-cli` adapter)                | `claude`                 |
| `ENGRAM_DREAM_INTERVAL` | Automatic dreaming interval (Go duration); unset disables it | _(disabled)_      |
| `ENGRAM_OWNER_ALIASES` | Comma-separated names for the memory owner (e.g. `Mike,Mike Gray`); grounds owner facts and enables built-in aliases (`I`, `me`, `my`, `user`, `the user`) | _(none)_ |
| `ENGRAM_DREAM_SKIP_TAGS` | Comma-separated tags; episodes carrying any of them are never dreamed (nor stamped) | _(none)_ |

`EMBEDDING_URL` accepts a bare host (`http://localhost:11434`), a `/v1` base (`http://localhost:1234/v1`), or a full `/v1/embeddings` endpoint — Engram normalizes it. `OLLAMA_URL` is still honored as a deprecated alias for `EMBEDDING_URL`.

Examples:

```bash
# LM Studio
EMBEDDING_URL=http://localhost:1234/v1 EMBEDDING_MODEL=text-embedding-nomic-embed-text-v1.5 engram serve

# Ollama (the default)
EMBEDDING_URL=http://localhost:11434 EMBEDDING_MODEL=nomic-embed-text engram serve
```

> **Note:** Engram's schema stores 768-dimensional vectors, so pick a 768-dim embedding model (the Nomic family fits).

See [`.env.example`](.env.example) for a template.

### Switching embedding models

Embeddings from different models live in different vector spaces — mixing them quietly degrades similarity scores. Engram records which model produced each stored vector, warns at startup when stored embeddings don't match the configured model, and can regenerate them in place:

```bash
# Refresh stale embeddings (missing, or produced by a different model)
curl -X POST http://localhost:3490/api/v1/admin/reembed

# Re-embed everything regardless of provenance
curl -X POST http://localhost:3490/api/v1/admin/reembed -d '{"force": true}'

# Check progress and staleness counts
curl http://localhost:3490/api/v1/admin/reembed
```

The job runs asynchronously inside the server, only touches derived data (episode content is never modified), and is safe to re-run — anything that fails is retried on the next pass. This is also how you backfill episodes written while the embedding server was down.

## Dreamer (knowledge extraction)

The dreamer reads stored episodes and extracts entities and relationships into the knowledge graph — subject-predicate-object triples like `Mike uses DuckDB`, plus links between episodes that mention the same entities. It runs entirely outside the write path: episodes are stored instantly, and enrichment happens later, in the background, one episode at a time. Extracted triples pass a deterministic validation pipeline (predicate whitelist, confidence bounds) before anything is written; a triple is also checked for *grounding* — whether at least one entity appears in the episode text, matches a configured owner alias, or resolves to an already-known entity. Triples grounded on neither side aren't rejected — they're quarantined: stored with `grounded: false` and excluded from `search_knowledge` results by default (pass `include_ungrounded` to see them). A `recurrence` count tracks corroboration: the same fact extracted again from a *different* episode increments it and raises confidence to the higher of the two observations; if that later extraction is grounded, it promotes the quarantined row to `grounded: true`. Set `ENGRAM_OWNER_ALIASES` so facts about you survive validation when episodes say "I" or "me", and `ENGRAM_DREAM_SKIP_TAGS` to keep tagged episodes (e.g. `private`) out of the dreamer entirely.

Dreaming is **disabled by default**. Trigger a pass manually:

```bash
# Enrich all episodes that haven't been processed yet
curl -X POST http://localhost:3490/api/v1/admin/dream

# Check progress and the enrichment backlog
curl http://localhost:3490/api/v1/admin/dream
```

Or set `ENGRAM_DREAM_INTERVAL` (e.g. `30m`) to run it automatically on a schedule. Each episode is processed once — failures are recorded in the episode's metadata and not retried, so a bad episode can't wedge the crawl.

Two LLM adapters are available:

- **`openai`** (default) — any OpenAI-compatible chat-completions endpoint (Ollama, LM Studio, llama.cpp, hosted providers). Point `ENGRAM_LLM_ENDPOINT` and `ENGRAM_LLM_MODEL` at a small local model; `qwen3:8b` on Ollama works well.
- **`claude-cli`** — shells out to the [Claude Code CLI](https://claude.com/claude-code) (`claude -p`) so you can reuse an existing Claude setup with no extra server.

> **Billing note for `claude-cli`:** the CLI's authentication determines what you pay. Logged in with a Claude subscription (OAuth), dream runs consume your plan usage. With `ANTHROPIC_API_KEY` set, every episode is a metered API call. Know which one you're configured for before enabling `ENGRAM_DREAM_INTERVAL`.

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

## Cleanup Patterns

Agents clean up stale memories via `update_episode` — engram intentionally does not expose a `delete_episode` MCP tool because permanent deletion is a deliberate human action, not something agents should do autonomously.

### Soft-delete (reversible)

Set `expired_at` to a past timestamp. The episode is hidden from default search but remains in the store — recover it later by clearing `expired_at`.

```json
{"tool": "update_episode", "id": "...", "expired_at": "2020-01-01T00:00:00Z"}
```

### Demote (visible but filtered)

Replace the episode's tags to include a marker like `deprecated` or `low-confidence`. The episode stays in search results so nothing is lost, but callers can filter at query time.

```json
{"tool": "update_episode", "id": "...", "tags": ["deprecated", "original-topic"]}
```

### Scheduled expiration

Set `expired_at` to a future timestamp — the episode disappears from default search after that time with no further action.

## Architecture

```text
engram/
├── cmd/engram/          # Entry point (serve / stdio subcommands)
├── internal/
│   ├── api/             # HTTP + MCP SSE server
│   ├── db/              # DuckDB operations + VSS
│   ├── embedding/       # OpenAI-compatible embeddings client
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
- **OpenAI-compatible embeddings** — LM Studio, Ollama, llama.cpp, or hosted providers (768-dimensional, e.g. `nomic-embed-text`)

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
