// SPEC: _spec/internal/dashboard/review-requests.puml
package choiceui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ReviewTemplate is one opening prompt a review can start from.
type ReviewTemplate struct {
	Name, Description string
}

// ReviewStart is the card that starts a review or submits a verdict: the PR, a line under it, one choice of
// several, a text field, then the action or Cancel.
type ReviewStart struct {
	d         dialog
	pr, agent string
	templates []ReviewTemplate

	choice, field, placeholder, verb string         // "Prompt", "Notes", the empty field's hint, the key line's word for ↑↓
	needsText                        func(int) bool // choices that cannot go without text
	warn                             string
	sel                              int
	notes                            []rune
	pos                              int
	done                             bool
	start                            bool
}

// NewReviewStart opens on the first template with empty notes; Start is the selected button.
func NewReviewStart(pr, agent string, templates []ReviewTemplate, width, height int) *ReviewStart {
	d := newDialog("Start a review", nil, 1, dialogButton{Label: "Start", Key: 's'}, dialogButton{Label: "Cancel", Key: 'c'})
	d.sel, d.w, d.h = 0, width, height
	return &ReviewStart{d: d, pr: pr, agent: agent, templates: templates, choice: "Prompt", field: "Notes",
		placeholder: "optional: concerns, files to look at, what to skip", verb: "prompt"}
}

// Result is the chosen template and the notes as typed.
func (r *ReviewStart) Result() (int, string) { return r.sel, strings.TrimSpace(string(r.notes)) }

// Update takes ↑↓ for the template, typing for the notes, enter to start and esc to cancel.
func (r *ReviewStart) Update(msg tea.Msg) (done, start bool) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.d.w, r.d.h = msg.Width, msg.Height
	case tea.PasteMsg:
		r.insert([]rune(strings.Join(strings.Fields(msg.Content), " ")))
	case tea.KeyPressMsg:
		switch msg.String() {
		case "enter":
			if r.needsText != nil && r.needsText(r.sel) && strings.TrimSpace(string(r.notes)) == "" {
				r.warn = "GitHub needs a " + strings.ToLower(r.field) + " for " + strings.ToLower(r.templates[r.sel].Name)
				return false, false
			}
			r.done, r.start = true, true
		case "esc", "ctrl+c":
			r.done = true
		case "up", "shift+tab":
			r.sel = (r.sel + len(r.templates) - 1) % max(1, len(r.templates))
		case "down", "tab":
			r.sel = (r.sel + 1) % max(1, len(r.templates))
		case "left":
			r.pos = max(0, r.pos-1)
		case "right":
			r.pos = min(len(r.notes), r.pos+1)
		case "home", "ctrl+a":
			r.pos = 0
		case "end", "ctrl+e":
			r.pos = len(r.notes)
		case "backspace":
			if r.pos > 0 {
				r.notes = append(r.notes[:r.pos-1], r.notes[r.pos:]...)
				r.pos--
			}
		case "delete", "ctrl+d":
			if r.pos < len(r.notes) {
				r.notes = append(r.notes[:r.pos], r.notes[r.pos+1:]...)
			}
		case "ctrl+u":
			r.notes, r.pos = r.notes[r.pos:], 0
		default:
			if t := msg.Text; t != "" {
				r.insert([]rune(t))
			}
		}
	}
	return r.done, r.done && r.start
}

func (r *ReviewStart) insert(text []rune) {
	r.warn = ""
	var keep []rune
	for _, c := range text {
		if !unicode.IsControl(c) {
			keep = append(keep, c)
		}
	}
	r.notes = append(r.notes[:r.pos], append(keep, r.notes[r.pos:]...)...)
	r.pos += len(keep)
}

// View is the card centred on its own screen, as the confirmations draw.
func (r *ReviewStart) View() tea.View {
	v := tea.NewView(lipgloss.Place(r.d.w, r.d.h, lipgloss.Center, lipgloss.Center, r.card().view()))
	v.AltScreen = true
	return v
}

func (r *ReviewStart) keyParts() []hintPart {
	return []hintPart{{"↑↓", r.verb, 0}, {"type", strings.ToLower(r.field), 0}, {enterMark(r.d.glyphs), strings.ToLower(r.d.buttons[0].Label), 0}, {"esc", "cancel", 0}}
}

