// SPEC: _spec/packages/lib/agent-activity.puml
package activity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// State is what an agent is doing as its own signals tell it.
type State string

const (
	Busy       State = "busy"       // a turn is in flight
	Background State = "background" // the turn ended with background work still running
	Waiting    State = "waiting"    // a dialog waits on the operator
	Idle       State = "idle"       // at its prompt, nothing running
	Exited     State = "exited"     // every agent that reported has gone
	Unknown    State = ""           // no signal
)

// Dir holds one marker per agent process; /dev/shm empties when the VM stops.
const Dir = "/dev/shm/proveo-activity"

// Marker is one agent process's last turn state, written by its hooks.
type Marker struct {
	Harness string `json:"harness"`
	PID     int    `json:"pid"`
	State   State  `json:"state"`
	Since   int64  `json:"since"`
}

var harnessName = regexp.MustCompile(`^[a-z]{1,32}$`)

// shells are the launchers between an agent and its hook command.
var shells = map[string]bool{"sh": true, "bash": true, "dash": true, "env": true, "timeout": true}

// AgentPID walks from pid up past shells to the process that ran the hook.
func AgentPID(procRoot string, pid int) int {
	for range 16 {
		if pid <= 1 {
			return pid
		}
		comm, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "comm"))
		if err != nil || !shells[strings.TrimSpace(string(comm))] {
			return pid
		}
		stat, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
		if err != nil {
			return pid
		}
		_, rest, ok := bytes.Cut(stat, []byte(") "))
		fields := strings.Fields(string(rest))
		if !ok || len(fields) < 2 {
			return pid
		}
		parent, err := strconv.Atoi(fields[1])
		if err != nil {
			return pid
		}
		pid = parent
	}
	return pid
}

// Mark records state for the agent above ppid, reading a Stop payload's background_tasks from stdin.
func Mark(dir, procRoot, harness string, state State, ppid int, stdin io.Reader, now time.Time) (Marker, error) {
	if !harnessName.MatchString(harness) {
		return Marker{}, fmt.Errorf("harness %q", harness)
	}
	switch state {
	case Busy, Idle, Waiting:
	default:
		return Marker{}, fmt.Errorf("state %q", state)
	}
	if state == Idle && stdin != nil {
		var payload struct {
			Background []json.RawMessage `json:"background_tasks"`
		}
		if b, err := io.ReadAll(io.LimitReader(stdin, 1<<20)); err == nil && json.Unmarshal(b, &payload) == nil && len(payload.Background) > 0 {
			state = Background
		}
	}
	m := Marker{Harness: harness, PID: AgentPID(procRoot, ppid), State: state, Since: now.UnixMilli()}
	if m.PID <= 1 {
		return Marker{}, errors.New("no agent process above the hook")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Marker{}, err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return Marker{}, err
	}
	tmp, err := os.CreateTemp(dir, ".marker-*")
	if err != nil {
		return Marker{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return Marker{}, err
	}
	if err := tmp.Close(); err != nil {
		return Marker{}, err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return Marker{}, err
	}
	return m, os.Rename(tmp.Name(), filepath.Join(dir, strconv.Itoa(m.PID)+".json"))
}

// MergeClaudeHook adds command under settings' hooks[event] once, keeping every other key.
func MergeClaudeHook(path, event, command string, timeout int, matcher string) error {
	if path == "" || event == "" || command == "" {
		return errors.New("settings path, event and command are required")
	}
	settings := map[string]any{}
	if b, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(b)) > 0 {
		if err := json.Unmarshal(b, &settings); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	groups, _ := hooks[event].([]any)
	for _, g := range groups {
		group, _ := g.(map[string]any)
		entries, _ := group["hooks"].([]any)
		for _, e := range entries {
			if entry, _ := e.(map[string]any); entry["command"] == command {
				return nil
			}
		}
	}
	group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": timeout}}}
	if matcher != "" {
		group["matcher"] = matcher
	}
	hooks[event] = append(groups, group)
	settings["hooks"] = hooks
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Sample is one signal the dashboard's guest probe read.
type Sample struct {
	Source  string // "claudecode" or "codex" for an agent's own state, "marker" for a hook marker
	Harness string
	State   State
	Since   time.Time
	Alive   bool
}

// native maps an agent's own status words onto State.
var native = map[string]State{
	"busy": Busy, "idle": Idle, "waiting": Waiting, "shell": Background,
	"task_started": Busy, "task_complete": Idle, "turn_aborted": Idle,
}

// Parse reads the probe's "activity\t<harness>\t<word>\t<ms>\t<alive>" and "marker\t<alive>\t<json>" lines.
func Parse(raw string) []Sample {
	var out []Sample
	for line := range strings.SplitSeq(raw, "\n") {
		f := strings.Split(line, "\t")
		switch {
		case len(f) == 5 && f[0] == "activity":
			state, ok := native[f[2]]
			if !ok || !harnessName.MatchString(f[1]) {
				continue
			}
			ms, _ := strconv.ParseInt(f[3], 10, 64)
			out = append(out, Sample{Source: f[1], Harness: f[1], State: state, Since: time.UnixMilli(ms), Alive: f[4] == "1"})
		case len(f) == 3 && f[0] == "marker":
			var m Marker
			if json.Unmarshal([]byte(f[2]), &m) != nil || !harnessName.MatchString(m.Harness) {
				continue
			}
			switch m.State {
			case Busy, Background, Idle, Waiting:
				out = append(out, Sample{Source: "marker", Harness: m.Harness, State: m.State, Since: time.UnixMilli(m.Since), Alive: f[1] == "1"})
			}
		}
	}
	return out
}

var urgency = map[State]int{Waiting: 4, Busy: 3, Background: 2, Idle: 1}

// Resolve is the sandbox's state: an agent's own state over hook markers for that agent, the most urgent live
// agent first; Exited when only dead agents reported; Unknown with no signal.
func Resolve(samples []Sample) (State, time.Time) {
	nativeFor := map[string]bool{}
	for _, s := range samples {
		if s.Source != "marker" && s.Alive {
			nativeFor[s.Harness] = true
		}
	}
	best, since, seen := Unknown, time.Time{}, false
	for _, s := range samples {
		seen = true
		if !s.Alive || (s.Source == "marker" && nativeFor[s.Harness]) {
			continue
		}
		if urgency[s.State] > urgency[best] || (s.State == best && s.Since.After(since)) {
			best, since = s.State, s.Since
		}
	}
	if best == Unknown && seen {
		return Exited, time.Time{}
	}
	return best, since
}
