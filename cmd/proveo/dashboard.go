// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package main

import (
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/proveo-ca/proveo/internal/backend/sandbox"
	"github.com/proveo-ca/proveo/internal/dashboard"
	"github.com/proveo-ca/proveo/internal/proveohome"
	"github.com/proveo-ca/proveo/internal/run"
	"github.com/proveo-ca/proveo/internal/schedule"
)

func openDashboard(cmd *cobra.Command) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	home := proveohome.Root(os.Getenv)
	dispatchErr := restartDispatcher(home)
	var targets []string
	if known, err := loadTargets(); err == nil {
		for target := range known {
			targets = append(targets, target)
		}
		slices.Sort(targets)
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	interrupted, err := dashboard.Run(ctx, dashboard.Options{
		CWD: cwd, Home: home, State: run.StateDir(), Binary: proveoExe(), Targets: targets, StartupError: dispatchErr,
		Launch: func(target, task, ticket, prompt string) (string, error) {
			name, j, err := schedule.NewDashboardJob(home, cwd, target, task, ticket, prompt)
			if err == nil {
				err = submitScheduled(home, name, "dashboard", j)
			}
			return name, err
		},
	})
	if interrupted {
		return sandbox.ExitError{Code: 130}
	}
	return err
}
