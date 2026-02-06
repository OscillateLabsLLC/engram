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

## Configuration

### For Claude Desktop

1. Open Claude Desktop config:
   - Press `Win + R`
   - Type: `%APPDATA%\Claude\`
   - Open `claude_desktop_config.json`

2. Add Engram configuration:

```json
{
  "mcpServers": {
    "engram-memory": {
      "command": "C:\\Users\\YourName\\engram\\engram.exe",
      "args": [],
      "env": {
        "DUCKDB_PATH": "C:\\Users\\YourName\\engram\\memory.duckdb",
        "OLLAMA_URL": "http://localhost:11434",
        "EMBEDDING_MODEL": "nomic-embed-text"
      }
    }
  }
}
```

**Important:** Use double backslashes (`\\`) in Windows paths!

3. Restart Claude Desktop

### For Claude Code (VS Code)

1. Open VS Code settings: `Ctrl + Shift + P` → "Preferences: Open User Settings (JSON)"
2. Add MCP configuration (similar to above)
3. Restart VS Code

## Running Manually (Testing)

### PowerShell

```powershell
$env:DUCKDB_PATH = "C:\Users\YourName\engram\test.duckdb"
$env:OLLAMA_URL = "http://localhost:11434"
$env:EMBEDDING_MODEL = "nomic-embed-text"

.\engram.exe
```

### Command Prompt

```cmd
set DUCKDB_PATH=C:\Users\YourName\engram\test.duckdb
set OLLAMA_URL=http://localhost:11434
set EMBEDDING_MODEL=nomic-embed-text

engram.exe
```

## Troubleshooting

### "DuckDB extension not found"

The VSS extension should be automatically installed. If you see errors:

1. Ensure you're running the latest version
2. Check that the DuckDB path is writable
3. Try deleting the `.duckdb` file and letting it recreate

### "Cannot connect to Ollama"

1. Check Ollama is running: `ollama list`
2. Verify the model is installed: `ollama pull nomic-embed-text`
3. Test the API: `curl http://localhost:11434/api/tags`

### "MCP server not starting in Claude"

1. Check paths use double backslashes (`\\`)
2. Verify `engram.exe` exists at the specified path
3. Look at Claude Desktop logs:
   - `%APPDATA%\Claude\logs\`

### Permission Errors

If you get "Access Denied" errors:

1. Run as Administrator (right-click → "Run as Administrator")
2. Or move to a user-writable directory (avoid `Program Files`)

## File Locations

Default paths on Windows:

- **Config**: `%APPDATA%\Claude\claude_desktop_config.json`
- **Database**: Same directory as `engram.exe` (or custom via `DUCKDB_PATH`)
- **Logs**: Check Claude Desktop logs in `%APPDATA%\Claude\logs\`

## Sharing Memory Database

The `.duckdb` file is portable! You can:

- Copy it to another machine (Windows, Mac, Linux)
- Back it up to cloud storage
- Keep separate databases for different projects

Just point `DUCKDB_PATH` to wherever you want to store/load from.

## Updating

To update Engram:

1. Download new version
2. Replace `engram.exe`
3. Restart Claude Desktop/Code

Your database file is separate and won't be affected.

## Getting Help

- **Issues**: https://github.com/oscillatelabsllc/engram/issues
- **Docs**: https://github.com/oscillatelabsllc/engram
