// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

func rowNames(rows []treeRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, strings.Repeat("  ", r.depth)+r.node.name)
	}
	return out
}

func newTree(changes []Change) *fileTree {
	return &fileTree{root: buildFileTree(changes), src: changes, open: map[string]bool{}}
}

func TestCompactsSingleChildFolders(t *testing.T) {
	got := rowNames(newTree(sampleChanges).rows())
	want := []string{
		"_spec/_experiments/dashboard", "  workspace-instances.puml",
		"internal", "  backend/sandbox", "    checkpoint.go", "    checkpoint_test.go",
		"    session_by_pwd.go", "    teardown.go", "  run", "    options.go", "    run.go",
		"go.mod",
	}
	if !slices.Equal(got, want) {
		t.Errorf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCursorSurvivesAnAbsorbedFolder(t *testing.T) {
	tree := newTree([]Change{{Kind: "A", Path: "chain/a/b/c/x.go"}, {Kind: "A", Path: "chain/a/y.go"}})
	tree.at = "chain/a/b/c"
	if n := tree.selected(); n == nil || n.path != "chain/a/b/c" {
		t.Fatalf("selected %v before the rebuild", n)
	}
	tree.at = "chain/a"
	tree.root = buildFileTree([]Change{{Kind: "A", Path: "chain/a/b/c/x.go"}})
	if n := tree.selected(); n == nil || n.path != "chain/a/b/c" || n.name != "chain/a/b/c" {
		t.Fatalf("cursor on an absorbed folder fell to %+v", n)
	}
}

func TestFoldDefaultOpensOnlyWhatFits(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	m.renderFiles(40, 30)
	if m.tree.closed {
		t.Error("twelve rows fit thirty, so the tree should open")
	}
	m = sampleModel(120, 40, ui.GlyphsSafe)
	m.renderFiles(40, 6)
	if !m.tree.closed {
		t.Error("twelve rows overflow six, so the tree should start folded")
	}
}

func TestFoldedFolderRollsUpItsChanges(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	tree := m.fileTree()
	tree.at = "internal"
	tree.fold()
	var line string
	for _, r := range tree.rows() {
		if r.node.path == "internal" {
			line = ansi.Strip(m.treeLine(r, false, false, 0))
		}
	}
	if !strings.Contains(line, "▸ internal  +2 ~2 -1 →1") {
		t.Errorf("folded row %q", line)
	}
	if got := tree.root.children[1].strongest(); got != 'D' {
		t.Errorf("strongest status %q, want D", got)
	}
	tree.unfold()
	if !tree.isOpen(tree.root.children[1]) {
		t.Error("right should reopen the folder")
	}
}

func TestFilterKeepsAncestorsAndMarksHits(t *testing.T) {
	tree := newTree(sampleChanges)
	for _, c := range []struct {
		filter string
		want   []string
	}{
		{"chk", []string{"internal", "  backend/sandbox", "    checkpoint.go", "    checkpoint_test.go"}},
		{"sandbox", []string{"internal", "  backend/sandbox", "    checkpoint.go", "    checkpoint_test.go", "    session_by_pwd.go", "    teardown.go"}},
		{"run/opt", []string{"internal", "  run", "    options.go"}},
		{"zzz", nil},
	} {
		tree.filter = c.filter
		if got := rowNames(tree.rows()); !slices.Equal(got, c.want) {
			t.Errorf("filter %q:\n%s", c.filter, strings.Join(got, "\n"))
		}
	}
	tree.filter = "chk"
	rows := tree.rows()
	if !slices.Equal(rows[2].hits, []int{0, 1, 4}) {
		t.Errorf("hits %v, want [0 1 4]", rows[2].hits)
	}
}

func TestRenameShowsTheOldName(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	for _, r := range m.fileTree().rows() {
		if r.node.path == "internal/run/options.go" {
			line := ansi.Strip(m.treeLine(r, false, false, 0))
			if !strings.Contains(line, "R options.go ← params.go") || strings.Contains(line, "R100") {
				t.Errorf("rename row %q", line)
			}
			return
		}
	}
	t.Fatal("no rename row")
}

func TestFlatModeListsPaths(t *testing.T) {
	tree := newTree(sampleChanges)
	tree.flat = true
	got := rowNames(tree.rows())
	if len(got) != len(sampleChanges) || got[0] != "workspace-instances.puml" {
		t.Errorf("flat rows %v", got)
	}
	m := sampleModel(120, 40, ui.GlyphsSafe)
	if line := ansi.Strip(m.treeLine(tree.rows()[1], false, true, 0)); !strings.Contains(line, "internal/backend/sandbox/checkpoint.go") {
		t.Errorf("flat row %q lacks its path", line)
	}
}

func TestEnterTogglesAFolderAndOpensAFile(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	m.focus = 1
	tree := m.fileTree()
	tree.at = "internal"
	m.enter()
	if tree.isOpen(tree.root.children[1]) || m.preview {
		t.Fatal("Enter on a folder should fold it without a preview")
	}
	tree.at = "go.mod"
	if m.enter() == nil || !m.preview || m.previewPath != "go.mod" {
		t.Fatal("Enter on a file should open its preview")
	}
}

func TestNarrowFoldedRowKeepsItsCounts(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	tree := m.fileTree()
	tree.setAll(false)
	tree.at = "internal"
	tree.unfold()
	for _, r := range tree.rows() {
		if r.node.path == "internal/backend/sandbox" {
			line := ansi.Strip(m.treeLine(r, true, false, 26))
			if !strings.HasSuffix(line, "+2 ~1 -1") || !strings.Contains(line, "…") || ansi.StringWidth(line) > 26 {
				t.Errorf("narrow folded row %q", line)
			}
			return
		}
	}
	t.Fatal("no folded backend/sandbox row")
}
