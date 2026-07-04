# Architecture

## Overview

Engram is an event-sourced memory system for AI agents. It uses a two-layer design where the **episode log is the source of truth** and any derived structures (graphs, indices) are materialized views that can be rebuilt from scratch.

## Problem Statement

Existing AI memory systems (Graphiti, Memento, etc.) couple write reliability to LLM API availability by performing entity extraction at write time. This creates fragile systems where:

- Writes fail silently when LLM endpoints are unavailable
- Multiple model variants produce inconsistent entity resolution
- The derived knowledge graph becomes the source of truth instead of raw data
- Infrastructure changes break the write path

Engram solves this by keeping the episode store as the foundation — no LLM in the write path. Every write succeeds if the database is up.

## Layer 1: Episode Store (Core)

### Data Model

```sql
INSTALL vss;
LOAD vss;

CREATE TABLE episodes (
    id VARCHAR PRIMARY KEY,
    content TEXT NOT NULL,
    name VARCHAR,
    source VARCHAR NOT NULL,
    source_model VARCHAR,
    source_description TEXT,
    group_id VARCHAR DEFAULT 'default',
    tags VARCHAR[],
    embedding FLOAT[768],
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    valid_at TIMESTAMP,
    expired_at TIMESTAMP,
    metadata JSON
);

-- Vector similarity index (HNSW)
CREATE INDEX idx_episodes_embedding ON episodes USING HNSW (embedding);

-- Standard indices for common query patterns
CREATE INDEX idx_episodes_created_at ON episodes (created_at DESC);
CREATE INDEX idx_episodes_group_id ON episodes (group_id);
CREATE INDEX idx_episodes_valid_at ON episodes (valid_at);
CREATE INDEX idx_episodes_expired_at ON episodes (expired_at);
CREATE INDEX idx_episodes_source ON episodes (source);
```

### Write Path

1. Receive episode text + metadata
2. Generate embedding via the configured OpenAI-compatible server (e.g., `nomic-embed-text`, 768 dimensions)
3. Insert into DuckDB
4. If embedding service is unavailable: insert with NULL embedding, queue for retry
5. Return success to caller immediately

### Search

Three search modes, selectable via the `search_mode` parameter:

- **Vector (default):** Finds memories by meaning. Uses HNSW vector index with cosine similarity — "deployment preferences" matches memories about CI/CD even without that exact phrase.
- **Keyword:** Finds memories by exact words. Uses DuckDB's FTS extension (BM25 scoring) on `content` and `name` fields. No embedding required — works even when the embeddings server is down.
- **Hybrid:** Combines both approaches with configurable weighting (alpha, default 0.7 favoring semantic). BM25 scores are min-max normalized to [0,1] before combining with cosine similarity.

All modes support additional filters:

- **Temporal:** Filter by `created_at`, `valid_at`, `expired_at` ranges
- **Tag-based:** List containment queries
- **Combined:** All of the above in a single query

When a search query is received in vector or hybrid mode, the query text is embedded and results are ranked by `array_cosine_similarity()`. If embedding generation fails (e.g., the embeddings server is down), vector search falls back to chronological ordering and hybrid degrades to keyword-only.

### Embedding provenance and re-embedding

Every stored vector is stamped with the model that produced it (`embedding_model` column on `episodes`, `entities`, and `knowledge`). A row is *stale* when its embedding is missing (the embedding server was down at write time) or was produced by a model other than the one currently configured — both silently degrade vector search because different models occupy different vector spaces.

Engram warns at startup when stale embeddings exist and exposes `POST /api/v1/admin/reembed` to regenerate them asynchronously in place. Embeddings are pure derived data, so the pass never touches episode content; it is idempotent and resumable (keyset pagination, per-row failures are skipped and retried on the next run). `{"force": true}` regenerates every row regardless of provenance. Progress is observable via `GET /api/v1/admin/reembed` and `/api/v1/status`.

## Layer 2: Derived Knowledge Graph (Dreamer)

The Dreamer is an asynchronous worker that reads stored episodes and extracts entities and subject-predicate-object triples into the knowledge graph (`entities`, `knowledge`, and `episode_links` tables), then links episodes that share entities. It is **disabled by default** — trigger a pass with `POST /api/v1/admin/dream` (progress via `GET`), or set `ENGRAM_DREAM_INTERVAL` to run it on a schedule.

Key properties:

