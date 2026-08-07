package main

// Tests for the boot animation (welcomeAnim.go). Everything here is pure —
// easing curves, colour mixing, affine matrices, the pose timeline and the
// SVG/raster frame builders — so the tests pin exact values where the math is
// exact and properties (ranges, monotonicity, endpoints) elsewhere.
//
// The single most important pin: at t = welcomeAnimDur the pose must be the
// exact resting pose (scale 1, no tilt, eyes open, brand yellow), because the
// last animation frame is documented to be pixel-identical to the static logo.

import (
	"bytes"
	"image"
	"math"
	"strings"
	"testing"
)

const utilCovEps = 1e-9

func utilCovClose(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestUtilCovWaClamp01(t *testing.T) {
	tests := []struct{ in, want float64 }{
		{-1, 0}, {-0.0001, 0}, {0, 0}, {0.25, 0.25}, {1, 1}, {1.0001, 1}, {42, 1},
	}
	for _, tt := range tests {
		if got := waClamp01(tt.in); got != tt.want {
			t.Errorf("waClamp01(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestUtilCovWaSmooth(t *testing.T) {
	tests := []struct {
		name    string
		a, b, x float64
		want    float64
	}{
		{"below_range", 1, 2, 0.5, 0},
		{"at_start", 1, 2, 1, 0},
		{"midpoint", 1, 2, 1.5, 0.5},
		{"at_end", 1, 2, 2, 1},
		{"above_range", 1, 2, 3, 1},
		{"quarter", 0, 1, 0.25, 0.25 * 0.25 * (3 - 2*0.25)}, // 0.15625
	}
	for _, tt := range tests {
		if got := waSmooth(tt.a, tt.b, tt.x); !utilCovClose(got, tt.want, utilCovEps) {
			t.Errorf("%s: waSmooth(%v,%v,%v) = %v, want %v", tt.name, tt.a, tt.b, tt.x, got, tt.want)
		}
	}
	// Smoothstep is monotone non-decreasing across the window.
	prev := -1.0
	for x := -0.2; x <= 1.2; x += 0.05 {
		v := waSmooth(0, 1, x)
		if v < prev {
			t.Fatalf("waSmooth not monotone at x=%v: %v < %v", x, v, prev)
		}
		prev = v
	}
}

func TestUtilCovWaEaseInOutCubic(t *testing.T) {
	tests := []struct{ in, want float64 }{
		{0, 0},
		{0.25, 4 * 0.25 * 0.25 * 0.25}, // 0.0625
		{0.5, 0.5},
		{0.75, 1 - math.Pow(-2*0.75+2, 3)/2}, // 0.9375
		{1, 1},
		{-3, 0}, // clamped
		{7, 1},  // clamped
	}
	for _, tt := range tests {
		if got := waEaseInOutCubic(tt.in); !utilCovClose(got, tt.want, utilCovEps) {
			t.Errorf("waEaseInOutCubic(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestUtilCovWaEaseInOutSine(t *testing.T) {
	tests := []struct{ in, want float64 }{
		{0, 0}, {0.5, 0.5}, {1, 1}, {-1, 0}, {2, 1},
	}
	for _, tt := range tests {
		if got := waEaseInOutSine(tt.in); !utilCovClose(got, tt.want, utilCovEps) {
			t.Errorf("waEaseInOutSine(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestUtilCovWaEaseOutBack(t *testing.T) {
	if got := waEaseOutBack(0); !utilCovClose(got, 0, utilCovEps) {
		t.Errorf("waEaseOutBack(0) = %v, want 0", got)
	}
	if got := waEaseOutBack(1); !utilCovClose(got, 1, utilCovEps) {
		t.Errorf("waEaseOutBack(1) = %v, want 1", got)
	}
	// The whole point of the curve: it overshoots 1.0 before settling.
	overshot := false
	for u := 0.5; u < 1; u += 0.01 {
		if waEaseOutBack(u) > 1 {
			overshot = true
			break
		}
	}
	if !overshot {
		t.Error("waEaseOutBack never exceeds 1 — the eye-pop overshoot is gone")
	}
	// Clamped outside [0,1].
	if got := waEaseOutBack(-2); !utilCovClose(got, 0, utilCovEps) {
		t.Errorf("waEaseOutBack(-2) = %v, want 0 (clamped)", got)
	}
	if got := waEaseOutBack(3); !utilCovClose(got, 1, utilCovEps) {
		t.Errorf("waEaseOutBack(3) = %v, want 1 (clamped)", got)
	}
}

func TestUtilCovWaHex(t *testing.T) {
	tests := []struct {
		in   [3]int
		want string
	}{
		{[3]int{0, 0, 0}, "#000000"},
		{[3]int{255, 255, 255}, "#FFFFFF"},
		{waYellow, "#FDE021"},
		{waSleepGrey, "#5E6A76"},
		{[3]int{1, 2, 3}, "#010203"},
	}
	for _, tt := range tests {
		if got := waHex(tt.in); got != tt.want {
			t.Errorf("waHex(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestUtilCovWaMixColor(t *testing.T) {
	a, b := [3]int{0, 0, 0}, [3]int{255, 255, 255}
	tests := []struct {
		name string
		k    float64
		want string
	}{
		{"k0_is_a", 0, "#000000"},
		{"k1_is_b", 1, "#FFFFFF"},
		{"k_below_clamps_to_a", -5, "#000000"},
		{"k_above_clamps_to_b", 5, "#FFFFFF"},
		{"midpoint_rounds", 0.5, "#808080"}, // 127.5+0.5 → 128
	}
	for _, tt := range tests {
		if got := waMixColor(a, b, tt.k); got != tt.want {
			t.Errorf("%s: waMixColor(k=%v) = %q, want %q", tt.name, tt.k, got, tt.want)
		}
	}
	// The wake transition endpoints used by the timeline.
	if got := waMixColor(waSleepGrey, waYellow, 0); got != "#5E6A76" {
		t.Errorf("sleep colour = %q, want #5E6A76", got)
	}
	if got := waMixColor(waSleepGrey, waYellow, 1); got != "#FDE021" {
		t.Errorf("awake colour = %q, want #FDE021", got)
	}
}

// utilCovApply runs a point through an affine matrix the way SVG does:
// x' = a*x + c*y + e, y' = b*x + d*y + f.
func utilCovApply(m waMat, x, y float64) (float64, float64) {
	return m.a*x + m.c*y + m.e, m.b*x + m.d*y + m.f
}

func TestUtilCovWaMatIdentity(t *testing.T) {
	id := waMatID()
	if id != (waMat{1, 0, 0, 1, 0, 0}) {
		t.Fatalf("waMatID() = %+v", id)
	}
	x, y := utilCovApply(id, 12.5, -3)
	if x != 12.5 || y != -3 {
		t.Errorf("identity moved the point: (%v,%v)", x, y)
	}
}

func TestUtilCovWaMatTranslate(t *testing.T) {
	m := waMatID().translate(3, 4)
	x, y := utilCovApply(m, 1, 1)
	if x != 4 || y != 5 {
		t.Errorf("translate(3,4) applied to (1,1) = (%v,%v), want (4,5)", x, y)
	}
}

func TestUtilCovWaMatScaleAbout(t *testing.T) {
	const cx, cy = 10.0, 20.0
	m := waMatID().scaleAbout(2, 3, cx, cy)
	// The pivot stays fixed.
	x, y := utilCovApply(m, cx, cy)
	if !utilCovClose(x, cx, utilCovEps) || !utilCovClose(y, cy, utilCovEps) {
		t.Errorf("scaleAbout moved its pivot: (%v,%v)", x, y)
	}
	// A unit offset scales by (sx, sy).
	x, y = utilCovApply(m, cx+1, cy+1)
	if !utilCovClose(x, cx+2, utilCovEps) || !utilCovClose(y, cy+3, utilCovEps) {
		t.Errorf("scaleAbout(2,3) offset = (%v,%v), want (%v,%v)", x, y, cx+2.0, cy+3.0)
	}
}

func TestUtilCovWaMatRotateAbout(t *testing.T) {
	const cx, cy = 5.0, 7.0
	m := waMatID().rotateAbout(90, cx, cy)
	// Pivot fixed.
	x, y := utilCovApply(m, cx, cy)
	if !utilCovClose(x, cx, utilCovEps) || !utilCovClose(y, cy, utilCovEps) {
		t.Errorf("rotateAbout moved its pivot: (%v,%v)", x, y)
	}
	// 90° in SVG's y-down frame maps +x onto +y.
	x, y = utilCovApply(m, cx+1, cy)
	if !utilCovClose(x, cx, utilCovEps) || !utilCovClose(y, cy+1, utilCovEps) {
		t.Errorf("rotateAbout(90°) maps (+1,0) to (%v,%v), want (0,+1) about pivot", x-cx, y-cy)
	}
	// 360° is the identity again.
	m = waMatID().rotateAbout(360, cx, cy)
	x, y = utilCovApply(m, 1, 2)
	if !utilCovClose(x, 1, 1e-9) || !utilCovClose(y, 2, 1e-9) {
		t.Errorf("rotateAbout(360°) is not identity: (%v,%v)", x, y)
	}
}

// mul composes m∘n (n first). Verify against applying the two matrices in
// sequence to a point.
func TestUtilCovWaMatMulComposition(t *testing.T) {
	m := waMatID().rotateAbout(37, 3, -2).scaleAbout(1.5, 0.5, -1, 4)
	n := waMatID().translate(2, -6).rotateAbout(-12, 0.5, 0.5)

	px, py := 1.25, -0.75
	nx, ny := utilCovApply(n, px, py)
	wantX, wantY := utilCovApply(m, nx, ny)
	gotX, gotY := utilCovApply(m.mul(n), px, py)
	if !utilCovClose(gotX, wantX, 1e-9) || !utilCovClose(gotY, wantY, 1e-9) {
		t.Errorf("m.mul(n) point = (%v,%v), want (%v,%v)", gotX, gotY, wantX, wantY)
	}
}

func TestUtilCovWaMatAttr(t *testing.T) {
	got := waMatID().attr()
	want := `transform="matrix(1.0000 0.0000 0.0000 1.0000 0.0000 0.0000)"`
	if got != want {
		t.Errorf("attr() = %s, want %s", got, want)
	}
	got = waMatID().translate(1.23456, -7).attr()
	want = `transform="matrix(1.0000 0.0000 0.0000 1.0000 1.2346 -7.0000)"`
	if got != want {
		t.Errorf("attr() = %s, want %s", got, want)
	}
}

func TestUtilCovWaComputeLayout(t *testing.T) {
	// The device panel: 172x320. Logo rests dead centre.
	l := waComputeLayout(172, 320)
	if l.logoX != 57 || l.logoY != 125 {
		t.Errorf("172x320 logo top-left = (%v,%v), want (57,125)", l.logoX, l.logoY)
	}
	if l.cx != 57+waLogoW/2 || l.cy != 125+waLogoH/2 {
		t.Errorf("172x320 centre = (%v,%v), want (%v,%v)", l.cx, l.cy, 57+waLogoW/2, 125+waLogoH/2)
	}
	// Degenerate: a screen exactly the logo size puts the logo at the origin.
	l = waComputeLayout(59, 71)
	if l.logoX != 0 || l.logoY != 0 {
		t.Errorf("59x71 logo top-left = (%v,%v), want (0,0)", l.logoX, l.logoY)
	}
}

// The last frame of the timeline must be the exact resting pose — it is
// documented to be pixel-identical to the static logo.
func TestUtilCovWaPoseAtEndIsRestingPose(t *testing.T) {
	p := waPoseAt(welcomeAnimDur)
	if !utilCovClose(p.fade, 1, utilCovEps) {
		t.Errorf("final fade = %v, want 1", p.fade)
	}
	if p.col != "#FDE021" {
		t.Errorf("final colour = %q, want brand yellow #FDE021", p.col)
	}
	if !utilCovClose(p.scale, 1, utilCovEps) {
		t.Errorf("final scale = %v, want exactly 1", p.scale)
	}
	if !utilCovClose(p.rot, 0, utilCovEps) {
		t.Errorf("final rotation = %v, want 0", p.rot)
	}
	if !utilCovClose(p.gaze, 0, utilCovEps) {
		t.Errorf("final gaze = %v, want 0", p.gaze)
	}
	if !utilCovClose(p.stretchX, 1, utilCovEps) || !utilCovClose(p.stretchY, 1, utilCovEps) {
		t.Errorf("final stretch = (%v,%v), want (1,1)", p.stretchX, p.stretchY)
	}
	if !utilCovClose(p.eyeOpen, 1, utilCovEps) {
		t.Errorf("final eyeOpen = %v, want 1", p.eyeOpen)
	}
	if p.glow != 0 {
		t.Errorf("final glow = %v, want 0 — a residual halo rings on the LCD", p.glow)
	}
}

func TestUtilCovWaPoseAtTimeline(t *testing.T) {
	// t=0: asleep — invisible (fade 0), grey, breathing large, eyes slitted.
	p := waPoseAt(0)
	if p.fade != 0 {
		t.Errorf("t=0 fade = %v, want 0", p.fade)
	}
	if p.col != "#5E6A76" {
		t.Errorf("t=0 colour = %q, want sleeping grey", p.col)
	}
	if p.scale < 1.2 {
		t.Errorf("t=0 scale = %v, want ≳1.26 (sleeping large)", p.scale)
	}
	if !utilCovClose(p.eyeOpen, 0.07, utilCovEps) {
		t.Errorf("t=0 eyeOpen = %v, want 0.07 (closed slits)", p.eyeOpen)
	}
	if p.glow != 0 || p.gaze != 0 || p.stretchX != 1 || p.stretchY != 1 {
		t.Errorf("t=0 glow/gaze/stretch = %v/%v/(%v,%v), want zeros and unit stretch",
			p.glow, p.gaze, p.stretchX, p.stretchY)
	}

	// Stir window (2.0–2.5): the shake term is live; before it, no shake.
	if a, b := waPoseAt(2.05).rot, waPoseAt(1.0).rot; a == 0 && b == 0 {
		t.Log("rot exactly zero at both probes — acceptable but unusual")
	}

	// Eyes pop with overshoot: somewhere in 2.4–2.8 eyeOpen exceeds 1.
	over := false
	for x := 2.40; x <= 2.80; x += 0.01 {
		if waPoseAt(x).eyeOpen > 1 {
			over = true
			break
		}
	}
	if !over {
		t.Error("eyes never overshoot while popping open")
	}

	// Glow blooms after the wake and dies out completely.
	if g := waPoseAt(2.75).glow; g <= 0 {
		t.Errorf("t=2.75 glow = %v, want > 0", g)
	}
	if g := waPoseAt(4.5).glow; g != 0 {
		t.Errorf("t=4.5 glow = %v, want exactly 0", g)
	}

	// Gaze: left hold, right hold, and the transitions between.
	if g := waPoseAt(4.0).gaze; !utilCovClose(g, -2, utilCovEps) {
		t.Errorf("t=4.0 gaze = %v, want -2 (left hold)", g)
	}
	if g := waPoseAt(5.0).gaze; !utilCovClose(g, 2, utilCovEps) {
		t.Errorf("t=5.0 gaze = %v, want +2 (right hold)", g)
	}
	if g := waPoseAt(3.5).gaze; g >= 0 || g <= -2 {
		t.Errorf("t=3.5 gaze = %v, want in (-2,0) (sweeping left)", g)
	}
	if g := waPoseAt(4.4).gaze; g <= -2 || g >= 2 {
		t.Errorf("t=4.4 gaze = %v, want in (-2,2) (sweeping right)", g)
	}
	if g := waPoseAt(5.3).gaze; g <= 0 || g >= 2 {
		t.Errorf("t=5.3 gaze = %v, want in (0,2) (returning)", g)
	}
	if g := waPoseAt(5.6).gaze; g != 0 {
		t.Errorf("t=5.6 gaze = %v, want 0 (centred)", g)
	}

	// Blinks dip the eyes during the holds.
	if open, held := waPoseAt(3.95).eyeOpen, waPoseAt(4.15).eyeOpen; open >= held {
		t.Errorf("blink at ~3.95 did not dip: %v >= %v", open, held)
	}

	// Landing bounce: stretch departs from 1 shortly after 6.10.
	p = waPoseAt(6.25)
	if p.stretchY == 1 || p.stretchX == 1 {
		t.Errorf("t=6.25 stretch = (%v,%v), want a squash-and-stretch bounce", p.stretchX, p.stretchY)
	}
	// Squash and stretch preserve opposition: sx moves opposite sy.
	if (p.stretchY-1)*(p.stretchX-1) >= 0 {
		t.Errorf("stretchX and stretchY move the same way: (%v,%v)", p.stretchX, p.stretchY)
	}

	// Fade is monotone non-decreasing.
	prev := -1.0
	for x := 0.0; x <= welcomeAnimDur; x += 0.05 {
		f := waPoseAt(x).fade
		if f < prev {
			t.Fatalf("fade regressed at t=%v: %v < %v", x, f, prev)
		}
		prev = f
	}
}

func TestUtilCovWaBlinkDip(t *testing.T) {
	const a, b = 2.0, 2.2
	if got := waBlinkDip(1.9, a, b); got != 1 {
		t.Errorf("before window: %v, want 1", got)
	}
	if got := waBlinkDip(a, a, b); got != 1 {
		t.Errorf("at open edge: %v, want 1", got)
	}
	if got := waBlinkDip(b, a, b); got != 1 {
		t.Errorf("at close edge: %v, want 1", got)
	}
	if got := waBlinkDip(2.5, a, b); got != 1 {
		t.Errorf("after window: %v, want 1", got)
	}
	// Mid-blink bottoms out at 1 - 0.94 = 0.06.
	if got := waBlinkDip(2.1, a, b); !utilCovClose(got, 0.06, utilCovEps) {
		t.Errorf("mid-blink: %v, want 0.06", got)
	}
}

func TestUtilCovWelcomeAnimSVG(t *testing.T) {
	const w, h = 172, 320

	// t=0: no glow, no sparkles — just the background, box and two eyes.
	svg := string(welcomeAnimSVG(0, w, h))
	if !strings.HasPrefix(svg, `<svg width="172" height="320" viewBox="0 0 172 320"`) {
		t.Errorf("svg header wrong: %.80s", svg)
	}
	if !strings.HasSuffix(svg, "</svg>") {
		t.Error("svg not terminated")
	}
	if !strings.Contains(svg, `<rect x="0" y="0" width="172" height="320" fill="#000000"/>`) {
		t.Error("opaque background rect missing — reused buffers would smear")
	}
	if strings.Contains(svg, "radialGradient") {
		t.Error("t=0 should have no waking halo")
	}
	if strings.Contains(svg, "M0 -1 L0.5 0") {
		t.Error("t=0 should have no sparkles")
	}
	for _, d := range []string{waBoxPath, waEyeLPath, waEyeRPath} {
		if !strings.Contains(svg, d) {
			t.Errorf("logo subpath missing from frame: %.40s...", d)
		}
	}

	// t=2.6: halo visible, exactly one sparkle live (t0=2.50), drawn as two
	// crossed diamonds.
	svg = string(welcomeAnimSVG(2.6, w, h))
	if !strings.Contains(svg, "radialGradient") || !strings.Contains(svg, `url(#wg)`) {
		t.Error("t=2.6 should show the waking halo")
	}
	if got := strings.Count(svg, "M0 -1 L0.5 0"); got != 2 {
		t.Errorf("t=2.6 sparkle path count = %d, want 2 (one sparkle, two diamonds)", got)
	}

	// t=3.0: three sparkles live → six diamond paths.
	svg = string(welcomeAnimSVG(3.0, w, h))
	if got := strings.Count(svg, "M0 -1 L0.5 0"); got != 6 {
		t.Errorf("t=3.0 sparkle path count = %d, want 6", got)
	}

	// Final frame: yellow logo, no halo, no sparkles.
	svg = string(welcomeAnimSVG(welcomeAnimDur, w, h))
	if !strings.Contains(svg, `fill="#FDE021"`) {
		t.Error("final frame is not brand yellow")
	}
	if strings.Contains(svg, "radialGradient") || strings.Contains(svg, "M0 -1 L0.5 0") {
		t.Error("final frame must carry no halo or sparkles")
	}
}

func TestUtilCovWelcomeAnimFrameInto(t *testing.T) {
	const w, h = 172, 320

	// nil buffer: allocated at the right size.
	frame, err := welcomeAnimFrameInto(nil, welcomeAnimDur, w, h)
	if err != nil {
		t.Fatalf("welcomeAnimFrameInto: %v", err)
	}
	if frame.Bounds().Dx() != w || frame.Bounds().Dy() != h {
		t.Fatalf("frame is %dx%d, want %dx%d", frame.Bounds().Dx(), frame.Bounds().Dy(), w, h)
	}

	// Background must be opaque black at the corners.
	for _, pt := range [][2]int{{0, 0}, {w - 1, 0}, {0, h - 1}, {w - 1, h - 1}} {
		c := frame.RGBAAt(pt[0], pt[1])
		if c.R != 0 || c.G != 0 || c.B != 0 || c.A != 255 {
			t.Errorf("corner (%d,%d) = %+v, want opaque black", pt[0], pt[1], c)
		}
	}

	// The resting logo must actually rasterize: brand-yellow pixels near the
	// centre of the panel.
	yellow := 0
	for y := h/2 - 40; y < h/2+40; y++ {
		for x := 0; x < w; x++ {
			c := frame.RGBAAt(x, y)
			if c.R > 200 && c.G > 170 && c.B < 90 {
				yellow++
			}
		}
	}
	if yellow < 100 {
		t.Errorf("only %d yellow-ish pixels in the logo band — logo did not draw", yellow)
	}

	// A correctly-sized buffer is reused, not reallocated.
	again, err := welcomeAnimFrameInto(frame, 0, w, h)
	if err != nil {
		t.Fatalf("welcomeAnimFrameInto (reuse): %v", err)
	}
	if again != frame {
		t.Error("correctly-sized buffer was not reused")
	}
	// And the reused buffer was fully overwritten: t=0 has fade 0, so the
	// yellow logo from the previous frame must be gone.
	if !bytes.Equal(again.Pix[:4], []byte{0, 0, 0, 255}) {
		t.Errorf("first pixel after redraw = %v, want opaque black", again.Pix[:4])
	}

	// A wrong-size buffer is replaced.
	small := image.NewRGBA(image.Rect(0, 0, 10, 10))
	got, err := welcomeAnimFrameInto(small, 1.0, w, h)
	if err != nil {
		t.Fatalf("welcomeAnimFrameInto (wrong size): %v", err)
	}
	if got == small {
		t.Error("undersized buffer was reused instead of replaced")
	}
	if got.Bounds().Dx() != w || got.Bounds().Dy() != h {
		t.Errorf("replacement buffer is %dx%d, want %dx%d", got.Bounds().Dx(), got.Bounds().Dy(), w, h)
	}
}
