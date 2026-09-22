package modstate

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeJars(t *testing.T, dir string, names ...string) {
	t.Helper()
	mods := filepath.Join(dir, "mods")
	if err := os.MkdirAll(mods, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(mods, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func jars(t *testing.T, dir string) []string {
	t.Helper()
	got, err := listJars(filepath.Join(dir, "mods"))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// setup simulates one setup run that installs the given jars.
func setup(t *testing.T, dir string, install []string, setupErr error) {
	t.Helper()
	u, err := Begin(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeJars(t, dir, install...)
	u.Finish(setupErr)
}

func TestUpdateRemovesOutdatedModsKeepsUserMods(t *testing.T) {
	dir := t.TempDir()
	setup(t, dir, []string{"jei-1.0.jar", "create-0.5.jar"}, nil)

	// The user adds a mod of their own between updates.
	writeJars(t, dir, "my-custom-mod.jar")

	// Pack v2 updates JEI and drops Create.
	setup(t, dir, []string{"jei-2.0.jar"}, nil)

	want := []string{"jei-2.0.jar", "my-custom-mod.jar"}
	if got := jars(t, dir); !slices.Equal(got, want) {
		t.Errorf("mods after update = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, stateDir, backupDir)); !os.IsNotExist(err) {
		t.Error("backup folder should be removed after a successful update")
	}

	// The user's mod was never recorded, so the next update keeps it too.
	setup(t, dir, []string{"jei-3.0.jar"}, nil)
	if got := jars(t, dir); !slices.Equal(got, []string{"jei-3.0.jar", "my-custom-mod.jar"}) {
		t.Errorf("mods after second update = %q", got)
	}
}

func TestFailedUpdateRestoresPreviousMods(t *testing.T) {
	dir := t.TempDir()
	setup(t, dir, []string{"jei-1.0.jar", "create-0.5.jar"}, nil)

	writeJars(t, dir, "my-custom-mod.jar")

	// v2 fails after installing one new jar and reinstalling create. Leaving
	// jei-2.0 next to the restored jei-1.0 would load both versions.
	setup(t, dir, []string{"jei-2.0.jar", "create-0.5.jar"}, errors.New("installer failed"))

	want := []string{"create-0.5.jar", "jei-1.0.jar", "my-custom-mod.jar"}
	if got := jars(t, dir); !slices.Equal(got, want) {
		t.Errorf("mods after failed update = %q, want %q", got, want)
	}

	// The record still describes v1, so a retry replaces it cleanly.
	setup(t, dir, []string{"jei-2.0.jar"}, nil)
	if got := jars(t, dir); !slices.Equal(got, []string{"jei-2.0.jar", "my-custom-mod.jar"}) {
		t.Errorf("mods after retried update = %q", got)
	}
}

// A mod the new version installs under the same name is not duplicated or
// deleted.
func TestUnchangedModSurvivesUpdate(t *testing.T) {
	dir := t.TempDir()
	setup(t, dir, []string{"jei-1.0.jar"}, nil)
	setup(t, dir, []string{"jei-1.0.jar"}, nil)
	if got := jars(t, dir); !slices.Equal(got, []string{"jei-1.0.jar"}) {
		t.Errorf("mods = %q", got)
	}
}

func TestRecordCannotEscapeModsDir(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "server.properties")
	if err := os.WriteFile(outside, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeRecord(dir, record{Mods: []string{"../server.properties"}}); err != nil {
		t.Fatal(err)
	}
	u, err := Begin(dir)
	if err != nil {
		t.Fatal(err)
	}
	u.Finish(nil)
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("file outside mods/ was touched: %v", err)
	}
}
