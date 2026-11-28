// SPEC: _spec/internal/dashboard/workspace-instances.puml
package dashboard

import "github.com/charmbracelet/x/ansi"

type taskGraph struct {
	tasks  map[string]Task
	active map[string]bool
	paths  map[string]*graphPath
}

type graphPath struct {
	label, first string
	count, width int
	parent       *graphPath
}

func newTaskGraph(tasks []Task) *taskGraph {
	g := &taskGraph{tasks: make(map[string]Task, len(tasks)), active: make(map[string]bool), paths: make(map[string]*graphPath, len(tasks))}
	for _, t := range tasks {
		g.tasks[t.ID] = t
	}
	return g
}

func (g *taskGraph) path(id string) *graphPath {
	if g.active[id] {
		label := "cycle: " + safeText(id)
		return &graphPath{label: label, first: label, count: 1, width: ansi.StringWidth(label)}
	}
	if p := g.paths[id]; p != nil {
		return p
	}
	label := safeText(id)
	t, exists := g.tasks[id]
	if exists {
		label = safeText(t.Title)
	}
	p := &graphPath{label: label, first: label, count: 1, width: ansi.StringWidth(label)}
	g.active[id] = true
	for _, dep := range t.DependsOn {
		parent := g.path(dep)
		if p.parent == nil || parent.count > p.parent.count {
			p.parent = parent
		}
	}
	delete(g.active, id)
	if p.parent != nil {
		p.first, p.count = p.parent.first, p.parent.count+1
		p.width += p.parent.width + 3
	}
	g.paths[id] = p
	return p
}
