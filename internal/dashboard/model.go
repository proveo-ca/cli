// SPEC: _spec/internal/dashboard/workspace-instances.puml, _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/proveo-ca/proveo/internal/choiceui"
	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

type Options struct {
	CWD, Home, State, Binary string
	StartupError             error
}

type dashModel struct {
	Options
	data                                              Data
	width, height, sbxSplit                           int
	view, focus, selected, previewScroll              int
	showFiles, details, preview, patch, graphExpanded bool
	helpModal                                         *choiceui.Confirmation
	reviewModal                                       *choiceui.ReviewStart
	reviewFor                                         Task // the ghost the review card starts
	reviews                                           []ReviewRequest
	reviewStarting                                    map[string]bool      // PR URLs a review is being queued for
	dropping                                          map[string]bool      // failed review runs being cleaned up
	dropFailed                                        map[string]bool      // failed review runs whose clean-up failed; never retried this session
	reviewFailures                                    map[string]string    // why the last review of a PR URL failed
	portsFor                                          string               // the task whose ports the open notice lists
	diagramSource                                     bool                 // a diagram shows its source, not its sketch
	ending                                            map[string]bool      // ended runs a retry or clean-up is under way for
	onConfirm                                         func() tea.Cmd       // what accepting the open confirm does; nil attaches
	report                                            *reportDoc           // the ended run report the right pane shows
	reportScroll, reportAt                            int                  // the report row at the top, the selected reference (-1 none)
	previewLine                                       int                  // the line a report reference opens the file at
	previewLineFor                                    uint64               // the preview request that line belongs to
	opener                                            func(string) error   // hands a file to the OS viewer; nil uses open or xdg-open
	clipboard                                         func(string) error   // the host clipboard tool; nil uses pbcopy, wl-copy or xclip
	leftPane, rightPane                               rect                 // where the panes sat on screen at the last draw, for the mouse
	sel                                               reportSelection      // the report rows a drag or v marks
	links                                             []linkSpot           // the hyperlinks of the last frame drawn, for a click to copy
	pointer                                           point                // where the last press or release landed; reset by any key
	tip                                               tooltip              // the note over the dashboard, such as Copied to clipboard
	configRead                                        bool                 // dashboard.yml has been read at least once
	configCache                                       configCache          // dashboard.yml as last read
	taskPicker                                        *choiceui.TaskPicker // the new-instance card: pick a task, edit its prompt
	pickGen                                           int                  // which opening of the picker a source's answer belongs to
	pickCache                                         map[string]pickCached
	promptTemp                                        string // the prompt file a new instance was handed; removed when it returns
	tipSeq                                            int
	verdictModal                                      *choiceui.ReviewStart
	verdictPR                                         verdictTarget // the PR the open verdict card judges
	exit, sbxTab, prefix, alert                       bool
	child                                             childTerminal
	childBlankAt                                      time.Time // when the sbx frame last went blank
	childPainted                                      bool      // sbx has drawn at least once
	changesLoad, filesLoad                            treeLoad
	loadTicking                                       bool
	quietReads                                        bool // the next reads refresh the same task in the background
	status                                            string
	tree                                              *fileTree
	glyphs                                            ui.GlyphTier
	lay                                               layout
	clock                                             func() time.Time
	confirm, exitConfirm                              *choiceui.Confirmation
	attachID                                          string
	changes                                           []Change
	taskFiles                                         []Change
	changesBase, changesMB                            string
	changesLive                                       bool
	filesSource                                       string
	pop                                               popAnchor
	cardPos                                           map[int]cardPos
	previewDoc                                        previewDoc
	split                                             bool // the diff shows before | after side by side
	previewPath                                       string
	previewRequest                                    uint64
	guest                                             guestSample
	samples                                           map[string]guestSample
	sampleAt                                          int
	guestPending                                      bool
	freshPending                                      bool
	metadataPending                                   bool
	metadata                                          map[string]taskMetadata
	runner                                            func(binary string, args ...string) tea.Cmd
	sched                                             scheduleData     // schedule.yml as view 3 last read it
	schedSel                                          int              // the job view 3 has selected
	graphOrder                                        []int            // the tasks in the order view 2 last drew them
	schedSpots                                        []schedSpot      // where each job showed in the last frame, for a click
	schedEdit                                         *scheduleEdit    // the editor's draft of schedule.yml, while one is open or refused
	pauseChoice                                       *choiceui.Choice // how long the job pauseFor holds its runs back
	pauseFor                                          string
	onDecline                                         func()                               // what declining the open confirm does
	tickStatus                                        func() schedule.Tick                 // the tick as the service manager holds it; nil asks launchctl or systemctl
	editor                                            func(file string, line int) []string // the editor argv; nil uses $VISUAL or $EDITOR
}

type dataEvent struct {
	data Data
	err  error
}
type changesEvent struct {
	id, revision string
	set          ChangeSet
	err          error
	req          uint64
}
type filesEvent struct {
	id, source string
	rows       []Change
	err        error
	req        uint64
}

// treeLoad is one in-flight read of the tree's list: which request, and since when.
type treeLoad struct {
	req     uint64
	pending bool
	quiet   bool // a background refresh of rows already shown
	since   time.Time
	err     string // why the last read for this task failed
}

