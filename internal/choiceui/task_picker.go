// SPEC: _spec/_experiments/dashboard/task-picker.puml
package choiceui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// TaskItem is one task the picker offers: a line, its status, and the prompt and label a pick starts with.
type TaskItem struct {
	Title, Status string
	Tone          string // "ok", "warn", "fail", or "" for grey
	Prompt, Task  string
	Branch        string // the working branch the run takes; empty keeps proveo's own local branch
}

// TaskSection is one source's tasks; Note stands in for them while they load or when the source cannot answer.
type TaskSection struct {
	Title string
	Items []TaskItem
	Note  string
}

// TaskPicker is the new-instance card: a filter on top, the sections under it, then the picked task's prompt to edit.
type TaskPicker struct {
	d        dialog
	sections []TaskSection
	query    []rune
	sel      int // index into the visible rows' items
	editing  bool
	picked   TaskItem
	prompt   []rune
	pos      int
	branch   []rune
	bpos     int
	onBranch bool // tab moved the edit keys from the prompt to the branch
	done     bool
	start    bool
}

// blankItem starts a run with no task and no prompt, as the dashboard did before the picker.
var blankItem = TaskItem{Title: "Blank session", Status: "no task, no opening prompt"}

// NewTaskPicker opens with an empty filter on the first row, the blank session.
func NewTaskPicker(sections []TaskSection, width, height int) *TaskPicker {
	d := newDialog("New instance", nil, 0, dialogButton{Label: "Start", Key: 's'}, dialogButton{Label: "Back", Key: 'b'})
	d.w, d.h = width, height
	return &TaskPicker{d: d, sections: sections}
}

// SetSection fills a section once its source answers; the selection keeps its place when it can.
func (t *TaskPicker) SetSection(i int, s TaskSection) {
	if i < 0 || i >= len(t.sections) {
		return
	}
	t.sections[i] = s
	t.sel = min(t.sel, max(0, len(t.visible())-1))
}

// Result is the picked task's label, the prompt and the branch as edited; all empty for a blank session.
func (t *TaskPicker) Result() (task, prompt, branch string) {
	return t.picked.Task, strings.TrimSpace(string(t.prompt)), strings.TrimSpace(string(t.branch))
}

// field is the buffer and cursor the edit keys work on: the prompt, or the branch after tab.
func (t *TaskPicker) field() (*[]rune, *int) {
	if t.onBranch {
		return &t.branch, &t.bpos
	}
	return &t.prompt, &t.pos
}

type pickRow struct {
	section int // -1 for the blank session
	item    TaskItem
	score   int
}

// visible are the items the filter keeps, the blank session first, each section's best matches first.
func (t *TaskPicker) visible() []pickRow {
	q := strings.TrimSpace(string(t.query))
	var rows []pickRow
	if score, ok := fuzzy(q, blankItem.Title); ok {
		rows = append(rows, pickRow{section: -1, item: blankItem, score: score})
	}
	for i, s := range t.sections {
		var in []pickRow
		for _, it := range s.Items {
			if score, ok := fuzzy(q, it.Title+" "+it.Status); ok {
				in = append(in, pickRow{section: i, item: it, score: score})
			}
		}
		if q != "" {
			sortByScore(in)
		} else if len(in) > sectionCap {
			in = in[:sectionCap]
		}
		rows = append(rows, in...)
	}
	return rows
}

// sectionCap is how many rows a section shows before the filter is typed, so every section fits on one screen.
const sectionCap = 8

func sortByScore(rows []pickRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].score > rows[j-1].score; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

// fuzzy matches query's runes in order inside text, case-folded; consecutive runes and word starts score higher.
func fuzzy(query, text string) (int, bool) {
	if query == "" {
		return 0, true
	}
	q, s := []rune(strings.ToLower(query)), []rune(strings.ToLower(text))
	score, qi, last := 0, 0, -2
	for i, r := range s {
		if qi == len(q) {
			break
		}
		if q[qi] == ' ' {
			qi++
			if qi == len(q) {
				break
			}
		}
		if r != q[qi] {
			continue
		}
		score++
		if i == last+1 {
			score += 3
		}
		if i == 0 || !unicode.IsLetter(s[i-1]) && !unicode.IsDigit(s[i-1]) {
			score += 2
		}
		last, qi = i, qi+1
	}
	return score - len(s)/40, qi == len(q)
}

