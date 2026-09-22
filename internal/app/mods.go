package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/bhhoang/AutoPackMC/internal/resolver"
)

// disabledSuffix marks a jar the user turned off. The loaders only load
// files ending in .jar, so a renamed jar stays in mods/ but is not loaded.
const disabledSuffix = ".disabled"

// ModView is a mod as listed on the Mods tab.
type ModView struct {
	FileName  string `json:"fileName"`
	Name      string `json:"name"`
	State     string `json:"state"`  // on, off or mine
	Reason    string `json:"reason"` // why it is off: client, list or you
	ProjectID int    `json:"projectId"`
}

// Mods lists a server's mods: those on the server, those the user added,
// and those left off.
func (s *Service) Mods(id string) ([]ModView, error) {
	rec, ok := s.store.Server(id)
	if !ok {
		return nil, &Error{Code: "no_server"}
	}
	modsDir := filepath.Join(rec.Dir, "mods")
	entries, err := os.ReadDir(modsDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, userError(err)
	}

	added := map[string]Added{}
	for _, a := range rec.Added {
		added[strings.ToLower(a.FileName)] = a
	}
	present := map[string]bool{}
	var out []ModView
	for _, e := range entries {
		name := e.Name()
		lower := strings.ToLower(name)
		switch {
		case e.IsDir():
		case strings.HasSuffix(lower, ".jar"):
			present[lower] = true
			if a, ok := added[lower]; ok {
				out = append(out, ModView{FileName: name, Name: displayName(a.Name, name), State: "mine", ProjectID: a.ProjectID})
			} else {
				out = append(out, ModView{FileName: name, Name: modName(name), State: "on"})
			}
		case strings.HasSuffix(lower, ".jar"+disabledSuffix):
			jar := strings.TrimSuffix(name, disabledSuffix)
			present[strings.ToLower(jar)] = true
			out = append(out, ModView{FileName: jar, Name: modName(jar), State: "off", Reason: "you"})
		}
	}
	for _, m := range rec.LeftOff {
		if m.FileName != "" && present[strings.ToLower(m.FileName)] {
			continue
		}
		reason := "client"
		if m.ByList {
			reason = "list"
		}
		name := modName(m.FileName)
		if m.Slug != "" {
			name = slugName(m.Slug)
		}
		file := m.FileName
		if file == "" {
			file = m.Slug
		}
		out = append(out, ModView{FileName: file, Name: name, State: "off", Reason: reason, ProjectID: m.ProjectID})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	if out == nil {
		out = []ModView{}
	}
	return out, nil
}

// SetModOff turns a mod off: its jar is renamed so the server skips it.
func (s *Service) SetModOff(id, fileName string) error {
	rec, ok := s.store.Server(id)
	if !ok {
		return &Error{Code: "no_server"}
	}
	// A running server keeps its jars open, and Windows refuses to rename them.
	if s.isRunning(id) {
		return &Error{Code: "stop_first"}
	}
	if err := disableMod(filepath.Join(rec.Dir, "mods"), fileName); err != nil {
		return userError(err)
	}
	return nil
}

// SetModOn turns a mod back on. A mod setup left off is downloaded again and
// kept on future updates.
func (s *Service) SetModOn(id, fileName string) error {
	rec, ok := s.store.Server(id)
	if !ok {
		return &Error{Code: "no_server"}
	}
	modsDir := filepath.Join(rec.Dir, "mods")
	if err := checkModFileName(fileName); err != nil {
		return err
	}
	disabled := filepath.Join(modsDir, fileName+disabledSuffix)
	if exists(disabled) {
		if err := os.Rename(disabled, filepath.Join(modsDir, fileName)); err != nil {
			return userError(err)
		}
		return nil
	}
	for _, m := range rec.LeftOff {
		if !strings.EqualFold(m.FileName, fileName) && !strings.EqualFold(m.Slug, fileName) {
			continue
		}
		if m.ProjectID == 0 || m.FileID == 0 {
			return &Error{Code: "cannot_restore"}
		}
		if err := s.downloaderFor().DownloadOne(m.ProjectID, m.FileID, modsDir); err != nil {
			return userError(err)
		}
		keep := m.Slug
		if keep == "" {
			keep = fmt.Sprint(m.ProjectID)
		}
		_, err := s.store.UpdateServer(id, func(r *ServerRecord) {
			r.Include = uniq(append(r.Include, keep))
			kept := r.LeftOff[:0]
			for _, x := range r.LeftOff {
				if x.ProjectID != m.ProjectID {
					kept = append(kept, x)
				}
			}
			r.LeftOff = kept
		})
		return err
	}
	return &Error{Code: "no_mod"}
}

// RemoveMod deletes a mod the user added.
func (s *Service) RemoveMod(id, fileName string) error {
	rec, ok := s.store.Server(id)
	if !ok {
		return &Error{Code: "no_server"}
	}
	if s.isRunning(id) {
		return &Error{Code: "stop_first"}
	}
	if err := checkModFileName(fileName); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(rec.Dir, "mods", fileName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return userError(err)
	}
	_, err := s.store.UpdateServer(id, func(r *ServerRecord) {
		kept := r.Added[:0]
		for _, a := range r.Added {
			if !strings.EqualFold(a.FileName, fileName) {
				kept = append(kept, a)
			}
		}
		r.Added = kept
	})
	return err
}

// ModResult is a CurseForge mod offered on the Add mods panel.
type ModResult struct {
	resolver.Project
	OnServer bool `json:"onServer"`
}

// SearchMods searches CurseForge for mods made for this server's Minecraft
// version and loader. An empty query lists popular mods.
func (s *Service) SearchMods(id, query string) ([]ModResult, error) {
	rec, ok := s.store.Server(id)
	if !ok {
		return nil, &Error{Code: "no_server"}
	}
	found, err := resolver.New(s.apiKey()).SearchMods(strings.TrimSpace(query), rec.MC, rec.Loader, 20)
	if err != nil {
		return nil, userError(err)
	}
	out := make([]ModResult, 0, len(found))
	for _, p := range found {
		out = append(out, ModResult{Project: p, OnServer: onServer(rec, p.ID)})
	}
	return out, nil
}

// ModFromLink looks up a pasted CurseForge mod link.
func (s *Service) ModFromLink(id, link string) (*ModResult, error) {
	rec, ok := s.store.Server(id)
	if !ok {
		return nil, &Error{Code: "no_server"}
	}
	if !resolver.IsCurseForgeModURL(strings.TrimSpace(link)) {
		return nil, &Error{Code: "bad_mod_link"}
	}
	res := resolver.New(s.apiKey())
	p, err := res.ModFromURL(strings.TrimSpace(link))
	if err != nil {
		return nil, userError(err)
	}
	file, err := res.ModFileFor(p.ID, rec.MC, rec.Loader)
	if errors.Is(err, resolver.ErrNoCompatibleFile) {
		return nil, &Error{Code: "no_version", Detail: p.Name}
	}
	if err != nil {
		return nil, userError(err)
	}
	p.ClientOnly = file.ClientOnly()
	return &ModResult{Project: *p, OnServer: onServer(rec, p.ID)}, nil
}

// AddResult says what AddMod did.
type AddResult struct {
	Status   string `json:"status"` // added, client_only, no_version or already
	Name     string `json:"name"`
	FileName string `json:"fileName"`
}

// AddMod downloads the file of a CurseForge mod made for this server into
// mods/. A client-only mod is only added when force is set.
func (s *Service) AddMod(id string, projectID int, name string, force bool) (*AddResult, error) {
	rec, ok := s.store.Server(id)
	if !ok {
		return nil, &Error{Code: "no_server"}
	}
	if onServer(rec, projectID) {
		return &AddResult{Status: "already", Name: name}, nil
	}
	file, err := resolver.New(s.apiKey()).ModFileFor(projectID, rec.MC, rec.Loader)
	if errors.Is(err, resolver.ErrNoCompatibleFile) {
		return &AddResult{Status: "no_version", Name: name}, nil
	}
	if err != nil {
		return nil, userError(err)
	}
	if file.ClientOnly() && !force {
		return &AddResult{Status: "client_only", Name: name, FileName: file.FileName}, nil
	}
	modsDir := filepath.Join(rec.Dir, "mods")
	if err := s.downloaderFor().DownloadOne(projectID, file.ID, modsDir); err != nil {
		return nil, userError(err)
	}
	_, err = s.store.UpdateServer(id, func(r *ServerRecord) {
		r.Added = append(r.Added, Added{ProjectID: projectID, FileID: file.ID, Name: name, FileName: file.FileName})
	})
	if err != nil {
		return nil, userError(err)
	}
	return &AddResult{Status: "added", Name: name, FileName: file.FileName}, nil
}

// AddJarFiles copies .jar files the user chose into mods/ and returns the
// names of files that were skipped because they are not jars.
func (s *Service) AddJarFiles(id string, paths []string) ([]string, error) {
	rec, ok := s.store.Server(id)
	if !ok {
		return nil, &Error{Code: "no_server"}
	}
	modsDir := filepath.Join(rec.Dir, "mods")
	if err := os.MkdirAll(modsDir, 0o755); err != nil {
		return nil, userError(err)
	}
	skipped := []string{}
	var added []Added
	for _, p := range paths {
		name := filepath.Base(p)
		if !strings.EqualFold(filepath.Ext(name), ".jar") {
			skipped = append(skipped, name)
			continue
		}
		if err := copyFile(p, filepath.Join(modsDir, name)); err != nil {
			return skipped, userError(err)
		}
		added = append(added, Added{Name: modName(name), FileName: name})
	}
	_, err := s.store.UpdateServer(id, func(r *ServerRecord) {
		for _, a := range added {
			known := false
			for _, x := range r.Added {
				known = known || strings.EqualFold(x.FileName, a.FileName)
			}
			if !known {
				r.Added = append(r.Added, a)
			}
		}
	})
	return skipped, err
}

// PickJarFiles asks for .jar files and adds them.
func (s *Service) PickJarFiles(id string) ([]string, error) {
	paths, err := s.ui.PickFiles("", "*.jar", true)
	if err != nil || len(paths) == 0 {
		return []string{}, err
	}
	return s.AddJarFiles(id, paths)
}

func onServer(rec ServerRecord, projectID int) bool {
	for _, a := range rec.Added {
		if a.ProjectID == projectID {
			return true
		}
	}
	for _, m := range rec.LeftOff {
		if m.ProjectID == projectID {
			return false
		}
	}
	for _, p := range rec.ProjectIDs {
		if p == projectID {
			return true
		}
	}
	return false
}

// disableMod renames mods/fileName so the server no longer loads it.
func disableMod(modsDir, fileName string) error {
	if err := checkModFileName(fileName); err != nil {
		return err
	}
	jar := filepath.Join(modsDir, fileName)
	return os.Rename(jar, jar+disabledSuffix)
}

// reapplyDisabled keeps mods the user turned off turned off after an update
// downloaded them again. before lists the jars mods/ held before the update.
// A turned-off mod matches the same file name, or else a new version: a jar
// the update brought in whose guessed mod name is the same, when exactly one
// such jar exists. The new version is then turned off in place of the old.
func reapplyDisabled(modsDir string, before map[string]bool) {
	entries, _ := os.ReadDir(modsDir)
	added := map[string][]string{} // mod name -> jars the update brought in
	var disabled []string
	for _, e := range entries {
		name, lower := e.Name(), strings.ToLower(e.Name())
		switch {
		case e.IsDir():
		case strings.HasSuffix(lower, ".jar"):
			if !before[lower] {
				key := strings.ToLower(modName(name))
				added[key] = append(added[key], name)
			}
		case strings.HasSuffix(lower, ".jar"+disabledSuffix):
			disabled = append(disabled, name)
		}
	}
	for _, d := range disabled {
		jar := strings.TrimSuffix(d, disabledSuffix)
		if exists(filepath.Join(modsDir, jar)) {
			_ = os.Remove(filepath.Join(modsDir, jar))
			continue
		}
		matches := added[strings.ToLower(modName(jar))]
		if len(matches) != 1 {
			continue // no new version, or too many to tell which
		}
		newer := matches[0]
		if err := os.Rename(filepath.Join(modsDir, newer), filepath.Join(modsDir, newer+disabledSuffix)); err == nil {
			_ = os.Remove(filepath.Join(modsDir, d))
		}
	}
}

// jarNames lists the .jar files in dir, lower-cased.
func jarNames(dir string) map[string]bool {
	entries, _ := os.ReadDir(dir)
	names := map[string]bool{}
	for _, e := range entries {
		if lower := strings.ToLower(e.Name()); !e.IsDir() && strings.HasSuffix(lower, ".jar") {
			names[lower] = true
		}
	}
	return names
}

// checkModFileName rejects names that would reach outside mods/.
func checkModFileName(name string) error {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return &Error{Code: "no_mod"}
	}
	return nil
}

func countJars(dir string) int {
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".jar") {
			n++
		}
	}
	return n
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

var versionPart = regexp.MustCompile(`^(v?\d|mc\d|forge$|neoforge$|fabric$|quilt$|all$)`)

// modName guesses a readable name from a jar file name, e.g.
// "FarmersDelight-1.20.1-1.2.4.jar" -> "Farmers Delight".
func modName(file string) string {
	base := strings.TrimSuffix(file, filepath.Ext(file))
	parts := strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '_' || r == ' ' || r == '+' })
	var words []string
	for _, p := range parts {
		if len(words) > 0 && versionPart.MatchString(strings.ToLower(p)) {
			break
		}
		words = append(words, splitCamel(p))
	}
	if len(words) == 0 {
		return base
	}
	return strings.Join(words, " ")
}

// splitCamel turns "FarmersDelight" into "Farmers Delight".
func splitCamel(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		if i > 0 && unicode.IsUpper(r) && unicode.IsLower(rs[i-1]) {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// slugName turns a CurseForge slug into a readable name.
func slugName(slug string) string {
	words := strings.Split(slug, "-")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func displayName(name, file string) string {
	if name != "" {
		return name
	}
	return modName(file)
}
