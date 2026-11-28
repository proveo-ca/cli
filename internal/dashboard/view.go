// SPEC: _spec/internal/dashboard/workspace-instances.puml, _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

var (
	dashBG    = lipgloss.Color("#1B1B20")
	ink       = lipgloss.Color("#E5E4E4")
	brand     = lipgloss.NewStyle().Foreground(lipgloss.Color("#00BAC6"))
	muted     = lipgloss.NewStyle().Foreground(lipgloss.Color("#8A8A8A"))
	errorText = lipgloss.NewStyle().Foreground(lipgloss.Color("#CB2000"))
)

func hint(k, action string) string {
	h := key.NewBinding(key.WithHelp(k, action)).Help()
	return brand.Render(h.Key) + " " + muted.Render(h.Desc)
}

func (m *dashModel) View() tea.View {
	if m.exitConfirm != nil {
		v := m.exitConfirm.View()
		v.BackgroundColor = dashBG
		return v
	}
	if m.confirm != nil && !m.sbxTab {
		v := m.confirm.View()
		v.BackgroundColor = dashBG
		return v
	}
	if m.helpModal != nil && !m.sbxTab {
		v := m.helpModal.View()
		v.BackgroundColor = dashBG
		return v
	}
	if m.taskPicker != nil && !m.sbxTab {
		v := m.taskPicker.View()
		v.BackgroundColor = dashBG
		return v
	}
	if m.reviewModal != nil && !m.sbxTab {
		v := m.reviewModal.View()
		v.BackgroundColor = dashBG
		return v
	}
	if m.verdictModal != nil && !m.sbxTab {
		v := m.verdictModal.View()
		v.BackgroundColor = dashBG
		return v
	}
	if m.pauseChoice != nil && !m.sbxTab {
		v := m.pauseChoice.View()
		v.BackgroundColor = dashBG
		return v
	}
	m.lay = newLayout(m.width, m.height, m.sbxSplit)
	var content string
	var cursor *tea.Cursor
	switch {
	case !m.sbxTab && m.lay.tooSmall:
		content = tooSmallCard(m.width, m.height)
	case m.sbxTab:
		content = m.header() + "\n"
		if m.child != nil {
			frame, c := m.child.Frame()
			if card, loading := m.sbxLoading(frame); loading {
				content += card
			} else {
				content += frame
				if cursor = c; cursor != nil {
					cursor.Y++
				}
			}
		} else {
			content += pad(m.status, m.width)
		}
	default:
		content = m.header() + "\n" + m.renderDashboard()
	}
	m.links = nil
	if !m.sbxTab && !m.lay.tooSmall {
		rows := m.overlayTip(strings.Split(content, "\n"))
		content = strings.Join(rows, "\n")
		m.links = linkSpots(rows)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.BackgroundColor, v.ForegroundColor = dashBG, ink
	v.Cursor = cursor
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// header keeps the tabs and the switch key; the path takes what remains, tail first.
func (m *dashModel) header() string {
	tab := func(on bool, name string) string {
		if on {
			return brand.Bold(true).Render("[" + name + "]")
		}
		return muted.Render(name)
	}
	switcher := hint("Ctrl+G Tab", "switch view")
	if m.width < 72 {
		switcher = brand.Render("Ctrl+G Tab")
	}
	right := tab(!m.sbxTab, "proveo") + "  " + tab(m.sbxTab, "sbx") + "   " + switcher
	head := right
	if room := m.width - ansi.StringWidth(right) - 2; room >= 8 {
		head = keepTail(safeText(ui.Tilde(m.CWD)), room) + "  " + right
	}
	return pad(head, m.width)
}

func (m *dashModel) renderDashboard() string {
	l := m.lay
	var rows []string
	if l.capacityVisible {
		for _, line := range m.frame([]string{m.capacity(l.w - 4)}, l.w, "", muted) {
			rows = append(rows, pad(line, l.w))
		}
	}
	right := func(w, h int) []string {
		if m.preview {
			return m.renderPreview(w, h)
		}
		if m.report != nil {
			return m.renderReport(w, h)
		}
		if m.view == scheduleView {
			return m.renderSchedule(w, h)
		}
		return m.renderTasks(w, h)
	}
	left := m.renderFiles
	if m.view == scheduleView {
		left = m.renderJob
	}
	leftFocus := m.focus == 1
	switch {
	case l.single:
		pane := right
		if leftFocus {
			pane = left
		}
		m.leftPane, m.rightPane = rect{}, rect{0, 1 + len(rows), l.w, l.body}
		if leftFocus {
			m.leftPane, m.rightPane = m.rightPane, rect{}
		}
		m.pop = popAnchor{x: paneInset, top: len(rows), w: l.w - 2*paneInset, h: l.body - 1}
		rows = append(rows, m.paneBox(pane, l.w, l.body, true)...)
	case l.portrait:
		m.leftPane = rect{0, 1 + len(rows), l.w, l.top}
		rows = append(rows, m.paneBox(left, l.w, l.top, leftFocus)...)
		rest := max(3, l.body-l.top)
		m.rightPane = rect{0, 1 + len(rows), l.w, rest}
		m.pop = popAnchor{x: paneInset, top: len(rows), w: l.w - 2*paneInset, h: rest - 1}
		rows = append(rows, m.paneBox(right, l.w, rest, !leftFocus)...)
	default:
		m.pop = popAnchor{x: l.left + 1 + paneInset, top: len(rows), w: l.right - 2*paneInset, h: l.body - 1}
		m.leftPane, m.rightPane = rect{0, 1 + len(rows), l.left, l.body}, rect{l.left + 1, 1 + len(rows), l.right, l.body}
		lf := m.paneBox(left, l.left, l.body, leftFocus)
		rf := m.paneBox(right, l.right, l.body, !leftFocus)
		for line := 0; line < l.body; line++ {
			rows = append(rows, pad(lf[line], l.left)+" "+pad(rf[line], l.right))
		}
	}
	rows = m.overlayDetails(rows)
	return strings.Join(append(rows, pad(m.footer(l.w), l.w)), "\n")
}

// paneInset is the cells a pane frame takes on each side: its edge and a space.
const paneInset = 2

// paneBox draws a pane w by h inside a rounded frame: the pane's title rides the top edge, its rows sit inside.
// The focused pane's edge is brand, the other's the rule grey.
func (m *dashModel) paneBox(render func(w, h int) []string, w, h int, focused bool) []string {
	if w < 8 || h < 3 {
		return render(w, h)
	}
	lines := render(w-2*paneInset, h-1)
	tl, tr, bl, br, hz, vt := "╭", "╮", "╰", "╯", "─", "│"
	if m.glyphs == ui.GlyphsASCII || m.glyphs == ui.GlyphsOff {
		tl, tr, bl, br, hz, vt = "+", "+", "+", "+", "-", "|"
	}
	edge := ruleText
	if focused {
		edge = brand
	}
	top := edge.Render(tl + hz)
	if len(lines) > 0 {
		if title := ansi.Truncate(strings.TrimRight(lines[0], " "), max(0, w-6), "…"); ansi.StringWidth(ansi.Strip(title)) > 0 {
			top += " " + title + " "
		}
	}
	top += edge.Render(strings.Repeat(hz, max(0, w-ansi.StringWidth(top)-1)) + tr)
	out := []string{top}
	for i := 1; i < h-1; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		out = append(out, edge.Render(vt)+" "+pad(line, w-2*paneInset)+" "+edge.Render(vt))
	}
	return append(out, edge.Render(bl+strings.Repeat(hz, max(0, w-2))+br))
}

// capacity drops meters, then labels, until the row fits.
func (m *dashModel) capacity(w int) string {
	c := m.data.Capacity
	ramUsed := c.RAM - min(c.RAMFree, c.RAM)
	diskUsed := c.Disk - min(c.DiskFree, c.Disk)
	cpu := fmt.Sprintf("%d cores", c.Cores)
	if c.CPUUsage && c.Cores > 0 {
		cpu = fmt.Sprintf("%d / %d cores", c.BusyCores, c.Cores)
	}
	ram := totalGiB(c.RAM)
	if c.RAMUsage && c.RAM > 0 {
		ram = usedOf(ramUsed, c.RAM)
	}
	disk := totalGiB(c.Disk)
	if c.Disk > 0 {
		disk = usedOf(diskUsed, c.Disk)
	}
	vram := ""
	if c.VRAM > 0 {
		vram = "   VRAM " + muted.Render(formatGiB(c.VRAM))
	}
	full := hint("c", "Host capacity") + "   " +
		"CPU " + maybeMeter(c.CPUUsage, uint64(c.BusyCores), uint64(c.Cores), 2) + " " + muted.Render(cpu) + "   " +
		"RAM " + maybeMeter(c.RAMUsage, ramUsed, c.RAM, 8<<30) + " " + muted.Render(ram) + "   " +
		"Disk " + maybeMeter(c.Disk > 0, diskUsed, c.Disk, 64<<30) + " " + muted.Render(disk) + vram
	bare := hint("c", "Host capacity") + "   CPU " + muted.Render(cpu) + "   RAM " + muted.Render(ram) + "   Disk " + muted.Render(disk)
	tight := brand.Render("c") + " CPU " + muted.Render(cpu) + "  RAM " + muted.Render(ram) + "  Disk " + muted.Render(disk)
	compact := func(s string) string { return strings.NewReplacer(" / ", "/", " GiB", "G", " cores", "c").Replace(s) }
	least := brand.Render("c") + " CPU " + muted.Render(compact(cpu)) + " RAM " + muted.Render(compact(ram)) + " Disk " + muted.Render(compact(disk))
	for _, line := range []string{full, bare, tight} {
		if ansi.StringWidth(line) <= w {
			return line
		}
	}
	return least
}

func maybeMeter(known bool, used, total, unit uint64) string {
	if !known || total == 0 {
		return muted.Render("—")
	}
	n := int(min(16, (total+unit-1)/unit))
	fill := int(min(uint64(n), (used+unit-1)/unit))
	return brand.Render(strings.Repeat("▮", fill)) + muted.Render(strings.Repeat("▯", n-fill))
}

func formatGiB(n uint64) string { return fmt.Sprintf("%.0f GiB", float64(n)/(1<<30)) }

func usedOf(used, total uint64) string {
	return fmt.Sprintf("%.0f / %.0f GiB", float64(used)/(1<<30), float64(total)/(1<<30))
}

func totalGiB(n uint64) string {
	if n == 0 {
		return "—"
	}
	return formatGiB(n)
}

func (m *dashModel) renderFiles(w, h int) []string {
	t := m.fileTree()
	if !t.decided && t.root != nil && t.root.files > 0 {
		t.closed, t.decided = t.root.nodes > max(1, h-2), true
	}
	rows := t.rows()
	label := "Changes"
	if m.showFiles {
		label = "Files"
	}
	header := brand.Bold(true).Render(label)
	if t.editing || t.filter != "" {
		query := "/ " + safeText(t.filter)
		if t.editing {
			query += "▏"
		}
		shown := 0
		for _, r := range rows {
			if !r.node.dir {
				shown++
			}
		}
		header += "  " + brand.Render(query) + "  " + muted.Render(fmt.Sprintf("%d shown", shown))
	} else {
		header += "  " + hint("f", "See files") + "  " + hint("g", "See changes")
	}
	load, reading, from := m.treeReading()
	if load.pending && !load.quiet && len(rows) > 0 && m.now().Sub(load.since) >= loadingBeat {
		header += "  " + brand.Render(m.spinner())
	}
	var lines []string
	if t.root != nil && t.root.files > 0 {
		lines = append(lines, m.treeSummary(t))
	}
	sel, at := -1, t.index(rows)
	for i, r := range rows {
		if i == at {
			sel = len(lines)
		}
		lines = append(lines, m.treeLine(r, i == at, t.flat, w))
	}
	switch {
	case len(rows) > 0:
	case load.pending && t.filter == "":
		if elapsed := m.now().Sub(load.since); elapsed >= loadingBeat {
			if elapsed >= 3*time.Second {
				reading += " · " + elapsed.Truncate(time.Second).String()
			}
			lines = append(lines, m.paneLoading(reading, w, h-1, from, m.guestBusy())...)
		}
	case load.err != "" && t.filter == "":
		what := "changes"
		if m.showFiles {
			what = "files"
		}
		for _, line := range strings.Split(ansi.Wrap("Couldn't read "+what+": "+load.err+" · retrying on the next refresh", max(8, w-2), ""), "\n") {
			lines = append(lines, warnText.Render(line))
		}
	default:
		msg := "No changes in this task's sandbox yet"
		switch {
		case t.filter != "":
			msg = "No matches · Esc clears"
		case m.task().ID == "":
			msg = "Select a task to see its sandbox"
		case m.showFiles:
			msg = "No sandbox files for this task yet"
		}
		for _, line := range strings.Split(ansi.Wrap(msg, max(8, w), ""), "\n") {
			lines = append(lines, muted.Render(line))
		}
	}
	if _, ok := m.verdictFor(m.task()); ok && h >= 4 {
		var foot []string
		if r, ok := m.runningReview(); ok && m.preview && h >= 14 {
			foot = append(foot, m.reviewCard(r, w)...)
		}
		foot = append(foot, m.verdictButton(w))
		rest := h - len(foot)
		start := 0
		if sel >= 0 {
			start = max(0, sel-rest+2)
		}
		return append(window(header, lines, start, w, rest), foot...)
	}
	start := 0
	if sel >= 0 {
		start = max(0, sel-h+2)
	}
	return window(header, lines, start, w, h)
}

func (m *dashModel) treeSummary(t *fileTree) string {
	line := muted.Render(fmt.Sprintf("%d files", t.root.files))
	if m.showFiles {
		if t.root.files >= 3000 {
			line += muted.Render(" · first 3000")
		}
		if m.filesSource != "" {
			line += muted.Render(" · " + m.filesSource)
		}
		return line
	}
	if s := rollup(t.root); s != "" && !strings.HasPrefix(ansi.Strip(s), fmt.Sprint(t.root.files)) {
		line += "  " + s
	}
	if label := m.baseLabel(); label != "" {
		line += "  " + muted.Render(label)
	}
	return line
}

func short(rev string) string { return rev[:min(7, len(rev))] }

// treeLine draws one row in w cells; a folded folder shortens its name from the left to keep its counts.
func (m *dashModel) treeLine(r treeRow, selected, flat bool, w int) string {
	var b strings.Builder
	reading := m.preview && !r.node.dir && r.node.path == m.previewPath
	if (selected && m.focus == 1) || reading {
		b.WriteString(brand.Render("› "))
	} else {
		b.WriteString("  ")
	}
	if g := glyph(m.glyphs, markGuide); g != "" {
		b.WriteString(strings.Repeat(ruleText.Render(g)+" ", r.depth))
	} else {
		b.WriteString(strings.Repeat("  ", r.depth))
	}
	n := r.node
	if n.dir {
		fold := glyph(m.glyphs, markFoldClosed)
		if r.open {
			fold = glyph(m.glyphs, markFoldOpen)
		}
		foldStyle := muted
		if !r.open {
			foldStyle = statusStyle(n.strongest())
		}
		if fold != "" {
			b.WriteString(foldStyle.Render(fold) + " ")
		}
		if icon := folderIcon(m.glyphs, r.open); icon != "" {
			b.WriteString(muted.Render(icon) + " ")
		}
		name := safeText(n.name)
		suffix := ""
		if !r.open {
			if s := rollup(n); s != "" {
				suffix = "  " + s
			}
		}
		if room := w - ansi.StringWidth(b.String()) - ansi.StringWidth(suffix); w > 0 && room >= 4 {
			name = keepTail(name, room)
		}
		if i := strings.LastIndex(name, "/"); i >= 0 {
			b.WriteString(muted.Render(name[:i+1]))
			name = name[i+1:]
		}
		style := inkText
		if selected {
			style = style.Bold(true)
		}
		b.WriteString(style.Render(name))
		if m.glyphs == ui.GlyphsOff {
			b.WriteString("/")
		}
		b.WriteString(suffix)
		return b.String()
	}
	letter := changeLetter(n.change.Kind)
	style := statusStyle(letter)
	if letter != 0 {
		b.WriteString(style.Render(string(letter)) + " ")
	}
	if icon := fileIcon(m.glyphs, n.name); icon != "" {
		b.WriteString(style.Render(icon) + " ")
	}
	name, split := n.name, 0
	if flat {
		name = n.path
		split = len([]rune(name)) - len([]rune(path.Base(name)))
	}
	nameStyle := style
	switch letter {
	case 0:
		nameStyle = inkText
	case 'D':
		nameStyle = muted.Strikethrough(true)
	}
	if selected {
		nameStyle = nameStyle.Bold(true)
	}
	if reading {
		nameStyle = nameStyle.Bold(true).Underline(true).UnderlineColor(hexColor(ui.ColorBrand))
	}
	b.WriteString(emphasise(safeText(name), r.hits, split, nameStyle))
	if old := n.change.OldPath; old != "" {
		if path.Dir(old) == path.Dir(n.change.Path) {
			old = path.Base(old)
		}
		b.WriteString(muted.Render(" ← " + safeText(old)))
	}
	return b.String()
}

func (m *dashModel) renderTasks(w, h int) []string {
	m.pop.ok = false
	title := []string{"Task Dashboard", "Task graph", "Schedule"}[m.view]
	header := brand.Bold(true).Render(title)
	var rows []string
	anchor, rowH := 0, 1
	if m.view == 1 {
		m.cardPos = nil
		rows, m.graphOrder, anchor = m.renderGraph(w)
	} else {
		labels := m.groupLabels()
		cols, width := cardGrid(w)
		height := m.cardHeight()
		m.cardPos = map[int]cardPos{}
		prev, button := -1, false
		var cards [][]string
		var idx []int
		flush := func() {
			for k, i := range idx {
				m.cardPos[i] = cardPos{line: len(rows) + (k/cols)*height, col: k % cols}
			}
			rows = append(rows, placeCards(cards, cols, width, cardGap, w)...)
			cards, idx = nil, nil
		}
		const queued = 3
		addButton := func() {
			if button {
				return
			}
			if prev != queued {
				flush()
				rows = append(rows, groupHeader(labels[queued], w))
				prev = queued
			}
			cards = append(cards, m.newInstanceCard(width))
			flush()
			button = true
		}
		for _, i := range m.taskOrder() {
			t := m.data.Tasks[i]
			g := m.groupOf(t)
			if g > queued {
				addButton()
			}
			if g != prev {
				flush()
				rows = append(rows, groupHeader(labels[g], w))
				prev = g
			}
			cards, idx = append(cards, m.taskCard(i, t, width)), append(idx, i)
		}
		addButton()
		flush()
		if p, ok := m.cardPos[m.selected]; ok {
			anchor, rowH = p.line, height
		}
	}
	start := max(0, anchor-max(0, (h-1-rowH)/2))
	start = min(max(0, start), max(0, len(rows)-1))
	if row := anchor - start + 1; m.selected < len(m.data.Tasks) && row >= 1 && row+rowH <= h {
		m.pop.row, m.pop.rowH, m.pop.ok = row, rowH, true
	}
	return window(header, rows, start, w, h)
}

func (m *dashModel) taskMarker(i int) string {
	if i == m.selected {
		return brand.Render("› ")
	}
	return "  "
}

func (m *dashModel) graphLine(g *taskGraph, t Task, w int) string {
	path := g.path(t.ID)
	blocker := ""
	if t.Reason != "" && (t.State == schedule.Queued || t.State == schedule.Attention) {
		blocker = safeText(t.Reason)
	} else if len(t.DependsOn) > 1 {
		blocker = "requires " + safeText(strings.Join(t.DependsOn, ", "))
	}
	if blocker != "" {
		blocker = " · " + ansi.Truncate(blocker, max(5, w/2), "…")
	}
	width := max(1, w-ansi.StringWidth(blocker))
	if !m.graphExpanded && path.count > 2 && path.width > width {
		middle := fmt.Sprintf(" → %d tasks → ", path.count-2)
		end := max(1, (width-ansi.StringWidth(middle))/2)
		return ansi.Truncate(path.first, end, "…") + middle + ansi.Truncate(path.label, end, "…") + blocker
	}
	var labels []string
	for p := path; p != nil; p = p.parent {
		labels = append(labels, p.label)
	}
	slices.Reverse(labels)
	return ansi.Truncate(strings.Join(labels, " → "), width, "…") + blocker
}

func (m *dashModel) renderPreview(w, h int) []string {
	d, mode := m.previewDoc, m.previewMode()
	body := max(1, h-1)
	m.previewScroll = min(m.previewScroll, max(0, d.size(mode)-body))
	gap := "┄"
	if m.glyphs == ui.GlyphsASCII || m.glyphs == ui.GlyphsOff {
		gap = "-"
	}
	base := m.baseLabel()
	if base == "" {
		base = "since the base"
	}
	return window(m.previewTitle(w), d.lines(mode, m.previewScroll, body, w, gap, base), 0, w, h)
}

// previewMode is file, unified or side by side, as p, u and i chose.
func (m *dashModel) previewMode() previewMode {
	d := m.previewDoc
	switch {
	case !m.patch && isDiagram(d.path) && !m.diagramSource && d.sketchNote == "" && d.note == "" && !d.deleted:
		return previewSketch
	case !m.patch:
		return previewFile
	case m.split:
		return previewSplit
	}
	return previewUnified
}

// previewTitle is the path, what the pane shows, and where the file sits among the changes.
func (m *dashModel) previewTitle(w int) string {
	d := m.previewDoc
	label := []string{"file", "diff", "side by side", "sketch · plantuml -tutxt drops arrow labels and colours"}[m.previewMode()]
	if !m.patch && d.sketchNote != "" {
		label = "file · no sketch: " + d.sketchNote
	}
	if d.deleted && !m.patch {
		label = "deleted · base copy"
	}
	if d.truncated {
		label += " · first 128 KB"
	}
	if at, files := m.fileTree().position(m.previewPath); at > 0 {
		label += fmt.Sprintf(" · %d of %d", at, files)
	}
	tail := muted.Render(" · " + label)
	room := max(4, w-ansi.StringWidth(tail)-2)
	return brand.Render(keepTail(safeText(m.previewPath), room)) + tail
}

// footer leads with quit, then adds keys by priority while the status tail keeps its room.
func (m *dashModel) footer(w int) string {
	hints := []string{hint("Ctrl+C", "quit")}
	add := func(k, action string) { hints = append(hints, hint(k, action)) }
	tree := m.tree
	switch {
	case m.focus == 1 && tree != nil && tree.editing:
		add("↵", "keep filter")
		add("Esc", "clear")
		add("↑↓", "move")
	case m.report != nil && !m.preview && m.focus == 2 && m.sel.on:
		add("y", "copy")
		add("↑↓", "extend")
		add("Esc", "clear")
		add("Y", "copy all")
	case m.report != nil && !m.preview && m.focus == 2:
		add("Esc", "back to tasks")
		add("↑↓", "scroll")
		add("n p", "next / previous reference")
		if r := m.report; m.reportAt >= 0 && m.reportAt < len(r.refs) {
			add("↵", "open "+safeText(r.refs[m.reportAt].label))
		}
		add("v", "select")
		add("Y", "copy all")
		add("o", "open in the system viewer")
		if t := m.task(); t.ID == m.report.task && m.endedRun(t) && m.canFollowUp(t) {
			add("s", "follow up")
		}
	case m.preview && m.focus == 2:
		add("Esc", "back to files")
		add("↑↓", "scroll")
		add("n p", "next / previous file")
		if isDiagram(m.previewPath) {
			if m.diagramSource {
				add("s", "sketch")
			} else {
				add("s", "source")
			}
			add("o", "open SVG")
		}
		if m.patch {
			add("d", "file")
			if m.split {
				add("u", "unified")
			} else {
				add("i", "side by side")
			}
		} else {
			add("d", "diff")
		}
	case m.inSchedule():
		m.scheduleHints(add)
	case m.focus == 1:
		add("Esc", "back to tasks")
		add("↑↓", "move")
		if n := m.fileTree().selected(); n != nil && n.dir {
			add("↵", "Fold")
		} else if n != nil {
			add("↵", "Open file")
		}
		add("/", "filter")
		add("←→", "fold")
		add("`", "tree/flat")
		add("n p", "open next / previous")
	default:
		add("↑↓", "move")
		t := m.task()
		switch {
		case t.Review != nil:
			add("↵", "Agentic review")
		case m.endedRun(t):
			switch {
			case t.Report != "":
				add("↵", "Report")
				if m.canFollowUp(t) {
					add("s", "follow up")
				}
			case m.canFollowUp(t):
				add("↵", "Follow up")
			}
			if canRetry(t) {
				add("r", "retry")
			}
			if canCleanUp(t) {
				add("x", "clean up")
			}
		case t.ID != "" && m.groupOf(t) == 1:
			add("↵", "Follow up")
		case t.Live:
			add("↵", "Attach")
		}
		if t.ID != "" {
			action := "See details"
			if m.details {
				action = "close details"
			}
			add("t", action)
		}
		if t := m.task(); t.Guest && t.Sandbox != "" {
			add("p", "ports")
		}
		if _, ok := m.runningReview(); ok && m.preview {
			add("a", "attach review")
		}
		if _, ok := m.verdictFor(m.task()); ok {
			add("v", "verdict")
		}
		add("f", "files")
		add("g", "changes")
	}
	if t := m.task(); !m.inSchedule() && t.Guest && m.guest.id == t.ID && m.guest.sandbox == t.Sandbox && m.guest.vmid == t.VMID && m.guest.fresh {
		add("e", "Fresh")
	}
	if m.focus == 0 && !m.inSchedule() {
		add("n", "new instance")
	}
	add("?", "help")
	const sep = "   "
	out := hints[0]
	status := safeText(m.status)
	reserve := 0
	if status != "" {
		reserve = len(sep) + min(ansi.StringWidth(status), max(0, w-ansi.StringWidth(out)-len(sep)))
	}
	for _, h := range hints[1:] {
		if ansi.StringWidth(out)+len(sep)+ansi.StringWidth(h)+reserve > w {
			break
		}
		out += sep + h
	}
	if status != "" {
		if room := w - ansi.StringWidth(out) - len(sep); room > 0 {
			style := muted
			if m.alert {
				style = errorText
			}
			out += sep + style.Render(keepTail(status, room))
		}
	}
	return out
}

func window(title string, rows []string, start, w, h int) []string {
	out := make([]string, h)
	out[0] = pad(title, w)
	start = min(max(0, start), max(0, len(rows)-1))
	for i := 1; i < h; i++ {
		if start+i-1 < len(rows) {
			out[i] = pad(rows[start+i-1], w)
		} else {
			out[i] = strings.Repeat(" ", max(0, w))
		}
	}
	return out
}

func pad(s string, w int) string {
	s = ansi.Truncate(s, max(0, w), "…")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

func safeText(s string) string { return strings.Join(strings.Fields(safeMultiline(s)), " ") }

func safeMultiline(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(s))
}
