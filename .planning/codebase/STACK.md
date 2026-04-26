# Technology Stack

**Analysis Date:** 2026-04-26

## Languages

**Primary:**
- Go 1.24.13 - All core functionality including CLI, downloader, installer, runtime

**Secondary:**
- None - Pure Go project

## Runtime

**Environment:**
- Go 1.24.13 (specified in go.mod)

**Package Manager:**
- Go modules (go.mod/go.sum)
- Lockfile: Present (`go.sum`)

## Frameworks

**CLI:**
- github.com/spf13/cobra v1.10.2 - CLI command framework

**Configuration:**
- github.com/spf13/viper v1.21.0 - Configuration management (YAML + env vars)

**Logging:**
- github.com/rs/zerolog v1.35.1 - Structured logging with console output

## Key Dependencies

**Core CLI:**
- github.com/spf13/cobra v1.10.2 - Command-line interface
- github.com/spf13/viper v1.21.0 - Config file and env var handling

**Logging:**
- github.com/rs/zerolog v1.35.1 - Zero-allocation JSON logging

**Standard Library (used heavily):**
- `archive/zip` - ZIP extraction
- `net/http` - HTTP client for downloads
- `os/exec` - Running Forge/Fabric installers and server
- `sync` - Worker pool for parallel downloads
- `encoding/json` - Parsing CurseForge manifests

## Configuration

**Environment:**
- Config file: `~/.config/mcpackctl/config.yaml` (YAML)
- Env prefix: `MCPACKCTL_` (e.g., `MCPACKCTL_RAM`, `MCPACKCTL_CACHE_DIR`)
- Viper handles automatic env var binding with key replacement (`.` → `_`)

**Key configs in `cmd/cmd.go`:**
- `ram` (default: "2G") - JVM heap size
- `java_path` (default: "java") - Java executable path
- `cache_dir` (default: "~/.cache/mcpackctl") - Mod download cache
- `workers` (default: 4) - Parallel download workers
- `curseforge_api_key` - API key for CurseForge (optional)

**Build:**
- Build command: `go build -o mcpackctl ./cmd/mcpackctl`
- No special build configuration files required

## Platform Requirements

**Development:**
- Go ≥ 1.24.13
- Access to internet for downloading mods from CurseForge CDN

**Production:**
- Java runtime (for running Minecraft server)
- Internet access (CurseForge API and CDN)
- ~2-6GB RAM for server (user-configurable)

---

*Stack analysis: 2026-04-26*