// SPEC: _spec/_experiments/dashboard/task-picker.puml
package dashboard

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"gopkg.in/yaml.v3"

	"github.com/proveo-ca/proveo/internal/backend/sandbox"
	"github.com/proveo-ca/proveo/internal/choiceui"
	"github.com/proveo-ca/proveo/internal/credentials"
	"github.com/proveo-ca/proveo/internal/workspace"
)

// taskConfig is dashboard.yml's tasks block: the picker's sources, in order.
type taskConfig struct {
	Sources []pickSource `yaml:"sources"`
}

// pickSource is one section of the picker; task_sources.yml documents each kind's fields.
type pickSource struct {
	Name   string `yaml:"name"`
	Kind   string `yaml:"kind"`
	Prompt string `yaml:"prompt"`
	Branch string `yaml:"branch"` // the working branch a pick defaults to; empty keeps proveo/<instance>, local only

	Dir        string   `yaml:"dir"`
	Depth      int      `yaml:"depth"`
	Extensions []string `yaml:"extensions"`
	HideStatus []string `yaml:"hide_status"`

	Search string `yaml:"search"`
	Stacks bool   `yaml:"stacks"`

	TokenVar   string   `yaml:"token_var"`
	HideStates []string `yaml:"hide_states"`

	Run     []string `yaml:"run"`
	Timeout string   `yaml:"timeout"`
}

//go:embed task_sources.yml
var defaultTaskSourcesYAML []byte

// defaultTaskSources are proveo's own sources, read from task_sources.yml.
var defaultTaskSources = func() []pickSource {
	var c taskConfig
	dec := yaml.NewDecoder(bytes.NewReader(defaultTaskSourcesYAML))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		panic(err)
	}
	if err := c.validate(); err != nil {
		panic(err)
	}
	return c.Sources
}()

func (c taskConfig) validate() error {
	for i, s := range c.Sources {
		where := fmt.Sprintf("tasks.sources[%d]", i)
		if s.Name != "" {
			where += " (" + s.Name + ")"
		}
		switch {
		case s.Name == "":
			return errors.New(where + ": a source needs a name")
		case s.Kind == "plans" && s.Dir == "":
			return errors.New(where + ": a plans source needs a dir")
		case s.Kind == "linear" && s.TokenVar == "":
			return errors.New(where + ": a linear source needs a token_var")
		case s.Kind == "command" && len(s.Run) == 0:
			return errors.New(where + ": a command source needs run")
		case !slices.Contains([]string{"plans", "github", "linear", "command"}, s.Kind):
			return fmt.Errorf("%s: kind %q is not plans, github, linear or command", where, s.Kind)
		}
		if s.Timeout != "" {
			if _, err := time.ParseDuration(s.Timeout); err != nil {
				return fmt.Errorf("%s: timeout: %w", where, err)
			}
		}
	}
	return nil
}

// taskSources are dashboard.yml's sources, else proveo's defaults.
func (m *dashModel) taskSources() []pickSource {
	if t := m.config().Tasks; t != nil && len(t.Sources) > 0 {
		return t.Sources
	}
	return defaultTaskSources
}

// task is one task a source found: its fields fill the source's prompt.
type task struct {
	fields map[string]string
	tone   string
	sort   time.Time
}

func (t task) item(prompt, branch string) choiceui.TaskItem {
	title := t.fields["title"]
	if id := t.fields["id"]; id != "" && !strings.HasPrefix(title, id) && !strings.HasPrefix(id, "_") && !strings.Contains(id, "/") {
		title = id + " " + title
	}
	p := t.fields["prompt"]
	if p == "" {
		p = fill(prompt, t.fields)
	}
	label := t.fields["task"]
	if label == "" {
		label = title
	}
	b := strings.Join(strings.Fields(fill(branch, t.fields)), "-")
	return choiceui.TaskItem{Title: safeText(title), Status: safeText(t.fields["status"]), Tone: t.tone,
		Prompt: strings.TrimSpace(p), Task: safeText(label), Branch: safeText(b)}
}

