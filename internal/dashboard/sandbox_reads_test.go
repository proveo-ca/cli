// SPEC: _spec/internal/dashboard/workspace-instances.puml
package dashboard

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeGuest makes t's sandbox readable and answers each script by the marker it contains.
func fakeGuest(t *testing.T, answers map[string]string) *[][]string {
	t.Helper()
	var calls [][]string
	live, run := liveGuest, guestScript
	t.Cleanup(func() { liveGuest, guestScript = live, run })
	liveGuest = func(Task) bool { return true }
	guestScript = func(_ Task, script string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		for marker, out := range answers {
			if strings.Contains(script, marker) {
				return []byte(out), nil
			}
		}
		return nil, errors.New("unexpected script")
	}
	return &calls
}

func TestFilesComeFromTheRunningSandbox(t *testing.T) {
	calls := fakeGuest(t, map[string]string{"ls-files": "src/main.go\x00README.md\x00"})
	files, source, err := TaskFiles(Task{ID: "x", Sandbox: "codex-1", Workdir: "/work/repo", Guest: true})
	if err != nil || source != "sandbox" || len(files) != 2 || files[0].Path != "README.md" {
		t.Fatalf("files %+v from %q: %v", files, source, err)
	}
	if got := (*calls)[0]; len(got) == 0 || got[0] != "/work/repo" {
		t.Errorf("the listing ran in %q, want the guest workdir", got)
	}
}

func TestFilesFallBackToTheCarriedTipNotTheHost(t *testing.T) {
	work := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", work, "-c", "user.email=t@e", "-c", "user.name=t"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(work, "carried.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "tip")
	tip := git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(work, "host-only.txt"), []byte("not in the sandbox"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, source, err := TaskFiles(Task{ID: "x", Repo: work, Tip: tip})
	if err != nil || !strings.HasPrefix(source, "carried ") || len(files) != 1 || files[0].Path != "carried.go" {
		t.Fatalf("files %+v from %q: %v", files, source, err)
	}
	if files, source, _ := TaskFiles(Task{ID: "y"}); files != nil || source != "" {
		t.Errorf("a task with no sandbox listed %+v from %q; the host checkout is not its files", files, source)
	}
}

