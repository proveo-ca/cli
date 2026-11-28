// SPEC: _spec/internal/sbx/startup-dispatch.puml
package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/proveo-ca/proveo/internal/sbx"
)

// reviewShapedInput is a new isolated clone instance with a task label, as `proveo run --new --task` makes.
func reviewShapedInput(t *testing.T) Input {
	t.Helper()
	repo := t.TempDir()
	return Input{Target: "claudecode", Clone: true, RepoRoot: repo, InstanceCloneRoot: repo, Lookup: func(string) string { return "" },
		Instance: &Instance{Name: "claudecode-review-399-2f53ad8f51", ID: "2f53ad8f51aa", Repo: repo, Workspace: repo,
			Task: "Review #3995: PLUV-1747: Hold E2E and image builds on draft PRs", Target: "claudecode",
			Base: strings.Repeat("a", 40)}}
}

func TestEveryStartupHookSurvivesTheKitsYAMLRoundTrip(t *testing.T) {
	in := reviewShapedInput(t)
	for _, c := range startupCommands(in, false) {
		b, err := yaml.Marshal(sbx.LifecycleV3{Startup: []sbx.HookV3{{Command: c.Command, Description: c.Description}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(b, &map[string]any{}); err != nil {
			t.Errorf("%q does not read back (%v): a script that opens on a blank or indented line breaks yaml.v3\n%s", c.Description, err, b)
		}
	}
}

func TestANewInstanceKitCarriesTheSeedAndNoMergeError(t *testing.T) {
	_, w, _ := Spec(reviewShapedInput(t))
	if w.Err != nil {
		t.Fatalf("the kit for a new instance did not merge: %v", w.Err)
	}
	b, err := yaml.Marshal(w.Descriptor)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"proveo-seed", "prepare the instance branch", "com.docker.sandbox/lifecycle@1"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the kit lacks %q; without the seed the agent is never released:\n%s", want, b)
		}
	}
}

func TestAReadOnlyRunDeclaresItsSpecShareReadOnly(t *testing.T) {
	wd := t.TempDir()
	if err := os.Mkdir(filepath.Join(wd, "_spec"), 0o755); err != nil {
		t.Fatal(err)
	}
	binds := []sbx.Mount{{Host: wd}}
	in := Input{Clone: true, RepoRoot: wd}
	if got := shares(in, binds); got != "_spec=rw" {
		t.Errorf("a clone run's shares = %q, want _spec=rw", got)
	}
	in.ReadOnlyShares = true
	if got := shares(in, binds); got != "_spec=ro" {
		t.Errorf("a review's shares = %q; the seed must not refuse a read-only _spec", got)
	}
}

func TestARunRecordsTheTmuxSessionItsAgentLivesIn(t *testing.T) {
	root := t.TempDir()
	old := receiptDir
	t.Cleanup(func() { receiptDir = old })
	receiptDir = func() string { return root }
	t.Setenv(RunSessionVar, "")
	recordSession("proveo-claudecode-1b3fcb7f")
	if got := SessionOf("proveo-claudecode-1b3fcb7f"); got != "" {
		t.Errorf("a run outside a session recorded %q", got)
	}
	t.Setenv(RunSessionVar, "proveo-run-claudecode-abc123")
	recordSession("proveo-claudecode-1b3fcb7f")
	if got := SessionOf("proveo-claudecode-1b3fcb7f"); got != "proveo-run-claudecode-abc123" {
		t.Errorf("session = %q", got)
	}
	t.Setenv(RunSessionVar, "proveo-run-claudecode-def456")
	recordSession("proveo-claudecode-1b3fcb7f")
	if got := SessionOf("proveo-claudecode-1b3fcb7f"); got != "proveo-run-claudecode-def456" {
		t.Errorf("a reused sandbox kept the old session: %q", got)
	}
}
