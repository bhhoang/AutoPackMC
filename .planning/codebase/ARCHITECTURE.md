# Architecture

**Analysis Date:** 2026-04-26

## System Overview

```
┌───────────────────────────────────────────────────────────────────────────┐
│                           CLI Interface Layer                             │
│           `cmd/mcpackctl/main.go`, `internal/cmd/cmd.go`                   │
│                         Cobra command handlers                            │
└────────────────────────────────┬──────────────────────────────────────────┘
                                 │
                                 ▼
┌───────────────────────────────────────────────────────────────────────────┐
│                         Workflow Orchestration                            │
│           `internal/cmd/cmd.go` — setup/start command logic                │
│     Coordinates: detector → parser → downloader → cleaner → installer    │
└────────────────────────────────┬──────────────────────────────────────────┘
                                 │
         ┌───────────────────────┼───────────────────────┐
         ▼                       ▼                       ▼
┌─────────────────┐   ┌─────────────────┐   ┌─────────────────┐
│     Detector    │   │     Parser      │   │    Downloader    │
│ detector.go     │   │   parser.go     │   │  downloader.go   │
│                 │   │                 │   │                  │
│ Detect pack type │   │ Parse manifests │   │ Download mods    │
│ (CurseForge/Raw)│   │ (JSON formats)  │   │ (parallel HTTP)  │
└────────┬────────┘   └────────┬────────┘   └────────┬────────┘
         │                     │                      │
         └─────────────────────┼──────────────────────┘
                               ▼
┌───────────────────────────────────────────────────────────────────────────┐
│                          Installer / Runtime                              │
│                    installer.go          runtime.go                      │
│              Install Forge/Fabric        Launch Minecraft server          │
└────────────────────────────────┬──────────────────────────────────────────┘
                                 │
                                 ▼
┌───────────────────────────────────────────────────────────────────────────┐
│                              Cleaner                                      │
│                        cleaner.go                                         │
│            Remove client-only mods from mods directory                    │
└────────────────────────────────┬──────────────────────────────────────────┘
                                 │
                                 ▼
┌───────────────────────────────────────────────────────────────────────────┐
│                         Shared Utilities                                  │
│              pkg/logger/logger.go          pkg/utils/utils.go             │
│             Zerolog wrapper (global)        File I/O, HTTP, ZIP          │
└───────────────────────────────────────────────────────────────────────────┘
```

## Component Responsibilities

| Component | Responsibility | File |
|-----------|----------------|------|
| CLI Entry | Wire Cobra commands, config via Viper, delegate to workflow functions | `internal/cmd/cmd.go` |
| Detector | Detect modpack format (CurseForge manifest.json or raw mods/) | `internal/detector/detector.go` |
| Parser | Parse CurseForge manifest.json and raw pack metadata | `internal/parser/parser.go` |
| Downloader | Download mod files from CurseForge API with parallel workers | `internal/downloader/downloader.go` |
| Cleaner | Remove client-only mod JARs (OptiFine, Sodium, etc.) | `internal/cleaner/cleaner.go` |
| Installer | Download/install Forge or Fabric server, write EULA + server.properties | `internal/installer/installer.go` |
| Runtime | Launch Minecraft server, handle graceful shutdown via signals | `internal/runtime/runtime.go` |
| Logger | Global zerolog singleton with console writer | `pkg/logger/logger.go` |
| Utils | File I/O helpers, ZIP extraction, HTTP downloads | `pkg/utils/utils.go` |

## Pattern Overview

**Overall:** CLI Pipeline with Sequential Workflow Steps

**Key Characteristics:**
- Single `cmd.Execute()` entry point; all business logic delegated to `internal/cmd/cmd.go`
- Two top-level commands: `setup` (download + configure) and `start` (run server)
- `setup` command dispatches to either `setupCurseForge()` or `setupRaw()` based on detector result
- Shared utilities (`pkg/`) are stateless helper packages with no internal dependencies
- No persistent state beyond cache directory and config file

## Layers

**CLI Interface:**
- Purpose: Parse user flags, load configuration, route to workflow functions
- Location: `internal/cmd/cmd.go`
- Contains: Cobra command definitions, `runSetup()`, `runStart()`, config init
- Depends on: All internal packages, Viper, zerolog
- Used by: `main.go` entry point

**Business Logic (Internal Packages):**
- Purpose: Encapsulate discrete operations — detection, parsing, download, install, clean, runtime
- Location: `internal/{detector,parser,downloader,installer,cleaner,runtime}/`
- Contains: Pure functions and struct-based services with clear responsibilities
- Depends on: `pkg/utils`, `pkg/logger`
- Used by: `internal/cmd/cmd.go`

**Shared Utilities:**
- Purpose: Reusable helpers for file I/O, networking, logging
- Location: `pkg/{logger,utils}/`
- Contains: Logger singleton, file operations, ZIP handling, HTTP download with retry
- Depends on: External packages (zerolog, stdlib)
- Used by: All internal packages

## Data Flow

### `setup` Command (CurseForge)

1. **Entry** — `cmd.go:runSetup()` (`internal/cmd/cmd.go:99`)
2. **Extract ZIP** — `utils.ExtractZip()` if input is `.zip` (`pkg/utils/utils.go:31`)
3. **Detect** — `detector.Detect()` checks for `manifest.json` or `mods/` dir (`internal/detector/detector.go:32`)
4. **Parse** — `parser.ParseCurseForge()` reads and derives loader info from `manifest.json` (`internal/parser/parser.go:46`)
5. **Download** — `downloader.DownloadMods()` spawns N goroutines to fetch mods from CurseForge API (`internal/downloader/downloader.go:52`)
6. **Copy overrides** — `utils.CopyDir()` copies manifest's `overrides/` directory to output (`pkg/utils/utils.go:95`)
7. **Clean** — `cleaner.Clean()` removes client-only mods by pattern matching JAR filenames (`internal/cleaner/cleaner.go:47`)
8. **Install** — `installer.Install()` downloads Forge installer JAR or Fabric server JAR, runs Forge installer, writes `eula.txt` and `server.properties` (`internal/installer/installer.go:35`)
9. **Exit**