var placeholder = regexp.MustCompile(`\{\{\s*([a-z_]+)\s*\}\}`)

// fill puts fields into a prompt's {{field}}s; a field the task lacks reads as empty.
func fill(prompt string, fields map[string]string) string {
	return placeholder.ReplaceAllStringFunc(prompt, func(s string) string {
		return fields[placeholder.FindStringSubmatch(s)[1]]
	})
}

// loadSource reads one source's tasks in the workspace at cwd; a source that cannot answer says why.
func loadSource(s pickSource, cwd string) choiceui.TaskSection {
	var tasks []task
	var err error
	switch s.Kind {
	case "plans":
		tasks, err = planTasks(s, cwd)
	case "github":
		tasks, err = githubTasks(s, cwd)
	case "linear":
		tasks, err = linearTasks(s, cwd)
	case "command":
		tasks, err = commandTasks(s, cwd)
	}
	sec := choiceui.TaskSection{Title: s.Name}
	if err != nil {
		sec.Note = safeText(err.Error())
		return sec
	}
	for _, t := range tasks {
		sec.Items = append(sec.Items, t.item(s.Prompt, s.Branch))
	}
	return sec
}

var (
	statusLine = regexp.MustCompile(`(?im)^[\s'#*>-]*status:\s*(.+)$`)
	pumlTitle  = regexp.MustCompile(`(?m)^title\s+(.+)$`)
	mdTitle    = regexp.MustCompile(`(?m)^#\s+(.+)$`)
)

// planTasks are the plan files under the source's dir not marked done, newest edit first.
func planTasks(s pickSource, cwd string) ([]task, error) {
	root, err := git(cwd, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		root = cwd
	}
	dir := filepath.Join(root, s.Dir)
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("no %s in this workspace", s.Dir)
	}
	var tasks []task
	var walk func(d string, depth int)
	walk = func(d string, depth int) {
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			p := filepath.Join(d, e.Name())
			st, err := os.Stat(p)
			if err != nil || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if st.IsDir() {
				if depth < s.Depth {
					walk(p, depth+1)
				}
				continue
			}
			if !slices.Contains(s.Extensions, strings.ToLower(filepath.Ext(p))) {
				continue
			}
			head, _ := readText(p)
			status := ""
			if m := statusLine.FindStringSubmatch(head); m != nil {
				status = strings.TrimSpace(m[1])
			}
			if planDone(status, s.HideStatus) {
				continue
			}
			title := ""
			if m := pumlTitle.FindStringSubmatch(head); m != nil && filepath.Ext(p) == ".puml" {
				title = planTitle(m[1])
			} else if m := mdTitle.FindStringSubmatch(head); m != nil {
				title = planTitle(m[1])
			}
			if title == "" {
				title = strings.TrimSuffix(e.Name(), filepath.Ext(p))
			}
			rel, _ := filepath.Rel(root, p)
			shown := status
			if shown == "" {
				shown, _ = filepath.Rel(dir, p)
			}
			tasks = append(tasks, task{fields: map[string]string{"id": rel, "path": rel, "title": title, "status": shown,
				"slug": strings.TrimSuffix(e.Name(), filepath.Ext(p))}, sort: st.ModTime()})
		}
	}
	walk(dir, 0)
	slices.SortStableFunc(tasks, func(a, b task) int { return b.sort.Compare(a.sort) })
	return tasks, nil
}

// planTitle is a title's first line without PlantUML markup: no **bold**, no $variables, no line breaks after the first.
func planTitle(s string) string {
	s, _, _ = strings.Cut(s, `\n`)
	s = planMarkup.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

var planMarkup = regexp.MustCompile(`\*\*|__|\$[A-Za-z_]\w*|</?[a-z]+>`)

// planDone is true when a plan's status starts with one of the done words.
func planDone(status string, done []string) bool {
	s := strings.ToLower(strings.TrimLeft(status, " *_`\"'"))
	for _, w := range done {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" && strings.HasPrefix(s, w) {
			return true
		}
	}
	return false
}

