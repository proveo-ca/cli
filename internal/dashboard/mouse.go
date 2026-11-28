// SPEC: _spec/internal/dashboard/run-reports.puml
package dashboard

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

// rect is a pane's frame on screen, in cells.
type rect struct{ x, y, w, h int }

func (r rect) has(x, y int) bool { return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h }

// paneAt names the pane under the pointer: 1 the file tree, 2 the right pane, 0 neither.
func (m *dashModel) paneAt(x, y int) int {
	switch {
	case m.leftPane.w > 0 && m.leftPane.has(x, y):
		return 1
	case m.rightPane.w > 0 && m.rightPane.has(x, y):
		return 2
	}
	return 0
}

// wheel scrolls the pane under the pointer, whichever pane has the keys.
func (m *dashModel) wheel(x, y, delta int) tea.Cmd {
	if m.inSchedule() {
		if delta < 0 {
			m.schedMove("up")
		} else {
			m.schedMove("down")
		}
		return nil
	}
	pane := m.paneAt(x, y)
	if pane == 0 {
		pane = 2
		if m.focus == 1 {
			pane = 1
		}
	}
	switch {
	case pane == 1:
		m.fileTree().move(delta)
	case m.preview:
		m.previewScroll = max(0, m.previewScroll+3*delta)
	case m.report != nil:
		m.reportScroll = max(0, m.reportScroll+3*delta)
	default:
		key := "down"
		if delta < 0 {
			key = "up"
		}
		return m.move(key)
	}
	return nil
}

// reportSelection is the rows a drag or v marks in the report, anchor to end, either way round.
type reportSelection struct {
	on, dragging, moved bool
	anchor, end         int
}

func (s reportSelection) span() (int, int) { return min(s.anchor, s.end), max(s.anchor, s.end) }

var selectedRow = lipgloss.NewStyle().Background(hexColor(ui.ColorSurface)).Foreground(ink)

// reportRowAt is the report row under screen row y, clamped to the rows on screen; ok is false off the report.
func (m *dashModel) reportRowAt(y int) (int, bool) {
	if m.report == nil || m.preview || m.rightPane.w == 0 {
		return 0, false
	}
	first, last := m.rightPane.y+1, m.rightPane.y+m.rightPane.h-2
	switch {
	case y < first:
		m.reportScroll = max(0, m.reportScroll-1)
		y = first
	case y > last:
		m.reportScroll++
		y = last
	}
	row := m.reportScroll + y - first
	return min(max(0, row), max(0, len(m.report.rows)-1)), true
}

// mouse handles a press, drag or release over the panes: a press gives its pane the keys, a drag over the report
// selects rows, and the release copies them.
func (m *dashModel) mouse(msg tea.Msg) tea.Cmd {
	switch e := msg.(type) {
	case tea.MouseClickMsg:
		if e.Button != tea.MouseLeft {
			return nil
		}
		m.pointer = point{e.X, e.Y, true}
		if target, ok := m.linkAt(e.X, e.Y); ok {
			return m.copyLink(target)
		}
		if m.inSchedule() {
			m.scheduleClick(e.X, e.Y)
			return nil
		}
		switch m.paneAt(e.X, e.Y) {
		case 1:
			m.focus = 1
		case 2:
			m.focus = 2
			if !m.preview && m.report == nil {
				m.focus = 0
			}
			if row, ok := m.reportRowAt(e.Y); ok {
				m.sel = reportSelection{on: true, dragging: true, anchor: row, end: row}
			}
		}
	case tea.MouseMotionMsg:
		if m.sel.dragging {
			if row, ok := m.reportRowAt(e.Y); ok && row != m.sel.end {
				m.sel.end, m.sel.moved = row, true
			}
		}
	case tea.MouseReleaseMsg:
		m.pointer = point{e.X, e.Y, true}
		if !m.sel.dragging {
			return nil
		}
		m.sel.dragging = false
		if !m.sel.moved {
			m.sel = reportSelection{}
			return nil
		}
		return m.copySelection()
	}
	return nil
}

