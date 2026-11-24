// SPEC: _spec/internal/sbx/instance-lifecycle.puml
package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/proveo-ca/proveo/internal/sbx"
)

type Instance struct {
	ID, Name, Target, Workspace, Repo, Task, Base, Host, VMID, Attempt string
	Legacy                                                             bool
	dir                                                                string
}

func (i *Instance) Isolated() bool { return i != nil && !i.Legacy }

type InstanceRequest struct {
	State, Target, Workspace, Repo, LegacyName, ID, Task string
	New, Print                                           bool
	Pick                                                 func([]string) (int, error)
}

const instanceRecord = "instance.json"

var instanceToken = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
var taskSlug = regexp.MustCompile(`[^a-z0-9]+`)
var instanceBase = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
var errInstanceOwned = errors.New("already has a Proveo owner")

func instanceDir(state, name string) string { return filepath.Join(state, "instances", name) }

func readInstance(state, name string) (*Instance, error) {
	if !instanceToken.MatchString(name) {
		return nil, nil
	}
	dir := instanceDir(state, name)
	b, err := os.ReadFile(filepath.Join(dir, instanceRecord))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entry Instance
	if err := json.Unmarshal(b, &entry); err != nil {
		return nil, fmt.Errorf("read instance %s: %w", name, err)
	}
	if entry.Name != name || !instanceToken.MatchString(entry.ID) || !filepath.IsAbs(entry.Workspace) {
		return nil, fmt.Errorf("invalid instance record for %s", name)
	}
	if !entry.Legacy && entry.Repo != "" && !instanceBase.MatchString(entry.Base) {
		return nil, fmt.Errorf("invalid Git base in instance record for %s", name)
	}
	entry.dir = dir
	return &entry, nil
}

func (i *Instance) save() error {
	b, err := json.Marshal(i)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(i.dir, 0o700); err != nil {
		return err
	}
	return writePrivateRecord(filepath.Join(i.dir, instanceRecord), b)
}

func instanceEntries(state string) ([]*Instance, error) {
	names, err := os.ReadDir(filepath.Join(state, "instances"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []*Instance
	for _, name := range names {
		if !name.IsDir() {
			continue
		}
		i, err := readInstance(state, name.Name())
		if err != nil {
			return nil, err
		}
		if i != nil {
			entries = append(entries, i)
		}
	}
	return entries, nil
}

func SelectInstance(q InstanceRequest) (*Instance, error) {
	if q.New {
		return allocateInstance(q)
	}
	entries, err := instanceEntries(q.State)
	if err != nil {
		return nil, err
	}
	var candidates []*Instance
	for _, entry := range entries {
		if entry.Workspace == q.Workspace && entry.Target == q.Target {
			candidates = append(candidates, entry)
		}
	}
	if q.LegacyName != "" && !slices.ContainsFunc(entries, func(i *Instance) bool { return i.Name == q.LegacyName }) &&
		(len(candidates) == 0 || readReceipt(q.LegacyName) != nil) {
		host, err := os.Hostname()
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, &Instance{ID: "legacy-" + digest(q.Target+"\x00"+q.Workspace),
			Name: q.LegacyName, Target: q.Target, Workspace: q.Workspace, Repo: q.Repo,
			Host: host, Legacy: true, dir: instanceDir(q.State, q.LegacyName)})
	}
	if q.ID != "" {
		for _, entry := range candidates {
			if entry.ID == q.ID {
				return entry, nil
			}
		}
		return nil, fmt.Errorf("instance %q is not registered for %s in this workspace", q.ID, q.Target)
	}
	if len(candidates) == 0 && q.LegacyName != "" && slices.ContainsFunc(entries, func(i *Instance) bool { return i.Name == q.LegacyName }) {
		return nil, fmt.Errorf("sandbox name %s is already recorded for another workspace; use --new", q.LegacyName)
	}
	switch len(candidates) {
	case 0:
		return nil, nil
	case 1:
		return candidates[0], nil
	default:
		labels := make([]string, 0, len(candidates))
		for _, entry := range candidates {
			labels = append(labels, strings.Join([]string{entry.ID, entry.Name, entry.Task}, "  "))
		}
		if q.Pick == nil {
			return nil, fmt.Errorf("select an instance with --instance ID or start one with --new:\n  %s", strings.Join(labels, "\n  "))
		}
		picked, err := q.Pick(labels)
		if err != nil {
			return nil, err
		}
		if picked < 0 || picked >= len(candidates) {
			return nil, errors.New("instance selection cancelled")
		}
		return candidates[picked], nil
	}
}

func allocateInstance(q InstanceRequest) (*Instance, error) {
	host, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	var base string
	if q.Repo != "" {
		out, err := exec.Command("git", "-C", q.Repo, "rev-parse", "--verify", "HEAD^{commit}").Output()
		if err != nil {
			return nil, fmt.Errorf("record the instance's Git base in %s: %w", q.Repo, err)
		}
		base = strings.TrimSpace(string(out))
	}
	for range 8 {
		var random [10]byte
		_, _ = rand.Read(random[:])
		id := hex.EncodeToString(random[:])
		name := taskSandboxName(q.Target, q.Task, id[:10])
		dir := instanceDir(q.State, name)
		i := &Instance{ID: id, Name: name, Target: q.Target, Workspace: q.Workspace, Repo: q.Repo,
			Task: q.Task, Base: base, Host: host, dir: dir}
		if q.Print {
			return i, nil
		}
		row, err := inventoryInstance(name)
		if err != nil {
			return nil, err
		}
		if row != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return nil, err
		}
		switch err := os.Mkdir(dir, 0o700); {
		case errors.Is(err, os.ErrExist):
			continue
		case err != nil:
			return nil, err
		}
		return i, i.save()
	}
	return nil, errors.New("could not allocate a unique sandbox name")
}