// openPR is one of gh's open PRs with what the picker reports about it.
type openPR struct {
	Number                   int
	URL, Title               string
	HeadRefName, BaseRefName string
	IsDraft                  bool
	Mergeable                string
	StatusCheckRollup        []checkItem
	ReviewDecision           string
	LatestReviews            []reviewItem
	UpdatedAt                time.Time
}

func (p openPR) request() ReviewRequest {
	return ReviewRequest{Number: p.Number, URL: p.URL, Title: p.Title, Draft: p.IsDraft, Mergeable: p.Mergeable,
		Checks: checksOf(p.StatusCheckRollup), Decision: p.ReviewDecision, Reviews: reviewsOf(p.LatestReviews)}
}

// state is the PR's status line in plain words, and its tone.
func (p openPR) state() (string, string) {
	r := p.request()
	words := ansi.Strip(r.statusLine())
	switch {
	case r.Mergeable == "CONFLICTING" || r.Checks.Failed > 0:
		return words, "fail"
	case r.Draft || r.Checks.Pending > 0 || r.Mergeable != "MERGEABLE":
		return words, "warn"
	}
	return words, "ok"
}

var stackPart = regexp.MustCompile(`\s*\(\d+/\d+\)\s*`)

// githubTasks are the open PRs gh's search finds, a stack of PRs folded into one task when the source asks.
func githubTasks(s pickSource, cwd string) ([]task, error) {
	if ghPath() != nil {
		return nil, errors.New("install gh to list pull requests")
	}
	out, err := reviewCommand(cwd, "pr", "list", "--search", s.Search, "--state", "open", "--limit", "50", "--json",
		"number,url,title,headRefName,baseRefName,isDraft,mergeable,statusCheckRollup,reviewDecision,latestReviews,updatedAt")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return nil, errors.New("gh: " + lastLine(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("gh: %w", err)
	}
	var prs []openPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("gh: %w", err)
	}
	groups := [][]openPR{}
	if s.Stacks {
		groups = stacksOf(prs)
	} else {
		for _, p := range prs {
			groups = append(groups, []openPR{p})
		}
	}
	var tasks []task
	for _, g := range groups {
		tasks = append(tasks, stackTask(g))
	}
	slices.SortStableFunc(tasks, func(a, b task) int { return b.sort.Compare(a.sort) })
	return tasks, nil
}

// stacksOf groups PRs that build on each other, each stack ordered from the PR nearest the base branch up.
func stacksOf(prs []openPR) [][]openPR {
	byHead := map[string]int{}
	for i, p := range prs {
		byHead[p.HeadRefName] = i
	}
	parent := make([]int, len(prs))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i, p := range prs {
		if j, ok := byHead[p.BaseRefName]; ok {
			parent[find(i)] = find(j)
		}
	}
	order := map[int][]openPR{}
	var roots []int
	for i, p := range prs {
		r := find(i)
		if _, ok := order[r]; !ok {
			roots = append(roots, r)
		}
		order[r] = append(order[r], p)
	}
	var out [][]openPR
	for _, r := range roots {
		g := order[r]
		depth := func(p openPR) int {
			d := 0
			for seen := 0; seen < len(g); seen++ {
				i, ok := byHead[p.BaseRefName]
				if !ok {
					break
				}
				p, d = prs[i], d+1
			}
			return d
		}
		slices.SortStableFunc(g, func(a, b openPR) int { return depth(a) - depth(b) })
		out = append(out, g)
	}
	return out
}

