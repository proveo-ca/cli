// SPEC: _spec/internal/dashboard/schedule-manager.puml
package dashboard

import (
	"errors"
	"io/fs"
	"os"
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

// scheduleView is view 3: schedule.yml as a week of runs, its jobs, and the selected job's details.
const scheduleView = 2

// scheduleData is schedule.yml as the schedule view last read it.
type scheduleData struct {
	path  string
	jobs  []scheduledJob
	slots []schedule.Slot
	week  time.Time // Monday 00:00 of the week shown, on the dashboard's clock
	tick  schedule.Tick
	err   string // why schedule.yml could not be used
	read  bool
}

type scheduledJob struct {
	name   string
	job    schedule.Job
	next   time.Time // zero for a job nothing starts on its own
	live   bool
	last   *schedule.Result
	line   int    // the line that opens the job in schedule.yml
	prompt string // the head of its prompt file
}

type scheduleEvent struct{ data scheduleData }

func (m *dashModel) readSchedule() tea.Cmd {
	home, now, tick := m.Home, m.now(), m.tickStatus
	if tick == nil {
		tick = func() schedule.Tick { return schedule.TickStatus(schedule.ExecRunner) }
	}
	return func() tea.Msg { return scheduleEvent{loadSchedule(home, now, tick)} }
}

func loadSchedule(home string, now time.Time, tick func() schedule.Tick) scheduleData {
	d := scheduleData{path: schedule.Path(home), week: schedule.WeekStart(now, now.Location()), read: true, tick: tick()}
	c, err := schedule.LoadFile(d.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return d
	case err != nil:
		d.err = strings.TrimPrefix(err.Error(), d.path+": ")
		return d
	}
	src, _ := os.ReadFile(d.path)
	for _, name := range c.Names() {
		j := c.Jobs[name]
		sj := scheduledJob{name: name, job: j, line: schedule.JobLine(src, name), last: schedule.LastResult(home, name)}
		sj.next, _ = j.NextRun(now)
		sj.live = schedule.NewTmux(schedule.SessionName(name), nil).Alive()
		sj.prompt, _ = readText(j.PromptFile)
		d.jobs = append(d.jobs, sj)
	}
	slices.SortStableFunc(d.jobs, func(a, b scheduledJob) int {
		switch {
		case a.next.IsZero() != b.next.IsZero():
			if a.next.IsZero() {
				return 1
			}
			return -1
		case !a.next.Equal(b.next):
			return a.next.Compare(b.next)
		}
		return strings.Compare(a.name, b.name)
	})
	d.slots = schedule.Week(c, d.week)
	return d
}

// receiveSchedule keeps the selection on the same job across a read.
func (m *dashModel) receiveSchedule(d scheduleData) {
	name := m.schedJob().name
	m.sched = d
	if i := slices.IndexFunc(d.jobs, func(j scheduledJob) bool { return j.name == name }); i >= 0 {
		m.schedSel = i
	}
	m.schedSel = min(max(0, m.schedSel), max(0, len(d.jobs)-1))
}

func (m *dashModel) schedJob() scheduledJob {
	if m.schedSel >= 0 && m.schedSel < len(m.sched.jobs) {
		return m.sched.jobs[m.schedSel]
	}
	return scheduledJob{}
}

// inSchedule says whether the schedule view owns the keys: view 3, with no report or file over it.
func (m *dashModel) inSchedule() bool { return m.view == scheduleView && m.report == nil && !m.preview }

// scheduleKey handles the schedule view's keys; false hands the key to the dashboard's own.
func (m *dashModel) scheduleKey(k string) (tea.Cmd, bool) {
	j := m.schedJob()
	switch k {
	case "up", "down", "pgup", "pgdown", "home", "end":
		m.schedMove(k)
	case "enter":
		return m.openJob(j), true
	case "a":
		m.attachJob(j)
	case "e":
		if j.name == "" {
			return m.editSchedule("", 0), true
		}
		return m.editSchedule(j.name, j.line), true
	case "n":
		return m.editSchedule(newJobName(m.sched.jobs), 0), true
	case "p":
		m.openPause(j)
	case "r":
		if j.name != "" && !j.live {
			m.say("Starting " + safeText(j.name) + "…")
			return m.scheduleCmd(j.name, "run", "run", j.name), true
		}
	case "1", "2", "3", "?", "c", "esc":
		return nil, false
	}
	return nil, true
}

func (m *dashModel) schedMove(k string) {
	n := len(m.sched.jobs)
	switch k {
	case "up":
		m.schedSel--
	case "down":
		m.schedSel++
	case "pgup", "home":
		m.schedSel = 0
	case "pgdown", "end":
		m.schedSel = n - 1
	}
	m.schedSel = min(max(0, m.schedSel), max(0, n-1))
}

// openJob attaches to a running job, or reads its last report.
func (m *dashModel) openJob(j scheduledJob) tea.Cmd {
	switch {
	case j.name == "":
	case j.live:
		m.attachJob(j)
	case j.last != nil && j.last.Report != "":
		m.openReport(Task{ID: j.name, Title: j.name, Job: j.name, Report: j.last.Report})
	case j.last != nil:
		m.say(safeText(j.name) + " left no report; its transcript is " + ui.Tilde(j.last.Transcript))
	default:
		m.say(safeText(j.name) + " has not run yet; r starts it now")
	}
	return nil
}

func (m *dashModel) attachJob(j scheduledJob) {
	if !j.live {
		return
	}
	name := j.name
	m.confirm = choiceui.NewAttachConfirm(safeText(j.job.Target), safeText(name), m.width, m.height)
	m.onConfirm = func() tea.Cmd {
		return m.handoffTo("Attaching to "+safeText(name), j.job.Target, hint("Ctrl+B d", "detaches back here"), "Back from "+safeText(name),
			m.Binary, "attach", name)
	}
}

// openPause asks how long the job holds its runs back, or whether a paused one runs again.
func (m *dashModel) openPause(j scheduledJob) {
	if j.name == "" {
		return
	}
	name := j.name
	if held := pauseText(j.job, m.now()); held != "" {
		m.confirm = choiceui.NewResumeConfirm(safeText(name), []string{held}, m.width, m.height)
		m.onConfirm = func() tea.Cmd { return m.setPause(name, schedule.Pause{}) }
		return
	}
	m.pauseFor = name
	m.pauseChoice = choiceui.NewPauseChoice(safeText(name), []string{
		"Writes paused or paused_until into its block of " + ui.Tilde(m.sched.path) + ".",
		"A run already under way keeps going.",
	}, m.width, m.height)
}

// pauseAnswer writes the pause the choice named.
func (m *dashModel) pauseAnswer(answer int) tea.Cmd {
	name := m.pauseFor
	var p schedule.Pause
	switch answer {
	case choiceui.PauseWeek:
		p.Until = m.resumeDay(name, 7)
	case choiceui.PauseFourWeeks:
		p.Until = m.resumeDay(name, 28)
	case choiceui.PauseForever:
		p.Forever = true
	default:
		return nil
	}
	return m.setPause(name, p)
}

// resumeDay is the day, in the job's tz, days after today.
func (m *dashModel) resumeDay(name string, days int) time.Time {
	loc := time.Local
	if i := slices.IndexFunc(m.sched.jobs, func(j scheduledJob) bool { return j.name == name }); i >= 0 {
		if l, err := m.sched.jobs[i].job.Location(); err == nil {
			loc = l
		}
	}
	n := m.now().In(loc)
	return time.Date(n.Year(), n.Month(), n.Day()+days, 0, 0, 0, 0, time.UTC)
}

type pausedEvent struct {
	job string
	p   schedule.Pause
	err error
}

func (m *dashModel) setPause(name string, p schedule.Pause) tea.Cmd {
	path := m.sched.path
	return func() tea.Msg { return pausedEvent{name, p, schedule.SetPaused(path, name, p)} }
}

func (m *dashModel) receivePaused(e pausedEvent) tea.Cmd {
	switch {
	case e.err != nil:
		m.fail(e.err)
	case e.p.Forever:
		m.say(safeText(e.job) + " paused until resumed")
	case !e.p.Until.IsZero():
		m.say(safeText(e.job) + " paused; runs again from " + e.p.Until.Format("Mon Jan 2"))
	default:
		m.say(safeText(e.job) + " resumed")
	}
	return m.readSchedule()
}

// pauseText says how a job is held back, or that it is not.
func pauseText(j schedule.Job, now time.Time) string {
	if j.Paused {
		return "Paused until resumed"
	}
	if until, err := j.ResumeAt(); err == nil && until.After(now) {
		return "Paused until " + until.Format("Mon Jan 2")
	}
	return ""
}

// newJobName is the first job-N no job holds.
func newJobName(jobs []scheduledJob) string {
	for i := 1; ; i++ {
		name := "job-" + strconv.Itoa(i)
		if !slices.ContainsFunc(jobs, func(j scheduledJob) bool { return j.name == name }) {
			return name
		}
	}
}

// scheduleHints are the schedule view's footer keys for the selected job.
func (m *dashModel) scheduleHints(add func(k, action string)) {
	j := m.schedJob()
	add("↑↓", "move")
	switch {
	case j.live:
		add("↵", "attach")
	case j.last != nil && j.last.Report != "":
		add("↵", "report")
	}
	if j.name != "" {
		add("e", "edit")
		if pauseText(j.job, m.now()) != "" {
			add("p", "resume")
		} else {
			add("p", "pause")
		}
		if !j.live {
			add("r", "run now")
		}
	} else {
		add("e", "open schedule.yml")
	}
	add("n", "new job")
}

// scheduleClick selects the job under a click in the schedule pane.
func (m *dashModel) scheduleClick(x, y int) {
	for _, s := range m.schedSpots {
		if s.y == y && x >= s.x0 && x < s.x1 {
			if i := slices.IndexFunc(m.sched.jobs, func(j scheduledJob) bool { return j.name == s.job }); i >= 0 {
				m.schedSel = i
			}
			return
		}
	}
}

// schedSpot is where a job shows on screen: a row of the job list or a run in the grid.
type schedSpot struct {
	y, x0, x1 int
	job       string
}

// renderSchedule draws the schedule pane: the tick, the week grid, then the job list.
func (m *dashModel) renderSchedule(w, h int) []string {
	d, now := m.sched, m.now()
	header := brand.Bold(true).Render("Schedule") + "  " + tickChip(d.tick, m.glyphs)
	var rows []string
	type lineSpot struct {
		row    int
		x0, x1 int
		job    string
	}
	var spots []lineSpot
	switch {
	case !d.read:
		rows = append(rows, muted.Render("Reading "+ui.Tilde(d.path)+"…"))
	case d.err != "":
		rows = append(rows, errorText.Render(glyph(m.glyphs, markFailed)+" "+filepath.Base(d.path)+" is not valid"), "  "+muted.Render(ui.Tilde(d.path)))
		for _, l := range strings.Split(d.err, "\n") {
			rows = append(rows, "  "+muted.Render(safeText(l)))
		}
		rows = append(rows, "", hint("e", "open it")+"   "+muted.Render("the tick keeps the last file it could read"))
	case len(d.jobs) == 0:
		rows = append(rows, muted.Render("No jobs in "+ui.Tilde(d.path)), "", hint("n", "add one"))
	default:
		g := weekGrid{slots: d.slots, week: d.week, now: now, sel: m.schedJob().name, glyphs: m.glyphs}
		grid, cells := g.render(w)
		for _, c := range cells {
			spots = append(spots, lineSpot{row: len(rows) + c.row, x0: c.x0, x1: c.x1, job: c.job})
		}
		rows = append(rows, grid...)
		if len(grid) > 0 {
			rows = append(rows, "")
		}
		rows = append(rows, groupHeader("Jobs", w))
	}
	anchor := 0
	for i, j := range d.jobs {
		if d.err != "" {
			break
		}
		if i == m.schedSel {
			anchor = len(rows)
		}
		spots = append(spots, lineSpot{row: len(rows), x0: 0, x1: w, job: j.name})
		rows = append(rows, m.jobRow(j, i == m.schedSel, now, w))
	}
	body := max(1, h-1)
	start := 0
	if anchor >= body {
		start = anchor - body + 1
	}
	m.schedSpots = m.schedSpots[:0]
	for _, s := range spots {
		if s.row >= start && s.row < start+body {
			x := m.rightPane.x + paneInset
			m.schedSpots = append(m.schedSpots, schedSpot{y: m.rightPane.y + 1 + s.row - start, x0: x + s.x0, x1: x + s.x1, job: s.job})
		}
	}
	return window(header, rows, start, w, h)
}

// tickChip says whether the minutely tick that starts the runs is registered.
func tickChip(t schedule.Tick, glyphs ui.GlyphTier) string {
	switch {
	case t.NoManager != "":
		return muted.Render("no tick on " + t.NoManager)
	case t.Installed:
		return successText.Render(glyph(glyphs, markDone)+" tick") + muted.Render(" "+firstField(t.Manager))
	}
	return warnText.Render("! tick not installed") + muted.Render(" · proveo schedule install")
}

func firstField(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return strings.TrimSuffix(f[0], ",")
	}
	return ""
}

