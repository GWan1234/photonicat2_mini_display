package main

import (
	"image/color"
	"testing"
)

// The SMS pages are drawn in Go rather than from display_template elements,
// so the web UI's colour themes -- which work by rewriting each element's
// "color" -- never reached them: a themed device showed every cfg page in its
// new palette and the SMS pages still in hardcoded yellow. smsThemeColors()
// derives the palette from the themed pages instead, using the same role
// split the web UI applies (first big-font element = the theme's primary).
//
// These tests pin both halves of that contract: an unthemed device must stay
// pixel-identical to the old hardcoded look, and a themed one must follow.

// withLayout installs a display template for the duration of one test.
func withLayout(t *testing.T, pages map[string][]DisplayElement, numPages int) {
	t.Helper()
	configMutex.Lock()
	oldEls := cfg.DisplayTemplate.Elements
	oldNum := cfgNumPages
	cfg.DisplayTemplate.Elements = pages
	cfgNumPages = numPages
	configMutex.Unlock()
	t.Cleanup(func() {
		configMutex.Lock()
		cfg.DisplayTemplate.Elements = oldEls
		cfgNumPages = oldNum
		configMutex.Unlock()
	})
}

func TestSmsThemeColorsUnthemedFallsBackToClassic(t *testing.T) {
	// No colours anywhere: an unthemed device must look exactly as it did
	// before this function existed.
	withLayout(t, map[string][]DisplayElement{
		"page0": {{Type: "text", Font: "huge", DataKey: "CpuUsage"}},
	}, 1)

	recv, title := smsThemeColors()
	if recv != PCAT_YELLOW {
		t.Errorf("received text = %v, want PCAT_YELLOW %v", recv, PCAT_YELLOW)
	}
	if title != PCAT_WHITE {
		t.Errorf("sender line = %v, want PCAT_WHITE %v", title, PCAT_WHITE)
	}
}

func TestSmsThemeColorsFollowsThemePrimary(t *testing.T) {
	aurora := color.RGBA{70, 235, 145, 255}
	withLayout(t, map[string][]DisplayElement{
		"page0": {
			{Type: "text", Font: "small", DataKey: "MemUsage",
				Color: []int{215, 255, 235}},
			{Type: "text", Font: "huge", DataKey: "CpuUsage",
				Color: []int{70, 235, 145}},
		},
	}, 1)

	recv, _ := smsThemeColors()
	if recv != aurora {
		t.Errorf("received text = %v, want the theme primary %v", recv, aurora)
	}
}

func TestSmsThemeColorsSkipsIcons(t *testing.T) {
	// Icons carry a colour too but are tinted by role, not by font; picking
	// one would give the SMS page an arbitrary accent shade.
	want := color.RGBA{70, 235, 145, 255}
	withLayout(t, map[string][]DisplayElement{
		"page0": {
			{Type: "icon", Font: "huge", IconPath: "assets/batt.svg",
				Color: []int{1, 2, 3}},
			{Type: "text", Font: "huge", DataKey: "CpuUsage",
				Color: []int{70, 235, 145}},
		},
	}, 1)

	if recv, _ := smsThemeColors(); recv != want {
		t.Errorf("received text = %v, want %v (icon must be skipped)", recv, want)
	}
}

func TestSmsThemeColorsScansLaterPages(t *testing.T) {
	// The first page may carry no themed big-font element (page2 on the
	// shipped layout is sparse); the scan must continue rather than fall back.
	want := color.RGBA{255, 160, 40, 255}
	withLayout(t, map[string][]DisplayElement{
		"page0": {{Type: "text", Font: "small", Color: []int{215, 255, 235}}},
		"page1": {{Type: "text", Font: "big", Color: []int{255, 160, 40}}},
	}, 2)

	if recv, _ := smsThemeColors(); recv != want {
		t.Errorf("received text = %v, want %v from a later page", recv, want)
	}
}

func TestSmsThemeColorsIgnoresPagesBeyondCount(t *testing.T) {
	// cfgNumPages bounds the rotation; a stale page past it is not on screen
	// and must not decide the palette.
	withLayout(t, map[string][]DisplayElement{
		"page0": {{Type: "text", Font: "small", Color: []int{215, 255, 235}}},
		"page7": {{Type: "text", Font: "huge", Color: []int{1, 2, 3}}},
	}, 1)

	if recv, _ := smsThemeColors(); recv != PCAT_YELLOW {
		t.Errorf("received text = %v, want PCAT_YELLOW (page7 is out of range)",
			recv)
	}
}

func TestSmsThemeColorsHandlesEmptyLayout(t *testing.T) {
	// Must not panic before the config is loaded.
	withLayout(t, map[string][]DisplayElement{}, 0)

	recv, title := smsThemeColors()
	if recv != PCAT_YELLOW || title != PCAT_WHITE {
		t.Errorf("empty layout = (%v, %v), want the classic pair", recv, title)
	}
}

func TestSmsThemeColorsIgnoresShortColorArrays(t *testing.T) {
	// A malformed [R,G] must not index out of range.
	withLayout(t, map[string][]DisplayElement{
		"page0": {
			{Type: "text", Font: "huge", Color: []int{12, 34}},
			{Type: "text", Font: "huge", Color: []int{70, 235, 145}},
		},
	}, 1)

	want := color.RGBA{70, 235, 145, 255}
	if recv, _ := smsThemeColors(); recv != want {
		t.Errorf("received text = %v, want %v", recv, want)
	}
}
