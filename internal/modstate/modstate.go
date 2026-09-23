// Package modstate tracks which jars in a server's mods/ folder were
// installed by setup, so that updating a pack removes jars the new version
// no longer uses without touching mods the user added.
package modstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bhhoang/Maple/pkg/logger"
)

const (
	stateDir   = ".mcpackctl"
	recordFile = "installed-mods.json"
	backupDir  = "previous-mods"
)

type record struct {
	Mods []string `json:"mods"`
}

// Update is an in-progress setup of a server. The jars recorded by the
// previous setup are set aside when it begins, and deleted or restored
// when it finishes.
type Update struct {
	serverDir string
	moved     []string        // jars moved into the backup directory
	untracked map[string]bool // jars setup did not install, e.g. the user's
}

// Begin moves the jars recorded by the previous setup out of mods/ into
// .mcpackctl/previous-mods/, so the new setup installs exactly its own jars.
// Jars that setup did not record, such as ones the user added, stay put.
func Begin(serverDir string) (*Update, error) {
	u := &Update{serverDir: serverDir}
	rec, err := readRecord(serverDir)
	if err != nil {
		return nil, err
	}
	defer u.noteUntracked()
	if len(rec.Mods) == 0 {
		return u, nil
	}

	backup := filepath.Join(serverDir, stateDir, backupDir)
	// Left over from a setup that could not restore; its jars are superseded.
	if err := os.RemoveAll(backup); err != nil {
		return nil, fmt.Errorf("clear %s: %w", backup, err)
	}
	if err := os.MkdirAll(backup, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", backup, err)
	}

	modsDir := filepath.Join(serverDir, "mods")
	for _, name := range rec.Mods {
		if !safeName(name) {
			continue
		}
		src := filepath.Join(modsDir, name)
		if _, err := os.Stat(src); err != nil {
			continue // already gone
		}
		if err := os.Rename(src, filepath.Join(backup, name)); err != nil {
			u.restore()
			return nil, fmt.Errorf("set aside %s: %w", name, err)
		}
		u.moved = append(u.moved, name)
	}
	logger.Get().Info().Int("mods", len(u.moved)).Msg("set aside mods from the previous setup")
	return u, nil
}

// noteUntracked remembers the jars in mods/ once the recorded ones are set
// aside: setup did not install them, so they are never recorded or removed.
// On a server set up before records existed this is every jar, which keeps
// the first update conservative.
func (u *Update) noteUntracked() {
	u.untracked = make(map[string]bool)
	names, _ := listJars(filepath.Join(u.serverDir, "mods"))
	for _, name := range names {
		u.untracked[name] = true
	}
}

// Finish completes the update. On success (setupErr nil) it records the jars
// now in mods/ and deletes the set-aside ones that the new setup did not
// install again. On failure it moves the set-aside jars back.
func (u *Update) Finish(setupErr error) {
	log := logger.Get()
	if setupErr != nil {
		u.rollback()
		return
	}

	current, err := listJars(filepath.Join(u.serverDir, "mods"))
	if err != nil {
		log.Warn().Err(err).Msg("cannot record installed mods; the next update will not remove outdated ones")
		return
	}
	var installed []string
	for _, name := range current {
		if !u.untracked[name] {
			installed = append(installed, name)
		}
	}
	if err := writeRecord(u.serverDir, record{Mods: installed}); err != nil {
		log.Warn().Err(err).Msg("cannot record installed mods; the next update will not remove outdated ones")
		return
	}

	have := make(map[string]bool, len(current))
	for _, name := range current {
		have[name] = true
	}
	var removed []string
	for _, name := range u.moved {
		if !have[name] {
			removed = append(removed, name)
		}
	}
	if len(removed) > 0 {
		log.Info().Strs("mods", removed).Msg("removed mods the new pack version no longer uses")
	}
	_ = os.RemoveAll(filepath.Join(u.serverDir, stateDir, backupDir))
}

// rollback returns mods/ to its state before the failed setup: jars the
// setup added are deleted, so a half-installed new version cannot load next
// to the old one, and the set-aside jars are moved back.
func (u *Update) rollback() {
	log := logger.Get()
	moved := make(map[string]bool, len(u.moved))
	for _, name := range u.moved {
		moved[name] = true
	}
	modsDir := filepath.Join(u.serverDir, "mods")
	current, _ := listJars(modsDir)
	var added []string
	for _, name := range current {
		// A jar in moved that is present again was reinstalled under the
		// same name; restore keeps that copy.
		if !u.untracked[name] && !moved[name] {
			if err := os.Remove(filepath.Join(modsDir, name)); err != nil {
				log.Warn().Err(err).Str("mod", name).Msg("cannot remove mod installed by the failed setup")
				continue
			}
			added = append(added, name)
		}
	}
	u.restore()
	if len(added) > 0 || len(u.moved) > 0 {
		log.Warn().Int("removed", len(added)).Int("restored", len(u.moved)).Msg("setup failed; mods/ is back to the previous setup")
	}
}

// restore moves set-aside jars back into mods/, skipping names that were
// installed again in the meantime.
func (u *Update) restore() {
	backup := filepath.Join(u.serverDir, stateDir, backupDir)
	modsDir := filepath.Join(u.serverDir, "mods")
	for _, name := range u.moved {
		dst := filepath.Join(modsDir, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := os.Rename(filepath.Join(backup, name), dst); err != nil {
			logger.Get().Warn().Err(err).Str("mod", name).Str("backup", backup).Msg("cannot restore mod; it is still in the backup folder")
		}
	}
}

// safeName rejects record entries that would escape mods/.
func safeName(name string) bool {
	return name != "" && filepath.Base(name) == name && name != "." && name != ".."
}

func listJars(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var jars []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".jar") {
			jars = append(jars, e.Name())
		}
	}
	sort.Strings(jars)
	return jars, nil
}

func readRecord(serverDir string) (record, error) {
	var rec record
	data, err := os.ReadFile(filepath.Join(serverDir, stateDir, recordFile))
	if errors.Is(err, os.ErrNotExist) {
		return rec, nil
	}
	if err != nil {
		return rec, fmt.Errorf("read installed mods record: %w", err)
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return rec, fmt.Errorf("parse installed mods record: %w", err)
	}
	return rec, nil
}

func writeRecord(serverDir string, rec record) error {
	dir := filepath.Join(serverDir, stateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, recordFile), append(data, '\n'), 0o644)
}
