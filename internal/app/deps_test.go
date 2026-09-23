package app

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fakeMod is a CurseForge project in the fake API: a name, the file made for
// Minecraft 1.20.1 with Forge (none when noFile) and its required mods.
type fakeMod struct {
	name     string
	requires []int
	noFile   bool
}

var fakeCatalog = map[int]fakeMod{
	1:  {name: "Infinite Trading", requires: []int{2}},
	2:  {name: "Collective"},
	10: {name: "Villager Names", requires: []int{2}},
	20: {name: "Broken Mod", requires: []int{21}},
	21: {name: "Fabric Only Lib", noFile: true},
	30: {name: "Pack Addon", requires: []int{99}},
	40: {name: "Chain Top", requires: []int{41}},
	41: {name: "Chain Middle", requires: []int{42}},
	42: {name: "Chain Bottom", requires: []int{40}}, // a loop back to the top
}

func fakeFileName(id int) string { return fmt.Sprintf("mod%d-1.20.1.jar", id) }

// depsService is a service whose CurseForge API is fakeCatalog and whose
// downloads write the jar straight into mods/.
func depsService(t *testing.T, failFor int) (*Service, ServerRecord) {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 2 || parts[0] != "mods" {
			http.NotFound(w, r)
			return
		}
		id, _ := strconv.Atoi(parts[1])
		m, ok := fakeCatalog[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if len(parts) == 2 {
			fmt.Fprintf(w, `{"data":{"id":%d,"name":%q}}`, id, m.name)
			return
		}
		if m.noFile {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		var deps []string
		for _, d := range m.requires {
			deps = append(deps, fmt.Sprintf(`{"modId":%d,"relationType":3}`, d))
		}
		fmt.Fprintf(w, `{"data":[{"id":%d,"fileName":%q,"gameVersions":["1.20.1","Forge","Server"],"releaseType":1,
			"isAvailable":true,"fileDate":"2024-01-01","dependencies":[%s]}]}`, id*100, fakeFileName(id), strings.Join(deps, ","))
	}))
	t.Cleanup(api.Close)

	s, _ := newTestService(t)
	s.cfg.CurseForgeAPI = api.URL
	s.downloadMod = func(projectID, fileID int, dir string) error {
		if projectID == failFor {
			return errors.New("download failed")
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, fakeFileName(projectID)), []byte("jar"), 0o644)
	}
	rec, err := s.store.UpdateServer("pack", func(r *ServerRecord) {
		r.Name, r.Dir, r.MC, r.Loader = "Pack", t.TempDir(), "1.20.1", "forge"
		r.ProjectIDs = []int{99}
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, rec
}

func jarsIn(t *testing.T, rec ServerRecord) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(rec.Dir, "mods"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestAddModBringsItsDependencies(t *testing.T) {
	s, rec := depsService(t, 0)

	r, err := s.AddMod(rec.ID, 1, "Infinite Trading", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "added" || !slices.Equal(r.Deps, []string{"Collective"}) {
		t.Fatalf("result = %+v", r)
	}
	if got := jarsIn(t, rec); !slices.Equal(got, []string{fakeFileName(1), fakeFileName(2)}) {
		t.Fatalf("mods/ = %v", got)
	}
	mods, _ := s.Mods(rec.ID)
	var collective ModView
	for _, m := range mods {
		if m.ProjectID == 2 {
			collective = m
		}
	}
	if collective.Reason != "dep" || !slices.Equal(collective.NeededBy, []string{"Infinite Trading"}) {
		t.Errorf("Collective shown as %+v", collective)
	}

	// A second mod needing Collective shares it instead of downloading it again.
	r, err = s.AddMod(rec.ID, 10, "Villager Names", false)
	if err != nil || r.Status != "added" || len(r.Deps) != 0 {
		t.Fatalf("second add = %+v, %v", r, err)
	}

	// Collective stays while anything still needs it.
	gone, err := s.RemoveMod(rec.ID, fakeFileName(1))
	if err != nil || len(gone) != 0 {
		t.Fatalf("remove Infinite Trading: gone %v, err %v", gone, err)
	}
	if !exists(filepath.Join(rec.Dir, "mods", fakeFileName(2))) {
		t.Fatal("Collective removed while Villager Names still needs it")
	}
	gone, err = s.RemoveMod(rec.ID, fakeFileName(10))
	if err != nil || !slices.Equal(gone, []string{"Collective"}) {
		t.Fatalf("remove Villager Names: gone %v, err %v", gone, err)
	}
	if got := jarsIn(t, rec); len(got) != 0 {
		t.Errorf("mods/ after removing everything = %v", got)
	}
	if left := mustServer(t, s, rec.ID).Added; len(left) != 0 {
		t.Errorf("added list after removing everything = %+v", left)
	}
}

func TestAddModWithMissingDependencyAddsNothing(t *testing.T) {
	s, rec := depsService(t, 0)
	r, err := s.AddMod(rec.ID, 20, "Broken Mod", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "dep_missing" || r.Missing != "Fabric Only Lib" {
		t.Fatalf("result = %+v", r)
	}
	if got := jarsIn(t, rec); len(got) != 0 {
		t.Errorf("mods/ = %v, want nothing added", got)
	}
}

func TestAddModSkipsDependenciesInThePack(t *testing.T) {
	s, rec := depsService(t, 0)
	r, err := s.AddMod(rec.ID, 30, "Pack Addon", false)
	if err != nil || r.Status != "added" || len(r.Deps) != 0 {
		t.Fatalf("result = %+v, %v", r, err)
	}
	if got := jarsIn(t, rec); !slices.Equal(got, []string{fakeFileName(30)}) {
		t.Errorf("mods/ = %v", got)
	}
}

func TestAddModRollsBackWhenADependencyFails(t *testing.T) {
	s, rec := depsService(t, 2)
	if _, err := s.AddMod(rec.ID, 1, "Infinite Trading", false); err == nil {
		t.Fatal("AddMod succeeded although Collective failed to download")
	}
	if got := jarsIn(t, rec); len(got) != 0 {
		t.Errorf("mods/ = %v, want the half-added mod removed", got)
	}
	if left := mustServer(t, s, rec.ID).Added; len(left) != 0 {
		t.Errorf("added list = %+v", left)
	}
}

func TestAddModHandlesDependencyLoops(t *testing.T) {
	s, rec := depsService(t, 0)
	r, err := s.AddMod(rec.ID, 40, "Chain Top", false)
	if err != nil || r.Status != "added" || len(r.Deps) != 2 {
		t.Fatalf("result = %+v, %v", r, err)
	}
	gone, err := s.RemoveMod(rec.ID, fakeFileName(40))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(gone)
	if !slices.Equal(gone, []string{"Chain Bottom", "Chain Middle"}) {
		t.Errorf("gone = %v", gone)
	}
}

func TestAddingADependencyByNameKeepsIt(t *testing.T) {
	s, rec := depsService(t, 0)
	if _, err := s.AddMod(rec.ID, 1, "Infinite Trading", false); err != nil {
		t.Fatal(err)
	}
	r, err := s.AddMod(rec.ID, 2, "Collective", false)
	if err != nil || r.Status != "already" {
		t.Fatalf("result = %+v, %v", r, err)
	}
	gone, err := s.RemoveMod(rec.ID, fakeFileName(1))
	if err != nil || len(gone) != 0 {
		t.Fatalf("gone %v, err %v", gone, err)
	}
	if !exists(filepath.Join(rec.Dir, "mods", fakeFileName(2))) {
		t.Error("Collective was removed although it was added by name")
	}
}
