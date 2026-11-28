// SPEC: _spec/packages/lib/agent-activity.puml
package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/proveo-ca/proveo/internal/activity"
	"github.com/proveo-ca/proveo/internal/claudecfg"
)

// runActivity is `activity <harness> <busy|idle|waiting>`, run by an agent's turn hooks; it never fails the turn.
func runActivity(args []string) {
	if len(args) != 2 {
		return
	}
	var stdin io.Reader
	if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
		stdin = os.Stdin
	}
	_, _ = activity.Mark(activity.Dir, "/proc", args[0], activity.State(args[1]), os.Getppid(), stdin, time.Now())
	if args[0] == "cursor" && args[1] == string(activity.Busy) {
		fmt.Println(`{"continue":true}`)
	}
}

// runClaudeHook is `claude-hook <settings.json> <event> <timeout> <matcher> <command>`.
func runClaudeHook(args []string) error {
	if len(args) != 5 {
		return fmt.Errorf("want <settings.json> <event> <timeout> <matcher> <command>")
	}
	timeout, err := strconv.Atoi(args[2])
	if err != nil {
		return err
	}
	return activity.MergeClaudeHook(args[0], args[1], args[4], timeout, args[3])
}

// runClaudeTrust is `claude-trust <home> <dir>`: Claude Code's first-run flags and trust, merged into ~/.claude.json.
func runClaudeTrust(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("want <home> <dir>")
	}
	return claudecfg.AcceptWorkspace(args[0], args[1])
}