// Update takes typing for the filter, ↑↓ to move, enter to pick and then to start, esc to go back or cancel.
func (t *TaskPicker) Update(msg tea.Msg) (done, start bool) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		t.d.w, t.d.h = msg.Width, msg.Height
	case tea.PasteMsg:
		t.insert([]rune(strings.Join(strings.Fields(msg.Content), " ")))
	case tea.KeyPressMsg:
		if t.editing {
			t.editKey(msg.String(), msg.Text)
		} else {
			t.listKey(msg.String(), msg.Text)
		}
	}
	return t.done, t.done && t.start
}

func (t *TaskPicker) listKey(k, text string) {
	rows := t.visible()
	switch k {
	case "esc", "ctrl+c":
		t.done = true
	case "up", "shift+tab":
		t.sel = max(0, t.sel-1)
	case "down", "tab":
		t.sel = min(max(0, len(rows)-1), t.sel+1)
	case "pgup":
		t.sel = max(0, t.sel-8)
	case "pgdown":
		t.sel = min(max(0, len(rows)-1), t.sel+8)
	case "enter":
		if t.sel >= len(rows) {
			return
		}
		r := rows[t.sel]
		if r.section < 0 {
			t.picked, t.prompt, t.branch, t.done, t.start = TaskItem{}, nil, nil, true, true
			return
		}
		t.picked, t.prompt, t.branch, t.editing, t.onBranch = r.item, []rune(r.item.Prompt), []rune(r.item.Branch), true, false
		t.pos, t.bpos = len(t.prompt), len(t.branch)
	case "backspace":
		if len(t.query) > 0 {
			t.query, t.sel = t.query[:len(t.query)-1], 0
		}
	case "ctrl+u":
		t.query, t.sel = nil, 0
	default:
		if text != "" {
			t.insert([]rune(text))
		}
	}
}

func (t *TaskPicker) editKey(k, text string) {
	buf, pos := t.field()
	switch k {
	case "enter":
		t.done, t.start = true, true
	case "esc":
		t.editing = false
	case "ctrl+c":
		t.done = true
	case "tab", "shift+tab":
		t.onBranch = !t.onBranch
	case "left":
		*pos = max(0, *pos-1)
	case "right":
		*pos = min(len(*buf), *pos+1)
	case "home", "ctrl+a":
		*pos = 0
	case "end", "ctrl+e":
		*pos = len(*buf)
	case "backspace":
		if *pos > 0 {
			*buf = append((*buf)[:*pos-1], (*buf)[*pos:]...)
			*pos--
		}
	case "delete", "ctrl+d":
		if *pos < len(*buf) {
			*buf = append((*buf)[:*pos], (*buf)[*pos+1:]...)
		}
	case "ctrl+u":
		*buf, *pos = (*buf)[*pos:], 0
	default:
		if text != "" {
			t.insert([]rune(text))
		}
	}
}

func (t *TaskPicker) insert(text []rune) {
	var keep []rune
	for _, c := range text {
		switch {
		case unicode.IsControl(c):
		case t.editing && t.onBranch && unicode.IsSpace(c):
			keep = append(keep, '-')
		default:
			keep = append(keep, c)
		}
	}
	if !t.editing {
		t.query, t.sel = append(t.query, keep...), 0
		return
	}
	buf, pos := t.field()
	*buf = append((*buf)[:*pos], append(keep, (*buf)[*pos:]...)...)
	*pos += len(keep)
}

// View is the card centred on its own screen, as the confirmations draw.
func (t *TaskPicker) View() tea.View {
	v := tea.NewView(lipgloss.Place(t.d.w, t.d.h, lipgloss.Center, lipgloss.Center, t.card().view()))
	v.AltScreen = true
	return v
}

