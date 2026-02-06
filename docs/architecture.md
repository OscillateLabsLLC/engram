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
2. Generate embedding via Ollama (e.g., `nomic-embed-text`, 768 dimensions)
3. Insert into DuckDB
4. If embedding service is unavailable: insert with NULL embedding, queue for retry
5. Return success to caller immediately

### Search

- **Semantic:** VSS HNSW index with cosine similarity on embeddings
- **Temporal:** Filter by `created_at`, `valid_at`, `expired_at` ranges
- **Tag-based:** List containment queries
- **Combined:** All of the above in a single query

When a search query is received, the query text is embedded and results are ranked by `array_cosine_similarity()`. If embedding generation fails (e.g., Ollama is down), search gracefully falls back to chronological ordering.

## Layer 2: Derived Knowledge Graph (Future)

A periodic batch process that reads episodes and builds entity/relationship structures. **Not currently implemented.** The episode store alone with semantic search provides the majority of the value.

When built:

- Runs as a background job, not in the write path
- Can use any graph backend
- Failures don't lose data — just means the graph is stale until the next successful run
- Can be rebuilt from scratch at any time from the episode log
- Entity resolution happens here, with human review capability

## MCP Server

Go service using the official MCP SDK, exposing tools over stdio transport:

| Tool | Description | LLM Required |
| --- | --- | :---: |
| `add_memory` | Store a new episode | No |
| `search` | Semantic + temporal + tag search | No |
| `get_episodes` | Retrieve by time range, source, or group | No |
| `update_episode` | Modify metadata/tags/expiration | No |
| `get_status` | Health check | No |

Episodes can be marked as expired but not deleted. This prevents accidental memory loss.

## Infrastructure

- **Database:** DuckDB with VSS extension — single-file, portable, HNSW indexing, native LIST and JSON support
- **Application:** Go with official MCP SDK — single static binary, cross-platform
- **Embeddings:** Ollama — local generation, OpenAI-compatible `/v1/embeddings` endpoint, no external API costs
- **Transport:** stdio (for Claude Desktop/Code/Cursor), HTTP planned for future

## Deployment Options

**Native binary:**
Single executable + DuckDB file. No Python, no system packages, no database server.

**Docker container:**
Multi-stage build, Alpine-based final image. See the [`Dockerfile`](../Dockerfile).

**Kubernetes:**
StatefulSet with PersistentVolume for the `.duckdb` file.

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
- **No hybrid ranking:** Pure semantic search, no temporal decay factor
- **No similarity threshold:** Returns all results regardless of relevance score
- **No full-text search:** Semantic search covers most use cases; FTS is a future enhancement

## Future Roadmap

- HTTP transport for OpenWebUI and other HTTP-based clients
- Layer 2 knowledge graph with entity extraction
- Memory consolidation and summarization
- Similarity score in search results and `min_similarity` threshold
- Support for multiple embedding models and dimensions
- Batch embedding generation for bulk imports
