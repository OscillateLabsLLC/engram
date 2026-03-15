# Engram on Windows

Quick start guide for running Engram on Windows.

## Prerequisites

1. **Ollama** - Download and install from [ollama.ai](https://ollama.ai)
2. **Embedding model** - Pull the model in PowerShell/CMD:
   ```
   ollama pull nomic-embed-text
   ```

## Installation

### Option 1: Download Pre-built Binary

1. Download `engram-windows-amd64.exe` from releases
2. Rename to `engram.exe` (optional)
3. Place in a permanent location (e.g., `C:\Users\YourName\engram\`)

### Option 2: Build from Source

Requirements:

- Go 1.25+
- GCC (via MinGW or similar for CGO support)

```powershell
git clone https://github.com/oscillatelabsllc/engram
cd engram
go build -o engram.exe ./cmd/engram/main.go
```

## Running the Server

Engram runs as a background server that all your MCP clients connect to. You need to start it once -- it stays running and handles all memory operations.

### Run as a Startup Task (recommended)

1. Create a batch file `start-engram.bat` in `C:\Users\YourName\engram\`:

```batch
@echo off
set DUCKDB_PATH=C:\Users\YourName\engram\memory.duckdb
set OLLAMA_URL=http://localhost:11434
C:\Users\YourName\engram\engram.exe serve
```

2. Press `Win + R`, type `shell:startup`, press Enter
3. Create a shortcut to `start-engram.bat` in the Startup folder
4. Right-click the shortcut → Properties → set "Run" to **Minimized**

Engram will now start automatically on login.

### Run manually (for testing)

#### PowerShell

```powershell
$env:DUCKDB_PATH = "C:\Users\YourName\engram\memory.duckdb"
$env:OLLAMA_URL = "http://localhost:11434"

.\engram.exe serve
```

#### Command Prompt

```cmd
set DUCKDB_PATH=C:\Users\YourName\engram\memory.duckdb
set OLLAMA_URL=http://localhost:11434

engram.exe serve
```

Verify it's running:

```powershell
curl http://localhost:3490/health
```

## Configuring MCP Clients

With the server running, configure your MCP clients to connect.

### Cursor

Add to `.cursor/mcp.json` in your project or `%USERPROFILE%\.cursor\mcp.json` for global access:

```json
{
  "mcpServers": {
    "engram": {
      "url": "http://localhost:3490/mcp/sse"
    }
  }
}
```

### Claude Desktop

1. Press `Win + R`, type `%APPDATA%\Claude\`, press Enter
2. Open `claude_desktop_config.json`
3. Add:

```json
{
  "mcpServers": {
    "engram": {
      "command": "C:\\Users\\YourName\\engram\\engram.exe",
      "args": ["stdio"]
    }
  }
}
```

**Important:** Use double backslashes (`\\`) in Windows paths.

4. Restart Claude Desktop

### Claude Code

```powershell
claude mcp add engram --transport sse http://localhost:3490/mcp/sse
```

## Troubleshooting

### "Cannot connect to engram server"

The stdio proxy (used by Claude Desktop) prints this when the server isn't running. Start `engram serve` first.

### "Cannot connect to Ollama"

1. Check Ollama is running: `ollama list`
2. Verify the model is installed: `ollama pull nomic-embed-text`
3. Test the API: `curl http://localhost:11434/api/tags`

### "Database permission denied"

Move to a user-writable directory (avoid `Program Files`).

### Port already in use

If port 3490 is taken, set a custom port:

```powershell
$env:ENGRAM_PORT = "3491"
.\engram.exe serve
```

Then update the URLs in your MCP configs to use the new port.

## File Locations

- **Config (Claude)**: `%APPDATA%\Claude\claude_desktop_config.json`
- **Config (Cursor)**: `%USERPROFILE%\.cursor\mcp.json`
- **Database**: wherever `DUCKDB_PATH` points (default: current directory)
- **Logs**: stderr output from `engram serve`

## Updating

1. Download the new `engram.exe`
2. Stop the running server (close the terminal or end the startup task)
3. Replace `engram.exe`
4. Restart

Your database file is separate and won't be affected.