func (t *TaskPicker) keyParts() []hintPart {
	if t.editing {
		return []hintPart{{"type", "edit", 0}, {"tab", "prompt · branch", 0}, {enterMark(t.d.glyphs), "start", 0}, {"esc", "back to tasks", 0}}
	}
	return []hintPart{{"type", "filter", 0}, {"↑↓", "move", 0}, {enterMark(t.d.glyphs), "pick", 0}, {"esc", "cancel", 0}}
}

// card is the filter and the sections, or the picked task's prompt, framed like the other cards.
func (t *TaskPicker) card() *screen {
	p := styles()
	g := glyphsFor(t.d.glyphs)
	w := min(max(64, t.d.w*3/4), max(40, t.d.w-4), 120)
	h := max(12, min(t.d.h-2, 40))
	s := newScreen(w, h)
	c := newCanvas(s)
	k := card{w: w, framed: true, edge: w - 2, g: g, p: p}
	k.border(c, k.g.frameTL, k.g.frameTR)
	c.y++
	row := func(paint func()) { k.line(c, false, paint) }
	inner := w - modalBodyX - modalRight
	row(func() { c.put(modalTitleX, p.title, t.d.title) })
	var body []func()
	if t.editing {
		body = t.promptRows(c, p, inner)
	} else {
		body = t.listRows(c, p, inner, h-8)
	}
	for _, paint := range body {
		row(paint)
	}
	for c.y < h-4 {
		row(func() {})
	}
	k.foot(c)
	c.y++
	if t.editing {
		row(func() { t.d.putButtons(c, modalButtonX) })
	} else {
		row(func() {})
	}
	row(func() { paintHint(c, p, t.keyParts(), modalKeyX, k.edge-modalKeyX) })
	k.bottom(c)
	return s
}

// listRows are the filter, then each section's heading and its rows, scrolled so the selection shows.
func (t *TaskPicker) listRows(c *canvas, p palette, w, room int) []func() {
	out := []func(){
		func() {
			c.put(modalBodyX, p.brand, "›")
			if len(t.query) == 0 {
				c.put(modalBodyX+2, p.focus, " ")
				c.put(modalBodyX+3, p.word, clip("type to filter the tasks", w-3))
				return
			}
			c.put(modalBodyX+2, p.body, clip(string(t.query), w-3))
			c.put(modalBodyX+2+textWidth(clip(string(t.query), w-3)), p.focus, " ")
		},
		func() {},
	}
	rows := t.visible()
	type line struct {
		paint func()
		item  int // index into rows, -1 for a heading
	}
	var lines []line
	section := -2
	for i, r := range rows {
		if r.section != section {
			section = r.section
			if section >= 0 {
				title, n := t.sections[section].Title, 0
				for _, x := range rows {
					if x.section == section {
						n++
					}
				}
				if total := len(t.sections[section].Items); len(t.query) == 0 && total > n {
					n = total
				}
				lines = append(lines, line{func() {
					c.put(modalTitleX, p.accent, title+"  ")
					c.put(modalTitleX+textWidth(title)+2, p.word, itoa(n))
				}, -1})
			}
		}
		lines = append(lines, line{t.itemPainter(c, p, r, i == t.sel, w), i})
		if last := i == len(rows)-1 || rows[i+1].section != r.section; last && r.section >= 0 && len(t.query) == 0 {
			if more := len(t.sections[r.section].Items) - sectionCap; more > 0 {
				lines = append(lines, line{func() { c.put(modalBodyX+2, p.word, "+"+itoa(more)+" more · type to filter") }, -1})
			}
		}
	}
	for i, s := range t.sections {
		has := false
		for _, r := range rows {
			has = has || r.section == i
		}
		if !has {
			title, note := s.Title, s.Note
			if note == "" && len(t.query) > 0 {
				note = "nothing matches"
			} else if note == "" {
				note = "nothing to do"
			}
			lines = append(lines, line{func() { c.put(modalTitleX, p.accent, title) }, -1},
				line{func() { c.put(modalBodyX+2, p.word, clip(note, w-2)) }, -1})
		}
	}
	room = max(1, room-len(out))
	at := 0
	for i, l := range lines {
		if l.item == t.sel {
			at = i
		}
	}
	start := max(0, min(at-room/2, len(lines)-room))
	for _, l := range lines[start:min(len(lines), start+room)] {
		out = append(out, l.paint)
	}
	return out
}

