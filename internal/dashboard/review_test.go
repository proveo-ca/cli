// SPEC: _spec/_experiments/dashboard/review-requests.puml
package dashboard

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/choiceui"
	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

const ghReviews = `[
 {"number":3975,"url":"https://github.com/acme/app/pull/3975","title":"Move cloud accounts to User Settings","author":{"login":"dana"},
  "headRefName":"rs-cloud-accounts-ui","baseRefName":"rs-cloud-revoke-reach","body":"Map: https://claude.ai/code/artifact/abc123. Plan: _spec/_plans/cloud.md",
  "files":[{"path":"apps/web/a.ts"},{"path":"apps/web/b.ts"}],"additions":40,"deletions":7,"createdAt":"2026-11-24T08:00:00Z","isDraft":false,
  "mergeable":"CONFLICTING","statusCheckRollup":[{"__typename":"CheckRun","name":"Typecheck","status":"COMPLETED","conclusion":"FAILURE"},
   {"__typename":"CheckRun","name":"Unit","status":"COMPLETED","conclusion":"SUCCESS"},{"__typename":"CheckRun","name":"E2E","status":"COMPLETED","conclusion":"SKIPPED"}],
  "reviewDecision":"APPROVED","latestReviews":[{"author":{"login":"copilot-pull-request-reviewer"},"state":"COMMENTED"},{"author":{"login":"kai"},"state":"APPROVED"}]},
 {"number":3976,"url":"https://github.com/acme/app/pull/3976","title":"Add the Projector PSA connector","author":{"login":"lee"},
  "headRefName":"rs-projector","baseRefName":"dev","body":"","files":[],"additions":1,"deletions":0,"createdAt":"2026-11-24T08:30:00Z","isDraft":true,
  "mergeable":"UNKNOWN","statusCheckRollup":[{"__typename":"CheckRun","name":"Build","status":"IN_PROGRESS","conclusion":""}]}
]`

func fakeReviews(t *testing.T, out string) *[]string {
	t.Helper()
	var asked []string
	path, run := ghPath, reviewCommand
	t.Cleanup(func() { ghPath, reviewCommand = path, run })
	ghPath = func() error { return nil }
	reviewCommand = func(_ string, args ...string) ([]byte, error) {
		asked = append(asked, strings.Join(args, " "))
		return []byte(out), nil
	}
	return &asked
}

func reviewModel(t *testing.T) *dashModel {
	t.Helper()
	fakeReviews(t, ghReviews)
	m := sampleModel(140, 44, ui.GlyphsSafe)
	m.Home = t.TempDir()
	reviews, err := readReviewRequests("/repo")
	if err != nil {
		t.Fatal(err)
	}
	m.reviews = reviews
	m.regroup()
	return m
}

func TestReviewRequestsComeFromGhForTheUserDirectly(t *testing.T) {
	asked := fakeReviews(t, ghReviews)
	reviews, err := readReviewRequests("/repo")
	if err != nil || len(reviews) != 2 {
		t.Fatalf("reviews = %+v, %v", reviews, err)
	}
	if !strings.Contains((*asked)[0], "--search user-review-requested:@me --state open") {
		t.Errorf("asked %q; the gh user's own requests, open only", (*asked)[0])
	}
	if !strings.Contains((*asked)[0], ",mergeable,statusCheckRollup,reviewDecision,latestReviews") {
		t.Errorf("asked %q; the cards need mergeability and checks", (*asked)[0])
	}
	if r := reviews[0]; r.Number != 3976 || reviews[1].Repo != "acme/app" || len(reviews[1].Files) != 2 || reviews[1].Author != "dana" {
		t.Errorf("newest first, with repo, files and author: %+v", reviews)
	}
}

func TestAGhostSitsOnTopUntilARunTakesItsPR(t *testing.T) {
	m := reviewModel(t)
	order := m.taskOrder()
	first := m.data.Tasks[order[0]]
	if first.Review == nil || m.groupLabel(first) != "Review requests" {
		t.Fatalf("the queue opens with %q under %q", first.Title, m.groupLabel(first))
	}
	frame := ansi.Strip(strings.Join(m.renderTasks(140, 44), "\n"))
	if strings.Index(frame, "Review requests") > strings.Index(frame, "Attention required") {
		t.Errorf("Review requests is not on top:\n%s", frame)
	}
	m.data.Tasks = append(m.data.Tasks, Task{ID: "job", Title: "Review #3975", Ticket: "https://github.com/acme/app/pull/3975"})
	m.regroup()
	ghosts := 0
	for _, task := range m.data.Tasks {
		if task.Review != nil {
			ghosts++
			if task.Review.Number == 3975 {
				t.Error("a run with the PR as its ticket left its ghost")
			}
		}
	}
	if ghosts != 1 {
		t.Errorf("%d ghosts, want the untaken one", ghosts)
	}
}

