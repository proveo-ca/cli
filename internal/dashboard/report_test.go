package dashboard

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

const sampleReport = "# Review: acme/app#3995 — Hold E2E on draft PRs\n\n" +
	"Read-only review. Nothing posted.\n\n" +
	"## Findings\n\n" +
	"| # | Sev | Location | Finding |\n|---|-----|----------|---------|\n" +
	"| F1 | [HIGH] | `.github/workflows/ci.yml:139` | The authz test left core. |\n" +
	"| F2 | [MEDIUM] | `apps/web/Dockerfile:103` | The cache mount is shared. |\n\n" +
	"```go\nfunc main() {}\n```\n\n" +
	"![before and after](shot.png)\n"

func reportModel(t *testing.T) (*dashModel, *Task) {
	t.Helper()
	dir := t.TempDir()
	report := filepath.Join(dir, "run.report.md")
	if err := os.WriteFile(report, []byte(sampleReport), 0o600); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := range 40 {
		for y := range 20 {
			img.Set(x, y, color.RGBA{R: uint8(x * 6), G: 90, B: uint8(y * 12), A: 255})
		}
	}
	f, err := os.Create(filepath.Join(dir, "shot.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	m, ended := endedModel(t)
	ended.Outcome, ended.Report, ended.Agent = "done", report, "claudecode"
	m.changes = []Change{{Kind: "M", Path: ".github/workflows/ci.yml"}, {Kind: "M", Path: "apps/web/Dockerfile"}}
	m.tree = nil
	return m, ended
}

func TestAFinishedRunLeadsWithItsReport(t *testing.T) {
	m, ended := reportModel(t)
	if actions := ansi.Strip(m.cardActions(*ended, true)); !strings.HasPrefix(actions, "↵ report   s follow up   x clean up") {
		t.Errorf("actions = %q", actions)
	}
	if foot := ansi.Strip(m.footer(160)); !strings.Contains(foot, "↵ Report") || !strings.Contains(foot, "s follow up") {
		t.Errorf("footer = %q", foot)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.report == nil || m.focus != 2 || m.confirm != nil {
		t.Fatalf("enter did not open the report: report %v focus %d", m.report != nil, m.focus)
	}
	pane := ansi.Strip(strings.Join(m.renderReport(90, 60), "\n"))
	for _, want := range []string{"run.report.md · report · 3 references", "Review: acme/app#3995", "F1", "func main() {}", "Image: before and after"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the report pane lacks %q:\n%s", want, pane)
		}
	}
	if !strings.Contains(pane, "▀") && !strings.Contains(pane, "▄") && !strings.Contains(pane, "█") {
		t.Errorf("the local image has no half-block thumbnail:\n%s", pane)
	}
	refs := m.report.refs
	if len(refs) != 3 || refs[0].label != ".github/workflows/ci.yml:139" || refs[1].path != "apps/web/Dockerfile" || refs[1].line != 103 || !refs[2].image {
		t.Fatalf("refs = %+v", refs)
	}
	for _, r := range refs {
		if r.row < 0 {
			t.Errorf("%s has no rendered row", r.label)
		}
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.report != nil || m.focus != 0 {
		t.Error("esc did not close the report")
	}
}

func TestAReportReferenceOpensItsFileAtTheLineAndEscReturns(t *testing.T) {
	m, _ := reportModel(t)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.renderReport(90, 60)
	m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.reportAt != 1 {
		t.Fatalf("reportAt = %d", m.reportAt)
	}
	if foot := ansi.Strip(m.footer(160)); !strings.Contains(foot, "↵ open apps/web/Dockerfile:103") {
		t.Errorf("footer = %q", foot)
	}
	pane := ansi.Strip(strings.Join(m.renderReport(90, 60), "\n"))
	if !strings.Contains(pane, "› ") {
		t.Errorf("the selected reference has no gutter mark:\n%s", pane)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || !m.preview || m.previewPath != "apps/web/Dockerfile" || m.previewLine != 103 {
		t.Fatalf("enter did not open the file: preview %v path %q line %d", m.preview, m.previewPath, m.previewLine)
	}
	lines := make([]string, 120)
	for i := range lines {
		lines[i] = "RUN step " + string(rune('a'+i%26))
	}
	m.Update(previewEvent{m.previewRequest, newPreviewDoc("apps/web/Dockerfile", Sides{Before: strings.Join(lines, "\n"), After: strings.Join(lines, "\n")})})
	if m.previewDoc.mark != 103 || m.previewScroll != 99 {
		t.Errorf("mark %d scroll %d; the file opens at the referenced line", m.previewDoc.mark, m.previewScroll)
	}
	if view := ansi.Strip(strings.Join(m.renderPreview(80, 20), "\n")); !strings.Contains(view, "103›") {
		t.Errorf("the referenced line is not marked:\n%s", view)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.preview || m.report == nil || m.focus != 2 {
		t.Errorf("esc from the file did not return to the report: preview %v focus %d", m.preview, m.focus)
	}
	m.reportAt = 0
	m.changes = []Change{{Kind: "M", Path: "README.md"}}
	m.tree = nil
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil || !m.alert || !strings.Contains(m.status, "not among this task's changes") {
		t.Errorf("a reference outside the changes: status %q", m.status)
	}
}

func TestReportImagesAndTheReportOpenInTheSystemViewer(t *testing.T) {
	m, ended := reportModel(t)
	var opened []string
	m.opener = func(p string) error { opened = append(opened, p); return nil }
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.renderReport(90, 60)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	m.Update(cmd())
	m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	if len(opened) != 2 || opened[0] != ended.Report || filepath.Base(opened[1]) != "shot.png" {
		t.Errorf("opened = %q", opened)
	}
	if !strings.Contains(m.status, "Opened before and after in the system viewer") {
		t.Errorf("status = %q", m.status)
	}
	m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	if m.confirm == nil || !strings.Contains(ansi.Strip(m.View().Content), "--continue") {
		t.Error("s in the report did not ask to follow up")
	}
}

func TestAMissingReportSaysSo(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	m.openReport(Task{ID: "x", Report: filepath.Join(t.TempDir(), "gone.md")})
	if pane := ansi.Strip(strings.Join(m.renderReport(80, 10), "\n")); !strings.Contains(pane, "the report is gone") {
		t.Errorf("pane:\n%s", pane)
	}
}
