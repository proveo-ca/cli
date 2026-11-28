// SPEC: _spec/internal/dashboard/workspace-instances.puml
package schedule

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// NewAttendedJob snapshots an operator's run: the card's flags as Args, an optional opening prompt and what it works on.
func NewAttendedJob(home, cwd, target, label string, args []string, prompt, ticket string) (string, Job, error) {
	return newJob(home, prompt, Job{Target: target, New: true, Task: strings.TrimSpace(label), Workdir: cwd,
		Args: args, Mode: ModeAttended, Phase: "Session", Ticket: strings.TrimSpace(ticket)})
}

func newJob(home, prompt string, j Job) (string, Job, error) {
	var id [6]byte
	_, _ = rand.Read(id[:])
	key := safe(j.Target) + "-" + hex.EncodeToString(id[:])
	dir := filepath.Join(home, "dashboard-jobs", key)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", Job{}, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", Job{}, err
	}
	j.PromptFile = filepath.Join(dir, "prompt")
	if j.Task == "" {
		j.Task = key
	}
	if err := j.validate(); err != nil {
		return "", Job{}, err
	}
	if err := writeScheduleFile(j.PromptFile, []byte(prompt)); err != nil {
		return "", Job{}, err
	}
	b, err := json.Marshal(j)
	if err != nil {
		return "", Job{}, err
	}
	if err := writeScheduleFile(filepath.Join(dir, "job.json"), b); err != nil {
		return "", Job{}, err
	}
	return key, j, nil
}

func loadDashboardJobs(home string, c *Config) error {
	root := filepath.Join(home, "dashboard-jobs")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Jobs == nil {
		c.Jobs = make(map[string]Job)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		j, err := ReadJobSnapshot(filepath.Join(root, entry.Name(), "job.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if _, exists := c.Jobs[entry.Name()]; exists {
			return fmt.Errorf("duplicate dashboard job %s", entry.Name())
		}
		c.Jobs[entry.Name()] = j
	}
	return nil
}

// ReviewArgs are what every review run adds to `proveo run`: linked shares read-only, and the host Chrome its live checks drive.
var ReviewArgs = []string{"--read-only-shares", "--addon", "chrome"}

// ReviewBudget is how long a review run may take: a review map and a demo recording outrun the default budget.
const ReviewBudget = "90m"

// NewReviewJob snapshots a headless review of a pull request: the PR as the ticket, the rendered prompt as plain input.
func NewReviewJob(home, cwd, target, prURL, label, prompt string) (string, Job, error) {
	if !strings.Contains(prURL, "/pull/") {
		return "", Job{}, fmt.Errorf("%q is not a pull request URL", prURL)
	}
	if strings.TrimSpace(prompt) == "" {
		return "", Job{}, fmt.Errorf("a review needs a prompt")
	}
	return newJob(home, prompt, Job{Target: target, New: true, Task: strings.TrimSpace(label), Ticket: prURL, Workdir: cwd,
		Mode: "plain", Phase: "Review", Budget: ReviewBudget, Args: slices.Clone(ReviewArgs)})
}

// DropReviewJob forgets a dashboard review job: its queue entry and its job snapshot. The logs stay.
func DropReviewJob(home, name string) (Job, error) {
	j, err := ReadJobSnapshot(filepath.Join(home, "dashboard-jobs", name, "job.json"))
	if err != nil {
		return Job{}, err
	}
	if j.Phase != "Review" || !strings.Contains(j.Ticket, "/pull/") {
		return Job{}, fmt.Errorf("%s is not a review job", name)
	}
	return DropDashboardJob(home, name)
}

// DropDashboardJob forgets any job the dashboard made: its queue entry and its job snapshot. The logs stay.
func DropDashboardJob(home, name string) (Job, error) {
	dir := filepath.Join(home, "dashboard-jobs", name)
	j, err := ReadJobSnapshot(filepath.Join(dir, "job.json"))
	if err != nil {
		return Job{}, err
	}
	if err := queueTxn(home, func(q []QueueItem) ([]QueueItem, error) {
		return slices.DeleteFunc(q, func(item QueueItem) bool { return item.Name == name }), nil
	}); err != nil {
		return Job{}, err
	}
	return j, os.RemoveAll(dir)
}
