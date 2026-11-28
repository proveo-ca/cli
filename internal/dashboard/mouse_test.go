package dashboard

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func wheelAt(m *dashModel, r rect, down bool) {
	b := tea.MouseWheelUp
	if down {
		b = tea.MouseWheelDown
	}
	m.Update(tea.MouseWheelMsg(tea.Mouse{X: r.x + 3, Y: r.y + 3, Button: b}))
}

func TestTheWheelScrollsThePaneUnderThePointer(t *testing.T) {
	m, _ := reportModel(t)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.View()
	if m.leftPane.w == 0 || m.rightPane.w == 0 || m.rightPane.x <= m.leftPane.x {
		t.Fatalf("panes = %+v %+v", m.leftPane, m.rightPane)
	}
	selected := m.selected
	wheelAt(m, m.rightPane, true)
	if m.reportScroll != 3 || m.selected != selected {
		t.Errorf("the wheel over the report: scroll %d, selected %d (was %d); it scrolls the report, not the tasks", m.reportScroll, m.selected, selected)
	}
	at := m.fileTree().at
	wheelAt(m, m.leftPane, true)
	if m.fileTree().at == at || m.reportScroll != 3 || m.focus != 2 {
		t.Errorf("the wheel over the tree: at %q (was %q), report scroll %d, focus %d", m.fileTree().at, at, m.reportScroll, m.focus)
	}
	m.preview, m.previewScroll = true, 0
	wheelAt(m, m.rightPane, true)
	if m.previewScroll != 3 {
		t.Errorf("the wheel over a file: scroll %d", m.previewScroll)
	}
}

func TestDraggingOverTheReportSelectsRowsAndCopiesThemAsPlainText(t *testing.T) {
	m, _ := reportModel(t)
	var copied []string
	m.clipboard = func(s string) error { copied = append(copied, s); return nil }
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.View()
	r := m.report
	row := -1
	for i, line := range r.rows {
		if strings.Contains(ansi.Strip(line), "F1") {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("no F1 row")
	}
	y := m.rightPane.y + 1 + row - m.reportScroll
	m.Update(tea.MouseClickMsg(tea.Mouse{X: m.rightPane.x + 5, Y: y, Button: tea.MouseLeft}))
	m.Update(tea.MouseMotionMsg(tea.Mouse{X: m.rightPane.x + 9, Y: y + 1, Button: tea.MouseLeft}))
	if pane := ansi.Strip(m.View().Content); !strings.Contains(pane, "▌ ") {
		t.Errorf("the dragged rows are not highlighted:\n%s", pane)
	}
	_, cmd := m.Update(tea.MouseReleaseMsg(tea.Mouse{X: m.rightPane.x + 9, Y: y + 1, Button: tea.MouseLeft}))
	for _, msg := range runCmd(cmd) {
		m.Update(msg)
	}
	if len(copied) != 1 || !strings.HasPrefix(copied[0], "F1") || strings.Contains(copied[0], " ") || strings.Contains(copied[0], "\x1b") ||
		!strings.Contains(copied[0], ".github/workflows/ci.yml:139") || strings.Count(copied[0], "\n") != 1 {
		t.Errorf("copied %q", copied)
	}
	if !strings.Contains(m.status, "Copied 2 rows to the clipboard") || m.sel.on {
		t.Errorf("status %q, selection %+v", m.status, m.sel)
	}
	m.Update(tea.MouseClickMsg(tea.Mouse{X: m.rightPane.x + 5, Y: y, Button: tea.MouseLeft}))
	if _, cmd := m.Update(tea.MouseReleaseMsg(tea.Mouse{X: m.rightPane.x + 5, Y: y, Button: tea.MouseLeft})); cmd != nil || m.sel.on || len(copied) != 1 {
		t.Error("a click without a drag copied, or left a selection")
	}
	m.Update(tea.MouseClickMsg(tea.Mouse{X: m.leftPane.x + 3, Y: m.leftPane.y + 3, Button: tea.MouseLeft}))
	if m.focus != 1 {
		t.Errorf("a click on the tree did not give it the keys: focus %d", m.focus)
	}
}

func TestVSelectsRowsYCopiesThemAndShiftYCopiesTheWholeReport(t *testing.T) {
	m, ended := reportModel(t)
	var copied []string
	m.clipboard = func(s string) error { copied = append(copied, s); return nil }
	press := func(code rune, text string) tea.Cmd {
		_, cmd := m.Update(tea.KeyPressMsg{Code: code, Text: text})
		return cmd
	}
	run := func(cmd tea.Cmd) {
		for _, msg := range runCmd(cmd) {
			m.Update(msg)
		}
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.View()
	press('v', "v")
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if lo, hi := m.sel.span(); !m.sel.on || hi-lo != 2 || m.reportScroll != 0 {
		t.Fatalf("v then two ↓: %+v scroll %d", m.sel, m.reportScroll)
	}
	if foot := ansi.Strip(m.footer(160)); !strings.Contains(foot, "y copy") || !strings.Contains(foot, "Esc clear") {
		t.Errorf("footer while selecting = %q", foot)
	}
	run(press('y', "y"))
	if len(copied) != 1 || strings.Count(copied[0], "\n") > 2 || !strings.Contains(m.status, "Copied 3 rows") {
		t.Errorf("copied %q, status %q", copied, m.status)
	}
	run(press('Y', "Y"))
	b, _ := os.ReadFile(ended.Report)
	src := string(b)
	if len(copied) != 2 || copied[1] != src || !strings.Contains(m.status, "Copied the whole report") {
		t.Errorf("Y copied %q", copied[len(copied)-1])
	}
	press('n', "n")
	run(press('y', "y"))
	if copied[len(copied)-1] != ".github/workflows/ci.yml:139" {
		t.Errorf("y on a selected reference copied %q", copied[len(copied)-1])
	}
	press('v', "v")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.sel.on || m.report == nil {
		t.Error("the first Esc did not just clear the selection")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.report != nil {
		t.Error("the second Esc did not close the report")
	}
}
