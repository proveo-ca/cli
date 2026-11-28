// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package dashboard

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

// prInfo is a task's pull request, or why none can be read.
type prInfo struct {
	Number  int
	URL     string
	State   string // OPEN, MERGED or CLOSED
	Draft   bool
	Review  string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED or ""
	Note    string // why there is no PR status, e.g. gh is missing
	Head    string // the branch the PR was found by
	Base    string // the branch the PR merges into
	Title   string
	Tickets []string // Linear issues the PR names, strongest mention first
	Plan    string   // the plan the PR follows: a link or a repo path
	Created time.Time
	Links   []string // the links its description carries, artifacts among them
}

// label is "#104 · draft", "#104 · approved", "#104 · merged".
func (p prInfo) label() string {
	if p.Number == 0 {
		return ""
	}
	state := "open"
	switch {
	case p.State == "MERGED":
		state = "merged"
	case p.State == "CLOSED":
		state = "closed"
	case p.Draft:
		state = "draft"
	case p.Review == "APPROVED":
		state = "approved"
	case p.Review == "CHANGES_REQUESTED":
		state = "changes requested"
	case p.Review == "REVIEW_REQUIRED":
		state = "review pending"
	}
	return fmt.Sprintf("#%d · %s", p.Number, state)
}

// ghPR is one row of `gh pr list --json`.
type ghPR struct {
	Number            int
	URL               string
	State             string
	IsDraft           bool
	ReviewDecision    string
	IsCrossRepository bool
	BaseRefName       string
	Title             string
	Body              string
	HeadRefName       string
	Files             []struct{ Path string }
	CreatedAt         time.Time
}

var (
	ghPath    = func() error { _, err := exec.LookPath("gh"); return err }
	ghCommand = providerCommand
)

// findPRs asks gh for the PR of each head branch, in heads' order; a branch without one adds nothing.
// A lookup by commit is deliberately absent: a launch commit already on the base names the PR that merged it.
func findPRs(dir string, heads []string) ([]prInfo, string) {
	if ghPath() != nil {
		return nil, "install gh for PR status"
	}
	if _, err := ghCommand(dir, "gh", "auth", "token"); err != nil {
		return nil, "gh auth login for PR status"
	}
	var found []prInfo
	for _, head := range heads {
		if head == "" || head == "HEAD" {
			continue
		}
		out, err := ghCommand(dir, "gh", "pr", "list", "--head", head, "--state", "all", "--limit", "10",
			"--json", "number,url,state,isDraft,reviewDecision,isCrossRepository,baseRefName,title,body,headRefName,files,createdAt")
		var prs []ghPR
		if err != nil || json.Unmarshal(out, &prs) != nil {
			continue
		}
		prs = slices.DeleteFunc(prs, func(p ghPR) bool { return p.IsCrossRepository || p.HeadRefName != head })
		slices.SortStableFunc(prs, func(a, b ghPR) int {
			return boolRank(b.State == "OPEN") - boolRank(a.State == "OPEN")
		})
		if len(prs) == 0 || slices.ContainsFunc(found, func(f prInfo) bool { return f.Number == prs[0].Number }) {
			continue
		}
		p := prs[0]
		var files []string
		for _, f := range p.Files {
			files = append(files, f.Path)
		}
		found = append(found, prInfo{Number: p.Number, URL: p.URL, State: p.State, Draft: p.IsDraft, Review: p.ReviewDecision,
			Head: head, Base: p.BaseRefName, Title: p.Title, Tickets: linearRefs(p.Title, p.HeadRefName, p.Body),
			Plan: planRef(p.Body, files), Created: p.CreatedAt, Links: prLinks(p.Body, p.URL)})
	}
	return found, ""
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// taskPRs reads the PRs of every branch the task works on: the sandbox's checkouts when it runs, its carried branch otherwise.
func taskPRs(t Task, cwd string) ([]prInfo, string) {
	dir := t.Repo
	if dir == "" {
		dir = cwd
	}
	heads := slices.Clone(t.Branches)
	if len(heads) == 0 {
		heads = branches(readGuestWork(t))
	}
	if t.Branch != "" && !slices.Contains(heads, t.Branch) {
		heads = append(heads, t.Branch)
	}
	return findPRs(dir, heads)
}

var (
	linearURLRef   = regexp.MustCompile(`linear\.app/[^/\s]+/issue/([A-Za-z][A-Za-z0-9]{1,9}-\d{1,6})`)
	linearMagicRef = regexp.MustCompile(`(?i)\b(?:fix(?:es|ed)?|close[sd]?|resolve[sd]?|part of|related to|refs?|contributes to)\s+([A-Z][A-Z0-9]{1,9}-\d{1,6})\b`)
	linearBareRef  = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,9}-\d{1,6})\b`)
	linearBranch   = regexp.MustCompile(`(?i)(?:^|[/_])([a-z][a-z0-9]{1,9}-\d{1,6})(?:[-_]|$)`)
	planLine       = regexp.MustCompile(`(?im)^[\s>*_-]*plan[*_]*\s*:\s*(.+)$`)
	mdLink         = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)
	planPath       = regexp.MustCompile(`(?:https?://\S+/)?_spec/_plans/[A-Za-z0-9._/-]+\.(?:md|markdown|puml)`)
	// notTicket holds keys of identifiers that look like issue IDs and are not.
	notTicket = map[string]bool{"UTF": true, "ISO": true, "SHA": true, "RFC": true, "TLS": true, "SSL": true, "HTTP": true,
		"AES": true, "RSA": true, "CVE": true, "ES": true, "PR": true, "GPT": true, "WCAG": true, "IPV": true, "UUID": true}
)

