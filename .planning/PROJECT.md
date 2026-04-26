# AutoPackMC

## What This Is

Go-based CLI tool for creating and running Minecraft modpack servers. Manages setup, downloads, installs Forge/Fabric, runs servers. Pure CLI tool.

## Core Value

Single binary that automates Minecraft server setup from modpacks with automatic mod downloading and server installation.

## Requirements

### Validated

- Go CLI tool (cobra + viper + zerolog)
- CurseForge modpack parsing
- Parallel mod downloads with caching
- Forge/Fabric server installation
- Server runtime management
- Client-only mod cleaning

### Active

- [ ] Auto-update system for modpack updates

### Out of Scope

- Web dashboard/UI (CLI only)
- Mobile app
- Cloud hosting/infrastructure

## Context

Built with Go 1.24.13, targets Linux amd64. Single static binary. Already has working CLI:
- `mcpackctl setup --input <pack.zip|dir> --output ./server`
- `mcpackctl start ./server [--ram 6G --java-path /usr/bin/java]`

## Constraints

- **Tech Stack**: Go (>=1.22), cobra, viper, zerolog
- **No external runtimes**: Pure Go, no Python/Node.js
- **Target**: Linux (amd64)
- **UI**: CLI only, no web/GUI

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|--------|
| UI: CLI only | User preference for pure CLI | — Pending |

---

*Last updated: 2026-04-26 after initialization*