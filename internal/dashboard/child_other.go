//go:build !unix

// SPEC: _spec/internal/dashboard/workspace-instances.puml
package dashboard

import "errors"

func startChild(int, int) (childTerminal, error) {
	return nil, errors.New("the sbx tab requires a Unix PTY")
}
