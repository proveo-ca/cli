// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package dashboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/proveo-ca/proveo/internal/backend/sandbox"
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
	changes, err := Changes(task)
	if err != nil || len(changes) != 2 {
		t.Fatalf("changes = %+v, %v", changes, err)
	}
	for _, c := range changes {
		got, err := Preview(work, task, c, false)
		if err != nil {
			t.Fatal(err)
		}
		switch c.Kind {
		case "D":
			if got != "only on base" {
				t.Errorf("deleted preview = %q", got)
			}
		case "R100":
			patch, err := Preview(work, task, c, true)
			if got != "renamed content" || err != nil || !strings.Contains(patch, "rename from old name.txt") {
				t.Errorf("renamed preview = %q, patch = %q, error = %v", got, patch, err)
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
