# Coding Conventions

**Analysis Date:** 2026-04-26

## Language

**Go Version:**
- Go 1.24.13 (from `go.mod`)

## Naming Patterns

**Files:**
- snake_case: `downloader.go`, `parser.go`, `utils.go`
- Pattern: Simple lowercase with underscores where needed

**Packages:**
- Simple descriptive nouns: `downloader`, `parser`, `detector`, `cleaner`, `installer`, `runtime`, `cmd`, `logger`, `utils`
- One package per directory (no subpackages)

**Functions:**
- PascalCase exported: `DownloadMods`, `Install`, `Detect`, `ParseCurseForge`
- camelCase private: `runSetup`, `absPath`, `downloadMod`

**Variables:**
- camelCase: `cacheDir`, `apiKey`, `workers`, `downloadURL`
- Acronyms handled case-sensitively: `APIKey`, not `ApiKey`

**Types:**
- PascalCase: `Manifest`, `Downloader`, `Task`, `PackType`
- Descriptive with clear purpose

**Constants:**
- PascalCase or mixed: `defaultWorkers`, `maxRetries`, `forgeInstallerURL`

## Code Style

**Formatting:**
- No explicit formatter config detected (no `.prettierrc`, Makefile or `.golangci.yaml`)
- Standard `go fmt` assumed

**Linting:**
- No `golangci-lint` config detected
- No CI lint checks evident
- Minimal annotations present (e.g., `// #nosec G204` in `installer.go:84` and `runtime.go:40`)

## Import Organization

**Order:**
1. Standard library (e.g., `fmt`, `os`, `path/filepath`, `net/http`, `sync`)
2. External packages (e.g., `github.com/spf13/cobra`, `github.com/rs/zerolog`)
3. Local packages (e.g., `github.com/bhhoang/AutoPackMC/pkg/logger`)

**Grouping:**
- Multiple imports grouped in single `import (...)` block with clear separation

**Path Aliases:**
- Full import paths: `github.com/bhhoang/AutoPackMC/internal/detector`

## Error Handling

**Pattern:**
- Explicit error wrapping with `fmt.Errorf("context: %w", err)` (see `cmd.go:125`, `downloader.go:56`, `parser.go:50`)
- Error messages include context: "extract zip", "detect pack type", "download mods"

**Return Values:**
- Errors returned directly: `return fmt.Errorf(...)` 
- Multi-value returns used: `(string, []string, error)`

**Logging:**
- Errors also logged via zerolog: `log.Warn().Err(err).Msg("...")` (see `cmd.go:190`, `cleaner.go:67`)

## Logging

**Framework:** `github.com/rs/zerolog`

**Initialization:**
- Global singleton via `logger.Init(level)` called in `initConfig()` (`cmd.go:74`)
- Lazy initialization via `logger.Get()` when accessed

**Patterns:**
- Structured logging: `log.Info().Str("key", value).Msg("message")`
- Levels: Debug, Info, Warn, Error
- Output: Console format to stderr with RFC3339 timestamps

## Comments

**When to Comment:**
- Public API functions: `// ParseCurseForge reads and parses manifest.json from dir.`
- Non-obvious logic: `// Serve from cache if available`
- Security notes: `// #nosec G204` for exec.Command usage

**Style:**
- Sentence case, concise
- Explains "what" or "why", not "how"

## Function Design

**Size:**
- Moderate: Functions average 50-100 lines
- Complex logic factored into smaller helpers: `downloadMod()`, `resolveDownloadURL()`, `buildLaunchArgs()`

**Parameters:**
- Named, typed parameters: `func New(cacheDir, apiKey string, workers int) *Downloader`
- Clear purpose

**Return Values:**
- Error as last return value by convention
- Typed returns: `(*Manifest, error)`, `(string, []string, error)`

## Module Design

**Package Structure:**
- `internal/cmd/` - CLI entry and commands
- `internal/downloader/` - Mod download logic
- `internal/parser/` - Manifest parsing
- `internal/detector/` - Pack type detection
- `internal/installer/` - Forge/Fabric installation
- `internal/cleaner/` - Client mod removal
- `internal/runtime/` - Server startup
- `pkg/logger/` - Logging wrapper
- `pkg/utils/` - Shared utilities

**Exports:**
- Exported functions capitalised: `func DownloadMods(...)`, `func Install(...)`
- Unexport internal helpers: `func runSetup(...)`, `func downloadMod(...)`

**Dependencies:**
- Internal packages depend on `pkg/` packages (logger, utils)
- `internal/cmd` orchestrates all other packages

---

*Convention analysis: 2026-04-26*