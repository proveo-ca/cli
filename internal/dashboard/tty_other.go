//go:build !(darwin || freebsd || netbsd || openbsd || dragonfly || linux)

// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package dashboard

import "os"

type ttyState struct{}

func saveTTY(*os.File) *ttyState { return nil }

func (*ttyState) restore() {}
