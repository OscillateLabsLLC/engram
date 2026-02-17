# Deployment Guide

This guide covers different deployment methods for Engram in production and development.

## Quick Start (Development)

For local development with persistent storage and auto-restart:

### macOS/Windows

```bash
just docker-up
```

This uses `docker-compose.yml` which connects to Ollama on your host machine via `host.docker.internal`.

### Linux

```bash
just docker-up-linux
```

This uses `docker-compose.linux.yml` which uses host networking to connect to Ollama.

### Common Commands

```bash
just docker-down       # Stop services
just docker-logs       # View logs
just docker-restart    # Restart services
```

## Docker Compose

### macOS/Windows Configuration

`docker-compose.yml`:

```yaml
services:
  engram:
    build: .
    container_name: engram
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - engram-data:/data
    environment:
      - DUCKDB_PATH=/data/engram.duckdb
      - OLLAMA_URL=http://host.docker.internal:11434
      - EMBEDDING_MODEL=nomic-embed-text
    extra_hosts:
      - "host.docker.internal:host-gateway"
    networks:
      - engram-network

volumes:
  engram-data:
    driver: local

networks:
  engram-network:
    driver: bridge
```

**Key features:**
- `restart: unless-stopped` — starts with Docker and keeps running
- `extra_hosts` — ensures proper host connectivity on macOS
- Named volume `engram-data` — persists database across restarts
- `host.docker.internal` — connects to Ollama running on host

### Linux Configuration

`docker-compose.linux.yml`:

```yaml
services:
  engram:
    build: .
    container_name: engram
    restart: unless-stopped
    network_mode: host
    volumes:
      - engram-data:/data
    environment:
      - DUCKDB_PATH=/data/engram.duckdb
      - OLLAMA_URL=http://localhost:11434
      - EMBEDDING_MODEL=nomic-embed-text

volumes:
  engram-data:
    driver: local
```

**Key differences:**
- `network_mode: host` — container shares host network stack
- `OLLAMA_URL=http://localhost:11434` — direct localhost connection
- No explicit port mapping needed (host mode)

## Manual Docker Usage

### Local/stdio Mode (Default)

```bash
docker build -t engram .
docker run -e OLLAMA_URL=http://host.docker.internal:11434 \
           -v $(pwd)/data:/data \
           -e DUCKDB_PATH=/data/engram.duckdb \
           engram
```

### HTTP/SSE Mode (Remote Access)

```bash
docker build -t engram .
docker run -p 8080:8080 \
           -e OLLAMA_URL=http://host.docker.internal:11434 \
           -v $(pwd)/data:/data \
           -e DUCKDB_PATH=/data/engram.duckdb \
           engram -mode http -port 8080
```

HTTP mode exposes:
- `/mcp/sse` — MCP over Server-Sent Events (for Cursor/Claude Desktop via remote)
- `/mcp/message` — MCP message endpoint
- `/api/v1/*` — REST API for Open WebUI integration
- `/openapi.json` — OpenAPI 3.0 specification
- `/health`, `/ready` — Kubernetes health probes

## Kubernetes Deployment

Engram can be deployed to Kubernetes with persistent storage:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: engram
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: engram
          image: your-registry/engram:latest
          command: ["/engram"]
          args: ["-mode", "http", "-port", "8080"]
          env:
            - name: DUCKDB_PATH
              value: "/data/engram.duckdb"
            - name: OLLAMA_URL
              value: "http://ollama-service:11434"
            - name: EMBEDDING_MODEL
              value: "nomic-embed-text"
          ports:
            - containerPort: 8080
          volumeMounts:
            - name: data
              mountPath: /data
          readinessProbe:
            httpGet:
              path: /ready
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 10
            periodSeconds: 30
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: engram-data
---
apiVersion: v1
kind: Service
metadata:
  name: engram
spec:
  selector:
    app: engram
  ports:
    - port: 8080
      targetPort: 8080
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: engram
  annotations:
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-connect-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-buffering: "off"
    nginx.ingress.kubernetes.io/proxy-request-buffering: "off"
    nginx.ingress.kubernetes.io/proxy-http-version: "1.1"
spec:
  rules:
    - host: engram.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: engram
                port:
                  number: 8080
```

> **Important:** SSE connections require long timeouts and no buffering. The ingress annotations above are critical for stable MCP connections.

## Troubleshooting

### Docker on macOS: Cannot connect to Ollama

If you see connection errors to Ollama:

1. Verify Ollama is running: `ollama list`
2. Check the `extra_hosts` configuration is present in `docker-compose.yml`
3. Try accessing Ollama from within the container:
   ```bash
   docker exec -it engram /bin/sh
   curl http://host.docker.internal:11434/api/tags
   ```

### Docker on Linux: Port conflicts

If using `network_mode: host`, ensure port 8080 is available:

```bash
sudo lsof -i :8080
```

### Volume permissions

If you encounter permission errors with the volume:

```bash
# Check volume location
docker volume inspect engram-data

# On Linux, you may need to adjust ownership
docker run --rm -v engram-data:/data alpine chown -R 1000:1000 /data
```

### Viewing logs

```bash
# Docker Compose
docker compose logs -f engram

# Docker directly
docker logs -f engram
```
