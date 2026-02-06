#!/bin/bash
# Build binaries for all platforms

set -e

echo "Building Engram for all platforms..."

mkdir -p bin

# Linux
echo "Building for Linux (amd64)..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -o bin/engram-linux-amd64 ./cmd/engram/main.go

echo "Building for Linux (arm64)..."
GOOS=linux GOARCH=arm64 CGO_ENABLED=1 go build -o bin/engram-linux-arm64 ./cmd/engram/main.go

# macOS
echo "Building for macOS (amd64)..."
GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -o bin/engram-darwin-amd64 ./cmd/engram/main.go

echo "Building for macOS (arm64)..."
GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -o bin/engram-darwin-arm64 ./cmd/engram/main.go

# Windows
echo "Building for Windows (amd64)..."
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 go build -o bin/engram-windows-amd64.exe ./cmd/engram/main.go

echo
echo "Build complete! Binaries are in ./bin/"
ls -lh bin/
