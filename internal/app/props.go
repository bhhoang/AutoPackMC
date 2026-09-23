package app

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// ServerProps are the server.properties settings shown on the Server
// settings tab.
type ServerProps struct {
	OnlineMode  bool   `json:"onlineMode"` // only accounts Mojang has verified can join
	MaxPlayers  int    `json:"maxPlayers"`
	Gamemode    string `json:"gamemode"`    // survival, creative or adventure
	Difficulty  string `json:"difficulty"`  // peaceful, easy, normal or hard
	PVP         bool   `json:"pvp"`         // players can hurt each other
	AllowFlight bool   `json:"allowFlight"` // flying (from mods) does not get players kicked
	MOTD        string `json:"motd"`        // the name shown in the multiplayer list
	// SpawnProtection is the radius around spawn where only ops can build.
	SpawnProtection int `json:"spawnProtection"`
	// Other holds every other key in server.properties, for the Advanced
	// settings. Saving writes the keys it holds and keeps the rest.
	Other map[string]string `json:"other"`
}

// basicKeys are the keys ServerProps has fields for.
var basicKeys = map[string]bool{
	"online-mode": true, "max-players": true, "gamemode": true, "difficulty": true,
	"pvp": true, "allow-flight": true, "motd": true, "spawn-protection": true,
}

// propertyKey is what a server.properties key may look like.
var propertyKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)

// Minecraft's defaults, used for keys a server.properties does not set.
var defaultProps = ServerProps{
	OnlineMode: true, MaxPlayers: 20, Gamemode: "survival", Difficulty: "easy",
	PVP: true, AllowFlight: false, MOTD: "A Minecraft Server", SpawnProtection: 16,
}

var (
	gamemodes    = map[string]bool{"survival": true, "creative": true, "adventure": true}
	difficulties = map[string]bool{"peaceful": true, "easy": true, "normal": true, "hard": true}
)

// ServerProperties reads a server's settings.
func (s *Service) ServerProperties(id string) (*ServerProps, error) {
	rec, ok := s.store.Server(id)
	if !ok {
		return nil, &Error{Code: "no_server"}
	}
	values, err := readProperties(filepath.Join(rec.Dir, "server.properties"))
	if err != nil {
		return nil, userError(err)
	}
	p := defaultProps
	boolOf := func(key string, into *bool) {
		if v, ok := values[key]; ok {
			*into = strings.EqualFold(v, "true")
		}
	}
	boolOf("online-mode", &p.OnlineMode)
	boolOf("pvp", &p.PVP)
	boolOf("allow-flight", &p.AllowFlight)
	if n, err := strconv.Atoi(values["max-players"]); err == nil && n > 0 {
		p.MaxPlayers = n
	}
	if v := strings.ToLower(values["gamemode"]); gamemodes[v] {
		p.Gamemode = v
	}
	if v := strings.ToLower(values["difficulty"]); difficulties[v] {
		p.Difficulty = v
	}
	if v, ok := values["motd"]; ok {
		p.MOTD = v
	}
	if n, err := strconv.Atoi(values["spawn-protection"]); err == nil && n >= 0 {
		p.SpawnProtection = n
	}
	p.Other = map[string]string{}
	for k, v := range values {
		if !basicKeys[k] {
			p.Other[k] = v
		}
	}
	return &p, nil
}

