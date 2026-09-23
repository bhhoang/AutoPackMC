// Package app is the logic behind the AutoPack desktop window: servers the
// user has set up, setting up and updating packs, running servers, and
// managing their mods. It has no dependency on the window toolkit; the
// window reaches it through the exported methods of Service and receives
// updates as events through UI.Emit.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/bhhoang/AutoPackMC/internal/detector"
	"github.com/bhhoang/AutoPackMC/internal/downloader"
	"github.com/bhhoang/AutoPackMC/internal/resolver"
	"github.com/bhhoang/AutoPackMC/internal/setup"
	"github.com/bhhoang/AutoPackMC/pkg/logger"
)

// UI is what the service needs from the window it runs in.
type UI interface {
	// Emit sends an event to the page.
	Emit(event string, data any)
	// PickFolder asks for a folder; "" means the user cancelled.
	PickFolder(title, start string) (string, error)
	// PickFiles asks for files matching pattern, such as "*.jar".
	PickFiles(title, pattern string, multiple bool) ([]string, error)
	// Open shows a folder in File Explorer or a web page in the browser.
	Open(target string)
}

// Config holds what the service needs from the environment.
type Config struct {
	DataDir           string // where settings.json and servers.json live
	CacheDir          string // mod download cache
	DefaultServersDir string
	DefaultAPIKey     string
	ExcludeListSource string
	Version           string // this build's version, such as v0.2.0; anything else is a development build
	UpdateRepo        string // GitHub owner/name whose releases hold new versions
	UpdateAPI         string // GitHub API base; empty means api.github.com (set in tests)
	ExePath           string // the running executable; empty means os.Executable (set in tests)
	CurseForgeAPI     string // CurseForge API base; empty means the real one (set in tests)
}

// Service is bound to the window; each exported method can be called from
// the page.
type Service struct {
	cfg   Config
	ui    UI
	store *store

	setupMu     sync.Mutex
	setupCancel context.CancelFunc // non-nil while a setup runs
	setupDone   chan struct{}      // closed when the running setup has finished

	update updater

	// downloadMod fetches one CurseForge file into dir; tests replace it.
	downloadMod func(projectID, fileID int, dir string) error

	serversMu sync.Mutex
	running   map[string]*running // by server ID, while running
	logs      map[string]*logRing // by server ID, kept after the server stops
	states    map[string]*stateView
}

