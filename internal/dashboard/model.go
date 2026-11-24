// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package dashboard

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/proveo-ca/proveo/internal/choiceui"
	"github.com/proveo-ca/proveo/internal/schedule"
)

type Options struct {
	CWD, Home, State, Binary string
	Targets                  []string
	Launch                   func(target, task, ticket, prompt string) (string, error)
	StartupError             error
}

type dashModel struct {
	Options
	data                                                                      Data
	width, height                                                             int
	view, focus, selected, file, detailScroll, previewScroll, detailPlacement int
	showFiles, details, preview, patch, graphExpanded, help                   bool
	exit, sbxTab, prefix                                                      bool
	child                                                                     childTerminal
	status                                                                    string
	confirm                                                                   *choiceui.AttachConfirm
	attachID                                                                  string
	changes                                                                   []Change
	previewText                                                               string
	previewPath                                                               string
	previewRequest                                                            uint64
	guest                                                                     guestSample
	guestPending                                                              bool
	freshPending                                                              bool
	metadataPending                                                           bool
	metadata                                                                  map[string]taskMetadata
	wizard                                                                    *newWizard
}

type dataEvent struct {
	data Data
	err  error
}
type changesEvent struct {
	id, revision string
	rows         []Change
	err          error
}
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

func Run(ctx context.Context, opts Options) (bool, error) {
	model := &dashModel{Options: opts, width: 80, height: 24, details: true, status: "Loading workspace", metadata: make(map[string]taskMetadata)}
	if opts.StartupError != nil {
		model.status = opts.StartupError.Error()
	}
	final, err := tea.NewProgram(model, tea.WithContext(ctx), tea.WithoutSignalHandler()).Run()
	if model.child != nil {
		model.child.Close()
	}
	if ctx.Err() != nil {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return final.(*dashModel).exit, nil
}

func (m *dashModel) Init() tea.Cmd { return tea.Batch(m.readData(), nextGuestPoll()) }

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
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return guestPollEvent{} })
}

func (m *dashModel) task() Task {
	if m.selected < len(m.data.Tasks) {
		return m.data.Tasks[m.selected]
	}
	return Task{}
}

func (m *dashModel) taskOrder() []int {
	var order []int
	for i, t := range m.data.Tasks {
		if m.view != 0 || t.State != schedule.Complete {
			order = append(order, i)
		}
	}
	if m.view != 1 {
		slices.SortStableFunc(order, func(a, b int) int {
			return taskGroup(m.data.Tasks[a].State) - taskGroup(m.data.Tasks[b].State)
		})
	}
	return order
}

func taskGroup(state schedule.QueueState) int {
	switch state {
	case schedule.Attention:
		return 0
	case schedule.Reserved, schedule.Running, schedule.Settling:
		return 1
	case schedule.Complete:
		return 3
	default:
		return 2
	}
}

func (m *dashModel) selectVisible() {
	order := m.taskOrder()
	if !slices.Contains(order, m.selected) {
		m.selected = len(m.data.Tasks)
		if len(order) > 0 {
			m.selected = order[0]
		}
	}
}

func (m *dashModel) receiveData(d Data) tea.Cmd {
	id := m.task().ID
	m.data = d
	i := slices.IndexFunc(d.Tasks, func(t Task) bool { return t.ID == id })
	if i >= 0 {
		m.selected = i
	} else {
		m.selected = max(0, min(m.selected, len(d.Tasks)-1))
	}
	m.selectVisible()
	if m.status == "Loading workspace" {
		m.status = ""
	}
	t := m.task()
	if t.ID != id {
		return tea.Batch(m.readChanges(t), m.readGuest(t), m.readMetadata(t), nextPoll())
	}
	return tea.Batch(m.readChanges(t), m.readMetadata(t), nextPoll())
}

