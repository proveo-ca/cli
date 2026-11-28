// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	udiff "github.com/aymanbagabas/go-udiff"
	"github.com/charmbracelet/x/ansi"
)

// codeTone is the SGR around one token type's text.
type codeTone struct{ pre, post string }

// defaultCodeTheme is the chroma style code takes unless PROVEO_CODE_THEME names another.
const defaultCodeTheme = "catppuccin-mocha"

var (
	codeTonesMu sync.Mutex
	codeStyle   *chroma.Style
	codeTones   map[chroma.TokenType]codeTone
)

// codeThemeName is the chroma style PROVEO_CODE_THEME names, or the default.
func codeThemeName() string {
	if name := strings.TrimSpace(os.Getenv("PROVEO_CODE_THEME")); name != "" {
		return name
	}
	return defaultCodeTheme
}

// toneOf is the theme's foreground, bold, italic and underline for t; the pane keeps its own background.
// Code is the one place colour leaves the identity palette.
func toneOf(t chroma.TokenType) (codeTone, bool) {
	codeTonesMu.Lock()
	defer codeTonesMu.Unlock()
	if codeStyle == nil {
		codeStyle, codeTones = styles.Get(codeThemeName()), map[chroma.TokenType]codeTone{}
	}
	if tone, ok := codeTones[t]; ok {
		return tone, tone.pre != ""
	}
	e, s, styled := codeStyle.Get(t), lipgloss.NewStyle(), false
	if e.Colour.IsSet() {
		s, styled = s.Foreground(lipgloss.Color(e.Colour.String())), true
	}
	if e.Bold == chroma.Yes {
		s, styled = s.Bold(true), true
	}
	if e.Italic == chroma.Yes {
		s, styled = s.Italic(true), true
	}
	if e.Underline == chroma.Yes {
		s, styled = s.Underline(true), true
	}
	var tone codeTone
	if styled {
		tone.pre, tone.post, _ = strings.Cut(s.Render("\uE000"), "\uE000")
	}
	codeTones[t] = tone
	return tone, styled
}

// highlight colours text as path's language, the whole file at once, and returns its lines.
func highlight(path, text string) []string {
	if text == "" {
		return nil
	}
	lexer := lexers.Match(path)
	if lexer == nil {
		lexer = lexers.Analyse(text)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, text)
	if err != nil {
		return plainLines(text)
	}
	var lines []string
	var line strings.Builder
	for tok := it(); tok != chroma.EOF; tok = it() {
		tone, styled := toneOf(tok.Type)
		for i, part := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				lines = append(lines, line.String())
				line.Reset()
			}
			if part = cleanCode(part); part == "" {
				continue
			}
			if styled {
				line.WriteString(tone.pre + part + tone.post)
			} else {
				line.WriteString(part)
			}
		}
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return lines
}

func plainLines(text string) []string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for i := range lines {
		lines[i] = cleanCode(lines[i])
	}
	return lines
}

// cleanCode drops control bytes and escapes from source text and expands its tabs.
func cleanCode(s string) string {
	return strings.ReplaceAll(safeMultiline(strings.TrimSuffix(s, "\r")), "\t", "    ")
}

// diffRow is one line of the diff: ' ' kept, '-' removed, '+' added, '~' the gap between hunks.
type diffRow struct {
	op       byte
	old, new int // 1-based line numbers; 0 when that side has no line
	text     string
}

// splitRow is a before | after pair; an op of 0 leaves that half empty.
type splitRow struct{ left, right diffRow }

// diffRows lines the two coloured files up, three lines of context around each change.
func diffRows(before, after string, hb, ha []string) []diffRow {
	u, err := udiff.ToUnifiedDiff("", "", before, udiff.Lines(before, after), 3)
	if err != nil {
		return nil
	}
	var rows []diffRow
	shift := 0
	for i, h := range u.Hunks {
		if i > 0 {
			rows = append(rows, diffRow{op: '~'})
		}
		o, n := h.FromLine, h.FromLine+shift
		for _, l := range h.Lines {
			switch l.Kind {
			case udiff.Equal:
				rows = append(rows, diffRow{' ', o, n, lineAt(ha, n, l.Content)})
				o, n = o+1, n+1
			case udiff.Delete:
				rows = append(rows, diffRow{'-', o, 0, lineAt(hb, o, l.Content)})
				o, shift = o+1, shift-1
			case udiff.Insert:
				rows = append(rows, diffRow{'+', 0, n, lineAt(ha, n, l.Content)})
				n, shift = n+1, shift+1
			}
		}
	}
	return rows
}

func lineAt(lines []string, n int, fallback string) string {
	if n >= 1 && n <= len(lines) {
		return lines[n-1]
	}
	return cleanCode(strings.TrimSuffix(fallback, "\n"))
}

