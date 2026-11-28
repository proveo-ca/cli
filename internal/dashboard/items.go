// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"fmt"
	"image/color"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/activity"
	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

func hexColor(rgb int) color.Color { return lipgloss.Color(fmt.Sprintf("#%06X", rgb)) }

var (
	successText = lipgloss.NewStyle().Foreground(hexColor(ui.ColorSuccess))
	warnText    = lipgloss.NewStyle().Foreground(hexColor(ui.ColorWarn))
	ruleText    = lipgloss.NewStyle().Foreground(hexColor(ui.ColorRule))
	inkText     = lipgloss.NewStyle().Foreground(ink)
)

type mark int

const (
	markReview mark = iota
	markAttention
	markFailed
	markRunning
	markQueued
	markDone
	markFoldOpen
	markFoldClosed
	markGuide
)

// glyph is a mark's rune at the session's glyph tier; GlyphsOff draws none.
func glyph(t ui.GlyphTier, m mark) string {
	if t == ui.GlyphsOff {
		return ""
	}
	pick := func(unicode, ascii string) string {
		if t == ui.GlyphsASCII {
			return ascii
		}
		return unicode
	}
	switch m {
	case markReview:
		return pick("◇", ">")
	case markAttention:
		return "!"
	case markFailed:
		return pick("×", "x")
	case markRunning:
		return pick("●", "*")
	case markQueued:
		return pick("○", "o")
	case markDone:
		return pick("✓", "+")
	case markFoldOpen:
		return pick("▾", "-")
	case markFoldClosed:
		return pick("▸", "+")
	case markGuide:
		return pick("│", "|")
	}
	return ""
}

// Nerd Font codepoints for common names.
var (
	nerdNames = map[string]rune{
		"go.mod": 0xe627, "go.sum": 0xe627, "Dockerfile": 0xf308, "Makefile": 0xe673,
		"LICENSE": 0xf02d, ".gitignore": 0xf02a2, "README.md": 0xf00ba,
	}
	nerdExts = map[string]rune{
		".go": 0xe627, ".md": 0xeb1d, ".json": 0xe60b, ".yaml": 0xe6a8, ".yml": 0xe6a8,
		".toml": 0xe6b2, ".sh": 0xf018d, ".js": 0xf031e, ".mjs": 0xf031e, ".cjs": 0xe60c,
		".ts": 0xf06e6, ".py": 0xed1b, ".rs": 0xe68b, ".html": 0xf13b, ".css": 0xe749,
		".lock": 0xf023, ".txt": 0xf0219, ".png": 0xf021f, ".puml": 0xf0e8,
	}
)

const (
	nerdFile       = 0xf15b
	nerdTest       = 0xf0c3
	nerdFolder     = 0xf07b
	nerdFolderOpen = 0xf07c
)

func fileIcon(t ui.GlyphTier, name string) string {
	if t != ui.GlyphsNerd {
		return ""
	}
	if r, ok := nerdNames[name]; ok {
		return string(r)
	}
	if strings.HasSuffix(name, "_test.go") || strings.Contains(name, ".test.") {
		return string(rune(nerdTest))
	}
	if r, ok := nerdExts[strings.ToLower(path.Ext(name))]; ok {
		return string(r)
	}
	return string(rune(nerdFile))
}

func folderIcon(t ui.GlyphTier, open bool) string {
	switch {
	case t != ui.GlyphsNerd:
		return ""
	case open:
		return string(rune(nerdFolderOpen))
	}
	return string(rune(nerdFolder))
}

// statusStyle colours a git status letter and the name it marks.
func statusStyle(letter byte) lipgloss.Style {
	switch letter {
	case 'A', 'C':
		return successText
	case 'M', 'T':
		return warnText
	case 'D', 'U':
		return errorText
	case 'R':
		return brand
	case '?':
		return muted
	}
	return inkText
}

// rollup is a folded folder's hidden change counts, or its file count without statuses.
func rollup(n *treeNode) string {
	groups := []struct {
		sign  string
		kinds string
		style lipgloss.Style
	}{
		{"!", "U", errorText}, {"+", "AC", successText}, {"~", "MT", warnText},
		{"-", "D", errorText}, {"→", "R", brand}, {"?", "?", muted},
	}
	var parts []string
	for _, g := range groups {
		total := 0
		for i := range len(g.kinds) {
			total += n.counts[g.kinds[i]]
		}
		if total > 0 {
			parts = append(parts, g.style.Render(fmt.Sprintf("%s%d", g.sign, total)))
		}
	}
	if len(parts) == 0 && n.files > 0 {
		return muted.Render(fmt.Sprintf("%d", n.files))
	}
	return strings.Join(parts, " ")
}

// emphasise styles text, underlining the runes at hits and dimming those before split.
func emphasise(text string, hits []int, split int, base lipgloss.Style) string {
	var b strings.Builder
	var run []rune
	runStyle := base
	flush := func() {
		if len(run) > 0 {
			b.WriteString(runStyle.Render(string(run)))
			run = run[:0]
		}
	}
	for i, r := range []rune(text) {
		style := base
		if i < split {
			style = muted
		}
		if slices.Contains(hits, i) {
			style = style.Underline(true).Bold(true)
		}
		if len(run) > 0 && !sameStyle(style, runStyle) {
			flush()
		}
		runStyle = style
		run = append(run, r)
	}
	flush()
	return b.String()
}