// card is title, the PR and agent, the templates, the notes field, a rule, the buttons and the key line.
func (r *ReviewStart) card() *screen {
	p := styles()
	g := glyphsFor(r.d.glyphs)
	nameW := 0
	for _, t := range r.templates {
		nameW = max(nameW, textWidth(t.Name))
	}
	w := max(56, modalButtonX+lipgloss.Width(r.d.buttonRow())+modalRight, modalKeyX+textWidth(joinHint(r.keyParts()))+modalRight)
	for _, t := range r.templates {
		w = max(w, modalBodyX+2+nameW+2+textWidth(t.Description)+modalRight)
	}
	w = max(w, modalBodyX+textWidth(r.pr)+modalRight)
	w = min(w, max(40, r.d.w-4))
	h := 2 + 1 + 2 + 1 + 1 + len(r.templates) + 1 + 1 + 1 + 1 + 1 + 1 + 1 + 1
	s := newScreen(w, h)
	c := newCanvas(s)
	k := card{w: w, framed: true, edge: w - 2, g: g, p: p}
	k.border(c, k.g.frameTL, k.g.frameTR)
	c.y++
	row := func(paint func()) {
		k.line(c, false, paint)
	}
	row(func() { c.put(modalTitleX, p.title, r.d.title) })
	row(func() { c.put(modalBodyX, p.body, clip(r.pr, w-modalBodyX-modalRight)) })
	row(func() { c.put(modalBodyX, p.word, clip(r.agent, w-modalBodyX-modalRight)) })
	row(func() {})
	row(func() { c.put(modalTitleX, p.accent, r.choice) })
	for i, t := range r.templates {
		row(func() {
			mark, name := radio(r.d.glyphs, false), p.body
			if i == r.sel {
				mark, name = radio(r.d.glyphs, true), p.title
			}
			c.put(modalBodyX, p.brand, mark)
			c.put(modalBodyX+2, name, t.Name)
			c.put(modalBodyX+2+nameW+2, p.word, clip(t.Description, w-(modalBodyX+2+nameW+2)-modalRight))
		})
	}
	row(func() {})
	row(func() { c.put(modalTitleX, p.accent, r.field) })
	row(func() { r.putNotes(c, p, modalBodyX, w-modalBodyX-modalRight) })
	row(func() {
		if r.warn != "" {
			c.put(modalBodyX, p.warn, clip(r.warn, w-modalBodyX-modalRight))
		}
	})
	k.foot(c)
	c.y++
	row(func() { r.d.putButtons(c, modalButtonX) })
	row(func() {})
	row(func() { paintHint(c, p, r.keyParts(), modalKeyX, k.edge-modalKeyX) })
	k.bottom(c)
	return s
}

// putNotes draws the field w cells wide with its cursor, scrolled so the cursor shows; empty, it shows a hint.
func (r *ReviewStart) putNotes(c *canvas, p palette, x, w int) {
	c.put(x, p.brand, "›")
	x, w = x+2, max(1, w-2)
	if len(r.notes) == 0 {
		c.put(x, p.focus, " ")
		c.put(x+1, p.word, clip(r.placeholder, w-1))
		return
	}
	start := max(0, r.pos-w+1)
	shown := r.notes[start:min(len(r.notes), start+w)]
	c.put(x, p.body, string(shown))
	under := " "
	if r.pos < len(r.notes) {
		under = string(r.notes[r.pos])
	}
	c.put(x+textWidth(string(r.notes[start:r.pos])), p.focus, under)
}

// radio is a template's mark: filled when chosen; plain tiers use * and o.
func radio(t GlyphTier, on bool) string {
	switch {
	case plainGlyphs(t) && on:
		return "*"
	case plainGlyphs(t):
		return "o"
	case on:
		return "●"
	}
	return "○"
}

// NewVerdict is the card that submits a review of a pull request: Comment, Approve or Request changes, and its text.
// Comment and Request changes cannot go without text.
func NewVerdict(pr, who string, width, height int) *ReviewStart {
	d := newDialog("Submit a review", nil, 1, dialogButton{Label: "Submit", Key: 's'}, dialogButton{Label: "Cancel", Key: 'c'})
	d.sel, d.w, d.h = 0, width, height
	return &ReviewStart{d: d, pr: pr, agent: who, templates: []ReviewTemplate{
		{"Comment", "general feedback, without approving"},
		{"Approve", "approve merging these changes"},
		{"Request changes", "feedback that must be addressed before merging"},
	}, choice: "Verdict", field: "Comment", placeholder: "required for Comment and Request changes", verb: "verdict",
		needsText: func(i int) bool { return i != 1 }}
}
