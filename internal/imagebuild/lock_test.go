//go:build unix

package imagebuild_test

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/proveo-ca/proveo/internal/imagebuild"
	"github.com/proveo-ca/proveo/internal/ui"
)

// sharedDocker is a Docker two builds can use at once: an image is fresh once either of them built it.
type sharedDocker struct {
	mu     sync.Mutex
	built  map[string]int
	digest string
}

func (d *sharedDocker) Output(args ...string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case len(args) == 5 && args[0] == "image" && args[1] == "inspect" && args[4] == imagebuild.InputsFormat:
		if d.built[args[2]] > 0 {
			return d.digest, nil
		}
		return "built-from-older-defs", nil
	case len(args) >= 2 && args[0] == "context":
		return "default\n", nil
	case len(args) >= 2 && args[0] == "buildx" && args[1] == "inspect":
		return "Driver: docker\nStatus: running\n", nil
	case len(args) >= 1 && args[0] == "pull":
		return "", errors.New("pull denied")
	}
	return "", nil
}

func (d *sharedDocker) Stream(_ io.Writer, args ...string) error {
	ref, ok := imagebuild.ArgRef(args)
	if !ok {
		return nil
	}
	if strings.Contains(ref, "/base") {
		time.Sleep(30 * time.Millisecond)
	}
	d.mu.Lock()
	d.built[ref]++
	d.mu.Unlock()
	return nil
}

// Two harness builds in one wave share their stale bases: the second waits for the first's rebuild instead of racing it.
func TestConcurrentBuildsRebuildASharedStaleBaseOnce(t *testing.T) {
	root := repoRoot(t)
	d := &sharedDocker{built: map[string]int{}, digest: digest(t, root)}
	cache := t.TempDir()
	builder := func() *imagebuild.Builder {
		return &imagebuild.Builder{
			RepoRoot: root, Docker: d, Arch: "arm64", Out: io.Discard, UI: ui.New(io.Discard), MkdirAll: os.MkdirAll,
			Getenv: func(k string) string {
				if v, ok := pinned[k]; ok {
					return v
				}
				if k == "PROVEO_BUILDKIT_CACHE_DIR" {
					return cache
				}
				return ""
			},
			Fetch: func(string) ([]byte, error) { return nil, errors.New("pinned builds must not reach the network") },
		}
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, target := range []string{"claudecode", "codex"} {
		wg.Go(func() { errs[i] = builder().BuildTarget(target, "local", imagebuild.Options{}) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []string{"proveo/base:local", "proveo/base-node:local"} {
		if n := d.built[ref]; n != 1 {
			t.Errorf("%s was built %d times by two concurrent harness builds, want once", ref, n)
		}
	}
	if d.built["proveo/claudecode:local"] != 1 || d.built["proveo/codex:local"] != 1 {
		t.Errorf("built = %v", d.built)
	}
}
