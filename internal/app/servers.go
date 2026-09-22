package app

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bhhoang/AutoPackMC/internal/java"
	"github.com/bhhoang/AutoPackMC/internal/runtime"
	"github.com/bhhoang/AutoPackMC/pkg/logger"
)

// Server states shown on the page.
const (
	StateStopped  = "stopped"
	StateStarting = "starting"
	StateRunning  = "running"
	StateCrashed  = "crashed"
)

// stopTimeout is how long a server may take to save and exit.
const stopTimeout = 60 * time.Second

// ServerView is a server as shown on the page.
type ServerView struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Dir           string     `json:"dir"`
	Source        string     `json:"source"`
	MC            string     `json:"mc"`
	Loader        string     `json:"loader"`
	LoaderVersion string     `json:"loaderVersion"`
	LogoURL       string     `json:"logoUrl"`
	RAMGB         int        `json:"ramGb"`
	Port          int        `json:"port"`
	ModsOnServer  int        `json:"modsOnServer"`
	State         string     `json:"state"`
	Players       []string   `json:"players"`
	MaxPlayers    int        `json:"maxPlayers"`
	KeepClient    bool       `json:"keepClient"` // client-only mods are kept on updates
	Crash         *CrashView `json:"crash,omitempty"`
}

// CrashView explains why a server stopped unexpectedly.
type CrashView struct {
	Mods   []CrashedMod `json:"mods"`   // client-only mods to blame, if any
	Detail string       `json:"detail"` // the exit error
}

// CrashedMod is a mod that crashed the server.
type CrashedMod struct {
	Name string `json:"name"`
	File string `json:"file"` // jar in mods/, when known
}

// stateView is the live state of a server, kept while the app runs.
type stateView struct {
	state      string
	players    []string
	maxPlayers int
	crash      *CrashView
}

// running is a server started from the app. srv is nil while it launches;
// launched is closed once Launch has returned.
type running struct {
	srv      *runtime.Server
	stopping bool
	lines    *lineWriter
	launched chan struct{}
}

// Servers lists the servers the app knows about.
func (s *Service) Servers() []ServerView {
	recs := s.store.Servers()
	out := make([]ServerView, 0, len(recs))
	for _, r := range recs {
		out = append(out, s.view(r))
	}
	return out
}

func (s *Service) view(r ServerRecord) ServerView {
	v := ServerView{
		ID: r.ID, Name: r.Name, Dir: r.Dir, Source: r.Source, MC: r.MC, Loader: r.Loader,
		LoaderVersion: r.LoaderVersion, LogoURL: r.LogoURL, RAMGB: r.RAMGB,
		Port:         serverPort(r.Dir),
		ModsOnServer: countJars(filepath.Join(r.Dir, "mods")),
		State:        StateStopped,
		Players:      []string{},
		MaxPlayers:   maxPlayers(r.Dir),
		KeepClient:   r.SkipClean,
	}
	if v.RAMGB == 0 {
		v.RAMGB = defaultRAMGB()
	}
	s.serversMu.Lock()
	if st := s.states[r.ID]; st != nil {
		v.State = st.state
		v.Players = append(v.Players, st.players...)
		v.Crash = st.crash
	}
	s.serversMu.Unlock()
	return v
}

func (s *Service) emitServer(id string) {
	if rec, ok := s.store.Server(id); ok {
		s.ui.Emit("server", s.view(rec))
	}
}

func (s *Service) isRunning(id string) bool {
	s.serversMu.Lock()
	defer s.serversMu.Unlock()
	return s.running[id] != nil
}

// RunningCount is how many servers are running, asked before the window closes.
func (s *Service) RunningCount() int {
	s.serversMu.Lock()
	defer s.serversMu.Unlock()
	return len(s.running)
}

// SetMemory saves how much memory a server gets from its next start.
func (s *Service) SetMemory(id string, gb int) error {
	if gb < 1 {
		return &Error{Code: "bad_memory"}
	}
	_, err := s.store.UpdateServer(id, func(r *ServerRecord) { r.RAMGB = gb })
	return err
}

// StartServer starts a server; its progress arrives as "server" and
// "server-log" events.
func (s *Service) StartServer(id string) error {
	rec, ok := s.store.Server(id)
	if !ok {
		return &Error{Code: "no_server"}
	}
	s.serversMu.Lock()
	if s.running[id] != nil {
		s.serversMu.Unlock()
		return nil
	}
	// Hold the slot while java launches, so a second click cannot start a
	// second server in the same folder.
	slot := &running{launched: make(chan struct{})}
	s.running[id] = slot
	defer close(slot.launched)
	ring := s.logs[id]
	if ring == nil {
		ring = newLogRing(2000)
		s.logs[id] = ring
	}
	s.states[id] = &stateView{state: StateStarting}
	s.serversMu.Unlock()

	javaPath := rec.JavaPath
	if javaPath == "" {
		javaPath = "java"
	}
	// Unless the user chose a Java, prefer the one setup downloaded into the
	// server folder, as `mcpackctl start` does.
	if !rec.JavaExplicit {
		if local := java.FindLocal(rec.Dir); local != "" {
			javaPath = local
		}
	}
	gb := rec.RAMGB
	if gb == 0 {
		gb = defaultRAMGB()
	}
	ram := fmt.Sprintf("%dG", gb)

	lines := newLineWriter(func(batch []string) {
		ring.add(batch...)
		for _, l := range batch {
			s.watchLine(id, l)
		}
		s.ui.Emit("server-log", map[string]any{"id": id, "lines": batch})
	})
	srv, err := runtime.Launch(rec.Dir, ram, javaPath, lines, lines)
	if err != nil {
		lines.Close()
		s.serversMu.Lock()
		delete(s.running, id)
		s.states[id] = &stateView{state: StateCrashed, crash: &CrashView{Detail: err.Error()}}
		s.serversMu.Unlock()
		s.emitServer(id)
		return userError(err)
	}
	s.serversMu.Lock()
	slot.srv, slot.lines = srv, lines
	s.serversMu.Unlock()
	s.emitServer(id)

	go s.watchExit(id, srv)
	return nil
}