// linearRefs lists the Linear issues a PR names: links, closing keywords, the title, the branch, then the prose.
func linearRefs(title, branch, body string) []string {
	var out []string
	add := func(id string) {
		id = strings.ToUpper(id)
		key, _, _ := strings.Cut(id, "-")
		if !notTicket[key] && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, re := range []*regexp.Regexp{linearURLRef, linearMagicRef} {
		for _, m := range re.FindAllStringSubmatch(title+"\n"+body, -1) {
			add(m[1])
		}
	}
	for _, m := range linearBareRef.FindAllStringSubmatch(title, -1) {
		add(m[1])
	}
	if m := linearBranch.FindStringSubmatch(branch); m != nil {
		add(m[1])
	}
	for _, m := range linearBareRef.FindAllStringSubmatch(body, -1) {
		add(m[1])
	}
	return out
}

// planRef is the plan a PR follows: its "Plan:" line, else a _spec/_plans path or link in the body, else a plan it changes.
func planRef(body string, files []string) string {
	if m := planLine.FindStringSubmatch(body); m != nil {
		value := strings.TrimSpace(m[1])
		if l := mdLink.FindStringSubmatch(value); l != nil {
			value = l[1]
		}
		if value = strings.Trim(strings.Fields(value + " ")[0], "`<>\"'"); value != "" {
			return value
		}
	}
	if p := planPath.FindString(body); p != "" {
		return p
	}
	for _, f := range files {
		if planPath.MatchString(f) {
			return f
		}
	}
	return ""
}

var bodyLink = regexp.MustCompile(`https?://[^\s<>()\[\]"'` + "`" + `]+`)

// prLinks are the description's links in order, its own URL and image links left out, 32 at most; dashboard.yml's
// link rules apply when they are shown.
func prLinks(body, self string) []string {
	var out []string
	for _, u := range bodyLink.FindAllString(body, -1) {
		u = strings.TrimRight(u, ".,;:!?*_")
		lower := strings.ToLower(u)
		if u == self || strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".gif") || strings.HasSuffix(lower, ".jpg") ||
			strings.Contains(lower, "/user-attachments/assets/") || slices.Contains(out, u) {
			continue
		}
		if out = append(out, u); len(out) == 32 {
			break
		}
	}
	return out
}
