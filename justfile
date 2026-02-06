# Engram task runner
# Install just: https://github.com/casey/just

# Default recipe (shows all available commands)
default:
    @just --list

# Build for current platform
build:
    go build -o engram ./cmd/engram/main.go

# Build for all platforms
build-all:
    mkdir -p bin
    GOOS=linux GOARCH=amd64 go build -o bin/engram-linux-amd64 ./cmd/engram/main.go
    GOOS=linux GOARCH=arm64 go build -o bin/engram-linux-arm64 ./cmd/engram/main.go
    GOOS=darwin GOARCH=amd64 go build -o bin/engram-darwin-amd64 ./cmd/engram/main.go
    GOOS=darwin GOARCH=arm64 go build -o bin/engram-darwin-arm64 ./cmd/engram/main.go
    GOOS=windows GOARCH=amd64 go build -o bin/engram-windows-amd64.exe ./cmd/engram/main.go
    @echo "Binaries built in ./bin/"
    @ls -lh bin/

# Build Windows binary
build-windows:
    mkdir -p bin
    GOOS=windows GOARCH=amd64 go build -o bin/engram-windows-amd64.exe ./cmd/engram/main.go
    @echo "Windows binary ready: bin/engram-windows-amd64.exe"

# Clean build artifacts and test databases
clean:
    rm -f engram
    rm -rf bin/
    rm -f *.duckdb *.duckdb.wal
    rm -f test.duckdb dev.duckdb
    @echo "Cleaned build artifacts"

# Run tests
test:
    go test -v ./...

# Run tests with coverage
test-coverage:
    go test -v -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report: coverage.html"

# Install/update dependencies
deps:
    go mod download
    go mod tidy

# Format code
fmt:
    go fmt ./...

# Lint code (requires golangci-lint)
lint:
    golangci-lint run

# Run engram locally for development
dev:
    #!/usr/bin/env bash
    export DUCKDB_PATH=./dev.duckdb
    export OLLAMA_URL=http://localhost:11434
    export EMBEDDING_MODEL=nomic-embed-text
    ./engram

# Build and run for quick testing
run: build dev

# Build Docker image
docker-build:
    docker build -t engram:latest .
    @echo "Docker image built: engram:latest"

# Run in Docker
docker-run:
    docker run -it --rm \
        -e OLLAMA_URL=http://host.docker.internal:11434 \
        -v {{justfile_directory()}}/data:/data \
        engram:latest

# Check if Ollama is running
check-ollama:
    @echo "Checking Ollama..."
    @curl -s http://localhost:11434/api/tags > /dev/null && echo "✅ Ollama is running" || echo "❌ Ollama is not running"

# Pull required embedding model
setup-ollama:
    ollama pull nomic-embed-text
    @echo "✅ Embedding model ready"

# Full setup (deps + ollama + build)
setup: deps setup-ollama build
    @echo "✅ Setup complete! Run 'just dev' to start"

# Watch for changes and rebuild (requires watchexec)
watch:
    watchexec -e go -r -- just build

# Create release build with version info
release version:
    mkdir -p dist
    GOOS=linux GOARCH=amd64 go build -ldflags="-X main.Version={{version}}" -o dist/engram-{{version}}-linux-amd64 ./cmd/engram/main.go
    GOOS=darwin GOARCH=arm64 go build -ldflags="-X main.Version={{version}}" -o dist/engram-{{version}}-darwin-arm64 ./cmd/engram/main.go
    GOOS=windows GOARCH=amd64 go build -ldflags="-X main.Version={{version}}" -o dist/engram-{{version}}-windows-amd64.exe ./cmd/engram/main.go
    cd dist && shasum -a 256 * > checksums.txt
    @echo "Release {{version}} built in ./dist/"

# Generate checksums for bin/ directory
checksums:
    cd bin && shasum -a 256 * > checksums.txt
    @echo "Checksums created: bin/checksums.txt"

# Quick health check
status:
    @echo "=== Engram Status ==="
    @echo "Go version: $(go version)"
    @echo "Binary exists: $(test -f engram && echo '✅ yes' || echo '❌ no')"
    @just check-ollama
    @echo "====================="
