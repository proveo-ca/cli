// SPEC: _spec/internal/sbx/instance-lifecycle.puml, _spec/internal/sbx/clone-workspace.puml
package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/proveo-ca/proveo/internal/proveohome"
	"github.com/proveo-ca/proveo/internal/sbx"
	"github.com/proveo-ca/proveo/internal/workspace"
)

func (i *Instance) PrepareWorkspace(runDir string, print bool) (work, bundle string, err error) {
	if i.Repo != "" {
		bundle, err = i.prepareBundle(runDir, print)
		return i.Workspace, bundle, err
	}
	dest := i.worktree()
	if print {
		dest = filepath.Join(runDir, "workspace")
	}
	if fi, err := os.Stat(dest); err == nil && fi.IsDir() {
		return dest, "", nil
	}
	if within(canonicalInstancePath(filepath.Dir(dest)), i.Workspace) {
		return "", "", errors.New("instance state must be outside the source directory for a private copy")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", "", err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".workspace-")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := os.CopyFS(stage, os.DirFS(i.Workspace)); err != nil {
		return "", "", fmt.Errorf("copy private workspace: %w", err)
	}
	return dest, "", os.Rename(stage, dest)
}

func (i *Instance) prepareBundle(runDir string, print bool) (string, error) {
	dir := i.bundleDir()
	args := []string{"HEAD"}
	if print {
		cached := filepath.Join(dir, "base.bundle")
		if fi, err := os.Stat(cached); err == nil && fi.Mode().IsRegular() {
			return cached, nil
		}
		dir = filepath.Join(runDir, "git", i.Base)
		if out, err := exec.Command("git", "-C", i.Repo, "rev-parse", "HEAD").Output(); err == nil &&
			strings.TrimSpace(string(out)) != i.Base {
			out, err := exec.Command("git", "-C", i.Repo, "for-each-ref", "--count=1", "--format=%(refname)", "--points-at", i.Base).Output()
			if err != nil || len(strings.Fields(string(out))) == 0 {
				return "", fmt.Errorf("no host ref points at the recorded Git revision %s", i.Base)
			}
			args = strings.Fields(string(out))
		}
	} else {
		ref := "refs/proveo-instance-base/" + i.ID + "-" + i.Base
		if out, err := exec.Command("git", "-C", i.Repo, "update-ref", ref, i.Base).CombinedOutput(); err != nil {
			return "", fmt.Errorf("pin Git base %s: %w: %s", i.Base, err, strings.TrimSpace(string(out)))
		}
		args = []string{ref}
	}
	path := filepath.Join(dir, "base.bundle")
	if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(dir, ".bundle-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	tmp := filepath.Join(stage, "base.bundle")
	cmd := exec.Command("git", append([]string{"-C", i.Repo, "bundle", "create", tmp}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("bundle Git base %s: %w: %s", i.Base, err, strings.TrimSpace(string(out)))
	}
	return path, os.Rename(tmp, path)
}

func (i *Instance) PrepareResumeTip(runDir, tip string, print bool) (string, error) {
	if !instanceBase.MatchString(tip) || i.Repo == "" {
		return "", fmt.Errorf("invalid resume commit %q", tip)
	}
	copy := *i
	copy.Base = tip
	return copy.prepareBundle(runDir, print)
}

func (in Input) instanceCheckout() sbx.KitCommand {
	base := in.Instance.Base
	if in.InstanceResumeTip != "" {
		base = in.InstanceResumeTip
	}
	return sbx.KitCommand{Description: "prepare the instance branch", Command: []string{"sh", "-ec", instanceCheckoutScript,
		"prepare-instance", in.InstanceCloneRoot, base, in.Instance.Branch(), in.InstanceBundle, in.ScopeRel}}
}

func (in Input) instanceMounts(mounts []sbx.Mount) []sbx.Mount {
	src, dest := in.Instance.Workspace, in.InstanceWork
	if in.InstanceCloneRoot != "" {
		src, dest = in.Instance.Repo, in.InstanceCloneRoot
	}
	out := []sbx.Mount{{Host: dest}}
	shared := map[string]bool{}
	for _, l := range in.Links {
		if l.Action == workspace.LinkMounted && !in.ReadOnlyShares {
			shared[canonicalInstancePath(l.Target)] = true
		}
	}
	for _, m := range mounts {
		if within(canonicalInstancePath(m.Host), src) {
			continue
		}
		if within(canonicalInstancePath(m.Host), canonicalInstancePath(in.HomeRoot)) ||
			within(canonicalInstancePath(m.Host), canonicalInstancePath(in.RunDir)) ||
			m.Host == in.OutputDir || shared[canonicalInstancePath(m.Host)] {
			out = append(out, m)
		} else {
			m.ReadOnly = true
			out = append(out, m)
		}
	}
	if in.InstanceBundle != "" {
		out = append(out, sbx.Mount{Host: filepath.Dir(in.InstanceBundle), ReadOnly: true})
	}
	if dir := os.Getenv("PROVEO_SCHEDULE_REPORT_DIR"); filepath.IsAbs(dir) && in.Instance.Job != "" {
		root := filepath.Join(proveohome.Root(os.Getenv), "logs", "schedule")
		dir = canonicalInstancePath(dir)
		if strings.HasSuffix(dir, ".handoff") && within(dir, canonicalInstancePath(root)) {
			out = append(out, sbx.Mount{Host: dir})
		}
	}
	return out
}

const instanceCheckoutScript = `repo=$1 base=$2 branch=$3 bundle=$4 scope=$5
if ! git -C "$repo" show-ref --verify --quiet "refs/heads/$branch"; then
  if ! git -C "$repo" cat-file -e "$base^{commit}" 2>/dev/null; then
    head=$(git bundle list-heads "$bundle")
    git -C "$repo" fetch "$bundle" "${head#* }"
  fi
  git -C "$repo" branch "$branch" "$base"
fi
git -C "$repo" switch "$branch"
case "$branch" in proveo/*)
  git -C "$repo" config "branch.$branch.remote" .
  git -C "$repo" config "branch.$branch.merge" "refs/heads/$branch"
esac
if [ -n "$scope" ]; then git -C "$repo" sparse-checkout set --cone -- "$scope"; fi
`