// jobRow is one line of the job list: mark, name, when it runs next, and how its last run ended.
func (m *dashModel) jobRow(j scheduledJob, selected bool, now time.Time, w int) string {
	lead := "  "
	if selected {
		lead = brand.Render("› ")
	}
	mark, style := glyph(m.glyphs, markQueued), muted
	switch {
	case j.live:
		mark, style = glyph(m.glyphs, markRunning), brand
	case j.last != nil && j.last.Failed():
		mark, style = glyph(m.glyphs, markFailed), errorText
	case j.last != nil:
		mark, style = glyph(m.glyphs, markDone), successText
	}
	name := safeText(j.name)
	if selected {
		name = brand.Bold(true).Render(name)
	}
	when := ""
	switch {
	case j.live:
		when = "running"
	case pauseText(j.job, now) != "":
		when = strings.ToLower(pauseText(j.job, now)[:1]) + pauseText(j.job, now)[1:]
	case j.next.IsZero():
		when = "manual"
	default:
		when = j.next.In(now.Location()).Format("Mon 15:04") + " · " + compactUntil(j.next.Sub(now))
	}
	last := ""
	if j.last != nil {
		last = j.last.Outcome
	}
	left := lead + style.Render(mark) + " " + name
	right := muted.Render(when)
	if last != "" {
		right += muted.Render("  " + last)
	}
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 2 {
		return ansi.Truncate(left+"  "+right, w, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}

// compactUntil is a wait as "in 1d 9h", "in 40m", or "now".
func compactUntil(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return "in " + strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 24*time.Hour:
		return "in " + strconv.Itoa(int(d/time.Hour)) + "h " + strconv.Itoa(int(d%time.Hour/time.Minute)) + "m"
	}
	return "in " + strconv.Itoa(int(d/(24*time.Hour))) + "d " + strconv.Itoa(int(d%(24*time.Hour)/time.Hour)) + "h"
}

// renderJob draws the selected job in the left pane: when it runs, what it runs, its last run and its prompt.
func (m *dashModel) renderJob(w, h int) []string {
	j, now := m.schedJob(), m.now()
	if j.name == "" {
		return window(brand.Bold(true).Render("Job"), []string{muted.Render("No job selected")}, 0, w, h)
	}
	header := brand.Bold(true).Render(safeText(j.name))
	var rows []string
	add := func(s string) { rows = append(rows, ansi.Truncate(s, w, "…")) }
	field := func(k, v string) { add(muted.Render(pad(k, 9)) + v) }
	switch held := pauseText(j.job, now); {
	case j.live:
		field("Now", brand.Render(glyph(m.glyphs, markRunning)+" running")+muted.Render(" · ↵ attaches"))
	case held != "":
		field("Now", warnText.Render(held))
	case j.next.IsZero():
		field("Next", "manual"+muted.Render(" · r starts it"))
	default:
		field("Next", j.next.In(now.Location()).Format("Mon 15:04")+muted.Render(" · "+compactUntil(j.next.Sub(now))))
	}
	target := j.job.Target
	if len(j.job.Command) > 0 {
		target = "command"
	}
	budget, _ := j.job.BudgetDuration()
	field("Runs", safeText(target)+muted.Render(" · ")+j.job.ModeOrDefault())
	field("Budget", durText(budget))
	if j.job.Model != "" {
		field("Model", safeText(j.job.Model))
	}
	if len(j.job.Addons) > 0 {
		field("Add-ons", safeText(strings.Join(j.job.Addons, ", ")))
	}
	loc, _ := j.job.Location()
	if loc != nil && len(j.job.At) > 0 {
		zone := loc.String()
		same := loc.String() == now.Location().String()
		if !same {
			zone += muted.Render(" → your " + now.Format("MST"))
		}
		field("At", zone)
		for _, e := range j.job.At {
			line := "  " + safeText(e)
			if t, err := schedule.NextOccurrence(e, now, loc); err == nil && !same {
				line = pad(line, 13) + muted.Render(t.In(now.Location()).Format("Mon 15:04"))
			}
			add(line)
		}
	}
	if clash := m.overlapsOf(j.name); len(clash) > 0 {
		field("Overlaps", warnText.Render(strings.Join(clash, ", ")))
	}
	if r := j.last; r != nil {
		mark, style := glyph(m.glyphs, markDone), successText
		if r.Failed() {
			mark, style = glyph(m.glyphs, markFailed), errorText
		}
		field("Last", style.Render(mark+" "+r.Outcome)+muted.Render(" · "+r.Started.In(now.Location()).Format("Mon 15:04")))
		if s := firstNonEmpty(r.Summary, r.Detail); s != "" {
			for _, l := range strings.Split(ansi.Wordwrap(safeText(s), max(8, w-2), ""), "\n") {
				add("  " + muted.Render(l))
			}
		}
	}
	if j.job.PromptFile != "" {
		rows = append(rows, "")
		field("Prompt", keepTail(ui.Tilde(j.job.PromptFile), max(8, w-9)))
		for _, para := range strings.Split(strings.TrimSpace(safeMultiline(j.prompt)), "\n") {
			for _, l := range strings.Split(ansi.Wordwrap(para, max(8, w-2), ""), "\n") {
				if len(rows) < h-2 {
					add("  " + inkText.Render(l))
				}
			}
		}
	}
	return window(header, rows, 0, w, h)
}

// overlapsOf names each run of another job that shares time with one of job's this week, by its job and start.
func (m *dashModel) overlapsOf(job string) []string {
	var out []string
	for _, s := range m.sched.slots {
		if s.Job != job || len(s.Overlaps) == 0 {
			continue
		}
		for _, o := range m.sched.slots {
			if o.Job == job || o.Paused || !o.Start.Before(s.End) || !s.Start.Before(o.End) {
				continue
			}
			if label := safeText(o.Job) + " " + o.Start.In(m.now().Location()).Format("Mon 15:04"); !slices.Contains(out, label) {
				out = append(out, label)
			}
		}
	}
	return out
}

// durText is a duration as a schedule writes it: 40m, 1h, 1h30m.
func durText(d time.Duration) string {
	s := strings.TrimSuffix(d.String(), "0s")
	if strings.Contains(s, "h") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