// selectionText is the selected rows as plain text: no gutter, no colour, ordinary spaces, common indent removed.
func (m *dashModel) selectionText() (string, int) {
	r := m.report
	if r == nil || !m.sel.on || len(r.rows) == 0 {
		return "", 0
	}
	lo, hi := m.sel.span()
	hi = min(hi, len(r.rows)-1)
	lines := make([]string, 0, hi-lo+1)
	indent := -1
	for _, row := range r.rows[lo : hi+1] {
		line := strings.TrimRight(strings.ReplaceAll(ansi.Strip(row), " ", " "), " ")
		lines = append(lines, line)
		if trimmed := strings.TrimLeft(line, " "); trimmed != "" {
			if n := len(line) - len(trimmed); indent < 0 || n < indent {
				indent = n
			}
		}
	}
	for i, line := range lines {
		if len(line) >= indent && indent > 0 {
			lines[i] = line[indent:]
		}
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n"), len(lines)
}

// copySelection puts the selected rows on the clipboard and clears the selection.
func (m *dashModel) copySelection() tea.Cmd {
	text, n := m.selectionText()
	m.sel = reportSelection{}
	if text == "" {
		return nil
	}
	return m.copyText(text, fmt.Sprintf("%d rows", n))
}

type copiedEvent struct {
	what string
	at   point // the pointer that asked for the copy; at.ok false for a key
	err  error
}

// copyText sets the clipboard through OSC 52 and the host's own clipboard tool, so tmux and SSH sessions get it too.
func (m *dashModel) copyText(text, what string) tea.Cmd {
	native := m.clipboard
	if native == nil {
		native = hostClipboard
	}
	at := m.pointer
	return tea.Batch(tea.SetClipboard(text), func() tea.Msg { return copiedEvent{what: what, at: at, err: native(text)} })
}

// hostClipboard writes text with pbcopy on macOS, else wl-copy or xclip; with none, OSC 52 carries it alone.
func hostClipboard(text string) error {
	tools := [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}}
	if runtime.GOOS == "darwin" {
		tools = [][]string{{"pbcopy"}}
	}
	for _, t := range tools {
		if _, err := exec.LookPath(t[0]); err != nil {
			continue
		}
		cmd := exec.Command(t[0], t[1:]...)
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}
	return nil
}

// noModal is true when no card sits over the dashboard to take the mouse.
func (m *dashModel) noModal() bool {
	return m.confirm == nil && m.exitConfirm == nil && m.helpModal == nil && m.reviewModal == nil && m.verdictModal == nil && m.taskPicker == nil
}

// point is a screen cell the pointer pressed or released on; ok is false when a key acted instead.
type point struct {
	x, y int
	ok   bool
}

// tooltipFor is how long "Copied to clipboard" stays up.
const tooltipFor = 2400 * time.Millisecond

// tooltip is a short note drawn over the dashboard: at the pointer that copied, else at the right pane's foot.
type tooltip struct {
	text string
	at   point
	id   int
}

type tipDoneEvent struct{ id int }

// showTip raises text as the tooltip and takes it down after tooltipFor, unless a newer one replaced it.
func (m *dashModel) showTip(text string, at point) tea.Cmd {
	m.tipSeq++
	m.tip = tooltip{text: text, at: at, id: m.tipSeq}
	id := m.tipSeq
	return tea.Tick(tooltipFor, func(time.Time) tea.Msg { return tipDoneEvent{id} })
}

// overlayTip draws the tooltip as a small card over rows: above the pointer, below it when there is no room.
func (m *dashModel) overlayTip(rows []string) []string {
	if m.tip.text == "" || len(rows) == 0 {
		return rows
	}
	mark := successText.Render("✓") + " "
	if m.glyphs == ui.GlyphsASCII || m.glyphs == ui.GlyphsOff {
		mark = ""
	}
	body := mark + inkText.Render(m.tip.text)
	w := ansi.StringWidth(body) + 4
	box := m.frame([]string{body}, w, "", brand)
	var x, y int
	if at := m.tip.at; at.ok {
		x, y = at.x-w/2, at.y-len(box)
		if y < 1 {
			y = at.y + 1
		}
	} else {
		x, y = m.rightPane.x+m.rightPane.w-w-2, m.rightPane.y+m.rightPane.h-len(box)-1
	}
	x = min(max(0, x), max(0, m.width-w))
	y = min(max(0, y), max(0, len(rows)-1-len(box)))
	for i, line := range box {
		if r := y + i; r < len(rows) {
			rows[r] = splice(rows[r], x, w, line)
		}
	}
	return rows
}
