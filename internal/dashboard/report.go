// SPEC: _spec/_experiments/dashboard/run-reports.puml
package dashboard

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	glamouransi "charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/mosaic"

	"github.com/proveo-ca/proveo/internal/ui"
)

// reportRef is one place a report points at: a file and line, or a local image.
type reportRef struct {
	label string // as the report writes it: ci.yml:663, or the image's alt text
	path  string // the file as written, or the image's absolute path
	line  int
	image bool
	row   int // the rendered row it sits on; -1 when the render split it
}

// reportDoc is a run's Markdown report, rendered for one pane width.
type reportDoc struct {
	task, file, source string
	rows               []string
	refs               []reportRef
	width              int
	note               string
}

var (
	codeRef  = regexp.MustCompile("`([A-Za-z0-9_.@+/-]*[A-Za-z0-9_+-]):([0-9]+)[0-9,-]*`")
	imageRef = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)`)
)

// newReport reads the report a finished run left on the host.
func newReport(t Task) *reportDoc {
	r := &reportDoc{task: t.ID, file: t.Report}
	b, err := os.ReadFile(t.Report)
	switch {
	case err != nil:
		r.note = "the report is gone: " + safeText(err.Error())
	case len(b) == 0:
		r.note = "the report is empty"
	default:
		r.source = string(b)
	}
	return r
}

// render lays the report out w cells wide: glamour for the text, half-block thumbnails under local images.
func (r *reportDoc) render(w int) {
	if r.width == w && r.rows != nil {
		return
	}
	r.width, r.rows, r.refs = w, nil, nil
	if r.note != "" {
		r.rows = []string{muted.Render(r.note)}
		return
	}
	tagged := r.tag()
	out, err := "", error(nil)
	if md, mdErr := markdownRenderer(max(20, w-2)); mdErr != nil {
		err = mdErr
	} else {
		out, err = md.Render(tagged)
	}
	if err != nil {
		r.rows = plainLines(r.source)
		r.note = "rendered as plain text: " + safeText(err.Error())
		for i := range r.refs {
			r.refs[i].row = -1
		}
		return
	}
	r.rows = strings.Split(strings.Trim(out, "\n"), "\n")
	r.place(w)
}

// tag lists the report's references and returns its source with an invisible marker at the start of each one,
// so the row a reference starts on survives wrapping, tables and styling.
func (r *reportDoc) tag() string {
	type at struct {
		pos int
		ref reportRef
	}
	var spots []at
	for _, m := range codeRef.FindAllStringSubmatchIndex(r.source, -1) {
		path, num := r.source[m[2]:m[3]], r.source[m[4]:m[5]]
		n, _ := strconv.Atoi(num)
		if n > 0 && (strings.ContainsAny(path, "./") || isUpper(path)) {
			spots = append(spots, at{m[2], reportRef{label: path + ":" + num, path: path, line: n}})
		}
	}
	for _, m := range imageRef.FindAllStringSubmatchIndex(r.source, -1) {
		alt, ref := r.source[m[2]:m[3]], r.source[m[4]:m[5]]
		if p := localImage(filepath.Dir(r.file), ref); p != "" {
			spots = append(spots, at{m[2], reportRef{label: firstNonEmpty(alt, filepath.Base(p)), path: p, image: true}})
		}
	}
	slices.SortStableFunc(spots, func(a, b at) int { return a.pos - b.pos })
	var b strings.Builder
	last := 0
	for i, s := range spots {
		b.WriteString(r.source[last:s.pos])
		b.WriteString(refMarker(i))
		last = s.pos
		s.ref.row = -1
		r.refs = append(r.refs, s.ref)
	}
	b.WriteString(r.source[last:])
	return b.String()
}

// refMarker is reference i as invisible format characters: a separator, i in binary, a separator.
func refMarker(i int) string {
	return markEdge + strings.NewReplacer("0", markZero, "1", markOne).Replace(strconv.FormatInt(int64(i), 2)) + markEdge
}

const markEdge, markZero, markOne = "\u2063", "\u2061", "\u2062"

var refMarkers = regexp.MustCompile(markEdge + "([" + markZero + markOne + "]+)" + markEdge)

// place reads each reference's row off its marker, strips the markers, draws each image's thumbnail under it,
// and orders the references as they read top to bottom.
func (r *reportDoc) place(w int) {
	for row, line := range r.rows {
		for _, m := range refMarkers.FindAllStringSubmatch(line, -1) {
			bits := strings.NewReplacer(markZero, "0", markOne, "1").Replace(m[1])
			if i, err := strconv.ParseInt(bits, 2, 32); err == nil && int(i) < len(r.refs) && r.refs[i].row < 0 {
				r.refs[i].row = row
			}
		}
		r.rows[row] = refMarkers.ReplaceAllString(line, "")
	}
	for i := len(r.refs) - 1; i >= 0; i-- {
		ref := r.refs[i]
		if !ref.image || ref.row < 0 {
			continue
		}
		thumb := thumbnail(ref.path, min(w-4, 60))
		if len(thumb) == 0 {
			continue
		}
		at := ref.row + 1
		r.rows = append(r.rows[:at], append(thumb, r.rows[at:]...)...)
		for k := range r.refs {
			if r.refs[k].row >= at {
				r.refs[k].row += len(thumb)
			}
		}
	}
	slices.SortStableFunc(r.refs, func(a, b reportRef) int {
		switch {
		case a.row == b.row:
			return 0
		case a.row < 0:
			return 1
		case b.row < 0:
			return -1
		}
		return a.row - b.row
	})
}

// thumbnail is the image at path in half blocks, w cells wide at most and 18 rows tall at most.
func thumbnail(path string, w int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	if err != nil || w < 4 {
		return nil
	}
	b := img.Bounds()
	h := max(1, min(18, w*b.Dy()/max(1, b.Dx())/2))
	w = max(1, min(w, h*2*b.Dx()/max(1, b.Dy())))
	mo := mosaic.New().Width(w).Height(h)
	return strings.Split(strings.TrimRight(mo.Render(img), "\n"), "\n")
}

func localImage(dir, ref string) string {
	if strings.Contains(ref, "://") {
		return ""
	}
	p := strings.TrimPrefix(ref, "file://")
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif":
	default:
		return ""
	}
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return ""
	}
	return p
}

func isUpper(s string) bool { return s != "" && s[0] >= 'A' && s[0] <= 'Z' }

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// markdownRenderer draws Markdown in the identity palette; code blocks take the same chroma theme as code previews.
func markdownRenderer(w int) (*glamour.TermRenderer, error) {
	return glamour.NewTermRenderer(glamour.WithStyles(reportStyle()), glamour.WithWordWrap(w), glamour.WithTableWrap(true))
}

func reportStyle() glamouransi.StyleConfig {
	c := func(rgb int) *string { s := fmt.Sprintf("#%06X", rgb); return &s }
	yes, zero, one := true, uint(0), uint(1)
	s := styles.DarkStyleConfig
	s.Document.Color, s.Document.Margin, s.Document.BlockPrefix, s.Document.BlockSuffix = c(ui.ColorSecondary), &zero, "", ""
	s.Heading.Color, s.Heading.Bold = c(ui.ColorBrand), &yes
	s.H1.Color, s.H1.BackgroundColor, s.H1.Bold = c(ui.ColorBrand), c(ui.ColorSurface), &yes
	s.H6.Color = c(ui.ColorMuted)
	s.Link.Color, s.Link.Underline = c(ui.ColorBrand), &yes
	s.LinkText.Color, s.LinkText.Underline, s.LinkText.Bold = c(ui.ColorBrand), &yes, nil
	s.Image.Color, s.ImageText.Color = c(ui.ColorMuted), c(ui.ColorMuted)
	s.Code.Color, s.Code.BackgroundColor = c(ui.ColorAccentBright), c(ui.ColorSurface)
	s.CodeBlock.Color, s.CodeBlock.Margin, s.CodeBlock.Chroma, s.CodeBlock.Theme = c(ui.ColorSecondary), &one, nil, codeThemeName()
	s.HorizontalRule.Color = c(ui.ColorRule)
	s.BlockQuote.Color = c(ui.ColorMuted)
	s.Table.Color = c(ui.ColorRule)
	s.Strong.Color = c(ui.ColorSecondary)
	return s
}

// openReport shows the selected run's report in the right pane.
func (m *dashModel) openReport(t Task) {
	if t.Report == "" {
		return
	}
	m.report, m.reportScroll, m.reportAt, m.sel = newReport(t), 0, -1, reportSelection{}
	m.preview, m.focus, m.details = false, 2, false
}

// renderReport draws the report pane: the path as its title, a gutter mark on the selected reference's row.
func (m *dashModel) renderReport(w, h int) []string {
	r := m.report
	r.render(max(1, w-2))
	body := max(1, h-1)
	m.reportScroll = min(max(0, m.reportScroll), max(0, len(r.rows)-body))
	sel := -1
	if m.reportAt >= 0 && m.reportAt < len(r.refs) {
		sel = r.refs[m.reportAt].row
	}
	rows := make([]string, 0, body)
	lo, hi := -1, -2
	if m.sel.on {
		lo, hi = m.sel.span()
	}
	for i := m.reportScroll; i < min(len(r.rows), m.reportScroll+body); i++ {
		if i >= lo && i <= hi {
			plain := strings.ReplaceAll(ansi.Strip(r.rows[i]), "\u00a0", " ")
			rows = append(rows, brand.Render("▌ ")+selectedRow.Render(pad(plain, max(1, w-2))))
			continue
		}
		mark := "  "
		if i == sel {
			mark = brand.Bold(true).Render("› ")
		}
		rows = append(rows, mark+r.rows[i])
	}
	tail := " · report"
	if n := len(r.refs); n > 0 {
		tail += fmt.Sprintf(" · %d references", n)
	}
	if r.note != "" && len(r.rows) > 1 {
		tail += " · " + r.note
	}
	room := max(4, w-ansi.StringWidth(tail)-2)
	return window(brand.Render(keepTail(safeText(ui.Tilde(r.file)), room))+muted.Render(tail), rows, 0, w, h)
}

// reportKey handles a key while the report has the focus.
func (m *dashModel) reportKey(k string) (tea.Cmd, bool) {
	r := m.report
	page := max(1, m.lay.body-4)
	switch k {
	case "esc":
		if m.sel.on {
			m.sel = reportSelection{}
			return nil, true
		}
		m.report, m.focus = nil, 0
	case "up", "down":
		delta := 1
		if k == "up" {
			delta = -1
		}
		if !m.sel.on {
			m.reportScroll += delta
			return nil, true
		}
		m.sel.end = min(max(0, m.sel.end+delta), max(0, len(r.rows)-1))
		if m.sel.end < m.reportScroll || m.sel.end >= m.reportScroll+page {
			m.reportScroll = max(0, m.reportScroll+delta)
		}
	case "v":
		if m.sel.on {
			m.sel = reportSelection{}
			return nil, true
		}
		row := m.reportScroll
		if m.reportAt >= 0 && m.reportAt < len(r.refs) && r.refs[m.reportAt].row >= m.reportScroll {
			row = r.refs[m.reportAt].row
		}
		m.sel = reportSelection{on: true, anchor: row, end: row}
	case "y":
		switch {
		case m.sel.on:
			return m.copySelection(), true
		case m.reportAt >= 0 && m.reportAt < len(r.refs) && !r.refs[m.reportAt].image:
			label := r.refs[m.reportAt].label
			return m.copyText(label, label), true
		}
		m.say("v or a drag selects rows to copy; Y copies the whole report")
	case "Y":
		if r.source == "" {
			return nil, true
		}
		return m.copyText(r.source, "the whole report"), true
	case "pgup":
		m.reportScroll -= page
	case "pgdown", " ", "space":
		m.reportScroll += page
	case "n", "p", "]", "[":
		if len(r.refs) == 0 {
			m.say("This report points at no file:line and no local image")
			return nil, true
		}
		delta := 1
		if k == "p" || k == "[" {
			delta = -1
		}
		switch {
		case m.reportAt < 0 && delta < 0:
			m.reportAt = len(r.refs) - 1
		case m.reportAt < 0:
			m.reportAt = 0
		default:
			m.reportAt = (m.reportAt + delta + len(r.refs)) % len(r.refs)
		}
		if row := r.refs[m.reportAt].row; row >= 0 && (row < m.reportScroll || row >= m.reportScroll+page) {
			m.reportScroll = max(0, row-page/3)
		}
	case "enter":
		if m.reportAt < 0 || m.reportAt >= len(r.refs) {
			m.say("n p pick a file:line or an image first")
			return nil, true
		}
		ref := r.refs[m.reportAt]
		if ref.image {
			return m.openExternal(ref.path, ref.label), true
		}
		return m.openRef(ref), true
	case "o":
		if m.reportAt >= 0 && m.reportAt < len(r.refs) && r.refs[m.reportAt].image {
			ref := r.refs[m.reportAt]
			return m.openExternal(ref.path, ref.label), true
		}
		return m.openExternal(r.file, filepath.Base(r.file)), true
	case "s":
		if t := m.task(); t.ID == r.task && m.endedRun(t) {
			m.openFollowUp(t)
		}
	default:
		return nil, false
	}
	return nil, true
}

// openRef opens the file a reference names, at its line, from the task's file tree.
func (m *dashModel) openRef(ref reportRef) tea.Cmd {
	t := m.fileTree()
	path := t.resolve(ref.path)
	if path == "" || !t.reveal(path) {
		where := "changes"
		if m.showFiles {
			where = "files"
		}
		m.fail(fmt.Errorf("%s is not among this task's %s; f lists every file", ref.path, where))
		return nil
	}
	m.preview, m.patch, m.previewScroll, m.focus, m.details = true, false, 0, 2, false
	cmd := m.openFile()
	m.previewLine, m.previewLineFor = ref.line, m.previewRequest
	return cmd
}

type externalEvent struct {
	label string
	err   error
}

// openExternal hands path to the host's viewer: open on macOS, xdg-open on Linux.
func (m *dashModel) openExternal(path, label string) tea.Cmd {
	opener := m.opener
	if opener == nil {
		opener = osOpen
	}
	return func() tea.Msg { return externalEvent{label: label, err: opener(path)} }
}

func osOpen(path string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	cmd := exec.Command(name, path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
