package setup

import (
	"fmt"
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"

	"github.com/bhhoang/AutoPackMC/internal/downloader"
	"github.com/bhhoang/AutoPackMC/pkg/logger"
)

// failedModsPageName is the report written next to the generated server.
const failedModsPageName = "failed-mods.html"

// reportFailedMods prints every mod that could not be downloaded automatically,
// along with the CurseForge page it can be fetched from by hand, and writes an
// HTML page that opens all of those pages at once. It never fails the run: a
// broken report must not hide the mods it is reporting.
func reportFailedMods(failed []downloader.FailedMod, reportDir, modsDir string) {
	if len(failed) == 0 {
		return
	}

	log := logger.Get()

	log.Error().
		Int("count", len(failed)).
		Str("modsDir", modsDir).
		Msg("the following mods could not be downloaded and must be installed manually")

	for _, f := range failed {
		event := log.Error().
			Int("projectID", f.ProjectID).
			Int("fileID", f.FileID)
		if f.FileName != "" {
			event = event.Str("filename", f.FileName)
		}
		if f.Err != nil {
			event = event.Str("reason", f.Err.Error())
		}
		event.Str("url", f.PageURL).Msg(modLabel(f))
	}

	pagePath, err := writeFailedModsPage(failed, reportDir, modsDir)
	if err != nil {
		log.Warn().Err(err).Msg("could not write the manual download page")
		return
	}

	// Always print the path: it is the fallback for terminals that do not
	// render OSC 8 hyperlinks, and for logs piped to a file.
	log.Error().Str("page", pagePath).Msg("this page links to every mod above")

	if isatty.IsTerminal(os.Stderr.Fd()) || isatty.IsCygwinTerminal(os.Stderr.Fd()) {
		log.Error().Msgf("%s  (then drop the JARs into %s)",
			terminalHyperlink(fileURL(pagePath), "Open all the mods"), modsDir)
	}
}

// modLabel describes a failed mod in a single line.
func modLabel(f downloader.FailedMod) string {
	if f.Name != "" {
		return f.Name
	}
	if f.FileName != "" {
		return f.FileName
	}
	return fmt.Sprintf("project %d", f.ProjectID)
}

// terminalHyperlink wraps label in an OSC 8 hyperlink pointing at target.
func terminalHyperlink(target, label string) string {
	const (
		osc = "\x1b]8;;"
		st  = "\x1b\\"
	)
	return osc + target + st + label + osc + st
}

// fileURL converts a local path into a file:// URL usable from a terminal or
// browser, escaping spaces and other path characters.
func fileURL(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slashed := filepath.ToSlash(abs)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed // Windows drive paths: C:/... -> /C:/...
	}
	u := url.URL{Scheme: "file", Path: slashed}
	return u.String()
}

// writeFailedModsPage renders the manual-download page into reportDir and
// returns its path.
func writeFailedModsPage(failed []downloader.FailedMod, reportDir, modsDir string) (string, error) {
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(reportDir, failedModsPageName)
	if err := os.WriteFile(path, []byte(buildFailedModsPage(failed, modsDir)), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

type failedModView struct {
	downloader.FailedMod
	Label  string
	Reason string
}

var failedModsTemplate = template.Must(template.New("failed-mods").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Mods to download manually</title>
<style>
 body { font-family: system-ui, sans-serif; margin: 2rem auto; max-width: 60rem; color: #1b1b1f; }
 h1 { font-size: 1.4rem; }
 p.hint { color: #55555f; }
 code { background: #f2f2f5; padding: .1rem .3rem; border-radius: .2rem; }
 button { font-size: 1rem; padding: .6rem 1rem; border: 0; border-radius: .4rem;
          background: #f16436; color: #fff; cursor: pointer; }
 table { border-collapse: collapse; width: 100%; margin-top: 1.5rem; }
 th, td { text-align: left; padding: .5rem .6rem; border-bottom: 1px solid #e3e3e8; vertical-align: top; }
 td.reason { color: #8a3324; font-size: .85rem; }
</style>
</head>
<body>
<h1>{{len .Mods}} mod(s) could not be downloaded automatically</h1>
<p class="hint">Download each JAR from CurseForge and drop it into <code>{{.ModsDir}}</code>.</p>
<p><button onclick="openAll()">Open all the mods</button>
   <span class="hint">(your browser may ask to allow pop-ups)</span></p>
<table>
<tr><th>Mod</th><th>Project / File</th><th>Why it failed</th><th>Download</th></tr>
{{range .Mods}}<tr>
  <td>{{.Label}}</td>
  <td>{{.ProjectID}} / {{.FileID}}</td>
  <td class="reason">{{.Reason}}</td>
  <td><a href="{{.PageURL}}" target="_blank" rel="noreferrer">CurseForge page</a></td>
</tr>
{{end}}</table>
<script>
const urls = [
{{- range .Mods}}
  {{.PageURL}},
{{- end}}
];
function openAll() {
  for (const url of urls) { window.open(url, '_blank', 'noreferrer'); }
}
</script>
</body>
</html>
`))

// buildFailedModsPage renders the HTML report for the failed mods.
func buildFailedModsPage(failed []downloader.FailedMod, modsDir string) string {
	mods := make([]failedModView, 0, len(failed))
	for _, f := range failed {
		reason := ""
		if f.Err != nil {
			reason = f.Err.Error()
		}
		mods = append(mods, failedModView{FailedMod: f, Label: modLabel(f), Reason: reason})
	}

	var sb strings.Builder
	data := struct {
		Mods    []failedModView
		ModsDir string
	}{Mods: mods, ModsDir: modsDir}
	if err := failedModsTemplate.Execute(&sb, data); err != nil {
		// Templates are static, so this cannot fail in practice; degrade to a
		// plain list rather than losing the URLs.
		var plain strings.Builder
		plain.WriteString("<!DOCTYPE html><meta charset=\"utf-8\"><ul>\n")
		for _, m := range mods {
			fmt.Fprintf(&plain, "<li><a href=%q>%s</a></li>\n", m.PageURL, template.HTMLEscapeString(m.Label))
		}
		plain.WriteString("</ul>\n")
		return plain.String()
	}
	return sb.String()
}
