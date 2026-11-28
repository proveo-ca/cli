// SPEC: _spec/internal/dashboard/task-graph.puml
package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func graphModel(t *testing.T) *dashModel {
	t.Helper()
	user := t.TempDir()
	t.Setenv("HOME", user)
	m := mediaDashboard(t, user, mediaHome(t, user))
	mediaGraph(m)
	m.view = 1
	return m
}

func edgeNames(m *dashModel) []string {
	g := m.taskDAG()
	name := func(i int) string {
		if n := g.nodes[i]; n.task >= 0 {
			return m.data.Tasks[n.task].ID
		} else {
			return n.key
		}
	}
	var out []string
	for _, e := range g.edges {
		out = append(out, name(e.from)+" → "+name(e.to))
	}
	slices.Sort(out)
	return out
}

func TestTheGraphLinksTasksFourWays(t *testing.T) {
	m := graphModel(t)
	got := edgeNames(m)
	review := m.data.Tasks[slices.IndexFunc(m.data.Tasks, func(t Task) bool { return t.Review != nil })].ID
	for _, want := range []string{
		"acme-39 → acme-44", "acme-43 → acme-44", // depends_on
		review + " → acme-41",                                            // acme-41's PR is based on the review's branch
		"linear:ACME-40 → acme-41",                                       // Linear: ACME-40 blocks ACME-41
		"plan:billing-usage-meter → acme-37", "linear:ACME-40 → acme-37", // the plan's blocked_by lines
	} {
		if !slices.Contains(got, want) {
			t.Errorf("no edge %s in %q", want, got)
		}
	}
	g := m.taskDAG()
	i := slices.IndexFunc(g.nodes, func(n graphNode) bool { return n.key == "linear:ACME-40" })
	if i < 0 || g.nodes[i].note != "Linear · In Progress" || !strings.Contains(g.nodes[i].label, "Meter billing usage") {
		t.Errorf("a ghost named by a plan and by Linear takes Linear's title and state: %+v", g.nodes[i])
	}
	meta := m.metadata[metadataKey(m.data.Tasks[0])]
	meta.planRefs["plan:billing-usage-meter"] = planInfo{Slug: "billing-usage-meter", Done: true}
	m.metadata[meta.key] = meta
	if slices.Contains(edgeNames(m), "plan:billing-usage-meter → acme-37") {
		t.Error("a done plan blocks nothing")
	}
}

func TestTheGraphDrawsLayersWithJoinedEdges(t *testing.T) {
	m := graphModel(t)
	rows, order, _ := m.renderGraph(90)
	plain := ansi.Strip(strings.Join(rows, "\n"))
	for _, want := range []string{
		"◌ ACME-40 Meter billing usage ─", "Linear · In Progress", "┴┬▶ ● Add OpenAPI examples for billing",
		"◌ billing-usage-meter ─", "┴▶ × Cap overnight worker budgets", "plan · planned",
		"◇ Persist session history ─", "╮", "┴▶ ○ Bind evidence to the review",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("the graph lacks %q:\n%s", want, plain)
		}
	}
	if len(order) != len(m.data.Tasks) {
		t.Fatalf("every task is in the drawn order once: %v", order)
	}
	m.View()
	m.selected = order[0]
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.selected != order[1] {
		t.Errorf("↓ follows the drawing: %d, want %d", m.selected, order[1])
	}
}

func TestALoneTaskListsUnderNoDependencies(t *testing.T) {
	m := graphModel(t)
	m.data.Tasks = append(m.data.Tasks, Task{ID: "acme-50", Title: "Tidy the changelog", Agent: "codex"})
	rows, _, _ := m.renderGraph(90)
	plain := ansi.Strip(strings.Join(rows, "\n"))
	head := strings.Index(plain, "No dependencies")
	if head < 0 || !strings.Contains(plain[head:], "Tidy the changelog") {
		t.Errorf("a task nothing links lists under No dependencies:\n%s", plain)
	}
}

func TestANarrowPaneFallsBackToOneLinePerTask(t *testing.T) {
	m := graphModel(t)
	rows, order, _ := m.renderGraph(30)
	if len(order) != len(m.data.Tasks) {
		t.Fatalf("the fallback still lists every task: %v", order)
	}
	for _, r := range rows {
		if strings.ContainsAny(ansi.Strip(r), "╮┴▶") {
			t.Errorf("30 cells hold no layers; got an edge:\n%s", ansi.Strip(strings.Join(rows, "\n")))
			break
		}
	}
}

