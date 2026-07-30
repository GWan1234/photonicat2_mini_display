package main

import (
	"image"
	"testing"
)

// ensureImageCache stands in for the setup main() does at startup: the bar
// renderers memoise into imageCache, which is a nil map until then.
func ensureImageCache() {
	imageCacheMu.Lock()
	if imageCache == nil {
		imageCache = make(map[string]*image.RGBA)
	}
	imageCacheMu.Unlock()
}

// countDiskBarRuns counts the separate bars drawDiskBars painted in the slot by
// walking the columns and counting runs that have any ink in them. The bars are
// separated by a transparent gap, so a run is exactly one bar.
func countDiskBarRuns(frame *image.RGBA, x0, y0, w, h int) int {
	runs, inRun := 0, false
	for x := x0; x < x0+w; x++ {
		lit := false
		for y := y0; y < y0+h; y++ {
			if frame.RGBAAt(x, y).A > 0 {
				lit = true
				break
			}
		}
		if lit && !inRun {
			runs++
		}
		inRun = lit
	}
	return runs
}

// drawDiskBars hides the disks that are not there, so pulling the SD card out
// takes the row from three bars back to two (or to one when there is no NVMe
// either). Fonts are not loaded under `go test`, so only the bar geometry is
// exercised here — the labels are skipped.
func TestDiskBarsCountFollowsPresentDisks(t *testing.T) {
	ensureImageCache()
	const x0, y0, w, h = 5, 104, 159, 24

	cases := []struct {
		name     string
		nvme, sd bool
		want     int
	}{
		{"eMMC only", false, false, 1},
		{"eMMC + NVMe", true, false, 2},
		{"eMMC + SD (no NVMe)", false, true, 2},
		{"eMMC + NVMe + SD", true, true, 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			globalData.Store("DiskUsagePercent", 40.0)
			globalData.Store("DiskNvmePresent", tc.nvme)
			globalData.Store("DiskNvmePercent", 12.0)
			globalData.Store("DiskSDPresent", tc.sd)
			globalData.Store("DiskSDPercent", 80.0)

			frame := image.NewRGBA(image.Rect(0, 0, 172, 320))
			drawDiskBars(frame, x0, y0, w, h)

			if got := countDiskBarRuns(frame, x0, y0, w, h); got != tc.want {
				t.Errorf("drew %d bars, want %d", got, tc.want)
			}
		})
	}
}

// An SD card sitting in the slot unmounted has no usage to report: it still
// gets a bar, drawn empty.
func TestDiskBarsUnmountedSDDrawsEmptyBar(t *testing.T) {
	ensureImageCache()
	const x0, y0, w, h = 5, 104, 159, 24

	globalData.Store("DiskUsagePercent", 40.0)
	globalData.Store("DiskNvmePresent", false)
	globalData.Store("DiskSDPresent", true)
	globalData.Store("DiskSDPercent", 0.0)

	frame := image.NewRGBA(image.Rect(0, 0, 172, 320))
	drawDiskBars(frame, x0, y0, w, h)

	if got := countDiskBarRuns(frame, x0, y0, w, h); got != 2 {
		t.Fatalf("drew %d bars, want 2 (eMMC + empty SD)", got)
	}

	// The SD bar is the right-hand one: its interior must carry no yellow fill.
	for x := x0 + w/2; x < x0+w; x++ {
		for y := y0; y < y0+h; y++ {
			c := frame.RGBAAt(x, y)
			if c == PCAT_YELLOW {
				t.Fatalf("unmounted SD bar has fill at (%d,%d)", x, y)
			}
		}
	}
}

// Too narrow to split: the row falls back to the onboard bar alone instead of
// drawing slivers.
func TestDiskBarsTooNarrowFallsBackToOneBar(t *testing.T) {
	ensureImageCache()
	globalData.Store("DiskUsagePercent", 40.0)
	globalData.Store("DiskNvmePresent", true)
	globalData.Store("DiskNvmePercent", 12.0)
	globalData.Store("DiskSDPresent", true)
	globalData.Store("DiskSDPercent", 80.0)

	frame := image.NewRGBA(image.Rect(0, 0, 172, 320))
	drawDiskBars(frame, 5, 104, 10, 12)

	if got := countDiskBarRuns(frame, 5, 104, 10, 12); got != 1 {
		t.Errorf("drew %d bars in a 10px slot, want 1", got)
	}
}
