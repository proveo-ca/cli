// SPEC: _spec/internal/dashboard/review-requests.puml
package dashboard

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/choiceui"
	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

// ReviewRequest is an open pull request that asks the gh user for a review.
type ReviewRequest struct {
	Number               int
	URL, Title, Author   string
	Head, Base, Body     string
	Repo                 string // owner/name
	Files                []string
	Additions, Deletions int
	Created              time.Time
	Draft                bool
	Mergeable            string    // MERGEABLE, CONFLICTING, or UNKNOWN while GitHub computes it
	Checks               prChecks  // the head commit's checks, by how they ended
	Decision             string    // gh's reviewDecision: APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, or empty
	Reviews              prReviews // each reviewer's latest review, by state
}

// reviewState marks a ghost: a review request no proveo run has taken yet.
const reviewState schedule.QueueState = "review-required"

// reviewSearch finds the open PRs that ask the gh user directly, not through a team.
const reviewSearch = "user-review-requested:@me"

// reviewAgent runs reviews: /review-map and /review-demo are Claude Code skills.
const reviewAgent = "claudecode"

type (
	reviewsEvent struct {
		reviews []ReviewRequest
		err     error
	}
	reviewPollEvent   struct{}
	reviewQueuedEvent struct {
		url, name string
		number    int
		err       error
	}
)

// reviewCommand runs gh for the review list; a search with bodies and files outlasts the PR lookups' 3 s.
var reviewCommand = func(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	return cmd.Output()
}

// readReviewRequests lists the open PRs in dir's repository that ask the gh user for a review.
func readReviewRequests(dir string) ([]ReviewRequest, error) {
	if ghPath() != nil {
		return nil, errors.New("install gh to see review requests")
	}
	out, err := reviewCommand(dir, "pr", "list", "--search", reviewSearch, "--state", "open", "--limit", "30",
		"--json", "number,url,title,author,headRefName,baseRefName,body,files,additions,deletions,createdAt,isDraft,mergeable,statusCheckRollup,reviewDecision,latestReviews")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Number                   int
		URL, Title, Body         string
		Author                   struct{ Login string }
		HeadRefName, BaseRefName string
		Files                    []struct{ Path string }
		Additions, Deletions     int
		CreatedAt                time.Time
		IsDraft                  bool
		Mergeable                string
		StatusCheckRollup        []checkItem
		ReviewDecision           string
		LatestReviews            []reviewItem
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	var reviews []ReviewRequest
	for _, p := range raw {
		r := ReviewRequest{Number: p.Number, URL: p.URL, Title: p.Title, Author: p.Author.Login, Head: p.HeadRefName,
			Base: p.BaseRefName, Body: p.Body, Additions: p.Additions, Deletions: p.Deletions, Created: p.CreatedAt, Draft: p.IsDraft,
			Mergeable: p.Mergeable, Checks: checksOf(p.StatusCheckRollup),
			Decision: p.ReviewDecision, Reviews: reviewsOf(p.LatestReviews)}
		if _, rest, ok := strings.Cut(p.URL, "github.com/"); ok {
			if owner, tail, ok := strings.Cut(rest, "/"); ok {
				repo, _, _ := strings.Cut(tail, "/")
				r.Repo = owner + "/" + repo
			}
		}
		for _, f := range p.Files {
			r.Files = append(r.Files, f.Path)
		}
		reviews = append(reviews, r)
	}
	slices.SortStableFunc(reviews, func(a, b ReviewRequest) int { return b.Created.Compare(a.Created) })
	return reviews, nil
}

func (m *dashModel) readReviews() tea.Cmd {
	dir := m.CWD
	return func() tea.Msg {
		reviews, err := readReviewRequests(dir)
		return reviewsEvent{reviews, err}
	}
}

func nextReviewPoll() tea.Cmd {
	return tea.Tick(time.Minute, func(time.Time) tea.Msg { return reviewPollEvent{} })
}

