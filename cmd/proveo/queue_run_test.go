// SPEC: _spec/internal/dashboard/workspace-instances.puml
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/proveo-ca/proveo/internal/schedule"
)

func TestAwaitAdmissionReturnsOnceTheSessionIsUp(t *testing.T) {
	orig := tmuxHasSession
	t.Cleanup(func() { tmuxHasSession = orig })
	polls := 0
	tmuxHasSession = func(name string) bool { polls++; return name == schedule.SessionName("codex-1a2b") && polls > 2 }
	if why, up := awaitAdmission(t.TempDir(), "codex-1a2b", 5*time.Second); !up {
		t.Fatalf("not attached: %s", why)
	}
}

func TestAwaitAdmissionHandsBackAJobStillQueued(t *testing.T) {
	orig := tmuxHasSession
	t.Cleanup(func() { tmuxHasSession = orig })
	tmuxHasSession = func(string) bool { return false }
	home := t.TempDir()
	name, j, err := schedule.NewAttendedJob(home, t.TempDir(), "codex", "", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := schedule.Submit(home, name, "dashboard", j, time.Now()); err != nil {
		t.Fatal(err)
	}
	why, up := awaitAdmission(home, name, 300*time.Millisecond)
	if up || !strings.HasPrefix(why, "still queued") {
		t.Errorf("awaitAdmission = %q, %v; want a still-queued hand-back", why, up)
	}
}

func TestAPromptFileNeedsTheQueueAndIsHidden(t *testing.T) {
	root := rootCmd()
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"run", "claudecode", "--prompt-file", filepath.Join(t.TempDir(), "p.md")})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--prompt-file requires --queue") {
		t.Errorf("err = %v", err)
	}
	run, _, err := rootCmd().Find([]string{"run"})
	if err != nil {
		t.Fatal(err)
	}
	if f := run.Flags().Lookup("prompt-file"); f == nil || !f.Hidden {
		t.Error("--prompt-file is missing, or shown in help")
	}
}

func TestAQueuedJobKeepsItsOpeningPrompt(t *testing.T) {
	home := t.TempDir()
	_, j, err := schedule.NewAttendedJob(home, t.TempDir(), "claudecode", "OPS-7 Rotate keys", nil, "Fix OPS-7: Rotate keys", "OPS-7")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(j.PromptFile); err != nil || string(b) != "Fix OPS-7: Rotate keys" || j.Task != "OPS-7 Rotate keys" || j.Ticket != "OPS-7" {
		t.Errorf("prompt %q (%v), task %q, ticket %q", b, err, j.Task, j.Ticket)
	}
}

func TestDropCanStopALoneInstancesRunningSandbox(t *testing.T) {
	drop, _, err := rootCmd().Find([]string{"schedule", "drop"})
	if err != nil || drop.Name() != "drop" {
		t.Fatalf("schedule drop = %v, %v", drop, err)
	}
	if f := drop.Flags().Lookup("stop"); f == nil || f.DefValue != "false" {
		t.Error("drop lacks --stop, so a lone instance whose sandbox still runs cannot be cleaned up")
	}
}

func TestABranchNeedsANewInstanceAndAValidName(t *testing.T) {
	for args, want := range map[string]string{
		"run claudecode --branch CONN-123":                  "--branch requires --new",
		"run claudecode --new --branch proveo/claudecode-x": "proveo/ is kept for instance branches",
		"run claudecode --new --branch bad..name":           "is not a valid branch name",
	} {
		root := rootCmd()
		var out strings.Builder
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(strings.Fields(args))
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want %q", args, err, want)
		}
	}
}
