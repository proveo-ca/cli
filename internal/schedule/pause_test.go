// SPEC: _spec/internal/dashboard/schedule-manager.puml
package schedule

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const sampleSchedule = `# fantasy football — times are kickoff-relative
jobs:
  ff-lineup:          # pass 1 of 2
    target: hermes
    prompt_file: /p/lineup.md
    budget: 36m
    tz: America/New_York
    at:
      - thu 18:50   # TNF 8:15 pm
      - sun 11:35
  ff-refine:   # pass 2 of 2
    target: hermes
    prompt_file: /p/refine.md
    budget: 36m
    tz: America/New_York
    at:
      - thu 19:32
  ff-trades:
    target: hermes
    prompt_file: /p/trades.md
`

func writeSchedule(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSetPausedChangesOnlyThePauseLine(t *testing.T) {
	t.Parallel()
	path := writeSchedule(t, sampleSchedule)
	until := time.Date(2026, 12, 8, 0, 0, 0, 0, time.UTC)
	if err := SetPaused(path, "ff-lineup", Pause{Until: until}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := strings.Replace(sampleSchedule, "  ff-lineup:          # pass 1 of 2\n", "  ff-lineup:          # pass 1 of 2\n    paused_until: 2026-12-08\n", 1)
	if string(b) != want {
		t.Fatalf("paused file:\n%s\nwant:\n%s", b, want)
	}
	c, err := LoadFile(path)
	if err != nil || c.Jobs["ff-lineup"].PausedUntil != "2026-12-08" {
		t.Fatalf("an unquoted date must read back as the string: %+v %v", c.Jobs["ff-lineup"], err)
	}
	if err := SetPaused(path, "ff-lineup", Pause{Forever: true}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "paused_until") || strings.Count(string(b), "paused: true") != 1 {
		t.Fatalf("a new pause replaces the old one:\n%s", b)
	}
	if err := SetPaused(path, "ff-lineup", Pause{}); err != nil {
		t.Fatal(err)
	}
	if b, _ = os.ReadFile(path); string(b) != sampleSchedule {
		t.Fatalf("resuming must restore the file byte for byte:\n%s", b)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o644 {
		t.Errorf("the file must keep its mode, got %v", st.Mode().Perm())
	}
}

func TestSetPausedReachesTheLastJobAndRefusesWhatItCannotPlace(t *testing.T) {
	t.Parallel()
	path := writeSchedule(t, sampleSchedule)
	if err := SetPaused(path, "ff-trades", Pause{Forever: true}); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(path)
	if err != nil || !c.Jobs["ff-trades"].Paused || c.Jobs["ff-refine"].Paused {
		t.Fatalf("only the last job pauses: %+v %v", c.Jobs, err)
	}
	if err := SetPaused(path, "nope", Pause{Forever: true}); err == nil {
		t.Error("an unknown job must be refused")
	}
	inline := writeSchedule(t, "jobs:\n  a: {target: hermes, prompt_file: /p}\n")
	if err := SetPaused(inline, "a", Pause{Forever: true}); err == nil || !strings.Contains(err.Error(), "inline") {
		t.Errorf("an inline job must be refused, got %v", err)
	}
	before, _ := os.ReadFile(inline)
	if string(before) != "jobs:\n  a: {target: hermes, prompt_file: /p}\n" {
		t.Errorf("a refused pause must leave the file alone:\n%s", before)
	}
}

func TestPauseFieldsAreChecked(t *testing.T) {
	t.Parallel()
	for body, want := range map[string]string{
		"jobs:\n  a:\n    target: hermes\n    prompt_file: /p\n    paused: true\n    paused_until: 2026-12-08\n": "exclusive",
		"jobs:\n  a:\n    target: hermes\n    prompt_file: /p\n    paused_until: next week\n":                    "YYYY-MM-DD",
		"jobs:\n  a:\n    target: hermes\n    prompt_file: /p\n    pause: true\n":                                "pause",
	} {
		if _, err := LoadFile(writeSchedule(t, body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("LoadFile(%q) = %v, want an error naming %q", body, err, want)
		}
	}
}

func TestAPausedJobSkipsItsRunsUntilTheDate(t *testing.T) {
	t.Parallel()
	loc := ny(t)
	j := Job{Target: "hermes", PromptFile: "/p", At: []string{"thu 18:50"}, Timezone: "America/New_York", PausedUntil: "2026-12-08"}
	thu := time.Date(2026, 12, 3, 18, 50, 0, 0, loc)
	c := Config{Jobs: map[string]Job{"a": j}}
	if due := c.Due(thu.Add(time.Minute), State{}); len(due) != 0 {
		t.Errorf("a paused run must not fire: %+v", due)
	}
	next, ok := j.NextRun(thu.Add(-time.Hour))
	if want := time.Date(2026, 12, 10, 18, 50, 0, 0, loc); !ok || !next.Equal(want) {
		t.Errorf("NextRun = %v %v, want the first run on or after the date, %v", next, ok, want)
	}
	if due := c.Due(next.Add(time.Minute), State{}); len(due) != 1 {
		t.Errorf("the run after the date must fire: %+v", due)
	}
	j.PausedUntil, j.Paused = "", true
	if _, ok := j.NextRun(thu); ok {
		t.Error("a job paused with no date has no next run")
	}
}

func TestWeekPlacesRunsAndNamesOverlaps(t *testing.T) {
	t.Parallel()
	loc := ny(t)
	c := Config{Jobs: map[string]Job{
		"odds":    {Target: "hermes", PromptFile: "/p", Budget: "20m", At: []string{"thu 18:15"}},
		"lineup":  {Target: "hermes", PromptFile: "/p", Budget: "36m", At: []string{"thu 18:30", "sun 23:50"}},
		"refine":  {Target: "hermes", PromptFile: "/p", Budget: "36m", At: []string{"mon 00:10"}},
		"stopped": {Target: "hermes", PromptFile: "/p", Budget: "60m", At: []string{"thu 18:00"}, Paused: true},
	}}
	start := WeekStart(time.Date(2026, 12, 3, 9, 0, 0, 0, loc), loc)
	if want := time.Date(2026, 11, 30, 0, 0, 0, 0, loc); !start.Equal(want) {
		t.Fatalf("WeekStart = %v, want Monday %v", start, want)
	}
	slots := Week(c, start)
	find := func(job string, at time.Time) Slot {
		t.Helper()
		i := slices.IndexFunc(slots, func(s Slot) bool { return s.Job == job && s.Start.Equal(at) })
		if i < 0 {
			t.Fatalf("no %s slot at %v in %+v", job, at, slots)
		}
		return slots[i]
	}
	odds := find("odds", time.Date(2026, 12, 3, 18, 15, 0, 0, loc))
	if !slices.Equal(odds.Overlaps, []string{"lineup"}) {
		t.Errorf("odds 18:15–18:35 overlaps lineup 18:30: %v", odds.Overlaps)
	}
	if s := find("stopped", time.Date(2026, 12, 3, 18, 0, 0, 0, loc)); !s.Paused || len(s.Overlaps) != 0 {
		t.Errorf("a paused run shows, marked, and overlaps nothing: %+v", s)
	}
	spill := find("lineup", time.Date(2026, 11, 29, 23, 50, 0, 0, loc))
	if !slices.Equal(spill.Overlaps, []string{"refine"}) {
		t.Errorf("last Sunday's run spills into Monday and meets refine 00:10: %v", spill.Overlaps)
	}
	if s := find("lineup", time.Date(2026, 12, 6, 23, 50, 0, 0, loc)); !slices.Equal(s.Overlaps, []string{"refine"}) {
		t.Errorf("this Sunday's run meets next Monday's refine: %v", s.Overlaps)
	}
	for i := 1; i < len(slots); i++ {
		if slots[i].Start.Before(slots[i-1].Start) {
			t.Fatalf("slots must be soonest first: %+v", slots)
		}
	}
}
