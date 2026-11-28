// SPEC: _spec/_experiments/dashboard/diagram-preview.puml
package dashboard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// isDiagram is true for the PlantUML sources the preview draws as a sketch.
func isDiagram(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".puml", ".plantuml", ".pu", ".iuml", ".wsd":
		return true
	}
	return false
}

type sketchEvent struct {
	request uint64
	path    string
	rows    []string
	note    string
}

type sketchResult struct {
	rows []string
	note string
}

var (
	sketchesMu sync.Mutex
	sketches   = map[string]sketchResult{}
	boldMarkup = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	errorLine  = regexp.MustCompile(`(?m)^ERROR\n([0-9]+)\n(.+)$`)
	exception  = regexp.MustCompile(`([A-Za-z]+Exception)(?:: ([^\n]+))?`)
)

// plantUML runs plantuml on src from dir with the given output flag; a missing plantuml or a timeout is an error.
var plantUML = func(dir, src, format string) ([]byte, []byte, error) {
	bin, err := exec.LookPath("plantuml")
	if err != nil {
		return nil, nil, errors.New("plantuml is not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, format, "-pipe", "-charset", "UTF-8")
	cmd.Dir, cmd.Stdin = dir, strings.NewReader(src)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	if ctx.Err() != nil {
		err = errors.New("plantuml took over 30 s")
	}
	return out.Bytes(), errOut.Bytes(), err
}

// sketch draws an opened diagram with `plantuml -tutxt` in the background; the source shows until it lands.
func (m *dashModel) sketch(request uint64, d previewDoc) tea.Cmd {
	if !isDiagram(d.path) || d.raw == "" {
		return nil
	}
	dir, path, src := m.diagramDir(d.path), d.path, d.raw
	return func() tea.Msg {
		r := sketchOf(dir, src)
		return sketchEvent{request: request, path: path, rows: r.rows, note: r.note}
	}
}

func sketchOf(dir, src string) sketchResult {
	key := digestOf(dir + "\x00" + src)
	sketchesMu.Lock()
	r, ok := sketches[key]
	sketchesMu.Unlock()
	if ok {
		return r
	}
	out, errOut, err := plantUML(dir, src, "-tutxt")
	if err != nil {
		r.note = whyNoSketch(out, errOut, err)
	} else {
		for _, line := range strings.Split(strings.TrimRight(cleanCode(string(out)), "\n "), "\n") {
			r.rows = append(r.rows, boldMarkup.ReplaceAllStringFunc(strings.TrimRight(line, " "), func(s string) string {
				text := strings.Trim(s, "*")
				return inkText.Bold(true).Render(text) + "    "
			}))
		}
	}
	sketchesMu.Lock()
	sketches[key] = r
	sketchesMu.Unlock()
	return r
}

// whyNoSketch names plantuml's complaint in one line: the source line it stopped at, the exception, or the error.
func whyNoSketch(out, errOut []byte, err error) string {
	all := string(errOut) + "\n" + string(out)
	if m := errorLine.FindStringSubmatch(all); m != nil {
		return "line " + m[1] + ": " + safeText(m[2])
	}
	if m := exception.FindStringSubmatch(all); m != nil {
		why := m[1]
		if at := strings.TrimPrefix(filepath.Ext(strings.TrimSpace(m[2])), "."); at != "" {
			why += " in " + at
		}
		return "plantuml crashed: " + safeText(why)
	}
	if line := lastLine(string(errOut)); line != "" && !strings.Contains(err.Error(), "PATH") {
		return line
	}
	return safeText(err.Error())
}

// diagramDir is where a diagram's relative !include lines resolve: its folder in the task's checkout.
func (m *dashModel) diagramDir(path string) string {
	base := m.task().Repo
	if base == "" {
		base = m.CWD
	}
	if dir := filepath.Join(base, filepath.Dir(path)); dirExists(dir) {
		return dir
	}
	return base
}

func dirExists(dir string) bool {
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// openDiagram renders the open diagram as SVG and hands it to the OS viewer.
func (m *dashModel) openDiagram() tea.Cmd {
	d := m.previewDoc
	if !isDiagram(d.path) || d.raw == "" {
		return nil
	}
	dir, src, name := m.diagramDir(d.path), d.raw, strings.TrimSuffix(filepath.Base(d.path), filepath.Ext(d.path))
	opener := m.opener
	if opener == nil {
		opener = osOpen
	}
	m.say("Rendering " + filepath.Base(d.path) + " as SVG…")
	return func() tea.Msg {
		label := name + ".svg"
		out, errOut, err := plantUML(dir, src, "-tsvg")
		if err != nil {
			return externalEvent{label: label, err: errors.New(whyNoSketch(out, errOut, err))}
		}
		file := filepath.Join(os.TempDir(), "proveo-diagrams", name+"-"+digestOf(src)[:8]+".svg")
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			return externalEvent{label: label, err: err}
		}
		if err := os.WriteFile(file, out, 0o600); err != nil {
			return externalEvent{label: label, err: err}
		}
		return externalEvent{label: label, err: opener(file)}
	}
}

// sketchLines is the sketch's rows [from, from+n), w cells wide.
func (d previewDoc) sketchLines(from, n, w int) []string {
	if d.sketch == nil {
		return []string{muted.Render("drawing a sketch with plantuml -tutxt…")}
	}
	var out []string
	for _, row := range d.sketch[min(from, len(d.sketch)):min(len(d.sketch), from+n)] {
		out = append(out, ansi.Truncate(row, max(1, w), "…"))
	}
	return out
}
