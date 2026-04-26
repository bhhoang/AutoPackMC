# Codebase Structure

**Analysis Date:** 2026-04-26

## Directory Layout

```
/run/media/bhhoang/Transcend1/AutoPackMC/
├── cmd/
│   └── mcpackctl/
│       └── main.go          # CLI entrypoint (binary build target)
├── internal/
│   ├── cmd/                 # Cobra commands + workflow orchestration
│   ├── detector/            # Modpack format detection
│   ├── parser/              # manifest.json and raw pack parsing
│   ├── downloader/          # Parallel mod file downloads
│   ├── installer/          # Forge/Fabric installation
│   ├── cleaner/             # Remove client-only mods
│   └── runtime/             # Launch and manage Minecraft server process
├── pkg/
│   ├── logger/              # Zerolog wrapper (global singleton)
│   └── utils/               # File I/O, ZIP, HTTP helpers
├── main.go                  # Alternative entrypoint (go run)
├── go.mod                   # Module definition (Go 1.24.13)
├── go.sum
├── Dockerfile
├── README.md
└── mcpackctl                # Compiled binary (gitignored)
```

## Directory Purposes

**`cmd/mcpackctl/`:**
- Purpose: CLI entrypoint for the compiled binary
- Contains: Single `main.go` that delegates to `internal/cmd`
- Key files: `cmd/mcpackctl/main.go`

**`internal/`:**
- Purpose: All application business logic, organized by responsibility
- Contains: One sub-package per operation (detector, parser, downloader, installer, cleaner, runtime, cmd)
- Key files: `internal/cmd/cmd.go` (main orchestrator)

**`pkg/`:**
- Purpose: Shared, stateless utilities
- Contains: Logger singleton, file/network helpers
- Key files: `pkg/utils/utils.go`, `pkg/logger/logger.go`

## Key File Locations

**Entry Points:**
- `cmd/mcpackctl/main.go`: Binary entrypoint for production builds
- `main.go`: Alternative entrypoint for `go run main.go`

**Configuration:**
- `internal/cmd/cmd.go:49-75`: Viper config initialization (`initConfig()`)
- Config file path: `~/.config/mcpackctl/config.yaml` (default)
- Env var prefix: `MCPACKCTL_` (e.g., `MCPACKCTL_CURSEFORGE_API_KEY`)

**Core Logic:**
- `internal/cmd/cmd.go:99-143`: `runSetup()` — main setup workflow
- `internal/cmd/cmd.go:258-271`: `runStart()` — server startup
- `internal/detector/detector.go:32`: `Detect()` function
- `internal/parser/parser.go:46-77`: `ParseCurseForge()`
- `internal/parser/parser.go:88-111`: `ParseRaw()`
- `internal/downloader/downloader.go:52`: `DownloadMods()`
- `internal/installer/installer.go:35`: `Install()`
- `internal/runtime/runtime.go:19`: `Start()`
- `internal/cleaner/cleaner.go:47`: `Clean()`

**Testing:**
- No test files exist in this repository (per AGENTS.md)

## Naming Conventions

**Files:**
- Go source files: lowercase with underscores (e.g., `detector.go`, `download_manager.go` — but current codebase uses single-word filenames)
- Directories: lowercase with underscores (e.g., `cmd/`, `internal/`)

**Functions:**
- Exported functions: PascalCase (e.g., `Detect()`, `ParseCurseForge()`, `DownloadMods()`)
- Unexported functions: camelCase (e.g., `installForge()`, `buildLaunchArgs()`, `isClientOnly()`)
- Command constructors: `new<Name>Cmd()` (e.g., `newSetupCmd()`, `newStartCmd()`)

**Variables:**
- Local variables: camelCase or short single-letter for simple loops
- Package-level exports: PascalCase
- Unexported package vars: camelCase

**Types:**
- Structs: PascalCase (e.g., `Manifest`, `RawPack`, `Downloader`, `Task`)
- Interfaces: PascalCase (none currently defined)
- Enums as const groups: PascalCase with `iota` (e.g., `PackType` in `detector.go`)

## Where to Add New Code

**New Subcommand (e.g., `mcpackctl validate`):**
- Primary code: `internal/cmd/cmd.go` — add `rootCmd.AddCommand(newValidateCmd())` in `init()`
- Handler function: `runValidate()` in `internal/cmd/cmd.go`
- If complex, consider a new package under `internal/validator/` with `cmd.go` file

**New Modpack Format Support (e.g., Modrinth):**
- New package: `internal/modrinth/` with `downloader.go`, `parser.go`
- Register detection in `internal/detector/detector.go:Detect()`
- Add new `PackTypeModrinth` constant
- Wire in `internal/cmd/cmd.go:runSetup()` switch statement

**New Download Source:**
- Add to `internal/downloader/downloader.go` — extend `resolveDownloadURL()` or add new method
- May require new config env vars in `internal/cmd/cmd.go:initConfig()`

**New Installer (e.g., Quilt):**
- Add function to `internal/installer/installer.go:Install()` switch
- Implement `installQuilt()` in same file or new `installer/quilt.go`

**Utilities:**
- File I/O helpers: `pkg/utils/utils.go`
- Logging helpers: `pkg/logger/logger.go`
- Do not import `internal/` packages from `pkg/`

## Special Directories

**`cmd/mcpackctl/`:**
- Purpose: Binary entrypoint package
- Generated: No (source)
- Committed: Yes

**`pkg/`:**
- Purpose: Reusable utilities meant for external use if project becomes a library
- Generated: No
- Committed: Yes

**`internal/`:**
- Purpose: Application-specific logic; Go visibility rule prevents import by external packages
- Generated: No
- Committed: Yes

---

*Structure analysis: 2026-04-26*