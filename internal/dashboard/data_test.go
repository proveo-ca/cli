// SPEC: _spec/internal/dashboard/workspace-instances.puml
package dashboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/proveo-ca/proveo/internal/backend/sandbox"
	"github.com/proveo-ca/proveo/internal/sbx"
	"github.com/proveo-ca/proveo/internal/schedule"
)

func TestChangesPreviewRenameAndDeletion(t *testing.T) {
	work := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(work, path), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	write("delete.txt", "only on base")
	write("old name.txt", "renamed content")
	run("add", "--all")
	run("commit", "-qm", "base")
	base := run("rev-parse", "HEAD")
	run("rm", "-q", "delete.txt")
	run("mv", "old name.txt", "new name.txt")
	run("commit", "-qm", "carried")
	task := Task{Base: base, Tip: run("rev-parse", "HEAD"), Repo: work}
	set, err := Changes(task, "", "")
	changes := set.Rows
	if err != nil || len(changes) != 2 || set.Base != "launch" || set.MergeBase != base {
		t.Fatalf("changes = %+v, %v", set, err)
	}
	for _, c := range changes {
		got, err := ReadSides(task, c, set.MergeBase)
		if err != nil {
			t.Fatal(err)
		}
		switch c.Kind {
		case "D":
			if got.Before != "only on base" || got.After != "" {
				t.Errorf("deleted sides = %+v", got)
			}
		case "R100":
			if got.Before != "renamed content" || got.After != "renamed content" {
				t.Errorf("renamed sides = %+v; the base side reads the old name", got)
			}
		default:
			t.Errorf("unexpected change %+v", c)
		}
	}
}

func TestDashboardLinksInstanceByJobKey(t *testing.T) {
	instances := []sandbox.Instance{
		{ID: "legacy", Target: "codex", Task: "same title"},
		{ID: "one", Target: "codex", Task: "same title", Job: "night-one"},
		{ID: "two", Target: "codex", Task: "same title", Job: "night-two"},
	}
	job := schedule.Job{Task: "same title", Target: "codex"}
	if index := matchingInstance(instances, nil, "night-two", job); index != 2 {
		t.Fatalf("match for night-two = %d", index)
	}
	if index := matchingInstance(instances, nil, "night-three", job); index != -1 {
		t.Fatalf("match for unrelated job = %d", index)
	}
	job.Instance = "legacy"
	if index := matchingInstance(instances, nil, "night-two", job); index != 0 {
		t.Fatalf("match for explicit legacy selector = %d", index)
	}
}

func TestDashboardDiscoversRunningWorkspaceWithoutClaimingTheSandbox(t *testing.T) {
	work, other := t.TempDir(), t.TempDir()
	work, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	rows := []sbx.Listed{
		{ID: "raw", Name: "codex-prv-284", Status: "running", Workspaces: []string{work + ":ro"}},
		{ID: "managed", Name: "codex-managed", Status: "running", Workspaces: []string{work}},
		{ID: "other", Name: "codex-other", Status: "running", Workspaces: []string{other, work}},
		{ID: "stopped", Status: "stopped", Workspaces: []string{work}},
	}
	got := discoveredSandboxes(work, []sandbox.Instance{{VMID: "managed"}}, rows)
	host, _ := os.Hostname()
	if len(got) != 1 || got[0].Sandbox != "codex-prv-284" || got[0].Live || got[0].Instance != "" {
		t.Fatalf("discovery = %+v", got)
	}
	if !got[0].Guest || got[0].Host != host || got[0].Workdir != work {
		t.Errorf("a running discovered sandbox must be readable for usage and files: %+v", got[0])
	}
	if id := linearIdentifier(got[0]); id != "PRV-284" {
		t.Errorf("Linear key = %q", id)
	}
}

func TestTaskGraphFoldsChainAndKeepsBlocker(t *testing.T) {
	tasks := []Task{
		{ID: "a", Title: "First task"},
		{ID: "b", Title: "Second task", DependsOn: []string{"a"}},
		{ID: "c", Title: "Third task", DependsOn: []string{"b"}},
		{ID: "d", Title: "Fourth task", DependsOn: []string{"c"}},
		{ID: "e", Title: "Fifth task", DependsOn: []string{"d"}, State: schedule.Queued, Reason: "blocked by d"},
	}
	m := dashModel{}
	line := m.graphLine(newTaskGraph(tasks), tasks[4], 36)
	if !strings.Contains(line, "3 tasks") || !strings.Contains(line, "blocked by d") {
		t.Errorf("folded graph line = %q", line)
	}
}

func TestAQueuedRunTakesTheOutcomeOfTheResultThatEndedIt(t *testing.T) {
	submitted := time.Date(2026, 11, 26, 9, 16, 34, 0, time.UTC)
	failed := &schedule.Result{Outcome: "failed", Detail: "the agent exited 1 before it was ready", Finished: submitted.Add(2 * time.Minute)}
	queued := Task{State: schedule.Attention, Reason: "the agent exited 1 before it was ready: …", Updated: submitted}
	if got := withResult(queued, failed); got.Outcome != "failed" || got.Reason != queued.Reason || !got.Updated.Equal(submitted) {
		t.Errorf("queued attention task = %+v; it takes only the outcome", got)
	}
	if got := withResult(queued, &schedule.Result{Outcome: "failed", Finished: submitted.Add(-time.Hour)}); got.Outcome != "" {
		t.Errorf("an older run's result marked the queued one %q", got.Outcome)
	}
	if got := withResult(Task{State: schedule.Queued}, failed); got.State != schedule.Attention || got.Outcome != "failed" || got.Reason != failed.Detail {
		t.Errorf("a job with no queue entry = %+v", got)
	}
	if got := withResult(Task{State: schedule.Running, Live: true}, failed); got.Outcome != "" {
		t.Error("a live run took a finished result")
	}
}
