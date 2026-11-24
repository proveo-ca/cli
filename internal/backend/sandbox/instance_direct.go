// SPEC: _spec/internal/sbx/instance-lifecycle.puml
package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/proveo-ca/proveo/internal/sbx"
)

func directWorkspaceLease(in Input, cfg sbx.RunConfig) (func(), error) {
	dir := filepath.Join(receiptDir(), "direct-workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	gate, err := directGate(filepath.Join(dir, "gate.lock"))
	if err != nil {
		return nil, err
	}
	defer gate.Close()
	home, run := canonicalInstancePath(in.HomeRoot), canonicalInstancePath(in.RunDir)
	var paths []string
	for _, m := range cfg.Mounts {
		path := canonicalInstancePath(m.Host)
		if !m.ReadOnly && !within(path, home) && !within(path, run) {
			paths = append(paths, path)
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		name, ok := strings.CutSuffix(file.Name(), ".json")
		if !ok || name == cfg.Name {
			continue
		}
		record := filepath.Join(dir, file.Name())
		release, err := instanceLease(name)
		if err == nil {
			_ = os.Remove(record)
			release()
			continue
		}
		if !errors.Is(err, errInstanceOwned) {
			return nil, err
		}
		if err := checkDirectOverlap(record, name, paths); err != nil {
			return nil, err
		}
	}
	b, err := json.Marshal(paths)
	if err != nil {
		return nil, err
	}
	record := filepath.Join(dir, cfg.Name+".json")
	if err := writePrivateRecord(record, b); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(record) }, nil
}

func checkDirectOverlap(record, name string, paths []string) error {
	b, err := os.ReadFile(record)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var active []string
	if err := json.Unmarshal(b, &active); err != nil {
		return err
	}
	for _, p := range paths {
		if slices.ContainsFunc(active, func(a string) bool { return within(a, p) || within(p, a) }) {
			return fmt.Errorf("%s overlaps the writable workspace of %s; use a private instance with --new", p, name)
		}
	}
	return nil
}

func directGate(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		busy, err := tryProfileLock(f)
		if err == nil {
			return f, nil
		}
		if !busy || time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("lock writable workspace registry: %w", err)
		}
	}
}
