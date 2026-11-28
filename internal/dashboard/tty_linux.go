//go:build linux

// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package dashboard

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios      = unix.TCGETS
	ioctlSetTermiosFlush = unix.TCSETSF
)
