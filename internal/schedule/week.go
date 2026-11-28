// SPEC: _spec/internal/dashboard/schedule-manager.puml
package schedule

import (
	"slices"
	"sort"
	"time"
)

// Slot is one run of one job: from its start to the end of its budget.
type Slot struct {
	Job, Entry string
	Start, End time.Time
	Paused     bool
	Overlaps   []string // the other jobs a live run shares time with
}

// WeekStart is the Monday 00:00 of the week holding now, in loc.
func WeekStart(now time.Time, loc *time.Location) time.Time {
	n := now.In(loc)
	back := (int(n.Weekday()) + 6) % 7
	return time.Date(n.Year(), n.Month(), n.Day()-back, 0, 0, 0, 0, loc)
}

// Week lists every run that touches the seven days from start, soonest first, each live run's overlaps named.
func Week(c Config, start time.Time) []Slot {
	end := start.AddDate(0, 0, 7)
	var all []Slot
	for _, name := range c.Names() {
		j := c.Jobs[name]
		loc, err := j.Location()
		if err != nil {
			continue
		}
		budget, _ := j.BudgetDuration()
		for _, e := range j.At {
			t, err := lastOccurrence(e, start.AddDate(0, 0, -8), loc)
			if err != nil {
				continue
			}
			for ; t.Before(end.AddDate(0, 0, 8)); t = t.AddDate(0, 0, 7) {
				all = append(all, Slot{Job: name, Entry: e, Start: t, End: t.Add(budget), Paused: j.PausedAt(t)})
			}
		}
	}
	for i := range all {
		for k := range all {
			a, b := all[i], all[k]
			if i == k || a.Paused || b.Paused || !a.Start.Before(b.End) || !b.Start.Before(a.End) {
				continue
			}
			if !slices.Contains(all[i].Overlaps, b.Job) {
				all[i].Overlaps = append(all[i].Overlaps, b.Job)
			}
		}
	}
	var out []Slot
	for _, s := range all {
		if s.Start.Before(end) && s.End.After(start) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, k int) bool {
		if !out[i].Start.Equal(out[k].Start) {
			return out[i].Start.Before(out[k].Start)
		}
		return out[i].Job < out[k].Job
	})
	return out
}