func taskSandboxName(agent, task, suffix string) string {
	slug := func(s string) string { return strings.Trim(taskSlug.ReplaceAllString(strings.ToLower(s), "-"), "-") }
	a, t := slug(agent), slug(task)
	if t == "" {
		t = "adhoc"
	}
	if a == "" {
		a = "agent"
	}
	a = strings.TrimRight(a[:min(len(a), 32-len(suffix)-3)], "-")
	t = strings.TrimRight(t[:min(len(t), 32-len(a)-len(suffix)-2)], "-")
	return a + "-" + t + "-" + suffix
}

func instanceLease(name string) (func(), error) {
	if !instanceToken.MatchString(name) || receiptDir() == "" {
		return nil, fmt.Errorf("invalid sandbox lease name %q", name)
	}
	dir := filepath.Join(receiptDir(), "instance-leases")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	busy, err := tryProfileLock(f)
	if busy || err != nil {
		_ = f.Close()
		if busy {
			return nil, fmt.Errorf("sandbox %s %w", name, errInstanceOwned)
		}
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

func inventoryInstance(name string) (*sbx.Listed, error) {
	rows, ok := listSandboxes()
	if !ok || (len(rows) == 0 && sbx.Exists(name)) {
		return nil, errNoListing
	}
	var found *sbx.Listed
	for _, row := range rows {
		if row.Name != name {
			continue
		}
		if found != nil || row.ID == "" {
			return nil, fmt.Errorf("sbx inventory cannot establish the identity of %s", name)
		}
		found = &row
	}
	return found, nil
}

func (i *Instance) verify(row *sbx.Listed) error {
	if row != nil && (i.VMID == "" || row.ID != i.VMID) {
		return fmt.Errorf("sbx identity for %s is unbound or changed; inspect sbx ls before recovery", i.Name)
	}
	return nil
}

func (i *Instance) Begin(agent, source string) (func(), error) {
	release, err := instanceLease(i.Name)
	if err != nil {
		return nil, err
	}
	if err := i.begin(agent, source); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (i *Instance) begin(agent, source string) error {
	old, err := readInstance(filepath.Dir(filepath.Dir(i.dir)), i.Name)
	if err != nil {
		return err
	}
	if old != nil {
		if old.ID != i.ID || old.Workspace != i.Workspace || old.Target != i.Target {
			return fmt.Errorf("sandbox name %s belongs to a different instance", i.Name)
		}
		*i = *old
	}
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	if i.Host != host {
		return fmt.Errorf("instance %s belongs to host %s", i.ID, i.Host)
	}
	row, err := inventoryInstance(i.Name)
	if err != nil {
		return err
	}
	if row != nil && i.Legacy && old == nil {
		receipt := readReceipt(i.Name)
		if receipt == nil || receipt.InstanceID != "" || receipt.Agent != agent || row.Agent != agent ||
			!listedWorkspace(row, source) ||
			!slices.ContainsFunc(receipt.Mounts, func(mount string) bool { return strings.HasPrefix(mount, source+":") }) {
			return fmt.Errorf("cannot verify ownership of legacy sandbox %s; inspect sbx ls or start a separate instance with --new", i.Name)
		}
		i.VMID, i.Attempt = row.ID, receipt.Run
		if i.Attempt == "" && filepath.IsAbs(receipt.KitDir) {
			run := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(receipt.KitDir))))
			if strings.HasPrefix(run, "proveo-") && instanceToken.MatchString(run) {
				i.Attempt = run
			}
		}
	}
	if row != nil && i.VMID == "" && old != nil {
		if receipt := readReceipt(i.Name); receipt != nil && i.Attempt != "" &&
			receipt.InstanceID == i.ID && receipt.Run == i.Attempt && receipt.VMID == row.ID {
			i.VMID = row.ID
			if err := i.save(); err != nil {
				return err
			}
		}
	}
	if err := i.verify(row); err != nil {
		return err
	}
	if err := idleInstance(row); err != nil {
		return err
	}
	if old == nil {
		return i.save()
	}
	return nil
}

