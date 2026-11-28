// SPEC: _spec/_devops/unit-baseline.puml
package testx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// LinuxLaneImage is the guest base image the Linux lane runs tests in.
const LinuxLaneImage = "proveo/base-node:local"

const (
	linuxLaneEnv   = "PROVEO_LINUX_LANE"
	linuxLaneImage = "PROVEO_LINUX_LANE_IMAGE"
)

type laneBuild struct {
	once sync.Once
	path string
	err  error
}

var laneBuilds sync.Map

// InLinux runs the calling top-level test inside the guest base image when the host is not Linux.
// It reports true when the test ran there, and the caller returns.
func InLinux(t *testing.T) bool {
	t.Helper()
	if runtime.GOOS == "linux" || os.Getenv(linuxLaneEnv) == "inside" {
		return false
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skipf("needs Docker: this test drives the guest shell lib, which targets Linux (%v)", err)
	}
	image := LinuxLaneImage
	if v := strings.TrimSpace(os.Getenv(linuxLaneImage)); v != "" {
		image = v
	}
	if out, err := exec.Command(docker, "image", "inspect", "--format", "{{.Os}}", image).Output(); err != nil || strings.TrimSpace(string(out)) != "linux" {
		t.Skipf("needs Docker image %s: build it with `mise run build base-node` (%v)", image, err)
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := moduleRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	bin, err := laneBinary(dir)
	if err != nil {
		t.Fatalf("Linux lane build: %v", err)
	}
	name, _, _ := strings.Cut(t.Name(), "/")
	cmd := exec.Command(docker, "run", "--rm", "--entrypoint", "", "--network", "none",
		"-e", linuxLaneEnv+"=inside", "-v", root+":"+root+":ro", "-v", bin+":/proveo-lane.test:ro", "-w", dir,
		image, "/proveo-lane.test", "-test.run", "^"+regexp.QuoteMeta(name)+"$", "-test.count=1", "-test.v")
	out, err := cmd.CombinedOutput()
	switch {
	case err != nil:
		t.Fatalf("in %s: %v\n%s", image, err, out)
	case bytes.Contains(out, []byte("--- SKIP: "+name+" ")):
		t.Skipf("skipped in %s:\n%s", image, out)
	case !bytes.Contains(out, []byte("--- PASS: "+name+" ")):
		t.Fatalf("in %s %s did not run:\n%s", image, name, out)
	}
	return true
}

// laneBinary compiles the package in dir for linux once per process.
func laneBinary(dir string) (string, error) {
	v, _ := laneBuilds.LoadOrStore(dir, &laneBuild{})
	b := v.(*laneBuild)
	b.once.Do(func() {
		cache := filepath.Join(os.TempDir(), "proveo-linux-lane")
		if b.err = os.MkdirAll(cache, 0o700); b.err != nil {
			return
		}
		out := filepath.Join(cache, strings.ReplaceAll(strings.Trim(filepath.ToSlash(dir), "/"), "/", "_")+".test")
		tmp := fmt.Sprintf("%s.%d", out, os.Getpid())
		gobin, err := exec.LookPath("go")
		if err != nil {
			b.err = err
			return
		}
		build := exec.Command(gobin, "test", "-c", "-o", tmp, ".")
		build.Dir = dir
		build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
		if o, err := build.CombinedOutput(); err != nil {
			b.err = fmt.Errorf("go test -c: %v\n%s", err, o)
			return
		}
		if b.err = os.Rename(tmp, out); b.err == nil {
			b.path = out
		}
	})
	return b.path, b.err
}

func moduleRoot(dir string) (string, error) {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("no go.mod above %s", dir)
		}
	}
}
