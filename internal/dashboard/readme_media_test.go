// SPEC: _spec/_assets/readme-topology.puml
package dashboard

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"github.com/proveo-ca/proveo/internal/activity"
	"github.com/proveo-ca/proveo/internal/choiceui"
	"github.com/proveo-ca/proveo/internal/sbx"
	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

// TestREADMEMedia draws dashboard.gif into $PROVEO_README_MEDIA from fixture data.
func TestREADMEMedia(t *testing.T) {
	out := os.Getenv("PROVEO_README_MEDIA")
	if out == "" {
		t.Skip("PROVEO_README_MEDIA=<dir> draws the README's dashboard GIF")
	}
	for _, tool := range []string{"ffmpeg", "gifsicle"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is needed: %v", tool, err)
		}
	}
	faces := loadMediaFaces(t)
	toolEnv := append(os.Environ(), "HOME="+os.Getenv("HOME"))
	user := t.TempDir()
	t.Setenv("HOME", user)
	home := mediaHome(t, user)

	m := mediaDashboard(t, user, home)
	mediaGraph(m)
	var d reel
	d.shot(m, 2600, "proveo — every task in this repo: review requests, failed runs, working agents, the queue")
	d.keys(m, 2000, "↑↓ ←→ move · an agent waiting on you shows under Attention required", "down")
	d.keys(m, 1600, "an ended run keeps its report", "right")
	d.keys(m, 3400, "↵ reads the report in the pane; a click on a link copies it", "enter")
	d.keys(m, 500, "", "esc")
	d.keys(m, 2000, "r retries a failed run · x cleans it up", "down")
	d.keys(m, 2400, "t shows the task's details", "t")
	d.keys(m, 400, "", "esc")
	d.keys(m, 2200, "n starts a new instance from a plan, a pull request or a Linear issue", "n")
	d.keys(m, 2600, "typing filters every source", "r", "a", "t", "e")
	d.keys(m, 2400, "Ctrl+G Tab switches to the stock sbx view · ? lists every key", "esc")
	d.write(t, faces, toolEnv, filepath.Join(out, "dashboard.gif"))

}

const mediaW, mediaH = 124, 36

var mediaNow = time.Date(2026, 12, 3, 9, 12, 0, 0, mustLoad("America/New_York"))

