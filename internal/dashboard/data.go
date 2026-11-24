// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package dashboard

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/proveo-ca/proveo/internal/backend/sandbox"
	"github.com/proveo-ca/proveo/internal/sbx"
	"github.com/proveo-ca/proveo/internal/schedule"
)

type Task struct {
	ID, Title, Agent, Phase, Ticket, Source, Git, Prompt, Reason string
	Instance, Sandbox, VMID, Branch, Repo, Base, Tip, Workdir    string
	Host                                                         string
	PR                                                           string
	Revision                                                     string
	LastPush, Updated                                            time.Time
	Live, Guest                                                  bool
	DependsOn                                                    []string
	State                                                        schedule.QueueState
}

type Change struct{ Kind, Path, OldPath string }

type Data struct {
	Tasks         []Task
	Capacity      sbx.Capacity
	CapacityError string
	Files         []Change
}

func Read(home, state, cwd string) (Data, error) {
	var d Data
	queued, err := schedule.ReadQueue(home)
	if err != nil {
		return d, err
	}
	c, err := schedule.Load(home)
	if err != nil && !os.IsNotExist(err) {
		return d, err
	}
	instances, err := sandbox.WorkspaceInstances(state, cwd)
	if err != nil {
		return d, err
	}
	var inventory []sbx.Listed
	if len(instances) != 0 {
		inventory, _ = sbx.List()
	}
	capacity, diskErr := sbx.DiskCapacity(state)
	d.Capacity = sbx.HostTelemetry(capacity)
	if diskErr != nil {
		d.CapacityError = diskErr.Error()
	}
	sortTasks := func(a, b Task) int { return strings.Compare(a.ID, b.ID) }
	seen := map[string]bool{}
	for name, job := range c.WithSubmissions(queued).Jobs {
		if !sameDir(job.WorkDir(home, name), cwd) {
			continue
		}
		task := Task{ID: name, Title: name, Agent: job.Target, Phase: job.Phase, Ticket: job.Ticket,
			Source: taskSource(job), State: schedule.Queued, DependsOn: job.DependsOn}
		if origin, ok := c.Jobs[name]; ok && task.Source == "Local" {
			task.Source = taskSource(origin)
		}
		if item := slices.IndexFunc(queued, func(q schedule.QueueItem) bool { return q.Name == name }); item >= 0 {
			q := queued[item]
			job = q.Job
			task.State, task.Reason, task.Updated, task.DependsOn = q.State, q.Reason, q.Submitted, job.DependsOn
			task.Revision = q.Revision
		}
		if job.Task != "" {
			task.Title = job.Task
		}
		task.Live = schedule.NewTmux(schedule.SessionName(name), nil).Alive()
		if task.Live {
			task.State = schedule.Running
		}
		if res := schedule.LastResult(home, name); res != nil && task.Updated.IsZero() && !task.Live {
			task.State, task.Reason, task.Updated = schedule.Attention, res.Detail, res.Finished
		}
		if task.Phase == "" {
			task.Phase = "Spec"
			if task.State == schedule.Attention {
				task.Phase = "Review"
			}
		}
		task.Prompt, _ = readText(job.PromptFile)
		if i := matchingInstance(instances, inventory, name, job); i >= 0 {
			setInstance(&task, instances[i], inventory)
			seen[instances[i].ID] = true
		}
		if task.Revision != "" && task.Tip != "" && task.Revision != task.Tip && task.Phase != "Spec" {
			task.Phase = "Spec revision"
		}
		d.Tasks = append(d.Tasks, task)
	}
	for _, i := range instances {
		if seen[i.ID] {
			continue
		}
		label := i.Task
		if label == "" {
			label = i.Name
		}
		task := Task{ID: i.ID, Title: label, Agent: i.Target, Phase: "Review", State: schedule.Attention}
		setInstance(&task, i, inventory)
		if task.Guest {
			task.State, task.Phase = schedule.Running, "Working"
		}
		d.Tasks = append(d.Tasks, task)
	}
	slices.SortFunc(d.Tasks, sortTasks)
	d.Files = workspaceFiles(cwd)
	return d, nil
}

func matchingInstance(instances []sandbox.Instance, rows []sbx.Listed, name string, job schedule.Job) int {
	best, bestLive := -1, false
	for n, i := range instances {
		if i.Target != job.Target {
			continue
		}
		if job.Instance != "" {
			if i.ID != job.Instance {
				continue
			}
		} else if i.Job != name {
			continue
		}
		live := instanceLive(i, rows)
		if best < 0 || (live && !bestLive) || (live == bestLive && i.Attempt > instances[best].Attempt) {
			best, bestLive = n, live
		}
	}
	return best
}

func sameDir(path, cwd string) bool {
	if v, err := filepath.EvalSymlinks(path); err == nil {
		path = v
	}
	abs, err := filepath.Abs(path)
	return err == nil && abs == cwd
}

