// SPEC: _spec/_experiments/dashboard/review-requests.puml
package schedule

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAReviewJobIsHeadlessAndTicketedToItsPR(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	url := "https://github.com/acme/app/pull/3975"
	name, j, err := NewReviewJob(home, cwd, "claudecode", url, "Review #3975: Move cloud accounts", "HEAD is read-only review mode for "+url)
	if err != nil {
		t.Fatal(err)
	}
	if j.Mode != "plain" || j.Ticket != url || j.Phase != "Review" || j.Budget != ReviewBudget || !j.New || j.Workdir != cwd ||
		j.Task != "Review #3975: Move cloud accounts" || !strings.HasPrefix(name, "claudecode-") {
		t.Errorf("job = %s %+v", name, j)
	}
	if argv := strings.Join(j.RunArgv("proveo"), " "); !strings.Contains(argv, "--new --task Review #3975: Move cloud accounts --read-only-shares --addon chrome") {
		t.Errorf("argv = %q; a review runs with read-only shares accepted and host Chrome for its live checks", argv)
	}
	j.Args[0] = "changed"
	if ReviewArgs[0] != "--read-only-shares" {
		t.Error("a job's args alias ReviewArgs")
	}
	if b, err := os.ReadFile(j.PromptFile); err != nil || !strings.Contains(string(b), "read-only review mode") {
		t.Errorf("prompt file = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(home, "dashboard-jobs", name, "job.json")); err != nil {
		t.Errorf("no job snapshot: %v", err)
	}
	c, err := Load(home)
	if err != nil || c.Jobs[name].Ticket != url {
		t.Errorf("the dashboard job does not load with its ticket: %v", err)
	}
	if _, _, err := NewReviewJob(home, cwd, "claudecode", "https://github.com/acme/app/issues/1", "x", "p"); err == nil {
		t.Error("an issue URL made a review job")
	}
	if _, _, err := NewReviewJob(home, cwd, "claudecode", url, "x", "  "); err == nil {
		t.Error("an empty prompt made a review job")
	}
}

func TestDroppingAReviewJobForgetsItsQueueEntryAndSnapshot(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	url := "https://github.com/acme/app/pull/3995"
	name, j, err := NewReviewJob(home, cwd, "claudecode", url, "Review #3995", "read-only review")
	if err != nil {
		t.Fatal(err)
	}
	if err := Submit(home, name, "dashboard", j, time.Now()); err != nil {
		t.Fatal(err)
	}
	other, oj, err := NewAttendedJob(home, cwd, "codex", "mine", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Submit(home, other, "dashboard", oj, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := DropReviewJob(home, name)
	if err != nil || got.Ticket != url {
		t.Fatalf("drop = %+v, %v", got, err)
	}
	q, err := ReadQueue(home)
	if err != nil || len(q) != 1 || q[0].Name != other {
		t.Errorf("queue after the drop = %+v, %v; the other job stays", q, err)
	}
	if _, err := os.Stat(filepath.Join(home, "dashboard-jobs", name)); !os.IsNotExist(err) {
		t.Errorf("the review job's snapshot survived: %v", err)
	}
	if _, err := DropReviewJob(home, other); err == nil || !strings.Contains(err.Error(), "not a review job") {
		t.Errorf("an attended job was dropped as a review: %v", err)
	}
	if got, err := DropDashboardJob(home, other); err != nil || got.Target != "codex" {
		t.Fatalf("drop of the attended job = %+v, %v", got, err)
	}
	if q, err := ReadQueue(home); err != nil || len(q) != 0 {
		t.Errorf("queue after both drops = %+v, %v", q, err)
	}
}