- **Async, never in the write path** — episodes are stored instantly; enrichment happens later in a background job
- **Derived data only** — episode content is never modified; the graph can always be rebuilt from the episode log
- **Deterministic validation pipeline** — LLM output is filtered before anything is written: predicates must come from the controlled vocabulary, confidence is clamped to [0,1], triples whose subject and object both fail to appear in the episode text are rejected as hallucinations, and at most 10 triples are stored per episode
- **Failures don't loop** — each episode is processed once; LLM or parse failures stamp the episode with an `enrichment_error` in its metadata rather than retrying forever
- **Pluggable LLM** — an OpenAI-compatible chat endpoint (default, works with Ollama/LM Studio) or the Claude Code CLI as a subprocess

Triples written by the Dreamer carry `source: "dreamer/<model>"`, the LLM's confidence score, and `verified: false`, distinguishing them from client-written facts.

## MCP Server

Go service using the official MCP SDK, exposing tools over SSE:

| Tool | Description | LLM Required |
| --- | --- | :---: |
| `add_memory` | Store a new episode | No |
| `add_conversation` | Store a multi-turn conversation as one episode | No |
| `search` | Semantic + temporal + tag search, optional graph traversal (`graph_depth`) | No |
| `search_knowledge` | Semantic search over knowledge triples | No |
| `add_knowledge` | Store a knowledge triple directly | No |
| `link_episodes` | Link two related episodes | No |
| `find_loose_ends` | Surface weakly-connected episodes, entities, and clusters | No |
| `get_episodes` | Retrieve by time range, source, or group | No |
| `update_episode` | Modify metadata/tags/expiration | No |
| `get_status` | Health check | No |

Episodes can be marked as expired but not deleted. This prevents accidental memory loss.

## Transport

Engram uses a **server-first architecture** to avoid DuckDB's single-writer file lock:

- **`engram serve`** (default) — HTTP server owning the DuckDB database, exposing MCP over SSE at `/mcp/sse`, REST API at `/api/v1/*`, health probes at `/health` and `/ready`. This is the only process that touches the database.
- **`engram stdio`** — Thin stateless proxy that bridges stdin/stdout JSON-RPC to the server via SSE. For clients that don't support SSE natively (e.g., Claude Desktop). Uses `mcp-go/client/transport.SSE` for robust endpoint discovery and session management.

**SSE is the primary transport.** Clients that support it (Cursor, Claude Code) connect directly to `http://localhost:3490/mcp/sse`. The stdio proxy is a compatibility shim for clients that only speak stdio.

## Infrastructure

- **Database:** DuckDB with VSS and FTS extensions — single-file, portable, HNSW indexing for vector search, BM25 indexing for full-text search, native LIST and JSON support
- **Application:** Go with official MCP SDK — single static binary, cross-platform
- **Embeddings:** any OpenAI-compatible `/v1/embeddings` server — LM Studio, Ollama, vLLM, llama.cpp, or hosted providers; local generation means no external API costs
- **Default port:** 3490 (configurable via `ENGRAM_PORT`)

## Deployment Options

**Native binary:**
Single executable + DuckDB file. Run `engram serve` as a background service. See [MCP Integration Guide](mcp-integration.md#running-as-a-background-service) for platform-specific instructions.

**Docker container:**
Multi-stage build using Debian Bookworm (glibc compatibility). See the [`Dockerfile`](../Dockerfile).

**Kubernetes:**
Deployment with PersistentVolume for the `.duckdb` file. Requires ingress configuration for SSE support (no buffering, long timeouts).

## Design Principles

1. **Writes never fail** (if the database is up)
2. **No LLM in the write path** — embeddings only, and those are retryable
3. **Episode log is source of truth** — everything else is derived
4. **Rebuild over repair** — if derived data is wrong, rebuild from episodes
5. **Simple over clever** — vector search covers 80% of use cases without a graph
6. **Multi-tenant by default** — `group_id` supports multiple users/contexts
7. **Observable** — every episode records which client and model wrote it

## Current Limitations

- **Hardcoded embedding dimension:** Schema uses `FLOAT[768]` (tied to `nomic-embed-text`)
- **FTS index rebuild scales linearly:** DuckDB FTS doesn't support incremental updates, so the full-text index is rebuilt lazily (on the next keyword/hybrid search after a write). This is imperceptible under 1K episodes, takes 1–5 seconds at 1K–10K, and may need a different strategy beyond 10K.

## Future Roadmap

- Memory consolidation and summarization via the Dreamer
- Support for multiple embedding models and dimensions
- Batch embedding generation for bulk imports
