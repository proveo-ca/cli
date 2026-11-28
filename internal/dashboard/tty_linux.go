//go:build linux

// SPEC: _spec/internal/dashboard/workspace-instances.puml
package dashboard

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios      = unix.TCGETS
	ioctlSetTermiosFlush = unix.TCSETSF
)
