# SHELVED — dreamer-graph-layer experiment (negative result)

**Status: frozen. Do not merge to `main`. Do not resume without new evidence.**
**Date shelved: 2026-07-07.**

This branch adds a derived knowledge-graph layer on top of Engram's episodic
store: an LLM "dreamer" that extracts knowledge triples, a typed episode-link
graph (`same_entity` / `supersedes` / `contradicts` / `elaborates` /
`follows_up`), `graph_depth` traversal, `find_loose_ends`, and `search_knowledge`.
It was built to answer one question:

> **Does engaging the graph produce answers good episodic search can't?**

Two controlled evals (engram-eval `v0.2`, `v0.3`) answer: **no. On this corpus the
graph layer is net-negative.** It is shelved as a falsified hypothesis, not an
unfinished feature.

## What the evals showed

Three cross-time synthesis questions, OLD (search-only) vs NEW (full graph),
fresh sub-agents, every tool call + edge type logged for mechanistic attribution.

- **Q1 (tonal contrast): wash.** Traversal returned only `same_entity`
  co-occurrence noise; the retracted-canon catch came from plain search on
  episode prose. `search_knowledge` net-negative (off-topic + content-free
  name-pair triples).
- **Q2 (project evolution): OLD slightly better.** Typed `supersedes` edges DID
  appear and were correctly oriented (newer→older, zero bidirectional — the
  orientation fix works). But they **pointed at non-retrievable episodes**, so
  they added an *unverifiable assertion*, not signal. The causal arc lived in
  prose, which search read equally.
- **Q3 (loose ends — the decisive one): NEW did not beat OLD.**
  `find_loose_ends.unlinked_episodes` **proxies recency-of-ingestion for
  neglect**, so it surfaces flagship/recent episodes — the exact opposite of the
  "forgotten small things" ask, reproducing the v0.1 Class-4 failure it was
  meant to fix. Both arms needed a plain-search workaround.

## Why it's net-negative (the mechanism, so this isn't re-litigated)

1. **The truth already lives in timestamped prose.** Engram's episodes carry the
   causal/narrative content *and* `created_at` / `valid_at`. Good hybrid search
   reads the prose; the timestamps already order it. The graph is a third
   representation of information the other two already cover — and a lossier one
   (predicates encode structure, not the "why").
2. **Derived edges manufacture confident-but-unverifiable structure.** A
   correctly-built, correctly-oriented `supersedes` edge *degraded* Q2 because it
   asserted a relationship to a ghost target. Better edge machinery → more
   confident pointing at nothing. See the `98de67ef` fan-in and the REVISED-CANON
   dangling targets.
3. **Supersession is temporal, not textual.** `MarkerReferenceBreakdown` on the
   live corpus: of 194 marker-bearing episodes, **172 (89%) cite no target ID at
   all.** Deterministic prose→edge extraction structurally cannot reach them.
   Fuzzy (LLM) resolution — the proposed "tier 3" — would only add more edges into
   the same non-retrievable target space. Killed before build.

## What was salvaged into the shipping product (NOT shelved)

These are real product wins that came out of the experiment and live on `main`
via their own PRs — they are independent of the graph and stay:

- **#27** WAL-checkpoint durability (anti-bricking).
- **#25** OpenAI-compatible embeddings + provenance.
- **#29** reembed skips expired rows.
- **#31** active embedding-endpoint health probe (`/health`, `/status`).
- **#33** accept novel predicates at assertion time (open predicates) — refines
  the shipping `add_knowledge` surface; not part of the episode-link graph.
- **#34** bounded shutdown drain — SSE clients no longer hang shutdown into a
  SIGKILL that skips `store.Close()` (data-durability fix).

## If you ever reconsider

The graph is not disproven *in general* — it's disproven **for a dense personal
corpus whose supersession knowledge is temporal and ID-less**. It might earn its
keep on a corpus with explicit, ID-bearing relationships (issue trackers, code
review threads, structured logs). For *this* store, the higher-leverage direction
the evals pointed to is **temporal-recency retrieval** (surface current state by
`valid_at`, history on request) and **loose-ends ranking by neglect, not
ingestion recency** — both graph-free, both operating on data the episodic store
already has. Start there, not here.

Raw evidence: `engram-eval/v0.2/COMPARISON.md`, `engram-eval/v0.3/COMPARISON.md`.
