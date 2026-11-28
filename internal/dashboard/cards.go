// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

const (
	cardMinWidth = 38
	cardGap      = 1
)

var chipStyle = lipgloss.NewStyle().Background(hexColor(ui.ColorSurface)).Foreground(ink)

// cardPos is where a task's card sits in the queue: its first row and its column.
type cardPos struct{ line, col int }

// cardGrid is how many cards fit side by side in w cells, and their width.
func cardGrid(w int) (cols, width int) {
	cols = max(1, min(3, (w+cardGap)/(cardMinWidth+cardGap)))
	return cols, (w - (cols-1)*cardGap) / cols
}

// cardHeight is a card's rows: a compact card on a short terminal.
func (m *dashModel) cardHeight() int {
	if m.lay.short {
		return 5
	}
	return 10
}

const cardDivider = "\x00divider"

// cardFrame draws body inside a rounded frame w cells wide, chip on its top edge; the selected card's frame is brand.
func (m *dashModel) cardFrame(body []string, w int, chip string, selected bool) []string {
	tl, tr, bl, br, hz, vt := "╭", "╮", "╰", "╯", "─", "│"
	if m.glyphs == ui.GlyphsASCII || m.glyphs == ui.GlyphsOff {
		tl, tr, bl, br, hz, vt = "+", "+", "+", "+", "-", "|"
	}
	edge := ruleText
	if selected {
		edge = brand
	}
	inner := max(0, w-4)
	chipW := ansi.StringWidth(chip)
	if chipW == 0 || chipW+4 > w {
		chip, chipW = "", 0
	}
	fill := max(0, w-2-chipW)
	if chipW > 0 {
		fill--
	}
	top := edge.Render(tl+strings.Repeat(hz, fill)) + chip
	if chipW > 0 {
		top += edge.Render(hz)
	}
	out := []string{top + edge.Render(tr)}
	for _, line := range body {
		if line == cardDivider {
			out = append(out, edge.Render(vt)+" "+ruleText.Render(strings.Repeat(hz, inner))+" "+edge.Render(vt))
			continue
		}
		out = append(out, edge.Render(vt)+" "+pad(line, inner)+" "+edge.Render(vt))
	}
	return append(out, edge.Render(bl+strings.Repeat(hz, max(0, w-2))+br))
}

func (m *dashModel) chip(text string) string {
	if text = safeText(text); text == "" {
		return ""
	}
	if m.glyphs == ui.GlyphsASCII || m.glyphs == ui.GlyphsOff {
		return muted.Render("[" + text + "]")
	}
	return chipStyle.Render(" " + text + " ")
}

// titleFor is the task's title, or its PR's title when the task only knows its sandbox's name.
func (m *dashModel) titleFor(t Task) string {
	pr := m.metadata[metadataKey(t)].pr
	if pr.Title != "" && (t.Title == t.Sandbox || t.Title == t.ID || t.Title == "") {
		return pr.Title
	}
	return t.Title
}

// ticketFor is the task's ticket, else the Linear issues its PR names.
func (m *dashModel) ticketFor(t Task) string {
	if label := ticketLabel(t); label != "" {
		return label
	}
	tickets := m.metadata[metadataKey(t)].pr.Tickets
	switch len(tickets) {
	case 0:
		return ""
	case 1:
		return tickets[0]
	}
	return fmt.Sprintf("%s +%d", tickets[0], len(tickets)-1)
}

