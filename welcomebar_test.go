package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

// Validates the direct-draw welcome progress bar (no /tmp SVG files): the
// track and every fill width must render visible pixels, growing monotonically.
func TestWelcomeBarDirectDraw(t *testing.T) {
	barWidth, barHeight, radius := 82, 8, 5
	track := color.RGBA{0x62, 0x74, 0x82, 255}
	fill := color.RGBA{0xFD, 0xE0, 0x21, 255}

	bar := image.NewRGBA(image.Rect(0, 0, barWidth, barHeight))
	drawRect(bar, 0, 0, barWidth, barHeight, color.RGBA{0, 0, 0, 255})
	drawRoundedBar(bar, barWidth, barHeight, radius, track)
	if n := countColor(bar, track); n == 0 {
		t.Fatal("track rendered no pixels")
	}

	prev := 0
	for i := 1; i <= barWidth; i++ {
		drawRoundedBar(bar, i, barHeight, radius, fill)
		n := countColor(bar, fill)
		if n == 0 {
			t.Errorf("fill width %d rendered no pixels", i)
		}
		if n < prev {
			t.Errorf("fill width %d shrank: %d -> %d pixels", i, prev, n)
		}
		prev = n
	}
	// Full-width fill should cover the whole rounded rect (652 px measured
	// from the old SVG renderer at the same geometry).
	if prev < 600 {
		t.Errorf("full-width fill only covers %d pixels", prev)
	}

	// Save a preview of a 60%-filled bar composited like showWelcome does.
	preview := image.NewRGBA(image.Rect(0, 0, barWidth, barHeight))
	drawRect(preview, 0, 0, barWidth, barHeight, color.RGBA{0, 0, 0, 255})
	drawRoundedBar(preview, barWidth, barHeight, radius, track)
	drawRoundedBar(preview, barWidth*6/10, barHeight, radius, fill)
	f, err := os.Create("/tmp/welcome_bar_preview.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, preview); err != nil {
		t.Fatal(err)
	}
}

func countColor(img *image.RGBA, c color.RGBA) int {
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.RGBAAt(x, y) == c {
				n++
			}
		}
	}
	return n
}
