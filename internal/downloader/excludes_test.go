package downloader

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bhhoang/AutoPackMC/internal/parser"
)

const testExcludeList = `{
  "globalExcludes": ["mekalus-oculus-fork-with-fixed-mekanism-mekasuit", "appleskin", "ctm"],
  "modpacks": {
    "some-pack": {"excludes": ["modernfix"], "forceIncludes": ["ctm"]}
  }
}`

var testSlugs = map[int]string{
	1130957: "mekalus-oculus-fork-with-fixed-mekanism-mekasuit",
	238222:  "jei",
	267602:  "ctm",
	790626:  "modernfix",
}

// newExcludeTestServer serves the slug batch endpoint and per-file metadata
// with no Client/Server tags, like the real Mekalus file.
func newExcludeTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/mods" {
			var req struct {
				ModIDs []int `json:"modIds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode slug request: %v", err)
			}
			var data []map[string]any
			for _, id := range req.ModIDs {
				data = append(data, map[string]any{"id": id, "slug": testSlugs[id]})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		var projectID, fileID int
		if _, err := fmt.Sscanf(r.URL.Path, "/mods/%d/files/%d", &projectID, &fileID); err != nil {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"data":{"fileName":"%s.jar","gameVersions":["1.20.1","Forge"]}}`, testSlugs[projectID])
	}))
}

func testManifest() *parser.Manifest {
	m := &parser.Manifest{}
	for id := range testSlugs {
		m.Files = append(m.Files, parser.ModFile{ProjectID: id, FileID: id + 1, Required: true})
	}
	return m
}

func TestExcludedSlugsAppliesPackAdjustments(t *testing.T) {
	list, err := parseExcludeList([]byte(testExcludeList))
	if err != nil {
		t.Fatal(err)
	}

	global, _ := list.resolve("")
	if !global["ctm"] || global["modernfix"] {
		t.Errorf("without a pack slug: ctm excluded=%v, modernfix excluded=%v", global["ctm"], global["modernfix"])
	}

	pack, forced := list.resolve("some-pack")
	if pack["ctm"] || !forced["ctm"] {
		t.Error("ctm is force-included for some-pack but was excluded")
	}
	if !pack["modernfix"] || !pack["mekalus-oculus-fork-with-fixed-mekanism-mekasuit"] {
		t.Error("some-pack should exclude modernfix and the global excludes")
	}
}

// User entries win over the list, and project IDs work like slugs.
func TestUserOverridesTakePrecedence(t *testing.T) {
	list, err := parseExcludeList([]byte(testExcludeList))
	if err != nil {
		t.Fatal(err)
	}
	// The list force-includes ctm for some-pack; the user excludes it.
	// The list excludes mekalus; the user keeps it. jei is excluded by ID.
	list.UserExcludes = []string{"ctm", "238222"}
	list.UserIncludes = []string{"mekalus-oculus-fork-with-fixed-mekanism-mekasuit"}

	srv := newExcludeTestServer(t)
	defer srv.Close()
	d := New(t.TempDir(), "key", 1, true)
	d.siteAPIBase = srv.URL
	d.officialAPIBase = srv.URL

	if _, err := d.ApplyExcludeList(list, testManifest(), "some-pack"); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.excludedSlug(267602); !ok {
		t.Error("ctm should be excluded by the user")
	}
	if slug, ok := d.excludedSlug(238222); !ok || slug != "jei" {
		t.Errorf("jei should be excluded by project ID, got %q, %v", slug, ok)
	}
	if _, ok := d.excludedSlug(1130957); ok || !d.forceIncluded(1130957) {
		t.Error("mekalus should be kept by the user")
	}
}

// A force-included mod survives a Client-only tag on CurseForge.
func TestForceIncludeOverridesClientTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/mods" {
			fmt.Fprint(w, `{"data":[{"id":1,"slug":"mistagged"}]}`)
			return
		}
		fmt.Fprint(w, `{"data":{"fileName":"mistagged.jar","gameVersions":["1.20.1","Client"]}}`)
	}))
	defer srv.Close()

	d := New(t.TempDir(), "key", 1, true)
	d.siteAPIBase = srv.URL
	d.officialAPIBase = srv.URL
	manifest := &parser.Manifest{Files: []parser.ModFile{{ProjectID: 1, FileID: 2}}}

	modsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(modsDir, "mistagged.jar"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	list := &ExcludeList{UserIncludes: []string{"mistagged"}}
	if _, err := d.ApplyExcludeList(list, manifest, ""); err != nil {
		t.Fatal(err)
	}
	removed, err := d.CleanMods(manifest, modsDir)
	if err != nil || len(removed) != 0 {
		t.Errorf("CleanMods removed %v (%v); the force-included mod must stay", removed, err)
	}
}

// TestCleanModsRemovesExcludedUntaggedMod reproduces the Mekalus crash: the
// file has no Client tag, so only the exclude list identifies it.
func TestCleanModsRemovesExcludedUntaggedMod(t *testing.T) {
	srv := newExcludeTestServer(t)
	defer srv.Close()

	d := New(t.TempDir(), "key", 1, true)
	d.siteAPIBase = srv.URL
	d.officialAPIBase = srv.URL

	list, _ := parseExcludeList([]byte(testExcludeList))
	manifest := testManifest()
	n, err := d.ApplyExcludeList(list, manifest, "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 { // mekalus and ctm
		t.Fatalf("excluded %d projects, want 2", n)
	}

	modsDir := t.TempDir()
	for _, slug := range testSlugs {
		if err := os.WriteFile(filepath.Join(modsDir, slug+".jar"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := d.CleanMods(manifest, modsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Errorf("removed %v, want the mekalus and ctm jars", removed)
	}
	for _, kept := range []string{"jei.jar", "modernfix.jar"} {
		if _, err := os.Stat(filepath.Join(modsDir, kept)); err != nil {
			t.Errorf("%s should be kept: %v", kept, err)
		}
	}
	if _, err := os.Stat(filepath.Join(modsDir, "mekalus-oculus-fork-with-fixed-mekanism-mekasuit.jar")); !os.IsNotExist(err) {
		t.Error("mekalus jar should have been removed")
	}
}

func TestDownloadMissingModsSkipsExcluded(t *testing.T) {
	srv := newExcludeTestServer(t)
	defer srv.Close()

	d := New(t.TempDir(), "key", 1, true)
	d.siteAPIBase = srv.URL
	d.officialAPIBase = srv.URL

	list, _ := parseExcludeList([]byte(testExcludeList))
	manifest := &parser.Manifest{Files: []parser.ModFile{{ProjectID: 1130957, FileID: 1, Required: true}}}
	if _, err := d.ApplyExcludeList(list, manifest, ""); err != nil {
		t.Fatal(err)
	}
	// Nothing is downloadable from the test server, so any download attempt
	// for a required mod would fail the call.
	if err := d.DownloadMissingMods(manifest, t.TempDir()); err != nil {
		t.Fatalf("excluded mod was not skipped: %v", err)
	}
}

func TestLoadExcludeListFallsBackToCache(t *testing.T) {
	cacheDir := t.TempDir()

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testExcludeList)
	}))
	if _, err := LoadExcludeList(up.URL, cacheDir); err != nil {
		t.Fatal(err)
	}
	up.Close()

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer down.Close()

	list, err := LoadExcludeList(down.URL, cacheDir)
	if err != nil {
		t.Fatalf("expected cached list, got %v", err)
	}
	if len(list.GlobalExcludes) != 3 {
		t.Errorf("cached list has %d global excludes, want 3", len(list.GlobalExcludes))
	}
}
