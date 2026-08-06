package main

// Compass tape geometry. The tape wraps at north, so the tests below focus on
// the wrap arithmetic (where an off-by-360 puts ticks off-screen) and on the
// no-fix path, which must still render without a stored heading.

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestNormalizeDeg(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{0, 0},
		{359.5, 359.5},
		{360, 0},
		{450, 90},
		{-90, 270},
		{-450, 270},
		{math.NaN(), 0},
		{math.Inf(1), 0},
		{math.Inf(-1), 0},
	}
	for _, c := range cases {
		got := normalizeDeg(c.in)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("normalizeDeg(%v) = %v, want %v", c.in, got, c.want)
		}
		if got < 0 || got >= 360 {
			t.Errorf("normalizeDeg(%v) = %v, out of [0,360)", c.in, got)
		}
	}
}

// compassDelta must take the short way around the circle: heading 350 to
// bearing 10 is +20, not -340. Getting this wrong pushes ticks hundreds of
// pixels off-screen and the tape appears to freeze near north.
func TestCompassDeltaWrapsShortWay(t *testing.T) {
	cases := []struct {
		from, to, want float64
	}{
		{350, 10, 20},
		{10, 350, -20},
		{90, 91, 1},
		{359, 0, 1},
		{0, 359, -1},
	}
	for _, c := range cases {
		got := compassDelta(c.from, c.to)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("compassDelta(%v,%v) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestCompassDeltaAlwaysInRange(t *testing.T) {
	for from := 0; from < 360; from += 7 {
		for to := 0; to < 360; to += 11 {
			d := compassDelta(float64(from), float64(to))
			// [-180,180]: the antipode is a tie and may come back either sign.
			if d < -180 || d > 180 {
				t.Fatalf("compassDelta(%d,%d) = %v, outside [-180,180]", from, to, d)
			}
		}
	}
}

// The tape draws with raw pixel writes, so a bad heading must not panic or
// scribble outside the element box.
func TestDrawGpsCompassStaysInBounds(t *testing.T) {
	gpsPreviewFonts()
	assetsPrefix = "."

	const w, h = 172, 46
	// Pad the canvas so out-of-box writes land somewhere detectable.
	const pad = 20
	headings := []float64{0, 45, 132, 180, 270, 359.9, -90, 720, math.NaN(), math.Inf(1)}

	for _, hd := range headings {
		frame := image.NewRGBA(image.Rect(0, 0, w+2*pad, h+2*pad))
		drawGpsCompass(frame, pad, pad, w, h, hd, true, PCAT_YELLOW)

		for y := 0; y < h+2*pad; y++ {
			for x := 0; x < w+2*pad; x++ {
				inBox := x >= pad && x < pad+w && y >= pad && y < pad+h
				if inBox {
					continue
				}
				if (frame.RGBAAt(x, y) != color.RGBA{}) {
					t.Fatalf("heading %v drew outside the box at (%d,%d)", hd, x, y)
				}
			}
		}
	}
}

// No fix: the widget still occupies its slot (so the page does not reflow)
// and renders in grey rather than the theme fill.
func TestDrawGpsCompassNoFixStillDraws(t *testing.T) {
	gpsPreviewFonts()
	assetsPrefix = "."

	frame := image.NewRGBA(image.Rect(0, 0, 172, 46))
	drawGpsCompass(frame, 0, 0, 172, 46, 0, false, PCAT_YELLOW)

	painted := 0
	for y := 0; y < 46; y++ {
		for x := 0; x < 172; x++ {
			if (frame.RGBAAt(x, y) != color.RGBA{}) {
				painted++
			}
		}
	}
	if painted == 0 {
		t.Fatal("no-fix compass drew nothing; the slot would collapse")
	}
}

func TestDrawGpsCompassRejectsBadGeometry(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 10, 10))
	// Must not panic on degenerate sizes or a nil frame.
	drawGpsCompass(frame, 0, 0, 0, 46, 90, true, PCAT_YELLOW)
	drawGpsCompass(frame, 0, 0, 172, 0, 90, true, PCAT_YELLOW)
	drawGpsCompass(nil, 0, 0, 172, 46, 90, true, PCAT_YELLOW)
}
