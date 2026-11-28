// SPEC: _spec/internal/dashboard/workspace-instances.puml
package sandbox

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/proveo-ca/proveo/internal/sbx"
	"github.com/proveo-ca/proveo/internal/workspace"
)

func TestAnInstanceKeepsItsLinkedSharesWritableAndEverythingElseReadOnly(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo, spec, deps := filepath.Join(root, "repo"), filepath.Join(root, "Syncd", "_spec"), filepath.Join(root, "deps")
	in := Input{
		Instance:     &Instance{Workspace: repo, Repo: ""},
		InstanceWork: filepath.Join(root, "work"),
		HomeRoot:     filepath.Join(root, "home"), RunDir: filepath.Join(root, "run"),
		Links: []workspace.Link{{Rel: "_spec", Target: spec, Action: workspace.LinkMounted}},
	}
	mounts := []sbx.Mount{{Host: repo}, {Host: spec}, {Host: deps}}
	mode := func(out []sbx.Mount, host string) string {
		for _, m := range out {
			if m.Host == host {
				if m.ReadOnly {
					return "ro"
				}
				return "rw"
			}
		}
		return "absent"
	}
	out := in.instanceMounts(mounts)
	if got := mode(out, spec); got != "rw" {
		t.Errorf("_spec is %s; the seed needs it writable, or every new instance exits 1", got)
	}
	if got := mode(out, deps); got != "ro" {
		t.Errorf("a shared host folder that is no link is %s; one writer stays the rule", got)
	}
	if got := mode(out, repo); got != "absent" {
		t.Errorf("the workspace source is %s; the instance mounts its own copy", got)
	}
	in.ReadOnlyShares = true
	if got := mode(in.instanceMounts(mounts), spec); got != "ro" {
		t.Errorf("a review's _spec is %s; --read-only-shares keeps it read-only", got)
	}
}

func TestTheInstanceCheckoutKeepsProveoBranchesLocal(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "base")
	base := git("rev-parse", "HEAD")
	for branch, remote := range map[string]string{"proveo/claudecode-feat-integ-26d02feee4": ".", "CONN-123": ""} {
		out, err := exec.Command("sh", "-ec", instanceCheckoutScript, "prepare-instance", repo, base, branch, "", "").CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", branch, err, out)
		}
		got, _ := exec.Command("git", "-C", repo, "config", "branch."+branch+".remote").Output()
		if strings.TrimSpace(string(got)) != remote {
			t.Errorf("%s: upstream remote %q, want %q", branch, got, remote)
		}
	}
	if err := ValidBranch("CONN-123"); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"proveo/x", "bad..name", "with space"} {
		if ValidBranch(bad) == nil {
			t.Errorf("%q passed as a branch", bad)
		}
	}
	if (&Instance{Name: "claudecode-x"}).Branch() != "proveo/claudecode-x" || (&Instance{Name: "x", WorkBranch: "CONN-123"}).Branch() != "CONN-123" {
		t.Error("Branch does not prefer the chosen working branch")
	}
}

func TestAReadOnlyRunTurnsGitSyncOff(t *testing.T) {
	has := func(env []string) bool { return slices.Contains(env, GitSyncVar+"=off") }
	if has(launchEnv(Input{}, "claudecode", nil, nil)) {
		t.Error("an ordinary run turned git sync off")
	}
	if !has(launchEnv(Input{ReadOnlyShares: true}, "claudecode", nil, nil)) {
		t.Error("a review run still commits and pushes each turn")
	}
}

func TestAChosenBranchStartsFromItsTipOnOrigin(t *testing.T) {
	root := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	origin, work := filepath.Join(root, "origin"), filepath.Join(root, "work")
	run(root, "init", "-q", "--bare", "-b", "main", origin)
	run(root, "clone", "-q", origin, work)
	run(work, "commit", "-q", "--allow-empty", "-m", "base")
	run(work, "push", "-q", "origin", "HEAD:main")
	run(work, "switch", "-q", "-c", "conn-30")
	run(work, "commit", "-q", "--allow-empty", "-m", "teamwork")
	tip := run(work, "rev-parse", "HEAD")
	run(work, "push", "-q", "origin", "conn-30")
	run(work, "switch", "-q", "main")
	run(work, "branch", "-q", "-D", "conn-30")
	if got := remoteBranchTip(work, "conn-30"); got != tip {
		t.Errorf("tip = %q, want the PR branch's %q", got, tip)
	}
	if got := remoteBranchTip(work, "CONN-999"); got != "" {
		t.Errorf("a branch origin lacks has tip %q", got)
	}
}
