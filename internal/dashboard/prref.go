// SPEC: _spec/internal/dashboard/review-requests.puml
package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var prURLNumber = regexp.MustCompile(`github\.com/[^/]+/[^/]+/pull/(\d+)`)

// reviewPR is the pull request a ghost or a review run reads its changes from.
func reviewPR(t Task) (int, bool) {
	if t.Review != nil {
		return t.Review.Number, true
	}
	m := prURLNumber.FindStringSubmatch(t.Ticket)
	if m == nil || (t.Phase != "Review" && !strings.HasPrefix(t.Title, "Review #")) {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// prHead is a fetched pull request: the checkout it lives in, its head commit and the branch it merges into.
type prHead struct {
	repo, sha, base string
	at              time.Time
}

var (
	prHeadsMu sync.Mutex
	prHeads   = map[string]prHead{}
)

// gitFetch runs a git command that reaches the network; a fetch outlasts git's 4 s local budget.
var gitFetch = func(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// fetchPR brings pull request n's head into refs/proveo/review/<n> of the checkout at dir, at most once every two minutes.
func fetchPR(dir string, n int) (prHead, error) {
	root, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return prHead{}, fmt.Errorf("not a git checkout: %w", err)
	}
	key := root + "#" + strconv.Itoa(n)
	prHeadsMu.Lock()
	cached, ok := prHeads[key]
	prHeadsMu.Unlock()
	if ok && time.Since(cached.at) < 2*time.Minute {
		return cached, nil
	}
	ref := "refs/proveo/review/" + strconv.Itoa(n)
	if out, err := gitFetch(root, "fetch", "--quiet", "--no-tags", "origin", "+refs/pull/"+strconv.Itoa(n)+"/head:"+ref); err != nil {
		if line := lastLine(out); line != "" {
			return prHead{}, fmt.Errorf("git fetch of PR #%d: %s", n, line)
		}
		return prHead{}, fmt.Errorf("git fetch of PR #%d: %w", n, err)
	}
	sha, err := git(root, "rev-parse", ref)
	if err != nil {
		return prHead{}, err
	}
	h := prHead{repo: root, sha: sha, at: time.Now()}
	if out, err := ghCommand(root, "gh", "pr", "view", strconv.Itoa(n), "--json", "baseRefName"); err == nil {
		var v struct{ BaseRefName string }
		if json.Unmarshal(out, &v) == nil {
			h.base = v.BaseRefName
		}
	}
	prHeadsMu.Lock()
	prHeads[key] = h
	prHeadsMu.Unlock()
	return h, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return safeText(lines[len(lines)-1])
}

// prView is t read from its pull request's fetched head instead of a sandbox.
func prView(t Task, h prHead) Task {
	t.Repo, t.Tip, t.Guest, t.Base, t.Workdir = h.repo, h.sha, false, "", ""
	return t
}
