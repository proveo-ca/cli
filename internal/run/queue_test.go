// SPEC: _spec/internal/dashboard/workspace-instances.puml
package run

import (
	"errors"
	"slices"
	"testing"

	"github.com/proveo-ca/proveo/internal/manifest"
)

func TestQueueArgsRepeatOnlyWhatTheRunChose(t *testing.T) {
	t.Parallel()
	got := queueArgs(Params{
		Credentials: "forward", CredsSet: true, Clone: false, CloneSet: true, Model: "opencode-go/glm-5.3",
		BrowserProfile: "work", Branch: "CONN-123", Addons: []string{"browser", "android"}, AddonsSet: true, Extra: []string{"--verbose"},
	}, "/apps/web/", "/repo")
	want := []string{"--credentials", "forward", "--clone=false", "--model", "opencode-go/glm-5.3",
		"--browser-profile", "work", "--branch", "CONN-123", "--scope", "apps/web", "--addon", "browser", "--addon", "android", "--", "--verbose"}
	if !slices.Equal(got, want) {
		t.Errorf("queueArgs = %q\nwant %q", got, want)
	}
	if got := queueArgs(Params{Credentials: "broker", Clone: true, Addons: []string{"browser"}, Output: "/repo/reports"}, "", "/repo"); len(got) != 0 {
		t.Errorf("defaults and cached answers replay from the settings cache, so nothing repeats: %q", got)
	}
}

func TestQueueSubmitsInsteadOfLaunching(t *testing.T) {
	t.Setenv("PROVEO_HOME", t.TempDir())
	t.Setenv("PROVEO_STATE_ROOT", t.TempDir())
	t.Setenv("PROVEO_WIZARD", "off")
	previous := sandboxReady
	t.Cleanup(func() { sandboxReady = previous })
	sandboxReady = func(bool, func(string) bool) (bool, string) { return true, "" }

	var got QueueRequest
	err := Do(Params{Target: "codex", Input: t.TempDir(), Queue: true, NewInstance: true, Task: "Persist history",
		Model: "gpt-5.5", CredsSet: true, Credentials: "forward"}, Deps{
		ManifestFor: func(string) (manifest.Manifest, error) { return manifest.Manifest{Name: "codex"}, nil },
		PreflightImages: func(manifest.Manifest, ImageRequest) (string, error) {
			return "", errors.New("a queued run must not preflight images")
		},
		Queue: func(q QueueRequest) error { got = q; return nil },
	})
	if err != nil {
		t.Fatalf("Do = %v", err)
	}
	if got.Target != "codex" || got.Task != "Persist history" ||
		!slices.Contains(got.Args, "--credentials") || !slices.Contains(got.Args, "gpt-5.5") || slices.Contains(got.Args, "--output") {
		t.Errorf("queued %+v", got)
	}
}

func TestTheStagedEnvAlwaysDropsHostOnlyKeys(t *testing.T) {
	for _, forwards := range []bool{true, false} {
		strip := cloneEnvStrip(forwards)
		if !slices.Contains(strip, "LINEAR_API_KEY") {
			t.Errorf("forwards=%v: strip %v keeps the operator's Linear token in the sandbox", forwards, strip)
		}
		if got := slices.Contains(strip, "ANTHROPIC_API_KEY"); got == forwards {
			t.Errorf("forwards=%v: provider keys stripped = %v", forwards, got)
		}
	}
}
