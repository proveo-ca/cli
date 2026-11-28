// SPEC: _spec/internal/dashboard/workspace-instances.puml
package dashboard

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// guestScript runs script inside t's running sandbox with args as $1...; the caller verified it runs.
var guestScript = func(t Task, script string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeoutFor(script))
	defer cancel()
	argv := append([]string{"exec", t.Sandbox, "--", "sh", "-c", script, "sh"}, args...)
	cmd := exec.CommandContext(ctx, "sbx", argv...)
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

// timeoutFor gives the tree and preview reads a minute on a loaded guest; the sampling probes stay short.
func timeoutFor(script string) time.Duration {
	switch script {
	case guestChangesScript, guestFilesScript, guestSidesScript:
		return time.Minute
	}
	return 6 * time.Second
}

// liveGuest reports whether t's sandbox can be read right now without starting it.
var liveGuest = func(t Task) bool { return t.Guest && t.Sandbox != "" && t.Workdir != "" && verifiedGuest(t) }

const guestFilesScript = `export GIT_OPTIONAL_LOCKS=0
cd "$1" || exit 1
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  git ls-files -z --cached --others --exclude-standard
else
  find . -type f -not -path './.git/*' -print0 | sed -z 's|^\./||'
fi | head -z -n 3000`

const guestChangesScript = `export GIT_OPTIONAL_LOCKS=0
cd "$1" || exit 1
pr=$2 launch=$3
shift 3
head=$(git rev-parse --abbrev-ref HEAD)
pick= mb=
if [ -n "$pr" ]; then
  for r in "origin/$pr" "$pr"; do
    if m=$(git merge-base HEAD "$r" 2>/dev/null); then pick=$r mb=$m; break; fi
  done
fi
if [ -z "$mb" ]; then
  best=
  for r in "$@"; do
    [ "$r" = "origin/$head" ] && continue
    m=$(git merge-base HEAD "$r" 2>/dev/null) || continue
    n=$(git rev-list --count "$m..HEAD")
    if [ -z "$best" ] || [ "$n" -lt "$best" ]; then best=$n pick=$r mb=$m; fi
  done
fi
if [ -z "$mb" ] && [ -n "$launch" ] && git cat-file -e "$launch^{commit}" 2>/dev/null; then pick=launch mb=$launch; fi
printf 'base\t%s\t%s\n' "$pick" "$mb"
if [ -n "$mb" ]; then
  git diff --name-status -z -M "$mb"
  git ls-files -z --others --exclude-standard | sed -z 's/^/??\x00/'
else
  git status --porcelain=v1 -z --untracked-files=all
fi`

const guestSidesScript = `export GIT_OPTIONAL_LOCKS=0
cd "$1" || exit 1
t=$(mktemp) || exit 1
trap 'rm -f "$t"' EXIT
side() { printf '%s %s\n' "$1" "$(wc -c < "$t")"; cat "$t"; : > "$t"; }
[ -n "$3" ] && git show "$3:$4" 2>/dev/null | head -c 131072 > "$t"
side before
[ -f "$2" ] && head -c 131072 -- "$2" > "$t"
side after`

// TaskFiles lists the task's sandbox files: the running guest, else the carried tip, else the base.
func TaskFiles(t Task) ([]Change, string, error) {
	var guestErr error
	if liveGuest(t) {
		out, err := guestScript(t, guestFilesScript, t.Workdir)
		if err == nil {
			return nulPaths(out), "sandbox", nil
		}
		guestErr = sandboxReadError(err)
	}
	for _, rev := range []struct{ label, rev string }{{"carried", t.Tip}, {"base", t.Base}} {
		if rev.rev == "" || t.Repo == "" {
			continue
		}
		out, err := git(t.Repo, "ls-tree", "-r", "-z", "--name-only", rev.rev)
		if err != nil {
			return nil, "", err
		}
		return nulPaths([]byte(out)), rev.label + " " + short(rev.rev), nil
	}
	return nil, "", guestErr
}

func nulPaths(out []byte) []Change {
	var files []Change
	for p := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if p = strings.TrimSpace(p); p != "" {
			files = append(files, Change{Path: p})
		}
	}
	slices.SortFunc(files, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	return files
}

