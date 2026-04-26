# External Integrations

**Analysis Date:** 2026-04-26

## APIs & External Services

**Mod Distribution:**
- **CurseForge API** - Downloads mod metadata and resolves actual download URLs
  - Endpoint: `https://api.curseforge.com/v1/mods/{projectID}/files/{fileID}/download-url`
  - SDK: Native HTTP (uses `net/http`)
  - Auth: `X-Api-Key` header with `CURSEFORGE_API_KEY` from config/env
  - Implementation: `internal/downloader/downloader.go:158-198`

- **CurseForge CDN** - Direct file download fallback when no API key
  - Pattern: `https://edge.forgecdn.net/files/{fileID/1000}/{fileID%1000}/`
  - Auth: None (public CDN)
  - Implementation: `internal/downloader/downloader.go:167-168`

**Mod Loader Distribution:**
- **Minecraft Forge Maven** - Downloads Forge installer JARs
  - URL: `https://maven.minecraftforge.net/net/minecraftforge/forge/{mcVersion}-{loaderVersion}/forge-{mcVersion}-{loaderVersion}-installer.jar`
  - Implementation: `internal/installer/installer.go:15,75-81`

- **Fabric Meta API** - Downloads Fabric server launcher
  - URL: `https://meta.fabricmc.net/v2/versions/loader/{mcVersion}/{loaderVersion}/server/jar`
  - Implementation: `internal/installer/installer.go:16,103-109`

## Data Storage

**File Cache:**
- Location: `~/.cache/mcpackctl/` (configurable via `MCPACKCTL_CACHE_DIR`)
- Storage: Local filesystem
- Purpose: Caches downloaded mod JARs to avoid re-downloading
- Implementation: `internal/downloader/downloader.go:106-116`

**Configuration:**
- Location: `~/.config/mcpackctl/config.yaml`
- Format: YAML
- Implementation: `internal/cmd/cmd.go:49-72`

**No databases** - All data stored in filesystem

## Authentication & Identity

**CurseForge API Key:**
- Provided via: `MCPACKCTL_CURSEFORGE_API_KEY` env var or config file
- Used for: Authenticated requests to CurseForge API (higher rate limits, access to all mods)
- Implementation: `internal/downloader/downloader.go:36,139-141`

## Monitoring & Observability

**Logging:**
- Framework: `github.com/rs/zerolog` (structed JSON logging)
- Output: Console (stderr) with RFC3339 timestamps
- Levels: debug, info, warn, error (configurable via `--log-level`)
- Implementation: `pkg/logger/logger.go`

**No error tracking service** - Errors logged to console only

## Environment Configuration

**Required env vars:**
- `MCPACKCTL_CURSEFORGE_API_KEY` - Optional, for CurseForge API access

**Optional env vars (with defaults):**
- `MCPACKCTL_RAM` - Default: "2G"
- `MCPACKCTL_JAVA_PATH` - Default: "java"
- `MCPACKCTL_CACHE_DIR` - Default: "~/.cache/mcpackctl"
- `MCPACKCTL_WORKERS` - Default: 4
- `MCPACKCTL_LOG_LEVEL` - Default: "info"

**Config file:**
- Default path: `~/.config/mcpackctl/config.yaml`
- Custom path: `--config <path>` flag

## Webhooks & Callbacks

**Incoming:**
- None - CLI tool, not a server

**Outgoing:**
- None - No webhook integrations

---

*Integration audit: 2026-04-26*