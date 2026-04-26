# Codebase Concerns

**Analysis Date:** 2026-04-26

## Testing Gap

**No test coverage exists:**
- Issue: No test files found in the repository (confirmed by glob pattern `**/*_test.go`)
- Impact: Any code changes risk breaking existing functionality without detection
- Fix approach: Add unit tests for critical components: parser, downloader, cleaner, installer
- Priority: High

## Dependency & External API Risks

### CurseForge API Dependency
- Issue: Hardcoded API endpoint in `internal/downloader/downloader.go:21`
- Files: `internal/downloader/downloader.go`
- Impact: CurseForge API changes (deprecation, rate limits, authentication) will break downloads without warning
- Current mitigation: Falls back to direct CDN pattern when no API key is provided
- Recommendations: Add retry logic with exponential backoff for API errors, consider caching API responses, document required API key

### Direct CDN Fallback is Fragile
- Issue: When no API key is set, uses pattern-based URL construction (`internal/downloader/downloader.go:166-168`)
- Files: `internal/downloader/downloader.go`
- Why it's wrong: Many mods block direct CDN access; downloads will silently fail
- Do this instead: Require API key or fail fast with clear error message

## Security Considerations

### No TLS Certificate Validation Control
- Issue: Uses `http.DefaultClient` without custom transport settings
- Files: `pkg/utils/utils.go`, `internal/downloader/downloader.go`
- Risk: No way to disable certificate validation for corporate proxies; no way to enforce TLS
- Current mitigation: Uses Go's default transport (validates certificates)
- Recommendations: Add configuration option for custom HTTP transport

### Hardcoded Permissions on Files
- Issue: Files created with `0o644` mode (`internal/downloader/downloader.go:226`, `pkg/utils/utils.go:195`)
- Files: `internal/downloader/downloader.go`, `pkg/utils/utils.go`
- Risk: Files are world-readable; sensitive config files in mods directory exposure
- Current mitigation: Default umask may provide some protection
- Recommendations: Use `0o600` for sensitive operations

## Error Handling Weaknesses

### Silent Failure in Cleaner
- Issue: Client-only mod removal failures are logged as warnings but don't fail the operation (`internal/cleaner/cleaner.go:66-68`)
- Files: `internal/cleaner/cleaner.go`
- Why it's wrong: Server may run with incompatible client mods, causing crashes
- Do this instead: Return error or add flag to make client-only mod removal mandatory

### No Rollback on Setup Failure
- Issue: If `setup` command fails partway through, partial server installation remains
- Files: `internal/cmd/cmd.go`
- Impact: User must manually clean up failed setup directory
- Recommendations: Implement cleanup on failure or atomic operations

### Error Chaining Uses %w But Not Always Verbose
- Issue: Many error messages lack context for debugging
- Files: `internal/downloader/downloader.go`, `internal/installer/installer.go`
- Current mitigation: Structured logging available but not consistently used
- Recommendations: Wrap all errors with contextual information

## Threading & Concurrency Concerns

### Workers May Exhaust File Descriptors
- Issue: Concurrent downloads don't have explicit limit on open connections
- Files: `internal/downloader/downloader.go`
- Impact: On systems with low ulimit, may hit "too many open files" errors
- Current mitigation: Worker count limits concurrent operations partially
- Recommendations: Add connection pooling with limits

### Global Logger Singleton
- Issue: Package-level global state in `pkg/logger/logger.go:11-14`
- Files: `pkg/logger/logger.go`
- Why it's wrong: Makes testing difficult, potential race conditions with initialization
- Safe modification: Logger is initialized once via `sync.Once`, generally safe
- Test coverage: Hard to mock for testing

## Performance & Scalability

### No Cache Invalidation
- Issue: Downloaded mods are cached indefinitely (`internal/downloader/downloader.go:108-116`)
- Files: `internal/downloader/downloader.go`
- Impact: Updates to mods are never downloaded unless cache is manually cleared
- Workaround: User must manually delete cache directory
- Recommendations: Add cache expiration or manifest-based cache validation

