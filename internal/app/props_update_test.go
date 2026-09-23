package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"
)

func TestServerPropertiesRoundTrip(t *testing.T) {
	s, _ := newTestService(t)
	rec := addServer(t, s)
	path := filepath.Join(rec.Dir, "server.properties")
	writeFile(t, path, "#Minecraft server properties\r\nonline-mode=true\r\nlevel-name=world\r\nmotd=A Minecraft Server\r\n")

	p, err := s.ServerProperties(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !p.OnlineMode || p.MaxPlayers != 20 || p.Gamemode != "survival" || p.MOTD != "A Minecraft Server" {
		t.Fatalf("defaults read wrong: %+v", p)
	}

	p.OnlineMode = false
	p.MaxPlayers = 8
	p.Gamemode = "creative"
	p.Difficulty = "hard"
	p.AllowFlight = true
	p.MOTD = "Máy chủ của Linh"
	if err := s.SetServerProperties(rec.ID, *p); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	text := string(data)
	for _, want := range []string{"#Minecraft server properties", "level-name=world", "online-mode=false", "max-players=8", "allow-flight=true", `motd=M\u00e1y ch\u1ee7 c\u1ee7a Linh`} {
		if !strings.Contains(text, want) {
			t.Errorf("server.properties lacks %q:\n%s", want, text)
		}
	}
	got, err := s.ServerProperties(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, p) {
		t.Errorf("read back %+v, want %+v", got, p)
	}
	if s.view(mustServer(t, s, rec.ID)).MaxPlayers != 8 {
		t.Error("server view does not show the new max players")
	}
}

func TestSetServerPropertiesRejectsBadValues(t *testing.T) {
	s, _ := newTestService(t)
	rec := addServer(t, s)
	p := defaultProps
	p.Gamemode = "spectator"
	var ue *Error
	if err := s.SetServerProperties(rec.ID, p); !errors.As(err, &ue) || ue.Code != "bad_setting" {
		t.Errorf("SetServerProperties(spectator) = %v, want bad_setting", err)
	}
}

func TestPropertyEscaping(t *testing.T) {
	for _, v := range []string{"Máy chủ", "emoji 🎮 ok", `back\slash`, " leading space", "plain"} {
		if got := unescapeProperty(escapeProperty(v)); got != v {
			t.Errorf("round trip %q -> %q -> %q", v, escapeProperty(v), got)
		}
	}
	// Newer servers write UTF-8 directly; that reads back as is.
	if got := unescapeProperty("Máy chủ"); got != "Máy chủ" {
		t.Errorf("raw UTF-8 read as %q", got)
	}
}

func TestExistingDirAndServerDirIn(t *testing.T) {
	s, _ := newTestService(t)
	base := t.TempDir()
	if got := existingDir(filepath.Join(base, "not", "made", "yet")); got != base {
		t.Errorf("existingDir = %q, want %q", got, base)
	}
	empty := filepath.Join(base, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := s.ServerDirIn(empty, "DeceasedCraft"); got != empty {
		t.Errorf("empty folder: ServerDirIn = %q, want the folder itself", got)
	}
	writeFile(t, filepath.Join(base, "notes.txt"), "x")
	if got := s.ServerDirIn(base, "DeceasedCraft"); got != filepath.Join(base, "DeceasedCraft") {
		t.Errorf("busy folder: ServerDirIn = %q, want a subfolder", got)
	}
}

// fakeRelease serves a GitHub "latest release" with an AutoPack build for
// this OS and its SHA256SUMS.txt.
func fakeRelease(t *testing.T, tag string, exe []byte, corrupt bool) *httptest.Server {
	t.Helper()
	name := fmt.Sprintf("AutoPack-%s-%s-%s.exe", tag, goruntime.GOOS, goruntime.GOARCH)
	sum := sha256.Sum256(exe)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://example.com/r","assets":[
				{"name":%q,"browser_download_url":"%s/exe","size":%d},
				{"name":"SHA256SUMS.txt","browser_download_url":"%s/sums","size":1}]}`,
				tag, name, srv.URL, len(exe), srv.URL)
		case "/exe":
			if corrupt {
				w.Write(append([]byte{}, exe[:len(exe)-1]...))
				w.Write([]byte{'!'})
				return
			}
			w.Write(exe)
		case "/sums":
			fmt.Fprintf(w, "%s  mcpackctl-%s-linux-amd64\n%s  %s\n", strings.Repeat("0", 64), tag, hex.EncodeToString(sum[:]), name)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func updateService(t *testing.T, version, api string) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "AutoPack.exe")
	writeFile(t, exe, "old build")
	s, err := New(Config{
		DataDir: filepath.Join(dir, "data"), Version: version,
		UpdateRepo: "owner/repo", UpdateAPI: api, ExePath: exe,
	}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	return s, exe
}

func TestUpdateDownloadsVerifiesAndSwaps(t *testing.T) {
	srv := fakeRelease(t, "v0.3.0", []byte("new build"), false)
	s, exe := updateService(t, "v0.2.1", srv.URL)

	info, err := s.CheckForUpdate()
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || info.Latest != "v0.3.0" || info.Dev {
		t.Fatalf("info = %+v", info)
	}
	if err := s.DownloadUpdate(); err != nil {
		t.Fatal(err)
	}
	path, err := s.ApplyUpdate()
	if err != nil {
		t.Fatal(err)
	}
	if path != exe {
		t.Errorf("ApplyUpdate = %q, want %q", path, exe)
	}
	if data, _ := os.ReadFile(exe); string(data) != "new build" {
		t.Errorf("exe holds %q after the update", data)
	}
	if data, _ := os.ReadFile(exe + ".old"); string(data) != "old build" {
		t.Errorf("old build not kept aside: %q", data)
	}
	CleanUpAfterUpdate(exe)
	if exists(exe + ".old") {
		t.Error("the old build was not cleaned up")
	}
}

func TestUpdateRejectsWrongChecksum(t *testing.T) {
	srv := fakeRelease(t, "v0.3.0", []byte("new build"), true)
	s, exe := updateService(t, "v0.2.1", srv.URL)
	if _, err := s.CheckForUpdate(); err != nil {
		t.Fatal(err)
	}
	var ue *Error
	if err := s.DownloadUpdate(); !errors.As(err, &ue) || ue.Code != "update_bad_file" {
		t.Fatalf("DownloadUpdate = %v, want update_bad_file", err)
	}
	if exists(exe + ".download") {
		t.Error("a bad download was left behind")
	}
	if _, err := s.ApplyUpdate(); err == nil {
		t.Error("ApplyUpdate installed an unverified file")
	}
}

func TestNoUpdateForSameOrDevVersion(t *testing.T) {
	srv := fakeRelease(t, "v0.3.0", []byte("x"), false)
	s, _ := updateService(t, "v0.3.0", srv.URL)
	if info, err := s.CheckForUpdate(); err != nil || info.Available {
		t.Errorf("same version: info %+v err %v", info, err)
	}
	dev, _ := updateService(t, "dev", srv.URL)
	if info, err := dev.CheckForUpdate(); err != nil || !info.Dev || info.Available {
		t.Errorf("dev build: info %+v err %v", info, err)
	}
}

func TestParseVersionAndNewer(t *testing.T) {
	v := func(s string) [3]int {
		p, ok := parseVersion(s)
		if !ok {
			t.Fatalf("parseVersion(%q) failed", s)
		}
		return p
	}
	if !newer(v("v0.10.0"), v("v0.9.9")) || newer(v("v1.0.0"), v("v1.0.0")) || !newer(v("v1.0.1-rc1"), v("1.0.0")) {
		t.Error("version comparison is wrong")
	}
	if _, ok := parseVersion("0.1.0-dev.3+abc"); !ok {
		t.Error("suffix not ignored")
	}
	if _, ok := parseVersion("dev"); ok {
		t.Error("dev parsed as a version")
	}
}

func mustServer(t *testing.T, s *Service, id string) ServerRecord {
	t.Helper()
	rec, ok := s.store.Server(id)
	if !ok {
		t.Fatalf("no server %s", id)
	}
	return rec
}

func TestAdvancedPropertiesAreReadAndWritten(t *testing.T) {
	s, _ := newTestService(t)
	rec := addServer(t, s)
	path := filepath.Join(rec.Dir, "server.properties")
	writeFile(t, path, "#comment\nview-distance=10\nnetwork-compression-threshold=256\nspawn-protection=16\nsome-mod-key=abc\n")

	p, err := s.ServerProperties(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.SpawnProtection != 16 || p.Other["view-distance"] != "10" || p.Other["some-mod-key"] != "abc" {
		t.Fatalf("read %+v", p)
	}
	if _, ok := p.Other["spawn-protection"]; ok {
		t.Error("a basic key is also listed among the advanced ones")
	}

	p.SpawnProtection = 0
	p.Other["network-compression-threshold"] = "-1"
	p.Other["enable-command-block"] = "true" // not in the file yet
	p.Other["online-mode"] = "false"         // basic keys cannot be set this way
	if err := s.SetServerProperties(rec.ID, *p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	for _, want := range []string{"#comment", "view-distance=10", "network-compression-threshold=-1", "enable-command-block=true", "spawn-protection=0", "some-mod-key=abc", "online-mode=true"} {
		if !strings.Contains(text, want) {
			t.Errorf("server.properties lacks %q:\n%s", want, text)
		}
	}

	var ue *Error
	for _, bad := range []map[string]string{{"bad key": "x"}, {"motd-extra": "line\nbreak"}, {"": "x"}} {
		q := *p
		q.Other = bad
		if err := s.SetServerProperties(rec.ID, q); !errors.As(err, &ue) || ue.Code != "bad_setting" {
			t.Errorf("SetServerProperties(%q) = %v, want bad_setting", bad, err)
		}
	}
}
