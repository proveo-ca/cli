// SPEC: _spec/internal/dashboard/task-graph.puml
package dashboard

import (
	"slices"
	"strconv"
	"strings"
)

// graphNode is one node of the task graph: a task, or a ghost for a blocker no task here works on.
type graphNode struct {
	task        int // index into m.data.Tasks; -1 for a ghost
	key         string
	label, note string
}

// graphEdge runs from a blocker to what it blocks.
type graphEdge struct{ from, to int }

type taskDAG struct {
	nodes []graphNode
	edges []graphEdge
}

// taskDAG links the tasks by what they wait on: depends_on, a PR stacked on another's branch, Linear's
// "blocked by", and a plan's blocked_by lines. A blocker no task works on becomes a ghost node.
func (m *dashModel) taskDAG() taskDAG {
	var g taskDAG
	keys := map[string]int{}
	seen := map[graphEdge]bool{}
	for i, t := range m.data.Tasks {
		g.nodes = append(g.nodes, graphNode{task: i, label: m.titleFor(t)})
		for _, k := range m.graphKeys(t) {
			if _, taken := keys[k]; !taken {
				keys[k] = i
			}
		}
	}
	ghost := func(key, label, note string) int {
		if i, ok := keys[key]; ok {
			if n := &g.nodes[i]; n.task < 0 && len(note) > len(n.note) {
				n.label, n.note = label, note
			}
			return i
		}
		g.nodes = append(g.nodes, graphNode{task: -1, key: key, label: label, note: note})
		keys[key] = len(g.nodes) - 1
		return keys[key]
	}
	edge := func(from, to int) {
		if e := (graphEdge{from, to}); from != to && !seen[e] {
			seen[e] = true
			g.edges = append(g.edges, e)
		}
	}
	for i, t := range m.data.Tasks {
		meta := m.metadata[metadataKey(t)]
		for _, dep := range t.DependsOn {
			edge(ghost("task:"+dep, dep, "job"), i)
		}
		for _, base := range prBases(t, meta) {
			if from, ok := keys["head:"+base]; ok {
				edge(from, i)
			}
		}
		for _, b := range meta.linear.BlockedBy {
			edge(ghost("linear:"+strings.ToUpper(b.Identifier), b.Identifier+" "+b.Title, "Linear · "+b.State), i)
		}
		for _, ref := range meta.plan.BlockedBy {
			kind, key, label := refKind(ref)
			note := map[string]string{"linear": "Linear", "pr": "pull request", "plan": "plan"}[kind]
			if p, ok := meta.planRefs[key]; ok {
				if p.Done {
					continue
				}
				if p.Status != "" {
					note += " · " + p.Status
				}
			}
			edge(ghost(key, label, note), i)
		}
	}
	return g
}

// graphKeys are the names other tasks can wait on t by: its id and job, its Linear issue, its PRs and their
// branches, and its plan.
func (m *dashModel) graphKeys(t Task) []string {
	meta := m.metadata[metadataKey(t)]
	keys := []string{"task:" + t.ID}
	if t.Job != "" {
		keys = append(keys, "task:"+t.Job)
	}
	for _, id := range []string{linearIdentifier(t), meta.linear.Identifier} {
		if id != "" {
			keys = append(keys, "linear:"+strings.ToUpper(id))
		}
	}
	if r := t.Review; r != nil {
		keys = append(keys, "pr:"+strconv.Itoa(r.Number), "head:"+r.Head)
	}
	for _, p := range meta.prs {
		if p.Number > 0 {
			keys = append(keys, "pr:"+strconv.Itoa(p.Number))
		}
		if p.Head != "" {
			keys = append(keys, "head:"+p.Head)
		}
	}
	if meta.plan.Slug != "" {
		keys = append(keys, "plan:"+meta.plan.Slug)
	}
	return keys
}

// prBases are the branches t's PRs merge into; one that is another task's head stacks t on that task.
func prBases(t Task, meta taskMetadata) []string {
	var out []string
	if r := t.Review; r != nil && r.Base != "" {
		out = append(out, r.Base)
	}
	for _, p := range meta.prs {
		if p.Base != "" && !slices.Contains(out, p.Base) {
			out = append(out, p.Base)
		}
	}
	return out
}
