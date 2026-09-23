package resolver

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testResolver(t *testing.T, h http.HandlerFunc) *Resolver {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	r := New("key")
	r.apiBase = srv.URL
	return r
}

func TestSearchModsFiltersByVersionAndLoader(t *testing.T) {
	var got map[string]string
	r := testResolver(t, func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		got = map[string]string{
			"classId": q.Get("classId"), "gameVersion": q.Get("gameVersion"),
			"modLoaderType": q.Get("modLoaderType"), "searchFilter": q.Get("searchFilter"),
			"sortField": q.Get("sortField"),
		}
		fmt.Fprint(w, `{"data":[{"id":7,"slug":"carry-on","name":"Carry On","summary":"Pick things up.",
			"downloadCount":60000000,"authors":[{"name":"Tschipp"}],"logo":{"thumbnailUrl":"https://x/logo.png"},
			"links":{"websiteUrl":"https://www.curseforge.com/minecraft/mc-mods/carry-on"},
			"latestFiles":[{"gameVersions":["1.20.1","Forge","Client","Server"],"fileDate":"2024-01-01"}]},
			{"id":8,"slug":"xaeros-minimap","name":"Xaero's Minimap",
			"latestFiles":[{"gameVersions":["1.20.1","Forge","Client"],"fileDate":"2024-02-01"},
				{"gameVersions":["1.21","Forge","Client","Server"],"fileDate":"2024-03-01"}]}]}`)
	})

	mods, err := r.SearchMods("carry", "1.20.1", "forge", 10)
	if err != nil {
		t.Fatalf("SearchMods: %v", err)
	}
	want := map[string]string{"classId": "6", "gameVersion": "1.20.1", "modLoaderType": "1", "searchFilter": "carry", "sortField": "2"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("query = %v, want %v", got, want)
	}
	if len(mods) != 2 || mods[0].Name != "Carry On" || mods[0].Author != "Tschipp" || mods[0].LogoURL != "https://x/logo.png" {
		t.Fatalf("mods = %+v", mods)
	}
	if mods[0].ClientOnly || !mods[1].ClientOnly {
		t.Errorf("ClientOnly = %v, %v; want false, true", mods[0].ClientOnly, mods[1].ClientOnly)
	}
}

func TestModFromURL(t *testing.T) {
	r := testResolver(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("slug") != "chunky-pregenerator-forge" {
			t.Errorf("slug = %q", req.URL.Query().Get("slug"))
		}
		fmt.Fprint(w, `{"data":[{"id":1,"slug":"other"},{"id":2,"slug":"chunky-pregenerator-forge","name":"Chunky"}]}`)
	})
	p, err := r.ModFromURL("https://www.curseforge.com/minecraft/mc-mods/chunky-pregenerator-forge/files")
	if err != nil {
		t.Fatalf("ModFromURL: %v", err)
	}
	if p.ID != 2 || p.Name != "Chunky" {
		t.Errorf("project = %+v", p)
	}
	if _, err := r.ModFromURL("https://example.com/mod"); err == nil {
		t.Error("ModFromURL accepted a non-CurseForge link")
	}
}

func TestModFileForPicksNewestMatchingRelease(t *testing.T) {
	r := testResolver(t, func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprint(w, `{"data":[
			{"id":1,"fileName":"old.jar","gameVersions":["1.20.1","Forge"],"releaseType":1,"isAvailable":true,"fileDate":"2024-01-01T00:00:00Z"},
			{"id":2,"fileName":"new.jar","gameVersions":["1.20.1","Forge"],"releaseType":1,"isAvailable":true,"fileDate":"2024-06-01T00:00:00Z"},
			{"id":3,"fileName":"beta.jar","gameVersions":["1.20.1","Forge"],"releaseType":2,"isAvailable":true,"fileDate":"2024-09-01T00:00:00Z"},
			{"id":4,"fileName":"fabric.jar","gameVersions":["1.20.1","Fabric"],"releaseType":1,"isAvailable":true,"fileDate":"2024-10-01T00:00:00Z"},
			{"id":5,"fileName":"gone.jar","gameVersions":["1.20.1","Forge"],"releaseType":1,"isAvailable":false,"fileDate":"2024-11-01T00:00:00Z"}
		]}`)
	})
	f, err := r.ModFileFor(9, "1.20.1", "forge")
	if err != nil {
		t.Fatalf("ModFileFor: %v", err)
	}
	if f.ID != 2 {
		t.Errorf("picked %+v, want new.jar", f)
	}
}

func TestModFileForNoMatch(t *testing.T) {
	r := testResolver(t, func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":4,"fileName":"fabric.jar","gameVersions":["1.20.1","Fabric"],"releaseType":1,"isAvailable":true}]}`)
	})
	if _, err := r.ModFileFor(9, "1.20.1", "forge"); !errors.Is(err, ErrNoCompatibleFile) {
		t.Fatalf("err = %v, want ErrNoCompatibleFile", err)
	}
}

func TestModFileClientOnly(t *testing.T) {
	if !(ModFile{GameVersions: []string{"1.20.1", "Client"}}).ClientOnly() {
		t.Error("Client-only file not detected")
	}
	if (ModFile{GameVersions: []string{"Client", "Server"}}).ClientOnly() {
		t.Error("Client+Server file treated as client-only")
	}
}

func TestModFileRequiredModsAndMod(t *testing.T) {
	r := testResolver(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/mods/9/files":
			fmt.Fprint(w, `{"data":[{"id":1,"fileName":"infinitetrading-1.20.1-5.0.jar","gameVersions":["1.20.1","Forge"],
				"releaseType":1,"isAvailable":true,"fileDate":"2024-01-01",
				"dependencies":[{"modId":342584,"relationType":3},{"modId":7,"relationType":2}]}]}`)
		case "/mods/342584":
			fmt.Fprint(w, `{"data":{"id":342584,"slug":"collective","name":"Collective"}}`)
		default:
			http.NotFound(w, req)
		}
	})
	f, err := r.ModFileFor(9, "1.20.1", "forge")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.RequiredMods(); len(got) != 1 || got[0] != 342584 {
		t.Errorf("RequiredMods = %v, want [342584] (optional dependency skipped)", got)
	}
	p, err := r.Mod(342584)
	if err != nil || p.Name != "Collective" {
		t.Errorf("Mod = %+v, %v", p, err)
	}
}
