// SPEC: _spec/_conventions/tui-design-language.puml
package ui

import (
	"errors"
	"image/color"
	"math"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func identitySet() map[int]bool {
	return map[int]bool{
		IdentityAccent: true, IdentityAccent1: true, IdentityAlt: true, IdentityAlt1: true, IdentityBlack: true,
		IdentityError: true, IdentityGray: true, IdentityGray2: true, IdentityGray3: true, IdentityGray4: true,
		IdentityMain: true, IdentityMain3: true, IdentitySuccess: true, IdentitySurface: true, IdentityWhite: true,
	}
}

func TestEveryToneHasEveryValue(t *testing.T) {
	t.Parallel()
	known := identitySet()
	for _, tone := range Tones {
		v, ok := tones[tone]
		if !ok {
			t.Fatalf("tone %d has no values", tone)
		}
		for side, rgb := range map[string]int{"dark": v.dark, "light": v.light} {
			pending := tone == ToneSurface && side == "dark" && rgb == ColorSurface
			if !known[rgb] && !pending {
				t.Errorf("tone %d %s = #%06X is not an identity colour", tone, side, rgb)
			}
		}
		if v.ansi16 < -1 || v.ansi16 > 15 {
			t.Errorf("tone %d 16-colour pick %d", tone, v.ansi16)
		}
	}
	if len(tones) != len(Tones) {
		t.Errorf("%d tones have values, %d are listed", len(tones), len(Tones))
	}
}

func luminance(rgb int) float64 {
	ch := func(v int) float64 {
		c := float64(v&0xff) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(rgb>>16) + 0.7152*ch(rgb>>8) + 0.0722*ch(rgb)
}

func contrast(a, b int) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

func TestTextTonesReadOnTheirBackground(t *testing.T) {
	t.Parallel()
	for _, tone := range []Tone{ToneInk, ToneMuted, ToneBrand, ToneAccent, ToneSuccess, ToneWarn, ToneError} {
		v := tones[tone]
		if c := contrast(v.dark, IdentityBlack); c < 3 {
			t.Errorf("tone %d on a dark terminal: contrast %.2f, want at least 3", tone, c)
		}
		if c := contrast(v.light, IdentityWhite); c < 3 {
			t.Errorf("tone %d on a light terminal: contrast %.2f, want at least 3", tone, c)
		}
	}
	if c := contrast(tones[ToneOnBrand].dark, tones[ToneBrand].dark); c < 4.5 {
		t.Errorf("ink on a dark brand fill: contrast %.2f", c)
	}
	if c := contrast(tones[ToneOnBrand].light, tones[ToneBrand].light); c < 4.5 {
		t.Errorf("ink on a light brand fill: contrast %.2f", c)
	}
}

func TestThemePicksTheDepth(t *testing.T) {
	t.Parallel()
	if c := (Theme{Dark: true, Depth: DepthTrue}).Color(ToneBrand); c != rgbColor(IdentityAlt) {
		t.Errorf("truecolour brand = %v", c)
	}
	if c := (Theme{Dark: false, Depth: DepthTrue}).Color(ToneBrand); c != rgbColor(IdentityMain) {
		t.Errorf("light brand = %v", c)
	}
	if _, ok := (Theme{Dark: true, Depth: Depth256}).Color(ToneWarn).(ansi.IndexedColor); !ok {
		t.Error("256-colour warn is not an indexed colour")
	}
	if c := (Theme{Depth: Depth16}).Color(ToneError); c != ansi.BasicColor(1) {
		t.Errorf("16-colour error = %v, want red", c)
	}
	if c := (Theme{Depth: Depth16}).Color(ToneInk); c != nil {
		t.Errorf("16-colour ink = %v; body text keeps the terminal's own colour", c)
	}
	if c := (Theme{Dark: true}).Color(ToneBrand); c != nil {
		t.Errorf("no colour depth painted %v", c)
	}
}

func TestHintsJoinKeysTheSharedWay(t *testing.T) {
	t.Parallel()
	s := NewStyles(Theme{})
	if got := ansi.Strip(s.Hints(s.Hint("Ctrl+C", "quit"), s.Hint("↑↓", "move"))); got != "Ctrl+C quit · ↑↓ move" {
		t.Errorf("hints = %q", got)
	}
}

func TestBackgroundDetectionIsBoundedAndGuarded(t *testing.T) {
	t.Parallel()
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i+1 < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	white := func() (color.Color, error) { return color.RGBA{0xff, 0xff, 0xff, 0xff}, nil }
	if DetectDark(env(), white, time.Second) {
		t.Error("a white background read as dark")
	}
	if !DetectDark(env("TMUX", "/tmp/tmux-1/default,1,0"), white, time.Second) {
		t.Error("tmux's unasked #ffffff must not flip proveo to light")
	}
	if DetectDark(env("PROVEO_THEME", "light"), func() (color.Color, error) { return nil, errors.New("unused") }, time.Second) {
		t.Error("PROVEO_THEME=light was ignored")
	}
	hang := func() (color.Color, error) { time.Sleep(time.Minute); return nil, nil }
	start := time.Now()
	if !DetectDark(env(), hang, 50*time.Millisecond) || time.Since(start) > 5*time.Second {
		t.Error("a terminal that never answers must default to dark, quickly")
	}
}
