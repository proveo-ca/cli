// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// treeNode is a folder or a changed file; a compacted chain joins folder names with "/".
type treeNode struct {
	name     string
	path     string
	dir      bool
	change   Change
	children []*treeNode
	counts   map[byte]int
	files    int
	nodes    int
}

// treeRow is one visible row of the tree.
type treeRow struct {
	node  *treeNode
	depth int
	open  bool
	hits  []int
}

// fileTree is the Changes or Files pane state.
type fileTree struct {
	root    *treeNode
	src     []Change
	key     string
	open    map[string]bool
	closed  bool
	decided bool
	flat    bool
	filter  string
	editing bool
	at      string
}

var statusRank = map[byte]int{'U': 90, 'D': 80, 'A': 60, 'C': 60, 'M': 50, 'T': 50, 'R': 40, '?': 30}

func changeLetter(kind string) byte {
	if k := strings.TrimSpace(kind); k != "" {
		return k[0]
	}
	return 0
}

func buildFileTree(changes []Change) *treeNode {
	root := &treeNode{dir: true}
	dirs := map[string]*treeNode{"": root}
	for _, c := range changes {
		p := strings.Trim(filepath.ToSlash(c.Path), "/")
		if p == "" {
			continue
		}
		parts := strings.Split(p, "/")
		parent := root
		for i := range parts[:len(parts)-1] {
			at := strings.Join(parts[:i+1], "/")
			d := dirs[at]
			if d == nil {
				d = &treeNode{name: parts[i], path: at, dir: true}
				dirs[at] = d
				parent.children = append(parent.children, d)
			}
			parent = d
		}
		parent.children = append(parent.children, &treeNode{name: parts[len(parts)-1], path: p, change: c})
	}
	tally(root)
	compact(root)
	return root
}