func idleInstance(row *sbx.Listed) error {
	if row == nil {
		return nil
	}
	switch strings.ToLower(row.Status) {
	case "created", "exited", "stopped":
		return nil
	default:
		return fmt.Errorf("sandbox %s is %s; close its active session before reusing it", row.Name, row.Status)
	}
}

func checkReceiptVM(name string) error {
	if receipt := readReceipt(name); receipt != nil && receipt.InstanceID != "" {
		return (&Instance{Name: name, VMID: receipt.VMID}).checkVM()
	}
	return nil
}

func canonicalInstancePath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(resolved)
}

func (i *Instance) bind(attempt string, receipt launchReceipt) error {
	row, err := inventoryInstance(i.Name)
	if err != nil {
		return err
	}
	if row == nil || row.Agent != receipt.Agent || !listedWorkspace(row, receipt.Source) {
		return fmt.Errorf("sbx did not report the newly created sandbox %s", i.Name)
	}
	i.VMID, i.Attempt = row.ID, attempt
	receipt.InstanceID, receipt.VMID, receipt.Run = i.ID, i.VMID, attempt
	if err := writeReceipt(i.Name, receipt); err != nil {
		return err
	}
	return i.save()
}

func listedWorkspace(row *sbx.Listed, source string) bool {
	if source == "" {
		return false
	}
	source = canonicalInstancePath(source)
	return slices.ContainsFunc(row.Workspaces, func(path string) bool {
		return canonicalInstancePath(strings.TrimSuffix(path, ":ro")) == source
	})
}

func (i *Instance) checkVM() error {
	row, err := inventoryInstance(i.Name)
	if err != nil {
		return err
	}
	if row == nil {
		return fmt.Errorf("sbx no longer lists %s", i.Name)
	}
	return i.verify(row)
}

func InstanceRuns(state string) (map[string]bool, error) {
	entries, err := instanceEntries(state)
	if err != nil {
		return nil, err
	}
	pinned := make(map[string]bool)
	if len(entries) == 0 {
		return pinned, nil
	}
	rows, ok := listSandboxes()
	for _, entry := range entries {
		if entry.Attempt != "" && (!ok || len(rows) == 0 || slices.ContainsFunc(rows, func(row sbx.Listed) bool {
			return row.Name == entry.Name
		})) {
			pinned[entry.Attempt] = true
		}
	}
	return pinned, nil
}

func (i *Instance) Home(root string) string {
	return filepath.Join(root, "instances", i.ID)
}

func (i *Instance) Branch() string { return "proveo/" + i.Name }

func (i *Instance) worktree() string { return filepath.Join(i.dir, "workspace") }
func (i *Instance) bundleDir() string {
	return filepath.Join(filepath.Dir(filepath.Dir(i.dir)), "instance-bases", i.Base)
}
func (i *Instance) exportDir(sid string) string { return filepath.Join(i.dir, "outputs", sid) }