type loadTickEvent struct{}
type resultEvent struct {
	message string
	err     error
}
type paintEvent struct{}
type prefixTimeout struct{}
type pollEvent struct{}
type guestPollEvent struct{}
type freshEvent struct {
	task  Task
	ready bool
}

func newModel(opts Options, glyphs ui.GlyphTier) *dashModel {
	m := &dashModel{Options: opts, width: 80, height: 24, status: "Loading workspace", metadata: make(map[string]taskMetadata), glyphs: glyphs}
	if opts.StartupError != nil {
		m.fail(opts.StartupError)
	}
	return m
}

func (m *dashModel) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

func (m *dashModel) say(s string) { m.status, m.alert = s, false }

func (m *dashModel) fail(err error) { m.status, m.alert = err.Error(), true }

func Run(ctx context.Context, opts Options) (bool, error) {
	model := newModel(opts, ui.GlyphTierFrom(os.Getenv))
	tty := saveTTY(os.Stdin)
	final, err := tea.NewProgram(model, tea.WithContext(ctx), tea.WithoutSignalHandler()).Run()
	if model.child != nil {
		model.child.Close()
	}
	tty.restore()
	if ctx.Err() != nil {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return final.(*dashModel).exit, nil
}

func (m *dashModel) Init() tea.Cmd {
	return tea.Batch(m.readData(), nextGuestPoll(), m.readReviews(), nextReviewPoll())
}

func (m *dashModel) readData() tea.Cmd {
	home, state, cwd := m.Home, m.State, m.CWD
	return func() tea.Msg {
		d, err := Read(home, state, cwd)
		return dataEvent{d, err}
	}
}

func repaint() tea.Cmd {
	return tea.Tick(70*time.Millisecond, func(time.Time) tea.Msg { return paintEvent{} })
}
func nextPoll() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return pollEvent{} })
}

func nextGuestPoll() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return guestPollEvent{} })
}

func (m *dashModel) task() Task {
	if m.selected < len(m.data.Tasks) {
		return m.withGuestWork(m.data.Tasks[m.selected])
	}
	return Task{}
}

// withGuestWork points t at the checkout its agent works in, as the last sample found it.
func (m *dashModel) withGuestWork(t Task) Task {
	s, ok := m.samples[t.ID]
	if !ok || len(s.work) == 0 || s.sandbox != t.Sandbox || s.vmid != t.VMID {
		return t
	}
	t.Workdir, t.Branches = s.work[0].path, branches(s.work)
	return t
}

func (m *dashModel) taskOrder() []int {
	if m.view == 1 && len(m.graphOrder) == len(m.data.Tasks) {
		return slices.Clone(m.graphOrder)
	}
	var order []int
	for i, t := range m.data.Tasks {
		if m.view != 0 || t.State != schedule.Complete {
			order = append(order, i)
		}
	}
	if m.view != 1 {
		slices.SortStableFunc(order, func(a, b int) int {
			ta, tb := m.data.Tasks[a], m.data.Tasks[b]
			if g := m.groupOf(ta) - m.groupOf(tb); g != 0 {
				return g
			}
			return tb.Updated.Compare(ta.Updated)
		})
	}
	return order
}

func taskGroup(state schedule.QueueState) int {
	switch state {
	case reviewState:
		return 0
	case schedule.Attention:
		return 1
	case schedule.Reserved, schedule.Running, schedule.Settling:
		return 2
	case schedule.Complete:
		return 4
	default:
		return 3
	}
}

// groupLabels name the queue's groups in order, Review requests on top.
func (m *dashModel) groupLabels() []string {
	return []string{"Review requests", "Attention required", "Working", "Queued", "Completed"}
}

func (m *dashModel) selectVisible() {
	order := m.taskOrder()
	if !slices.Contains(order, m.selected) {
		m.closeDetails()
		m.selected = len(m.data.Tasks)
		if len(order) > 0 {
			m.selected = order[0]
		}
	}
}

func (m *dashModel) closeDetails() { m.details = false }

func (m *dashModel) receiveData(d Data) tea.Cmd {
	id := m.task().ID
	m.data = d
	m.data.Tasks = m.withGhosts(d.Tasks)
	i := slices.IndexFunc(m.data.Tasks, func(t Task) bool { return t.ID == id })
	switch {
	case i >= 0:
		m.selected = i
	case id == "":
		m.selected = len(m.data.Tasks)
	default:
		m.closeDetails()
		m.selected = max(0, min(m.selected, len(m.data.Tasks)-1))
	}
	m.selectVisible()
	if m.status == "Loading workspace" {
		m.say("")
	}
	drop := m.dropFailedReviews()
	t := m.task()
	if t.ID != id {
		return tea.Batch(m.readChanges(t), m.readFiles(t), m.readGuest(t), m.nextMetadata(), nextPoll(), drop)
	}
	m.quietReads = true
	defer func() { m.quietReads = false }()
	return tea.Batch(m.readChanges(t), m.readFiles(t), m.nextMetadata(), nextPoll(), drop)
}

