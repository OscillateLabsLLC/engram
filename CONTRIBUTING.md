# Contributing to Engram

Thanks for your interest in contributing! This document provides guidelines for contributing to Engram.

## Development Setup

### Prerequisites

- Go 1.25 or higher
- Ollama with `nomic-embed-text` model
- Git

### Getting Started

1. Fork and clone the repository:

   ```bash
   git clone https://github.com/OscillateLabsLLC/engram
   cd engram
   ```

2. Install [just](https://github.com/casey/just) (task runner):

   ```bash
   # macOS
   brew install just
   # Linux
   cargo install just
   # Windows
   scoop install just
   ```

3. Install dependencies and build:

   ```bash
   just setup
   ```

   Or step by step:

   ```bash
   just deps
   just build
   ```

4. Run tests:

   ```bash
   just test
   ```

## Project Structure

```text
engram/
├── cmd/engram/          # Main application entry point
├── internal/
│   ├── db/              # DuckDB operations
│   ├── embedding/       # Ollama client for embeddings
│   ├── mcp/             # MCP server implementation
│   └── models/          # Data models
├── docs/                # Architecture and design docs
├── scripts/             # Build and test scripts
├── Dockerfile           # Container image definition
├── justfile             # Task runner commands
└── README.md            # Project documentation
```

## Making Changes

### Code Style

- Follow standard Go conventions
- Run `just fmt` before committing
- Use meaningful variable names
- Add comments for exported functions

### Testing

- Add tests for new functionality
- Ensure all tests pass: `just test`
- Test cross-platform builds: `just build-all`

### Commits

Use clear, descriptive commit messages:

```text
Add semantic search filtering by tags

- Implement tag filtering in search queries
- Add tests for tag-based search
- Update documentation
```

### Pull Requests

1. Create a feature branch: `git checkout -b feature/my-feature`
2. Make your changes
3. Add tests
4. Run `just test` and `just fmt`
5. Commit with clear messages
6. Push to your fork
7. Open a pull request

## Areas for Contribution

### High Priority

- [ ] Improve vector search performance
- [ ] Add comprehensive error handling
- [ ] Implement retry logic for embedding failures
- [ ] Add integration tests
- [ ] Performance benchmarks
- [ ] OpenTelemetry observability

### Medium Priority

- [ ] HTTP transport for OpenWebUI
- [ ] Backup/restore utilities
- [ ] Memory consolidation strategies
- [ ] Admin/debugging tools

### Documentation

- [ ] Tutorial videos
- [ ] More usage examples
- [ ] API documentation
- [ ] Troubleshooting guide

## Running Tests

### Unit Tests

```bash
go test ./...
```

### Integration Tests

Requires Ollama running:

```bash
export OLLAMA_URL=http://localhost:11434
go test -tags=integration ./...
```

### Manual Testing

```bash
# Start server
just dev

# In another terminal, test with MCP client
./scripts/test-mcp.sh

# Or build and run in one command
just run
```

## Architecture Decisions

### Event Sourcing

Episodes are the source of truth. Any derived structures (graphs, indices) should be rebuildable from episodes.

### No LLM in Write Path

Embeddings can fail, but writes should succeed. If embedding fails, store with NULL and retry later.

### DuckDB Choice

Analytical workload (time-range queries, filtering) is DuckDB's strength. Plus it's a single file, making deployment trivial.

### Go Language

Official MCP SDK support, fast compilation, cross-platform binaries without runtime dependencies.

## Questions?

- Open an issue for bugs or feature requests
- Start a discussion for architecture questions
- Check existing issues before creating new ones

## Code of Conduct

Be respectful and constructive. We're all here to build something useful.

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
