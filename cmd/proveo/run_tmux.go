// SPEC: _spec/cmd/proveo/run-session.puml
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/proveo-ca/proveo/internal/backend/sandbox"
	"github.com/proveo-ca/proveo/internal/envflag"
	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

// runSessionPrefix names the host tmux sessions interactive runs host their agent in.
const runSessionPrefix = "proveo-run-"

// stdoutTerminal reports whether proveo writes to a terminal.
var stdoutTerminal = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// hostsInTmux reports whether this run hosts its agent in a host tmux session: an interactive run, not already in one.
func (f *runFlags) hostsInTmux() bool {
	return os.Getenv(sandbox.RunSessionVar) == "" && os.Getenv("PROVEO_SCHEDULE_JOB") == "" &&
		envflag.Bool(os.Getenv("PROVEO_RUN_TMUX"), true) && !f.printOnly && !f.queue && !f.listSessions &&
		stdinTerminal() && stdoutTerminal() && schedule.TmuxAvailable()
}

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// envScript is env as shell exports, so the session runs with this terminal's environment rather than the tmux server's.
func envScript(env []string, session string) string {
	var b strings.Builder
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !envKey.MatchString(k) || k == "TMUX" || k == "TMUX_PANE" || k == sandbox.RunSessionVar {
			continue
		}
		b.WriteString("export " + k + "='" + strings.ReplaceAll(v, "'", `'\''`) + "'\n")
	}
	b.WriteString("export " + sandbox.RunSessionVar + "='" + session + "'\n")
	return b.String()
}

// hostedScript runs the command with the saved environment, keeps its last screen until Enter (an hour at most),
// and leaves its exit status for the terminal that started it.
const hostedScript = `. "$1"; rm -f "$1"; status=$2; shift 2
"$@"; rc=$?
printf '%s' "$rc" > "$status"
printf '\nproveo run ended (%s) · Enter closes this session\n' "$rc"
read -r -t 3600 _ || true
exit "$rc"`

// hostInTmux reruns this command inside a new host tmux session and attaches this terminal to it: one session for
// the agent, which the dashboard's ↵ and `proveo attach` join too. Detaching leaves the agent running.
func hostInTmux(target string, args []string) error {
	var id [3]byte
	_, _ = rand.Read(id[:])
	name := runSessionPrefix + strings.Trim(regexp.MustCompile(`[^A-Za-z0-9_-]+`).ReplaceAllString(target, "-"), "-") + "-" + hex.EncodeToString(id[:])
	dir, err := os.MkdirTemp("", "proveo-run-")
	if err != nil {
		return err
	}
	envFile, status := filepath.Join(dir, "env"), filepath.Join(dir, "status")
	if err := os.WriteFile(envFile, []byte(envScript(os.Environ(), name)), 0o600); err != nil {
		return err
	}
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w < 20 || h < 5 {
		w, h = 200, 50
	}
	argv := append([]string{"bash", "-c", hostedScript, "bash", envFile, status, proveoExe()}, args...)
	sess := schedule.NewTmux(name, nil)
	if err := sess.Start(w, h, argv...); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("start the run's tmux session: %w", err)
	}
	if err := attachTmux(name, name, false); err != nil {
		return err
	}
	if sess.Alive() {
		ui.Hostf("%s keeps running · `proveo attach %s` or the dashboard's ↵ rejoins it", name, name)
		return nil
	}
	defer func() { _ = os.RemoveAll(dir) }()
	b, err := os.ReadFile(status)
	if err != nil {
		return nil
	}
	if rc, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && rc != 0 {
		return sandbox.ExitError{Code: rc}
	}
	return nil
}
