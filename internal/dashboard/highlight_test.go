// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

func TestHighlightColoursTheWholeFileSoACommentBodyStaysAComment(t *testing.T) {
	src := "package main\n\n/*\nreturn \"not code\"\n*/\nfunc main() { _ = \"hi\" }\n"
	lines := highlight("main.go", src)
	if len(lines) != 6 {
		t.Fatalf("%d lines, want 6: %q", len(lines), lines)
	}
	comment, _ := toneOf(chroma.CommentMultiline)
	if !strings.HasPrefix(lines[3], comment.pre) {
		t.Errorf("the line inside /* */ lost the comment tone: %q", lines[3])
	}
	str, _ := toneOf(chroma.LiteralString)
	if !strings.Contains(lines[5], str.pre+`"hi"`) {
		t.Errorf("a string outside the comment is not a string: %q", lines[5])
	}
	if ansi.Strip(lines[3]) != `return "not code"` {
		t.Errorf("the text changed: %q", ansi.Strip(lines[3]))
	}
	if got := highlight("evil.txt", "a\x1b]8;;x\x07\tb\r\n"); ansi.Strip(got[0]) != "a    b" {
		t.Errorf("escapes or tabs survived: %q", got)
	}
}

func TestDiffRowsNumberBothSidesAndPairEdits(t *testing.T) {
	before, after := "a\nb\nc\n", "a\nB\nc\nd\n"
	rows := diffRows(before, after, plainLines(before), plainLines(after))
	want := []diffRow{{' ', 1, 1, "a"}, {'-', 2, 0, "b"}, {'+', 0, 2, "B"}, {' ', 3, 3, "c"}, {'+', 0, 4, "d"}}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v", rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
	pairs := pairRows(rows)
	if len(pairs) != 4 || pairs[1].left.text != "b" || pairs[1].right.text != "B" || pairs[3].left.op != 0 || pairs[3].right.text != "d" {
		t.Errorf("pairs = %+v", pairs)
	}
}

func TestDiffRowsKeepNewNumbersAcrossHunks(t *testing.T) {
	var b, a strings.Builder
	for i := 1; i <= 30; i++ {
		line := "line " + strings.Repeat("x", i%3) + "\n"
		b.WriteString(line)
		if i == 2 {
			a.WriteString("inserted\n")
		}
		if i == 25 {
			line = "changed\n"
		}
		a.WriteString(line)
	}
	rows := diffRows(b.String(), a.String(), nil, nil)
	gaps, changed := 0, diffRow{}
	for _, r := range rows {
		if r.op == '~' {
			gaps++
		}
		if r.op == '+' && r.text == "changed" {
			changed = r
		}
	}
	if gaps != 1 || changed.new != 26 {
		t.Errorf("gaps %d, changed at new line %d (want 1, 26): %+v", gaps, changed.new, rows)
	}
	added := diffRows("", "x\ny\n", nil, nil)
	if len(added) != 2 || added[0].new != 1 || added[1].new != 2 || added[0].op != '+' {
		t.Errorf("a new file's rows = %+v", added)
	}
}

func TestPreviewDocDrawsEveryModeWithinItsWidth(t *testing.T) {
	d := newPreviewDoc("run.go", Sides{Before: "package run\n\nfunc A() int { return 1 }\n", After: "package run\n\nfunc A() int { return 2 }\n"})
	for _, mode := range []previewMode{previewFile, previewUnified, previewSplit} {
		lines := d.lines(mode, 0, 10, 40, "┄", "vs origin/dev")
		if len(lines) == 0 {
			t.Fatalf("mode %d drew nothing", mode)
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > 40 {
				t.Errorf("mode %d line is %d cells: %q", mode, ansi.StringWidth(l), ansi.Strip(l))
			}
		}
	}
	split := ansi.Strip(strings.Join(d.lines(previewSplit, 0, 10, 80, "┄", ""), "\n"))
	if !strings.Contains(split, "3 - func A() int { return 1 }") || !strings.Contains(split, "│3 + func A() int { return 2 }") {
		t.Errorf("side by side lacks both halves:\n%s", split)
	}
	same := newPreviewDoc("a.go", Sides{Before: "x\n", After: "x\n"})
	if got := ansi.Strip(same.lines(previewUnified, 0, 5, 40, "┄", "vs origin/dev")[0]); got != "no changes vs origin/dev" {
		t.Errorf("unchanged diff = %q", got)
	}
	gone := newPreviewDoc("old.go", Sides{Before: "package old\n"})
	if !gone.deleted || !strings.Contains(ansi.Strip(gone.lines(previewFile, 0, 5, 40, "┄", "")[0]), "package old") {
		t.Errorf("a deleted file shows its base copy: %+v", gone)
	}
	if bin := newPreviewDoc("x.png", Sides{After: "\x89PNG\x00\x01"}); bin.note != "binary file" {
		t.Errorf("binary note = %q", bin.note)
	}
}