func (m *dashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch event := msg.(type) {
	case dataEvent:
		if event.err != nil {
			m.fail(event.err)
			return m, nextPoll()
		}
		return m, m.receiveData(event.data)
	case pollEvent:
		if m.view == scheduleView {
			return m, tea.Batch(m.readData(), m.readSchedule())
		}
		return m, m.readData()
	case scheduleEvent:
		m.receiveSchedule(event.data)
		return m, nil
	case editedEvent:
		return m, m.receiveEdit(event)
	case pausedEvent:
		return m, m.receivePaused(event)
	case guestPollEvent:
		return m, tea.Batch(m.nextSample(), nextGuestPoll())
	case freshEvent:
		m.freshPending = false
		current := m.task()
		if !m.sbxTab && event.ready && current.ID == event.task.ID && current.VMID == event.task.VMID {
			t := event.task
			return m, m.handoffTo("Opening Fresh", t.Sandbox+" · "+t.Workdir, "", "Back from Fresh",
				"sbx", "exec", "-i", "-t", "-u", "agent", "-w", t.Workdir, t.Sandbox, "--", "fresh", ".")
		}
		return m, nil
	case guestSample:
		m.guestPending = false
		if m.samples == nil {
			m.samples = map[string]guestSample{}
		}
		if event.available {
			m.samples[event.id] = event
		} else {
			delete(m.samples, event.id)
		}
		if t := m.task(); event.id == t.ID && event.sandbox == t.Sandbox && event.vmid == t.VMID {
			if event.available || m.guest.vmid != event.vmid {
				m.guest = event
			} else {
				m.guest.fresh = false
			}
		}
		return m, nil
	case reviewsEvent:
		if event.err == nil {
			m.reviews = event.reviews
			m.regroup()
		}
		return m, nil
	case reviewPollEvent:
		return m, tea.Batch(m.readReviews(), nextReviewPoll())
	case verdictEvent:
		if event.err != nil {
			m.fail(fmt.Errorf("review of #%d: %w", event.number, event.err))
			return m, nil
		}
		m.say(fmt.Sprintf("%s #%d", verdictDone[event.choice], event.number))
		for key, meta := range m.metadata {
			if slices.ContainsFunc(meta.prs, func(p prInfo) bool { return p.Number == event.number }) {
				delete(m.metadata, key)
			}
		}
		return m, tea.Batch(m.readReviews(), m.nextMetadata())
	case portsEvent:
		if m.confirm != nil && m.portsFor == event.id {
			if i := slices.IndexFunc(m.data.Tasks, func(t Task) bool { return t.ID == event.id }); i >= 0 {
				m.confirm = m.portsNotice(m.data.Tasks[i], event.labels, event.live)
			}
		}
		return m, nil
	case endedEvent:
		delete(m.ending, event.id)
		if event.err != nil {
			m.fail(fmt.Errorf("%s: %w", event.verb, event.err))
			return m, m.readData()
		}
		switch event.verb {
		case "retry":
			m.say("Started again; it waits in the queue for a slot")
		case "run":
			m.say("Started " + safeText(event.id) + "; it waits in the queue for a slot")
			return m, tea.Batch(m.readData(), m.readSchedule())
		default:
			m.say("Cleaned up")
		}
		return m, tea.Batch(m.readData(), m.readReviews())
	case reviewDroppedEvent:
		delete(m.dropping, event.id)
		if event.err != nil {
			if m.dropFailed == nil {
				m.dropFailed = map[string]bool{}
			}
			m.dropFailed[event.id] = true
			m.fail(fmt.Errorf("clean-up of the failed review: %w", event.err))
			return m, m.readData()
		}
		m.say("The failed review was cleaned up; its pull request is back in Review requests")
		return m, m.readData()
	case reviewQueuedEvent:
		if event.err != nil {
			delete(m.reviewStarting, event.url)
			m.fail(fmt.Errorf("review of #%d: %w", event.number, event.err))
			return m, nil
		}
		m.say(fmt.Sprintf("Review of #%d queued as %s", event.number, event.name))
		return m, m.readData()
	case loadTickEvent:
		m.loadTicking = false
		return m, m.loadTick()
	case filesEvent:
		if event.req == m.filesLoad.req {
			m.filesLoad.pending, m.filesLoad.err = false, errText(event.err)
		}
		if event.id == m.task().ID {
			m.taskFiles, m.filesSource = event.rows, event.source
		}
		return m, nil
	case changesEvent:
		if event.req == m.changesLoad.req {
			m.changesLoad.pending, m.changesLoad.err = false, errText(event.err)
		}
		if event.id == m.task().ID && event.revision == m.task().Tip {
			m.changes = event.set.Rows
			m.changesBase, m.changesMB, m.changesLive = event.set.Base, event.set.MergeBase, event.set.Live
		}
		return m, nil
	case taskMetadata:
		m.metadataPending = false
		m.metadata[event.key] = event
		if t := m.task(); event.id == t.ID && event.pr.Base != "" && m.changesBase != event.pr.Base && m.changesBase != "origin/"+event.pr.Base {
			return m, tea.Batch(m.readChanges(t), m.nextMetadata())
		}
		return m, m.nextMetadata()
	case tasksEvent:
		m.receiveTasks(event)
		return m, nil
	case resultEvent:
		if m.promptTemp != "" {
			_ = os.Remove(m.promptTemp)
			m.promptTemp = ""
		}
		m.say(event.message)
		if event.err != nil {
			m.fail(event.err)
		}
		return m, nil
	case previewEvent:
		if !m.preview || event.request != m.previewRequest {
			return m, nil
		}
		m.previewDoc = event.doc
		if m.previewLine > 0 && m.previewLineFor == event.request {
			m.previewDoc.mark, m.previewScroll = m.previewLine, max(0, m.previewLine-4)
		}
		m.previewLine = 0
		return m, m.sketch(event.request, event.doc)
	case sketchEvent:
		if m.preview && event.request == m.previewRequest && m.previewDoc.path == event.path {
			m.previewDoc.sketch, m.previewDoc.sketchNote = event.rows, event.note
		}
		return m, nil
	case externalEvent:
		if event.err != nil {
			m.fail(fmt.Errorf("open %s: %w", event.label, event.err))
		} else {
			m.say("Opened " + event.label + " in the system viewer")
		}
		return m, nil
	case paintEvent:
		if m.child != nil {
			if why, done := m.child.Status(); done {
				m.say(why)
				m.sbxTab, m.child, m.childBlankAt, m.childPainted = false, nil, time.Time{}, false
			}
		}
		if m.child != nil {
			if m.sbxSplit == 0 {
				m.readSbxSplit()
			}
			return m, repaint()
		}
		return m, nil
	case prefixTimeout:
		if m.prefix {
			m.prefix = false
			m.forwardKey(tea.KeyPressMsg(tea.Key{Code: 'g', Mod: tea.ModCtrl}))
		}
		return m, nil
	case tea.WindowSizeMsg:
		if event.Width != m.width || event.Height != m.height {
			m.sbxSplit = 0
		}
		m.width, m.height = max(1, event.Width), max(1, event.Height)
		if m.child != nil {
			m.child.Resize(m.width, max(1, m.height-1))
		}
		if m.confirm != nil {
			m.confirm.Update(event)
		}
		if m.helpModal != nil {
			m.helpModal = choiceui.NewHelp("Keys", helpSections(), m.width, m.height)
		}
		if m.reviewModal != nil {
			m.reviewModal.Update(event)
		}
		if m.taskPicker != nil {
			m.taskPicker.Update(event)
		}
		if m.verdictModal != nil {
			m.verdictModal.Update(event)
		}
		if m.exitConfirm != nil {
			m.exitConfirm.Update(event)
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.key(event)
	case tea.PasteMsg:
		if m.taskPicker != nil {
			m.taskPicker.Update(event)
			return m, nil
		}
		if m.verdictModal != nil {
			m.verdictModal.Update(event)
			return m, nil
		}
		if m.reviewModal != nil {
			m.reviewModal.Update(event)
			return m, nil
		}
		if m.sbxTab && m.child != nil {
			m.child.Input(event)
		}
	case tea.MouseWheelMsg:
		if m.sbxTab && m.child != nil {
			m.child.Input(event)
			return m, nil
		}
		if !m.sbxTab && m.noModal() {
			switch mouse := event.Mouse(); mouse.Button {
			case tea.MouseWheelUp:
				return m, m.wheel(mouse.X, mouse.Y, -1)
			case tea.MouseWheelDown:
				return m, m.wheel(mouse.X, mouse.Y, 1)
			}
		}
	case tea.MouseClickMsg, tea.MouseReleaseMsg, tea.MouseMotionMsg:
		if m.sbxTab && m.child != nil {
			m.child.Input(event)
			return m, nil
		}
		if !m.sbxTab && m.noModal() {
			return m, m.mouse(event)
		}
	case copiedEvent:
		if event.err != nil {
			m.fail(fmt.Errorf("copy %s: %w", event.what, event.err))
			return m, nil
		}
		m.say("Copied " + event.what + " to the clipboard")
		return m, m.showTip("Copied to clipboard", event.at)
	case tipDoneEvent:
		if event.id == m.tip.id {
			m.tip = tooltip{}
		}
		return m, nil
	}
	return m, nil
}

func (m *dashModel) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.pointer = point{}
	if m.exitConfirm != nil {
		if done, exit := m.exitConfirm.Update(msg); done {
			m.exitConfirm = nil
			if exit {
				m.exit = true
				return m, tea.Quit
			}
		}
		return m, nil
	}
	k := msg.String()
	if k == "ctrl+c" {
		m.prefix = false
		if !m.sbxTab {
			m.exitConfirm = choiceui.NewDashboardExitConfirm(m.width, m.height)
			return m, nil
		}
		m.exit = true
		return m, tea.Quit
	}
	if m.prefix {
		m.prefix = false
		if k == "tab" {
			m.sbxTab = !m.sbxTab
			if !m.sbxTab && m.child != nil {
				m.readSbxSplit()
			}
			if m.sbxTab && m.child == nil {
				child, err := startChild(m.width, max(1, m.height-1))
				if err != nil {
					m.fail(fmt.Errorf("sbx unavailable · %w", err))
				}
				m.child = child
				m.childBlankAt, m.childPainted = time.Time{}, false
				if child != nil {
					return m, repaint()
				}
			}
			return m, nil
		}
		m.forwardKey(tea.KeyPressMsg(tea.Key{Code: 'g', Mod: tea.ModCtrl}))
	}
	if k == "ctrl+g" {
		m.prefix = true
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return prefixTimeout{} })
	}
	if m.sbxTab {
		m.forwardKey(msg)
		return m, nil
	}
	if m.confirm != nil {
		done, attach := m.confirm.Update(msg)
		if !done {
			return m, nil
		}
		m.confirm = nil
		m.portsFor = ""
		then, decline := m.onConfirm, m.onDecline
		m.onConfirm, m.onDecline = nil, nil
		if !attach && decline != nil {
			decline()
		}
		if then != nil {
			if attach {
				return m, then()
			}
			return m, nil
		}
		if i := slices.IndexFunc(m.data.Tasks, func(t Task) bool { return (t.ID == m.attachID || t.Session == m.attachID) && t.Live }); attach && i >= 0 {
			t := m.data.Tasks[i]
			title := safeText(m.titleFor(t))
			return m, m.handoffTo("Attaching to "+title, strings.Join(nonEmpty(t.Sandbox, agentLabel(t.Agent)), " · "),
				hint("Ctrl+B d", "detaches back here"), "Back from "+title, m.Binary, "attach", m.attachID)
		}
		return m, nil
	}
	if m.verdictModal != nil {
		if done, submit := m.verdictModal.Update(msg); done {
			choice, body := m.verdictModal.Result()
			m.verdictModal = nil
			if submit {
				m.say("Submitting the review…")
				return m, m.submitVerdict(m.verdictPR, choice, body)
			}
		}
		return m, nil
	}
	if m.taskPicker != nil {
		if done, start := m.taskPicker.Update(msg); done {
			task, prompt, branch := m.taskPicker.Result()
			ticket := m.taskPicker.Ticket()
			m.taskPicker = nil
			if start {
				return m, m.startInstance(task, prompt, branch, ticket)
			}
		}
		return m, nil
	}
	if m.reviewModal != nil {
		if done, start := m.reviewModal.Update(msg); done {
			template, notes := m.reviewModal.Result()
			m.reviewModal = nil
			if start {
				return m, m.startReview(m.reviewFor, template, notes)
			}
		}
		return m, nil
	}
	if m.pauseChoice != nil {
		if m.pauseChoice.Update(msg) {
			answer := m.pauseChoice.Answer()
			m.pauseChoice = nil
			return m, m.pauseAnswer(answer)
		}
		return m, nil
	}
	if m.helpModal != nil {
		if done, _ := m.helpModal.Update(msg); done || k == "?" {
			m.helpModal = nil
		}
		return m, nil
	}
	if m.focus == 1 && m.tree != nil && m.tree.editing {
		return m, m.filterKey(msg)
	}
	if m.report != nil && !m.preview && m.focus == 2 {
		if cmd, ok := m.reportKey(k); ok {
			return m, cmd
		}
	}
	if m.preview && m.focus == 2 {
		if cmd, ok := m.readingKey(k); ok {
			return m, cmd
		}
	}
	if m.inSchedule() {
		if cmd, ok := m.scheduleKey(k); ok {
			return m, cmd
		}
	}
	if m.focus == 1 && (k == "n" || k == "p") {
		return m, m.treeOpen(k)
	}
	switch k {
	case "n":
		return m, m.openTaskPicker()
	case "1", "2", "3":
		m.view = int(k[0] - '1')
		m.selectVisible()
		if m.view == scheduleView {
			m.focus, m.preview, m.report, m.details = 0, false, nil, false
			return m, m.readSchedule()
		}
		return m, tea.Batch(m.readChanges(m.task()), m.readFiles(m.task()), m.readGuest(m.task()), m.readMetadata(m.task()))
	case "f", "g":
		m.showFiles, m.focus, m.preview, m.tree, m.details = k == "f", 1, false, nil, false
		return m, m.readFiles(m.task())
	case "/":
		m.focus, m.preview = 1, false
		m.fileTree().editing = true
	case "left", "right", "-", "=", "`":
		switch {
		case m.focus == 1:
			m.treeKey(k)
		case k == "left" || k == "right":
			return m, m.move(k)
		}
	case "t":
		if m.preview || m.task().ID == "" {
			return m, nil
		}
		if m.details {
			m.closeDetails()
			return m, nil
		}
		m.details = true
		return m, tea.Batch(m.readGuest(m.task()), m.readMetadata(m.task()))
	case "c":
		msg := "Disk reservation per sandbox " + m.data.Capacity.Reservation
		if m.data.CapacityError != "" {
			msg += " · " + m.data.CapacityError
		}
		m.say(msg)
	case "tab", "shift+tab":
		m.details = false
		switch {
		case m.focus != 1:
			m.focus = 1
		case m.preview || m.report != nil:
			m.focus = 2
		default:
			m.focus = 0
		}
	case "up", "down", "pgup", "pgdown":
		return m, m.move(k)
	case " ", "space":
		m.graphExpanded = !m.graphExpanded
	case "enter":
		return m, m.enter()
	case "esc":
		switch {
		case m.details:
			m.closeDetails()
		case m.preview && m.report != nil:
			m.preview, m.focus = false, 2
		case m.preview:
			m.preview, m.focus = false, 1
		case m.focus == 1 && m.tree != nil && m.tree.filter != "":
			m.tree.filter = ""
		case m.focus != 0:
			m.focus = 0
		}
	case "p":
		if m.focus == 0 && !m.preview {
			return m, m.openPorts(m.task())
		}
	case "s":
		if t := m.task(); m.focus == 0 && !m.preview && m.endedRun(t) && m.canFollowUp(t) {
			m.openFollowUp(t)
		}
	case "r":
		if m.focus == 0 && !m.preview {
			return m, m.retry(m.task())
		}
	case "x":
		if m.focus == 0 && !m.preview {
			m.openCleanUp(m.task())
		}
	case "v":
		m.openVerdict()
	case "a":
		if r, ok := m.runningReview(); ok {
			m.attachTask(r)
		}
	case "u", "i":
		if m.preview {
			m.patch, m.split, m.previewScroll = true, k == "i", 0
		}
	case "[", "]":
		if m.focus == 1 || m.preview {
			delta := 1
			if k == "[" {
				delta = -1
			}
			if n := m.fileTree().step(delta); n != nil {
				m.preview, m.previewScroll, m.focus, m.details = true, 0, 2, false
				return m, m.openFile()
			}
		}
	case "e":
		if t := m.task(); !m.freshPending && t.Guest && m.guest.fresh && m.guest.id == t.ID && m.guest.sandbox == t.Sandbox && m.guest.vmid == t.VMID {
			m.freshPending = true
			return m, func() tea.Msg { return freshEvent{t, verifiedGuest(t)} }
		}
	case "?":
		m.helpModal = choiceui.NewHelp("Keys", helpSections(), m.width, m.height)
	}
	return m, nil
}

