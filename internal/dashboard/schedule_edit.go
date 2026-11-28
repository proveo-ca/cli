// SPEC: _spec/internal/dashboard/schedule-manager.puml
package dashboard

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/proveo-ca/proveo/internal/choiceui"
	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

// scheduleEdit is one edit of schedule.yml: the draft the editor changes, and the file as the draft was taken.
type scheduleEdit struct {
	path, draft string
	base        []byte // schedule.yml when the draft was taken; nil when it did not exist
	handed      []byte // the draft as the editor got it
	line        int
}

type editedEvent struct{ err error }

// draftPath is the editor's copy of schedule.yml, beside it so a save is one rename.
func draftPath(path string) string { return filepath.Join(filepath.Dir(path), ".schedule.draft.yml") }

// editSchedule hands a draft of schedule.yml to the editor at the job's line; a job not in the file is added as a stub.
func (m *dashModel) editSchedule(job string, line int) tea.Cmd {
	path := schedule.Path(m.Home)
	base, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		m.fail(err)
		return nil
	}
	draft := slices.Clone(base)
	if job != "" && line == 0 {
		draft, line = withStub(draft, job, m.now().Location())
	}
	e := &scheduleEdit{path: path, draft: draftPath(path), base: base, handed: draft, line: line}
	if err := os.WriteFile(e.draft, draft, 0o600); err != nil {
		m.fail(err)
		return nil
	}
	m.schedEdit = e
	return m.runEditor(e)
}

func (m *dashModel) runEditor(e *scheduleEdit) tea.Cmd {
	argv := editorArgv(e.draft, e.line, os.Getenv)
	if m.editor != nil {
		argv = m.editor(e.draft, e.line)
	}
	return tea.ExecProcess(exec.Command(argv[0], argv[1:]...), func(err error) tea.Msg { return editedEvent{err} })
}

// receiveEdit saves a valid draft over schedule.yml, or names why not and offers the editor again.
func (m *dashModel) receiveEdit(ev editedEvent) tea.Cmd {
	e := m.schedEdit
	if e == nil {
		return nil
	}
	if ev.err != nil {
		m.fail(fmt.Errorf("editor: %w", ev.err))
		return nil
	}
	body, err := os.ReadFile(e.draft)
	if err != nil {
		m.fail(err)
		return nil
	}
	if bytes.Equal(body, e.handed) {
		m.dropEdit()
		m.say(ui.Tilde(e.path) + " unchanged")
		return nil
	}
	now, readErr := os.ReadFile(e.path)
	if errors.Is(readErr, fs.ErrNotExist) {
		now, readErr = nil, nil
	}
	if readErr == nil && !bytes.Equal(now, e.base) {
		m.refuseEdit(e, []string{ui.Tilde(e.path) + " changed while you edited.", "Edit again saves your draft over it; Discard keeps it as it is now."}, 0)
		e.base, e.handed = now, body
		return nil
	}
	if _, err := schedule.LoadFile(e.draft); err != nil {
		msg := strings.TrimPrefix(err.Error(), e.draft+": ")
		m.refuseEdit(e, strings.Split(msg, "\n"), yamlErrorLine(msg))
		e.handed = body
		return nil
	}
	if err := schedule.ReplaceFile(e.path, body, nil); err != nil {
		m.fail(err)
		return nil
	}
	m.dropEdit()
	m.say(ui.Tilde(e.path) + " saved")
	return m.readSchedule()
}

func (m *dashModel) refuseEdit(e *scheduleEdit, lines []string, line int) {
	m.confirm = choiceui.NewEditRefused(ui.Tilde(e.path), lines, m.width, m.height)
	m.onConfirm = func() tea.Cmd {
		if line > 0 {
			e.line = line
		}
		return m.runEditor(e)
	}
	m.onDecline = m.dropEdit
}

func (m *dashModel) dropEdit() {
	if m.schedEdit != nil {
		_ = os.Remove(m.schedEdit.draft)
	}
	m.schedEdit = nil
}

var yamlLine = regexp.MustCompile(`line (\d+)`)

// yamlErrorLine is the line a YAML error names; 0 when it names none.
func yamlErrorLine(msg string) int {
	if s := yamlLine.FindStringSubmatch(msg); s != nil {
		n, _ := strconv.Atoi(s[1])
		return n
	}
	return 0
}

// withStub appends a job block for the operator to fill in, and the line that opens it.
func withStub(src []byte, job string, loc *time.Location) ([]byte, int) {
	out := string(src)
	if !strings.Contains(out, "jobs:") {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += "jobs:\n"
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	line := strings.Count(out, "\n") + 1
	tz := loc.String()
	if tz == "Local" || tz == "" {
		tz = schedule.DefaultTimezone
	}
	out += "  " + job + ":\n" +
		"    target: claudecode                 # or hermes, codex, cursor, opencode\n" +
		"    prompt_file: /abs/path/" + job + ".md\n" +
		"    mode: goal                         # goal | loop | plain\n" +
		"    budget: 45m\n" +
		"    tz: " + tz + "\n" +
		"    at: [\"mon 09:00\"]                  # weekday + 24h clock, in tz; none: only `proveo schedule run` starts it\n"
	return []byte(out), line
}

// editorArgv opens file at line in $VISUAL or $EDITOR (vi when neither is set), waiting for GUI editors to close it.
func editorArgv(file string, line int, getenv func(string) string) []string {
	ed := strings.Fields(getenv("VISUAL"))
	if len(ed) == 0 {
		ed = strings.Fields(getenv("EDITOR"))
	}
	if len(ed) == 0 {
		ed = []string{"vi"}
	}
	wait := func(flag string) {
		if !slices.Contains(ed, flag) && !slices.Contains(ed, "-w") && !slices.Contains(ed, "--wait") {
			ed = append(ed, flag)
		}
	}
	at := file + ":" + strconv.Itoa(max(1, line))
	switch filepath.Base(ed[0]) {
	case "code", "code-insiders", "codium", "cursor", "windsurf":
		wait("--wait")
		return append(ed, "--goto", at)
	case "zed", "subl":
		wait("--wait")
		return append(ed, at)
	case "hx", "helix":
		return append(ed, at)
	}
	if line <= 0 {
		return append(ed, file)
	}
	return append(ed, "+"+strconv.Itoa(line), file)
}
