//go:build unix

// SPEC: _spec/internal/dashboard/workspace-instances.puml
package dashboard

import (
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

type ptyChild struct {
	file   *os.File
	cmd    *exec.Cmd
	term   *vt.SafeEmulator
	done   chan struct{}
	frames sync.WaitGroup
	err    error
}

func startChild(width, height int) (childTerminal, error) {
	cmd := exec.Command("sbx")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
	if err != nil {
		return nil, err
	}
	c := &ptyChild{f, cmd, vt.NewSafeEmulator(width, height), make(chan struct{}), sync.WaitGroup{}, nil}
	c.frames.Go(func() { _, _ = io.Copy(c.term, f) })
	go func() { _, _ = io.Copy(f, c.term) }()
	go func() {
		c.err = cmd.Wait()
		_ = f.Close()
		c.frames.Wait()
		_ = c.term.Close()
		close(c.done)
	}()
	return c, nil
}

func (c *ptyChild) Frame() (string, *tea.Cursor) {
	frame := c.term.Render()
	if c.term.CursorHidden() {
		return frame, nil
	}
	p := c.term.CursorPosition()
	return frame, tea.NewCursor(p.X, p.Y)
}

func (c *ptyChild) Input(msg tea.Msg) {
	if _, done := c.Status(); done {
		return
	}
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		c.term.SendKey(uv.KeyPressEvent(uv.Key(m.Key())))
	case tea.PasteMsg:
		c.term.Paste(m.Content)
	case tea.MouseClickMsg:
		mouse := m.Mouse()
		mouse.Y--
		c.term.SendMouse(vt.MouseClick(uv.Mouse(mouse)))
	case tea.MouseReleaseMsg:
		mouse := m.Mouse()
		mouse.Y--
		c.term.SendMouse(vt.MouseRelease(uv.Mouse(mouse)))
	case tea.MouseWheelMsg:
		mouse := m.Mouse()
		mouse.Y--
		c.term.SendMouse(vt.MouseWheel(uv.Mouse(mouse)))
	case tea.MouseMotionMsg:
		mouse := m.Mouse()
		mouse.Y--
		c.term.SendMouse(vt.MouseMotion(uv.Mouse(mouse)))
	}
}

func (c *ptyChild) Resize(w, h int) {
	c.term.Resize(w, h)
	_ = pty.Setsize(c.file, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)})
}

func (c *ptyChild) Status() (string, bool) {
	select {
	case <-c.done:
		if c.err != nil {
			return "sbx exited · " + c.err.Error(), true
		}
		return "sbx exited", true
	default:
		return "", false
	}
}

func (c *ptyChild) Close() {
	if _, done := c.Status(); done {
		return
	}
	_ = c.cmd.Process.Signal(os.Interrupt)
	select {
	case <-c.done:
	case <-time.After(700 * time.Millisecond):
		_ = c.cmd.Process.Kill()
		_ = c.file.Close()
	}
}