// withGhosts is tasks with a ghost for every review request no task has taken; a task takes a PR by its ticket.
func (m *dashModel) withGhosts(tasks []Task) []Task {
	real := slices.DeleteFunc(slices.Clone(tasks), func(t Task) bool { return t.Review != nil || m.dropping[t.ID] })
	taken := map[string]bool{}
	for _, t := range real {
		if t.Ticket != "" {
			taken[t.Ticket] = true
		}
	}
	for _, r := range m.reviews {
		if taken[r.URL] {
			delete(m.reviewStarting, r.URL)
			continue
		}
		g := ghostTask(r)
		m.metadata[metadataKey(g)] = taskMetadata{id: g.ID, key: metadataKey(g), pr: prOf(r), prs: []prInfo{prOf(r)}, at: m.now()}
		real = append(real, g)
	}
	return real
}

func ghostTask(r ReviewRequest) Task {
	rr := r
	return Task{ID: "review:" + r.URL, Title: r.Title, Phase: "Review", State: reviewState, Branch: r.Head, Git: "GitHub",
		Updated: r.Created, Review: &rr}
}

func prOf(r ReviewRequest) prInfo {
	return prInfo{Number: r.Number, URL: r.URL, State: "OPEN", Draft: r.Draft, Review: "REVIEW_REQUIRED", Head: r.Head,
		Base: r.Base, Title: r.Title, Tickets: linearRefs(r.Title, r.Head, r.Body), Plan: planRef(r.Body, r.Files),
		Created: r.Created, Links: prLinks(r.Body, r.URL)}
}

// regroup re-merges the ghosts and keeps the selected task selected.
func (m *dashModel) regroup() {
	id := m.task().ID
	m.data.Tasks = m.withGhosts(m.data.Tasks)
	if i := slices.IndexFunc(m.data.Tasks, func(t Task) bool { return t.ID == id }); i >= 0 {
		m.selected = i
	}
	m.selectVisible()
}

// ghostCard is a review request nobody runs yet: a dashed, muted card whose keys light only when selected.
func (m *dashModel) ghostCard(i int, t Task, w int) []string {
	r := t.Review
	selected := i == m.selected
	inner := max(1, w-4)
	num := fmt.Sprintf("#%d", r.Number)
	titleStyle := muted
	if selected {
		titleStyle = muted.Bold(true)
	}
	mark := ""
	if g := glyph(m.glyphs, markQueued); g != "" {
		mark = muted.Render(g) + " "
	}
	room := inner - ansi.StringWidth(num) - 2 - ansi.StringWidth(ansi.Strip(mark))
	first := pad(mark+titleStyle.Render(ansi.Truncate(safeText(r.Title), max(1, room), "…")), inner-ansi.StringWidth(num)) + linked(r.URL, num)
	who := "@" + safeText(r.Author) + " · " + ago(r.Created, m.now())
	if r.Draft {
		who += " · draft"
	}
	branch := keepTail(safeText(r.Head), max(4, inner-ansi.StringWidth(safeText(r.Base))-3)) + " → " + safeText(r.Base)
	size := fmt.Sprintf("%d files · +%d -%d", len(r.Files), r.Additions, r.Deletions)
	state, sized := r.statusLine(), muted.Render(size)
	if why := m.reviewFailures[r.URL]; why != "" {
		state = warnText.Render(ansi.Truncate("last review failed: "+why, inner, "…"))
	}
	if m.reviewStarting[r.URL] {
		state = brand.Render(m.spinner()) + muted.Render(" starting the review…")
	}
	if state != r.statusLine() {
		sized += muted.Render(" · ") + r.statusLine()
	}
	state, sized = ansi.Truncate(state, inner, "…"), ansi.Truncate(sized, inner, "…")
	actions := ruleText.Render("↵ agentic review   t details")
	if selected {
		actions = hint("↵", "agentic review") + "   " + hint("t", "details")
	}
	body := []string{first, cardDivider, muted.Render(who), muted.Render(branch), sized, state, cardDivider, actions}
	if m.lay.short {
		body = []string{first, muted.Render(who), actions}
	}
	return m.dashedFrame(body, w, m.reviewChip(*r), selected)
}

