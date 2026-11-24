// SPEC: _spec/_experiments/dashboard/resource-queue.puml
package schedule

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQueueRunsOnCapacityAndAcceptedDependencies(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, "prompt")
	if err := os.WriteFile(path, []byte("opening prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := Job{Target: "codex", PromptFile: path}
	second := first
	second.DependsOn = []string{"first"}
	jobs := Config{Jobs: map[string]Job{"first": first, "second": second}}
	for _, name := range []string{"first", "second"} {
		if err := Submit(home, name, "manual", jobs.Jobs[name], time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte("edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	space := false
	var launches []string
	ops := QueueOps{
		Alive: func(string) bool { return false },
		Result: func(name string) *Result {
			if name == "first" && len(launches) > 0 {
				return &Result{Started: time.Now(), Finished: time.Now(), Outcome: "done"}
			}
			return nil
		},
		Exit: func(QueueItem) (int, bool) { return 0, false },
		Admit: func(QueueItem) error {
			if !space {
				return errors.New("disk full")
			}
			return nil
		},
		Launch: func(q QueueItem) error {
			b, err := os.ReadFile(q.Job.PromptFile)
			if err != nil || string(b) != "opening prompt" {
				t.Errorf("submitted prompt = %q, %v", b, err)
			}
			launches = append(launches, q.Name)
			return nil
		},
	}
	dispatch := func() {
		t.Helper()
		if _, err := Dispatch(home, jobs, ops); err != nil {
			t.Fatal(err)
		}
	}
	dispatch()
	if len(launches) != 0 {
		t.Errorf("launches while full = %v", launches)
	}
	space = true
	dispatch()
	dispatch()
	if len(launches) != 1 || launches[0] != "first" {
		t.Errorf("launches before acceptance = %v, want first", launches)
	}
	if err := Accept(home, "first"); err != nil {
		t.Fatal(err)
	}
	dispatch()
	if len(launches) != 2 || launches[1] != "second" {
		t.Errorf("launches after acceptance = %v, want second", launches)
	}
}

func TestPhaseRequiresEvidenceForCarriedRevision(t *testing.T) {
	home := t.TempDir()
	prompt := filepath.Join(home, "prompt")
	if err := os.WriteFile(prompt, []byte("prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Submit(home, "review", "manual", Job{Target: "codex", PromptFile: prompt}, time.Now()); err != nil {
		t.Fatal(err)
	}
	one, two := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, phase := range []string{"Verification", "Live verification", "Upgrade", "PR", "Review"} {
		if err := UpdatePhase(home, "review", phase, one); err != nil {
			t.Fatal(err)
		}
	}
	if err := UpdatePhase(home, "review", "Merge", two); err == nil {
		t.Fatal("merged a revision without its own verification")
	}
	if err := UpdatePhase(home, "review", "Verification", two); err != nil {
		t.Fatal(err)
	}
	queue, err := ReadQueue(home)
	if err != nil || len(queue) != 1 || queue[0].Revision != two || queue[0].Job.Phase != "Verification" {
		t.Fatalf("revision state = %+v (%v)", queue, err)
	}
}
