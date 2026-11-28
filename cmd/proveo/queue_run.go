// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/proveo-ca/proveo/internal/proveohome"
	"github.com/proveo-ca/proveo/internal/run"
	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

// admitWait is how long `proveo run --queue` waits for admission before handing back.
var admitWait = 20 * time.Second

// queueRun submits a card-resolved run as an attended job, then attaches once its session is up.
func queueRun(q run.QueueRequest) error {
	home := proveohome.Root(os.Getenv)
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	name, j, err := schedule.NewAttendedJob(home, cwd, q.Target, q.Task, q.Args, q.Prompt)
	if err != nil {
		return err
	}
	if err := submitScheduled(home, name, "dashboard", j); err != nil {
		return err
	}
	ui.Okf("%s — queued; attaching once the sandbox is admitted", name)
	if why, up := awaitAdmission(home, name, admitWait); !up {
		ui.Warnf("%s — %s; attach later with ↵ in the dashboard or `proveo attach %s`", name, why, name)
		return nil
	}
	return attachSession(name, false)
}

// awaitAdmission polls until the job's session is up, the queue gives up on it, or wait passes.
func awaitAdmission(home, name string, wait time.Duration) (string, bool) {
	sess := schedule.SessionName(name)
	why := fmt.Sprintf("still queued after %s", wait)
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if tmuxHasSession(sess) {
			return "", true
		}
		queue, err := schedule.ReadQueue(home)
		if err != nil {
			continue
		}
		i := slices.IndexFunc(queue, func(item schedule.QueueItem) bool { return item.Name == name })
		if i < 0 {
			continue
		}
		switch item := queue[i]; item.State {
		case schedule.Attention, schedule.Complete:
			return "ended before it could attach: " + item.Reason, false
		case schedule.Queued:
			if item.Reason != "" {
				why = "still queued: " + item.Reason
			}
		}
	}
	return why, false
}
