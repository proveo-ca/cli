//go:build darwin || freebsd || netbsd || openbsd || dragonfly || linux

// SPEC: _spec/internal/dashboard/workspace-instances.puml
package dashboard

import (
	"os"

	"golang.org/x/sys/unix"
)

// ttyState is the terminal mode the dashboard found at start.
type ttyState struct {
	fd    int
	saved *unix.Termios
}

func saveTTY(f *os.File) *ttyState {
	fd := int(f.Fd())
	t, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil
	}
	return &ttyState{fd: fd, saved: t}
}

// restore puts the start mode back and discards unread input.
func (s *ttyState) restore() {
	if s != nil {
		_ = unix.IoctlSetTermios(s.fd, ioctlSetTermiosFlush, s.saved)
	}
}
