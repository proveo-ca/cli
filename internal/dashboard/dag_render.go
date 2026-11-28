// SPEC: _spec/internal/dashboard/task-graph.puml
package dashboard

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

// graphMinCol is the narrowest a layer may be before a component falls back to one line per task.
const graphMinCol, graphMaxCol = 14, 36

// renderGraph draws view 2: each connected part of the task graph in layers, left to right, then the tasks
// nothing links under No dependencies. order is the tasks as drawn, for ↑↓; anchor is the selected task's row.
func (m *dashModel) renderGraph(w int) (rows []string, order []int, anchor int) {
	g := m.taskDAG()
	n := len(g.nodes)
	adj, radj := make([][]int, n), make([][]int, n)
	for _, e := range g.edges {
		adj[e.from] = append(adj[e.from], e.to)
		radj[e.to] = append(radj[e.to], e.from)
	}
	comp := make([]int, n)
	for i := range comp {
		comp[i] = -1
	}
	var comps [][]int
	for i := range n {
		if comp[i] >= 0 || len(adj[i])+len(radj[i]) == 0 {
			continue
		}
		c, stack := len(comps), []int{i}
		comp[i] = c
		var members []int
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			members = append(members, v)
			for _, u := range append(slices.Clone(adj[v]), radj[v]...) {
				if comp[u] < 0 {
					comp[u] = c
					stack = append(stack, u)
				}
			}
		}
		slices.Sort(members)
		comps = append(comps, members)
	}
	list := newTaskGraph(m.data.Tasks)
	line := func(i int) string {
		st := m.statusOf(m.data.Tasks[i])
		mark := glyph(m.glyphs, st.mark)
		if mark != "" {
			mark = st.style.Render(mark) + " "
		}
		return m.taskMarker(i) + mark + m.graphLine(list, m.data.Tasks[i], w-2-ansi.StringWidth(mark))
	}
	anchor = -1
	for _, members := range comps {
		block, drawn, at, ok := m.layoutComponent(g, adj, members, w)
		if !ok {
			block, drawn, at = nil, nil, -1
			for _, v := range members {
				if g.nodes[v].task >= 0 {
					if v == m.selected {
						at = len(block)
					}
					block, drawn = append(block, line(v)), append(drawn, v)
				}
			}
		}
		if at >= 0 {
			anchor = len(rows) + at
		}
		rows, order = append(append(rows, block...), ""), append(order, drawn...)
	}
	var lone []int
	for i := range m.data.Tasks {
		if comp[i] < 0 {
			lone = append(lone, i)
		}
	}
	if len(lone) > 0 && len(comps) > 0 {
		rows = append(rows, groupHeader("No dependencies", w))
	}
	for _, i := range lone {
		if i == m.selected {
			anchor = len(rows)
		}
		rows, order = append(rows, line(i)), append(order, i)
	}
	return rows, order, max(0, anchor)
}

// gridNode is a node placed in a component's layers: a task, a ghost, or a dummy that carries a long edge.
type gridNode struct {
	node, layer, y, h int // node is -1 for a dummy
	lines             []string
}

