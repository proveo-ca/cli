// SPEC: _spec/internal/dashboard/workspace-instances.puml
package dashboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/proveo-ca/proveo/internal/testx"
	"github.com/proveo-ca/proveo/internal/ui"
)

// branchedRepo is main (base.txt), dev from main (+dev.txt) and a feature from dev (+feat.go, +feat2.go),
// with origin/main and origin/dev published and the feature as HEAD.
func branchedRepo(t *testing.T) (work, mainSHA, devSHA, tip string) {
	t.Helper()
	work = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", work, "-c", "user.email=t@e", "-c", "user.name=t"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(name string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(work, name), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-qm", name)
		return git("rev-parse", "HEAD")
	}
	git("init", "-q", "-b", "main")
	mainSHA = commit("base.txt")
	git("checkout", "-qb", "dev")
	devSHA = commit("dev.txt")
	git("checkout", "-qb", "feature")
	commit("feat.go")
	tip = commit("feat2.go")
	git("update-ref", "refs/remotes/origin/main", mainSHA)
	git("update-ref", "refs/remotes/origin/dev", devSHA)
	git("update-ref", "refs/remotes/origin/feature", tip)
	return work, mainSHA, devSHA, tip
}

func paths(rows []Change) []string {
	var out []string
	for _, c := range rows {
		out = append(out, c.Kind+" "+c.Path)
	}
	return out
}

func TestCarriedChangesDiffFromTheNearestBase(t *testing.T) {
	t.Parallel()
	work, mainSHA, devSHA, tip := branchedRepo(t)
	task := Task{ID: "x", Repo: work, Tip: tip, Branch: "feature", Base: mainSHA}
	set, err := Changes(task, "", "")
	if err != nil || set.Base != "origin/dev" || set.MergeBase != devSHA || !slices.Equal(paths(set.Rows), []string{"A feat.go", "A feat2.go"}) {
		t.Fatalf("nearest base: %+v, %v; want feat.go and feat2.go against origin/dev", set, err)
	}
	set, err = Changes(task, "main", "")
	if err != nil || set.Base != "origin/main" || !slices.Contains(paths(set.Rows), "A dev.txt") {
		t.Errorf("a PR into main measures against origin/main: %+v, %v", set, err)
	}
}

func TestGuestChangesScriptPicksTheNearestBase(t *testing.T) {
	if testx.InLinux(t) {
		return
	}
	work, _, devSHA, _ := branchedRepo(t)
	if err := os.WriteFile(filepath.Join(work, "feat.go"), []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "notes.md"), []byte("draft\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", guestChangesScript, "sh", work, "", "", "origin/feature", "origin/main", "origin/dev").Output()
	if err != nil {
		t.Fatal(err)
	}
	header, body, _ := strings.Cut(string(out), "\n")
	if header != "base\torigin/dev\t"+devSHA {
		t.Errorf("header %q: the branch's own upstream is skipped and origin/dev is nearest", header)
	}
	if got := paths(parseGuestChanges(body)); !slices.Equal(got, []string{"A feat.go", "A feat2.go", "? notes.md"}) {
		t.Errorf("guest rows %q: committed work, uncommitted edits and untracked files all count", got)
	}
}

func TestGuestChangesReadTheBaseHeader(t *testing.T) {
	fakeGuest(t, map[string]string{"merge-base": "base\torigin/dev\tabc123\nM\x00src/a.go\x00??\x00notes.md\x00"})
	set, err := Changes(Task{ID: "x", Sandbox: "codex-1", Workdir: "/w", Guest: true}, "", "")
	if err != nil || set.Base != "origin/dev" || set.MergeBase != "abc123" || !set.Live || len(set.Rows) != 2 {
		t.Fatalf("set %+v, %v", set, err)
	}
}

func TestAPRBaseRereadsTheChanges(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	task := m.task()
	m.changesBase = "origin/dev"
	_, cmd := m.Update(taskMetadata{id: task.ID, key: metadataKey(task), pr: prInfo{Number: 7, Base: "main"}})
	if cmd == nil {
		t.Error("a PR into main must re-measure changes that were read against origin/dev")
	}
	m.changesBase = "origin/main"
	for _, other := range m.data.Tasks {
		m.metadata[metadataKey(other)] = taskMetadata{id: other.ID, key: metadataKey(other), at: time.Now()}
	}
	if _, cmd := m.Update(taskMetadata{id: task.ID, key: metadataKey(task), at: time.Now(), pr: prInfo{Number: 7, Base: "main"}}); cmd != nil {
		t.Error("the same base must not re-read")
	}
}