func (m *dashModel) readSbxSplit() {
	frame, _ := m.child.Frame()
	if split := SbxDivider(frame, m.width); split > 0 {
		m.sbxSplit = split
	}
}

func (m *dashModel) forwardKey(msg tea.KeyPressMsg) {
	if m.sbxTab && m.child != nil {
		m.child.Input(msg)
	}
}

func foreground(binary string, args ...string) tea.Cmd {
	return tea.ExecProcess(exec.Command(binary, args...), func(err error) tea.Msg { return resultEvent{err: err} })
}

func (m *dashModel) readChanges(t Task) tea.Cmd {
	if n, ok := reviewPR(t); ok {
		if m.quietReads && m.changesLoad.pending {
			return nil
		}
		req, cwd := m.changesLoad.start(m.now(), m.quietReads), m.CWD
		return tea.Batch(func() tea.Msg {
			h, err := fetchPR(cwd, n)
			if err != nil {
				if r := t.Review; r != nil {
					rows := make([]Change, 0, len(r.Files))
					for _, p := range r.Files {
						rows = append(rows, Change{Kind: "M", Path: p})
					}
					return changesEvent{t.ID, t.Tip, ChangeSet{Rows: rows, Base: "origin/" + r.Base}, nil, req}
				}
				return changesEvent{t.ID, t.Tip, ChangeSet{}, err, req}
			}
			base := h.base
			if base == "" && t.Review != nil {
				base = t.Review.Base
			}
			set, err := Changes(prView(t, h), base, h.repo)
			return changesEvent{t.ID, t.Tip, set, err, req}
		}, m.loadTick())
	}
	prBase, cwd := m.metadata[metadataKey(t)].pr.Base, m.CWD
	if m.quietReads && m.changesLoad.pending {
		return nil
	}
	req := m.changesLoad.start(m.now(), m.quietReads)
	return tea.Batch(func() tea.Msg {
		set, err := Changes(t, prBase, cwd)
		return changesEvent{t.ID, t.Tip, set, err, req}
	}, m.loadTick())
}