func TestTheGhostCardIsDashedMutedAndSaysWhatItWaitsFor(t *testing.T) {
	m := reviewModel(t)
	var ghost Task
	var at int
	for i, task := range m.data.Tasks {
		if task.Review != nil && task.Review.Number == 3975 {
			ghost, at = task, i
		}
	}
	m.selected = at
	lines := m.taskCard(at, ghost, 52)
	if len(lines) != m.cardHeight() {
		t.Fatalf("%d rows, want %d", len(lines), m.cardHeight())
	}
	card := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"╌", "╎", "#3975", "@dana", "rs-cloud-accounts-ui → rs-cloud-revoke-reach", "2 files · +40 -7", "↵ agentic review"} {
		if !strings.Contains(card, want) {
			t.Errorf("the ghost lacks %q:\n%s", want, card)
		}
	}
	m.reviewStarting = map[string]bool{ghost.Review.URL: true}
	if card := ansi.Strip(strings.Join(m.taskCard(at, ghost, 52), "\n")); !strings.Contains(card, "starting the review") {
		t.Errorf("a queued start does not show:\n%s", card)
	}
}

func TestEnterOnAGhostStartsAHeadlessReviewFromATemplate(t *testing.T) {
	m := reviewModel(t)
	for i, task := range m.data.Tasks {
		if task.Review != nil && task.Review.Number == 3975 {
			m.selected = i
		}
	}
	m.enter()
	if m.reviewModal == nil {
		t.Fatal("enter on a ghost opened nothing")
	}
	view := ansi.Strip(m.View().Content)
	for _, tpl := range defaultReviewTemplates {
		if !strings.Contains(view, tpl.Name) {
			t.Errorf("the start card lacks template %q:\n%s", tpl.Name, view)
		}
	}
	for _, want := range []string{"Start a review", "#3975 Move cloud accounts", "Claude Code · headless", "live app in host Chrome", "Notes", "Start"} {
		if !strings.Contains(view, want) {
			t.Errorf("the start card lacks %q:\n%s", want, view)
		}
	}
	bin := filepath.Join(t.TempDir(), "proveo")
	args := filepath.Join(t.TempDir(), "args")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+args+"\"\ncat \"$9\" >> \""+args+"\"\necho claudecode-ab12cd\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.Binary, m.CWD = bin, t.TempDir()
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.PasteMsg{Content: "focus on the revoke path"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || m.reviewModal != nil || !m.reviewStarting["https://github.com/acme/app/pull/3975"] {
		t.Fatal("enter did not start the review")
	}
	var queued reviewQueuedEvent
	switch msg := cmd().(type) {
	case reviewQueuedEvent:
		queued = msg
	case tea.BatchMsg:
		for _, c := range msg {
			if c == nil {
				continue
			}
			if e, ok := c().(reviewQueuedEvent); ok {
				queued = e
			}
		}
	}
	if queued.err != nil || queued.name != "claudecode-ab12cd" {
		t.Fatalf("queued = %+v (%v)", queued, queued.err)
	}
	b, _ := os.ReadFile(args)
	got := string(b)
	for _, want := range []string{"schedule\nreview\nhttps://github.com/acme/app/pull/3975\n--target\nclaudecode\n--title\nReview #3975: Move cloud accounts to User Settings",
		"My concerns: focus on the revoke path", "apps/web/a.ts, apps/web/b.ts", "read-only"} {
		if !strings.Contains(got, want) {
			t.Errorf("the queued review lacks %q:\n%s", want, got)
		}
	}
	m.Update(queued)
	if !strings.Contains(m.status, "Review of #3975 queued as claudecode-ab12cd") {
		t.Errorf("status = %q", m.status)
	}
}

func TestTemplatesFillTheirPlaceholdersAndCanBeTheOperatorsOwn(t *testing.T) {
	r := ReviewRequest{Number: 7, URL: "u", Title: "T", Repo: "o/r", Head: "h", Base: "b", Author: "a"}
	for i := range 20 {
		r.Files = append(r.Files, "f"+string(rune('a'+i)))
	}
	got := renderReviewPrompt(reviewTemplate{Body: "{{pr_url}} {{repo}}#{{pr_number}} {{head}}->{{base}} {{author}} [{{changed_files}}] {{notes}}"}, r, "  ")
	if !strings.HasPrefix(got, "u o/r#7 h->b a [fa, fb") || !strings.Contains(got, "(+5 more)] none") || strings.Contains(got, "{{") {
		t.Errorf("rendered %q", got)
	}
	for _, want := range []string{"Live verification (required", "tooling/AGENTS.md", "agent-browser", "Access table", "[Unverified: <integration> unreachable]", "no edits to the shared .env"} {
		if !strings.Contains(got, want) {
			t.Errorf("an operator's own template loses the live verification block: no %q", want)
		}
	}
	m := sampleModel(120, 40, ui.GlyphsSafe)
	m.Home = t.TempDir()
	if len(m.reviewTemplates()) != 3 {
		t.Error("the defaults are three templates")
	}
	if err := os.WriteFile(filepath.Join(m.Home, "review-templates.json"), []byte(`[{"name":"Mine","description":"d","body":"b {{pr_url}}"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if own := m.reviewTemplates(); len(own) != 1 || own[0].Name != "Mine" {
		t.Errorf("the operator's file was ignored: %+v", own)
	}
}

func TestTaskDetailsListThePRDescriptionLinks(t *testing.T) {
	if got := prLinks("See https://claude.ai/code/artifact/abc. Demo (https://x.dev/demo.mp4), again https://claude.ai/code/artifact/abc "+
		"![s](https://github.com/user-attachments/assets/1) self https://github.com/o/r/pull/1", "https://github.com/o/r/pull/1"); strings.Join(got, " ") !=
		"https://claude.ai/code/artifact/abc https://x.dev/demo.mp4" {
		t.Errorf("links = %q", got)
	}
	m := reviewModel(t)
	for i, task := range m.data.Tasks {
		if task.Review != nil && task.Review.Number == 3975 {
			m.selected = i
		}
	}
	m.details = true
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Links") || !strings.Contains(view, "claude.ai/code/artifact/abc123") {
		t.Errorf("details lack the PR's links:\n%s", view)
	}
}

func TestAFailedReviewRunIsCleanedUpOnceAndItsGhostComesBack(t *testing.T) {
	m := reviewModel(t)
	url := "https://github.com/acme/app/pull/3975"
	bin, calls := filepath.Join(t.TempDir(), "proveo"), filepath.Join(t.TempDir(), "calls")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" >> \""+calls+"\"\necho "+url+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.Binary, m.CWD = bin, t.TempDir()
	failed := Task{ID: "claudecode-59e0", Title: "Review #3975: Move cloud accounts", Phase: "Review", Ticket: url,
		State: schedule.Attention, Outcome: "failed", Reason: "the agent exited 1 before it was ready: a local Open of this path…"}
	cmd := m.receiveData(Data{Tasks: append(slices.Clone(sampleModel(140, 44, ui.GlyphsSafe).data.Tasks), failed)})
	if !m.dropping[failed.ID] || slices.ContainsFunc(m.data.Tasks, func(t Task) bool { return t.ID == failed.ID }) {
		t.Fatal("the failed review run is not being cleaned up, or still shows")
	}
	var ghost Task
	for _, task := range m.data.Tasks {
		if task.Review != nil && task.Review.URL == url {
			ghost = task
		}
	}
	if ghost.Review == nil {
		t.Fatal("the ghost did not come back while the clean-up runs")
	}
	if card := ansi.Strip(strings.Join(m.taskCard(0, ghost, 60), "\n")); !strings.Contains(card, "last review failed: the agent exited 1 before") {
		t.Errorf("the ghost does not say why the last review failed:\n%s", card)
	}
	var dropped reviewDroppedEvent
	for _, msg := range runCmd(cmd) {
		if e, ok := msg.(reviewDroppedEvent); ok {
			dropped = e
		}
	}
	if dropped.err != nil || dropped.id != failed.ID {
		t.Fatalf("dropped = %+v", dropped)
	}
	if b, _ := os.ReadFile(calls); strings.TrimSpace(string(b)) != "schedule drop-review claudecode-59e0" {
		t.Errorf("ran %q", b)
	}
	m.Update(dropped)
	if !strings.Contains(m.status, "back in Review requests") || m.dropping[failed.ID] {
		t.Errorf("status %q, dropping %v", m.status, m.dropping)
	}
	m.dropFailed = map[string]bool{failed.ID: true}
	if cmd := m.receiveData(Data{Tasks: []Task{failed}}); slices.ContainsFunc(runCmd(cmd), func(msg tea.Msg) bool {
		_, ok := msg.(reviewDroppedEvent)
		return ok
	}) {
		t.Error("a clean-up that failed was retried")
	}
}

// runCmd runs cmd and every command a batch holds, skipping ticks; it returns their messages.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range batch {
				out = append(out, runCmd(c)...)
			}
			return out
		}
		return []tea.Msg{msg}
	case <-time.After(300 * time.Millisecond):
		return nil
	}
}

func TestVerdictSubmitsTheOperatorsReviewThroughGh(t *testing.T) {
	m := reviewModel(t)
	for i, task := range m.data.Tasks {
		if task.Review != nil && task.Review.Number == 3975 {
			m.selected = i
		}
	}
	pane := m.renderFiles(44, 20)
	last := pane[len(pane)-1]
	if ansi.Strip(last) != pad(" "+ansi.Strip(choiceui.Button("Verdict on #3975", 'v', false, ui.GlyphsSafe)), 44) {
		t.Errorf("the tree does not end with the Verdict button:\n%s", ansi.Strip(strings.Join(pane, "\n")))
	}
	if last != pad(" "+choiceui.Button("Verdict on #3975", 'v', false, ui.GlyphsSafe), 44) {
		t.Errorf("the Verdict button is not drawn as the pop-up buttons are: %q", last)
	}
	if foot := ansi.Strip(m.footer(200)); !strings.Contains(foot, "v verdict") {
		t.Errorf("footer = %q", foot)
	}
	var asked []string
	reviewCommand = func(_ string, args ...string) ([]byte, error) {
		asked = append(asked, strings.Join(args, " "))
		return nil, nil
	}
	m.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
	if m.verdictModal == nil || !strings.Contains(ansi.Strip(m.View().Content), "#3975 Move cloud accounts to User Settings · @dana") {
		t.Fatalf("v opened:\n%s", ansi.Strip(m.View().Content))
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.PasteMsg{Content: "the revoke path leaks the token"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || m.verdictModal != nil {
		t.Fatal("submit ran nothing")
	}
	m.Update(cmd())
	if len(asked) != 1 || asked[0] != "pr review https://github.com/acme/app/pull/3975 --request-changes --body the revoke path leaks the token" {
		t.Errorf("gh asked %q", asked)
	}
	if !strings.Contains(m.status, "Requested changes on #3975") {
		t.Errorf("status = %q", m.status)
	}
	m.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	if asked[1] != "pr review https://github.com/acme/app/pull/3975 --approve" || !strings.Contains(m.status, "Approved #3975") {
		t.Errorf("approve asked %q, status %q", asked[1], m.status)
	}
	m.selected = slices.IndexFunc(m.data.Tasks, func(t Task) bool { _, ok := reviewPR(t); return !ok })
	if m.Update(tea.KeyPressMsg{Code: 'v', Text: "v"}); m.verdictModal != nil {
		t.Error("a task with no PR to judge opened a verdict")
	}
}

func TestReadingAReviewedPRPinsTheAgenticReviewAboveTheVerdict(t *testing.T) {
	m := reviewModel(t)
	url := "https://github.com/acme/app/pull/3975"
	run := Task{ID: "claudecode-3aa1c559496f", Title: "Review #3975: Move cloud accounts to User Settings", Phase: "Review",
		Ticket: url, Agent: "claudecode", State: schedule.Running, Live: true}
	m.data.Tasks = append(m.data.Tasks, run)
	m.regroup()
	m.selected = slices.IndexFunc(m.data.Tasks, func(t Task) bool { return t.ID == run.ID })
	foot := func() string { return ansi.Strip(strings.Join(m.renderFiles(44, 30), "\n")) }
	if strings.Contains(foot(), "Agentic review") {
		t.Errorf("the card shows while the right pane shows tasks:\n%s", foot())
	}
	m.preview, m.previewPath = true, "apps/web/a.ts"
	got := foot()
	card, verdict := strings.Index(got, "Agentic review"), strings.Index(got, "Verdict on #3975")
	if card < 0 || verdict < card || !strings.Contains(got, "Review #3975: Move cloud") || !strings.Contains(got, "a attach to reach a verdict") {
		t.Fatalf("the tree's foot lacks [task card] [verdict]:\n%s", got)
	}
	if foot := ansi.Strip(m.footer(200)); !strings.Contains(foot, "a attach review") {
		t.Errorf("footer = %q", foot)
	}
	m.focus = 2
	m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if m.confirm == nil || m.attachID != run.ID {
		t.Fatalf("a did not ask to attach the review run (attach %q)", m.attachID)
	}
	m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Error("confirming attached nothing")
	}
	m.data.Tasks[m.selected].Live = false
	if strings.Contains(ansi.Strip(strings.Join(m.renderFiles(44, 30), "\n")), "Agentic review") {
		t.Error("a review that no longer runs keeps its card")
	}
}
