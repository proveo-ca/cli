// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package dashboard

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
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
	if m.confirm != nil && !m.sbxTab {
		v := m.confirm.View()
		v.BackgroundColor = dashBG
		return v
	}
	tab := func(on bool, name string) string {
		if on {
			return brand.Bold(true).Render("[" + name + "]")
		}
		return muted.Render(name)
	}
	path := safeText(ui.Tilde(m.CWD))
	switcher := hint("Ctrl+G Tab", "switch view")
	header := pad(ansi.Truncate(path, max(4, m.width-44), "…")+"  "+tab(!m.sbxTab, "proveo")+"  "+tab(m.sbxTab, "sbx")+"   "+switcher, m.width)
	var content string
	var cursor *tea.Cursor
	if m.sbxTab {
		if m.child != nil {
			content, cursor = m.child.Frame()
			if cursor != nil {
				cursor.Y++
			}
		} else {
			content = pad(m.status, m.width)
		}
	} else {
		content = m.renderDashboard()
	}
	v := tea.NewView(header + "\n" + content)
	v.AltScreen = true
	v.BackgroundColor, v.ForegroundColor = dashBG, ink
	v.Cursor = cursor
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *dashModel) renderDashboard() string {
	if m.wizard != nil {
		return m.renderWizard()
	}
	if m.help {
		return strings.Join([]string{"Review queue · Task graph · Schedule", "1 / 2 / 3  switch task view   4  task detail options",
			"Tab / Shift+Tab  focus pane   ↑↓  move / scroll", "Enter  attach or open file   e  guest Fresh",
			"f  See files   g  See changes   t  See details", "n  new instance   c  host capacity", "Esc  back   Ctrl+C  quit"}, "\n")
	}
	h := max(1, m.height-3)
	left := max(1, (m.width-1)*36/100)
	right := max(1, m.width-left-1)
	lf := m.renderFiles(left, h)
	rf := m.renderTasks(right, h)
	if m.preview {
		rf = m.renderPreview(right, h)
	}
	if m.details && m.detailPlacement == 2 && !m.preview && m.focus != 1 {
		lf = m.renderDetails(left, h)
	}
	rows := []string{pad(m.capacity(), m.width)}
	for line := 0; line < h; line++ {
		rows = append(rows, pad(lf[line], left)+muted.Render("│")+pad(rf[line], right))
	}
	return strings.Join(append(rows, pad(m.footer(), m.width)), "\n")
}

