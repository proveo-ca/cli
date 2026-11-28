// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package schedule

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAttendedSessionTypesNothingWithoutAPromptAndEndsDone(t *testing.T) {
	fastWatch(t)
	home := t.TempDir()
	prompt := filepath.Join(home, "p")
	if err := os.WriteFile(prompt, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	polls := 0
	f := &fakeTmux{pane: "❯ ", alive: true, gone: func() bool { polls++; return polls > 3 }}
	res := Watch(home, "codex-1a2b", "dashboard", "t.log", Job{Target: "codex", New: true, Mode: ModeAttended, PromptFile: prompt}, f.run, nil)
	if len(f.typed) != 0 || len(f.keys) != 0 || f.killed {
		t.Errorf("an attended session is the operator's: typed %q keys %q killed %v", f.typed, f.keys, f.killed)
	}
	if res.Outcome != "done" || !strings.HasPrefix(res.Detail, "attended session closed") {
		t.Errorf("result %q: %s", res.Outcome, res.Detail)
	}
}

func TestAttendedSessionTypesAnOpeningPromptOncePlain(t *testing.T) {
	fastWatch(t)
	home := t.TempDir()
	prompt := filepath.Join(home, "p")
	if err := os.WriteFile(prompt, []byte("Check the\nthree branches."), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeTmux{pane: "❯ ", alive: true, endOn: "Check"}
	res := Watch(home, "codex-1a2b", "dashboard", "t.log", Job{Target: "codex", New: true, Mode: ModeAttended, PromptFile: prompt}, f.run, nil)
	if !slices.Equal(f.typed, []string{"Check the three branches."}) {
		t.Errorf("typed %q: want the prompt once, with no /goal, clock or report path", f.typed)
	}
	if res.Outcome != "done" {
		t.Errorf("outcome %q: %s", res.Outcome, res.Detail)
	}
}

func TestNewAttendedJobSnapshotsTheCardFlags(t *testing.T) {
	t.Parallel()
	home, cwd := t.TempDir(), t.TempDir()
	name, j, err := NewAttendedJob(home, cwd, "codex", "", []string{"--scope", "apps/web"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, "codex-") || j.Task != name || j.Mode != ModeAttended || !j.New {
		t.Fatalf("job %s = %+v", name, j)
	}
	var stored Job
	b, err := os.ReadFile(filepath.Join(home, "dashboard-jobs", name, "job.json"))
	if err != nil || json.Unmarshal(b, &stored) != nil || !slices.Equal(stored.Args, []string{"--scope", "apps/web"}) {
		t.Fatalf("job.json %s: %v", b, err)
	}
	argv := j.RunArgv("/bin/proveo")
	if !slices.Contains(argv, "--yes") || !slices.Contains(argv, "--new") || argv[len(argv)-1] != "apps/web" {
		t.Errorf("argv %q: the scheduled run replays the card with --yes and repeats the flags", argv)
	}
}

func TestAStartedWatcherIsReaped(t *testing.T) {
	t.Parallel()
	c := exec.Command("sh", "-c", "exit 0")
	done, err := startReaped(c)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the exited watcher was never waited for, so it lingers as a zombie")
	}
	if c.ProcessState == nil || !c.ProcessState.Exited() {
		t.Errorf("process state %v", c.ProcessState)
	}
}