func TestGuestChangesParseDiffUntrackedAndStatus(t *testing.T) {
	t.Parallel()
	diff := "M\x00src/a.go\x00R087\x00old.go\x00new.go\x00D\x00gone.go\x00??\x00draft.md\x00"
	got := parseGuestChanges(diff)
	want := []Change{{Kind: "D", Path: "gone.go"}, {Kind: "R087", OldPath: "old.go", Path: "new.go"},
		{Kind: "?", Path: "draft.md"}, {Kind: "M", Path: "src/a.go"}}
	slices.SortFunc(want, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	if !slices.Equal(got, want) {
		t.Errorf("diff rows = %+v\nwant %+v", got, want)
	}
	status := " M src/a.go\x00?? notes.txt\x00R  new.go\x00old.go\x00"
	got = parseGuestChanges(status)
	if len(got) != 3 || got[0].Path != "new.go" || got[0].OldPath != "old.go" || got[1].Kind != "?" || got[2].Kind != "M" {
		t.Errorf("status rows = %+v", got)
	}
}

func TestUsageProbeReadsCoresMemoryDiskAndUptime(t *testing.T) {
	t.Parallel()
	var s guestSample
	parseUsage(&s, "cpu=120.0\ncores=14\nram=9663676416\nramtotal=17179869184\ndisk=22548578304\nuptime=93600\nfresh=yes\n")
	if got := s.usageLine(); got != "CPU 1.2/14 cores · RAM 9 / 16 GiB · Disk 21 GiB · up 1d" || !s.fresh {
		t.Errorf("usage %q fresh %v", got, s.fresh)
	}
	if (guestSample{}).usageLine() != "" {
		t.Error("an empty sample must draw nothing")
	}
	if compactAge(90*time.Minute) != "1h" {
		t.Error("age")
	}
}

func TestPRsComeFromEveryWorkingBranch(t *testing.T) {
	path, run := ghPath, ghCommand
	t.Cleanup(func() { ghPath, ghCommand = path, run })
	ghPath = func() error { return nil }
	var asked []string
	ghCommand = func(_ string, _ string, args ...string) ([]byte, error) {
		asked = append(asked, strings.Join(args, " "))
		switch {
		case args[0] == "auth":
			return []byte("token"), nil
		case args[0] == "pr" && args[3] == "rs-cloud-accounts-ui":
			return []byte(`[{"number":9,"state":"OPEN","isCrossRepository":true,"headRefName":"rs-cloud-accounts-ui"},` +
				`{"number":3975,"url":"u","state":"OPEN","reviewDecision":"APPROVED","headRefName":"rs-cloud-accounts-ui","createdAt":"2026-11-24T10:00:00Z"}]`), nil
		case args[0] == "pr" && args[3] == "rs-cloud-revoke-reach":
			return []byte(`[{"number":3974,"state":"OPEN","isDraft":true,"headRefName":"rs-cloud-revoke-reach"}]`), nil
		case args[0] == "pr":
			return []byte(`[]`), nil
		}
		return nil, errors.New("unexpected")
	}
	prs, note := findPRs("/repo", []string{"rs-cloud-accounts-ui", "rs-cloud-revoke-reach", "proveo/codex-1"})
	if note != "" || len(prs) != 2 || prs[0].Number != 3975 || prs[1].Number != 3974 {
		t.Fatalf("prs = %+v %q; a fork PR must not win and every branch keeps its PR", prs, note)
	}
	if prs[0].label() != "#3975 · approved" || prs[0].Created.IsZero() || prs[1].label() != "#3974 · draft" {
		t.Errorf("labels = %q, %q; created %v", prs[0].label(), prs[1].label(), prs[0].Created)
	}
	if slices.ContainsFunc(asked, func(a string) bool { return strings.HasPrefix(a, "api") }) {
		t.Errorf("asked %q; a commit lookup misattributes shared commits", asked)
	}
	ghPath = func() error { return errors.New("no gh") }
	if _, note := findPRs("/repo", []string{"x"}); note != "install gh for PR status" {
		t.Errorf("no gh = %q", note)
	}
}

func TestPRsLookAtTheAgentWorktreesFirst(t *testing.T) {
	fakeGuest(t, map[string]string{"/proc/": "work\t/w/dev\trs-cloud-accounts-ui\tabc\n" +
		"nested\t/w/dev/.claude/worktrees/reach\trs-cloud-revoke-reach\tdef\n"})
	path, run := ghPath, ghCommand
	t.Cleanup(func() { ghPath, ghCommand = path, run })
	ghPath = func() error { return nil }
	var heads []string
	ghCommand = func(_ string, _ string, args ...string) ([]byte, error) {
		if args[0] == "pr" {
			heads = append(heads, args[3])
		}
		return []byte(`[]`), nil
	}
	taskPRs(Task{ID: "x", Sandbox: "codex-1", Workdir: "/w", Guest: true, Branch: "proveo/codex-1", Repo: "/repo"}, "/cwd")
	if want := []string{"rs-cloud-accounts-ui", "rs-cloud-revoke-reach", "proveo/codex-1"}; !slices.Equal(heads, want) {
		t.Errorf("heads asked = %q, want %q", heads, want)
	}
}

func TestGuestWorkKeepsTheAgentCheckoutThenItsWorktrees(t *testing.T) {
	out := "work\t/w/dev\trs-cloud-accounts-ui\tabc\n" +
		"nested\t/w/dev/.claude/worktrees/reach\trs-cloud-revoke-reach\tdef\n" +
		"work\t/w/monorepo\tHEAD\t123\n" +
		"nested\t/w/dev/.claude/worktrees/reach\tdup\tdef\n" +
		"garbage line\n"
	work := parseGuestWork(out)
	if len(work) != 3 || work[0].path != "/w/dev" || !work[1].nested || work[2].branch != "HEAD" {
		t.Fatalf("work = %+v", work)
	}
	if got := branches(work); !slices.Equal(got, []string{"rs-cloud-accounts-ui", "rs-cloud-revoke-reach"}) {
		t.Errorf("branches = %q; a detached HEAD names no branch", got)
	}
}

func TestLinearRefsComeFromThePRText(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		title, branch, body string
		want                []string
	}{
		{"TopHat: Add Ashby and Tipalti connectors", "rs-ashby-tipalti",
			"TopHat needs recruiting data. Requested in CONN-116 and CONN-114.", []string{"CONN-116", "CONN-114"}},
		{"Speed up sync", "main", "Fixes ENG-42\nSee https://linear.app/pluvo/issue/OPS-7/flaky", []string{"OPS-7", "ENG-42"}},
		{"[PRV-284] Persist history", "rob/prv-284-history", "", []string{"PRV-284"}},
		{"Bump to UTF-8 and SHA-256", "feat/abc-12-x", "Uses ISO-8601 dates", []string{"ABC-12"}},
	} {
		if got := linearRefs(c.title, c.branch, c.body); !slices.Equal(got, c.want) {
			t.Errorf("linearRefs(%q, %q) = %q, want %q", c.title, c.branch, got, c.want)
		}
	}
}

func TestThePlanComesFromThePlanLineFirst(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		body  string
		files []string
		want  string
	}{
		{"## How\nStuff.\n\nPlan: _spec/_plans/connectors.md", nil, "_spec/_plans/connectors.md"},
		{"**Plan:** [rollout](https://github.com/o/r/blob/main/docs/rollout.md)", nil, "https://github.com/o/r/blob/main/docs/rollout.md"},
		{"Plan: `docs/rollout.md`", nil, "docs/rollout.md"},
		{"Follows _spec/_plans/history.puml closely.", nil, "_spec/_plans/history.puml"},
		{"No plan named here.", []string{"apps/web/x.ts", "_spec/_plans/queue.md"}, "_spec/_plans/queue.md"},
		{"No plan named here.", []string{"README.md"}, ""},
	} {
		if got := planRef(c.body, c.files); got != c.want {
			t.Errorf("planRef(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}
