// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package dashboard

import (
	"context"
	"io"
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
	Outcome                                                      string
	Instance, Sandbox, VMID, Branch, Repo, Base, Tip, Workdir    string
	Host                                                         string
	PR                                                           string
	Revision                                                     string
	LastPush, Updated                                            time.Time
	Live, Guest                                                  bool
	Branches                                                     []string         // the running sandbox's working branches, its own checkout first
	Review                                                       *ReviewRequest   // a ghost: a review request no run has taken
	Ports                                                        []sbx.ListedPort // the sandbox's published ports, from sbx ls
	Session                                                      string           // the host tmux session a `proveo run` hosts this sandbox's agent in
	Job                                                          string           // the schedule job this task runs; empty for an instance alone
	DashboardJob                                                 bool             // the dashboard made the job, so clean up may forget it
	Report, Summary                                              string           // the report the run that ended it left on the host, and its RESULT line
	DependsOn                                                    []string
	State                                                        schedule.QueueState
}

type Change struct{ Kind, Path, OldPath string }

type Data struct {
	Tasks         []Task
	Capacity      sbx.Capacity
	CapacityError string
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
	inventory, _ := sbx.List()
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
			Source: taskSource(job), State: schedule.Queued, DependsOn: job.DependsOn, Job: name}
		if _, err := os.Stat(filepath.Join(home, "dashboard-jobs", name, "job.json")); err == nil {
			task.DashboardJob = true
		}
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
		task = withResult(task, schedule.LastResult(home, name))
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
			if task.Live {
				task.State = schedule.Running
			}
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
		if task.Guest || task.Live {
			task.State, task.Phase = schedule.Running, "Working"
		}
		d.Tasks = append(d.Tasks, task)
	}
	d.Tasks = append(d.Tasks, discoveredSandboxes(cwd, instances, inventory)...)
	slices.SortFunc(d.Tasks, sortTasks)
	return d, nil
}

func discoveredSandboxes(cwd string, instances []sandbox.Instance, rows []sbx.Listed) []Task {
	host, _ := os.Hostname()
	var discovered []Task
	for _, row := range rows {
		if row.ID == "" || !row.Running() || len(row.Workspaces) == 0 ||
			!sameDir(strings.TrimSuffix(row.Workspaces[0], ":ro"), cwd) {
			continue
		}
		if slices.ContainsFunc(instances, func(i sandbox.Instance) bool { return i.VMID == row.ID }) {
			continue
		}
		session := liveSession(row.Name)
		discovered = append(discovered, Task{
			ID: "sbx:" + row.ID, Title: row.Name, Sandbox: row.Name, VMID: row.ID, Host: host, Guest: true,
			Workdir: strings.TrimSuffix(row.Workspaces[0], ":ro"),
			Agent:   row.Agent, Phase: "Working", State: schedule.Running, Reason: "Discovered from the local inventory",
			Ports:   row.Ports,
			Session: session, Live: session != "",
		})
	}
	return discovered
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
	case linearID.MatchString(job.Ticket):
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
	if k := slices.IndexFunc(rows, func(r sbx.Listed) bool { return r.Name == i.Name && (i.VMID == "" || r.ID == i.VMID) }); k >= 0 {
		t.Ports = rows[k].Ports
	}
	if s := liveSession(i.Name); s != "" {
		t.Session, t.Live = s, true
	}
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

// withResult folds a job's last result into its task: all of it when the queue holds nothing newer,
// only the outcome when the result ended the run the queue still shows.
func withResult(task Task, res *schedule.Result) Task {
	if res == nil || task.Live {
		return task
	}
	switch {
	case task.Updated.IsZero():
		task.State, task.Reason, task.Updated, task.Outcome = schedule.Attention, res.Detail, res.Finished, res.Outcome
		task.Report, task.Summary = res.Report, res.Summary
	case task.State == schedule.Attention && !res.Finished.Before(task.Updated):
		task.Outcome, task.Report, task.Summary = res.Outcome, res.Report, res.Summary
	}
	return task
}

// liveSession is the host tmux session a `proveo run` hosts sandbox name's agent in, while that session lives.
var liveSession = func(name string) string {
	s := sandbox.SessionOf(name)
	if s == "" || !schedule.NewTmux(s, nil).Alive() {
		return ""
	}
	return s
}
