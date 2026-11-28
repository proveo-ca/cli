package dashboard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

func TestTheDefaultTaskSourcesAreTheDocumentedFile(t *testing.T) {
	var names, kinds []string
	for _, s := range defaultTaskSources {
		names, kinds = append(names, s.Name), append(kinds, s.Kind)
	}
	if strings.Join(names, ",") != "Plans,Pull requests,Linear" || strings.Join(kinds, ",") != "plans,github,linear" {
		t.Errorf("defaults = %v %v", names, kinds)
	}
	for _, s := range defaultTaskSources {
		if s.Prompt == "" {
			t.Errorf("%s has no prompt", s.Name)
		}
	}
	for _, c := range []struct{ yaml, why string }{
		{"sources:\n  - {name: X, kind: jira}\n", `kind "jira" is not plans, github, linear or command`},
		{"sources:\n  - {kind: plans, dir: x}\n", "a source needs a name"},
		{"sources:\n  - {name: P, kind: plans}\n", "a plans source needs a dir"},
		{"sources:\n  - {name: L, kind: linear}\n", "a linear source needs a token_var"},
		{"sources:\n  - {name: C, kind: command}\n", "a command source needs run"},
		{"sources:\n  - {name: C, kind: command, run: [x], timeout: soon}\n", "timeout"},
	} {
		file := filepath.Join(t.TempDir(), "dashboard.yml")
		if err := os.WriteFile(file, []byte("tasks:\n"+indent(c.yaml)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readDashboardConfig(file); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%q: err %v, want %q", c.yaml, err, c.why)
		}
	}
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestPlanTasksSkipDonePlansNewestFirst(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "_spec", "_plans")
	write := func(rel, body string, age time.Duration) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	write("secret-custody.puml", "@startuml\n' status: PLANNED. DECIDED 2026-11-14\ntitle **Secret custody** — keys never reach $who\\nPlan. Companion: x.md\n@enduml\n", time.Hour)
	write("ship.puml", "@startuml\n' status: implemented; native sbx verification pending\ntitle Ship\n@enduml\n", 0)
	write("old.puml", "@startuml\n' status: DEFERRED. Nothing here is scheduled\n@enduml\n", 0)
	write("notes.md", "# Cloud file wave 3\n\nStatus: in progress\n", 2*time.Hour)
	write("sub/deep.md", "# One level down\n", 3*time.Hour)
	write("sub/two/deeper.md", "# Too deep\n", 0)
	write("diagram.svg", "<svg/>", 0)
	s := defaultTaskSources[0]
	tasks, err := planTasks(s, root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range tasks {
		got = append(got, x.fields["path"]+"|"+x.fields["title"]+"|"+x.fields["status"])
	}
	want := []string{
		"_spec/_plans/secret-custody.puml|Secret custody — keys never reach|PLANNED. DECIDED 2026-11-14",
		"_spec/_plans/notes.md|Cloud file wave 3|in progress",
		"_spec/_plans/sub/deep.md|One level down|sub/deep.md",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("plans:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if it := tasks[0].item(s.Prompt, s.Branch); it.Branch != "secret-custody" || !strings.Contains(it.Prompt, "Implement the plan in _spec/_plans/secret-custody.puml (Secret custody") ||
		it.Task != "Secret custody — keys never reach" {
		t.Errorf("item = %+v", it)
	}
	if _, err := planTasks(s, t.TempDir()); err == nil || !strings.Contains(err.Error(), "no _spec/_plans") {
		t.Errorf("a workspace without plans: %v", err)
	}
}

const myPRs = `[
 {"number":3731,"url":"https://github.com/acme/app/pull/3731","title":"PLUV-1402 (3/5): Parity check","headRefName":"pdf-3","baseRefName":"pdf-2",
  "isDraft":false,"mergeable":"MERGEABLE","statusCheckRollup":[],"updatedAt":"2026-11-20T10:00:00Z"},
 {"number":3720,"url":"https://github.com/acme/app/pull/3720","title":"PLUV-1402 (2/5): Store documents","headRefName":"pdf-2","baseRefName":"pdf-1",
  "isDraft":false,"mergeable":"CONFLICTING","statusCheckRollup":[],"updatedAt":"2026-11-19T10:00:00Z"},
 {"number":3710,"url":"https://github.com/acme/app/pull/3710","title":"PLUV-1402 (1/5): Classify PDFs","headRefName":"pdf-1","baseRefName":"dev",
  "isDraft":false,"mergeable":"MERGEABLE","statusCheckRollup":[{"__typename":"CheckRun","name":"Lint","status":"COMPLETED","conclusion":"FAILURE"}],
  "updatedAt":"2026-11-18T10:00:00Z"},
 {"number":4009,"url":"https://github.com/acme/app/pull/4009","title":"CONN-123: Standardize OAuth connect errors","headRefName":"conn-123","baseRefName":"dev",
  "isDraft":false,"mergeable":"MERGEABLE","statusCheckRollup":[],"updatedAt":"2026-11-26T10:00:00Z"}
]`

func TestGithubTasksFoldAStackIntoOneTask(t *testing.T) {
	asked := fakeReviews(t, myPRs)
	s := defaultTaskSources[1]
	tasks, err := githubTasks(s, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*asked)[0], "pr list --search author:@me --state open") {
		t.Errorf("asked %q", (*asked)[0])
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks = %d, want the stack folded into one beside #4009", len(tasks))
	}
	single, stack := tasks[0], tasks[1]
	if single.fields["title"] != "CONN-123: Standardize OAuth connect errors" || single.fields["status"] != "ready" || single.tone != "ok" {
		t.Errorf("single = %+v", single)
	}
	if stack.fields["title"] != "PLUV-1402: Classify PDFs" || stack.fields["id"] != "#3710–#3731" || stack.tone != "fail" ||
		stack.fields["status"] != "3 PRs: 1 errors: 1 check failed · 1 conflicts · 1 ready" {
		t.Errorf("stack = %+v", stack.fields)
	}
	prs := strings.Split(stack.fields["prs"], "\n")
	if len(prs) != 3 || !strings.HasPrefix(prs[0], "- #3710") || !strings.HasPrefix(prs[2], "- #3731") || !strings.Contains(prs[1], "on pdf-2: conflicts") {
		t.Errorf("prs bottom first:\n%s", stack.fields["prs"])
	}
	it := stack.item(s.Prompt, s.Branch)
	if it.Branch != "pdf-3" {
		t.Errorf("a stack's branch = %q, want its top PR's head", it.Branch)
	}
	if !strings.Contains(it.Prompt, "Continue PLUV-1402: Classify PDFs:") || !strings.Contains(it.Prompt, "#3720") ||
		!strings.Contains(it.Prompt, "(errors: 1 check failed, conflicts)") || it.Task != "PLUV-1402: Classify PDFs" {
		t.Errorf("stack prompt:\n%s", it.Prompt)
	}
	s.Stacks = false
	if tasks, _ := githubTasks(s, "/repo"); len(tasks) != 4 {
		t.Errorf("without stacks: %d tasks", len(tasks))
	}
}

func TestLinearTasksPutStartedWorkFirstAndSayWhenNoTokenIsSet(t *testing.T) {
	was, wasToken := linearQuery, tokenFor
	t.Cleanup(func() { linearQuery, tokenFor = was, wasToken })
	s := defaultTaskSources[2]
	tokenFor = func(string, string) string { return "" }
	if _, err := linearTasks(s, "/repo"); err == nil || !strings.Contains(err.Error(), "set LINEAR_API_KEY in the workspace .env") {
		t.Errorf("no token: %v", err)
	}
	tokenFor = func(_, name string) string { return "lin_test_" + name }
	var hidden any
	linearQuery = func(token, _ string, vars any, _ time.Duration, out any) error {
		if token != "lin_test_LINEAR_API_KEY" {
			return errors.New("wrong token")
		}
		hidden = vars.(map[string]any)["hide"]
		return json.Unmarshal([]byte(`{"viewer":{"assignedIssues":{"nodes":[
			{"identifier":"CONN-113","title":"Share discovery","url":"https://linear.app/x/CONN-113","updatedAt":"2026-11-27T10:00:00Z","state":{"name":"Backlog","type":"backlog"}},
			{"identifier":"CONN-4","title":"Acumatica re-fetches","url":"https://linear.app/x/CONN-4","updatedAt":"2026-11-20T10:00:00Z","state":{"name":"In Progress","type":"started"}},
			{"identifier":"CONN-28","title":"Projector PSA","url":"https://linear.app/x/CONN-28","updatedAt":"2026-11-25T10:00:00Z","state":{"name":"In Review","type":"started"}}]}}}`), out)
	}
	tasks, err := linearTasks(s, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, x := range tasks {
		order = append(order, x.fields["id"])
	}
	if strings.Join(order, ",") != "CONN-28,CONN-4,CONN-113" {
		t.Errorf("order = %v; started first, newest first within", order)
	}
	if h, _ := json.Marshal(hidden); string(h) != `["completed","canceled"]` {
		t.Errorf("hidden states = %s", h)
	}
	it := tasks[0].item(s.Prompt, s.Branch)
	if it.Branch != "CONN-28" {
		t.Errorf("a Linear pick's branch = %q, want the issue id", it.Branch)
	}
	if it.Title != "CONN-28 Projector PSA" || it.Status != "In Review" || it.Tone != "ok" ||
		it.Prompt != `Work on CONN-28 "Projector PSA" (https://linear.app/x/CONN-28), In Review in Linear. Read the issue, then follow the repository's AGENTS.md.` {
		t.Errorf("item = %+v", it)
	}
}

func TestACommandSourcePlugsInAnyTracker(t *testing.T) {
	s := pickSource{Name: "Jira", Kind: "command", Prompt: "Fix {{id}}: {{title}} ({{url}})",
		Run: []string{"sh", "-c", `printf '[{"id":"OPS-7","title":"Rotate keys","status":"To Do","url":"https://jira.example/OPS-7","tone":"warn"},{"title":"Own prompt","prompt":"Do exactly this."},{"status":"no title"}]'`}}
	sec := loadSource(s, t.TempDir())
	if sec.Note != "" || len(sec.Items) != 2 {
		t.Fatalf("section = %+v", sec)
	}
	if it := sec.Items[0]; it.Title != "OPS-7 Rotate keys" || it.Tone != "warn" || it.Prompt != "Fix OPS-7: Rotate keys (https://jira.example/OPS-7)" {
		t.Errorf("item = %+v", it)
	}
	if sec.Items[1].Prompt != "Do exactly this." {
		t.Errorf("a printed prompt was replaced: %+v", sec.Items[1])
	}
	s.Run = []string{"sh", "-c", "echo 'jira: not logged in' >&2; exit 1"}
	if sec := loadSource(s, t.TempDir()); sec.Note != "sh: jira: not logged in" {
		t.Errorf("a failing command: %q", sec.Note)
	}
	s.Run, s.Timeout = []string{"sh", "-c", "sleep 5"}, "100ms"
	if sec := loadSource(s, t.TempDir()); !strings.Contains(sec.Note, "took over 100ms") {
		t.Errorf("a slow command: %q", sec.Note)
	}
}

func TestNewInstancePicksATaskAndHandsItsPromptToProveoRun(t *testing.T) {
	m := sampleModel(140, 44, ui.GlyphsSafe)
	m.Home, m.CWD = t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(m.Home, dashboardConfigFile), []byte(`tasks:
  sources:
    - name: Ops
      kind: command
      run: ["sh", "-c", "printf '[{\"id\":\"OPS-7\",\"title\":\"Rotate keys\",\"status\":\"To Do\"}]'"]
      prompt: "Fix {{id}}: {{title}}"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	var ran []string
	m.Binary = "/usr/local/bin/proveo"
	m.runner = func(_ string, args ...string) tea.Cmd { ran = args; return nil }
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.taskPicker == nil {
		t.Fatal("n opened no picker")
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Ops") || !strings.Contains(view, "loading…") || strings.Contains(view, "Plans") {
		t.Errorf("dashboard.yml's sources should replace the defaults:\n%s", view)
	}
	for _, msg := range runCmd(cmd) {
		m.Update(msg)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "OPS-7 Rotate keys") {
		t.Fatalf("the source's answer did not fill the picker:\n%s", view)
	}
	for _, r := range "rot" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Fix OPS-7: Rotate keys") {
		t.Fatalf("the prompt step:\n%s", view)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.taskPicker != nil || len(ran) < 5 || ran[0] != "run" || ran[1] != "--queue" || ran[2] != "--prompt-file" || ran[4] != "--task" || ran[5] != "OPS-7 Rotate keys" {
		t.Fatalf("ran %q", ran)
	}
	b, err := os.ReadFile(ran[3])
	if err != nil || strings.TrimSpace(string(b)) != "Fix OPS-7: Rotate keys" {
		t.Errorf("prompt file = %q, %v", b, err)
	}
	m.Update(resultEvent{message: "Back"})
	if _, err := os.Stat(ran[3]); !os.IsNotExist(err) {
		t.Errorf("the prompt file outlived the handoff: %v", err)
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if cmd != nil {
		t.Error("a fresh answer was asked for again within the minute")
	}
}

func TestANewInstancePassesItsBranchAndRefusesABadOne(t *testing.T) {
	m := sampleModel(140, 44, ui.GlyphsSafe)
	var ran []string
	m.Binary = "/usr/local/bin/proveo"
	m.runner = func(_ string, args ...string) tea.Cmd { ran = args; return nil }
	m.startInstance("CONN-123 Connector errors", "Work on CONN-123.", "CONN-123", "https://linear.app/acme/issue/CONN-123")
	if joined := strings.Join(ran, " "); !strings.Contains(joined, "--branch CONN-123") || !strings.HasSuffix(joined, "--ticket https://linear.app/acme/issue/CONN-123") {
		t.Errorf("ran %q: the branch and the ticket ride along", ran)
	}
	_ = os.Remove(m.promptTemp)
	ran = nil
	for _, bad := range []string{"proveo/claudecode-x", "bad..name"} {
		m.startInstance("x", "y", bad, "")
		if ran != nil || !m.alert {
			t.Errorf("branch %q ran %q; status %q", bad, ran, m.status)
		}
		m.alert = false
	}
}