// SetServerProperties saves a server's settings. Other lines of
// server.properties, including comments, are kept. The server uses them from
// its next start.
func (s *Service) SetServerProperties(id string, p ServerProps) error {
	rec, ok := s.store.Server(id)
	if !ok {
		return &Error{Code: "no_server"}
	}
	if p.MaxPlayers < 1 || p.MaxPlayers > 1000 || !gamemodes[p.Gamemode] || !difficulties[p.Difficulty] ||
		p.SpawnProtection < 0 || p.SpawnProtection > 100000 {
		return &Error{Code: "bad_setting"}
	}
	set := map[string]string{}
	for k, v := range p.Other {
		if basicKeys[k] {
			continue // the typed fields win
		}
		if !propertyKey.MatchString(k) || strings.ContainsAny(v, "\r\n") || len(v) > 4096 {
			return &Error{Code: "bad_setting", Detail: k}
		}
		set[k] = v
	}
	motd := strings.Join(strings.Fields(strings.ReplaceAll(p.MOTD, "\n", " ")), " ")
	if len([]rune(motd)) > 59 {
		motd = string([]rune(motd)[:59])
	}
	for k, v := range map[string]string{
		"online-mode":      strconv.FormatBool(p.OnlineMode),
		"max-players":      strconv.Itoa(p.MaxPlayers),
		"gamemode":         p.Gamemode,
		"difficulty":       p.Difficulty,
		"pvp":              strconv.FormatBool(p.PVP),
		"allow-flight":     strconv.FormatBool(p.AllowFlight),
		"motd":             motd,
		"spawn-protection": strconv.Itoa(p.SpawnProtection),
	} {
		set[k] = v
	}
	err := updateProperties(filepath.Join(rec.Dir, "server.properties"), set)
	if err != nil {
		return userError(err)
	}
	s.emitServer(id)
	return nil
}

// OpenServerProperties opens server.properties in the default editor, for
// settings the tab does not show.
func (s *Service) OpenServerProperties(id string) {
	if rec, ok := s.store.Server(id); ok {
		s.ui.Open(filepath.Join(rec.Dir, "server.properties"))
	}
}

// readProperties reads key=value lines, unescaping values the way Java's
// Properties does for the escapes Minecraft writes. A missing file has no
// values.
func readProperties(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	values := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, value, ok := splitProperty(sc.Text())
		if ok {
			values[key] = unescapeProperty(value)
		}
	}
	return values, sc.Err()
}

// updateProperties sets keys in a properties file, changing their lines in
// place and adding missing ones at the end. The file is replaced atomically.
func updateProperties(path string, set map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}
	done := map[string]bool{}
	for i, line := range lines {
		key, _, ok := splitProperty(line)
		if v, want := set[key]; ok && want {
			lines[i] = key + "=" + escapeProperty(v)
			done[key] = true
		}
	}
	for _, key := range sortedKeys(set) {
		if !done[key] {
			lines = append(lines, key+"="+escapeProperty(set[key]))
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// splitProperty splits a key=value (or key:value) line. Comments and blank
// lines report ok false.
func splitProperty(line string) (key, value string, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" || trimmed[0] == '#' || trimmed[0] == '!' {
		return "", "", false
	}
	i := strings.IndexAny(trimmed, "=:")
	if i < 0 {
		return strings.TrimSpace(trimmed), "", true
	}
	return strings.TrimSpace(trimmed[:i]), strings.TrimLeft(trimmed[i+1:], " \t"), true
}

// escapeProperty writes a value the way Java's Properties.store does, so
// servers that read the file as Latin-1 still get non-ASCII text (such as
// Vietnamese) right.
func escapeProperty(v string) string {
	var b strings.Builder
	for i, r := range v {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == ' ' && i == 0:
			b.WriteString(`\ `)
		case r < 0x20 || r > 0x7e:
			for _, u := range utf16.Encode([]rune{r}) {
				fmt.Fprintf(&b, `\u%04x`, u)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// unescapeProperty undoes escapeProperty and the other escapes Java's
// Properties uses.
func unescapeProperty(v string) string {
	var b strings.Builder
	var units []uint16
	flush := func() {
		if len(units) > 0 {
			b.WriteString(string(utf16.Decode(units)))
			units = units[:0]
		}
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c != '\\' || i+1 == len(v) {
			flush()
			b.WriteByte(c)
			continue
		}
		i++
		switch v[i] {
		case 'u':
			if i+4 < len(v) {
				if n, err := strconv.ParseUint(v[i+1:i+5], 16, 16); err == nil {
					units = append(units, uint16(n))
					i += 4
					continue
				}
			}
			flush()
			b.WriteByte('u')
		case 't':
			flush()
			b.WriteByte('\t')
		case 'n':
			flush()
			b.WriteByte('\n')
		default:
			flush()
			b.WriteByte(v[i])
		}
	}
	flush()
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
