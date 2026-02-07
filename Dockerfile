# Multi-stage build for minimal final image
FROM golang:1.25-bookworm AS builder

# Install build dependencies (CGO required for DuckDB)
RUN apt-get update && apt-get install -y gcc g++ && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy go mod files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=1 GOOS=linux go build -a -o engram ./cmd/engram/main.go

# Final stage - distroless for minimal attack surface
FROM gcr.io/distroless/cc-debian12

WORKDIR /

# Copy binary from builder
COPY --from=builder /app/engram /engram

# Default environment variables (can be overridden)
ENV DUCKDB_PATH=/data/engram.duckdb
ENV OLLAMA_URL=http://localhost:11434
ENV EMBEDDING_MODEL=nomic-embed-text

# Expose HTTP port
EXPOSE 8080

# Volume for persistent storage
VOLUME ["/data"]

CMD ["/engram", "-mode", "http", "-port", "8080"]