### `setup` Command (Raw Pack)

1. **Entry** — `cmd.go:runSetup()` (`internal/cmd/cmd.go:99`)
2. **Detect** — `detector.Detect()` returns `PackTypeRaw` if `mods/` exists (`internal/detector/detector.go:32`)
3. **Parse** — `parser.ParseRaw()` scans for `version.json`/`pack.json` metadata (`internal/parser/parser.go:88`)
4. **Copy** — `utils.CopyDir()` copies entire raw pack to output
5. **Install** — `installer.Install()` if loader type detected (`internal/installer/installer.go:35`)
6. **Clean** — `cleaner.Clean()` (`internal/cleaner/cleaner.go:47`)
7. **Exit**

### `start` Command

1. **Entry** — `cmd.go:runStart()` (`internal/cmd/cmd.go:258`)
2. **Build args** — `runtime.buildLaunchArgs()` discovers server JAR (Forge `run.sh`, Fabric `fabric-server-launch.jar`, or generic `server.jar`) (`internal/runtime/runtime.go:72`)
3. **Launch** — `runtime.Start()` executes Java process, attaches signal handler for SIGINT/SIGTERM graceful shutdown (`internal/runtime/runtime.go:19`)
4. **Exit on server termination**

## Key Abstractions

**PackType Enum:**
- Purpose: Represents the detected modpack format
- Examples: `PackTypeCurseForge`, `PackTypeRaw`, `PackTypeUnknown`
- File: `internal/detector/detector.go:10`

**Manifest Struct:**
- Purpose: Parsed CurseForge `manifest.json` with derived loader fields
- File: `internal/parser/parser.go:30`

**RawPack Struct:**
- Purpose: Metadata for raw mod pack directories
- File: `internal/parser/parser.go:79`

**Downloader Service:**
- Purpose: Manages parallel mod download via worker goroutine pool
- File: `internal/downloader/downloader.go:33`

**Task Struct:**
- Purpose: Single mod download request (projectID, fileID, destDir)
- File: `internal/downloader/downloader.go:24`

## Entry Points

**Binary Entry:**
- Location: `cmd/mcpackctl/main.go`
- Triggers: CLI invocation via `./mcpackctl`
- Responsibilities: Thin wrapper that calls `cmd.Execute()`

**Secondary Entry:**
- Location: `main.go`
- Triggers: `go run main.go` (alternative entry for development)
- Responsibilities: Same as `cmd/mcpackctl/main.go` — imports `internal/cmd`

**Cobra Root Command:**
- Location: `internal/cmd/cmd.go:24`
- Triggers: After `rootCmd.Execute()` is called
- Responsibilities: Command registration, config loading, logging init

## Architectural Constraints

- **Threading:** Goroutine pool in downloader for parallel HTTP downloads; signal goroutine in runtime for graceful shutdown
- **Global state:** `pkg/logger` uses `sync.Once` singleton for zerolog instance (`pkg/logger/logger.go:12`)
- **Circular imports:** None detected; `internal/` packages import `pkg/` only; `internal/cmd` is the top-level orchestrator
- **Concurrency:** Downloader uses `sync.WaitGroup` and channels; runtime uses signal notification
- **Error propagation:** Errors wrapped with `fmt.Errorf("context: %w", err)` and returned up the call stack; no centralized error handling middleware

## Anti-Patterns

### Error swallowed with `_`

**What happens:** `viper.ReadInConfig()` return value is ignored with `_` in `cmd.go:72`
**Why it's wrong:** Config file may be missing; the error is silently ignored, making setup/debugging harder
**Do this instead:** Log a warning or return the error:
```go
if err := viper.ReadInConfig(); err != nil {
    log.Warn().Err(err).Msg("no config file found, using defaults")
}
```
File: `internal/cmd/cmd.go:72`

### Misnamed utility function

**What happens:** `CopyDir()` in `pkg/utils/utils.go` is used for both directory and single-file copy operations (`downloader.go:115`)
**Why it's wrong:** The function name implies directory-only behavior; calling it for a single file copy is confusing and error-prone
**Do this instead:** Rename to `CopyFile()` or use `copyFile()` (the private helper) directly for single files
File: `pkg/utils/utils.go:95`, `internal/downloader/downloader.go:115`

## Error Handling

**Strategy:** Propagate errors up via return values; wrap with context using `fmt.Errorf`

**Patterns:**
- All `RunE` functions return `error` and let Cobra handle exit code
- `viper.AutomaticEnv()` silently ignores missing env vars — no validation layer
- Download errors: required mods fail the whole operation; optional mods log a warning and continue
- Installer subprocess errors are captured and wrapped

## Cross-Cutting Concerns

**Logging:** Zerolog console writer to stderr; global singleton initialized once via `sync.Once`; levels controlled via `--log-level` flag and `log_level` config key

**Validation:** Flag validation via Cobra (`MarkFlagRequired`); no runtime schema validation for config values

**Configuration:** Viper with YAML config file (`~/.config/mcpackctl/config.yaml`), env var overrides with `MCPACKCTL_` prefix, sensible defaults for `ram`, `java_path`, `cache_dir`, `workers`

---

*Architecture analysis: 2026-04-26*