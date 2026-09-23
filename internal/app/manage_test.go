package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runSetup starts a setup and waits for its done event.
func runSetupAndWait(t *testing.T, s *Service, ui *fakeUI, req SetupRequest) {
	t.Helper()
	ui.mu.Lock()
	ui.setup = nil
	ui.mu.Unlock()
	if err := s.StartSetup(req); err != nil {
		t.Fatalf("StartSetup: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if ev, ok := ui.lastSetup(); ok && (ev.Phase == "done" || ev.Phase == "error") {
			if ev.Phase == "error" {
				t.Fatalf("setup failed: %+v", ev.Error)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("setup did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRenameServer(t *testing.T) {
	s, _ := newTestService(t)
	rec := addServer(t, s)

	v, err := s.RenameServer(rec.ID, "   Friday   night\tpack  ")
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "Friday night pack" {
		t.Fatalf("name = %q", v.Name)
	}
	if _, err := s.RenameServer(rec.ID, "   "); err == nil {
		t.Fatal("renamed to nothing")
	} else {
		wantCode(t, err, "bad_name")
	}
	long := strings.Repeat("á", 100)
	if v, err := s.RenameServer(rec.ID, long); err != nil || len([]rune(v.Name)) != maxServerName {
		t.Fatalf("long name: %q, %v", v.Name, err)
	}
	if _, err := s.RenameServer("nope", "x"); err == nil {
		t.Fatal("renamed a server that does not exist")
	}
}

func TestPackUpdateKeepsAChosenName(t *testing.T) {
	s, ui := newTestService(t)
	pack := t.TempDir()
	writeFile(t, filepath.Join(pack, "mods", "create-0.5.jar"), "jar")
	out := filepath.Join(t.TempDir(), "My Pack")
	runSetupAndWait(t, s, ui, SetupRequest{Input: pack, Dir: out, AcceptEULA: true, Java: "auto"})
	id := s.Servers()[0].ID

	if _, err := s.RenameServer(id, "Our world"); err != nil {
		t.Fatal(err)
	}
	runSetupAndWait(t, s, ui, SetupRequest{ServerID: id, Input: pack, AcceptEULA: true, Java: "auto"})
	if got := s.Servers()[0].Name; got != "Our world" {
		t.Fatalf("after the update the name is %q", got)
	}
}

func TestMoveServer(t *testing.T) {
	s, _ := newTestService(t)
	rec := addServer(t, s, "a.jar")
	java := filepath.Join(rec.Dir, "jdk-17", "bin", "java.exe")
	writeFile(t, java, "java")
	if _, err := s.store.UpdateServer(rec.ID, func(r *ServerRecord) { r.JavaPath = java }); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()

	v, err := s.MoveServer(rec.ID, dest)
	if err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(dest, filepath.Base(rec.Dir))
	if !samePath(v.Dir, to) {
		t.Fatalf("dir = %s, want %s", v.Dir, to)
	}
	if _, err := os.Stat(filepath.Join(to, "mods", "a.jar")); err != nil {
		t.Fatalf("files did not move: %v", err)
	}
	if _, err := os.Stat(rec.Dir); !os.IsNotExist(err) {
		t.Fatalf("old folder still there: %v", err)
	}
	got, _ := s.store.Server(rec.ID)
	if !samePath(got.JavaPath, filepath.Join(to, "jdk-17", "bin", "java.exe")) {
		t.Fatalf("java path = %s", got.JavaPath)
	}
}

func TestMoveServerRefuses(t *testing.T) {
	s, _ := newTestService(t)
	rec := addServer(t, s, "a.jar")

	taken := t.TempDir()
	if err := os.Mkdir(filepath.Join(taken, filepath.Base(rec.Dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := s.MoveServer(rec.ID, taken)
	wantCode(t, err, "folder_exists")

	_, err = s.MoveServer(rec.ID, filepath.Join(rec.Dir, "mods"))
	wantCode(t, err, "move_into_itself")

	s.running[rec.ID] = &running{}
	_, err = s.MoveServer(rec.ID, t.TempDir())
	wantCode(t, err, "stop_first")
	delete(s.running, rec.ID)

	if got, _ := s.store.Server(rec.ID); !samePath(got.Dir, rec.Dir) {
		t.Fatalf("dir changed to %s", got.Dir)
	}
}

func TestMoveServerThatFailsStaysPut(t *testing.T) {
	s, _ := newTestService(t)
	s.move = func(from, to string) error { return &Error{Code: "move_cancelled"} }
	rec := addServer(t, s, "a.jar")

	_, err := s.MoveServer(rec.ID, t.TempDir())
	var ue *Error
	if !errors.As(err, &ue) || ue.Code != "move_cancelled" {
		t.Fatalf("err = %v", err)
	}
	if got, _ := s.store.Server(rec.ID); !samePath(got.Dir, rec.Dir) {
		t.Fatalf("dir changed to %s", got.Dir)
	}
}
