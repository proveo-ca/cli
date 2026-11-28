// SPEC: _spec/internal/schedule/resource-queue.puml
package main

import (
	"errors"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/proveo-ca/proveo/internal/backend/sandbox"
	"github.com/proveo-ca/proveo/internal/proveohome"
	"github.com/proveo-ca/proveo/internal/run"
	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

func queuedJob(j schedule.Job) bool { return j.Target != "" || len(j.DependsOn) != 0 }

func submitScheduled(home, name, entry string, j schedule.Job) error {
	if !queuedJob(j) {
		return schedule.Launch(home, proveoExe(), name, entry, j, nil)
	}
	if err := schedule.Submit(home, name, entry, j, time.Now()); err != nil {
		return err
	}
	return schedule.StartDispatcher(home, proveoExe())
}

func scheduleDispatchCmd() *cobra.Command {
	return &cobra.Command{Use: "dispatch", Hidden: true, Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := proveohome.Root(os.Getenv)
			for {
				lock, err := schedule.QueueLock(home, "admission-worker")
				if errors.Is(err, schedule.ErrQueueBusy) {
					return nil
				}
				if err != nil {
					return err
				}
				err = runDispatcher(cmd, home)
				_ = lock.Close()
				if err != nil {
					return err
				}
				pending, err := schedule.QueuePending(home)
				if err != nil || !pending {
					return err
				}
			}
		}}
}

func runDispatcher(cmd *cobra.Command, home string) error {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		jobs, err := schedule.Load(home)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		pending, err := dispatchScheduled(home, jobs)
		if err != nil || !pending {
			return err
		}
		select {
		case <-cmd.Context().Done():
			return cmd.Context().Err()
		case <-tick.C:
		}
	}
}

func dispatchScheduled(home string, jobs schedule.Config) (bool, error) {
	return schedule.Dispatch(home, jobs, schedule.QueueOps{
		Alive:        func(name string) bool { return tmuxHasSession(schedule.SessionName(name)) },
		Result:       func(name string) *schedule.Result { return schedule.LastResult(home, name) },
		Exit:         func(item schedule.QueueItem) (int, bool) { return schedule.QueueExit(home, item) },
		DiskWaitCode: sandbox.DiskWaitExit,
		Admit: func(item schedule.QueueItem) error {
			j := item.Job
			if j.Target == "" || (!j.New && sandbox.CanReuseInstance(run.StateDir(), j.Target, j.WorkDir(home, item.Name), j.Instance)) {
				return nil
			}
			state := run.StateDir()
			if err := os.MkdirAll(state, 0o700); err != nil {
				return err
			}
			return sandbox.CheckAdmission(state)
		},
		Launch: func(item schedule.QueueItem) error {
			snapshot, err := schedule.WriteJobSnapshot(item)
			if err != nil {
				return err
			}
			return schedule.Launch(home, proveoExe(), item.Name, item.Entry, item.Job, nil, snapshot)
		},
	})
}

func scheduleAcceptCmd() *cobra.Command {
	return &cobra.Command{Use: "accept <task>", Short: "Accept a reviewed result and unblock dependent tasks",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			home := proveohome.Root(os.Getenv)
			if err := schedule.Accept(home, args[0]); err != nil {
				return err
			}
			ui.Okf("%s — accepted", args[0])
			return schedule.StartDispatcher(home, proveoExe())
		}}
}

func schedulePhaseCmd() *cobra.Command {
	return &cobra.Command{Use: "phase <task> <phase> <revision>", Short: "Record a workflow phase against a Git revision", Args: cobra.ExactArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			home := proveohome.Root(os.Getenv)
			return schedule.UpdatePhase(home, args[0], args[1], args[2])
		}}
}
