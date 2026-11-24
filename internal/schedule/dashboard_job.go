// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package schedule

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func NewDashboardJob(home, cwd, target, label, ticket, prompt string) (string, Job, error) {
	var id [6]byte
	_, _ = rand.Read(id[:])
	key := safe(target) + "-" + hex.EncodeToString(id[:])
	dir := filepath.Join(home, "dashboard-jobs", key)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", Job{}, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", Job{}, err
	}
	j := Job{Target: target, New: true, Task: strings.TrimSpace(label), Ticket: ticket, Workdir: cwd,
		PromptFile: filepath.Join(dir, "prompt"), Phase: "Spec"}
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