var (
	doneRe   = regexp.MustCompile(`Done \([0-9.,]+s\)! For help`)
	joinRe   = regexp.MustCompile(`: ([A-Za-z0-9_]{2,16}) joined the game`)
	leaveRe  = regexp.MustCompile(`: ([A-Za-z0-9_]{2,16}) left the game`)
	stopRe   = regexp.MustCompile(`Stopping (the )?server`)
	ansiCode = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
)

// watchLine follows the console for the server becoming ready and players
// joining or leaving.
func (s *Service) watchLine(id, line string) {
	changed := false
	s.serversMu.Lock()
	st := s.states[id]
	if st != nil {
		switch {
		case doneRe.MatchString(line):
			st.state, changed = StateRunning, true
		case joinRe.MatchString(line):
			name := joinRe.FindStringSubmatch(line)[1]
			st.players, changed = appendUnique(st.players, name), true
		case leaveRe.MatchString(line):
			name := leaveRe.FindStringSubmatch(line)[1]
			st.players, changed = remove(st.players, name), true
		case stopRe.MatchString(line):
			st.players, changed = nil, true
		}
	}
	s.serversMu.Unlock()
	if changed {
		s.emitServer(id)
	}
}

func (s *Service) watchExit(id string, srv *runtime.Server) {
	err := srv.Wait()
	s.serversMu.Lock()
	run := s.running[id]
	if run == nil || run.srv != srv {
		// Not the server this app is tracking for id.
		s.serversMu.Unlock()
		return
	}
	delete(s.running, id)
	stopping := run.stopping
	st := &stateView{state: StateStopped}
	if err != nil && !stopping {
		st.state = StateCrashed
		st.crash = &CrashView{Detail: err.Error()}
		for _, m := range srv.ClientOnlyMods() {
			name := m.Name
			if name == "" {
				name = m.ModID
			}
			st.crash.Mods = append(st.crash.Mods, CrashedMod{Name: name, File: m.File})
		}
	}
	s.states[id] = st
	s.serversMu.Unlock()
	run.lines.Close()
	logger.Get().Info().Str("server", id).Str("state", st.state).AnErr("exit", err).Msg("server exited")
	s.emitServer(id)
}

// StopServer saves the world and stops the server.
func (s *Service) StopServer(id string) error {
	s.serversMu.Lock()
	run := s.running[id]
	if run != nil {
		run.stopping = true
	}
	s.serversMu.Unlock()
	if run == nil {
		return nil
	}
	// A server still launching is stopped as soon as java is up, so closing
	// the app at that moment cannot leave it running.
	<-run.launched
	s.serversMu.Lock()
	srv := run.srv
	s.serversMu.Unlock()
	if srv == nil {
		return nil // Launch failed; nothing is running.
	}
	err := srv.Stop(stopTimeout)
	<-srv.Done()
	return err
}

// RestartServer stops the server and starts it again.
func (s *Service) RestartServer(id string) error {
	if err := s.StopServer(id); err != nil {
		return userError(err)
	}
	// watchExit has to record the stop before the next start.
	for s.isRunning(id) {
		time.Sleep(50 * time.Millisecond)
	}
	return s.StartServer(id)
}

// StopAll stops every running server, saving their worlds.
func (s *Service) StopAll() {
	s.serversMu.Lock()
	ids := make([]string, 0, len(s.running))
	for id := range s.running {
		ids = append(ids, id)
	}
	s.serversMu.Unlock()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_ = s.StopServer(id)
		}(id)
	}
	wg.Wait()
}

// SendCommand types a command into the server console.
func (s *Service) SendCommand(id, command string) error {
	s.serversMu.Lock()
	run, ring := s.running[id], s.logs[id]
	s.serversMu.Unlock()
	if run == nil || run.srv == nil {
		return &Error{Code: "not_running"}
	}
	command = strings.TrimPrefix(strings.TrimSpace(command), "/")
	ring.add("> " + command)
	s.ui.Emit("server-log", map[string]any{"id": id, "lines": []string{"> " + command}})
	return run.srv.Send(command)
}

