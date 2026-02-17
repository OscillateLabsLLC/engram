# Engram Documentation

Welcome to the Engram documentation! This directory contains detailed guides for using and deploying Engram.

## Quick Navigation

### Getting Started
- [Main README](../README.md) - Quick start, installation, and overview
- [MCP Integration Guide](mcp-integration.md) - Connect Engram to Claude Desktop, Cursor, etc.

### Deployment
- [Deployment Guide](deployment.md) - Docker Compose, Kubernetes, production setup
- [Windows Setup](../WINDOWS.md) - Platform-specific Windows instructions

### Technical
- [Architecture](architecture.md) - System design and technical deep dive

## What is Engram?

Engram is an event-sourced memory system for AI agents. It stores episodes reliably first, then adds semantic search on top — no LLM in the write path.

**Key features:**
- Semantic search via vector similarity
- Graceful fallback when embeddings are unavailable
- Fast queries (sub-100ms with HNSW indexing)
- Local embeddings via Ollama
- Single portable binary
- MCP native integration

## Documentation Structure

```
docs/
├── README.md              # This file
├── mcp-integration.md     # MCP client setup and tools
├── deployment.md          # Deployment methods and configs
└── architecture.md        # Technical architecture

../
├── README.md              # Main project overview
├── CONTRIBUTING.md        # Development guide
├── WINDOWS.md             # Windows-specific setup
└── .env.example           # Configuration template
```

## Need Help?

- **Installation issues?** See [README Quick Start](../README.md#quick-start)
- **MCP not connecting?** See [MCP Integration Guide](mcp-integration.md#troubleshooting)
- **Deployment questions?** See [Deployment Guide](deployment.md#troubleshooting)
- **Want to contribute?** See [CONTRIBUTING.md](../CONTRIBUTING.md)

## Quick Links

- [GitHub Repository](https://github.com/OscillateLabsLLC/engram)
- [Releases](https://github.com/OscillateLabsLLC/engram/releases)
- [Issues](https://github.com/OscillateLabsLLC/engram/issues)