func (m *dashModel) capacity() string {
	c := m.data.Capacity
	ramUsed := c.RAM - min(c.RAMFree, c.RAM)
	free := c.DiskFree
	used := c.Disk - min(free, c.Disk)
	line := hint("c", "Host capacity") + "   " +
		"CPU " + maybeMeter(c.CPUUsage, uint64(c.BusyCores), uint64(c.Cores), 2) + " " + muted.Render(fmt.Sprintf("%d cores", c.Cores)) + "   " +
		"RAM " + maybeMeter(c.RAMUsage, ramUsed, c.RAM, 8<<30) + " " + muted.Render(totalGiB(c.RAM)) + "   " +
		"Disk " + maybeMeter(c.Disk > 0, used, c.Disk, 64<<30) + " " + muted.Render(totalGiB(c.Disk))
	if c.VRAM > 0 {
		line += "   VRAM " + muted.Render(formatGiB(c.VRAM))
	}
	return line
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

func totalGiB(n uint64) string {
	if n == 0 {
		return "—"
	}
	return formatGiB(n)
}

func (m *dashModel) renderFiles(w, h int) []string {
	label := "Changes"
	if m.showFiles {
		label = "Files"
	}
	header := brand.Bold(true).Render(label) + "  " + hint("f", "See files") + "  " + hint("g", "See changes")
	files := m.files()
	var rows []string
	selected, selectedRow := min(m.file, len(files)-1), 0
	var folder []string
	for i, c := range files {
		parts := strings.Split(filepath.ToSlash(c.Path), "/")
		parent := parts[:len(parts)-1]
		shared := 0
		for shared < min(len(folder), len(parent)) && folder[shared] == parent[shared] {
			shared++
		}
		for level := shared; level < len(parent); level++ {
			rows = append(rows, strings.Repeat("  ", level+1)+muted.Render(safeText(parent[level])+"/"))
		}
		folder = parent
		path := safeText(parts[len(parts)-1])
		mark := strings.TrimSpace(c.Kind)
		if mark == "D" {
			path = muted.Strikethrough(true).Render(path)
			mark = errorText.Render(mark)
		} else if mark != "" {
			mark = brand.Render(mark)
		}
		prefix := strings.Repeat("  ", len(parent)+1)
		if i == selected {
			selectedRow = len(rows)
			if m.focus == 1 {
				prefix = brand.Render("› ") + prefix[2:]
			}
		}
		rows = append(rows, prefix+mark+" "+path)
	}
	if len(rows) == 0 {
		rows = append(rows, muted.Render("No files to show"))
	}
	return window(header, rows, max(0, selectedRow-h+2), w, h)
}

func (m *dashModel) renderTasks(w, h int) []string {
	taskHeight := h
	if m.details && m.detailPlacement == 0 && !m.preview {
		taskHeight = max(1, (h+1)/2)
	}
	title := []string{"Review queue", "Task graph", "Schedule"}[m.view]
	header := brand.Bold(true).Render(title) + "  " + muted.Render("1 2 3")
	var rows []string
	anchor := 0
	if m.view == 1 {
		graph := newTaskGraph(m.data.Tasks)
		for i, t := range m.data.Tasks {
			if i == m.selected {
				anchor = len(rows)
			}
			rows = append(rows, m.taskMarker(i)+m.graphLine(graph, t, w-2))
			rows = append(rows, m.inlineDetails(i, w)...)
		}
	} else {
		for g, label := range []string{"Attention required", "Working", "Queued", "Completed"} {
			if g == 3 && m.view != 2 {
				break
			}
			rows = append(rows, muted.Render(label))
			for i, t := range m.data.Tasks {
				if taskGroup(t.State) != g {
					continue
				}
				if i == m.selected {
					anchor = len(rows)
				}
				rows = append(rows, m.taskLine(i, t))
				rows = append(rows, m.inlineDetails(i, w)...)
			}
			if g == 2 {
				rows = append(rows, muted.Render("┌─────────────────┐"), "│ "+hint("n", "new instance")+"  │", muted.Render("└─────────────────┘"))
			}
		}
	}
	start := max(0, anchor-(taskHeight-2)/2)
	out := window(header, rows, start, w, taskHeight)
	if taskHeight < h {
		out = append(out, m.renderDetails(w, h-taskHeight)...)
	}
	return out
}

func (m *dashModel) taskMarker(i int) string {
	if i == m.selected {
		return brand.Render("› ")
	}
	return "  "
}

func (m *dashModel) taskLine(i int, t Task) string {
	meta := safeText(t.Phase)
	if t.State == schedule.Queued && t.Reason != "" {
		meta = safeText(t.Reason)
	}
	if m.view == 2 && !t.Updated.IsZero() {
		meta = t.Updated.Local().Format("Jan 02 15:04") + "  " + meta
	}
	line := m.taskMarker(i) + safeText(t.Title) + "  " + muted.Render(meta)
	if i == m.selected {
		line += "  " + hint("t", "See details")
	}
	return line
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

func (m *dashModel) inlineDetails(i, w int) []string {
	if !m.details || m.detailPlacement != 1 || i != m.selected || m.preview {
		return nil
	}
	return m.detailLines(w)
}

func (m *dashModel) renderDetails(w, h int) []string {
	return window(brand.Bold(true).Render("Task details"), m.detailLines(w), m.detailScroll, w, h)
}

func (m *dashModel) detailLines(w int) []string {
	t := m.task()
	if t.ID == "" {
		return []string{muted.Render("No task selected")}
	}
	s := m.guest
	if s.id != t.ID || s.sandbox != t.Sandbox || s.vmid != t.VMID {
		s = guestSample{}
	}
	label := "Sandbox usage"
	if !s.at.IsZero() {
		label += " · " + s.at.Local().Format("15:04:05")
	}
	rows := []string{muted.Render(label), "CPU " + absent(s.cpu) + "   RAM " + absent(s.ram) + "   Disk " + absent(s.disk)}
	rows = append(rows, brand.Render(strings.Trim(safeText(t.Git)+" + "+safeText(t.Source), " +")))
	meta := m.metadata[metadataKey(t)]
	if meta.pr != "" {
		t.PR = meta.pr
	}
	for _, v := range [][2]string{{"Ticket", t.Ticket}, {"Branch", t.Branch}, {"Instance", t.Instance}, {"Sandbox", t.Sandbox}, {"PR", t.PR}} {
		if v[1] != "" {
			rows = append(rows, muted.Render(v[0])+"  "+safeText(v[1]))
		}
	}
	if !t.LastPush.IsZero() {
		rows = append(rows, muted.Render("Last pushed "+t.LastPush.Local().Format("Jan 02, 2006 15:04")))
	}
	if meta.excerpt != "" {
		excerpt := strings.Split(ansi.Wrap(safeText(meta.excerpt), max(1, w), ""), "\n")
		if len(excerpt) > 3 {
			excerpt = excerpt[:3]
			excerpt[2] = ansi.Truncate(excerpt[2], max(1, w-1), "") + "…"
		}
		rows = append(rows, excerpt...)
	}
	if t.Tip == "" && t.Base != "" {
		rows = append(rows, muted.Render("Waiting for first carried revision"))
	}
	if t.Prompt != "" {
		prompt := strings.Split(ansi.Wrap(safeMultiline(t.Prompt), max(1, w), ""), "\n")
		rows = append(rows, "", muted.Render("Initial prompt"))
		rows = append(rows, prompt...)
	}
	return rows
}

func (m *dashModel) renderPreview(w, h int) []string {
	return window(brand.Render(safeText(m.previewPath))+" · "+hint("p", "patch"), strings.Split(safeMultiline(m.previewText), "\n"), m.previewScroll, w, h)
}

func (m *dashModel) renderWizard() string {
	a := m.wizard
	head := brand.Bold(true).Render("New instance") + "\n\n"
	var body string
	switch a.step {
	case 0:
		body = "Agent  " + a.input.View() + "\n" + muted.Render(strings.Join(m.Targets, " · "))
	case 1:
		body = "Task label  " + a.input.View()
	case 2:
		body = "Ticket URL (optional)  " + a.input.View()
	case 3:
		body = "Initial prompt\n" + a.prompt.View() + "\n" + hint("Ctrl+S", "Review launch")
	case 4:
		body = safeText(a.values[0]) + " · " + safeText(a.values[1]) + "\n" + safeMultiline(a.prompt.Value()) + "\n" + hint("↵", "Queue instance")
	}
	return head + body + "\n\n" + hint("Esc", "Cancel") + "  " + muted.Render(safeText(m.status))
}

func (m *dashModel) footer() string {
	left := hint("Tab", "pane") + "   " + hint("↑↓", "move")
	if m.focus == 1 && len(m.files()) > 0 {
		left += "   " + hint("↵", "Open file")
	} else if m.task().Live {
		left += "   " + hint("↵", "Attach")
	}
	if t := m.task(); t.Guest && m.guest.id == t.ID && m.guest.sandbox == t.Sandbox && m.guest.vmid == t.VMID && m.guest.fresh {
		left += "   " + hint("e", "Fresh")
	}
	left += "   " + hint("?", "help") + "   " + hint("Ctrl+C", "quit")
	if m.status != "" {
		left += "   " + muted.Render(safeText(m.status))
	}
	return left
}

func absent(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func window(title string, rows []string, start, w, h int) []string {
	out := make([]string, h)
	out[0] = pad(title, w)
	start = min(max(0, start), max(0, len(rows)-1))
	for i := 1; i < h; i++ {
		if start+i-1 < len(rows) {
			out[i] = pad(rows[start+i-1], w)
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