func (m *dashModel) files() []Change {
	if m.showFiles {
		return m.taskFiles
	}
	return m.changes
}

// fileTree is the pane's tree for the current list; state survives a rebuild of the same list.
func (m *dashModel) fileTree() *fileTree {
	files := m.files()
	key := "changes\x00" + m.task().ID
	if m.showFiles {
		key = "files\x00" + m.task().ID
	}
	t := m.tree
	if t == nil || t.key != key {
		t = &fileTree{key: key, open: map[string]bool{}}
		m.tree = t
	}
	if t.root == nil || !slices.Equal(t.src, files) {
		t.src, t.root = files, buildFileTree(files)
		if len(t.open) == 0 {
			t.decided = false
		}
	}
	return t
}

func (m *dashModel) treeKey(k string) {
	t := m.fileTree()
	switch k {
	case "left":
		t.fold()
	case "right":
		t.unfold()
	case "-":
		t.setAll(false)
	case "=":
		t.setAll(true)
	case "`":
		t.flat = !t.flat
	}
}

func (m *dashModel) filterKey(msg tea.KeyPressMsg) tea.Cmd {
	t := m.fileTree()
	switch k := msg.String(); k {
	case "esc":
		t.filter, t.editing = "", false
	case "enter":
		t.editing = false
	case "backspace":
		if r := []rune(t.filter); len(r) > 0 {
			t.filter = string(r[:len(r)-1])
		}
	case "up", "down", "pgup", "pgdown":
		return m.move(k)
	default:
		if msg.Text == "" || msg.Mod&(tea.ModCtrl|tea.ModAlt) != 0 {
			return nil
		}
		t.filter += msg.Text
	}
	t.refilter()
	return nil
}

