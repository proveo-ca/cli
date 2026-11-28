// SPEC: _spec/internal/sbx/instance-lifecycle.puml
package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/proveo-ca/proveo/internal/backend/sandbox"
	"github.com/proveo-ca/proveo/internal/proveohome"
	"github.com/proveo-ca/proveo/internal/sbx"
	"github.com/proveo-ca/proveo/internal/ui"
	"github.com/proveo-ca/proveo/internal/workspace"
)

func prepareResume(rs *Spec, p *Params) error {
	id, cont := p.ResumeID, p.Continue
	if i := rs.Backend.Instance; i != nil && (cont || id != "") && proveohome.HistorySupported(p.Target) {
		c, err := proveohome.FindCheckpoint(i.HistoryDir(), id)
		if err == nil {
			if c.Target != p.Target {
				return fmt.Errorf("checkpoint belongs to %s", c.Target)
			}
			if !p.PrintOnly {
				home := instanceHome(rs, proveohome.Root(os.Getenv), false)
				if err := os.MkdirAll(home, 0o700); err != nil {
					return err
				}
				if err := proveohome.RestoreCheckpoint(i.HistoryDir(), home, c); err != nil {
					return fmt.Errorf("restore session %s: %w", c.NativeID, err)
				}
			}
			id, cont = c.NativeID, false
			if !i.HasVM() && i.Isolated() && c.GitTip != "" {
				bundle, err := i.PrepareResumeTip(rs.RunDir, c.GitTip, p.PrintOnly)
				if err != nil {
					return fmt.Errorf("prepare session Git revision: %w", err)
				}
				rs.Backend.InstanceBundle, rs.Backend.RestoreTip = bundle, c.GitTip
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read native session checkpoint: %w", err)
		}
	}
	args, err := proveohome.ResumeArgs(p.Target, id, cont, p.ListSessions)
	if err == nil {
		p.Extra = append(args, p.Extra...)
	}
	return err
}

func prepareInstance(rs *Spec, p *Params, d Deps) (func(), error) {
	explicit := p.NewInstance || p.Instance != ""
	if hasAddon(p.Addons, addonBrowser) && (!explicit || p.BrowserProfile != "") {
		if explicit {
			return nil, fmt.Errorf("a named browser profile selects its own sandbox; use it without --new or --instance")
		}
		return nil, nil
	}

	ws := &rs.Workspace.WS
	effective := realPathOf(ws.InputDir)
	repo := ws.RepoRoot
	if repo != "" {
		repo = realPathOf(repo)
	}
	legacyKey, legacySource := effective, effective
	if p.Clone && repo != "" && workspace.LinkedWorktree(effective) {
		if src, err := workspace.CloneSource(effective); err == nil {
			legacyKey, legacySource = repo, realPathOf(src.Main)
		}
	}
	req := sandbox.InstanceRequest{
		State: realPathOf(StateDir()), Target: p.Target, Workspace: effective, Repo: repo,
		New: p.NewInstance, ID: p.Instance, Task: p.Task, Branch: p.Branch, Print: p.PrintOnly,
		Job: os.Getenv("PROVEO_SCHEDULE_JOB"),
	}
	if !p.PrintOnly || p.Instance != "" {
		req.LegacyName = sbx.SandboxName(p.Target, legacyKey)
	}
	if !p.PrintOnly && !p.Yes && !scheduled() && askOnTerminal() {
		req.Pick = d.PickInstance
	}
	if p.NewInstance && repo != "" && p.CloneSet && !p.Clone {
		return nil, fmt.Errorf("--new in a Git workspace needs a private clone")
	}
	if explicit && repo == "" && p.CloneSet && p.Clone {
		return nil, fmt.Errorf("--clone needs a Git repository; --new can copy a plain directory without it")
	}
	if explicit && repo != "" {
		if rel, err := filepath.Rel(repo, effective); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("workspace %s is outside repository %s", effective, repo)
		}
	}
	if fi, err := os.Stat(effective); explicit && (err != nil || !fi.IsDir()) {
		return nil, fmt.Errorf("instance workspace %s must be a directory", effective)
	}
	i, err := sandbox.SelectInstance(req)
	if err != nil || i == nil {
		return nil, err
	}
	rs.Backend.Instance = i
	var release func()
	if !p.PrintOnly {
		release, err = i.Begin(sbx.AgentName(sandbox.Harness(sandbox.Input{Target: p.Target, Man: rs.Man})), legacySource)
		if err != nil {
			return nil, err
		}
	}
	if i.Isolated() {
		if repo != i.Repo || (repo != "" && p.CloneSet && !p.Clone) {
			if release != nil {
				release()
			}
			return nil, fmt.Errorf("instance %s requires its original workspace and a private checkout", i.ID)
		}
		work, bundle, err := i.PrepareWorkspace(rs.RunDir, p.PrintOnly)
		if err != nil {
			if release != nil {
				release()
			}
			return nil, err
		}
		rs.Backend.InstanceWork, rs.Backend.InstanceBundle = work, bundle
		if i.HasVM() && i.CheckoutTip != "" {
			bundle, err := i.PrepareResumeTip(rs.RunDir, i.CheckoutTip, p.PrintOnly)
			if err != nil {
				if release != nil {
					release()
				}
				return nil, err
			}
			rs.Backend.RestoreTip, rs.Backend.InstanceBundle = i.CheckoutTip, bundle
		}
		p.Clone = repo != ""
		if p.Clone {
			p.CloneSet = true
		}
	}
	return release, nil
}

func instanceFacts(i *sandbox.Instance) []ui.Fact {
	if i == nil {
		return nil
	}
	facts := []ui.Fact{{Role: ui.RoleStore, Key: "instance", Value: i.ID, Detail: []string{i.Name}}}
	if i.Isolated() && i.Repo != "" {
		facts = append(facts, ui.Fact{Role: ui.RoleStore, Key: "branch", Value: i.Branch(), Detail: []string{i.Base}})
	}
	return facts
}

func instanceHome(rs *Spec, root string, print bool) string {
	if i := rs.Backend.Instance; i.Isolated() {
		if print {
			return filepath.Join(rs.RunDir, "home")
		}
		return i.Home(root)
	}
	return root
}

func configureInstanceOutput(rs *Spec, p *Params) error {
	i := rs.Backend.Instance
	out := p.Output
	if i.Isolated() {
		out = realPathOf(out)
		root := i.Repo
		if root == "" {
			root = i.Workspace
		}
		rel, err := filepath.Rel(root, out)
		switch {
		case err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)):
			out = filepath.Join(out, i.ID, rs.Sid)
		case rel == ".":
			return fmt.Errorf("instance output must be a directory below the checkout or outside it")
		case i.Repo != "":
			p.Output, rs.Workspace.WS.OutputDir = out, out
			return nil
		default:
			out = filepath.Join(rs.Backend.InstanceWork, rel)
		}
	}
	p.Output, rs.Workspace.WS.OutputDir = out, out
	if !p.PrintOnly && out != "" {
		if err := os.MkdirAll(out, 0o755); err != nil {
			return fmt.Errorf("output dir %s: %w", out, err)
		}
	}
	return nil
}