func tally(n *treeNode) {
	n.counts = map[byte]int{}
	n.files, n.nodes = 0, 0
	slices.SortFunc(n.children, func(a, b *treeNode) int {
		if a.dir != b.dir {
			if a.dir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
	})
	for _, c := range n.children {
		n.nodes++
		if !c.dir {
			n.files++
			if k := changeLetter(c.change.Kind); k != 0 {
				n.counts[k]++
			}
			continue
		}
		tally(c)
		n.files += c.files
		n.nodes += c.nodes
		for k, v := range c.counts {
			n.counts[k] += v
		}
	}
}

func compact(n *treeNode) {
	for i, c := range n.children {
		for c.dir && len(c.children) == 1 && c.children[0].dir {
			only := c.children[0]
			only.name = c.name + "/" + only.name
			c = only
		}
		n.children[i] = c
		if c.dir {
			compact(c)
		}
	}
	n.nodes = 0
	for _, c := range n.children {
		n.nodes++
		if c.dir {
			n.nodes += c.nodes
		}
	}
}

// strongest is the highest-priority status below n.
func (n *treeNode) strongest() byte {
	var best byte
	for k, v := range n.counts {
		if v > 0 && statusRank[k] > statusRank[best] {
			best = k
		}
	}
	return best
}

func (t *fileTree) isOpen(n *treeNode) bool {
	if v, ok := t.open[n.path]; ok {
		return v
	}
	return !t.closed
}

// rows projects the tree onto visible rows; a filter keeps every ancestor of a match.
func (t *fileTree) rows() []treeRow {
	if t.root == nil {
		return nil
	}
	terms := strings.Fields(strings.ToLower(t.filter))
	var out []treeRow
	if t.flat {
		var walk func(n *treeNode)
		walk = func(n *treeNode) {
			for _, c := range n.children {
				if c.dir {
					walk(c)
				} else if hits, ok := matchFile(c.path, c.path, terms); ok {
					out = append(out, treeRow{node: c, hits: hits})
				}
			}
		}
		walk(t.root)
		return out
	}
	var visit func(n *treeNode, depth int)
	visit = func(n *treeNode, depth int) {
		for _, c := range n.children {
			if !c.dir {
				if hits, ok := matchFile(c.path, c.name, terms); ok {
					out = append(out, treeRow{node: c, depth: depth, hits: hits})
				}
				continue
			}
			if len(terms) > 0 {
				mark := len(out)
				out = append(out, treeRow{node: c, depth: depth, open: true})
				visit(c, depth+1)
				if len(out) == mark+1 {
					out = out[:mark]
				}
				continue
			}
			open := t.isOpen(c)
			out = append(out, treeRow{node: c, depth: depth, open: open})
			if open {
				visit(c, depth+1)
			}
		}
	}
	visit(t.root, 0)
	return out
}

// matchFile reports whether every term matches; hits index the runes of shown.
func matchFile(full, shown string, terms []string) ([]int, bool) {
	low := strings.ToLower(full)
	name := path.Base(low)
	dir := strings.TrimSuffix(low, name)
	offset := utf8.RuneCountInString(strings.ToLower(shown)) - utf8.RuneCountInString(name)
	var hits []int
	for _, term := range terms {
		if strings.Contains(term, "/") {
			at, ok := subsequence(low, term)
			if !ok {
				return nil, false
			}
			lead := utf8.RuneCountInString(low) - utf8.RuneCountInString(strings.ToLower(shown))
			for _, i := range at {
				if i >= lead {
					hits = append(hits, i-lead)
				}
			}
			continue
		}
		if at, ok := subsequence(name, term); ok {
			for _, i := range at {
				hits = append(hits, i+offset)
			}
			continue
		}
		if !strings.Contains(dir, term) {
			return nil, false
		}
	}
	slices.Sort(hits)
	return slices.Compact(hits), true
}

// subsequence finds needle's runes in order inside hay, preferring a contiguous run.
func subsequence(hay, needle string) ([]int, bool) {
	h, n := []rune(hay), []rune(needle)
	if len(n) == 0 {
		return nil, true
	}
	if i := strings.Index(hay, needle); i >= 0 {
		start := utf8.RuneCountInString(hay[:i])
		at := make([]int, len(n))
		for k := range n {
			at[k] = start + k
		}
		return at, true
	}
	at := make([]int, 0, len(n))
	j := 0
	for i := 0; i < len(h) && j < len(n); i++ {
		if h[i] == n[j] {
			at = append(at, i)
			j++
		}
	}
	return at, j == len(n)
}

// index is the cursor's row; an absorbed or hidden path falls back to its nearest relative.
func (t *fileTree) index(rows []treeRow) int {
	for i, r := range rows {
		if r.node.path == t.at {
			return i
		}
	}
	if t.at != "" {
		for i, r := range rows {
			if strings.HasPrefix(t.at, r.node.path+"/") || strings.HasPrefix(r.node.path, t.at+"/") {
				return i
			}
		}
	}
	return 0
}

func (t *fileTree) selected() *treeNode {
	rows := t.rows()
	if len(rows) == 0 {
		return nil
	}
	return rows[t.index(rows)].node
}

func (t *fileTree) move(delta int) {
	rows := t.rows()
	if len(rows) == 0 {
		return
	}
	t.at = rows[max(0, min(t.index(rows)+delta, len(rows)-1))].node.path
}

// fold closes the selected folder, or steps to the folder that holds the selection.
func (t *fileTree) fold() {
	rows := t.rows()
	if len(rows) == 0 {
		return
	}
	i := t.index(rows)
	r := rows[i]
	if r.node.dir && r.open && t.filter == "" {
		t.open[r.node.path] = false
		t.at = r.node.path
		return
	}
	for j := i - 1; j >= 0; j-- {
		if rows[j].depth < r.depth && rows[j].node.dir {
			t.at = rows[j].node.path
			return
		}
	}
}

// unfold opens the selected folder, or steps into it when already open.
func (t *fileTree) unfold() {
	rows := t.rows()
	if len(rows) == 0 {
		return
	}
	i := t.index(rows)
	r := rows[i]
	if !r.node.dir {
		return
	}
	if !r.open {
		t.open[r.node.path] = true
		t.at = r.node.path
		return
	}
	if i+1 < len(rows) {
		t.at = rows[i+1].node.path
	}
}

func (t *fileTree) toggle(n *treeNode) {
	if n != nil && n.dir && t.filter == "" {
		t.open[n.path] = !t.isOpen(n)
		t.at = n.path
	}
}

func (t *fileTree) setAll(open bool) {
	t.open, t.closed, t.decided = map[string]bool{}, !open, true
}

// refilter keeps the cursor on a visible row after the filter changes.
func (t *fileTree) refilter() {
	rows := t.rows()
	for _, r := range rows {
		if r.node.path == t.at {
			return
		}
	}
	for _, r := range rows {
		if !r.node.dir {
			t.at = r.node.path
			return
		}
	}
	if len(rows) > 0 {
		t.at = rows[0].node.path
	}
}

// files are the filter's files in tree order, each with the folders that hold it.
func (t *fileTree) files() ([]*treeNode, map[*treeNode][]*treeNode) {
	if t == nil || t.root == nil {
		return nil, nil
	}
	terms := strings.Fields(strings.ToLower(t.filter))
	var files []*treeNode
	trail := map[*treeNode][]*treeNode{}
	var walk func(n *treeNode, dirs []*treeNode)
	walk = func(n *treeNode, dirs []*treeNode) {
		for _, c := range n.children {
			if c.dir {
				walk(c, append(slices.Clone(dirs), c))
			} else if _, ok := matchFile(c.path, c.name, terms); ok {
				files, trail[c] = append(files, c), dirs
			}
		}
	}
	walk(t.root, nil)
	return files, trail
}

// position is path's place among the files, from 1, and how many there are; 0 when it is not one.
func (t *fileTree) position(path string) (int, int) {
	files, _ := t.files()
	for i, f := range files {
		if f.path == path {
			return i + 1, len(files)
		}
	}
	return 0, len(files)
}

// resolve is the file a report names: the exact path, else the first file whose path ends with it.
func (t *fileTree) resolve(ref string) string {
	ref = strings.TrimPrefix(ref, "./")
	files, _ := t.files()
	suffix := ""
	for _, f := range files {
		switch {
		case f.path == ref:
			return f.path
		case suffix == "" && strings.HasSuffix(f.path, "/"+ref):
			suffix = f.path
		}
	}
	return suffix
}

// reveal moves the cursor to the file at path, opening the folders on the way.
func (t *fileTree) reveal(path string) bool {
	files, trail := t.files()
	for _, f := range files {
		if f.path != path {
			continue
		}
		if t.open == nil {
			t.open = map[string]bool{}
		}
		for _, d := range trail[f] {
			t.open[d.path] = true
		}
		t.at = f.path
		return true
	}
	return false
}

// step moves the cursor to the file delta files away in tree order, opening the folders on the way.
func (t *fileTree) step(delta int) *treeNode {
	files, trail := t.files()
	next := -1
	for i, f := range files {
		if f.path == t.at {
			next = i + delta
			break
		}
		if next < 0 && strings.HasPrefix(f.path, t.at+"/") {
			next = i + min(0, delta)
		}
	}
	if next < 0 && delta > 0 && t.at == "" {
		next = 0
	}
	if next < 0 || next >= len(files) {
		return nil
	}
	f := files[next]
	if t.open == nil {
		t.open = map[string]bool{}
	}
	for _, d := range trail[f] {
		t.open[d.path] = true
	}
	t.at = f.path
	return f
}
