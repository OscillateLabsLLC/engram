#!/bin/bash
# Test script for Engram HTTP API

BASE_URL="${BASE_URL:-http://localhost:8080}"

echo "Testing Engram HTTP API at $BASE_URL"
echo "=========================================="

# Test health endpoint
echo -e "\n1. Testing /health endpoint..."
curl -s "$BASE_URL/health" | jq .

# Test ready endpoint
echo -e "\n2. Testing /ready endpoint..."
curl -s "$BASE_URL/ready" | jq .

# Test OpenAPI spec
echo -e "\n3. Testing /openapi.json endpoint..."
curl -s "$BASE_URL/openapi.json" | jq '.info'

# Test add memory
echo -e "\n4. Testing POST /api/v1/memory (add_memory)..."
curl -s -X POST "$BASE_URL/api/v1/memory" \
  -H "Content-Type: application/json" \
  -d '{
    "content": "The user prefers dark mode and uses VS Code for development",
    "source": "test-script",
    "name": "User preferences test",
    "tags": ["preferences", "development"]
  }' | jq .

# Test search
echo -e "\n5. Testing GET /api/v1/memory/search..."
curl -s "$BASE_URL/api/v1/memory/search?query=preferences&max_results=5" | jq '.count, .episodes[0] | select(.)'

# Test get episodes
echo -e "\n6. Testing GET /api/v1/memory/episodes..."
curl -s "$BASE_URL/api/v1/memory/episodes?max_results=3" | jq '.count'

# Test get status
echo -e "\n7. Testing GET /api/v1/status..."
curl -s "$BASE_URL/api/v1/status" | jq .

echo -e "\n=========================================="
echo "All tests completed!"
