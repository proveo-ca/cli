// SPEC: _spec/internal/dashboard/task-graph.puml
package dashboard

import (
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// blockedByLine is a plan's `' blocked_by: <ref>` line, or `blocked_by:` in Markdown; a line may list refs by commas.
var blockedByLine = regexp.MustCompile(`(?im)^[\s'#*>-]*blocked_by:\s*(.+)$`)

// planInfo is a plan file as the graph reads it: its slug, status, and the refs it waits on.
type planInfo struct {
	Slug, Path, Status string
	Done               bool
	BlockedBy          []string
}

// readPlan reads a plan's status and blocked_by lines; done words mark a plan that blocks nothing.
func readPlan(file string, done []string) planInfo {
	p := planInfo{Path: file, Slug: strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))}
	head, err := readText(file)
	if err != nil {
		return p
	}
	if m := statusLine.FindStringSubmatch(head); m != nil {
		p.Status = strings.TrimSpace(m[1])
	}
	p.Done = planDone(p.Status, done)
	p.BlockedBy = blockedBy(head)
	return p
}

// blockedBy lists every ref a plan's blocked_by lines name, in order, each once.
func blockedBy(text string) []string {
	var out []string
	for _, m := range blockedByLine.FindAllStringSubmatch(text, -1) {
		for _, ref := range strings.Split(m[1], ",") {
			ref = strings.Trim(strings.TrimSpace(ref), "`\"'")
			if ref != "" && !slices.Contains(out, ref) {
				out = append(out, ref)
			}
		}
	}
	return out
}

// refKind sorts a ref into the graph's keys: a Linear issue, a pull request, or a plan by its slug.
func refKind(ref string) (kind, key, label string) {
	ref = strings.TrimSpace(ref)
	if id := strings.ToUpper(ref); linearID.MatchString(id) {
		return "linear", "linear:" + id, id
	}
	if strings.HasPrefix(ref, "#") && isDigits(ref[1:]) {
		return "pr", "pr:" + ref[1:], ref
	}
	if u, err := url.Parse(ref); err == nil && u.Scheme == "https" {
		if _, tail, ok := strings.Cut(u.Path, "/pull/"); ok {
			n, _, _ := strings.Cut(tail, "/")
			return "pr", "pr:" + n, "#" + n
		}
		if u.Host == "linear.app" {
			_, tail, _ := strings.Cut(u.Path, "/issue/")
			id, _, _ := strings.Cut(tail, "/")
			return "linear", "linear:" + strings.ToUpper(id), strings.ToUpper(id)
		}
		if _, tail, ok := strings.Cut(u.Path, "/blob/"); ok {
			_, file, _ := strings.Cut(tail, "/")
			ref = file
		}
	}
	slug := strings.TrimSuffix(path.Base(ref), path.Ext(ref))
	return "plan", "plan:" + slug, slug
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// planFile finds a plan ref under the repository: a path as written, else a slug in the plans dir.
func planFile(root, dir, ref string) string {
	if u, err := url.Parse(ref); err == nil && u.Scheme == "https" {
		_, tail, ok := strings.Cut(u.Path, "/blob/")
		if !ok {
			return ""
		}
		_, ref, _ = strings.Cut(tail, "/")
	}
	var tries []string
	switch ext := strings.ToLower(filepath.Ext(ref)); {
	case ext == ".puml" || ext == ".md":
		tries = []string{filepath.Join(root, ref), filepath.Join(root, dir, ref)}
	case !strings.ContainsAny(ref, "/\\"):
		tries = []string{filepath.Join(root, dir, ref+".puml"), filepath.Join(root, dir, ref+".md")}
	}
	for _, p := range tries {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// planSource is the first plans source: where plans live and which status words end one.
func (m *dashModel) planSource() pickSource {
	for _, s := range m.taskSources() {
		if s.Kind == "plans" {
			return s
		}
	}
	return pickSource{Kind: "plans", Dir: "_spec/_plans"}
}

// readTaskPlan is the plan a task follows, its ticket when that names a plan, else the plan its PR names,
// with each plan it waits on.
func readTaskPlan(cwd string, src pickSource, ticket, prPlan string) (planInfo, map[string]planInfo) {
	root, err := git(cwd, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		root = cwd
	}
	var plan planInfo
	for _, ref := range []string{ticket, prPlan} {
		if kind, _, _ := refKind(ref); ref == "" || kind != "plan" {
			continue
		}
		if file := planFile(root, src.Dir, ref); file != "" {
			plan = readPlan(file, src.HideStatus)
			break
		}
	}
	refs := map[string]planInfo{}
	for _, ref := range plan.BlockedBy {
		if kind, key, _ := refKind(ref); kind == "plan" {
			if file := planFile(root, src.Dir, ref); file != "" {
				refs[key] = readPlan(file, src.HideStatus)
			}
		}
	}
	return plan, refs
}