func mustLoad(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

// reel is a GIF's frames: what the dashboard drew, how long it holds, and the caption under it.
type reel struct{ frames []reelFrame }

type reelFrame struct {
	screen, caption string
	hold            time.Duration
}

func (r *reel) shot(m *dashModel, ms int, caption string) {
	r.frames = append(r.frames, reelFrame{m.View().Content, caption, time.Duration(ms) * time.Millisecond})
}

func (r *reel) keys(m *dashModel, ms int, caption string, keys ...string) {
	for _, k := range keys {
		press(m, k)
		if len(keys) > 1 {
			r.shot(m, 180, caption)
		}
	}
	r.shot(m, ms, caption)
}

// press types one key into the model and runs none of the reads it asks for; the fixture already holds them.
func press(m *dashModel, k string) tea.Cmd {
	msg := tea.KeyPressMsg{Text: k}
	switch k {
	case "up":
		msg = tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		msg = tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		msg = tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		msg = tea.KeyPressMsg{Code: tea.KeyRight}
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	default:
		msg.Code = []rune(k)[0]
	}
	_, cmd := m.Update(msg)
	if c, ok := mediaChanges[m.task().ID]; ok {
		m.changes, m.changesLoad, m.tree = c, treeLoad{}, nil
	}
	return cmd
}

var mediaChanges = map[string][]Change{
	"acme-43": {
		{Kind: "M", Path: "store/clone.go"}, {Kind: "A", Path: "store/clone_isolation_test.go"},
	},
	"acme-41": {
		{Kind: "M", Path: "api/billing/limits.go"}, {Kind: "A", Path: "api/billing/limits_test.go"},
		{Kind: "M", Path: "api/router.go"}, {Kind: "M", Path: "docs/billing.md"},
	},
	"acme-37": {
		{Kind: "M", Path: "worker/budget.go"}, {Kind: "M", Path: "worker/budget_test.go"}, {Kind: "M", Path: "go.mod"},
	},
	"acme-39": {
		{Kind: "A", Path: "store/history.go"}, {Kind: "A", Path: "store/history_test.go"},
		{Kind: "M", Path: "store/migrations/0007_history.sql"}, {Kind: "R100", Path: "store/session.go", OldPath: "store/sessions.go"},
	},
}

func mediaDashboard(t *testing.T, user, home string) *dashModel {
	t.Helper()
	m := newModel(Options{CWD: filepath.Join(user, "projects", "acme"), Home: home}, ui.GlyphsSafe)
	m.clock = func() time.Time { return mediaNow }
	m.width, m.height = mediaW, mediaH
	m.say("")
	m.tickStatus = func() schedule.Tick {
		return schedule.Tick{Installed: true, Manager: "launchd io.proveo.schedule, every 60s"}
	}
	m.opener = func(string) error { return nil }
	m.clipboard = func(string) error { return nil }
	report := filepath.Join(home, "logs", "acme-39-report.md")
	if err := os.WriteFile(report, []byte(mediaReport), 0o600); err != nil {
		t.Fatal(err)
	}
	m.reviews = []ReviewRequest{{
		Number: 128, URL: "https://github.com/acme/api/pull/128", Title: "Rate-limit the billing API", Author: "dana",
		Repo: "acme/api", Head: "rate-limit-billing", Base: "main", Additions: 214, Deletions: 37,
		Created: mediaNow.Add(-26 * time.Hour), Mergeable: "MERGEABLE", Checks: prChecks{Passed: 9},
		Decision: "REVIEW_REQUIRED", Reviews: prReviews{Approved: []string{"lee"}},
		Files: []string{"api/billing/limits.go", "api/billing/limits_test.go", "api/router.go", "docs/billing.md"},
	}}
	tasks := []Task{
		{ID: "acme-37", Title: "Cap overnight worker budgets", Agent: "claudecode", Phase: "Review", Ticket: "ACME-37",
			State: schedule.Attention, Outcome: "failed", Updated: mediaNow.Add(-5 * time.Hour), Job: "acme-37",
			Reason: "go test ./worker: TestBudgetCap failed (1 of 41)"},
		{ID: "acme-39", Title: "Persist session history", Agent: "codex", Phase: "Review", Ticket: "ACME-39",
			State: schedule.Attention, Outcome: "done", Updated: mediaNow.Add(-3 * time.Hour), Job: "acme-39", Report: report,
			Summary: "history persists across restarts; 12 tests added", Base: "6bf4c2a9e1", Tip: "1c2f6e4b07"},
		{ID: "acme-41", Title: "Add OpenAPI examples for billing", Agent: "claudecode", Phase: "Working", Ticket: "ACME-41",
			State: schedule.Running, Live: true, Guest: true, Sandbox: "proveo-claudecode-41c2", VMID: "vm-41",
			Updated: mediaNow.Add(-40 * time.Minute)},
		{ID: "acme-43", Title: "Verify parallel clone isolation", Agent: "opencode", Phase: "Live verification", Ticket: "ACME-43",
			State: schedule.Running, Live: true, Guest: true, Sandbox: "proveo-opencode-3f2a", VMID: "vm-43",
			Updated: mediaNow.Add(-15 * time.Minute)},
		{ID: "acme-44", Title: "Bind evidence to the review revision", Agent: "claudecode", Phase: "Upgrade", Ticket: "ACME-44",
			State: schedule.Queued, DependsOn: []string{"acme-39", "acme-43"}},
	}
	m.data = Data{Tasks: m.withGhosts(tasks), Capacity: sbx.Capacity{Cores: 10, BusyCores: 5, CPUUsage: true,
		RAM: 32 << 30, RAMFree: 14 << 30, RAMUsage: true, Disk: 1 << 40, DiskFree: 402 << 30}}
	m.samples = map[string]guestSample{
		"acme-41": {id: "acme-41", sandbox: "proveo-claudecode-41c2", vmid: "vm-41", busy: 1.4, cores: 4, ram: 3 << 30,
			ramTotal: 8 << 30, disk: 6 << 30, uptime: 40 * time.Minute, at: mediaNow, available: true,
			activity: activity.Busy, activitySince: mediaNow.Add(-2 * time.Minute)},
		"acme-43": {id: "acme-43", sandbox: "proveo-opencode-3f2a", vmid: "vm-43", busy: 0.6, cores: 4, ram: 2 << 30,
			ramTotal: 8 << 30, disk: 4 << 30, uptime: 15 * time.Minute, at: mediaNow, available: true,
			activity: activity.Waiting, activitySince: mediaNow.Add(-1 * time.Minute)},
	}
	m.pickCache = map[string]pickCached{
		"plans\x00Plans": {choiceui.TaskSection{Title: "Plans", Items: []choiceui.TaskItem{
			{Title: "billing-rate-limits", Status: "plan", Prompt: "Implement the plan in _plans/billing-rate-limits.md.", Branch: "billing-rate-limits"},
			{Title: "webhook-retries", Status: "draft", Prompt: "Implement the plan in _plans/webhook-retries.md.", Branch: "webhook-retries"},
		}}, time.Now()},
		"github\x00Pull requests": {choiceui.TaskSection{Title: "Pull requests", Items: []choiceui.TaskItem{
			{Title: "#131 Retry failed webhooks", Status: "conflicts", Tone: "warn", Prompt: "Continue #131.", Branch: "webhook-retries"},
			{Title: "#126 Split the rate limiter (3 PRs)", Status: "ready", Tone: "ok", Prompt: "Continue the stack.", Branch: "limiter-3"},
		}}, time.Now()},
		"linear\x00Linear": {choiceui.TaskSection{Title: "Linear", Items: []choiceui.TaskItem{
			{Title: "ACME-45 Rate-limit headers in API responses", Status: "Todo", Prompt: "Work on ACME-45.", Branch: "ACME-45"},
			{Title: "ACME-46 Flaky invoice export test", Status: "In Progress", Tone: "warn", Prompt: "Work on ACME-46.", Branch: "ACME-46"},
			{Title: "ACME-48 Document the rate limits", Status: "Backlog", Prompt: "Work on ACME-48.", Branch: "ACME-48"},
		}}, time.Now()},
	}
	m.receiveSchedule(loadSchedule(home, mediaNow, m.tickStatus))
	m.selected = len(m.data.Tasks)
	m.selectVisible()
	m.changes = mediaChanges["acme-41"]
	return m
}

const mediaReport = `# Persist session history

RESULT: history persists across restarts; 12 tests added

## What changed

| File | Change |
|---|---|
| store/history.go | new: append-only history per session |
| store/migrations/0007_history.sql | new table, indexed by session |
| store/session.go | renamed from sessions.go |

## How it was checked

- ` + "`go test ./store/...`" + ` passes (12 new tests)
- Restarted the API twice; the history survived both
- See [the migration](store/migrations/0007_history.sql)
`

// mediaHome is a proveo home with the README's schedule: a fantasy-football team and a weekly triage.
func mediaHome(t *testing.T, user string) string {
	t.Helper()
	home := filepath.Join(user, ".proveo")
	for _, d := range []string{"prompts", "logs"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	prompts := map[string]string{
		"ff-lineup":    "Scheduled run, pass 1 of 2.\nGoal: set the week's lineup before inactives post.\nCheck injuries and byes, fill every slot, save, then report.\n",
		"ff-refine":    "Scheduled run, pass 2 of 2.\nGoal: break ties by matchup, scan free agents, save, report.\n",
		"ff-odds":      "Read-only: collect the week's NFL odds into ff-odds.md.\n",
		"ff-waivers":   "Claim the best waiver targets within the FAAB budget.\n",
		"issue-triage": "Label new issues, close duplicates, name the ones that need a decision.\n",
	}
	for name, body := range prompts {
		if err := os.WriteFile(filepath.Join(home, "prompts", name+".md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	yml := strings.ReplaceAll(strings.ReplaceAll(mediaSchedule, "PROMPTS", filepath.Join(home, "prompts")), "WORKDIR", filepath.Join(home, "schedule"))
	if err := os.WriteFile(schedule.Path(home), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := schedule.LoadFile(schedule.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]schedule.Result{
		"ff-lineup":    {Outcome: "done", Entry: "mon 18:50", Summary: "1 change — a questionable RB to the bench for a TE with a better matchup"},
		"ff-refine":    {Outcome: "budget", Entry: "mon 19:32", Summary: "kept the lineup; no free agent beat the bench"},
		"ff-odds":      {Outcome: "done", Entry: "tue 23:15", Summary: "week 13 — 16 games from 2 books"},
		"ff-waivers":   {Outcome: "done", Entry: "tue 22:00", Summary: "claimed 1 player for $4 FAAB"},
		"issue-triage": {Outcome: "done", Entry: "thu 18:30", Summary: "labelled 9 issues; 2 need a decision"},
	}
	for name, r := range results {
		loc, _ := c.Jobs[name].Location()
		at, _ := mediaLast(r.Entry, loc)
		r.Job, r.Started, r.Finished = name, at, at.Add(30*time.Minute)
		dir := schedule.LogDir(home, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(r)
		if err := os.WriteFile(filepath.Join(dir, at.UTC().Format("20060102T150405Z")+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func mediaLast(entry string, loc *time.Location) (time.Time, error) {
	next, err := schedule.NextOccurrence(entry, mediaNow, loc)
	return next.AddDate(0, 0, -7), err
}

const mediaSchedule = `# Times are kickoff-relative: each pass ends before that window's lineup lock.
jobs:
  ff-lineup:          # pass 1 of 2: injuries, byes, fill slots, save, report
    target: hermes
    model: inkling-2-mini
    addons: [host-chrome]
    prompt_file: PROMPTS/ff-lineup.md
    mode: goal
    budget: 36m
    tz: America/New_York
    at:
      - thu 18:50   # TNF 8:15 pm, inactives 6:45
      - sun 11:35   # 1:00 pm games
      - sun 15:00   # 4:05 / 4:25 pm games
      - sun 18:55   # SNF 8:20 pm
      - mon 18:50   # MNF 8:15 pm
  ff-refine:          # pass 2 of 2: matchup tie-breaks, free agents
    target: hermes
    model: inkling-2-mini
    addons: [host-chrome]
    prompt_file: PROMPTS/ff-refine.md
    workdir: WORKDIR/ff-lineup   # reads pass 1's handoff
    budget: 36m
    tz: America/New_York
    at: ["thu 19:32", "sun 12:17", "sun 19:37", "mon 19:32"]
  ff-odds:            # read-only: the week's odds, the lineup passes' fallback
    target: hermes
    model: inkling-2-mini
    addons: [host-chrome]
    prompt_file: PROMPTS/ff-odds.md
    budget: 20m
    tz: America/New_York
    at: ["tue 23:15", "thu 18:15"]
  ff-waivers:
    target: hermes
    model: inkling-2-mini
    addons: [host-chrome]
    prompt_file: PROMPTS/ff-waivers.md
    budget: 60m
    tz: America/Los_Angeles
    at: ["tue 22:00", "tue 23:55"]
  issue-triage:       # a coding agent on the repo, weekly
    target: codex
    prompt_file: PROMPTS/issue-triage.md
    mode: plain
    budget: 30m
    tz: America/New_York
    at: ["thu 18:30"]
`

// mediaFaces are the fonts a frame is drawn in: regular, bold, then fallbacks for runes they lack.
type mediaFaces struct {
	faces      []font.Face
	fonts      []*sfnt.Font
	bold       font.Face
	cw, ch, as int
}

func loadMediaFaces(t *testing.T) mediaFaces {
	t.Helper()
	home, _ := os.UserHomeDir()
	reg := envOr("PROVEO_README_FONT", filepath.Join(home, "Library/Fonts/FiraCodeNerdFont-Regular.ttf"))
	bold := envOr("PROVEO_README_FONT_BOLD", filepath.Join(home, "Library/Fonts/FiraCodeNerdFont-Bold.ttf"))
	fallbacks := []string{"/System/Library/Fonts/Menlo.ttc", "/System/Library/Fonts/Apple Symbols.ttf"}
	if v := os.Getenv("PROVEO_README_FONT_FALLBACK"); v != "" {
		fallbacks = strings.Split(v, ":")
	}
	const size = 15
	var f mediaFaces
	open := func(path string) (*sfnt.Font, font.Face) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("font %s: %v (set PROVEO_README_FONT*)", path, err)
		}
		var fnt *sfnt.Font
		if c, err := opentype.ParseCollection(b); err == nil && c.NumFonts() > 0 {
			fnt, _ = c.Font(0)
		} else if fnt, err = opentype.Parse(b); err != nil {
			t.Fatalf("font %s: %v", path, err)
		}
		face, err := opentype.NewFace(fnt, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			t.Fatal(err)
		}
		return fnt, face
	}
	for _, p := range append([]string{reg}, fallbacks...) {
		fnt, face := open(p)
		f.fonts, f.faces = append(f.fonts, fnt), append(f.faces, face)
	}
	_, f.bold = open(bold)
	adv, _ := f.faces[0].GlyphAdvance('M')
	met := f.faces[0].Metrics()
	f.cw, f.as = adv.Ceil(), met.Ascent.Ceil()
	f.ch = (met.Ascent + met.Descent).Ceil() + 3
	return f
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// face is the first face that has r, bold when asked and the bold face has it.
func (f mediaFaces) face(r rune, bold bool) font.Face {
	var buf sfnt.Buffer
	for i, fnt := range f.fonts {
		if idx, err := fnt.GlyphIndex(&buf, r); err == nil && idx != 0 {
			if i == 0 && bold {
				return f.bold
			}
			return f.faces[i]
		}
	}
	return f.faces[0]
}

const mediaPad, captionH = 14, 30

// draw rasterises one screen of ANSI text, with its caption in a strip under it.
func (f mediaFaces) draw(screen, caption string) *image.RGBA {
	e := vt.NewEmulator(mediaW, mediaH)
	_, _ = e.WriteString(strings.ReplaceAll(screen, "\n", "\r\n"))
	bg, fg := rgb(0x1B1B20), rgb(0xE5E4E4)
	img := image.NewRGBA(image.Rect(0, 0, mediaW*f.cw+2*mediaPad, mediaH*f.ch+2*mediaPad+captionH))
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	for y := range mediaH {
		for x := range mediaW {
			c := e.CellAt(x, y)
			if c == nil || c.Width == 0 {
				continue
			}
			var cbg, cfg color.Color = bg, fg
			if c.Style.Bg != nil {
				cbg = c.Style.Bg
			}
			if c.Style.Fg != nil {
				cfg = c.Style.Fg
			}
			if c.Style.Attrs&uv.AttrReverse != 0 {
				cbg, cfg = cfg, cbg
			}
			x0, y0 := mediaPad+x*f.cw, mediaPad+y*f.ch
			cell := image.Rect(x0, y0, x0+f.cw*max(1, c.Width), y0+f.ch)
			draw.Draw(img, cell, &image.Uniform{cbg}, image.Point{}, draw.Src)
			r := []rune(c.Content)
			if len(r) == 0 || r[0] == ' ' {
				continue
			}
			if f.block(img, cell, r[0], cfg, cbg) {
				continue
			}
			d := font.Drawer{Dst: img, Src: &image.Uniform{cfg}, Face: f.face(r[0], c.Style.Attrs&uv.AttrBold != 0),
				Dot: fixed.P(x0, y0+f.as+1)}
			d.DrawString(c.Content)
			if c.Style.Underline != uv.UnderlineNone {
				draw.Draw(img, image.Rect(x0, y0+f.as+3, x0+f.cw*max(1, c.Width), y0+f.as+4), &image.Uniform{cfg}, image.Point{}, draw.Src)
			}
		}
	}
	x, y := mediaPad, mediaPad+mediaH*f.ch+captionH/2+f.as/2
	for _, r := range caption {
		d := font.Drawer{Dst: img, Src: &image.Uniform{rgb(0x00BAC6)}, Face: f.face(r, false), Dot: fixed.P(x, y)}
		d.DrawString(string(r))
		x += f.cw
	}
	return img
}

// block draws the box and block runes as geometry, so frames and grid cells join without seams.
func (f mediaFaces) block(img *image.RGBA, r image.Rectangle, ch rune, fg, bg color.Color) bool {
	midX, midY := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
	line := func(rr image.Rectangle) { draw.Draw(img, rr, &image.Uniform{fg}, image.Point{}, draw.Src) }
	hz := func(x0, x1 int) { line(image.Rect(x0, midY, x1, midY+1)) }
	vl := func(y0, y1 int) { line(image.Rect(midX, y0, midX+1, y1)) }
	switch ch {
	case '█':
		line(r)
	case '▒', '░':
		step := 2
		if ch == '░' {
			step = 3
		}
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				if (x+y)%step == 0 {
					img.Set(x, y, fg)
				}
			}
		}
	case '─':
		hz(r.Min.X, r.Max.X)
	case '│':
		vl(r.Min.Y, r.Max.Y)
	case '╭':
		hz(midX, r.Max.X)
		vl(midY, r.Max.Y)
	case '╮':
		hz(r.Min.X, midX+1)
		vl(midY, r.Max.Y)
	case '╰':
		hz(midX, r.Max.X)
		vl(r.Min.Y, midY+1)
	case '╯':
		hz(r.Min.X, midX+1)
		vl(r.Min.Y, midY+1)
	default:
		return false
	}
	_ = bg
	return true
}

func rgb(v int) color.RGBA { return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xFF} }

// write draws every frame to PNG, then ffmpeg holds each as long as it asks and gifsicle packs the GIF.
func (r reel) write(t *testing.T, f mediaFaces, env []string, gif string) {
	t.Helper()
	dir := t.TempDir()
	var list strings.Builder
	for i, fr := range r.frames {
		name := filepath.Join(dir, fmt.Sprintf("f%03d.png", i))
		out, err := os.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(out, f.draw(fr.screen, fr.caption)); err != nil {
			t.Fatal(err)
		}
		_ = out.Close()
		fmt.Fprintf(&list, "file '%s'\nduration %.3f\n", name, fr.hold.Seconds())
	}
	fmt.Fprintf(&list, "file '%s'\n", filepath.Join(dir, fmt.Sprintf("f%03d.png", len(r.frames)-1)))
	listFile := filepath.Join(dir, "frames.txt")
	if err := os.WriteFile(listFile, []byte(list.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	raw := filepath.Join(dir, "raw.gif")
	ff := exec.Command("ffmpeg", "-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", listFile,
		"-vf", "fps=10,split[a][b];[a]palettegen=max_colors=128:stats_mode=full[p];[b][p]paletteuse=dither=none", "-loop", "0", raw)
	ff.Env = env
	if out, err := ff.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	gs := exec.Command("gifsicle", "-O3", "--no-comments", "-o", gif, raw)
	gs.Env = env
	if out, err := gs.CombinedOutput(); err != nil {
		t.Fatalf("gifsicle: %v\n%s", err, out)
	}
	st, _ := os.Stat(gif)
	t.Logf("%s: %d frames, %d KiB", gif, len(r.frames), st.Size()/1024)
}

// mediaGraph links the fixture tasks the four ways the graph reads: depends_on (already in the tasks), a PR
// stacked on the review's branch, a Linear blocker, and a plan's blocked_by.
func mediaGraph(m *dashModel) {
	set := func(id string, meta taskMetadata) {
		i := slices.IndexFunc(m.data.Tasks, func(t Task) bool { return t.ID == id })
		meta.id, meta.key, meta.at = id, metadataKey(m.data.Tasks[i]), time.Now()
		m.metadata[meta.key] = meta
	}
	set("acme-41", taskMetadata{prs: []prInfo{{Number: 131, Head: "billing-openapi", Base: "rate-limit-billing"}},
		linear: linearIssue{Identifier: "ACME-41", BlockedBy: []linearRef{{Identifier: "ACME-40", Title: "Meter billing usage", State: "In Progress"}}}})
	set("acme-37", taskMetadata{plan: planInfo{Slug: "worker-budgets", BlockedBy: []string{"billing-usage-meter", "ACME-40"}},
		planRefs: map[string]planInfo{"plan:billing-usage-meter": {Slug: "billing-usage-meter", Status: "planned"}}})
}