### Large ZIP Extraction Loads Entire Archive
- Issue: `ExtractZip` in `pkg/utils/utils.go` walks entire archive before extracting
- Files: `pkg/utils/utils.go`
- Impact: Memory usage spikes with very large packs
- Current mitigation: Processes entries in single pass
- Recommendations: Stream extraction for very large packs (though current approach is acceptable for typical modpack sizes)

### No Progress Reporting
- Issue: Long-running operations (download, extract) provide no progress feedback
- Files: `internal/downloader/downloader.go`, `pkg/utils/utils.go`
- Impact: User uncertainty about operation status for large modpacks
- Recommendations: Add progress callbacks or events

## Input Validation Gaps

### No Validation of Download URLs
- Issue: URLs from CurseForge API are used directly without validation
- Files: `internal/downloader/downloader.go`
- Risk: Redirect-based downloads could lead to arbitrary URL manipulation (though unlikely with CurseForge)
- Current mitigation: API response structure limits possibilities
- Recommendations: Validate redirect destinations

### No Validation on Config Values
- Issue: Viper config values used directly without validation (e.g., negative RAM values)
- Files: `internal/cmd/cmd.go`
- Risk: Invalid JVM arguments passed to server
- Current mitigation: Java will reject invalid arguments
- Recommendations: Add explicit validation for RAM format (e.g., `^\d+[GMgm]$`)

## Modpack Detection Limitations

### Simple Detection Logic
- Issue: Pack type detection only checks for two patterns (`internal/detector/detector.go:32-43`)
- Files: `internal/detector/detector.go`
- Why fragile: Won't detect other common formats (ATLauncher, Feed The Beast)
- Do this instead: Support additional pack formats or clearly document supported types
- Test coverage: No tests for edge cases

### Cleaner Patterns May Be Incomplete
- Issue: Client-only mod patterns are hardcoded and may miss newmods
- Files: `internal/cleaner/cleaner.go:12-44`
- Impact: New client-only mods may cause server crashes
- Recommendations: Document that user should verify cleaner results, allow custom patterns via config

## Fragile Areas

### Raw Pack Parsing Relies on Optional Files
- Issue: Raw pack metadata from optional `version.json` or `pack.json` without clear specification
- Files: `internal/parser/parser.go:96-109`
- Why fragile: Silent failures when files are missing or malformed
- Current mitigation: Limited logging when parsing fails
- Test coverage: No tests for malformed input

### Server Properties Are Overwritten
- Issue: `writeServerProperties` skips if file exists, but doesn't merge changes
- Files: `internal/installer/installer.go:131-136`
- Impact: Server properties may retain old configurations
- Current mitigation: Only writes if file doesn't exist
- Do this instead: Merge template with existing properties or clearly warn user

### JVM Memory Arguments Not Tuned
- Issue: Fixed minimum heap (`-Xms512M`) and simple maximum heap
- Files: `internal/runtime/runtime.go:117-124`
- Impact: May not be optimal for all server configurations
- Recommendations: Allow custom JVM arguments via config

## Logging & Observability

### No Structured Error Summary
- Issue: Errors logged individually but no summary at end of operation
- Files: `internal/downloader/downloader.go`
- Impact: Difficult to assess overall operation success/failure
- Recommendations: Add error summary before returning

### No Operation Telemetry
- Issue: No way to track download statistics, success rates, or timing
- Impact: Hard to diagnose user issues without logs
- Recommendations: Add optional anonymous telemetry or structured logs for debugging

## Code Quality Issues

### Inconsistent Error Handling Patterns
- Issue: Some functions return error, others log and continue
- Examples: 
  - `internal/cleaner/cleaner.go:66-68` logs and continues
  - `internal/installer/installer.go:93-94` ignores errors with `_`
- Impact: Unpredictable behavior
- Fix approach: Establish clear error handling policy

### Duplicated Download Logic
- Issue: Retry logic exists in both `pkg/utils/utils.go` and `internal/downloader/downloader.go:201-213`
- Files: `pkg/utils/utils.go`, `internal/downloader/downloader.go`
- Impact: Maintainability and potential for inconsistent retry behavior
- Fix approach: Consolidate to single download utility

---

*Concerns audit: 2026-04-26*