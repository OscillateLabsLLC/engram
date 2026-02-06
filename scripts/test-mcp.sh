#!/bin/bash
# Simple test script for MCP server
# This sends a basic MCP initialize request to test the server

set -e

echo "Testing Engram MCP server..."
echo

# Set environment
export DUCKDB_PATH="./test.duckdb"
export OLLAMA_URL="http://localhost:11434"
export EMBEDDING_MODEL="nomic-embed-text"

# Start server and send initialize request
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}' | ./engram

echo
echo "Test complete!"