// dashedFrame draws body in a dashed muted frame w cells wide; the selected card's frame is brand.
func (m *dashModel) dashedFrame(body []string, w int, chip string, selected bool) []string {
	tl, tr, bl, br, hz, vt := "╭", "╮", "╰", "╯", "╌", "╎"
	if m.glyphs == ui.GlyphsASCII || m.glyphs == ui.GlyphsOff {
		tl, tr, bl, br, hz, vt = "+", "+", "+", "+", "-", ":"
	}
	edge := muted
	if selected {
		edge = brand
	}
	inner := max(0, w-4)
	chipW := ansi.StringWidth(chip)
	if chipW == 0 || chipW+4 > w {
		chip, chipW = "", 0
	}
	top := edge.Render(tl + strings.Repeat(hz, max(0, w-2)) + tr)
	if chipW > 0 {
		top = edge.Render(tl+strings.Repeat(hz, max(0, w-3-chipW))) + chip + edge.Render(hz+tr)
	}
	out := []string{top}
	for _, line := range body {
		if line == cardDivider {
			out = append(out, edge.Render(vt)+" "+ruleText.Render(strings.Repeat(hz, inner))+" "+edge.Render(vt))
			continue
		}
		out = append(out, edge.Render(vt)+" "+pad(line, inner)+" "+edge.Render(vt))
	}
	return append(out, edge.Render(bl+strings.Repeat(hz, max(0, w-2))+br))
}

// reviewTemplate is an opening prompt for a review run; its body takes {{placeholders}}.
type reviewTemplate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

// reviewTemplates are the operator's own templates from <proveo home>/review-templates.json, else the defaults.
func (m *dashModel) reviewTemplates() []reviewTemplate {
	if b, err := os.ReadFile(filepath.Join(m.Home, "review-templates.json")); err == nil {
		var own []reviewTemplate
		if json.Unmarshal(b, &own) == nil && len(own) > 0 && !slices.ContainsFunc(own, func(t reviewTemplate) bool {
			return strings.TrimSpace(t.Name) == "" || strings.TrimSpace(t.Body) == ""
		}) {
			return own
		}
	}
	return defaultReviewTemplates
}

// renderReviewPrompt fills a template's placeholders from the request and the operator's notes.
func renderReviewPrompt(t reviewTemplate, r ReviewRequest, notes string) string {
	files := r.Files
	more := ""
	if len(files) > 15 {
		files, more = files[:15], fmt.Sprintf(" (+%d more)", len(r.Files)-15)
	}
	if strings.TrimSpace(notes) == "" {
		notes = "none"
	}
	return strings.NewReplacer(
		"{{pr_url}}", r.URL, "{{pr_number}}", strconv.Itoa(r.Number), "{{pr_title}}", r.Title, "{{repo}}", r.Repo,
		"{{head}}", r.Head, "{{base}}", r.Base, "{{author}}", r.Author,
		"{{changed_files}}", strings.Join(files, ", ")+more, "{{notes}}", strings.TrimSpace(notes),
	).Replace(t.Body) + reviewLive
}

// openReview shows the start card for a ghost.
func (m *dashModel) openReview(t Task) {
	if t.Review == nil || m.reviewStarting[t.Review.URL] {
		return
	}
	var choices []choiceui.ReviewTemplate
	for _, tpl := range m.reviewTemplates() {
		choices = append(choices, choiceui.ReviewTemplate{Name: tpl.Name, Description: tpl.Description})
	}
	r := t.Review
	pr := fmt.Sprintf("#%d %s · @%s", r.Number, safeText(r.Title), safeText(r.Author))
	agent := agentLabel(reviewAgent) + " · headless · read-only · live app in host Chrome · up to " + schedule.ReviewBudget
	m.reviewFor, m.reviewModal = t, choiceui.NewReviewStart(pr, agent, choices, m.width, m.height)
}

