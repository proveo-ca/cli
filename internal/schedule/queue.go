// SPEC: _spec/_experiments/dashboard/resource-queue.puml
package schedule

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type QueueState string

const (
	Queued    QueueState = "queued"
	Reserved  QueueState = "reserved"
	Running   QueueState = "running"
	Settling  QueueState = "settling"
	Attention QueueState = "attention"
	Complete  QueueState = "complete"
)

var ErrQueueBusy = errors.New("schedule queue is busy")

type QueueItem struct {
	Name, Entry, Reason string
	Revision            string
	Submitted, Started  time.Time
	State               QueueState
	Job                 Job
}

func ReadQueue(home string) ([]QueueItem, error) {
	var q []QueueItem
	b, err := os.ReadFile(filepath.Join(home, "admission-queue.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(b, &q)
	return q, err
}

func QueuePending(home string) (bool, error) {
	q, err := ReadQueue(home)
	return slices.ContainsFunc(q, func(i QueueItem) bool { return i.State.pending() }), err
}

func (s QueueState) pending() bool {
	return s == Queued || s == Reserved || s == Running || s == Settling
}

func QueueLock(home, purpose string) (*os.File, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(home, purpose+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockQueueFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func queueTxn(home string, update func([]QueueItem) ([]QueueItem, error)) error {
	var f *os.File
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(25 * time.Millisecond) {
		file, err := QueueLock(home, "admission-queue")
		if err == nil {
			f = file
			break
		}
		if !errors.Is(err, ErrQueueBusy) || time.Now().After(deadline) {
			return err
		}
	}
	defer f.Close()
	q, err := ReadQueue(home)
	if err != nil {
		return err
	}
	q, err = update(q)
	if err != nil {
		return err
	}
	return saveQueue(home, q)
}

func saveQueue(home string, q []QueueItem) error {
	b, err := json.Marshal(q)
	if err != nil {
		return err
	}
	return writeScheduleFile(filepath.Join(home, "admission-queue.json"), b)
}

func writeScheduleFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".schedule-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, werr := f.Write(body)
	if err := errors.Join(werr, f.Sync(), f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func Submit(home, name, entry string, job Job, stamp time.Time) error {
	return queueTxn(home, func(q []QueueItem) ([]QueueItem, error) {
		if index := queueIndex(q, name); index >= 0 {
			existing := q[index]
			if existing.State.pending() {
				return q, ErrRunning
			}
			q = slices.Delete(q, index, index+1)
		}
		prompt, err := os.ReadFile(job.PromptFile)
		if err != nil {
			return q, fmt.Errorf("snapshot task prompt: %w", err)
		}
		dir := filepath.Join(LogDir(home, name), "submissions", time.Now().UTC().Format("20060102T150405.000000000"))
		job.PromptFile = filepath.Join(dir, "prompt")
		if err := writeScheduleFile(job.PromptFile, prompt); err != nil {
			return q, err
		}
		return append(q, QueueItem{Name: name, Entry: entry, Job: job, State: Queued, Submitted: stamp}), nil
	})
}

func WriteJobSnapshot(item QueueItem) (string, error) {
	path := filepath.Join(filepath.Dir(item.Job.PromptFile), "job.json")
	b, err := json.Marshal(item.Job)
	if err != nil {
		return "", err
	}
	return path, writeScheduleFile(path, b)
}

func ReadJobSnapshot(path string) (Job, error) {
	var j Job
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &j)
	}
	if err == nil {
		err = j.validate()
	}
	return j, err
}

func StartDispatcher(home, proveo string) error {
	cmd := exec.Command(proveo, "schedule", "dispatch")
	cmd.Env = append(os.Environ(), "PROVEO_HOME="+home)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func Accept(home, name string) error {
	return queueTxn(home, func(q []QueueItem) ([]QueueItem, error) {
		i := queueIndex(q, name)
		if i < 0 || q[i].State != Attention || !strings.HasPrefix(q[i].Reason, "review") {
			return q, fmt.Errorf("%s has no result waiting for acceptance", name)
		}
		q[i].State, q[i].Reason = Complete, ""
		return q, nil
	})
}

func queueIndex(q []QueueItem, name string) int {
	return slices.IndexFunc(q, func(item QueueItem) bool { return item.Name == name })
}

type QueueOps struct {
	Alive        func(string) bool
	Result       func(string) *Result
	Exit         func(QueueItem) (int, bool)
	Admit        func(QueueItem) error
	Launch       func(QueueItem) error
	DiskWaitCode int
}

func Dispatch(home string, jobs Config, ops QueueOps) (bool, error) {
	more := false
	err := queueTxn(home, func(q []QueueItem) ([]QueueItem, error) {
		jobs := jobs.WithSubmissions(q)
		for i := range q {
			reconcileQueue(&q[i], ops)
		}
		for i := range q {
			item := &q[i]
			switch item.State {
			case Queued:
				more = true
			case Reserved, Running, Settling:
				more = true
				continue
			default:
				continue
			}
			if err := jobs.CheckDependencies(item.Name, item.Job); err != nil {
				item.State, item.Reason = Attention, err.Error()
				continue
			}
			if dep := firstBlocker(q, item.Job.DependsOn); dep != "" {
				item.Reason = "blocked by " + dep
				continue
			}
			if err := ops.Admit(*item); err != nil {
				item.Reason = err.Error()
				continue
			}
			item.State, item.Reason, item.Started = Reserved, "", time.Now()
			if err := saveQueue(home, q); err != nil {
				return q, err
			}
			if err := ops.Launch(*item); err != nil {
				item.State, item.Reason = Attention, err.Error()
				continue
			}
			item.State = Running
			break
		}
		return q, nil
	})
	return more, err
}

func (c Config) WithSubmissions(items []QueueItem) Config {
	jobs := make(map[string]Job, len(c.Jobs)+len(items))
	for name, job := range c.Jobs {
		jobs[name] = job
	}
	for _, item := range items {
		jobs[item.Name] = item.Job
	}
	return Config{Jobs: jobs}
}

func reconcileQueue(item *QueueItem, ops QueueOps) {
	switch item.State {
	case Reserved, Running, Settling:
	default:
		return
	}
	if ops.Alive(item.Name) {
		item.State = Running
		return
	}
	if code, ok := ops.Exit(*item); ok && code == ops.DiskWaitCode {
		item.State, item.Reason = Queued, "waiting for sandbox disk"
		return
	}
	if res := ops.Result(item.Name); res != nil && !res.Finished.Before(item.Started) && !res.Started.Before(item.Started.Add(-time.Second)) {
		item.State = Attention
		if res.Failed() {
			item.Reason = res.Detail
		} else {
			item.Reason = "review"
		}
		return
	}
	if time.Since(item.Started) > 2*time.Minute {
		item.State, item.Reason = Attention, "runner ended without a result"
		return
	}
	item.State = Settling
}

func QueueExit(home string, item QueueItem) (int, bool) {
	logs, _ := filepath.Glob(filepath.Join(LogDir(home, item.Name), "*.log"))
	if len(logs) == 0 {
		return 0, false
	}
	log := logs[len(logs)-1]
	if fi, err := os.Stat(ExitFile(log)); err == nil && !fi.ModTime().Before(item.Started) {
		return exitStatus(log)
	}
	return 0, false
}

func firstBlocker(q []QueueItem, deps []string) string {
	for _, dep := range deps {
		if i := queueIndex(q, dep); i < 0 || q[i].State != Complete {
			return dep
		}
	}
	return ""
}

func (c Config) CheckDependencies(name string, root Job) error {
	active, checked := map[string]bool{}, map[string]bool{}
	var check func(string, Job) error
	check = func(id string, j Job) error {
		if active[id] {
			return fmt.Errorf("dependency cycle at %s", id)
		}
		if checked[id] {
			return nil
		}
		active[id] = true
		defer delete(active, id)
		for _, dep := range j.DependsOn {
			d, ok := c.Jobs[dep]
			if !ok {
				return fmt.Errorf("%s requires missing task %s", id, dep)
			}
			if err := check(dep, d); err != nil {
				return err
			}
		}
		checked[id] = true
		return nil
	}
	return check(name, root)
}