func (m *dashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch event := msg.(type) {
	case dataEvent:
		if event.err != nil {
			m.status = event.err.Error()
			return m, nextPoll()
		}
		return m, m.receiveData(event.data)
	case pollEvent:
		return m, m.readData()
	case guestPollEvent:
		return m, tea.Batch(m.readGuest(m.task()), nextGuestPoll())
	case freshEvent:
		m.freshPending = false
		current := m.task()
		if !m.sbxTab && event.ready && current.ID == event.task.ID && current.VMID == event.task.VMID {
			t := event.task
			return m, foreground("sbx", "exec", "-i", "-t", "-u", "agent", "-w", t.Workdir, t.Sandbox, "--", "fresh", ".")
		}
		return m, nil
	case guestSample:
		m.guestPending = false
		if event.id == m.task().ID && event.sandbox == m.task().Sandbox && event.vmid == m.task().VMID {
			if event.available || m.guest.vmid != event.vmid {
				m.guest = event
			} else {
				m.guest.fresh = false
			}
			return m, nil
		}
		return m, m.readGuest(m.task())
	case changesEvent:
		if event.id == m.task().ID && event.revision == m.task().Tip {
			m.changes = event.rows
			if event.err != nil {
				m.status = event.err.Error()
			}
		}
		return m, nil
	case taskMetadata:
		m.metadataPending = false
		m.metadata[event.key] = event
		if event.id != m.task().ID {
			return m, m.readMetadata(m.task())
		}
		return m, nil
	case resultEvent:
		m.status = event.message
		if event.err != nil {
			m.status = event.err.Error()
		}
		return m, nil
	case previewEvent:
		if !m.preview || event.request != m.previewRequest {
			return m, nil
		}
		m.previewText = event.text
		if event.err != nil {
			m.previewText = event.err.Error()
		}
		return m, nil
	case paintEvent:
		if m.child != nil {
			if why, done := m.child.Status(); done {
				m.status, m.sbxTab, m.child = why, false, nil
			}
		}
		if m.child != nil {
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
		m.width, m.height = max(1, event.Width), max(1, event.Height)
		if m.child != nil {
			m.child.Resize(m.width, max(1, m.height-1))
		}
		if m.confirm != nil {
			m.confirm.Update(event)
		}
		if m.wizard != nil {
			m.wizard.prompt.SetWidth(max(1, m.width-8))
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.key(event)
	case tea.PasteMsg:
		if m.sbxTab && m.child != nil {
			m.child.Input(event)
		} else if !m.sbxTab && m.wizard != nil {
			return m, m.wizard.edit(event)
		}
	case tea.MouseClickMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg, tea.MouseMotionMsg:
		if m.sbxTab && m.child != nil {
			m.child.Input(event)
		}
	}
	return m, nil
}

func (m *dashModel) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		m.exit = true
		return m, tea.Quit
	}
	if m.prefix {
		m.prefix = false
		if k == "tab" {
			m.sbxTab = !m.sbxTab
			if m.sbxTab && m.child == nil {
				child, err := startChild(m.width, max(1, m.height-1))
				if err != nil {
					m.status = "sbx unavailable · " + err.Error()
				}
				m.child = child
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
		if attach && slices.ContainsFunc(m.data.Tasks, func(t Task) bool { return t.ID == m.attachID && t.Live }) {
			return m, foreground(m.Binary, "attach", m.attachID)
		}
		return m, nil
	}
	if m.wizard != nil {
		return m, m.wizardKey(msg)
	}
	if m.help {
		m.help = false
		return m, nil
	}
	switch k {
	case "n":
		m.wizard = startWizard(m.Targets, m.width)
	case "1", "2", "3":
		m.view = int(k[0] - '1')
		m.selectVisible()
		return m, tea.Batch(m.readChanges(m.task()), m.readGuest(m.task()), m.readMetadata(m.task()))
	case "4":
		m.detailPlacement = (m.detailPlacement + 1) % 3
		m.details = true
		m.status = []string{"Task details · lower-right split", "Task details · accordion", "Task details · left pane"}[m.detailPlacement]
	case "f", "g":
		m.showFiles, m.focus, m.file, m.preview = k == "f", 1, 0, false
	case "t":
		m.details = !m.details
		return m, tea.Batch(m.readGuest(m.task()), m.readMetadata(m.task()))
	case "c":
		m.status = "Disk reservation per sandbox " + m.data.Capacity.Reservation
		if m.data.CapacityError != "" {
			m.status += " · " + m.data.CapacityError
		}
	case "tab":
		m.focus = (m.focus + 1) % 3
	case "shift+tab":
		m.focus = (m.focus + 2) % 3
	case "up", "down", "pgup", "pgdown":
		return m, m.move(k)
	case " ", "space":
		m.graphExpanded = !m.graphExpanded
	case "enter":
		return m, m.enter()
	case "esc":
		m.preview, m.help = false, false
	case "p":
		if m.preview {
			m.patch = !m.patch
			return m, m.openFile()
		}
	case "e":
		if t := m.task(); !m.freshPending && t.Guest && m.guest.fresh && m.guest.id == t.ID && m.guest.sandbox == t.Sandbox && m.guest.vmid == t.VMID {
			m.freshPending = true
			return m, func() tea.Msg { return freshEvent{t, verifiedGuest(t)} }
		}
	case "?":
		m.help = true
	}
	return m, nil
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
	return func() tea.Msg {
		c, err := Changes(t)
		return changesEvent{t.ID, t.Tip, c, err}
	}
}

func (m *dashModel) files() []Change {
	if m.showFiles {
		return m.data.Files
	}
	return m.changes
}

func (m *dashModel) move(key string) tea.Cmd {
	delta := 1
	if key == "up" || key == "pgup" {
		delta = -1
	}
	if strings.HasPrefix(key, "pg") {
		delta *= max(1, m.height/3)
	}
	switch {
	case m.preview && m.focus != 1:
		m.previewScroll = max(0, m.previewScroll+delta)
	case m.focus == 1:
		m.file = max(0, min(m.file+delta, len(m.files())-1))
	case m.focus == 2:
		m.detailScroll = max(0, m.detailScroll+delta)
	default:
		order := m.taskOrder()
		if len(order) == 0 {
			return nil
		}
		m.selected = order[max(0, min(slices.Index(order, m.selected)+delta, len(order)-1))]
		m.file, m.detailScroll, m.preview = 0, 0, false
		m.changes = nil
		t := m.task()
		return tea.Batch(m.readChanges(t), m.readGuest(t), m.readMetadata(t))
	}
	return nil
}

func (m *dashModel) enter() tea.Cmd {
	if m.focus == 1 && len(m.files()) > 0 {
		m.preview, m.patch, m.previewScroll, m.focus = true, false, 0, 2
		return m.openFile()
	}
	if t := m.task(); t.Live {
		m.attachID = t.ID
		m.confirm = choiceui.NewAttachConfirm(safeText(t.Agent), safeText(t.Title), m.width, m.height)
	}
	return nil
}

type previewEvent struct {
	request uint64
	text    string
	err     error
}

func (m *dashModel) openFile() tea.Cmd {
	files := m.files()
	if len(files) == 0 {
		return nil
	}
	cwd, t, file, patch := m.CWD, m.task(), files[min(m.file, len(files)-1)], m.patch
	m.previewPath, m.previewText = file.Path, ""
	m.previewRequest++
	request := m.previewRequest
	return func() tea.Msg {
		text, err := Preview(cwd, t, file, patch)
		return previewEvent{request, text, err}
	}
}

type newWizard struct {
	step   int
	values [3]string
	input  textinput.Model
	prompt textarea.Model
}

func startWizard(targets []string, w int) *newWizard {
	a := &newWizard{input: textinput.New(), prompt: textarea.New()}
	if len(targets) > 0 {
		a.input.SetValue(targets[0])
	}
	a.input.Focus()
	a.prompt.SetWidth(max(1, w-8))
	a.prompt.SetHeight(5)
	return a
}

func (a *newWizard) edit(msg tea.Msg) (cmd tea.Cmd) {
	if a.step < 3 {
		a.input, cmd = a.input.Update(msg)
	} else if a.step == 3 {
		a.prompt, cmd = a.prompt.Update(msg)
	}
	return cmd
}

func (m *dashModel) wizardKey(msg tea.KeyPressMsg) tea.Cmd {
	a := m.wizard
	switch {
	case msg.String() == "esc":
		m.wizard = nil
	case a.step < 3 && msg.String() == "enter":
		value := strings.TrimSpace(a.input.Value())
		if a.step == 0 && !slices.Contains(m.Targets, value) {
			m.status = "Choose a harness from the list"
			return nil
		}
		a.values[a.step], a.step = value, a.step+1
		a.input.SetValue("")
		if a.step == 3 {
			return a.prompt.Focus()
		}
	case a.step == 3 && msg.String() == "ctrl+s":
		if strings.TrimSpace(a.prompt.Value()) == "" {
			m.status = "Enter an initial prompt"
		} else {
			a.step++
		}
	case a.step == 4 && msg.String() == "enter":
		m.wizard = nil
		return func() tea.Msg {
			if m.Launch == nil {
				return resultEvent{err: errors.New("instance launch unavailable")}
			}
			job, err := m.Launch(a.values[0], a.values[1], a.values[2], a.prompt.Value())
			return resultEvent{message: fmt.Sprintf("%s · queued", job), err: err}
		}
	default:
		return a.edit(msg)
	}
	return nil
}
