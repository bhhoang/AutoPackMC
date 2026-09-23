package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeTrash records the folders RemoveServer moves to the Recycle Bin.
func fakeTrash(s *Service, fail error) *[]string {
	var moved []string
	s.trash = func(dir string) error {
		if fail != nil {
			return fail
		}
		moved = append(moved, dir)
		return os.RemoveAll(dir)
	}
	return &moved
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var ue *Error
	if !errors.As(err, &ue) || ue.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

func TestRemoveServerKeepsTheFolder(t *testing.T) {
	s, _ := newTestService(t)
	moved := fakeTrash(s, nil)
	rec := addServer(t, s, "a.jar")

	if err := s.RemoveServer(rec.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.store.Server(rec.ID); ok {
		t.Fatal("server still listed")
	}
	if len(*moved) != 0 {
		t.Fatalf("moved %v", *moved)
	}
	if _, err := os.Stat(filepath.Join(rec.Dir, "mods", "a.jar")); err != nil {
		t.Fatalf("files were touched: %v", err)
	}
}

func TestRemoveServerMovesTheFolder(t *testing.T) {
	s, _ := newTestService(t)
	moved := fakeTrash(s, nil)
	rec := addServer(t, s, "a.jar")

	if err := s.RemoveServer(rec.ID, true); err != nil {
		t.Fatal(err)
	}
	if len(*moved) != 1 || !samePath((*moved)[0], rec.Dir) {
		t.Fatalf("moved %v, want %s", *moved, rec.Dir)
	}
	if _, ok := s.store.Server(rec.ID); ok {
		t.Fatal("server still listed")
	}
}

func TestRemoveServerWhileRunning(t *testing.T) {
	s, _ := newTestService(t)
	fakeTrash(s, nil)
	rec := addServer(t, s)
	s.running[rec.ID] = &running{}

	wantCode(t, s.RemoveServer(rec.ID, true), "stop_first")
	if _, ok := s.store.Server(rec.ID); !ok {
		t.Fatal("running server was removed")
	}
}

func TestRemoveServerRefusesFoldersThatAreNotServers(t *testing.T) {
	s, _ := newTestService(t)
	moved := fakeTrash(s, nil)
	cases := map[string]func(t *testing.T) string{
		"no server files": func(t *testing.T) string {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "notes.txt"), "mine")
			return dir
		},
		"the folder for new servers": func(t *testing.T) string {
			dir := s.cfg.DefaultServersDir
			writeFile(t, filepath.Join(dir, "server.properties"), "")
			return dir
		},
		"a folder holding the folder for new servers": func(t *testing.T) string {
			dir := filepath.Dir(s.cfg.DefaultServersDir)
			writeFile(t, filepath.Join(dir, "server.properties"), "")
			return dir
		},
		"a folder holding another server": func(t *testing.T) string {
			other := addServer(t, s, "b.jar")
			dir := filepath.Dir(other.Dir)
			writeFile(t, filepath.Join(dir, "mods", "c.jar"), "jar")
			return dir
		},
	}
	for name, folder := range cases {
		t.Run(name, func(t *testing.T) {
			dir := folder(t)
			rec, err := s.store.UpdateServer("x-"+name, func(r *ServerRecord) { r.Name, r.Dir = name, dir })
			if err != nil {
				t.Fatal(err)
			}
			wantCode(t, s.RemoveServer(rec.ID, true), "not_server_folder")
			if _, ok := s.store.Server(rec.ID); !ok {
				t.Fatal("server was removed from the list")
			}
			if len(*moved) != 0 {
				t.Fatalf("moved %v", *moved)
			}
			// Taking it off the list alone is still fine.
			if err := s.RemoveServer(rec.ID, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRemoveServerKeepsItListedWhenTheMoveFails(t *testing.T) {
	s, _ := newTestService(t)
	fakeTrash(s, &Error{Code: "trash_cancelled"})
	rec := addServer(t, s, "a.jar")

	wantCode(t, s.RemoveServer(rec.ID, true), "trash_cancelled")
	if _, ok := s.store.Server(rec.ID); !ok {
		t.Fatal("server was removed although its folder was not")
	}
}

func TestRemoveServerWithAMissingFolder(t *testing.T) {
	s, _ := newTestService(t)
	moved := fakeTrash(s, nil)
	rec := addServer(t, s)
	if err := os.RemoveAll(rec.Dir); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveServer(rec.ID, true); err != nil {
		t.Fatal(err)
	}
	if len(*moved) != 0 {
		t.Fatalf("moved %v", *moved)
	}
}

func TestStartServerWhileBeingRemoved(t *testing.T) {
	s, _ := newTestService(t)
	rec := addServer(t, s)
	s.removing[rec.ID] = true
	wantCode(t, s.StartServer(rec.ID), "no_server")
}
