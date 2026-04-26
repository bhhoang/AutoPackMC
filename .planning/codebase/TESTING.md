# Testing Patterns

**Analysis Date:** 2026-04-26

## Test Framework

**Status:** No testing infrastructure detected

**No test files found in codebase:**
- No `*_test.go` files present
- No test configuration files (e.g., `go.sum` has no test dependencies)
- No testing imports in any source files

## Test Configuration

**Not applicable:**
- No `go test` configuration present
- No Makefile or CI test commands

**Run Commands:**
```bash
# No tests currently exist
go test ./...
```

## Test File Organization

**Location:** Not applicable

**Naming:** Not applicable

**Note:** Codebase currently lacks test infrastructure. Tests should be added for:
- Parser functions (`ParseCurseForge`, `ParseRaw`)
- Detector (`Detect`)
- Cleaner (`Clean`)
- Downloader (`DownloadMods`)
- Installer (`Install`)
- Runtime (`Start`)

## Test Patterns to Implement

**When adding tests, use these patterns:**

### Unit Tests
```go
package parser

func TestParseCurseForge(t *testing.T) {
    // Create temp manifest.json
    // Call ParseCurseForge
    // Assert expected values
}
```

### Test Fixtures
```go
func testManifestJSON() []byte {
    return []byte(`{
        "minecraft": {"version": "1.20.1"},
        "manifestType": "modpack",
        "name": "TestPack",
        "version": "1.0.0",
        "files": []
    }`)
}
```

### File-Based Tests
- Create temporary directories with `t.TempDir()` for file parsing tests
- Use embed or inline test data for manifest fixtures

### Mocking HTTP
- Use `net/http/httptest` for HTTP client mocking
- Example: `httptest.NewServer(http.HandlerFunc(...))`

## Coverage

**Requirements:** Not defined (no tests exist)

**Target recommendation:** 70%+ for core business logic:
- `internal/parser/` - manifest parsing
- `internal/detector/` - pack type detection
- `internal/cleaner/` - client-only mod filtering

---

*Testing analysis: 2026-04-26*