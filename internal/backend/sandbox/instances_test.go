// SPEC: _spec/internal/sbx/instance-lifecycle.puml, _spec/internal/sbx/clone-workspace.puml
package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/proveo-ca/proveo/internal/sbx"
)

func instanceInventory(t *testing.T, state string, rows func() ([]sbx.Listed, bool)) {
	t.Helper()
	oldList, oldDir := listSandboxes, receiptDir
	t.Cleanup(func() { listSandboxes, receiptDir = oldList, oldDir })
	listSandboxes = rows
	receiptDir = func() string { return state }
}

func TestThreeInstancesInOneWorkspace(t *testing.T) {
	state, work := t.TempDir(), t.TempDir()
	instanceInventory(t, state, func() ([]sbx.Listed, bool) {
		return []sbx.Listed{{Name: "unrelated", ID: "vm-unrelated"}}, true
	})
	req := InstanceRequest{State: state, Target: "claudecode", Workspace: work,
		New: true, Task: "PRV-284 Verify Something With A Long Task Name"}
	var entries [3]*Instance
	var group sync.WaitGroup
	for n := range entries {
		group.Go(func() {
			i, err := SelectInstance(req)
			if err != nil {
				t.Errorf("allocate instance: %v", err)
			}
			entries[n] = i
		})
	}
	group.Wait()
	req.New, req.Task = false, ""
	if got, err := SelectInstance(req); err == nil {
		t.Errorf("SelectInstance without a selector = %v, want ambiguity error", got)
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		if ids[entry.ID] || names[entry.Name] || len(entry.Name) > 32 ||
			!strings.HasPrefix(entry.Name, "claudecode-prv-284") {
			t.Errorf("invalid or reused instance identity: %+v", entry)
		}
		ids[entry.ID], names[entry.Name] = true, true
		req.ID = entry.ID
		got, err := SelectInstance(req)
		if err != nil || got == nil || got.Name != entry.Name {
			t.Errorf("SelectInstance(%q) = %v, %v, want %s", entry.ID, got, err, entry.Name)
		}
	}
}

func TestLegacyInstanceBindsOnlyVerifiedIdleSandbox(t *testing.T) {
	state, work := t.TempDir(), t.TempDir()
	name := sbx.SandboxName("codex", work)
	row := sbx.Listed{Name: name, Agent: "proveo-codex", ID: "vm-1", Status: "stopped", Workspaces: []string{work}}
	known := true
	instanceInventory(t, state, func() ([]sbx.Listed, bool) { return []sbx.Listed{row}, known })
	req := InstanceRequest{State: state, Target: "codex", Workspace: work, LegacyName: name}
	i, err := SelectInstance(req)
	if err != nil {
		t.Fatal(err)
	}
	if release, err := i.Begin(row.Agent, work); err == nil {
		release()
		t.Fatal("adopted a legacy sandbox without a receipt")
	}
	if err := writeReceipt(name, launchReceipt{Agent: row.Agent, Run: "proveo-1-2", Mounts: []string{work + "::false"}}); err != nil {
		t.Fatal(err)
	}
	release, err := i.Begin(row.Agent, work)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	if !i.Legacy || i.VMID != "vm-1" {
		t.Errorf("migrated legacy record = %+v", i)
	}
	if other, err := i.Begin(row.Agent, work); err == nil {
		other()
		t.Error("second run acquired the same owner lease")
	}
	release()
	for _, tc := range []struct {
		name, id, status string
		known            bool
	}{
		{name: "different VM", id: "vm-other", status: "stopped", known: true},
		{name: "external owner", id: "vm-1", status: "running", known: true},
		{name: "unavailable inventory", id: "vm-1", status: "stopped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row.ID, row.Status, known = tc.id, tc.status, tc.known
			if one, err := i.Begin(row.Agent, work); err == nil {
				one()
				t.Errorf("Begin accepted %q", tc.name)
			}
		})
	}
}