func TestParseSidesReadsByLengthNotByMarker(t *testing.T) {
	s, err := parseSides([]byte("before 9\nafter 3\nxafter 2\nok"))
	if err != nil || s.Before != "after 3\nx" || s.After != "ok" {
		t.Errorf("sides = %+v, %v", s, err)
	}
	if _, err := parseSides([]byte("before 99\nshort")); err == nil {
		t.Error("a short body parsed")
	}
	calls := fakeGuest(t, map[string]string{"side before": "before 2\nb\nafter 2\na\n"})
	got, err := ReadSides(Task{Workdir: "/w"}, Change{Kind: "R100", Path: "new.go", OldPath: "old.go"}, "mb1")
	if err != nil || got.Before != "b\n" || got.After != "a\n" {
		t.Fatalf("guest sides = %+v, %v", got, err)
	}
	if args := (*calls)[0]; strings.Join(args, " ") != "/w new.go mb1 old.go" {
		t.Errorf("guest args = %q; the base side reads the old name", args)
	}
	if _, err := ReadSides(Task{Workdir: "/w"}, Change{Path: "../etc/passwd"}, "mb1"); err == nil {
		t.Error("a path outside the workspace was read")
	}
}

func TestPreviewKeysSwitchModesAndStepFiles(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	m.focus = 1
	tree := m.fileTree()
	tree.at = "go.mod"
	m.preview, m.focus, m.previewPath = true, 2, "go.mod"
	m.previewDoc = newPreviewDoc("go.mod", Sides{Before: "module a\n", After: "module b\n"})
	press := func(k string) tea.Cmd {
		_, cmd := m.Update(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
		return cmd
	}
	if cmd := press("d"); cmd != nil || m.previewMode() != previewUnified {
		t.Errorf("d: mode %d, refetch %v", m.previewMode(), cmd != nil)
	}
	press("i")
	if m.previewMode() != previewSplit || !strings.Contains(ansi.Strip(m.footer(120)), "u unified") {
		t.Errorf("i: mode %d, footer %q", m.previewMode(), ansi.Strip(m.footer(120)))
	}
	press("u")
	if m.previewMode() != previewUnified {
		t.Errorf("u: mode %d", m.previewMode())
	}
	tree.setAll(false)
	if cmd := press("p"); cmd == nil || m.previewPath == "go.mod" {
		t.Fatalf("p stayed on %q", m.previewPath)
	}
	if n := tree.selected(); n == nil || n.path != m.previewPath {
		t.Errorf("the tree cursor did not follow to %q", m.previewPath)
	}
	if !m.patch || m.previewMode() != previewUnified {
		t.Error("stepping files dropped the diff mode")
	}
	at, total := tree.position(m.previewPath)
	if at != total-1 || !strings.Contains(ansi.Strip(m.previewTitle(120)), "· 7 of 8") {
		t.Errorf("position %d of %d, title %q", at, total, ansi.Strip(m.previewTitle(120)))
	}
	press("n")
	if m.previewPath != "go.mod" {
		t.Errorf("n went to %q", m.previewPath)
	}
	if cmd := press("n"); cmd != nil {
		t.Error("n past the last file opened something")
	}
	if foot := ansi.Strip(m.footer(140)); !strings.Contains(foot, "n p next / previous file") || !strings.Contains(foot, "d file") || strings.Contains(foot, "new instance") {
		t.Errorf("footer while reading: %q", foot)
	}
	var ran []string
	m.runner = func(_ string, args ...string) tea.Cmd { ran = args; return nil }
	m.preview, m.focus = false, 0
	press("n")
	if m.taskPicker == nil || len(ran) != 0 {
		t.Errorf("n away from a file ran %q; it should open the new-instance picker", ran)
	}
}

func TestCodeTakesItsSyntaxThemeNotTheIdentityPalette(t *testing.T) {
	reset := func(name string) {
		t.Setenv("PROVEO_CODE_THEME", name)
		codeTonesMu.Lock()
		codeStyle = nil
		codeTonesMu.Unlock()
	}
	t.Cleanup(func() { reset("") })
	rgb := func(theme string, tt chroma.TokenType) string {
		c := styles.Get(theme).Get(tt).Colour
		return fmt.Sprintf("38;2;%d;%d;%d", c.Red(), c.Green(), c.Blue())
	}
	reset("")
	if kw, _ := toneOf(chroma.Keyword); !strings.Contains(kw.pre, rgb(defaultCodeTheme, chroma.Keyword)) {
		t.Errorf("keyword tone %q lacks %s's colour %s", kw.pre, defaultCodeTheme, rgb(defaultCodeTheme, chroma.Keyword))
	}
	reset("dracula")
	if kw, _ := toneOf(chroma.Keyword); !strings.Contains(kw.pre, rgb("dracula", chroma.Keyword)) {
		t.Errorf("PROVEO_CODE_THEME=dracula gave %q", kw.pre)
	}
	if hasBackground(strings.Join(highlight("main.go", "package main\n\nfunc main() {}\n"), "\n")) {
		t.Error("the theme painted a background over the pane")
	}
}

// hasBackground reports an SGR background (40-47, 48;..., 100-107) in s, skipping 38;2;r;g;b and 38;5;n operands.
func hasBackground(s string) bool {
	for _, seq := range regexp.MustCompile("\x1b\\[([0-9;]*)m").FindAllStringSubmatch(s, -1) {
		params := strings.Split(seq[1], ";")
		for i := 0; i < len(params); i++ {
			n, _ := strconv.Atoi(params[i])
			switch {
			case n == 38 && i+1 < len(params) && params[i+1] == "2":
				i += 4
			case n == 38 && i+1 < len(params) && params[i+1] == "5":
				i += 2
			case n == 48 || (n >= 40 && n <= 47) || (n >= 100 && n <= 107):
				return true
			}
		}
	}
	return false
}

func TestTabThenNOpensTheFirstFileAndTheTreeUnderlinesIt(t *testing.T) {
	m := sampleModel(140, 40, ui.GlyphsSafe)
	m.focus = 0
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focus != 1 {
		t.Fatalf("tab went to %d", m.focus)
	}
	files, _ := m.fileTree().files()
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if cmd == nil || !m.preview || m.previewPath != files[0].path || m.focus != 2 {
		t.Fatalf("n from the tree opened %q (preview %v, focus %d), want the first file %q", m.previewPath, m.preview, m.focus, files[0].path)
	}
	var row string
	for _, line := range m.renderFiles(60, 30) {
		if strings.Contains(ansi.Strip(line), path.Base(m.previewPath)) {
			row = line
		}
	}
	if !strings.HasPrefix(ansi.Strip(row), "› ") {
		t.Errorf("the file being read lacks the › marker while the file has focus: %q", ansi.Strip(row))
	}
	if !regexp.MustCompile(`\x1b\[(?:[0-9;]*;)?4;[0-9;]*58;2;0;186;198`).MatchString(row) {
		t.Errorf("the file being read is not underlined in brand cyan: %q", row)
	}
	m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.previewPath != files[1].path {
		t.Errorf("n while reading went to %q, want %q", m.previewPath, files[1].path)
	}
	m.focus = 0
	var ran []string
	m.runner = func(_ string, args ...string) tea.Cmd { ran = args; return nil }
	m.preview = false
	m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.taskPicker == nil || len(ran) != 0 {
		t.Errorf("n on the task cards ran %q; it should open the new-instance picker", ran)
	}
}
