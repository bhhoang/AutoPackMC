package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// What RemoveServer does with the server's folder.
const (
	FolderKeep   = "keep"   // leave it where it is
	FolderTrash  = "trash"  // move it to the Recycle Bin, where it can be restored
	FolderDelete = "delete" // delete it for good
)

// RemoveServer takes the server off the list, and keeps, moves to the
// Recycle Bin or deletes its folder (world, mods, settings) as folder says.
// A running server must be stopped first, and a folder that does not look
// like this server's own folder is never moved or deleted.
func (s *Service) RemoveServer(id, folder string) error {
	if folder != FolderKeep && folder != FolderTrash && folder != FolderDelete {
		return &Error{Code: "bad_request", Detail: "folder: " + folder}
	}
	rec, ok := s.store.Server(id)
	if !ok {
		return &Error{Code: "no_server"}
	}
	if s.settingUp(rec.Dir) {
		return &Error{Code: "setup_running"}
	}

	// While it is being removed the server cannot be started.
	s.serversMu.Lock()
	if s.running[id] != nil {
		s.serversMu.Unlock()
		return &Error{Code: "stop_first"}
	}
	if s.removing[id] {
		s.serversMu.Unlock()
		return nil
	}
	s.removing[id] = true
	s.serversMu.Unlock()
	defer func() {
		s.serversMu.Lock()
		delete(s.removing, id)
		s.serversMu.Unlock()
	}()

	if folder != FolderKeep {
		if _, err := os.Stat(rec.Dir); err == nil {
			if !s.safeToMove(rec) {
				return &Error{Code: "not_server_folder", Detail: rec.Dir}
			}
			if folder == FolderTrash {
				err = s.trash(rec.Dir)
			} else {
				err = deleteFolder(rec.Dir)
			}
			if err != nil {
				// A delete that stopped part way leaves some files; the server
				// stays listed so the user can see where they are.
				return userError(err)
			}
		}
	}
	if err := s.store.RemoveServer(id); err != nil {
		return userError(err)
	}
	s.serversMu.Lock()
	delete(s.logs, id)
	delete(s.states, id)
	s.serversMu.Unlock()
	return nil
}

// deleteFolder deletes dir and everything in it. Read-only files, which
// Windows refuses to delete, are made writable first.
func deleteFolder(dir string) error {
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil {
			_ = os.Chmod(path, 0o777)
		}
		return nil
	})
	if err := os.RemoveAll(dir); err != nil {
		return &Error{Code: "delete_failed", Detail: err.Error()}
	}
	return nil
}

// safeToMove reports whether rec's folder can be moved or deleted: it has
// to look like a Minecraft server, and must not be a drive, the user's home,
// the folder new servers go into, or a folder that holds another server.
func (s *Service) safeToMove(rec ServerRecord) bool {
	dir := filepath.Clean(rec.Dir)
	if !filepath.IsAbs(dir) || filepath.Dir(dir) == dir {
		return false
	}
	protected := []string{s.cfg.DefaultServersDir, s.store.Settings().ServersDir}
	if home, err := os.UserHomeDir(); err == nil {
		protected = append(protected, home)
	}
	for _, p := range protected {
		// The folder must not be one of these, or contain one.
		if p != "" && (samePath(dir, p) || within(p, dir)) {
			return false
		}
	}
	for _, other := range s.store.Servers() {
		if other.ID != rec.ID && (samePath(other.Dir, dir) || within(other.Dir, dir)) {
			return false
		}
	}
	for _, marker := range []string{"server.properties", "eula.txt", "mods", ".mcpackctl"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// settingUp reports whether a setup is running in dir.
func (s *Service) settingUp(dir string) bool {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	return s.setupCancel != nil && samePath(s.setupDir, dir)
}

// samePath compares two folder paths; Windows ignores letter case.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// within reports whether path is inside dir (not dir itself).
func within(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil || rel == "." {
		return false
	}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.VolumeName(path), filepath.VolumeName(dir)) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