func (m *dashModel) move(key string) tea.Cmd {
	delta := 1
	if key == "up" || key == "pgup" || key == "left" {
		delta = -1
	}
	if strings.HasPrefix(key, "pg") {
		delta *= max(1, m.height/3)
	}
	switch {
	case m.preview && m.focus == 2:
		m.previewScroll = max(0, m.previewScroll+delta)
	case m.focus == 1:
		m.fileTree().move(delta)
	default:
		order := m.taskOrder()
		if len(order) == 0 {
			return nil
		}
		m.selected = m.neighbor(key, delta, order)
		m.preview = false
		m.changes, m.taskFiles, m.filesSource = nil, nil, ""
		m.changesLoad.err, m.filesLoad.err = "", ""
		t := m.task()
		return tea.Batch(m.readChanges(t), m.readFiles(t), m.readGuest(t), m.readMetadata(t))
	}
	return nil
}

func (m *dashModel) enter() tea.Cmd {
	if m.focus == 1 {
		t := m.fileTree()
		switch n := t.selected(); {
		case n == nil:
		case n.dir:
			t.toggle(n)
		default:
			m.preview, m.patch, m.previewScroll, m.focus = true, false, 0, 2
			return m.openFile()
		}
		return nil
	}
	if t := m.task(); t.Review != nil {
		m.openReview(t)
		return nil
	}
	switch t := m.task(); {
	case t.Live:
		m.attachTask(t)
	case m.endedRun(t) && t.Report != "":
		m.openReport(t)
	case m.endedRun(t):
		m.openFollowUp(t)
	case t.ID != "" && m.groupOf(t) == 1:
		m.confirm = m.followUpNotice(t)
	}
	return nil
}

