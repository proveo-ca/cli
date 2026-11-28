// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

// popAnchor is where this frame drew the task pane and its selected task.
type popAnchor struct {
	x, top, w, h int // the task pane in frame rows and cells; top is its title row
	row          int // the selected task's first row inside the pane
	rowH         int // the rows the selected task takes
	ok           bool
}

// overlayDetails draws the selected task's details as a card over the frame; it never scrolls.
func (m *dashModel) overlayDetails(rows []string) []string {
	a, t := m.pop, m.task()
	if !m.details || !a.ok || t.ID == "" || m.preview || a.h < 6 {
		return rows
	}
	w := min(a.w-2, 100)
	if w < 32 {
		w = a.w
	}
	inner := max(1, w-4)
	lines := m.detailLines(t, inner, max(3, a.h-3))
	h := len(lines) + 2
	y := a.top + a.row + max(1, a.rowH)
	if y+h > a.top+a.h {
		y = a.top + a.row - h
	}
	y = max(a.top+1, min(y, a.top+a.h-h))
	x := a.x + max(0, (a.w-w)/2)
	hints := m.detailHints(t)
	if strings.Contains(strings.Join(lines, ""), "\x1b]8;") {
		hints += muted.Render(" · ") + hint("click", "a link copies it")
	}
	for i, line := range m.card(lines, w, hints) {
		if r := y + i; r >= 0 && r < len(rows) {
			rows[r] = splice(rows[r], x, w, line)
		}
	}
	return rows
}

// splice paints over on base from cell x, w cells wide, keeping base on both sides.
func splice(base string, x, w int, over string) string {
	return ansi.Cut(base, 0, x) + ansi.ResetStyle + over + ansi.ResetStyle + ansi.Cut(base, x+w, ansi.StringWidth(base))
}

// card frames lines w cells wide, with hints on its bottom edge; runes follow the glyph tier.
func (m *dashModel) card(lines []string, w int, hints string) []string {
	return m.frame(lines, w, hints, ruleText)
}

// frame draws lines inside a rounded edge w cells wide, hints on the bottom edge.
func (m *dashModel) frame(lines []string, w int, hints string, edge lipgloss.Style) []string {
	tl, tr, bl, br, hz, vt := "╭", "╮", "╰", "╯", "─", "│"
	if m.glyphs == ui.GlyphsASCII || m.glyphs == ui.GlyphsOff {
		tl, tr, bl, br, hz, vt = "+", "+", "+", "+", "-", "|"
	}
	out := []string{edge.Render(tl + strings.Repeat(hz, max(0, w-2)) + tr)}
	for _, line := range lines {
		out = append(out, edge.Render(vt)+" "+pad(line, w-4)+" "+edge.Render(vt))
	}
	bottom := edge.Render(bl + hz)
	if hints != "" && ansi.StringWidth(hints)+6 <= w {
		bottom += " " + hints + " "
	}
	fill := max(0, w-ansi.StringWidth(bottom)-1)
	return append(out, bottom+edge.Render(strings.Repeat(hz, fill)+br))
}

func (m *dashModel) detailHints(t Task) string {
	hints := []string{hint("t", "close")}
	if t.Live {
		hints = append(hints, hint("↵", "attach"))
	}
	if m.freshReady(t) {
		hints = append(hints, hint("e", "Fresh"))
	}
	return strings.Join(hints, muted.Render(" · "))
}

func (m *dashModel) freshReady(t Task) bool {
	return t.Guest && m.guest.fresh && m.guest.id == t.ID && m.guest.sandbox == t.Sandbox && m.guest.vmid == t.VMID
}