// startReview queues the review through `proveo schedule review`; the run's ticket claims the ghost.
func (m *dashModel) startReview(t Task, template int, notes string) tea.Cmd {
	templates := m.reviewTemplates()
	if t.Review == nil || template < 0 || template >= len(templates) {
		return nil
	}
	r := *t.Review
	if m.reviewStarting == nil {
		m.reviewStarting = map[string]bool{}
	}
	m.reviewStarting[r.URL] = true
	prompt := renderReviewPrompt(templates[template], r, notes)
	label := fmt.Sprintf("Review #%d: %s", r.Number, r.Title)
	bin, cwd := m.Binary, m.CWD
	return tea.Batch(func() tea.Msg {
		f, err := os.CreateTemp("", "proveo-review-*.md")
		if err != nil {
			return reviewQueuedEvent{url: r.URL, number: r.Number, err: err}
		}
		defer func() { _ = os.Remove(f.Name()) }()
		if _, err := f.WriteString(prompt); err != nil {
			_ = f.Close()
			return reviewQueuedEvent{url: r.URL, number: r.Number, err: err}
		}
		if err := f.Close(); err != nil {
			return reviewQueuedEvent{url: r.URL, number: r.Number, err: err}
		}
		cmd := exec.Command(bin, "schedule", "review", r.URL, "--target", reviewAgent, "--title", label, "--prompt-file", f.Name())
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if err != nil {
			if why := safeText(lines[len(lines)-1]); why != "" {
				err = errors.New(why)
			}
			return reviewQueuedEvent{url: r.URL, number: r.Number, err: err}
		}
		return reviewQueuedEvent{url: r.URL, number: r.Number, name: safeText(lines[len(lines)-1])}
	}, m.loadTick())
}

//go:embed review_templates.json
var reviewTemplatesJSON []byte

// reviewLive follows every template: run the app as the repository's AGENTS.md files say, verify claims live, and name
// every integration the review could not reach.
//
//go:embed review_live.md
var reviewLive string

// defaultReviewTemplates are three openers drawn from the operator's own review sessions.
var defaultReviewTemplates = func() []reviewTemplate {
	var t []reviewTemplate
	if err := json.Unmarshal(reviewTemplatesJSON, &t); err != nil {
		panic(err)
	}
	return t
}()

type reviewDroppedEvent struct {
	id, ticket string
	err        error
}

// failedReview reports a review run that ended before it reviewed anything.
func failedReview(t Task) bool {
	_, isReview := reviewPR(t)
	return isReview && t.Review == nil && t.State == schedule.Attention && !t.Live && (t.Outcome == "failed" || t.Outcome == "not-ready")
}

// dropFailedReviews cleans up every failed review run once, so its pull request is a ghost again.
func (m *dashModel) dropFailedReviews() tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.data.Tasks {
		if !failedReview(t) || m.dropping[t.ID] || m.dropFailed[t.ID] {
			continue
		}
		if m.dropping == nil {
			m.dropping, m.reviewFailures = map[string]bool{}, map[string]string{}
		}
		why, _, _ := strings.Cut(t.Reason, ": ")
		m.dropping[t.ID], m.reviewFailures[t.Ticket] = true, safeText(why)
		id, ticket, bin, cwd := t.ID, t.Ticket, m.Binary, m.CWD
		cmds = append(cmds, func() tea.Msg {
			cmd := exec.Command(bin, "schedule", "drop-review", id)
			cmd.Dir = cwd
			out, err := cmd.CombinedOutput()
			if err != nil {
				if why := lastLine(string(out)); why != "" {
					err = errors.New(why)
				}
			}
			return reviewDroppedEvent{id: id, ticket: ticket, err: err}
		})
	}
	if len(cmds) > 0 {
		m.regroup()
	}
	return tea.Batch(cmds...)
}
