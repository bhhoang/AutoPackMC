package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeUI records events and answers dialogs with fixed paths.
type fakeUI struct {
	mu     sync.Mutex
	events []string
	setup  []SetupEvent
	files  []string
	opened []string
}

func (u *fakeUI) Emit(event string, data any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.events = append(u.events, event)
	if ev, ok := data.(SetupEvent); ok {
		u.setup = append(u.setup, ev)
	}
}
func (u *fakeUI) PickFolder(string, string) (string, error) { return "", nil }
func (u *fakeUI) PickFiles(string, string, bool) ([]string, error) {
	return u.files, nil
}
func (u *fakeUI) Open(target string) { u.opened = append(u.opened, target) }

func (u *fakeUI) lastSetup() (SetupEvent, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.setup) == 0 {
		return SetupEvent{}, false
	}
	return u.setup[len(u.setup)-1], true
}

func newTestService(t *testing.T) (*Service, *fakeUI) {
	t.Helper()
	ui := &fakeUI{}
	dir := t.TempDir()
	s, err := New(Config{
		DataDir:           filepath.Join(dir, "data"),
		CacheDir:          filepath.Join(dir, "cache"),
		DefaultServersDir: filepath.Join(dir, "servers"),
	}, ui)
	if err != nil {
		t.Fatal(err)
	}
	return s, ui
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// addServer registers a server whose folder holds the given mod files.
func addServer(t *testing.T, s *Service, files ...string) ServerRecord {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		writeFile(t, filepath.Join(dir, "mods", f), "jar")
	}
	rec, err := s.store.UpdateServer("pack", func(r *ServerRecord) {
		r.Name, r.Dir, r.MC, r.Loader = "Pack", dir, "1.20.1", "forge"
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestSetupRawPackCreatesServer(t *testing.T) {
	s, ui := newTestService(t)
	pack := t.TempDir()
	writeFile(t, filepath.Join(pack, "mods", "create-0.5.jar"), "jar")
	writeFile(t, filepath.Join(pack, "mods", "journeymap-5.9.jar"), "jar")
	out := filepath.Join(t.TempDir(), "My Pack")

	if err := s.StartSetup(SetupRequest{Input: pack, Dir: out, RAMGB: 6}); err == nil {
		t.Fatal("setup started without accepting the EULA")
	}
	if err := s.StartSetup(SetupRequest{Input: pack, Dir: out, RAMGB: 6, AcceptEULA: true, Java: "auto"}); err != nil {
		t.Fatalf("StartSetup: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if ev, ok := ui.lastSetup(); ok && (ev.Phase == "done" || ev.Phase == "error") {
			if ev.Phase == "error" {
				t.Fatalf("setup failed: %+v", ev.Error)
			}
			if ev.Server == nil || ev.Server.ModsOnServer != 1 || ev.LeftOff != 1 {
				t.Fatalf("done event = %+v, server %+v", ev, ev.Server)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("setup did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}

	servers := s.Servers()
	if len(servers) != 1 || servers[0].Dir != out || servers[0].RAMGB != 6 || servers[0].State != StateStopped {
		t.Fatalf("servers = %+v", servers)
	}
	mods, err := s.Mods(servers[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprint(mods)
	want := fmt.Sprint([]ModView{
		{FileName: "create-0.5.jar", Name: "create", State: "on"},
		{FileName: "journeymap-5.9.jar", Name: "journeymap", State: "off", Reason: "client"},
	})
	if got != want {
		t.Errorf("mods = %s, want %s", got, want)
	}

	// The saved list survives a restart of the app.
	again, err := New(s.cfg, ui)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(again.Servers()); n != 1 {
		t.Errorf("reloaded %d servers, want 1", n)
	}
}

func TestModOffOnAndRemove(t *testing.T) {
	s, _ := newTestService(t)
	rec := addServer(t, s, "FarmersDelight-1.20.1-1.2.4.jar")
	modsDir := filepath.Join(rec.Dir, "mods")

	if err := s.SetModOff(rec.ID, "FarmersDelight-1.20.1-1.2.4.jar"); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(modsDir, "FarmersDelight-1.20.1-1.2.4.jar.disabled")) {
		t.Fatal("jar was not turned off")
	}
	mods, _ := s.Mods(rec.ID)
	if len(mods) != 1 || mods[0].State != "off" || mods[0].Reason != "you" || mods[0].Name != "Farmers Delight" {
		t.Fatalf("mods = %+v", mods)
	}

	// An update downloads the jar again; the user's choice wins.
	writeFile(t, filepath.Join(modsDir, "FarmersDelight-1.20.1-1.2.4.jar"), "jar")
	reapplyDisabled(modsDir, map[string]bool{})
	if exists(filepath.Join(modsDir, "FarmersDelight-1.20.1-1.2.4.jar")) {
		t.Fatal("update turned a disabled mod back on")
	}

	if err := s.SetModOn(rec.ID, "FarmersDelight-1.20.1-1.2.4.jar"); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(modsDir, "FarmersDelight-1.20.1-1.2.4.jar")) {
		t.Fatal("jar was not turned back on")
	}
}

func TestAddJarFiles(t *testing.T) {
	s, ui := newTestService(t)
	rec := addServer(t, s)
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "Chunky-1.3.146.jar"), "jar")
	writeFile(t, filepath.Join(src, "notes.txt"), "text")
	ui.files = []string{filepath.Join(src, "Chunky-1.3.146.jar"), filepath.Join(src, "notes.txt")}

	skipped, err := s.PickJarFiles(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(skipped, []string{"notes.txt"}) {
		t.Errorf("skipped = %v", skipped)
	}
	mods, _ := s.Mods(rec.ID)
	if len(mods) != 1 || mods[0].State != "mine" || mods[0].Name != "Chunky" {
		t.Fatalf("mods = %+v", mods)
	}
	if _, err := s.RemoveMod(rec.ID, "Chunky-1.3.146.jar"); err != nil {
		t.Fatal(err)
	}
	if mods, _ := s.Mods(rec.ID); len(mods) != 0 {
		t.Errorf("mods after remove = %+v", mods)
	}
}

func TestModFileNamesCannotLeaveModsFolder(t *testing.T) {
	s, _ := newTestService(t)
	rec := addServer(t, s)
	writeFile(t, filepath.Join(rec.Dir, "server.properties"), "x")
	for _, name := range []string{`..\server.properties`, "../server.properties", "..", ""} {
		if _, err := s.RemoveMod(rec.ID, name); err == nil {
			t.Errorf("RemoveMod(%q) was allowed", name)
		}
		if err := s.SetModOff(rec.ID, name); err == nil {
			t.Errorf("SetModOff(%q) was allowed", name)
		}
	}
	if !exists(filepath.Join(rec.Dir, "server.properties")) {
		t.Fatal("a file outside mods/ was removed")
	}
}

func TestModName(t *testing.T) {
	cases := map[string]string{
		"FarmersDelight-1.20.1-1.2.4.jar":          "Farmers Delight",
		"waystones-forge-1.20.1-14.1.3.jar":        "waystones",
		"sophisticatedbackpacks-1.20.1-3.20.5.jar": "sophisticatedbackpacks",
		"Xaeros_Minimap_24.2.0_Forge_1.20.jar":     "Xaeros Minimap",
		"oculus-mc1.20.1-1.7.0.jar":                "oculus",
		"create-1.20.1-0.5.1.f.jar":                "create",
		"1.20.1-something.jar":                     "1.20.1 something",
	}
	for file, want := range cases {
		if got := modName(file); got != want {
			t.Errorf("modName(%q) = %q, want %q", file, got, want)
		}
	}
}

func TestWatchLineTracksReadyAndPlayers(t *testing.T) {
	s, ui := newTestService(t)
	rec := addServer(t, s)
	s.states[rec.ID] = &stateView{state: StateStarting}

	for _, l := range []string{
		`[12:04:53] [Server thread/INFO] [minecraft/DedicatedServer]: Done (41.8s)! For help, type "help"`,
		`[12:06:20] [Server thread/INFO] [minecraft/MinecraftServer]: Nguyet joined the game`,
		`[12:09:41] [Server thread/INFO] [minecraft/MinecraftServer]: tobias_k joined the game`,
		`[12:30:02] [Server thread/INFO] [minecraft/MinecraftServer]: Nguyet left the game`,
	} {
		s.watchLine(rec.ID, l)
	}
	v := s.view(rec)
	if v.State != StateRunning || !slices.Equal(v.Players, []string{"tobias_k"}) {
		t.Fatalf("view = state %s players %v", v.State, v.Players)
	}
	if len(ui.events) == 0 {
		t.Error("no server events emitted")
	}
}

func TestLineWriterBatchesLines(t *testing.T) {
	var mu sync.Mutex
	var got []string
	w := newLineWriter(func(b []string) {
		mu.Lock()
		got = append(got, b...)
		mu.Unlock()
	})
	fmt.Fprint(w, "first\r\nsec")
	fmt.Fprint(w, "ond\n\x1b[32mgreen\x1b[0m\npartial")
	w.Close()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"first", "second", "green", "partial"}; !slices.Equal(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

func TestLogRingKeepsLatest(t *testing.T) {
	r := newLogRing(3)
	r.add("a", "b")
	if !slices.Equal(r.lines(), []string{"a", "b"}) {
		t.Fatalf("lines = %v", r.lines())
	}
	r.add("c", "d", "e")
	if !slices.Equal(r.lines(), []string{"c", "d", "e"}) {
		t.Fatalf("lines = %v", r.lines())
	}
}

func TestUserError(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{context.Canceled, "cancelled"},
		{fmt.Errorf("download: %w", &net.DNSError{Err: "no such host", Name: "api.curseforge.com"}), "network"},
		{&Error{Code: "eula"}, "eula"},
		{errors.New("prepare java: exec: \"java\": not found"), "java"},
		{errors.New("detect pack type: cannot detect pack type in \"x\""), "not_a_pack"},
		{errors.New("something odd"), "unknown"},
	}
	for _, c := range cases {
		if got := userError(c.err).Code; got != c.code {
			t.Errorf("userError(%v) = %s, want %s", c.err, got, c.code)
		}
	}
}

func TestSuggestServerDirAvoidsTakenFolders(t *testing.T) {
	s, _ := newTestService(t)
	first := s.SuggestServerDir(`All the Mods: 10?`)
	if filepath.Base(first) != "All the Mods  10" {
		t.Errorf("first = %q", first)
	}
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	if second := s.SuggestServerDir(`All the Mods: 10?`); second == first {
		t.Errorf("suggested a taken folder %q", second)
	}
}

func TestSuggestRAMGB(t *testing.T) {
	for total, want := range map[int]int{0: 6, 8: 4, 12: 4, 16: 6, 24: 8, 32: 8, 64: 8} {
		if got := suggestRAMGB(total); got != want {
			t.Errorf("suggestRAMGB(%d) = %d, want %d", total, got, want)
		}
	}
}

func TestReapplyDisabledFollowsNewVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "journeymap-1.20.1-5.9.18.jar.disabled"), "old")
	writeFile(t, filepath.Join(dir, "create-0.5.jar"), "jar")
	before := jarNames(dir)
	writeFile(t, filepath.Join(dir, "journeymap-1.20.1-5.10.0.jar"), "new")

	reapplyDisabled(dir, before)

	if exists(filepath.Join(dir, "journeymap-1.20.1-5.10.0.jar")) {
		t.Error("the new version of a turned-off mod is loaded")
	}
	if !exists(filepath.Join(dir, "journeymap-1.20.1-5.10.0.jar.disabled")) {
		t.Error("the new version was not turned off")
	}
	if exists(filepath.Join(dir, "journeymap-1.20.1-5.9.18.jar.disabled")) {
		t.Error("the stale turned-off file was left behind")
	}
	if !exists(filepath.Join(dir, "create-0.5.jar")) {
		t.Error("an unrelated mod was touched")
	}
}

func TestModChangesRefusedWhileRunning(t *testing.T) {
	s, _ := newTestService(t)
	rec := addServer(t, s, "create-0.5.jar")
	// Launched but not yet recorded as stopped; the channel is closed as
	// StartServer does once Launch returns.
	launched := make(chan struct{})
	close(launched)
	s.running[rec.ID] = &running{launched: launched}

	for name, err := range map[string]error{
		"SetModOff": s.SetModOff(rec.ID, "create-0.5.jar"),
		"RemoveMod": func() error { _, err := s.RemoveMod(rec.ID, "create-0.5.jar"); return err }(),
	} {
		var ue *Error
		if !errors.As(err, &ue) || ue.Code != "stop_first" {
			t.Errorf("%s while running = %v, want stop_first", name, err)
		}
	}
	// Stopping a server that is still launching must not block or panic.
	if err := s.StopServer(rec.ID); err != nil {
		t.Errorf("StopServer while launching = %v", err)
	}
	if err := s.SendCommand(rec.ID, "list"); err == nil {
		t.Error("SendCommand to a launching server succeeded")
	}
}

func TestStoreCopiesDoNotShareSlices(t *testing.T) {
	s, _ := newTestService(t)
	rec := addServer(t, s)
	if _, err := s.store.UpdateServer(rec.ID, func(r *ServerRecord) {
		r.Added = []Added{{FileName: "a.jar"}, {FileName: "b.jar"}}
	}); err != nil {
		t.Fatal(err)
	}
	copy1, _ := s.store.Server(rec.ID)
	if _, err := s.store.UpdateServer(rec.ID, func(r *ServerRecord) {
		kept := r.Added[:0]
		for _, a := range r.Added {
			if a.FileName != "a.jar" {
				kept = append(kept, a)
			}
		}
		r.Added = kept
	}); err != nil {
		t.Fatal(err)
	}
	if len(copy1.Added) != 2 || copy1.Added[0].FileName != "a.jar" {
		t.Errorf("an earlier copy changed underneath its reader: %+v", copy1.Added)
	}
}

func TestReapplyDisabledLeavesOldAndAmbiguousJars(t *testing.T) {
	dir := t.TempDir()
	// The user's own jar was already there; an update does not touch it.
	writeFile(t, filepath.Join(dir, "journeymap-addon-1.0.jar"), "mine")
	writeFile(t, filepath.Join(dir, "journeymap-5.9.jar.disabled"), "old")
	before := jarNames(dir)
	// Two new jars guess the same name: too ambiguous to follow.
	writeFile(t, filepath.Join(dir, "journeymap-6.0.jar"), "a")
	writeFile(t, filepath.Join(dir, "journeymap_6.1.jar"), "b")

	reapplyDisabled(dir, before)

	for _, f := range []string{"journeymap-addon-1.0.jar", "journeymap-6.0.jar", "journeymap_6.1.jar", "journeymap-5.9.jar.disabled"} {
		if !exists(filepath.Join(dir, f)) {
			t.Errorf("%s was changed", f)
		}
	}
}

func TestStopServerWaitsForLaunch(t *testing.T) {
	s, _ := newTestService(t)
	rec := addServer(t, s)
	slot := &running{launched: make(chan struct{})}
	s.running[rec.ID] = slot

	done := make(chan error)
	go func() { done <- s.StopServer(rec.ID) }()
	select {
	case <-done:
		t.Fatal("StopServer returned before the launch finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(slot.launched) // launch failed: no server to stop
	if err := <-done; err != nil {
		t.Fatalf("StopServer = %v", err)
	}
	if !slot.stopping {
		t.Error("slot not marked as stopping")
	}
}