// layoutComponent places one connected part: longest-path layers, dummies on long edges, two barycentre
// sweeps for the order, each node at the median row of what it waits on. false: too narrow for its layers.
func (m *dashModel) layoutComponent(g taskDAG, adj [][]int, members []int, w int) ([]string, []int, int, bool) {
	in := map[int]bool{}
	for _, v := range members {
		in[v] = true
	}
	state := map[int]int{}
	var acyclic [][2]int
	var visit func(v int)
	visit = func(v int) {
		state[v] = 1
		for _, u := range adj[v] {
			switch state[u] {
			case 0:
				acyclic = append(acyclic, [2]int{v, u})
				visit(u)
			case 2:
				acyclic = append(acyclic, [2]int{v, u})
			}
		}
		state[v] = 2
	}
	indeg := map[int]int{}
	for _, v := range members {
		for _, u := range adj[v] {
			indeg[u]++
		}
	}
	for _, roots := range []bool{true, false} {
		for _, v := range members {
			if state[v] == 0 && (indeg[v] == 0) == roots {
				visit(v)
			}
		}
	}
	layer := map[int]int{}
	deg, out := map[int]int{}, map[int][]int{}
	for _, e := range acyclic {
		deg[e[1]]++
		out[e[0]] = append(out[e[0]], e[1])
	}
	var queue []int
	for _, v := range members {
		if deg[v] == 0 {
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, u := range out[v] {
			layer[u] = max(layer[u], layer[v]+1)
			if deg[u]--; deg[u] == 0 {
				queue = append(queue, u)
			}
		}
	}
	var gn []gridNode
	at := map[int]int{}
	for _, v := range members {
		at[v] = len(gn)
		gn = append(gn, gridNode{node: v, layer: layer[v]})
	}
	var edges [][2]int
	for _, e := range acyclic {
		prev := at[e[0]]
		for l := layer[e[0]] + 1; l < layer[e[1]]; l++ {
			gn = append(gn, gridNode{node: -1, layer: l, h: 1})
			edges = append(edges, [2]int{prev, len(gn) - 1})
			prev = len(gn) - 1
		}
		edges = append(edges, [2]int{prev, at[e[1]]})
	}
	L := 0
	for _, x := range gn {
		L = max(L, x.layer+1)
	}
	layers := make([][]int, L)
	for i, x := range gn {
		layers[x.layer] = append(layers[x.layer], i)
	}
	preds, succs := make([][]int, len(gn)), make([][]int, len(gn))
	for _, e := range edges {
		succs[e[0]] = append(succs[e[0]], e[1])
		preds[e[1]] = append(preds[e[1]], e[0])
	}
	pos := make([]float64, len(gn))
	place := func() {
		for _, ids := range layers {
			for k, i := range ids {
				pos[i] = float64(k)
			}
		}
	}
	sweep := func(l int, by [][]int) {
		key := map[int]float64{}
		for _, i := range layers[l] {
			key[i] = pos[i]
			if len(by[i]) > 0 {
				sum := 0.0
				for _, j := range by[i] {
					sum += pos[j]
				}
				key[i] = sum / float64(len(by[i]))
			}
		}
		slices.SortStableFunc(layers[l], func(a, b int) int {
			switch {
			case key[a] < key[b]:
				return -1
			case key[a] > key[b]:
				return 1
			}
			return 0
		})
		place()
	}
	place()
	for range 2 {
		for l := 1; l < L; l++ {
			sweep(l, preds)
		}
		for l := L - 2; l >= 0; l-- {
			sweep(l, succs)
		}
	}
	lanes := make([][]int, max(0, L-1))
	for l := range lanes {
		for _, i := range layers[l+1] {
			if len(preds[i]) > 0 {
				lanes[l] = append(lanes[l], i)
			}
		}
	}
	channels := 0
	for _, ls := range lanes {
		channels += len(ls) + 3
	}
	colW := min(graphMaxCol, (w-channels)/max(1, L))
	if colW < graphMinCol {
		return nil, nil, -1, false
	}
	colX := make([]int, L)
	for l := 1; l < L; l++ {
		colX[l] = colX[l-1] + colW + len(lanes[l-1]) + 3
	}
	for i := range gn {
		x := &gn[i]
		if x.node < 0 {
			continue
		}
		node := g.nodes[x.node]
		if node.task < 0 {
			x.lines = []string{ansi.Truncate(safeText(node.label), colW-3, "…"), ansi.Truncate(safeText(node.note), colW-3, "…")}
		} else {
			x.lines = strings.Split(ansi.Wordwrap(safeText(node.label), colW-3, ""), "\n")
			if len(x.lines) > 2 {
				x.lines = []string{x.lines[0], ansi.Truncate(strings.Join(x.lines[1:], " "), colW-3, "…")}
			}
		}
		x.h = len(x.lines)
	}
	height := 0
	for _, ids := range layers {
		next := 0
		for _, i := range ids {
			want := next
			if ps := preds[i]; len(ps) > 0 {
				ys := make([]int, len(ps))
				for k, p := range ps {
					ys[k] = gn[p].y
				}
				slices.Sort(ys)
				want = max(next, ys[len(ys)/2])
			}
			gn[i].y = want
			next = want + gn[i].h
			if gn[i].node >= 0 {
				next++
			}
			height = max(height, gn[i].y+gn[i].h)
		}
	}
	c := newCanvas(colX[L-1]+colW, height, m.glyphs)
	sel := -1
	if v, ok := at[m.selected]; ok && m.selected < len(m.data.Tasks) {
		sel = v
	}
	for l := range lanes {
		c0 := colX[l] + colW
		for _, e := range edges {
			s, t := e[0], e[1]
			if gn[s].layer != l {
				continue
			}
			lane := c0 + 1 + slices.Index(lanes[l], t)
			hot := s == sel || t == sel
			sx := colX[l]
			if gn[s].node >= 0 {
				sx += ansi.StringWidth(gn[s].lines[0]) + 3
			}
			c.hline(gn[s].y, sx, lane, hot)
			c.vline(lane, gn[s].y, gn[t].y, hot)
			if gn[t].node < 0 {
				c.hline(gn[t].y, lane, colX[l+1], hot)
				continue
			}
			c.hline(gn[t].y, lane, colX[l+1]-2, hot)
			c.arrow(colX[l+1]-2, gn[t].y, hot)
		}
	}
	var order []int
	anchor := -1
	for i, x := range gn {
		if x.node < 0 {
			continue
		}
		node := g.nodes[x.node]
		mark, markStyle, text := "◌", muted, muted
		if m.glyphs == ui.GlyphsASCII || m.glyphs == ui.GlyphsOff {
			mark = "o"
		}
		if node.task >= 0 {
			st := m.statusOf(m.data.Tasks[node.task])
			mark, markStyle, text = glyph(m.glyphs, st.mark), st.style, inkText
			if i == sel {
				text = brand.Bold(true)
				anchor = x.y
			}
		}
		c.text(colX[x.layer], x.y, mark, markStyle)
		for k, l := range x.lines {
			style := text
			if node.task < 0 && k > 0 {
				style = ruleText
			}
			c.text(colX[x.layer]+2, x.y+k, l, style)
		}
	}
	slices.SortStableFunc(gn, func(a, b gridNode) int {
		if a.layer != b.layer {
			return a.layer - b.layer
		}
		return a.y - b.y
	})
	for _, x := range gn {
		if x.node >= 0 && g.nodes[x.node].task >= 0 {
			order = append(order, g.nodes[x.node].task)
		}
	}
	return c.rows(), order, anchor, true
}

// canvas is a grid of cells: text with a style, or edge stubs that join into box-drawing runes.
type canvas struct {
	w, h    int
	cells   [][]string
	styles  [][]int // index into palette
	palette []lipgloss.Style
	set     [][]bool
	mask    [][]uint8
	hot     [][]bool
	arrows  [][]bool
	glyphs  ui.GlyphTier
}

const (
	edgeN uint8 = 1 << iota
	edgeS
	edgeE
	edgeW
)

func newCanvas(w, h int, glyphs ui.GlyphTier) *canvas {
	c := &canvas{w: w, h: h, glyphs: glyphs, palette: []lipgloss.Style{{}, ruleText, brand}}
	for range h {
		c.cells = append(c.cells, make([]string, w))
		c.styles = append(c.styles, make([]int, w))
		c.set = append(c.set, make([]bool, w))
		c.mask = append(c.mask, make([]uint8, w))
		c.hot = append(c.hot, make([]bool, w))
		c.arrows = append(c.arrows, make([]bool, w))
	}
	return c
}

func (c *canvas) in(x, y int) bool { return x >= 0 && y >= 0 && x < c.w && y < c.h }

func (c *canvas) text(x, y int, s string, style lipgloss.Style) {
	id := c.styleID(style)
	for _, r := range s {
		if c.in(x, y) {
			c.cells[y][x], c.styles[y][x], c.set[y][x] = string(r), id, true
		}
		x++
	}
}

func (c *canvas) hline(y, x0, x1 int, hot bool) {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	for x := x0; x < x1; x++ {
		if c.in(x, y) && c.in(x+1, y) {
			c.mask[y][x] |= edgeE
			c.mask[y][x+1] |= edgeW
			c.hot[y][x] = c.hot[y][x] || hot
			c.hot[y][x+1] = c.hot[y][x+1] || hot
		}
	}
}

func (c *canvas) vline(x, y0, y1 int, hot bool) {
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	for y := y0; y < y1; y++ {
		if c.in(x, y) && c.in(x, y+1) {
			c.mask[y][x] |= edgeS
			c.mask[y+1][x] |= edgeN
			c.hot[y][x] = c.hot[y][x] || hot
			c.hot[y+1][x] = c.hot[y+1][x] || hot
		}
	}
}

func (c *canvas) arrow(x, y int, hot bool) {
	if c.in(x, y) {
		c.arrows[y][x] = true
		c.hot[y][x] = c.hot[y][x] || hot
	}
}

// edgeRunes join a cell's stubs: rounded corners, tees where edges merge or split, a cross where two meet.
var edgeRunes = map[uint8]string{
	edgeE | edgeW: "─", edgeN | edgeS: "│", edgeE: "─", edgeW: "─", edgeN: "│", edgeS: "│",
	edgeE | edgeS: "╭", edgeW | edgeS: "╮", edgeE | edgeN: "╰", edgeW | edgeN: "╯",
	edgeN | edgeS | edgeE: "├", edgeN | edgeS | edgeW: "┤", edgeE | edgeW | edgeS: "┬", edgeE | edgeW | edgeN: "┴",
	edgeN | edgeS | edgeE | edgeW: "┼",
}

func (c *canvas) rune(x, y int) (string, int) {
	if c.set[y][x] {
		return c.cells[y][x], c.styles[y][x]
	}
	style := 1
	if c.hot[y][x] {
		style = 2
	}
	ascii := c.glyphs == ui.GlyphsASCII || c.glyphs == ui.GlyphsOff
	switch m := c.mask[y][x]; {
	case c.arrows[y][x]:
		if ascii {
			return ">", style
		}
		return "▶", style
	case m == 0:
		return " ", 0
	case ascii && (m == edgeE|edgeW || m == edgeE || m == edgeW):
		return "-", style
	case ascii && (m == edgeN|edgeS || m == edgeN || m == edgeS):
		return "|", style
	case ascii:
		return "+", style
	default:
		return edgeRunes[m], style
	}
}

// rows renders the grid, one styled run per stretch of cells that share a style.
func (c *canvas) rows() []string {
	out := make([]string, c.h)
	for y := range c.h {
		var b strings.Builder
		var run strings.Builder
		cur, started := 0, false
		flush := func() {
			if run.Len() > 0 {
				b.WriteString(c.palette[cur].Render(run.String()))
				run.Reset()
			}
		}
		for x := range c.w {
			r, st := c.rune(x, y)
			if !started || st != cur {
				flush()
				cur, started = st, true
			}
			run.WriteString(r)
		}
		flush()
		out[y] = strings.TrimRight(b.String(), " ")
	}
	return out
}

// styleID is style's place in the palette, added on first use.
func (c *canvas) styleID(style lipgloss.Style) int {
	probe := style.Render("x")
	for i, p := range c.palette {
		if p.Render("x") == probe {
			return i
		}
	}
	c.palette = append(c.palette, style)
	return len(c.palette) - 1
}