func (t *TaskPicker) itemPainter(c *canvas, p palette, r pickRow, selected bool, w int) func() {
	return func() {
		mark, title := " ", p.body
		if selected {
			mark, title = "›", p.title
		}
		c.put(modalBodyX, p.brand, mark)
		status := clip(r.item.Status, max(8, w/3))
		tone := p.word
		switch r.item.Tone {
		case "ok":
			tone = p.ok
		case "warn":
			tone = p.warn
		case "fail":
			tone = p.warn.Bold(true)
		}
		room := w - 2 - textWidth(status) - 2
		c.put(modalBodyX+2, title, clip(r.item.Title, max(4, room)))
		c.put(modalBodyX+w-textWidth(status), tone, status)
	}
}

// promptRows are the picked task, then its prompt wrapped to the card with the cursor where it edits.
func (t *TaskPicker) promptRows(c *canvas, p palette, w int) []func() {
	out := []func(){
		func() { c.put(modalBodyX, p.body, clip(t.picked.Title, w)) },
		func() { c.put(modalBodyX, p.word, clip(t.picked.Status, w)) },
		func() {},
		func() { c.put(modalTitleX, p.accent, "Prompt") },
	}
	lines, cur := wrapRunes(t.prompt, t.pos, max(8, w-2))
	for i, l := range lines {
		out = append(out, func() {
			c.put(modalBodyX, p.brand, map[bool]string{true: "›", false: " "}[i == 0])
			c.put(modalBodyX+2, p.body, string(l))
			if i == cur[0] && !t.onBranch {
				under := " "
				if cur[1] < len(l) {
					under = string(l[cur[1]])
				}
				c.put(modalBodyX+2+textWidth(string(l[:cur[1]])), p.focus, under)
			}
		})
	}
	return append(out, func() {}, func() { c.put(modalTitleX, p.accent, "Branch") }, func() {
		c.put(modalBodyX, p.brand, "›")
		x := modalBodyX + 2
		if len(t.branch) == 0 {
			if t.onBranch {
				c.put(x, p.focus, " ")
			}
			c.put(x+1, p.word, clip("none: proveo/<instance>, kept on this machine", w-3))
			return
		}
		c.put(x, p.body, clip(string(t.branch), w-3))
		if t.onBranch {
			under := " "
			if t.bpos < len(t.branch) {
				under = string(t.branch[t.bpos])
			}
			c.put(x+textWidth(string(t.branch[:t.bpos])), p.focus, under)
		}
	})
}

// wrapRunes breaks text into lines w runes wide at spaces and newlines, and finds the cursor's line and column.
func wrapRunes(text []rune, pos, w int) ([][]rune, [2]int) {
	var lines [][]rune
	cur := [2]int{0, 0}
	start := 0
	place := func(from, to int) {
		if pos >= from && pos <= to {
			cur = [2]int{len(lines), pos - from}
		}
		lines = append(lines, text[from:to])
	}
	for start <= len(text) {
		end := min(len(text), start+w)
		if nl := indexRune(text[start:end], '\n'); nl >= 0 {
			place(start, start+nl)
			start += nl + 1
			continue
		}
		if end < len(text) {
			if sp := lastIndexRune(text[start:end], ' '); sp > 0 {
				end = start + sp + 1
			}
		}
		place(start, end)
		if end == len(text) {
			break
		}
		start = end
	}
	if len(lines) == 0 {
		lines = [][]rune{{}}
	}
	return lines, cur
}

func indexRune(s []rune, r rune) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}

func lastIndexRune(s []rune, r rune) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == r {
			return i
		}
	}
	return -1
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