func sameStyle(a, b lipgloss.Style) bool { return a.Render("x") == b.Render("x") }

type taskStatus struct {
	mark  mark
	label string
	style lipgloss.Style
}

func (m *dashModel) statusOf(t Task) taskStatus {
	switch t.State {
	case reviewState:
		return taskStatus{markQueued, "Review requested", muted}
	case schedule.Attention:
		switch t.Outcome {
		case "failed":
			return taskStatus{markFailed, "Failed", errorText}
		case "not-ready":
			return taskStatus{markAttention, "Agent not ready", warnText}
		case "budget":
			return taskStatus{markAttention, "Budget reached", warnText}
		case "ended":
			return taskStatus{markAttention, "Ended early", warnText}
		}
		if t.Outcome == "done" || t.Reason == "" || t.Reason == "review" {
			return taskStatus{markReview, "Ready for review", brand}
		}
		return taskStatus{markAttention, sentence(t.Reason), warnText}
	case schedule.Reserved:
		return taskStatus{markRunning, "Starting", brand}
	case schedule.Running:
		if st, ok := m.activityStatus(t); ok {
			return st
		}
		return taskStatus{markRunning, "Running", brand}
	case schedule.Settling:
		return taskStatus{markRunning, "Settling", brand}
	case schedule.Complete:
		return taskStatus{markDone, "Done", successText}
	}
	if blockers := m.blockers(t); len(blockers) > 0 {
		return taskStatus{markQueued, "Blocked by " + strings.Join(blockers, ", "), muted}
	}
	if t.Reason != "" && !strings.HasPrefix(t.Reason, "blocked by") {
		return taskStatus{markQueued, sentence(t.Reason), muted}
	}
	return taskStatus{markQueued, "Queued", muted}
}

// blockers names the unfinished prerequisites of t by ticket when one is known.
func (m *dashModel) blockers(t Task) []string {
	var out []string
	for _, dep := range t.DependsOn {
		i := slices.IndexFunc(m.data.Tasks, func(o Task) bool { return o.ID == dep })
		if i >= 0 && m.data.Tasks[i].State == schedule.Complete {
			continue
		}
		label := dep
		if i >= 0 && m.data.Tasks[i].Ticket != "" {
			label = ticketLabel(m.data.Tasks[i])
		}
		out = append(out, safeText(label))
	}
	return out
}

func sentence(s string) string {
	s = safeText(s)
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

var agentLabels = map[string]string{
	"claude": "Claude Code", "claudecode": "Claude Code", "codex": "Codex",
	"opencode": "OpenCode", "hermes": "Hermes", "cursor": "Cursor",
}

func agentLabel(target string) string {
	if label, ok := agentLabels[strings.TrimPrefix(strings.ToLower(target), "proveo-")]; ok {
		return label
	}
	return target
}

func ticketLabel(t Task) string {
	if t.Ticket == "" {
		return ""
	}
	if id := linearIdentifier(t); id != "" {
		return id
	}
	if strings.Contains(t.Ticket, "://") {
		base := path.Base(strings.TrimSuffix(t.Ticket, "/"))
		if strings.Contains(t.Ticket, "/issues/") || strings.Contains(t.Ticket, "/pull/") {
			return "#" + base
		}
		return base
	}
	return t.Ticket
}

func ago(at, now time.Time) string {
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return at.Local().Format("Jan 02")
}

func groupHeader(label string, w int) string {
	out := brand.Render(label)
	if rest := w - ansi.StringWidth(label) - 1; rest > 0 {
		out += " " + ruleText.Render(strings.Repeat("─", rest))
	}
	return out
}

// activityOf is what t's agent is doing, as its sandbox's last sample read it.
func (m *dashModel) activityOf(t Task) (activity.State, time.Time) {
	if s, ok := m.sampleFor(t); ok && t.Guest {
		return s.activity, s.activitySince
	}
	return activity.Unknown, time.Time{}
}

// activityStatus names a running task by its agent's own signals: working, background work, waiting, idle.
func (m *dashModel) activityStatus(t Task) (taskStatus, bool) {
	if s, ok := m.sampleFor(t); ok && t.Guest && s.seedRefused != "" {
		return taskStatus{markFailed, "Seed refused: " + s.seedRefused, errorText}, true
	}
	state, since := m.activityOf(t)
	age := ""
	if !since.IsZero() {
		age = " " + compactAge(max(0, m.now().Sub(since)))
	}
	switch state {
	case activity.Busy:
		return taskStatus{markRunning, "Working", brand}, true
	case activity.Background:
		return taskStatus{markRunning, "Background work", brand}, true
	case activity.Waiting:
		return taskStatus{markAttention, "Waiting for you" + age, warnText}, true
	case activity.Idle:
		return taskStatus{markAttention, "Idle" + age, warnText}, true
	case activity.Exited:
		return taskStatus{markAttention, "Agent exited", warnText}, true
	}
	return taskStatus{}, false
}

// groupOf is t's group: a running agent that waits, idles or exited needs the operator.
func (m *dashModel) groupOf(t Task) int {
	if t.State == schedule.Running {
		switch state, _ := m.activityOf(t); state {
		case activity.Waiting, activity.Idle, activity.Exited:
			return 1
		}
	}
	return taskGroup(t.State)
}
