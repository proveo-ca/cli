// SPEC: _spec/_conventions/tui-design-language.puml
package ui

import (
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Tone is what a painted cell means; the Theme picks its value for the terminal.
type Tone int

const (
	ToneInk Tone = iota + 1
	ToneMuted
	ToneRule
	ToneBrand
	ToneAccent
	ToneSuccess
	ToneWarn
	ToneError
	ToneSurface
	ToneOnBrand
)

// Tones lists every tone, in declaration order.
var Tones = []Tone{ToneInk, ToneMuted, ToneRule, ToneBrand, ToneAccent, ToneSuccess, ToneWarn, ToneError, ToneSurface, ToneOnBrand}

// toneValue is a tone on a dark and a light background, and its 16-colour pick; -1 keeps the terminal's own.
type toneValue struct {
	dark, light int
	ansi16      int
}

var tones = map[Tone]toneValue{
	ToneInk:     {IdentityGray4, IdentityBlack, -1},
	ToneMuted:   {IdentityGray, IdentityGray2, 8},
	ToneRule:    {IdentityGray2, IdentityGray3, 8},
	ToneBrand:   {IdentityAlt, IdentityMain, 6},
	ToneAccent:  {IdentityMain3, IdentityMain3, 4},
	ToneSuccess: {IdentitySuccess, IdentitySuccess, 2},
	ToneWarn:    {IdentityAccent, IdentityAccent1, 3},
	ToneError:   {IdentityError, IdentityError, 1},
	ToneSurface: {ColorSurface, IdentitySurface, -1},
	ToneOnBrand: {IdentityBlack, IdentityWhite, 0},
}

// Depth is how many colours the terminal shows.
type Depth int

const (
	DepthNone Depth = iota // NO_COLOR, TERM=dumb, not a terminal
	Depth16
	Depth256
	DepthTrue
)

// Theme is one session's background and colour depth.
type Theme struct {
	Dark  bool
	Depth Depth
}

// Color is tone t at the theme's background and depth; nil means leave the terminal's own colour.
func (th Theme) Color(t Tone) color.Color {
	v, ok := tones[t]
	if !ok || th.Depth == DepthNone {
		return nil
	}
	rgb := v.light
	if th.Dark {
		rgb = v.dark
	}
	c := rgbColor(rgb)
	switch th.Depth {
	case Depth16:
		if v.ansi16 < 0 {
			return nil
		}
		return ansi.BasicColor(v.ansi16)
	case Depth256:
		return ansi.Convert256(c)
	}
	return c
}

// Fg is a style that paints tone t, or an unstyled one where the theme paints nothing.
func (th Theme) Fg(t Tone) lipgloss.Style {
	if c := th.Color(t); c != nil {
		return lipgloss.NewStyle().Foreground(c)
	}
	return lipgloss.NewStyle()
}

func rgbColor(rgb int) color.Color {
	return color.RGBA{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 0xff}
}

// Styles are the shared component styles one theme paints.
type Styles struct {
	Theme                                                 Theme
	Ink, Muted, Rule, Brand, Accent, Success, Warn, Error lipgloss.Style
	Key                                                   lipgloss.Style
}

// HintJoin separates key hints on every surface.
const HintJoin = " · "

func NewStyles(th Theme) Styles {
	return Styles{
		Theme: th, Ink: th.Fg(ToneInk), Muted: th.Fg(ToneMuted), Rule: th.Fg(ToneRule), Brand: th.Fg(ToneBrand),
		Accent: th.Fg(ToneAccent), Success: th.Fg(ToneSuccess), Warn: th.Fg(ToneWarn), Error: th.Fg(ToneError),
		Key: th.Fg(ToneInk).Bold(true),
	}
}

// Hint is one key and its action: the key bold, the action muted.
func (s Styles) Hint(key, action string) string {
	return s.Key.Render(key) + " " + s.Muted.Render(action)
}

// Hints joins key hints the one way every surface does.
func (s Styles) Hints(hints ...string) string {
	return strings.Join(hints, s.Muted.Render(HintJoin))
}

// ThemeOverride reads PROVEO_THEME: dark or light decides; anything else asks the terminal.
func ThemeOverride(lookup func(string) string) (dark, ok bool) {
	switch strings.ToLower(strings.TrimSpace(lookup("PROVEO_THEME"))) {
	case "dark":
		return true, true
	case "light":
		return false, true
	}
	return false, false
}

// DarkFrom reads a reported background: unknown is dark, and so is tmux's unasked #ffffff.
func DarkFrom(c color.Color, lookup func(string) string) bool {
	if c == nil {
		return true
	}
	r, g, b, _ := c.RGBA()
	r, g, b = r>>8, g>>8, b>>8
	if lookup("TMUX") != "" && r == 0xff && g == 0xff && b == 0xff {
		return true
	}
	return 0.2126*float64(r)+0.7152*float64(g)+0.0722*float64(b) < 128
}

// DetectDark is PROVEO_THEME, else one query bounded by wait, else dark.
func DetectDark(lookup func(string) string, query func() (color.Color, error), wait time.Duration) bool {
	if dark, ok := ThemeOverride(lookup); ok {
		return dark
	}
	got := make(chan color.Color, 1)
	go func() {
		c, err := query()
		if err != nil {
			c = nil
		}
		got <- c
	}()
	select {
	case c := <-got:
		return DarkFrom(c, lookup)
	case <-time.After(wait):
		return true
	}
}