// New loads the saved settings and servers.
func New(cfg Config, ui UI) (*Service, error) {
	st, err := loadStore(cfg.DataDir, Settings{
		Theme:      "system",
		ServersDir: cfg.DefaultServersDir,
		AutoJava:   true,
	})
	if err != nil {
		return nil, err
	}
	s := &Service{
		cfg:     cfg,
		ui:      ui,
		store:   st,
		running: map[string]*running{},
		logs:    map[string]*logRing{},
		states:  map[string]*stateView{},
	}
	s.downloadMod = func(projectID, fileID int, dir string) error {
		return s.downloaderFor().DownloadOne(projectID, fileID, dir)
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Overview and settings
// ---------------------------------------------------------------------------

// AppState is everything the page needs on first load.
type AppState struct {
	Version  string       `json:"version"`
	Settings Settings     `json:"settings"`
	Servers  []ServerView `json:"servers"`
	MemoryGB int          `json:"memoryGb"` // this PC's memory, 0 when unknown
	LanIP    string       `json:"lanIp"`
}

// State returns the app state for the page.
func (s *Service) State() AppState {
	return AppState{
		Version:  s.cfg.Version,
		Settings: s.store.Settings(),
		Servers:  s.Servers(),
		MemoryGB: systemMemoryGB(),
		LanIP:    lanIP(),
	}
}

// SaveSettings stores the Settings screen.
func (s *Service) SaveSettings(v Settings) error {
	if v.ServersDir == "" {
		v.ServersDir = s.cfg.DefaultServersDir
	}
	return s.store.SaveSettings(v)
}

func (s *Service) apiKey() string {
	if k := s.store.Settings().APIKey; k != "" {
		return k
	}
	return s.cfg.DefaultAPIKey
}

// PickFolder asks for a folder, starting at start or, when start does not
// exist yet (a new server's folder is only made by setup), the nearest
// folder above it that does. Windows refuses to open the dialog at a missing
// folder.
func (s *Service) PickFolder(start string) (string, error) {
	return s.ui.PickFolder("", existingDir(start))
}

// existingDir returns dir or its nearest existing parent, or "" if none exists.
func existingDir(dir string) string {
	for dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// PickPackFile asks for a modpack archive.
func (s *Service) PickPackFile() (string, error) {
	files, err := s.ui.PickFiles("", "*.zip;*.rar", false)
	if err != nil || len(files) == 0 {
		return "", err
	}
	return files[0], nil
}

// PickJavaExe asks for a java.exe to run servers with.
func (s *Service) PickJavaExe() (string, error) {
	files, err := s.ui.PickFiles("", "java.exe;javaw.exe;java", false)
	if err != nil || len(files) == 0 {
		return "", err
	}
	return files[0], nil
}

// OpenURL opens a web page in the browser. Only http(s) links are opened.
func (s *Service) OpenURL(link string) {
	if u, err := url.Parse(link); err == nil && (u.Scheme == "https" || u.Scheme == "http") {
		s.ui.Open(link)
	}
}

// OpenServerFolder shows the server's folder in File Explorer.
func (s *Service) OpenServerFolder(id string) {
	if rec, ok := s.store.Server(id); ok {
		s.ui.Open(rec.Dir)
	}
}

// ---------------------------------------------------------------------------
// New servers and updates
// ---------------------------------------------------------------------------

// PackPreview describes a modpack link before it is set up.
type PackPreview struct {
	Kind    string `json:"kind"` // curseforge, drive, file or folder
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Author  string `json:"author"`
	LogoURL string `json:"logoUrl"`
}

// LookupPack checks a pasted link or chosen file and describes the pack.
func (s *Service) LookupPack(input string) (*PackPreview, error) {
	input = strings.TrimSpace(input)
	switch {
	case resolver.IsCurseForgeURL(input):
		p, err := s.resolver().PackFromURL(input)
		if err != nil {
			return nil, userError(err)
		}
		return &PackPreview{Kind: "curseforge", Name: p.Name, Summary: p.Summary, Author: p.Author, LogoURL: p.LogoURL}, nil
	case detector.IsGoogleDriveURL(input):
		return &PackPreview{Kind: "drive"}, nil
	}
	info, err := os.Stat(input)
	if err != nil {
		return nil, &Error{Code: "bad_link"}
	}
	name := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	if info.IsDir() {
		return &PackPreview{Kind: "folder", Name: name}, nil
	}
	return &PackPreview{Kind: "file", Name: name}, nil
}

// SuggestServerDir returns a free folder for a new server called name.
func (s *Service) SuggestServerDir(name string) string {
	base := s.store.Settings().ServersDir
	if base == "" {
		base = s.cfg.DefaultServersDir
	}
	slug := safeFolderName(name)
	dir := filepath.Join(base, slug)
	for i := 2; exists(dir); i++ {
		dir = filepath.Join(base, fmt.Sprintf("%s %d", slug, i))
	}
	return dir
}

// ServerDirIn returns where a server called name goes when the user picks
// parent: parent itself when it is empty, or else a new folder named after
// the pack inside it, so the server's files never mix with other files.
func (s *Service) ServerDirIn(parent, name string) string {
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) == 0 {
		return parent
	}
	slug := safeFolderName(name)
	dir := filepath.Join(parent, slug)
	for i := 2; exists(dir); i++ {
		dir = filepath.Join(parent, fmt.Sprintf("%s %d", slug, i))
	}
	return dir
}

// SetupRequest is the New server form (or an update of an existing server).
type SetupRequest struct {
	ServerID   string `json:"serverId"` // set when updating an existing server
	Input      string `json:"input"`
	Dir        string `json:"dir"`
	RAMGB      int    `json:"ramGb"`
	AcceptEULA bool   `json:"acceptEula"`
	Java       string `json:"java"`   // "auto", a major version such as "17", or a java.exe path
	Loader     string `json:"loader"` // "auto", forge, neoforge or fabric
	KeepClient bool   `json:"keepClient"`
	Exclude    string `json:"exclude"`
	Include    string `json:"include"`
}

// SetupEvent is sent as "setup" while a setup runs.
type SetupEvent struct {
	Phase     string         `json:"phase"` // stage, mods, done, error
	Stage     setup.Stage    `json:"stage,omitempty"`
	Info      *setup.Info    `json:"info,omitempty"`
	ModsDone  int            `json:"modsDone,omitempty"`
	ModsTotal int            `json:"modsTotal,omitempty"`
	Server    *ServerView    `json:"server,omitempty"`
	LeftOff   int            `json:"leftOff,omitempty"`
	Failed    []FailedModRef `json:"failed,omitempty"`
	Error     *Error         `json:"error,omitempty"`
}

// FailedModRef is a mod that could not be downloaded.
type FailedModRef struct {
	Name    string `json:"name"`
	PageURL string `json:"pageUrl"`
}

// StartSetup begins setting up (or updating) a server in the background and
// reports progress as "setup" events.
func (s *Service) StartSetup(req SetupRequest) error {
	if !req.AcceptEULA {
		return &Error{Code: "eula"}
	}
	if strings.TrimSpace(req.Input) == "" {
		return &Error{Code: "bad_link"}
	}
	if req.ServerID != "" {
		rec, ok := s.store.Server(req.ServerID)
		if !ok {
			return &Error{Code: "no_server"}
		}
		if s.isRunning(req.ServerID) {
			return &Error{Code: "server_running"}
		}
		req.Dir = rec.Dir
	}
	if req.Dir == "" {
		return &Error{Code: "no_folder"}
	}

	s.setupMu.Lock()
	if s.setupCancel != nil {
		s.setupMu.Unlock()
		return &Error{Code: "setup_running"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.setupCancel, s.setupDone = cancel, done
	s.setupMu.Unlock()

	go func() {
		defer func() {
			s.setupMu.Lock()
			s.setupCancel, s.setupDone = nil, nil
			s.setupMu.Unlock()
			cancel()
			close(done)
		}()
		s.runSetup(ctx, req)
	}()
	return nil
}

// SetupRunning reports whether a setup is in progress.
func (s *Service) SetupRunning() bool {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	return s.setupCancel != nil
}

// CancelSetupAndWait stops a running setup and waits until it has rolled
// back, so the server folder is left as it was. Used when the app closes.
func (s *Service) CancelSetupAndWait() {
	s.setupMu.Lock()
	cancel, done := s.setupCancel, s.setupDone
	s.setupMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// CancelSetup stops the running setup after its current step.
func (s *Service) CancelSetup() {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.setupCancel != nil {
		s.setupCancel()
	}
}

func (s *Service) runSetup(ctx context.Context, req SetupRequest) {
	settings := s.store.Settings()
	opts := setup.Options{
		Input:             strings.TrimSpace(req.Input),
		Output:            req.Dir,
		JavaPath:          "java",
		SkipClean:         req.KeepClient,
		ExcludeMods:       splitList(req.Exclude),
		IncludeMods:       splitList(req.Include),
		APIKey:            s.apiKey(),
		CacheDir:          s.cfg.CacheDir,
		Workers:           4,
		ExcludeListSource: s.cfg.ExcludeListSource,
		OnStage: func(st setup.Stage, info setup.Info) {
			s.ui.Emit("setup", SetupEvent{Phase: "stage", Stage: st, Info: &info})
		},
		OnMods: func(done, total int) {
			s.ui.Emit("setup", SetupEvent{Phase: "mods", ModsDone: done, ModsTotal: total})
		},
	}
	if req.RAMGB > 0 {
		opts.RAM = fmt.Sprintf("%dG", req.RAMGB)
	}
	if req.Loader != "" && req.Loader != "auto" {
		opts.ForceLoader = req.Loader
	}
	switch java := strings.TrimSpace(req.Java); {
	case java == "" || java == "auto":
		if !settings.AutoJava {
			// Java from PATH, as chosen on the Settings screen.
			opts.JavaPathExplicit = true
		}
	case isDigits(java):
		opts.JavaVersion, _ = strconv.Atoi(java)
	default:
		opts.JavaPath, opts.JavaPathExplicit = java, true
	}

	var prev ServerRecord
	if req.ServerID != "" {
		prev, _ = s.store.Server(req.ServerID)
		// Keep the user's earlier choices on update.
		opts.ExcludeMods = append(opts.ExcludeMods, prev.Exclude...)
		opts.IncludeMods = append(opts.IncludeMods, prev.Include...)
	}

	modsDir := filepath.Join(req.Dir, "mods")
	before := jarNames(modsDir)
	res, err := setup.Run(ctx, opts)
	if err != nil {
		logger.Get().Error().Err(err).Str("input", opts.Input).Msg("setup failed")
		s.ui.Emit("setup", SetupEvent{Phase: "error", Error: userError(err)})
		return
	}
	reapplyDisabled(modsDir, before)

	id := req.ServerID
	if id == "" {
		id = s.newServerID(res.Name)
	}
	var logo string
	if resolver.IsCurseForgeURL(opts.Input) {
		if p, err := s.resolver().PackFromURL(opts.Input); err == nil {
			logo = p.LogoURL
		}
	}
	rec, err := s.store.UpdateServer(id, func(r *ServerRecord) {
		r.Name = res.Name
		if r.Name == "" {
			r.Name = filepath.Base(req.Dir)
		}
		r.Dir = req.Dir
		r.Source = opts.Input
		r.PackVersion = res.Version
		r.MC = res.MinecraftVersion
		r.Loader = res.Loader
		r.LoaderVersion = res.LoaderVersion
		if logo != "" {
			r.LogoURL = logo
		}
		r.JavaPath = res.JavaPath
		r.JavaExplicit = opts.JavaPathExplicit
		if req.RAMGB > 0 {
			r.RAMGB = req.RAMGB
		}
		r.ProjectIDs = res.ProjectIDs
		r.LeftOff = r.LeftOff[:0]
		for _, m := range res.LeftOff {
			r.LeftOff = append(r.LeftOff, LeftOff{ProjectID: m.ProjectID, FileID: m.FileID, Slug: m.Slug, FileName: m.FileName, ByList: m.ByList})
		}
		r.Exclude = uniq(append(r.Exclude, splitList(req.Exclude)...))
		r.Include = uniq(append(r.Include, splitList(req.Include)...))
		r.SkipClean = opts.SkipClean
	})
	if err != nil {
		s.ui.Emit("setup", SetupEvent{Phase: "error", Error: userError(err)})
		return
	}
	view := s.view(rec)
	ev := SetupEvent{Phase: "done", Server: &view, LeftOff: len(res.LeftOff)}
	for _, f := range res.Failed {
		name := f.Name
		if name == "" {
			name = f.FileName
		}
		ev.Failed = append(ev.Failed, FailedModRef{Name: name, PageURL: f.PageURL})
	}
	s.ui.Emit("setup", ev)
}

func (s *Service) newServerID(name string) string {
	base := strings.ToLower(safeFolderName(name))
	base = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "server"
	}
	id := base
	for i := 2; ; i++ {
		if _, taken := s.store.Server(id); !taken {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// Error is an error the page shows in the user's language, picked by Code.
// Detail is the technical message, shown under "Details".
type Error struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return e.Code + ": " + e.Detail
	}
	return e.Code
}

// userError classifies err for the page.
func userError(err error) *Error {
	var ue *Error
	if errors.As(err, &ue) {
		return ue
	}
	e := &Error{Code: "unknown", Detail: err.Error()}
	var netErr net.Error
	var dnsErr *net.DNSError
	var urlErr *url.Error
	msg := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.Canceled):
		e.Code = "cancelled"
	case errors.As(err, &dnsErr), errors.As(err, &netErr), errors.As(err, &urlErr),
		strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"):
		e.Code = "network"
	case errors.Is(err, syscall.ENOSPC), strings.Contains(msg, "not enough space"), strings.Contains(msg, "no space left"):
		e.Code = "disk_full"
	case errors.Is(err, os.ErrPermission):
		e.Code = "no_permission"
	case strings.Contains(msg, "nothing on curseforge"), strings.Contains(msg, "no mod found"):
		e.Code = "not_found"
	case strings.Contains(msg, "java"):
		e.Code = "java"
	case strings.Contains(msg, "cannot detect pack type"), strings.Contains(msg, "parse manifest"):
		e.Code = "not_a_pack"
	}
	return e
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// splitList splits a comma or space separated list.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
}

func uniq(list []string) []string {
	seen := map[string]bool{}
	out := list[:0]
	for _, v := range list {
		if v != "" && !seen[strings.ToLower(v)] {
			seen[strings.ToLower(v)] = true
			out = append(out, v)
		}
	}
	return out
}

var badFolderChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]+`)

// safeFolderName turns a pack name into a folder name Windows accepts.
func safeFolderName(name string) string {
	name = strings.TrimSpace(badFolderChars.ReplaceAllString(name, " "))
	name = strings.TrimRight(name, ". ")
	if name == "" {
		return "Minecraft server"
	}
	return name
}

// resolver returns a CurseForge client using the configured key.
func (s *Service) resolver() *resolver.Resolver {
	r := resolver.New(s.apiKey())
	if s.cfg.CurseForgeAPI != "" {
		r.WithBaseURL(s.cfg.CurseForgeAPI)
	}
	return r
}

// downloaderFor returns a downloader for adding single mods.
func (s *Service) downloaderFor() *downloader.Downloader {
	return downloader.New(s.cfg.CacheDir, s.apiKey(), 1, false)
}