func taskSource(job schedule.Job) string {
	switch {
	case strings.Contains(job.Ticket, "linear.app/"):
		return "Linear"
	case strings.Contains(job.Ticket, "github.com/"):
		return "GitHub Issues"
	case strings.Contains(filepath.ToSlash(job.PromptFile), "/_plans/"):
		return "_plans/"
	default:
		return "Local"
	}
}

func instanceLive(i sandbox.Instance, rows []sbx.Listed) bool {
	host, err := os.Hostname()
	return err == nil && host == i.Host && i.VMID != "" &&
		slices.ContainsFunc(rows, func(r sbx.Listed) bool { return r.ID == i.VMID && r.Name == i.Name && r.Running() })
}

func setInstance(t *Task, i sandbox.Instance, rows []sbx.Listed) {
	t.Instance, t.Sandbox, t.Base, t.Repo, t.Workdir = i.ID, i.Name, i.Base, i.Repo, i.GuestWorkspace()
	t.VMID, t.Host = i.VMID, i.Host
	t.Guest = instanceLive(i, rows)
	if i.Isolated() && i.Repo != "" {
		t.Branch = i.Branch()
		t.Tip = carriedTip(i)
	}
	if t.Repo != "" {
		remote, _ := git(t.Repo, "config", "--get", "remote.origin.url")
		t.Git = "Git"
		switch {
		case strings.Contains(remote, "github.com"):
			t.Git = "GitHub"
		case strings.Contains(remote, "gitlab"):
			t.Git = "GitLab"
		}
		if t.Branch != "" {
			log, _ := git(t.Repo, "reflog", "show", "--date=unix", "--format=%gD %gs", "refs/remotes/origin/"+t.Branch)
			for line := range strings.SplitSeq(log, "\n") {
				if ref, action, _ := strings.Cut(line, " "); action == "update by push" {
					_, stamp, _ := strings.Cut(ref, "@{")
					if n, err := strconv.ParseInt(strings.TrimSuffix(stamp, "}"), 10, 64); err == nil {
						t.LastPush = time.Unix(n, 0)
						break
					}
				}
			}
		}
	}
}

func carriedTip(i sandbox.Instance) string {
	out, err := git(i.Repo, "for-each-ref", "--sort=-refname", "--format=%(objectname) %(refname)", "refs/proveo/")
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(out, "\n") {
		tip, ref, _ := strings.Cut(line, " ")
		if strings.HasSuffix(ref, "/"+i.Branch()) {
			return tip
		}
	}
	return ""
}

func Changes(t Task) ([]Change, error) {
	if t.Base == "" || t.Tip == "" || t.Repo == "" {
		return nil, nil
	}
	out, err := git(t.Repo, "diff", "--name-status", "-z", "-M", t.Base, t.Tip)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	var changes []Change
	for len(fields) >= 2 {
		kind, path := fields[0], fields[1]
		fields = fields[2:]
		c := Change{Kind: kind, Path: path}
		if strings.HasPrefix(kind, "R") || strings.HasPrefix(kind, "C") {
			if len(fields) == 0 {
				return nil, fmt.Errorf("truncated git change for %s", path)
			}
			c.OldPath, c.Path, fields = path, fields[0], fields[1:]
		}
		changes = append(changes, c)
	}
	slices.SortFunc(changes, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	return changes, nil
}

func Preview(cwd string, t Task, c Change, diff bool) (string, error) {
	if !filepath.IsLocal(filepath.FromSlash(c.Path)) {
		return "", fmt.Errorf("path lies outside the workspace")
	}
	if c.Kind != "" && t.Tip != "" {
		if diff {
			args := []string{"--literal-pathspecs", "diff", "--no-ext-diff", "-M", t.Base, t.Tip, "--", c.Path}
			if c.OldPath != "" {
				args = append(args, c.OldPath)
			}
			return git(t.Repo, args...)
		}
		tip := t.Tip
		if c.Kind == "D" {
			tip = t.Base
		}
		return git(t.Repo, "show", tip+":"+c.Path)
	}
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	path := filepath.FromSlash(c.Path)
	info, err := root.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("preview requires a regular file")
	}
	f, err := root.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 128*1024))
	return string(b), err
}

func workspaceFiles(cwd string) []Change {
	var out []Change
	_ = filepath.WalkDir(cwd, func(path string, e fs.DirEntry, err error) error {
		if len(out) >= 3000 {
			return fs.SkipAll
		}
		if err != nil {
			return nil
		}
		if e.IsDir() {
			if path != cwd && slices.Contains([]string{".git", "node_modules", "vendor", ".venv"}, e.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(cwd, path); err == nil {
			out = append(out, Change{Path: rel})
		}
		return nil
	})
	slices.SortFunc(out, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	return out
}

func readText(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 64*1024))
	return string(b), err
}

func git(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSuffix(string(out), "\n"), err
}