// stackTask is one task for a stack: its title without the (n/m) parts, each PR on its own prompt line.
func stackTask(g []openPR) task {
	first, last := g[0], g[len(g)-1]
	title := strings.TrimSpace(stackPart.ReplaceAllString(first.Title, ""))
	var lines, blockers []string
	tone, newest := "ok", first.UpdatedAt
	counts := map[string]int{}
	var keys []string
	rank := map[string]int{"ok": 0, "warn": 1, "fail": 2}
	for _, p := range g {
		words, t := p.state()
		lines = append(lines, fmt.Sprintf("- #%d %s (%s) on %s: %s", p.Number, p.Title, p.URL, p.HeadRefName, words))
		if rank[t] > rank[tone] {
			tone = t
		}
		if p.UpdatedAt.After(newest) {
			newest = p.UpdatedAt
		}
		for _, w := range strings.Split(words, " · ") {
			if !slices.Contains(keys, w) {
				keys = append(keys, w)
			}
			counts[w]++
			if w != "ready" && !slices.Contains(blockers, w) {
				blockers = append(blockers, w)
			}
		}
	}
	status := strings.Join(keys, " · ")
	id := fmt.Sprintf("#%d", first.Number)
	if len(g) > 1 {
		var parts []string
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
		}
		status = fmt.Sprintf("%d PRs: %s", len(g), strings.Join(parts, " · "))
		id = fmt.Sprintf("#%d–#%d", first.Number, last.Number)
	}
	if len(blockers) == 0 {
		blockers = []string{"nothing blocks them"}
	}
	return task{fields: map[string]string{"id": id, "title": title, "status": status, "url": first.URL,
		"branch": last.HeadRefName, "prs": strings.Join(lines, "\n"), "blockers": strings.Join(blockers, ", "),
		"task": title}, tone: tone, sort: newest}
}

// tokenFor is a source's token variable from the dashboard's environment, else the workspace .env.
var tokenFor = func(cwd, name string) string {
	repo, _ := git(cwd, "rev-parse", "--show-toplevel")
	return credentials.ProviderLookup(workspace.ProjectEnvFile(cwd, repo))(name)
}

// linearTasks are the issues assigned to the token's user whose state type the source does not hide.
func linearTasks(s pickSource, cwd string) ([]task, error) {
	token := tokenFor(cwd, s.TokenVar)
	if token == "" {
		return nil, fmt.Errorf("set %s in the workspace .env or the environment to list your assigned issues", s.TokenVar)
	}
	var v struct {
		Viewer struct {
			AssignedIssues struct {
				Nodes []struct {
					Identifier, Title, URL string
					UpdatedAt              time.Time
					State                  struct{ Name, Type string }
				}
			}
		}
	}
	q := `query($hide: [String!]) { viewer { assignedIssues(first: 100, orderBy: updatedAt,
		filter: { state: { type: { nin: $hide } } }) { nodes { identifier title url updatedAt state { name type } } } } }`
	if err := linearQuery(token, q, map[string]any{"hide": s.HideStates}, 15*time.Second, &v); err != nil {
		return nil, err
	}
	order := map[string]int{"started": 0, "unstarted": 1, "triage": 2, "backlog": 3}
	var tasks []task
	for _, n := range v.Viewer.AssignedIssues.Nodes {
		tone := ""
		if n.State.Type == "started" {
			tone = "ok"
		}
		tasks = append(tasks, task{fields: map[string]string{"id": n.Identifier, "title": n.Title, "url": n.URL,
			"status": n.State.Name, "type": n.State.Type, "task": n.Identifier + ": " + n.Title}, tone: tone, sort: n.UpdatedAt})
	}
	slices.SortStableFunc(tasks, func(a, b task) int {
		oa, ok := order[a.fields["type"]]
		if !ok {
			oa = 9
		}
		ob, ok := order[b.fields["type"]]
		if !ok {
			ob = 9
		}
		if oa != ob {
			return oa - ob
		}
		return b.sort.Compare(a.sort)
	})
	return tasks, nil
}