// pairRows sets removed lines beside the lines that replaced them.
func pairRows(rows []diffRow) []splitRow {
	var out []splitRow
	for i := 0; i < len(rows); {
		if op := rows[i].op; op != '-' && op != '+' {
			out = append(out, splitRow{rows[i], rows[i]})
			i++
			continue
		}
		var dels, adds []diffRow
		for i < len(rows) && rows[i].op == '-' {
			dels, i = append(dels, rows[i]), i+1
		}
		for i < len(rows) && rows[i].op == '+' {
			adds, i = append(adds, rows[i]), i+1
		}
		for k := range max(len(dels), len(adds)) {
			var p splitRow
			if k < len(dels) {
				p.left = dels[k]
			}
			if k < len(adds) {
				p.right = adds[k]
			}
			out = append(out, p)
		}
	}
	return out
}

// previewDoc is one opened file, coloured whole on both sides, with its diff against the merge base.
type previewDoc struct {
	path, note    string
	before, after []string
	rows          []diffRow
	pairs         []splitRow
	deleted       bool
	truncated     bool
	numW          int
	mark          int      // the line a report reference points at; 0 marks none
	raw           string   // a diagram's source, for its sketch and its SVG
	sketch        []string // the diagram as plantuml -tutxt draws it; nil until it lands
	sketchNote    string   // why no sketch could be drawn
}

func newPreviewDoc(path string, s Sides) previewDoc {
	d := previewDoc{path: path, truncated: s.Truncated}
	if strings.ContainsRune(s.Before, 0) || strings.ContainsRune(s.After, 0) {
		d.note = "binary file"
		return d
	}
	d.before, d.after = highlight(path, s.Before), highlight(path, s.After)
	d.deleted = s.After == "" && s.Before != ""
	if isDiagram(path) && !d.deleted {
		d.raw = s.After
	}
	if s.Before != s.After {
		d.rows = diffRows(s.Before, s.After, d.before, d.after)
		d.pairs = pairRows(d.rows)
	}
	d.numW = len(strconv.Itoa(max(1, len(d.before), len(d.after))))
	return d
}

// previewMode is what the preview pane shows of the open file.
type previewMode int

const (
	previewFile previewMode = iota
	previewUnified
	previewSplit
	previewSketch
)

// size is how many rows mode draws.
func (d previewDoc) size(mode previewMode) int {
	switch {
	case d.note != "":
		return 1
	case mode == previewSketch:
		return max(1, len(d.sketch))
	case mode == previewFile && d.deleted:
		return len(d.before)
	case mode == previewFile:
		return max(1, len(d.after))
	case len(d.rows) == 0:
		return 1
	case mode == previewSplit:
		return len(d.pairs)
	}
	return len(d.rows)
}

// lines draws rows [from, from+n) of mode w cells wide; gap marks the space between hunks.
func (d previewDoc) lines(mode previewMode, from, n, w int, gap, base string) []string {
	if d.note != "" {
		return []string{muted.Render(d.note)}
	}
	var out []string
	switch {
	case mode == previewSketch:
		return d.sketchLines(from, n, w)
	case mode == previewFile:
		src := d.after
		if d.deleted {
			src = d.before
		}
		if len(src) == 0 {
			return []string{muted.Render("empty file")}
		}
		for i := from; i < min(len(src), from+n); i++ {
			num := muted.Render(fmt.Sprintf("%*d ", d.numW, i+1))
			if i+1 == d.mark {
				num = brand.Bold(true).Render(fmt.Sprintf("%*d›", d.numW, i+1))
			}
			out = append(out, num+ansi.Truncate(src[i], max(1, w-d.numW-1), "…"))
		}
	case len(d.rows) == 0:
		return []string{muted.Render("no changes " + base)}
	case mode == previewSplit:
		col := max(1, (w-1)/2)
		for _, p := range d.pairs[min(from, len(d.pairs)):min(len(d.pairs), from+n)] {
			if p.left.op == '~' {
				out = append(out, muted.Render(strings.Repeat(gap, max(1, w/ansi.StringWidth(gap)))))
				continue
			}
			out = append(out, pad(d.cell(p.left, p.left.old, col), col)+ruleText.Render("│")+d.cell(p.right, p.right.new, w-col-1))
		}
	default:
		for _, r := range d.rows[min(from, len(d.rows)):min(len(d.rows), from+n)] {
			if r.op == '~' {
				out = append(out, muted.Render(strings.Repeat(gap, max(1, w/ansi.StringWidth(gap)))))
				continue
			}
			num := r.new
			if r.op == '-' {
				num = r.old
			}
			out = append(out, d.cell(r, num, w))
		}
	}
	return out
}

// cell is one diff line w cells wide: number, sign in its tone, coloured code.
func (d previewDoc) cell(r diffRow, num, w int) string {
	if r.op == 0 {
		return ""
	}
	tone, sign := muted, " "
	switch r.op {
	case '-':
		tone, sign = errorText, "-"
	case '+':
		tone, sign = successText, "+"
	}
	gutter := tone.Render(fmt.Sprintf("%*d %s ", d.numW, num, sign))
	return gutter + ansi.Truncate(r.text, max(1, w-d.numW-3), "…")
}