type previewEvent struct {
	request uint64
	doc     previewDoc
}

func (m *dashModel) openFile() tea.Cmd {
	n := m.fileTree().selected()
	if n == nil || n.dir {
		return nil
	}
	t, file, mb, cwd := m.task(), n.change, m.changesMB, m.CWD
	m.previewPath, m.previewDoc = file.Path, previewDoc{path: file.Path, note: "reading…"}
	m.previewRequest++
	request := m.previewRequest
	return func() tea.Msg {
		if n, ok := reviewPR(t); ok {
			if h, err := fetchPR(cwd, n); err == nil {
				t = prView(t, h)
			}
		}
		sides, err := ReadSides(t, file, mb)
		if err != nil {
			return previewEvent{request, previewDoc{path: file.Path, note: safeText(err.Error())}}
		}
		return previewEvent{request, newPreviewDoc(file.Path, sides)}
	}
}

// readFiles lists the selected task's sandbox files while the Files view is up.
func (m *dashModel) readFiles(t Task) tea.Cmd {
	if !m.showFiles || t.ID == "" {
		return nil
	}
	if m.quietReads && m.filesLoad.pending {
		return nil
	}
	req, cwd := m.filesLoad.start(m.now(), m.quietReads), m.CWD
	return tea.Batch(func() tea.Msg {
		if n, ok := reviewPR(t); ok {
			h, err := fetchPR(cwd, n)
			if err != nil {
				return filesEvent{t.ID, "", nil, err, req}
			}
			rows, _, err := TaskFiles(prView(t, h))
			return filesEvent{t.ID, fmt.Sprintf("PR #%d head %s", n, short(h.sha)), rows, err, req}
		}
		rows, source, err := TaskFiles(t)
		return filesEvent{t.ID, source, rows, err, req}
	}, m.loadTick())
}

// neighbor is the task a move lands on: left and right step through the order, up and down
// take the nearest card in the row above or below, and the graph moves line by line.
func (m *dashModel) neighbor(key string, delta int, order []int) int {
	at := slices.Index(order, m.selected)
	linear := order[max(0, min(at+delta, len(order)-1))]
	from, ok := m.cardPos[m.selected]
	if !ok || key == "left" || key == "right" {
		return linear
	}
	steps := 1
	if strings.HasPrefix(key, "pg") {
		steps = max(1, m.height/(2*m.cardHeight()))
	}
	sel := m.selected
	for range steps {
		best, bestLine, bestCol := -1, 0, 0
		for _, i := range order {
			p, ok := m.cardPos[i]
			if !ok || (delta > 0 && p.line <= from.line) || (delta < 0 && p.line >= from.line) {
				continue
			}
			gap, col := abs(p.line-from.line), abs(p.col-from.col)
			if best < 0 || gap < bestLine || (gap == bestLine && col < bestCol) {
				best, bestLine, bestCol = i, gap, col
			}
		}
		if best < 0 {
			break
		}
		sel, from = best, m.cardPos[best]
	}
	return sel
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// nextMetadata reads the selected task's PR and ticket, else the next task whose copy is stale; one at a time.
func (m *dashModel) nextMetadata() tea.Cmd {
	if cmd := m.readMetadata(m.task()); cmd != nil {
		return cmd
	}
	for _, i := range m.taskOrder() {
		if cmd := m.readMetadata(m.withGuestWork(m.data.Tasks[i])); cmd != nil {
			return cmd
		}
	}
	return nil
}

// nextSample probes the selected task's sandbox when its sample is old, else the next running sandbox in turn.
func (m *dashModel) nextSample() tea.Cmd {
	if m.guestPending {
		return nil
	}
	t := m.task()
	if s, ok := m.samples[t.ID]; t.Guest && (!ok || time.Since(s.at) > 5*time.Second) {
		return m.readGuest(t)
	}
	order := m.taskOrder()
	for range order {
		m.sampleAt = (m.sampleAt + 1) % max(1, len(order))
		other := m.data.Tasks[order[m.sampleAt]]
		if s, ok := m.samples[other.ID]; other.Guest && (!ok || time.Since(s.at) > 15*time.Second) {
			return m.readGuest(other)
		}
	}
	return nil
}

// nonEmpty keeps the parts that say something.
func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p = safeText(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// start begins a read and returns its request; the pending clock keeps running across back-to-back reads.
func (l *treeLoad) start(now time.Time, quiet bool) uint64 {
	l.req++
	if !l.pending {
		l.since, l.quiet = now, quiet
	} else if !quiet {
		l.quiet = false
	}
	l.pending = true
	return l.req
}

// loadTick animates the tree's loading card while a read is in flight.
func (m *dashModel) loadTick() tea.Cmd {
	if m.loadTicking || (!m.changesLoad.pending && !m.filesLoad.pending) {
		return nil
	}
	m.loadTicking = true
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return loadTickEvent{} })
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return safeText(err.Error())
}

