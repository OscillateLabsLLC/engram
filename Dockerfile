# Multi-stage build for minimal final image
FROM golang:1.25-alpine AS builder

# Install build dependencies (CGO required for DuckDB)
RUN apk add --no-cache gcc g++ musl-dev

WORKDIR /app

# Copy go mod files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o engram ./cmd/engram/main.go

# Final stage
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates libc6-compat

WORKDIR /root/

# Copy binary from builder
COPY --from=builder /app/engram .

# Default environment variables (can be overridden)
ENV DUCKDB_PATH=/data/engram.duckdb
ENV OLLAMA_URL=http://localhost:11434
ENV EMBEDDING_MODEL=nomic-embed-text

# Create data directory
RUN mkdir -p /data

# Volume for persistent storage
VOLUME ["/data"]

CMD ["./engram"]
