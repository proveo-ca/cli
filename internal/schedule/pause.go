// SPEC: _spec/internal/dashboard/schedule-manager.puml
package schedule

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Pause is what SetPaused writes: Forever, a day the job runs again, or neither to resume.
type Pause struct {
	Forever bool
	Until   time.Time
}

// SetPaused rewrites one job's pause in the schedule file at path; every other line stays as it was.
func SetPaused(path, job string, p Pause) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := withPause(string(b), job, p)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return ReplaceFile(path, []byte(out), func(tmp string) error {
		c, err := LoadFile(tmp)
		if err != nil {
			return err
		}
		got := c.Jobs[job]
		want := ""
		if !p.Until.IsZero() {
			want = p.Until.Format(time.DateOnly)
		}
		if got.Paused != p.Forever || got.PausedUntil != want {
			return fmt.Errorf("job %q did not take the pause; edit it with e", job)
		}
		return nil
	})
}

// withPause drops the job's paused and paused_until lines and writes the one p names under its key.
func withPause(src, job string, p Pause) (string, error) {
	lines := strings.SplitAfter(src, "\n")
	key, child, err := findJob(lines, job)
	if err != nil {
		return "", err
	}
	if rest := strings.TrimSpace(strings.SplitN(lines[key], ":", 2)[1]); rest != "" && !strings.HasPrefix(rest, "#") {
		return "", fmt.Errorf("job %q is written inline; edit it with e", job)
	}
	field, end := -1, len(lines)
	for i := key + 1; i < len(lines); i++ {
		ind, ok := indentOf(lines[i])
		if !ok {
			continue
		}
		if ind <= child {
			end = i
			break
		}
		if field < 0 {
			field = ind
		}
	}
	if field < 0 {
		field = child + 2
	}
	var body []string
	for i := key + 1; i < end; i++ {
		if k := keyOf(lines[i], field); k == "paused" || k == "paused_until" {
			continue
		}
		body = append(body, lines[i])
	}
	pad := strings.Repeat(" ", field)
	switch {
	case p.Forever:
		body = append([]string{pad + "paused: true\n"}, body...)
	case !p.Until.IsZero():
		body = append([]string{pad + "paused_until: " + p.Until.Format(time.DateOnly) + "\n"}, body...)
	}
	head := append([]string{}, lines[:key+1]...)
	if !strings.HasSuffix(head[key], "\n") {
		head[key] += "\n"
	}
	return strings.Join(append(append(head, body...), lines[end:]...), ""), nil
}

// indentOf is a line's leading spaces; false for a blank or comment-only line.
func indentOf(l string) (int, bool) {
	t := strings.TrimLeft(l, " ")
	if s := strings.TrimSpace(t); s == "" || strings.HasPrefix(s, "#") {
		return 0, false
	}
	return len(l) - len(t), true
}

// keyOf is the mapping key a line opens at exactly ind spaces, unquoted; empty otherwise.
func keyOf(l string, ind int) string {
	if got, ok := indentOf(l); !ok || got != ind {
		return ""
	}
	k, _, ok := strings.Cut(strings.TrimSpace(l), ":")
	if !ok {
		return ""
	}
	return strings.Trim(k, `"'`)
}

// ReplaceFile swaps body in for path when check passes on a sibling copy; path keeps its mode.
func ReplaceFile(path string, body []byte, check func(tmp string) error) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".schedule-*.yml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, werr := f.Write(body)
	if err := errors.Join(werr, f.Chmod(mode), f.Sync(), f.Close()); err != nil {
		return err
	}
	if check != nil {
		if err := check(f.Name()); err != nil {
			return err
		}
	}
	return os.Rename(f.Name(), path)
}

// findJob is the line that opens job under jobs:, and the indent every job key sits at.
func findJob(lines []string, job string) (key, child int, err error) {
	jobs := slices.IndexFunc(lines, func(l string) bool { return keyOf(l, 0) == "jobs" })
	if jobs < 0 {
		return -1, -1, errors.New("no jobs: block")
	}
	child = -1
	for i := jobs + 1; i < len(lines); i++ {
		ind, ok := indentOf(lines[i])
		if !ok {
			continue
		}
		if ind == 0 {
			break
		}
		if child < 0 {
			child = ind
		}
		if ind == child && keyOf(lines[i], child) == job {
			return i, child, nil
		}
	}
	return -1, child, fmt.Errorf("job %q is not in the jobs: block", job)
}

// JobLine is the 1-based line that opens job in a schedule file's source; 0 when it has none.
func JobLine(src []byte, job string) int {
	key, _, err := findJob(strings.SplitAfter(string(src), "\n"), job)
	if err != nil {
		return 0
	}
	return key + 1
}