// readingKey is a key while a file has focus: n and p step to the next and previous file, d toggles the diff.
func (m *dashModel) readingKey(k string) (tea.Cmd, bool) {
	switch k {
	case "n", "p", "]", "[":
		delta := 1
		if k == "p" || k == "[" {
			delta = -1
		}
		if n := m.fileTree().step(delta); n != nil {
			m.previewScroll, m.details = 0, false
			return m.openFile(), true
		}
		return nil, true
	case "d":
		m.patch, m.previewScroll = !m.patch, 0
		return nil, true
	case "s":
		if isDiagram(m.previewPath) {
			m.diagramSource, m.previewScroll = !m.diagramSource, 0
			return nil, true
		}
	case "o":
		if isDiagram(m.previewPath) {
			return m.openDiagram(), true
		}
	}
	return nil, false
}

// helpSections are the dashboard's keys as the ? modal lists them.
func helpSections() []choiceui.HelpSection {
	return []choiceui.HelpSection{
		{Title: "Tasks", Keys: [][2]string{
			{"↑↓ wheel", "move through tasks"}, {"1 2 3", "dashboard, graph, schedule"}, {"↵", "attach, or read a run's report"}, {"s r x", "follow up · retry · clean up"},
			{"t", "task details"}, {"click link", "copy it"}, {"p", "sbx ports of a sandbox"}, {"v", "verdict on its PR"}, {"n", "new instance"}, {"c", "host capacity"}, {"3: e n p r", "edit · add · pause · run a job"},
		}},
		{Title: "Files", Keys: [][2]string{
			{"f g", "files · changes"}, {"Tab", "tasks or files"}, {"↵", "open file or fold"},
			{"n p", "open next / previous file"}, {"/", "filter files"}, {"← → - =", "fold, collapse, expand"}, {"`", "tree or flat list"},
		}},
		{Title: "Reading a file or report", Keys: [][2]string{
			{"n p", "next · previous file or reference"}, {"↵", "open the reference under ›"}, {"o", "open as SVG or in the viewer"},
			{"s", "diagram source · follow up"}, {"v y Y", "select rows · copy · copy all"}, {"a", "attach the agentic review"}, {"d", "file or diff"}, {"u i", "unified · side by side"}, {"↑↓", "scroll"},
		}},
		{Title: "Anywhere", Keys: [][2]string{
			{"e", "Fresh in the sandbox"}, {"Ctrl+G Tab", "proveo or sbx"}, {"Esc", "back"}, {"Ctrl+C", "quit"},
		}},
	}
}

// followUpNotice says why Enter cannot attach to an attention task: its agent's session is live in another terminal, or the run ended.
func (m *dashModel) followUpNotice(t Task) *choiceui.Confirmation {
	agent := agentLabel(t.Agent)
	if st := m.statusOf(t); st.label != "" {
		agent += " · " + strings.ToLower(st.label[:1]) + st.label[1:]
	}
	where := []string{}
	if t.Sandbox != "" {
		where = append(where, "Sandbox  "+safeText(t.Sandbox))
	}
	where = append(where, "Agent    "+safeText(agent))
	if t.Guest {
		return choiceui.NewNotice("Follow up in its own terminal", append([]string{
			"proveo can't attach: this agent's session is live in another terminal.",
			"Follow up in the terminal tab where `proveo run` started it.", "",
		}, where...), m.width, m.height)
	}
	return choiceui.NewNotice("Nothing to attach to", append([]string{
		"This run has ended, so no session is left to follow up in.",
		"t shows its details; n starts a new instance.", "",
	}, where...), m.width, m.height)
}

// treeOpen is n or p from the tree: with no file open, the file under the cursor, else the first file after it;
// with a file open, the next or previous one.
func (m *dashModel) treeOpen(k string) tea.Cmd {
	t := m.fileTree()
	if n := t.selected(); !m.preview && n != nil && !n.dir {
		m.preview, m.previewScroll, m.focus, m.details = true, 0, 2, false
		return m.openFile()
	}
	delta := 1
	if k == "p" {
		delta = -1
	}
	if t.step(delta) == nil {
		return nil
	}
	m.preview, m.previewScroll, m.focus, m.details = true, 0, 2, false
	return m.openFile()
}

// attachTask asks before handing the terminal to t's live session: its own tmux session, or its job's.
func (m *dashModel) attachTask(t Task) {
	if !t.Live {
		return
	}
	m.attachID = t.ID
	if t.Session != "" {
		m.attachID = t.Session
	}
	m.confirm = choiceui.NewAttachConfirm(safeText(t.Agent), safeText(t.Title), m.width, m.height)
}
