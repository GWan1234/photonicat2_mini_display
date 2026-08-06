package main

// Applying a colour theme from the web UI must repaint the whole screen at
// once. Three surfaces cached their pixels behind keys that a pure recolour
// did not move, so the new palette only appeared where something else forced a
// redraw — in practice the user had to press the button to page forward before
// the footer and SMS pages caught up.
//
// The shared fix is configVersion: mergeConfigs bumps it on every remerge, and
// each cache now includes it in the key.

import (
	"image"
	"testing"
	"time"
)

// withConfigVersion pins configVersion for one test and restores it after, so
// these tests stay independent under -shuffle.
func withConfigVersion(t *testing.T, v int) {
	t.Helper()
	old := configVersion
	configVersion = v
	t.Cleanup(func() { configVersion = old })
}

// The footer key was page+count+isSMS only. Recolouring changes none of those,
// so the stale footer (background included) survived until the page changed.
func TestRenderFooterRepaintsOnConfigVersionBump(t *testing.T) {
	gpsPreviewFonts()
	assetsPrefix = "."
	withConfigVersion(t, 1)

	old := cacheFooterStr
	t.Cleanup(func() { cacheFooterStr = old })
	cacheFooterStr = ""

	frame := image.NewRGBA(image.Rect(0, 0, PCAT2_LCD_WIDTH, PCAT2_FOOTER_HEIGHT))

	if !renderFooter(frame, 0, 3, false) {
		t.Fatal("first render should draw")
	}
	if renderFooter(frame, 0, 3, false) {
		t.Fatal("identical state should hit the cache")
	}

	// A theme change: same page, same count, new configVersion.
	configVersion++
	if !renderFooter(frame, 0, 3, false) {
		t.Error("a config remerge must invalidate the footer cache")
	}
}

// Same contract for the top bar. It self-corrected within a minute when the
// clock minute rolled, which is exactly the lag the user saw.
func TestRenderTopBarRepaintsOnConfigVersionBump(t *testing.T) {
	gpsPreviewFonts()
	assetsPrefix = "."
	withConfigVersion(t, 1)

	old := cacheTopBarStr
	t.Cleanup(func() { cacheTopBarStr = old })
	cacheTopBarStr = ""

	frame := image.NewRGBA(image.Rect(0, 0, PCAT2_LCD_WIDTH, PCAT2_TOP_BAR_HEIGHT))

	renderTopBar(frame)
	if renderTopBar(frame) {
		t.Fatal("identical state should hit the cache")
	}

	configVersion++
	if !renderTopBar(frame) {
		t.Error("a config remerge must invalidate the top-bar cache")
	}
}

// signalSmsRerender must never block, whatever the channel state — it is
// called from mergeConfigs while configMutex is held, so a blocking send would
// deadlock the config save against the SMS goroutine.
func TestSignalSmsRerenderNeverBlocks(t *testing.T) {
	// Drain first so the test starts from a known state.
	select {
	case <-smsRerender:
	default:
	}
	t.Cleanup(func() {
		select {
		case <-smsRerender:
		default:
		}
	})

	done := make(chan struct{})
	go func() {
		// Several calls in a row: the buffer holds one, the rest must fall
		// through the default branch rather than block.
		for i := 0; i < 10; i++ {
			signalSmsRerender()
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("signalSmsRerender blocked")
	}

	// The coalesced signal is still pending for the SMS goroutine to pick up.
	select {
	case <-smsRerender:
	default:
		t.Error("signalSmsRerender should leave one pending wakeup")
	}
}

// The CPU/disk/memory meters' unfilled track used to be hardcoded black, so on
// a themed background (ember's [26,7,2]) each bar drew as a black rectangle
// punched out of the page. The track now follows screenBgColor.
func TestBarBackgroundFollowsThemeBackground(t *testing.T) {
	old := screenBgColor
	t.Cleanup(func() { setScreenBgColor([]int{int(old.R), int(old.G), int(old.B)}) })

	setScreenBgColor([]int{0, 0, 0})
	if got := barBgHex(); got != "#000000" {
		t.Errorf("black background -> barBgHex() = %s, want #000000", got)
	}

	setScreenBgColor([]int{26, 7, 2}) // ember
	if got := barBgHex(); got != "#1A0702" {
		t.Errorf("ember background -> barBgHex() = %s, want #1A0702", got)
	}
}

// The rendered frame bakes the background in, so the image cache must key on
// it — otherwise a theme change serves the previous theme's bitmap and the
// bars keep the old colour until restart.
func TestBarFrameCacheIsKeyedOnBackground(t *testing.T) {
	old := screenBgColor
	t.Cleanup(func() { setScreenBgColor([]int{int(old.R), int(old.G), int(old.B)}) })

	imageCacheMu.Lock()
	if imageCache == nil {
		imageCache = make(map[string]*image.RGBA)
	}
	imageCacheMu.Unlock()

	setScreenBgColor([]int{0, 0, 0})
	black := getBarOuterFrame(40, 20, 5)

	setScreenBgColor([]int{26, 7, 2})
	ember := getBarOuterFrame(40, 20, 5)

	if black == ember {
		t.Fatal("same cached frame returned for two different backgrounds")
	}
	// Sample an interior pixel: it should carry the theme background.
	r, g, b, _ := ember.At(20, 10).RGBA()
	if r>>8 != 26 || g>>8 != 7 || b>>8 != 2 {
		t.Errorf("ember bar interior = (%d,%d,%d), want (26,7,2)", r>>8, g>>8, b>>8)
	}
}
