// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

// loadingAfter is how long a blank sbx frame waits before the loading card covers it.
const loadingAfter = 250 * time.Millisecond

// handoff runs a program in the dashboard's terminal behind a loading card on the alternate screen,
// which holds the screen until the program draws; the shell's own screen stays untouched.
type handoff struct {
	cmd  *exec.Cmd
	card string
	out  io.Writer
}

func (h *handoff) SetStdin(r io.Reader)  { h.cmd.Stdin = r }
func (h *handoff) SetStdout(w io.Writer) { h.cmd.Stdout, h.out = w, w }
func (h *handoff) SetStderr(w io.Writer) { h.cmd.Stderr = w }

func (h *handoff) Run() error {
	if h.out != nil && h.card != "" {
		_, _ = io.WriteString(h.out, ansi.SetModeAltScreenSaveCursor+ansi.HideCursor+ansi.EraseEntireScreen+ansi.CursorHomePosition+
			strings.ReplaceAll(h.card, "\n", "\r\n"))
	}
	err := h.cmd.Run()
	if h.out != nil && h.card != "" {
		_, _ = io.WriteString(h.out, ansi.ShowCursor+ansi.ResetModeAltScreenSaveCursor)
	}
	return err
}

// handoffTo hands the terminal to name args behind a card naming what it waits for; back names the return.
func (m *dashModel) handoffTo(title, detail, hint, back, name string, args ...string) tea.Cmd {
	card := m.loadingCard(title, detail, hint, glyph(m.glyphs, markRunning))
	return tea.Exec(&handoff{cmd: exec.Command(name, args...), card: card}, func(err error) tea.Msg {
		return resultEvent{message: back, err: err}
	})
}

// loadingCard is a full frame with title, detail and hint centred in a card; mark leads the title.
func (m *dashModel) loadingCard(title, detail, hint, mark string) string {
	w, h := max(1, m.width), max(1, m.height)
	inner := max(8, min(w-8, 72))
	head := brand.Bold(true).Render(ansi.Truncate(safeText(title), inner-2, "…"))
	if mark != "" {
		head = brand.Render(mark) + " " + head
	}
	lines := []string{head}
	if detail = safeText(detail); detail != "" {
		lines = append(lines, muted.Render(keepTail(detail, inner)))
	}
	card := m.card(lines, inner+4, hint)
	if inner+4 > w {
		card = lines
	}
	top := max(0, (h-len(card))/2)
	out := make([]string, 0, h)
	for range top {
		out = append(out, strings.Repeat(" ", w))
	}
	for _, line := range card {
		if len(out) == h {
			break
		}
		lead := max(0, (w-ansi.StringWidth(line))/2)
		out = append(out, pad(strings.Repeat(" ", lead)+line, w))
	}
	for len(out) < h {
		out = append(out, strings.Repeat(" ", w))
	}
	return strings.Join(out, "\n")
}

// spinner is the frame of a working mark at now; it moves on every repaint.
func (m *dashModel) spinner() string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	if m.glyphs == ui.GlyphsASCII || m.glyphs == ui.GlyphsOff {
		frames = []string{"|", "/", "-", "\\"}
	}
	return frames[int(m.now().UnixMilli()/100)%len(frames)]
}

// sbxLoading covers a blank sbx frame after loadingAfter: sbx is starting, or between its list and an agent.
func (m *dashModel) sbxLoading(frame string) (string, bool) {
	if strings.TrimSpace(ansi.Strip(frame)) != "" {
		m.childBlankAt, m.childPainted = time.Time{}, true
		return "", false
	}
	if m.childBlankAt.IsZero() {
		m.childBlankAt = m.now()
	}
	if m.now().Sub(m.childBlankAt) < loadingAfter {
		return "", false
	}
	title, detail := "Starting sbx", "sbx draws here once it is up"
	if m.childPainted {
		title, detail = "Waiting for sbx", "attaching or returning to the sandbox list"
	}
	saved := m.height
	m.height = max(1, m.height-1)
	card := m.loadingCard(title, detail, hint("Ctrl+G Tab", "back to proveo"), m.spinner())
	m.height = saved
	return card, true
}

// loadingBeat is how long a tree read runs before its pane says so; quicker reads never flash a card.
const loadingBeat = 150 * time.Millisecond

// treeReading is the shown list's read, what to call it, and where it reads from.
func (m *dashModel) treeReading() (treeLoad, string, string) {
	load, title := m.changesLoad, "Reading changes"
	if m.showFiles {
		load, title = m.filesLoad, "Listing files"
	}
	t, from := m.task(), ""
	switch {
	case t.Guest && t.Sandbox != "":
		from = "from " + t.Sandbox
	case t.Tip != "":
		from = "from the carried tip " + short(t.Tip)
	}
	return load, title, from
}

// paneLoading is a spinner card centred in a pane w by h, with details under its title.
func (m *dashModel) paneLoading(title string, w, h int, details ...string) []string {
	cw := min(w-2, 46)
	body := []string{brand.Render(m.spinner()) + " " + brand.Bold(true).Render(ansi.Truncate(title, max(1, cw-6), "…"))}
	for _, detail := range details {
		if detail = safeText(detail); detail != "" {
			body = append(body, muted.Render(keepTail(detail, max(1, cw-4))))
		}
	}
	card := body
	if cw >= 20 {
		card = m.card(body, cw, "")
	}
	out := make([]string, max(0, (h-len(card))/2))
	lead := strings.Repeat(" ", max(0, (w-ansi.StringWidth(card[0]))/2))
	for _, line := range card {
		out = append(out, lead+line)
	}
	return out
}

// guestBusy says so when the selected sandbox's last sample had most of its cores busy.
func (m *dashModel) guestBusy() string {
	s, ok := m.sampleFor(m.task())
	if !ok || s.cores == 0 || s.busy < 0.75*float64(s.cores) {
		return ""
	}
	return fmt.Sprintf("sandbox busy · CPU %.1f/%d cores", s.busy, s.cores)
}
