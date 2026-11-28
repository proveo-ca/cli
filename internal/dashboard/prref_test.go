// SPEC: _spec/_experiments/dashboard/review-requests.puml
package dashboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

func TestReviewTasksAndGhostsNameTheirPR(t *testing.T) {
	url := "https://github.com/acme/app/pull/3995"
	for _, c := range []struct {
		task Task
		want int
	}{
		{Task{Review: &ReviewRequest{Number: 7}}, 7},
		{Task{Ticket: url, Phase: "Review"}, 3995},
		{Task{Ticket: url, Title: "Review #3995: hold E2E"}, 3995},
		{Task{Ticket: url, Phase: "Upgrade", Title: "Fix the flaky test"}, 0},
		{Task{Ticket: "https://linear.app/x/issue/PLUV-1", Phase: "Review"}, 0},
	} {
		if n, _ := reviewPR(c.task); n != c.want {
			t.Errorf("%+v: PR %d, want %d", c.task, n, c.want)
		}
	}
}

func TestAReviewReadsThePRFetchedIntoAProveoRef(t *testing.T) {
	dir := t.TempDir()
	run := func(wd string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = wd
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	origin, work := filepath.Join(dir, "origin"), filepath.Join(dir, "work")
	run(dir, "init", "-q", "-b", "dev", origin)
	run(origin, "config", "user.email", "t@example.com")
	run(origin, "config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(origin, "keep.go"), []byte("package keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(origin, "add", "-A")
	run(origin, "commit", "-qm", "base")
	run(dir, "clone", "-q", origin, work)
	run(origin, "checkout", "-q", "-b", "pr")
	if err := os.WriteFile(filepath.Join(origin, "new.go"), []byte("package keep\n\nfunc New() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(origin, "rm", "-q", "keep.go")
	run(origin, "add", "-A")
	run(origin, "commit", "-qm", "the pr")
	head := run(origin, "rev-parse", "HEAD")
	run(origin, "update-ref", "refs/pull/12/head", head)
	run(origin, "checkout", "-q", "dev")

	path, gh := ghPath, ghCommand
	t.Cleanup(func() { ghPath, ghCommand = path, gh })
	ghCommand = func(string, string, ...string) ([]byte, error) { return []byte(`{"baseRefName":"dev"}`), nil }

	h, err := fetchPR(work, 12)
	if err != nil {
		t.Fatal(err)
	}
	if h.sha != head || h.base != "dev" || run(work, "rev-parse", "refs/proveo/review/12") != head {
		t.Fatalf("fetched %+v, want head %s under refs/proveo/review/12", h, head)
	}
	task := Task{ID: "job", Ticket: "https://github.com/acme/app/pull/12", Phase: "Review", Guest: true, Sandbox: "sb", Workdir: "/w"}
	set, err := Changes(prView(task, h), h.base, h.repo)
	if err != nil || len(set.Rows) != 2 || set.Base != "origin/dev" {
		t.Fatalf("changes = %+v, %v", set, err)
	}
	kinds := map[string]string{}
	for _, r := range set.Rows {
		kinds[r.Path] = r.Kind
	}
	if kinds["new.go"] != "A" || kinds["keep.go"] != "D" {
		t.Errorf("kinds = %v; the PR's own A and D, not the sandbox's diff", kinds)
	}
	sides, err := ReadSides(prView(task, h), Change{Kind: "A", Path: "new.go"}, set.MergeBase)
	if err != nil || !strings.Contains(sides.After, "func New()") {
		t.Errorf("preview after = %q, %v", sides.After, err)
	}
}

func TestAttentionCardsLeadWithFollowUpAndSayWhyTheyCannotAttach(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	m.selected = 2
	live := &m.data.Tasks[2]
	live.Live, live.Guest, live.Sandbox, live.VMID = false, true, "proveo-claudecode-1b3fcb7f", "vm"
	m.samples = map[string]guestSample{live.ID: {id: live.ID, sandbox: live.Sandbox, vmid: live.VMID, available: true}}
	sample := m.samples[live.ID]
	sample.activity = "idle"
	m.samples[live.ID] = sample
	if m.groupOf(*live) != 1 {
		t.Fatal("an idle agent is not under Attention required")
	}
	if actions := ansi.Strip(m.cardActions(*live, true)); !strings.HasPrefix(actions, "↵ follow up") {
		t.Errorf("actions = %q; follow up leads", actions)
	}
	if foot := ansi.Strip(m.footer(140)); !strings.Contains(foot, "↵ Follow up") {
		t.Errorf("footer = %q", foot)
	}
	m.enter()
	if m.confirm == nil || !strings.Contains(ansi.Strip(m.View().Content), "live in another terminal") {
		t.Fatalf("enter on an agent live elsewhere:\n%s", ansi.Strip(m.View().Content))
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "proveo-claudecode-1b3fcb7f") || !strings.Contains(view, "OK") || strings.Contains(view, "choose") {
		t.Errorf("the notice lacks its sandbox or its one button:\n%s", view)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.confirm != nil {
		t.Error("enter did not close the notice")
	}
	ended := m.data.Tasks[1]
	if ended.State != schedule.Attention || ended.Live {
		t.Fatalf("fixture: %+v", ended)
	}
	m.selected = 1
	m.enter()
	if m.confirm == nil || !strings.Contains(ansi.Strip(m.View().Content), "Nothing to follow up") {
		t.Errorf("enter on an ended run with no instance:\n%s", ansi.Strip(m.View().Content))
	}
	m.confirm = nil
	live.Live = true
	m.selected = 2
	m.enter()
	if m.confirm == nil || !strings.Contains(ansi.Strip(m.View().Content), "Attach") {
		t.Error("a live tmux task still attaches")
	}
}
