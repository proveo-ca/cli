// SPEC: _spec/internal/schedule/schedule.puml
package schedule

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	FileName        = "schedule.yml"
	stateName       = "schedule-state.json"
	DefaultBudget   = 45 * time.Minute
	DefaultCatchUp  = 20 * time.Minute
	DefaultTimezone = "America/New_York"
)

// Job is one scheduled agent run.
type Job struct {
	Target      string   `yaml:"target"`
	Instance    string   `yaml:"instance,omitempty"`
	New         bool     `yaml:"new,omitempty"`
	Task        string   `yaml:"task,omitempty"`
	DependsOn   []string `yaml:"depends_on,omitempty"`
	Ticket      string   `yaml:"ticket,omitempty"`
	Phase       string   `yaml:"phase,omitempty"`
	Model       string   `yaml:"model,omitempty"`
	Addons      []string `yaml:"addons,omitempty"`
	Args        []string `yaml:"args,omitempty"` // extra `proveo run` flags
	Workdir     string   `yaml:"workdir,omitempty"`
	PromptFile  string   `yaml:"prompt_file"`
	Mode        string   `yaml:"mode,omitempty"` // goal | loop | plain | attended
	Budget      string   `yaml:"budget,omitempty"`
	CatchUp     string   `yaml:"catch_up,omitempty"`
	Timezone    string   `yaml:"tz,omitempty"`
	At          []string `yaml:"at"`
	Ready       string   `yaml:"ready,omitempty"`        // pane text that means the agent takes input
	Accepted    string   `yaml:"accepted,omitempty"`     // pane text that means the agent took the instruction
	Command     []string `yaml:"command,omitempty"`      // argv run instead of `proveo run <target>`
	Paused      bool     `yaml:"paused,omitempty"`       // no run starts until the file says otherwise
	PausedUntil string   `yaml:"paused_until,omitempty"` // YYYY-MM-DD in tz: the first day the job runs again
}

// Config is the schedule file.
type Config struct {
	Jobs map[string]Job `yaml:"jobs"`
}

// Path is the schedule file under the proveo home.
func Path(home string) string { return filepath.Join(home, FileName) }

// LoadFile reads one schedule file strictly, unknown keys included, and checks every job in it.
func LoadFile(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	for _, name := range c.Names() {
		if err := c.Jobs[name].validate(); err != nil {
			return c, fmt.Errorf("%s: job %q: %w", path, name, err)
		}
	}
	return c, nil
}

// Names lists the jobs in name order.
func (c Config) Names() []string {
	names := make([]string, 0, len(c.Jobs))
	for name := range c.Jobs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Load(home string) (Config, error) {
	var c Config
	b, err := os.ReadFile(Path(home))
	if err != nil && !os.IsNotExist(err) {
		return c, err
	}
	if err == nil {
		if err := yaml.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("%s: %w", Path(home), err)
		}
	}
	manualErr := err
	if err := loadDashboardJobs(home, &c); err != nil {
		return c, fmt.Errorf("%s: %w", Path(home), err)
	}
	if len(c.Jobs) == 0 && manualErr != nil {
		return c, manualErr
	}
	for name, j := range c.Jobs {
		if err := j.validate(); err != nil {
			return c, fmt.Errorf("%s: job %q: %w", Path(home), name, err)
		}
	}
	return c, nil
}

func (j Job) validate() error {
	if j.Target == "" && len(j.Command) == 0 {
		return fmt.Errorf("target or command is required")
	}
	if j.PromptFile == "" {
		return fmt.Errorf("prompt_file is required")
	}
	if (j.New && j.Instance != "") || (j.Task != "" && !j.New) ||
		(len(j.Command) > 0 && (j.New || j.Instance != "")) {
		return fmt.Errorf("instance and new are exclusive, task requires new, and instance selection requires a target")
	}
	switch j.Mode {
	case "", "goal", "loop", "plain", ModeAttended:
	default:
		return fmt.Errorf("mode %q: want goal, loop, plain or attended", j.Mode)
	}
	if _, err := j.Location(); err != nil {
		return err
	}
	for _, a := range j.At {
		if _, _, _, err := parseAt(a); err != nil {
			return err
		}
	}
	if _, err := j.BudgetDuration(); err != nil {
		return err
	}
	if j.Paused && j.PausedUntil != "" {
		return fmt.Errorf("paused and paused_until are exclusive")
	}
	if _, err := j.ResumeAt(); err != nil {
		return err
	}
	_, err := j.CatchUpDuration()
	return err
}

// ResumeAt is the start of the day paused_until names, in the job's tz; zero when no date is set.
func (j Job) ResumeAt() (time.Time, error) {
	if strings.TrimSpace(j.PausedUntil) == "" {
		return time.Time{}, nil
	}
	loc, err := j.Location()
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(j.PausedUntil), loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("paused_until %q: want YYYY-MM-DD", j.PausedUntil)
	}
	return t, nil
}