// guestChanges is the running sandbox's diff from where its branch left the base, uncommitted work included.
func guestChanges(t Task, prBase string, bases []string) (ChangeSet, error) {
	out, err := guestScript(t, guestChangesScript, append([]string{t.Workdir, prBase, t.Base}, bases...)...)
	if err != nil {
		return ChangeSet{}, err
	}
	header, body, ok := strings.Cut(string(out), "\n")
	fields := strings.Split(header, "\t")
	if !ok || len(fields) != 3 || fields[0] != "base" {
		return ChangeSet{}, errors.New("unreadable change list")
	}
	return ChangeSet{Rows: parseGuestChanges(body), Base: fields[1], MergeBase: fields[2], Live: true}, nil
}

// parseGuestChanges reads `git diff --name-status -z`, `??` untracked rows, and `git status --porcelain -z`.
func parseGuestChanges(out string) []Change {
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	seen := map[string]bool{}
	var changes []Change
	add := func(c Change) {
		if c.Path != "" && !seen[c.Path] {
			seen[c.Path] = true
			changes = append(changes, c)
		}
	}
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		switch {
		case f == "":
		case f == "??" && i+1 < len(fields):
			add(Change{Kind: "?", Path: fields[i+1]})
			i++
		case len(f) > 3 && f[2] == ' ':
			code, path := strings.TrimSpace(f[:2]), f[3:]
			kind := code[:1]
			if code == "??" {
				kind = "?"
			}
			c := Change{Kind: kind, Path: path}
			if kind == "R" && i+1 < len(fields) {
				c.OldPath = fields[i+1]
				i++
			}
			add(c)
		case (strings.HasPrefix(f, "R") || strings.HasPrefix(f, "C")) && i+2 < len(fields):
			add(Change{Kind: f, OldPath: fields[i+1], Path: fields[i+2]})
			i += 2
		case len(f) <= 4 && i+1 < len(fields):
			add(Change{Kind: f, Path: fields[i+1]})
			i++
		}
	}
	slices.SortFunc(changes, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	return changes
}

// ChangeSet is a task's changes against the branch it will merge into.
type ChangeSet struct {
	Rows      []Change
	Base      string // the ref the merge base came from, e.g. origin/dev; "launch" for the recorded start
	MergeBase string
	Live      bool // read from the running sandbox
}

var commonBases = []string{"origin/dev", "origin/develop", "origin/main", "origin/master", "origin/trunk"}