func TestEveryGraphFrameFillsTheTerminal(t *testing.T) {
	for _, size := range breakpointGrid {
		m := graphModel(t)
		m.width, m.height = size[0], size[1]
		lines := strings.Split(m.View().Content, "\n")
		if len(lines) != size[1] {
			t.Fatalf("%dx%d: %d rows", size[0], size[1], len(lines))
		}
		for i, l := range lines {
			if got := ansi.StringWidth(l); got != size[0] {
				t.Errorf("%dx%d row %d is %d cells: %q", size[0], size[1], i, got, ansi.Strip(l))
			}
		}
	}
}

func TestPlansDeclareBlockersOnePerLine(t *testing.T) {
	t.Parallel()
	got := blockedBy("@startuml\n' status: planned\n' blocked_by: billing-usage-meter\n' blocked_by: CONN-123, #3951\n" +
		"' blocked_by: billing-usage-meter\n@enduml\n")
	if !slices.Equal(got, []string{"billing-usage-meter", "CONN-123", "#3951"}) {
		t.Errorf("puml blocked_by = %q", got)
	}
	if got := blockedBy("# Plan\nstatus: planned\nblocked_by: `other-plan.md`\n- blocked_by: https://github.com/acme/api/pull/12\n"); !slices.Equal(got, []string{"other-plan.md", "https://github.com/acme/api/pull/12"}) {
		t.Errorf("md blocked_by = %q", got)
	}
	for ref, want := range map[string]string{
		"CONN-123": "linear:CONN-123", "conn-123": "linear:CONN-123", "#3951": "pr:3951",
		"https://github.com/acme/api/pull/12/files": "pr:12", "https://linear.app/acme/issue/CONN-9/x": "linear:CONN-9",
		"_spec/_plans/billing-usage-meter.puml": "plan:billing-usage-meter", "billing-usage-meter": "plan:billing-usage-meter",
		"https://github.com/acme/api/blob/main/_spec/_plans/x.md": "plan:x",
	} {
		if _, key, _ := refKind(ref); key != want {
			t.Errorf("refKind(%q) = %q, want %q", ref, key, want)
		}
	}
}

func TestATasksPlanAndItsBlockersAreRead(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "_spec", "_plans")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("rate-limits.puml", "@startuml\n' status: planned\n' blocked_by: usage-meter\n' blocked_by: old-plan\n' blocked_by: CONN-7\n@enduml\n")
	write("usage-meter.md", "# Usage meter\nstatus: in progress\n")
	write("old-plan.puml", "' status: implemented 2026-10-01\n")
	src := pickSource{Kind: "plans", Dir: "_spec/_plans", HideStatus: []string{"implemented", "done"}}
	plan, refs := readTaskPlan(root, src, "_spec/_plans/rate-limits.puml", "")
	if plan.Slug != "rate-limits" || !slices.Equal(plan.BlockedBy, []string{"usage-meter", "old-plan", "CONN-7"}) {
		t.Fatalf("plan = %+v", plan)
	}
	if refs["plan:usage-meter"].Status != "in progress" || !refs["plan:old-plan"].Done {
		t.Errorf("blocker plans = %+v", refs)
	}
	if plan, _ := readTaskPlan(root, src, "https://linear.app/acme/issue/CONN-7", "https://github.com/acme/api/blob/main/_spec/_plans/usage-meter.md"); plan.Slug != "usage-meter" {
		t.Errorf("a PR's Plan: link names the plan when the ticket does not: %+v", plan)
	}
}

func TestLinearBlockersAreTheOpenInverseBlocks(t *testing.T) {
	saved := linearQuery
	t.Cleanup(func() { linearQuery = saved })
	var query string
	linearQuery = func(_, q string, _ any, _ time.Duration, out any) error {
		query = q
		return json.Unmarshal([]byte(`{"issue": {"identifier": "CONN-9", "title": "Export", "inverseRelations": {"nodes": [
			{"type": "blocks", "issue": {"identifier": "CONN-7", "title": "Meter usage", "state": {"name": "In Progress", "type": "started"}}},
			{"type": "blocks", "issue": {"identifier": "CONN-5", "title": "Old", "state": {"name": "Done", "type": "completed"}}},
			{"type": "related", "issue": {"identifier": "CONN-3", "title": "Nearby", "state": {"name": "Todo", "type": "unstarted"}}}]}}}`), out)
	}
	got := readLinearIssue("token", "CONN-9")
	if len(got.BlockedBy) != 1 || got.BlockedBy[0].Identifier != "CONN-7" || got.BlockedBy[0].State != "In Progress" {
		t.Errorf("blockers = %+v", got.BlockedBy)
	}
	if !strings.Contains(query, "inverseRelations") {
		t.Errorf("the query must ask for the relations that point at the issue:\n%s", query)
	}
}
