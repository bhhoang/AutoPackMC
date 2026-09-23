package app

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// maxServerName caps the name shown in the server list (characters).
const maxServerName = 64

// RenameServer changes the name the app shows for the server. Pack updates
// keep a name the user chose. It does not change the server's folder or the
// description friends see in Minecraft.
func (s *Service) RenameServer(id, name string) (ServerView, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return ServerView{}, &Error{Code: "bad_name"}
	}
	if utf8.RuneCountInString(name) > maxServerName {
		name = string([]rune(name)[:maxServerName])
	}
	if _, ok := s.store.Server(id); !ok {
		return ServerView{}, &Error{Code: "no_server"}
	}
	rec, err := s.store.UpdateServer(id, func(r *ServerRecord) {
		r.Name, r.CustomName = name, true
	})
	if err != nil {
		return ServerView{}, userError(err)
	}
	return s.view(rec), nil
}

// MoveServer moves the server's folder into parent, keeping the folder's
// name, and remembers the new place. The server must be stopped.
func (s *Service) MoveServer(id, parent string) (ServerView, error) {
	rec, ok := s.store.Server(id)
	if !ok {
		return ServerView{}, &Error{Code: "no_server"}
	}
	parent = filepath.Clean(parent)
	if parent == "" || parent == "." || !filepath.IsAbs(parent) {
		return ServerView{}, &Error{Code: "no_folder"}
	}
	from := filepath.Clean(rec.Dir)
	to := filepath.Join(parent, filepath.Base(from))
	if samePath(from, to) {
		return s.view(rec), nil
	}
	if samePath(parent, from) || within(parent, from) {
		return ServerView{}, &Error{Code: "move_into_itself"}
	}
	if _, err := os.Stat(to); err == nil {
		return ServerView{}, &Error{Code: "folder_exists", Detail: to}
	}
	if _, err := os.Stat(from); err != nil {
		return ServerView{}, &Error{Code: "folder_missing", Detail: from}
	}
	if s.settingUp(from) {
		return ServerView{}, &Error{Code: "setup_running"}
	}

	// Like RemoveServer: stopped, and not startable while it moves.
	s.serversMu.Lock()
	if s.running[id] != nil {
		s.serversMu.Unlock()
		return ServerView{}, &Error{Code: "stop_first"}
	}
	if s.removing[id] {
		s.serversMu.Unlock()
		return ServerView{}, &Error{Code: "busy"}
	}
	s.removing[id] = true
	s.serversMu.Unlock()
	defer func() {
		s.serversMu.Lock()
		delete(s.removing, id)
		s.serversMu.Unlock()
	}()

	moveErr := s.move(from, to)
	// Follow the files: if the move stopped part way, the server stays where
	// its folder still is.
	if _, err := os.Stat(from); err == nil {
		if moveErr == nil {
			moveErr = &Error{Code: "move_failed", Detail: "the folder is still there"}
		}
		return ServerView{}, userError(moveErr)
	}
	if _, err := os.Stat(to); err != nil {
		if moveErr == nil {
			moveErr = &Error{Code: "move_failed", Detail: to}
		}
		return ServerView{}, userError(moveErr)
	}
	rec, err := s.store.UpdateServer(id, func(r *ServerRecord) {
		r.Dir = to
		// A Java inside the server folder moved with it.
		if r.JavaPath != "" && within(r.JavaPath, from) {
			if rel, err := filepath.Rel(from, r.JavaPath); err == nil {
				r.JavaPath = filepath.Join(to, rel)
			}
		}
	})
	if err != nil {
		return ServerView{}, userError(err)
	}
	return s.view(rec), nil
}

// moveFolder moves from to to: a rename on the same drive, otherwise the
// platform's own move (on Windows, with its progress window).
func moveFolder(from, to string) error {
	if err := os.Rename(from, to); err == nil {
		return nil
	}
	return moveAcrossDrives(from, to)
}
