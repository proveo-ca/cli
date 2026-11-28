//go:build darwin || freebsd || netbsd || openbsd || dragonfly

// SPEC: _spec/internal/dashboard/workspace-instances.puml
package dashboard

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios      = unix.TIOCGETA
	ioctlSetTermiosFlush = unix.TIOCSETAF
)