// ServerLog returns the latest console lines of a server.
func (s *Service) ServerLog(id string) []string {
	s.serversMu.Lock()
	ring := s.logs[id]
	s.serversMu.Unlock()
	if ring == nil {
		return []string{}
	}
	return ring.lines()
}

// FixCrash turns off the client-only mods that crashed the server and
// starts it again.
func (s *Service) FixCrash(id string) error {
	rec, ok := s.store.Server(id)
	if !ok {
		return &Error{Code: "no_server"}
	}
	s.serversMu.Lock()
	st := s.states[id]
	s.serversMu.Unlock()
	if st == nil || st.crash == nil {
		return s.StartServer(id)
	}
	for _, m := range st.crash.Mods {
		if m.File == "" {
			continue
		}
		if err := disableMod(filepath.Join(rec.Dir, "mods"), m.File); err != nil && !errors.Is(err, os.ErrNotExist) {
			return userError(err)
		}
	}
	return s.StartServer(id)
}

// PublicAddress asks an outside service for this network's internet
// address, which friends elsewhere connect to. It is only called when the
// user asks for it.
func (s *Service) PublicAddress() (string, error) {
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return "", userError(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", userError(err)
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return "", &Error{Code: "network", Detail: "unexpected answer " + strconv.Quote(ip)}
	}
	return ip, nil
}

// ---------------------------------------------------------------------------
// Console lines
// ---------------------------------------------------------------------------

// lineWriter splits process output into lines and hands them over in
// batches, so a server printing thousands of lines at startup does not
// flood the page with events.
type lineWriter struct {
	mu      sync.Mutex
	partial []byte
	pending []string
	flush   func([]string)
	stop    chan struct{}
	once    sync.Once
}

func newLineWriter(flush func([]string)) *lineWriter {
	w := &lineWriter{flush: flush, stop: make(chan struct{})}
	go w.loop()
	return w
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		w.addLine(w.partial[:i])
		w.partial = w.partial[i+1:]
	}
	// Output that never ends a line still gets shown, in pieces.
	if len(w.partial) > maxLineBytes {
		w.addLine(w.partial)
		w.partial = nil
	}
	return len(p), nil
}

// maxLineBytes caps how much output is held while waiting for a newline.
const maxLineBytes = 16 << 10

func (w *lineWriter) addLine(b []byte) {
	line := strings.TrimRight(string(b), "\r")
	w.pending = append(w.pending, ansiCode.ReplaceAllString(line, ""))
}

func (w *lineWriter) take() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	batch := w.pending
	w.pending = nil
	return batch
}

func (w *lineWriter) loop() {
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if batch := w.take(); len(batch) > 0 {
				w.flush(batch)
			}
		case <-w.stop:
			w.mu.Lock()
			if len(w.partial) > 0 {
				w.pending = append(w.pending, string(w.partial))
				w.partial = nil
			}
			w.mu.Unlock()
			if batch := w.take(); len(batch) > 0 {
				w.flush(batch)
			}
			return
		}
	}
}

// Close flushes what is left.
func (w *lineWriter) Close() { w.once.Do(func() { close(w.stop) }) }

// logRing keeps the last n console lines.
type logRing struct {
	mu   sync.Mutex
	n    int
	buf  []string
	next int
	full bool
}

func newLogRing(n int) *logRing { return &logRing{n: n, buf: make([]string, n)} }

func (r *logRing) add(lines ...string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range lines {
		r.buf[r.next] = l
		r.next = (r.next + 1) % r.n
		if r.next == 0 {
			r.full = true
		}
	}
}

func (r *logRing) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return append([]string{}, r.buf[:r.next]...)
	}
	return append(append([]string{}, r.buf[r.next:]...), r.buf[:r.next]...)
}

// ---------------------------------------------------------------------------
// Server folder details
// ---------------------------------------------------------------------------

// serverProperty reads a key from server.properties.
func serverProperty(dir, key string) string {
	f, err := os.Open(filepath.Join(dir, "server.properties"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func serverPort(dir string) int {
	if p, err := strconv.Atoi(serverProperty(dir, "server-port")); err == nil && p > 0 {
		return p
	}
	return 25565
}

func maxPlayers(dir string) int {
	if n, err := strconv.Atoi(serverProperty(dir, "max-players")); err == nil && n > 0 {
		return n
	}
	return 20
}

// lanIP is this PC's address on the local network. No packet is sent: a UDP
// "connection" only picks the outgoing interface.
func lanIP() string {
	conn, err := net.Dial("udp", "192.0.2.1:9")
	if err != nil {
		return ""
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return ""
}

// defaultRAMGB suggests memory for a server: about a third of the PC's
// memory, rounded up to an even number of GB, between 4 and 8 GB. The page
// uses the same rule for the New server form.
func defaultRAMGB() int {
	return suggestRAMGB(systemMemoryGB())
}

func suggestRAMGB(totalGB int) int {
	if totalGB == 0 {
		return 6
	}
	gb := totalGB / 3
	gb += gb % 2
	return max(4, min(8, gb))
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func remove(list []string, v string) []string {
	out := list[:0]
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