func TestInstanceBundleSuppliesRecordedBase(t *testing.T) {
	t.Parallel()
	source, guest, state := t.TempDir(), t.TempDir(), t.TempDir()
	git := func(dir string, argv ...string) string {
		t.Helper()
		base := []string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@proveo.test"}
		out, err := exec.Command("git", append(base, argv...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", argv, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(source, "init", "-q")
	git(source, "commit", "-q", "--allow-empty", "-m", "recorded base")
	base := git(source, "rev-parse", "HEAD")
	i := &Instance{ID: "a1", Name: "codex-test-a1", Workspace: source, Repo: source, Base: base,
		dir: instanceDir(state, "codex-test-a1")}
	git(source, "commit", "-q", "--allow-empty", "-m", "later commit")
	git(guest, "init", "-q")
	git(guest, "commit", "-q", "--allow-empty", "-m", "different base")
	_, bundle, err := i.PrepareWorkspace("", false)
	if err != nil {
		t.Fatal(err)
	}
	in := Input{Instance: i, InstanceCloneRoot: guest, InstanceBundle: bundle}
	hook := in.instanceCheckout().Command
	if out, err := exec.Command(hook[0], hook[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("checkout instance branch: %v\n%s", err, out)
	}
	if got := git(guest, "rev-parse", "HEAD"); got != base {
		t.Errorf("guest HEAD = %s, want recorded base %s", got, base)
	}
	git(guest, "commit", "-q", "--allow-empty", "-m", "guest change")
	tip := git(guest, "rev-parse", "HEAD")
	if out, err := exec.Command(hook[0], hook[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("reattach instance branch: %v\n%s", err, out)
	}
	if got := git(guest, "rev-parse", "HEAD"); got != tip {
		t.Errorf("guest HEAD after reattach = %s, want %s", got, tip)
	}
	if got := git(guest, "branch", "--show-current"); got != i.Branch() {
		t.Errorf("guest branch = %s, want %s", got, i.Branch())
	}
}

func TestNonGitInstanceUsesPrivateCopy(t *testing.T) {
	t.Parallel()
	source, state := t.TempDir(), t.TempDir()
	original := filepath.Join(source, "README")
	if err := os.WriteFile(original, []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}
	i := &Instance{Workspace: source, dir: instanceDir(state, "codex-adhoc-a1")}
	work, _, err := i.PrepareWorkspace("", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "README"), []byte("guest"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(original); err != nil || string(got) != "host" {
		t.Errorf("source after private edit = %q, %v, want host", got, err)
	}
}

func TestDirectWorkspaceLeaseRejectsAnOverlappingWriter(t *testing.T) {
	state, work := t.TempDir(), t.TempDir()
	instanceInventory(t, state, func() ([]sbx.Listed, bool) { return nil, false })
	first := sbx.RunConfig{Name: "codex", Mounts: []sbx.Mount{{Host: work}}}
	closeFirst, err := instanceLease(first.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFirst()
	release, err := directWorkspaceLease(Input{}, first)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	closeSecond, err := instanceLease("claude")
	if err != nil {
		t.Fatal(err)
	}
	defer closeSecond()

	check := func(t *testing.T, path string) {
		t.Helper()
		second := sbx.RunConfig{Name: "claude", Mounts: []sbx.Mount{{Host: path}}}
		if release, err := directWorkspaceLease(Input{}, second); err == nil {
			release()
			t.Error("acquired two overlapping writable workspaces")
		} else if !strings.Contains(err.Error(), "overlaps the writable workspace") {
			t.Errorf("overlap returned the wrong error: %v", err)
		}
		second.Mounts[0].ReadOnly = true
		if release, err := directWorkspaceLease(Input{}, second); err != nil {
			t.Errorf("read-only overlap refused: %v", err)
		} else {
			release()
		}
	}
	check(t, filepath.Join(work, "scope"))
	t.Run("missing scope under symlink", func(t *testing.T) {
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(work, alias); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		check(t, filepath.Join(alias, "scope", "nested"))
	})
}

func TestInstanceCleanupKeepsUnknownVMsAndRefusesReusedNames(t *testing.T) {
	state := t.TempDir()
	row := sbx.Listed{Name: "codex-task-a1", ID: "different-vm", Status: "stopped"}
	known := false
	instanceInventory(t, state, func() ([]sbx.Listed, bool) { return []sbx.Listed{row}, known })
	i := &Instance{ID: "a1", Name: row.Name, VMID: "original-vm", Attempt: "proveo-1-2",
		Workspace: t.TempDir(), dir: instanceDir(state, row.Name)}
	if err := i.save(); err != nil {
		t.Fatal(err)
	}
	if pins, err := InstanceRuns(state); err != nil || !pins[i.Attempt] {
		t.Errorf("InstanceRuns with unavailable inventory = %v, %v, want pin for %s", pins, err, i.Attempt)
	}
	known = true
	err := RemoveKept(row.Name, filepath.Join(state, "results"), func(string, ...any) {}, true)
	if err == nil || !strings.Contains(err.Error(), "unbound or changed") {
		t.Errorf("RemoveKept with replacement VM = %v, want ownership refusal", err)
	}
}