// PausedAt says whether the job skips a run due at t.
func (j Job) PausedAt(t time.Time) bool {
	if j.Paused {
		return true
	}
	until, err := j.ResumeAt()
	return err == nil && t.Before(until)
}

// NextRun is the job's soonest run after now that no pause skips; false when nothing starts it on its own.
func (j Job) NextRun(now time.Time) (time.Time, bool) {
	loc, err := j.Location()
	if err != nil || j.Paused {
		return time.Time{}, false
	}
	from := now
	if until, err := j.ResumeAt(); err == nil && until.After(now) {
		from = until.Add(-time.Nanosecond)
	}
	var best time.Time
	for _, e := range j.At {
		t, err := NextOccurrence(e, from, loc)
		if err == nil && (best.IsZero() || t.Before(best)) {
			best = t
		}
	}
	return best, !best.IsZero()
}

func (j Job) Location() (*time.Location, error) {
	tz := j.Timezone
	if tz == "" {
		tz = DefaultTimezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("tz %q: %w", tz, err)
	}
	return loc, nil
}

func duration(v string, def time.Duration) (time.Duration, error) {
	if strings.TrimSpace(v) == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("duration %q: want e.g. 45m", v)
	}
	return d, nil
}

func (j Job) BudgetDuration() (time.Duration, error)  { return duration(j.Budget, DefaultBudget) }
func (j Job) CatchUpDuration() (time.Duration, error) { return duration(j.CatchUp, DefaultCatchUp) }

func (j Job) ModeOrDefault() string {
	if j.Mode == "" {
		return "goal"
	}
	return j.Mode
}

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// parseAt reads "thu 18:45" (weekday, 24h clock).
func parseAt(s string) (time.Weekday, int, int, error) {
	f := strings.Fields(strings.ToLower(s))
	if len(f) != 2 {
		return 0, 0, 0, fmt.Errorf("at %q: want \"<mon..sun> HH:MM\"", s)
	}
	wd, ok := weekdays[f[0][:min(3, len(f[0]))]]
	if !ok {
		return 0, 0, 0, fmt.Errorf("at %q: unknown weekday %q", s, f[0])
	}
	hh, mm, ok := strings.Cut(f[1], ":")
	h, errH := strconv.Atoi(hh)
	m, errM := strconv.Atoi(mm)
	if !ok || errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, 0, fmt.Errorf("at %q: want HH:MM", s)
	}
	return wd, h, m, nil
}

// lastOccurrence is the most recent time at or before now that entry names, in loc.
func lastOccurrence(entry string, now time.Time, loc *time.Location) (time.Time, error) {
	wd, h, m, err := parseAt(entry)
	if err != nil {
		return time.Time{}, err
	}
	n := now.In(loc)
	t := time.Date(n.Year(), n.Month(), n.Day(), h, m, 0, 0, loc)
	back := (int(n.Weekday()) - int(wd) + 7) % 7
	t = t.AddDate(0, 0, -back)
	if t.After(n) {
		t = t.AddDate(0, 0, -7)
	}
	return t, nil
}

// NextOccurrence is the first time strictly after now that entry names, in loc.
func NextOccurrence(entry string, now time.Time, loc *time.Location) (time.Time, error) {
	last, err := lastOccurrence(entry, now, loc)
	if err != nil {
		return time.Time{}, err
	}
	return last.AddDate(0, 0, 7), nil
}

// State records when each entry last fired.
type State map[string]map[string]time.Time

func statePath(home string) string { return filepath.Join(home, stateName) }

func LoadState(home string) State {
	s := State{}
	if b, err := os.ReadFile(statePath(home)); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func (s State) Save(home string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := statePath(home) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(home))
}

// Due names the jobs whose entry fell within catch-up of now and has not fired since.
type Due struct {
	Job   string
	Entry string
	At    time.Time
}

func (c Config) Due(now time.Time, st State) []Due {
	var out []Due
	for name, j := range c.Jobs {
		loc, err := j.Location()
		if err != nil {
			continue
		}
		catch, _ := j.CatchUpDuration()
		var best *Due
		for _, e := range j.At {
			t, err := lastOccurrence(e, now, loc)
			if err != nil || now.Sub(t) > catch || j.PausedAt(t) {
				continue
			}
			if fired, ok := st[name][e]; ok && !fired.Before(t) {
				continue
			}
			if best == nil || t.After(best.At) {
				best = &Due{Job: name, Entry: e, At: t}
			}
		}
		if best != nil {
			out = append(out, *best)
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Job < out[k].Job })
	return out
}

func (s State) Mark(job, entry string, at time.Time) {
	if s[job] == nil {
		s[job] = map[string]time.Time{}
	}
	s[job][entry] = at
}