// detailLines is the card body in priority order, cut to limit rows by dropping the least important.
func (m *dashModel) detailLines(t Task, w, limit int) []string {
	label := func(name, value string) string { return muted.Render(pad(name, 9)) + value }
	st := m.statusOf(t)
	right := strings.TrimSpace(glyph(m.glyphs, st.mark) + " " + st.label)
	title := inkText.Bold(true).Render(ansi.Truncate(safeText(m.titleFor(t)), max(1, w-ansi.StringWidth(right)-2), "…"))
	required := []string{pad(title, w-ansi.StringWidth(right)) + st.style.Render(right)}
	var meta []string
	for _, s := range []string{m.ticketFor(t), agentLabel(t.Agent), t.Phase} {
		if s = safeText(s); s != "" {
			meta = append(meta, s)
		}
	}
	if !t.Updated.IsZero() {
		meta = append(meta, ago(t.Updated, m.now()))
	}
	required = append(required, muted.Render(strings.Join(meta, " · ")))

	sandbox, usage := "none yet", muted.Render("—")
	if t.Sandbox != "" {
		sandbox = safeText(t.Sandbox)
		s, sampled := m.sampleFor(t)
		switch {
		case !t.Guest:
			sandbox += muted.Render(" · not running")
		case sampled:
			sandbox += muted.Render(" · running · sampled " + s.at.Local().Format("15:04:05"))
			usage = s.usageLine()
		default:
			sandbox += muted.Render(" · running")
			usage = muted.Render("sampling…")
		}
	}
	required = append(required, label("Sandbox", sandbox), label("Usage", usage))

	meta2 := m.metadata[metadataKey(t)]
	var optional []string
	if r := t.Review; r != nil {
		optional = append(optional, label("Status", r.statusLine()), label("Reviews", r.Reviews.line()), label("Checks", r.Checks.line()))
		if names := r.Checks.FailedNames; len(names) > 0 {
			optional = append(optional, label("Failed", errorText.Render(ansi.Truncate(strings.Join(names, ", "), max(8, w-9), "…"))))
		}
	}
	optional = append(optional, m.prRows(t, meta2, label)...)
	if len(m.changes) > 0 {
		root := buildFileTree(m.changes)
		value := fmt.Sprintf("%d files", root.files)
		if s := rollup(root); s != "" && !strings.HasPrefix(ansi.Strip(s), fmt.Sprint(root.files)) {
			value += "  " + s
		}
		if label := m.baseLabel(); label != "" {
			value += muted.Render(" · " + label)
		}
		optional = append(optional, label("Changes", value))
	}
	if !t.LastPush.IsZero() {
		optional = append(optional, label("Pushed", t.LastPush.Local().Format("Jan 02 15:04")))
	}
	ticket := safeText(t.Ticket)
	if ticket == "" && len(meta2.pr.Tickets) > 0 {
		ticket = strings.Join(meta2.pr.Tickets, ", ") + muted.Render(" · named in PR")
	}
	if title := safeText(meta2.linear.Title); title != "" {
		if ticket != "" {
			ticket += muted.Render(" · ")
		}
		ticket += title
	}
	if ticket != "" {
		optional = append(optional, label("Ticket", ticket))
	}
	if plan := safeText(meta2.pr.Plan); plan != "" {
		if strings.Contains(plan, "://") {
			plan = linked(plan, keepTail(plan, max(8, w-9)))
		}
		optional = append(optional, label("Plan", plan))
	}
	for i, link := range m.prLinksFor(meta2) {
		name := ""
		if i == 0 {
			name = "Links"
		}
		shown := strings.TrimPrefix(strings.TrimPrefix(link, "https://"), "http://")
		optional = append(optional, label(name, linked(link, keepTail(safeText(shown), max(8, w-9)))))
	}
	if meta2.excerpt != "" {
		for _, line := range strings.SplitN(ansi.Wrap(safeText(meta2.excerpt), max(1, w-9), ""), "\n", 3)[:min(2, len(strings.SplitN(ansi.Wrap(safeText(meta2.excerpt), max(1, w-9), ""), "\n", 3)))] {
			optional = append(optional, strings.Repeat(" ", 9)+muted.Render(line))
		}
	}
	if t.Prompt != "" {
		prompt := strings.Split(ansi.Wrap(safeText(t.Prompt), max(1, w-9), ""), "\n")
		for i, line := range prompt[:min(3, len(prompt))] {
			name := ""
			if i == 0 {
				name = "Prompt"
			}
			if i == 2 && len(prompt) > 3 {
				line = ansi.Truncate(line, max(1, w-10), "") + "…"
			}
			optional = append(optional, label(name, line))
		}
	}
	room := max(0, limit-len(required))
	return append(required, optional[:min(room, len(optional))]...)
}

// prStyle is the tone of a PR's state.
func prStyle(p prInfo) lipgloss.Style {
	switch {
	case p.State == "MERGED" || p.Review == "APPROVED":
		return successText
	case p.Review == "CHANGES_REQUESTED":
		return warnText
	case p.Draft || p.State == "CLOSED":
		return muted
	}
	return brand
}

// prText is "PR #104 · state" as a link to the PR; without an address, in the state's tone.
func prText(p prInfo) string {
	if p.URL != "" {
		return linked(p.URL, "PR "+p.label())
	}
	return prStyle(p).Render("PR " + p.label())
}

// baseLabel names what the changes are measured against: "vs origin/dev · live".
func (m *dashModel) baseLabel() string {
	var label string
	switch m.changesBase {
	case "":
		return ""
	case "launch":
		label = "since launch " + short(m.changesMB)
	default:
		label = "vs " + m.changesBase
	}
	if m.changesLive {
		label += " · live"
	}
	return label
}

// prRows are one "Branch" row per PR, stacked PRs under the first; without a PR, the branch and why.
func (m *dashModel) prRows(t Task, meta taskMetadata, label func(name, value string) string) []string {
	prs := meta.prs
	if len(prs) == 0 && meta.pr.Number != 0 {
		prs = []prInfo{meta.pr}
	}
	var rows []string
	for i, p := range prs {
		name, head := "", safeText(p.Head)
		if i == 0 {
			name = "Branch"
		}
		if head == "" {
			head = safeText(t.Branch)
		}
		value := prText(p)
		if head != "" {
			value = head + muted.Render(" · ") + value
		}
		rows = append(rows, label(name, value))
	}
	if len(rows) > 0 {
		return rows
	}
	branch := safeText(t.Branch)
	if len(t.Branches) > 0 {
		branch = safeText(t.Branches[0])
	}
	var parts []string
	if branch != "" {
		parts = append(parts, branch)
	}
	if meta.pr.Note != "" {
		parts = append(parts, muted.Render(meta.pr.Note))
	}
	if len(parts) == 0 {
		return nil
	}
	return []string{label("Branch", strings.Join(parts, muted.Render(" · ")))}
}

// prLinksFor are the links every PR of the task carries, the plan left out, five at most.
func (m *dashModel) prLinksFor(meta taskMetadata) []string {
	prs := meta.prs
	if len(prs) == 0 {
		prs = []prInfo{meta.pr}
	}
	rules := m.config().Links
	var out []string
	for _, p := range prs {
		for _, l := range p.Links {
			if l != meta.pr.Plan && !slices.Contains(out, l) && len(out) < 5 && rules.shows(l) {
				out = append(out, l)
			}
		}
	}
	return out
}