// taskCard is one task as a card: status and title, then agent · ticket · PR, where it runs, a live line and actions.
func (m *dashModel) taskCard(i int, t Task, w int) []string {
	if t.Review != nil {
		return m.ghostCard(i, t, w)
	}
	selected := i == m.selected
	inner := max(1, w-4)
	st := m.statusOf(t)
	mark := glyph(m.glyphs, st.mark)
	if mark != "" {
		mark = st.style.Render(mark) + " "
	}
	right, title := st.label, safeText(m.titleFor(t))
	room := inner - ansi.StringWidth(mark)
	statusBelow := room-ansi.StringWidth(right)-2 < ansi.StringWidth(title)
	if statusBelow {
		right = ""
	}
	titleRoom := room
	if right != "" {
		titleRoom -= ansi.StringWidth(right) + 2
	}
	style := inkText
	if selected {
		style = style.Bold(true)
	}
	first := mark + style.Render(ansi.Truncate(title, max(1, titleRoom), "…"))
	if right != "" {
		first = pad(first, inner-ansi.StringWidth(right)) + st.style.Render(right)
	}

	var who []string
	if s := safeText(agentLabel(t.Agent)); s != "" {
		who = append(who, s)
	}
	if s := safeText(m.ticketFor(t)); s != "" {
		who = append(who, linked(m.ticketURL(t), s))
	}
	work := strings.Join(who, muted.Render(" · "))
	if prs := m.prChips(t, inner-ansi.StringWidth(work)-3); prs != "" {
		if work != "" {
			work += muted.Render(" · ")
		}
		work += prs
	}
	prefix, where := "", "no sandbox yet"
	if statusBelow && st.label != "" {
		prefix = st.label + " · "
	}
	if t.Sandbox != "" {
		where = keepTail(safeText(t.Sandbox), max(1, inner-2-ansi.StringWidth(prefix)))
	}
	where = prefix + where
	body := []string{first}
	if m.lay.short {
		body = append(body, work, muted.Render(ansi.Truncate(where, inner, "…")))
	} else {
		body = append(body, cardDivider, work, m.cardAges(t),
			m.band(muted, where, inner), m.band(inkText, m.cardUsage(t), inner),
			cardDivider, m.cardActions(t, selected, inner))
	}
	chip := t.Phase
	if strings.EqualFold(chip, m.groupLabel(t)) || (chip == "Working" && m.groupOf(t) != taskGroup(t.State)) {
		chip = ""
	}
	return m.cardFrame(body, w, m.chip(chip), selected)
}

// prChips are the task's PRs as linked numbers in their state's tone, as many as fit room, then +N.
func (m *dashModel) prChips(t Task, room int) string {
	meta := m.metadata[metadataKey(t)]
	prs := meta.prs
	if len(prs) == 0 && meta.pr.Number != 0 {
		prs = []prInfo{meta.pr}
	}
	var out string
	for i, p := range prs {
		chip := prNumber(p)
		more := ""
		if rest := len(prs) - i - 1; rest > 0 {
			more = muted.Render(fmt.Sprintf(" +%d", rest))
		}
		gap := ""
		if out != "" {
			gap = " "
		}
		if i > 0 && ansi.StringWidth(out+gap+chip+more) > room {
			return out + muted.Render(fmt.Sprintf(" +%d", len(prs)-i))
		}
		out += gap + chip
	}
	return out
}

// prNumber is "#104" as a link to the PR; without an address, in the PR's state tone.
func prNumber(p prInfo) string {
	if p.URL != "" {
		return linked(p.URL, fmt.Sprintf("#%d", p.Number))
	}
	return prStyle(p).Render(fmt.Sprintf("#%d", p.Number))
}

// cardAges is "ticket 3d - PR 5h": how long the ticket and the oldest PR have been open; the task's age when neither is known.
func (m *dashModel) cardAges(t Task) string {
	meta, now := m.metadata[metadataKey(t)], m.now()
	var parts []string
	if at, err := time.Parse(time.RFC3339, meta.linear.CreatedAt); err == nil && m.ticketFor(t) != "" {
		parts = append(parts, muted.Render("ticket ")+compactAge(now.Sub(at)))
	}
	var opened time.Time
	for _, p := range meta.prs {
		if !p.Created.IsZero() && (opened.IsZero() || p.Created.Before(opened)) {
			opened = p.Created
		}
	}
	if !opened.IsZero() {
		parts = append(parts, muted.Render("PR ")+compactAge(now.Sub(opened)))
	}
	if len(parts) == 0 && !t.Updated.IsZero() {
		if m.view == 2 {
			return muted.Render(clock(t.Updated, now))
		}
		return muted.Render(ago(t.Updated, now))
	}
	return strings.Join(parts, muted.Render(" - "))
}

// cardUsage is the task's last sandbox sample, or why there is none.
func (m *dashModel) cardUsage(t Task) string {
	switch s, ok := m.sampleFor(t); {
	case ok:
		return s.bandUsage()
	case t.Sandbox == "":
		return "—"
	case !t.Guest:
		return "not running"
	}
	return "sampling…"
}

// sampleFor is the last usable sample of t's own sandbox.
func (m *dashModel) sampleFor(t Task) (guestSample, bool) {
	s, ok := m.samples[t.ID]
	return s, ok && s.available && s.sandbox == t.Sandbox && s.vmid == t.VMID
}