// baseCandidates is the host repo's default branch, then the common bases.
func baseCandidates(repo string) []string {
	var out []string
	if repo != "" {
		if ref, err := git(repo, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
			out = append(out, ref)
		}
	}
	for _, r := range commonBases {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// Changes is the task's diff from its merge base: the PR's base, else the nearest candidate, else the launch commit.
func Changes(t Task, prBase, repo string) (ChangeSet, error) {
	if t.Repo != "" {
		repo = t.Repo
	}
	bases := baseCandidates(repo)
	var guestErr error
	if liveGuest(t) {
		cs, err := guestChanges(t, prBase, bases)
		if err == nil {
			return cs, nil
		}
		guestErr = sandboxReadError(err)
	}
	if t.Repo == "" || t.Tip == "" {
		return ChangeSet{}, guestErr
	}
	run := func(args ...string) (string, error) { return git(t.Repo, args...) }
	base, mb := pickBase(run, t.Tip, t.Branch, prBase, bases, t.Base)
	if mb == "" {
		return ChangeSet{}, nil
	}
	out, err := git(t.Repo, "diff", "--name-status", "-z", "-M", mb, t.Tip)
	if err != nil {
		return ChangeSet{}, err
	}
	return ChangeSet{Rows: parseGuestChanges(out), Base: base, MergeBase: mb}, nil
}

// pickBase finds where tip left its base: the PR's base first, then the candidate with the fewest commits since.
func pickBase(run func(...string) (string, error), tip, branch, prBase string, bases []string, launch string) (string, string) {
	if prBase != "" {
		for _, r := range []string{"origin/" + prBase, prBase} {
			if mb, err := run("merge-base", tip, r); err == nil && mb != "" {
				return r, mb
			}
		}
	}
	best, pick, pickMB := -1, "", ""
	for _, r := range bases {
		if branch != "" && r == "origin/"+branch {
			continue
		}
		mb, err := run("merge-base", tip, r)
		if err != nil || mb == "" {
			continue
		}
		out, err := run("rev-list", "--count", mb+".."+tip)
		n, convErr := strconv.Atoi(strings.TrimSpace(out))
		if err != nil || convErr != nil {
			continue
		}
		if best < 0 || n < best {
			best, pick, pickMB = n, r, mb
		}
	}
	if pickMB != "" {
		return pick, pickMB
	}
	if launch != "" {
		if _, err := run("cat-file", "-e", launch+"^{commit}"); err == nil {
			return "launch", launch
		}
	}
	return "", ""
}

// guestWorkScript lists where the sandbox's agent works: git top levels of agent processes, then of the rest by
// process count, each followed by the worktrees nested inside it; it falls back to $1 when no process sits in a repository.
const guestWorkScript = `export GIT_OPTIONAL_LOCKS=0
cd / || exit 1
for p in /proc/[0-9]*; do
  d=$(readlink "$p/cwd" 2>/dev/null) || continue
  [ "$d" = / ] && continue
  case $(cat "$p/comm" 2>/dev/null) in claude|codex|opencode|cursor-agent|hermes) echo "0 $d" ;; *) echo "1 $d" ;; esac
done | sort | uniq -c | sort -k2,2n -k1,1rn | while read -r n rank d; do
  git -C "$d" rev-parse --show-toplevel 2>/dev/null
done | awk '!seen[$0]++' > /tmp/.proveo-work.$$
[ -s /tmp/.proveo-work.$$ ] || printf '%s\n' "$1" > /tmp/.proveo-work.$$
while read -r top; do
  [ -n "$top" ] || continue
  printf 'work\t%s\t%s\t%s\n' "$top" "$(git -C "$top" rev-parse --abbrev-ref HEAD 2>/dev/null)" "$(git -C "$top" rev-parse HEAD 2>/dev/null)"
  git -C "$top" worktree list --porcelain 2>/dev/null | awk -v top="$top/" '
    /^worktree /{path=substr($0,10)} /^HEAD /{sha=$2} /^branch /{b=$2; sub("refs/heads/","",b); if (index(path, top)==1) printf "nested\t%s\t%s\t%s\n", path, b, sha}'
done < /tmp/.proveo-work.$$
rm -f /tmp/.proveo-work.$$`

// guestWork is one checkout an agent works in.
type guestWork struct {
	path, branch, sha string
	nested            bool
}

func parseGuestWork(out string) []guestWork {
	var work []guestWork
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 || (f[0] != "work" && f[0] != "nested") || f[1] == "" {
			continue
		}
		if slices.ContainsFunc(work, func(w guestWork) bool { return w.path == f[1] }) {
			continue
		}
		work = append(work, guestWork{path: f[1], branch: f[2], sha: f[3], nested: f[0] == "nested"})
	}
	return work
}

// readGuestWork asks the running sandbox where its agent works.
func readGuestWork(t Task) []guestWork {
	if !liveGuest(t) {
		return nil
	}
	out, err := guestScript(t, guestWorkScript, t.Workdir)
	if err != nil {
		return nil
	}
	return parseGuestWork(string(out))
}

// branches are the work's branch names, the agent's own checkout first.
func branches(work []guestWork) []string {
	var out []string
	for _, w := range work {
		if w.branch != "" && w.branch != "HEAD" && !slices.Contains(out, w.branch) {
			out = append(out, w.branch)
		}
	}
	return out
}

// sideCap is the most of one file a preview reads.
const sideCap = 128 << 10

// Sides is a file at the merge base and as the task has it now.
type Sides struct {
	Before, After string
	Truncated     bool
}

var errUnreadableSides = errors.New("unreadable file sides")

// parseSides reads "before <n>\n<n bytes>after <m>\n<m bytes>".
func parseSides(out []byte) (Sides, error) {
	rest := string(out)
	read := func(name string) (string, error) {
		head, body, ok := strings.Cut(rest, "\n")
		f := strings.Fields(head)
		if !ok || len(f) != 2 || f[0] != name {
			return "", errUnreadableSides
		}
		n, err := strconv.Atoi(f[1])
		if err != nil || n < 0 || n > len(body) {
			return "", errUnreadableSides
		}
		rest = body[n:]
		return body[:n], nil
	}
	before, err := read("before")
	if err != nil {
		return Sides{}, err
	}
	after, err := read("after")
	if err != nil {
		return Sides{}, err
	}
	return Sides{Before: before, After: after, Truncated: len(before) >= sideCap || len(after) >= sideCap}, nil
}

// ReadSides reads c before and after the task's changes: from the running sandbox, else from the carried revisions.
func ReadSides(t Task, c Change, mergeBase string) (Sides, error) {
	if mergeBase == "" {
		mergeBase = t.Base
	}
	old := c.Path
	if c.OldPath != "" {
		old = c.OldPath
	}
	for _, p := range []string{c.Path, old} {
		if !filepath.IsLocal(filepath.FromSlash(p)) {
			return Sides{}, errors.New("path lies outside the workspace")
		}
	}
	if liveGuest(t) {
		out, err := guestScript(t, guestSidesScript, t.Workdir, c.Path, mergeBase, old)
		if err != nil {
			return Sides{}, err
		}
		return parseSides(out)
	}
	tip := t.Tip
	if tip == "" {
		tip = t.Base
	}
	if t.Repo == "" || tip == "" {
		return Sides{}, errors.New("this task has no sandbox file to show yet")
	}
	var s Sides
	if mergeBase != "" && c.Kind != "A" && c.Kind != "?" {
		s.Before, _ = git(t.Repo, "show", mergeBase+":"+old)
	}
	if !strings.HasPrefix(c.Kind, "D") {
		after, err := git(t.Repo, "show", tip+":"+c.Path)
		if err != nil && s.Before == "" {
			return Sides{}, err
		}
		s.After = after
	}
	for _, side := range []*string{&s.Before, &s.After} {
		if len(*side) > sideCap {
			*side, s.Truncated = (*side)[:sideCap], true
		}
	}
	return s, nil
}

// guestActivityScript prints each agent's own state, then proveo's hook markers, each with whether its agent still runs.
const guestActivityScript = `for f in "$HOME"/.claude/sessions/*.json; do
  [ -f "$f" ] || continue
  pid=$(sed -n 's/.*"pid":\([0-9]*\).*/\1/p' "$f")
  st=$(sed -n 's/.*"status":"\([a-z]*\)".*/\1/p' "$f")
  at=$(sed -n 's/.*"statusUpdatedAt":\([0-9]*\).*/\1/p' "$f")
  alive=0; [ -n "$pid" ] && [ -d "/proc/$pid" ] && alive=1
  printf 'activity\tclaudecode\t%s\t%s\t%s\n' "$st" "$at" "$alive"
done
for f in /dev/shm/proveo-activity/*.json; do
  [ -f "$f" ] || continue
  pid=${f##*/}; pid=${pid%.json}; alive=0; [ -d "/proc/$pid" ] && alive=1
  printf 'marker\t%s\t%s\n' "$alive" "$(head -c 512 "$f" | tr -d '\n\t')"
done
l=$(ls -t "$HOME"/.codex/sessions/*/*/*/*.jsonl 2>/dev/null | head -n 1)
if [ -n "$l" ]; then
  e=$(tail -n 400 "$l" | grep -o '"type":"\(task_started\|task_complete\|turn_aborted\)"' | tail -n 1 | cut -d'"' -f4)
  alive=0; for p in /proc/[0-9]*; do [ "$(cat "$p/comm" 2>/dev/null)" = codex ] && { alive=1; break; }; done
  [ -n "$e" ] && printf 'activity\tcodex\t%s\t%s000\t%s\n' "$e" "$(stat -c %Y "$l")" "$alive"
fi
if [ -s /dev/shm/proveo-seed-refused ]; then
  printf 'seed\trefused\t%s\n' "$(head -c 300 /dev/shm/proveo-seed-refused | tr -d '\n\t')"
fi`

// sandboxReadError names a failed guest read in the operator's words.
func sandboxReadError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "signal: killed") {
		return errors.New("the sandbox took over a minute to answer; it is busy")
	}
	return fmt.Errorf("the sandbox did not answer: %w", err)
}
