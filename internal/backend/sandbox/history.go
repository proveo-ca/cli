// SPEC: _spec/internal/sbx/state-sync.puml
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/proveo-ca/proveo/internal/proveohome"
	"github.com/proveo-ca/proveo/internal/sbx"
	"github.com/proveo-ca/proveo/internal/ui"
)

func (t *teardown) sessionCheckpoint(ctx context.Context, saveErr error) error {
	in := t.in
	if in.Instance == nil {
		return saveErr
	}
	if saveErr != nil {
		return errors.Join(saveErr, recordHistoryFailure(in, saveErr))
	}
	c, err := checkpointInstance(ctx, in, agentWorkdir(in, t.cfg.Mounts), t.started)
	if err != nil {
		return errors.Join(err, recordHistoryFailure(in, err))
	}
	if c.NativeID != "" {
		ui.Storef("session checkpoint · %s", c.NativeID)
	}
	return nil
}

func recordHistoryFailure(in Input, err error) error {
	return proveohome.RecordHistory(in.Instance.HistoryDir(),
		proveohome.HistoryStatus{Attempt: in.Sid, State: "retry-needed", Detail: err.Error()})
}

func checkpointInstance(ctx context.Context, in Input, cwd string, since time.Time) (proveohome.Checkpoint, error) {
	tip := ""
	if in.Instance.Isolated() && in.RepoRoot != "" {
		ref := sbx.CloneRefs(in.Sid) + "/" + in.Instance.Branch()
		if out, err := exec.CommandContext(ctx, "git", "-C", in.RepoRoot, "rev-parse", "--verify", ref+"^{commit}").Output(); err == nil {
			tip = strings.TrimSpace(string(out))
		}
	}
	if err := ctx.Err(); err != nil {
		return proveohome.Checkpoint{}, fmt.Errorf("session checkpoint interrupted: %w", err)
	}
	return proveohome.CheckpointSession(in.Instance.HistoryDir(), in.HomeRoot, in.Target, cwd, in.Sid, tip, since)
}

func startPeriodicHistory(ctx context.Context, in Input, cfg sbx.RunConfig, since time.Time) func() {
	if !in.Instance.Isolated() || !proveohome.HistorySupported(in.Target) || in.HomeRoot == "" {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case at := <-ticker.C:
				if err := in.Instance.checkVM(); err != nil {
					continue
				}
				probe, stop := context.WithTimeout(ctx, 15*time.Second)
				_, err := guestRunIn(probe, sbx.SaveSessionArgs(cfg.Name, in.Target)...)
				stop()
				if err != nil || ctx.Err() != nil {
					continue
				}
				attempt := in
				attempt.Sid += "-checkpoint-" + at.UTC().Format("20060102T150405")
				if _, err := checkpointInstance(ctx, attempt, agentWorkdir(in, cfg.Mounts), since); err != nil {
					ui.Logf("session checkpoint: %v", err)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