// band is text on the surface tone across w cells: the card's resources layer.
func (m *dashModel) band(style lipgloss.Style, text string, w int) string {
	text = ansi.Truncate(text, max(1, w-2), "…")
	bg := style.Background(hexColor(ui.ColorSurface))
	return bg.Render(" " + text + strings.Repeat(" ", max(0, w-2-ansi.StringWidth(text))) + " ")
}

// bandUsage is compactUsage without inner styling, so the band keeps its background.
func (s guestSample) bandUsage() string {
	return ansi.Strip(s.compactUsage())
}

// cardActions is the card's keys; only the selected card lights its mnemonics.
func (m *dashModel) cardActions(t Task, selected bool, w ...int) string {
	type action struct{ key, word string }
	actions := []action{{"t", "details"}, {"f", "files"}}
	if t.Guest && t.Sandbox != "" {
		actions = append(actions, action{"p", "ports"})
	}
	switch {
	case m.endedRun(t):
		var lead []action
		switch {
		case t.Report != "":
			lead = append(lead, action{"↵", "report"})
			if m.canFollowUp(t) {
				lead = append(lead, action{"s", "follow up"})
			}
		case m.canFollowUp(t):
			lead = append(lead, action{"↵", "follow up"})
		}
		if canRetry(t) {
			lead = append(lead, action{"r", "retry"})
		}
		if canCleanUp(t) {
			lead = append(lead, action{"x", "clean up"})
		}
		actions = append(lead, actions...)
	case m.groupOf(t) == 1:
		actions = append([]action{{"↵", "follow up"}}, actions...)
	case t.Live:
		actions = append([]action{{"↵", "attach"}}, actions...)
	}
	if m.freshReady(t) {
		actions = append(actions, action{"e", "Fresh"})
	}
	var parts []string
	for _, a := range actions {
		if selected {
			parts = append(parts, hint(a.key, a.word))
		} else {
			parts = append(parts, ruleText.Render(a.key+" "+a.word))
		}
	}
	if len(w) == 0 {
		return strings.Join(parts, "   ")
	}
	for _, sep := range []string{"   ", "  "} {
		if line := strings.Join(parts, sep); ansi.StringWidth(line) <= w[0] {
			return line
		}
	}
	for len(parts) > 1 && ansi.StringWidth(strings.Join(parts, "  ")) > w[0] {
		drop := slices.IndexFunc(actions, func(a action) bool { return a.key == "f" })
		if drop < 0 {
			drop = len(parts) - 1
		}
		parts, actions = slices.Delete(parts, drop, drop+1), slices.Delete(actions, drop, drop+1)
	}
	return strings.Join(parts, "  ")
}

// newInstanceCard is the empty card that ends the Queued section, as sbx ends its list with + New Sandbox.
func (m *dashModel) newInstanceCard(w int) []string {
	body := make([]string, m.cardHeight()-2)
	label := hint("n", "new instance")
	if m.glyphs != ui.GlyphsOff {
		label = muted.Render("+ ") + label
	}
	at := max(0, len(body)/2)
	body[at] = strings.Repeat(" ", max(0, (w-4-ansi.StringWidth(label))/2)) + label
	return m.cardFrame(body, w, "", false)
}

// compactUsage is the sample the way sbx's card writes it: 1.2/14c · 9/16G · 21G · 1d.
func (s guestSample) compactUsage() string {
	if !s.available {
		return ""
	}
	parts := []string{fmt.Sprintf("%.1f/%dc", s.busy, s.cores)}
	if s.ramTotal > 0 {
		parts = append(parts, fmt.Sprintf("%.0f/%.0fG", float64(s.ram)/(1<<30), float64(s.ramTotal)/(1<<30)))
	}
	if s.disk > 0 {
		parts = append(parts, fmt.Sprintf("%.0fG", float64(s.disk)/(1<<30)))
	}
	if s.uptime > 0 {
		parts = append(parts, compactAge(s.uptime))
	}
	return strings.Join(parts, muted.Render(" · "))
}

// placeCards lays cards out in rows of cols, gap apart, each row padded to w.
func placeCards(cards [][]string, cols, width, gap, w int) []string {
	var out []string
	for start := 0; start < len(cards); start += cols {
		row := cards[start:min(start+cols, len(cards))]
		for line := range row[0] {
			var parts []string
			for _, c := range row {
				parts = append(parts, pad(c[line], width))
			}
			out = append(out, pad(strings.Join(parts, strings.Repeat(" ", gap)), w))
		}
	}
	return out
}

// groupLabel is the heading the task's card sits under.
func (m *dashModel) groupLabel(t Task) string {
	return m.groupLabels()[m.groupOf(t)]
}