// commandTasks run the source's command in the workspace and read the JSON array of tasks it prints.
func commandTasks(s pickSource, cwd string) ([]task, error) {
	timeout := 20 * time.Second
	if d, err := time.ParseDuration(s.Timeout); err == nil && d > 0 {
		timeout = d
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Run[0], s.Run[1:]...)
	cmd.Dir = cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s took over %s", s.Run[0], timeout)
	}
	if err != nil {
		if why := lastLine(stderr.String()); why != "" {
			return nil, fmt.Errorf("%s: %s", s.Run[0], why)
		}
		return nil, fmt.Errorf("%s: %w", s.Run[0], err)
	}
	var raw []map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("%s printed no JSON array of tasks: %w", s.Run[0], err)
	}
	var tasks []task
	for _, r := range raw {
		f := map[string]string{}
		for k, v := range r {
			if s, ok := v.(string); ok {
				f[k] = s
			} else if v != nil {
				f[k] = fmt.Sprint(v)
			}
		}
		if f["title"] == "" {
			continue
		}
		tasks = append(tasks, task{fields: f, tone: f["tone"]})
	}
	return tasks, nil
}

// pickCached is a source's last answer, shown at once the next time the picker opens.
type pickCached struct {
	section choiceui.TaskSection
	at      time.Time
}

type tasksEvent struct {
	gen, index int
	key        string
	section    choiceui.TaskSection
}

// openTaskPicker shows the new-instance card and asks every source for its tasks; a fresh answer shows at once.
func (m *dashModel) openTaskPicker() tea.Cmd {
	sources := m.taskSources()
	m.pickGen++
	gen, cwd := m.pickGen, m.CWD
	sections := make([]choiceui.TaskSection, len(sources))
	var cmds []tea.Cmd
	for i, s := range sources {
		key := s.Kind + "\x00" + s.Name
		c, ok := m.pickCache[key]
		sections[i] = choiceui.TaskSection{Title: s.Name, Note: "loading…"}
		if ok {
			sections[i] = c.section
			if time.Since(c.at) < time.Minute {
				continue
			}
		}
		cmds = append(cmds, func() tea.Msg { return tasksEvent{gen: gen, index: i, key: key, section: loadSource(s, cwd)} })
	}
	m.taskPicker = choiceui.NewTaskPicker(sections, m.width, m.height)
	return tea.Batch(cmds...)
}

// receiveTasks fills the open picker's section and keeps the answer for the next opening.
func (m *dashModel) receiveTasks(e tasksEvent) {
	if m.pickCache == nil {
		m.pickCache = map[string]pickCached{}
	}
	m.pickCache[e.key] = pickCached{section: e.section, at: time.Now()}
	if m.taskPicker != nil && e.gen == m.pickGen {
		m.taskPicker.SetSection(e.index, e.section)
	}
}

// startInstance hands the terminal to `proveo run --queue` (its harness list, scope page and card, then the queue and
// an attach), with the picked task's label and prompt when there is one; the dashboard redraws when it returns.
func (m *dashModel) startInstance(label, prompt, branch string) tea.Cmd {
	run := m.runner
	if run == nil {
		run = foreground
	}
	if branch != "" {
		if err := sandbox.ValidBranch(branch); err != nil {
			m.fail(err)
			return nil
		}
	}
	args := []string{"run", "--queue"}
	if prompt != "" {
		f, err := os.CreateTemp("", "proveo-task-*.md")
		if err != nil {
			m.fail(fmt.Errorf("new instance: %w", err))
			return nil
		}
		_, werr := f.WriteString(prompt + "\n")
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(f.Name())
			m.fail(fmt.Errorf("new instance: %w", werr))
			return nil
		}
		m.promptTemp = f.Name()
		args = append(args, "--prompt-file", f.Name())
	}
	if label != "" {
		args = append(args, "--task", label)
	}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	return run(m.Binary, args...)
}
