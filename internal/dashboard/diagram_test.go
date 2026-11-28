package dashboard

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

const sampleDiagram = "@startuml\nstate \"Ended\" as E\nstate \"Live\" as L\nE --> L : follow up\nnote right of E\n  **A way out.**\nend note\n@enduml\n"

func fakePlantUML(t *testing.T, fn func(format string) ([]byte, []byte, error)) *[]string {
	t.Helper()
	was := plantUML
	var calls []string
	plantUML = func(_, _, format string) ([]byte, []byte, error) { calls = append(calls, format); return fn(format) }
	sketchesMu.Lock()
	sketches = map[string]sketchResult{}
	sketchesMu.Unlock()
	t.Cleanup(func() { plantUML = was })
	return &calls
}

func diagramModel(t *testing.T) *dashModel {
	t.Helper()
	m := sampleModel(120, 40, ui.GlyphsSafe)
	m.preview, m.focus, m.previewPath = true, 2, "_spec/flow.puml"
	m.previewRequest = 7
	return m
}

func TestADiagramShowsItsSketchAndSTogglesTheSource(t *testing.T) {
	calls := fakePlantUML(t, func(string) ([]byte, []byte, error) {
		return []byte("┌─────┐   ┌──────────────┐\n│Ended│---│**A way out.**│\n└─────┘   └──────────────┘\n"), nil, nil
	})
	m := diagramModel(t)
	_, cmd := m.Update(previewEvent{7, newPreviewDoc("_spec/flow.puml", Sides{Before: sampleDiagram, After: sampleDiagram})})
	if cmd == nil {
		t.Fatal("a diagram asked for no sketch")
	}
	if view := ansi.Strip(strings.Join(m.renderPreview(80, 10), "\n")); !strings.Contains(view, "drawing a sketch") {
		t.Errorf("the pane while plantuml runs:\n%s", view)
	}
	m.Update(cmd())
	view := ansi.Strip(strings.Join(m.renderPreview(90, 10), "\n"))
	for _, want := range []string{"sketch · plantuml -tutxt drops arrow labels", "│Ended│", "│A way out.    │"} {
		if !strings.Contains(view, want) {
			t.Errorf("the sketch lacks %q:\n%s", want, view)
		}
	}
	if foot := ansi.Strip(m.footer(160)); !strings.Contains(foot, "s source") || !strings.Contains(foot, "o open SVG") {
		t.Errorf("footer = %q", foot)
	}
	m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	if view := ansi.Strip(strings.Join(m.renderPreview(90, 10), "\n")); !strings.Contains(view, "@startuml") || strings.Contains(view, "│Ended│") {
		t.Errorf("s did not show the source:\n%s", view)
	}
	m.diagramSource = false
	m.Update(previewEvent{7, newPreviewDoc("_spec/flow.puml", Sides{Before: sampleDiagram, After: sampleDiagram})})
	if _, again := m.Update(sketchEvent{7, "_spec/flow.puml", nil, ""}); again != nil || len(*calls) != 1 {
		t.Errorf("plantuml ran %d times; the second open reads the cache", len(*calls))
	}
	if m.sketch(1, newPreviewDoc("main.go", Sides{After: "package main"})) != nil {
		t.Error("a Go file asked for a sketch")
	}
}

func TestADiagramPlantUMLCannotDrawFallsBackToItsSourceAndSaysWhy(t *testing.T) {
	fakePlantUML(t, func(string) ([]byte, []byte, error) {
		return []byte("Exception in thread \"main\" java.lang.UnsupportedOperationException: net.sourceforge.plantuml.sequencediagram.teoz.NoteTile\n\tat x"), nil, errors.New("exit status 1")
	})
	m := diagramModel(t)
	_, cmd := m.Update(previewEvent{7, newPreviewDoc("_spec/flow.puml", Sides{After: sampleDiagram})})
	m.Update(cmd())
	view := ansi.Strip(strings.Join(m.renderPreview(160, 10), "\n"))
	if !strings.Contains(view, "file · no sketch: plantuml crashed: UnsupportedOperationException in NoteTile") || !strings.Contains(view, "@startuml") {
		t.Errorf("a crash does not fall back to the source with its reason:\n%s", view)
	}
	if got := whyNoSketch(nil, []byte("ERROR\n4\nSyntax Error? (Assumed diagram type: state)\n"), errors.New("exit status 200")); got != "line 4: Syntax Error? (Assumed diagram type: state)" {
		t.Errorf("syntax error = %q", got)
	}
	if got := whyNoSketch(nil, nil, errors.New("plantuml is not on PATH")); got != "plantuml is not on PATH" {
		t.Errorf("missing plantuml = %q", got)
	}
}

func TestODrawsTheDiagramAsSVGForTheSystemViewer(t *testing.T) {
	calls := fakePlantUML(t, func(format string) ([]byte, []byte, error) {
		return []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>"), nil, nil
	})
	m := diagramModel(t)
	var opened string
	m.opener = func(p string) error { opened = p; return nil }
	m.Update(previewEvent{7, newPreviewDoc("_spec/flow.puml", Sides{After: sampleDiagram})})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	if cmd == nil {
		t.Fatal("o rendered nothing")
	}
	m.Update(cmd())
	b, err := os.ReadFile(opened)
	if err != nil || !strings.HasPrefix(string(b), "<svg") || !strings.HasSuffix(opened, ".svg") || (*calls)[len(*calls)-1] != "-tsvg" {
		t.Errorf("opened %q (%v): %q; calls %q", opened, err, b, *calls)
	}
	if !strings.Contains(m.status, "Opened flow.svg") {
		t.Errorf("status = %q", m.status)
	}
}

func TestRealPlantUMLDrawsASketch(t *testing.T) {
	if _, err := exec.LookPath("plantuml"); err != nil {
		t.Skip("plantuml is not on PATH")
	}
	r := sketchOf(t.TempDir(), "@startuml\nA -> B : hello\n@enduml\n")
	if r.note != "" || !strings.Contains(strings.Join(r.rows, "\n"), "hello") {
		t.Errorf("sketch = %q, note %q", r.rows, r.note)
	}
}
