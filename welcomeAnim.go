package main

// welcomeAnim.go — boot animation: the photonicat cat wakes up inside its box.
//
// Every frame is authored as a small SVG document built from the same logo
// geometry as assets/svg/welcome.svg and rasterized with the oksvg/rasterx
// stack already used for the static assets. The eyes are separate subpaths of
// the original logo, so they can blink and glance while the box stays exact;
// at t = welcomeAnimDur the composite is pixel-identical to the static logo
// plus the finished boot bar.
//
// Timeline (5 s):
//   0.0–1.8  asleep: dim grey box, slow breathing, Zzz drifting up
//   1.8–2.2  stir: quick shake, Zzz pops, colour warms to yellow
//   2.2–3.0  eyes pop open (overshoot), soft glow blooms, double blink
//   3.0–4.2  glances left then right with a small head tilt, sparkles
//   4.1–5.0  springs down to the resting pose with a squash-and-stretch
//            bounce and a final blink while the boot bar completes

import (
	"bytes"
	"fmt"
	"image"
	"math"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

const welcomeAnimDur = 5.0 // seconds the timeline below is authored for

// Logo geometry in the 59x71 viewBox units of assets/svg/welcome.svg.
const (
	waLogoW = 59.0
	waLogoH = 71.0
	// Box outline and inner faces, eyes excluded (they are drawn on top).
	waBoxPath = "M0 17.75L29.499 0L59 17.75V53.25L29.499 71L0 53.25V17.75ZM29.499 67.9003V35.4978L56.4314 19.2966L29.499 3.09318V35.4978L2.57065 19.2988V51.6991L29.499 67.9003Z"
	// Left eye: bright shape on the dark face.
	waEyeLPath = "M20.763 41.3068C22.3595 42.7796 23.4002 45.0941 23.6084 48.0788C17.3181 51.2696 11.8271 47.9638 11.3317 40.6908C12.6225 40.0138 14.0286 39.6084 15.4697 39.4977C14.7891 42.1593 15.6321 45.0702 18.0008 47.1483C19.8409 45.5344 20.763 43.4217 20.763 41.3068Z"
	// Right eye: dark shape on the lit face.
	waEyeRPath = "M38.2017 41.3393C36.626 42.8143 35.6019 45.1179 35.3938 48.0788C41.684 51.2696 47.175 47.966 47.6704 40.7037C46.3692 40.0147 44.9488 39.6039 43.4929 39.4955C44.1735 42.1592 43.3326 45.0702 40.9618 47.1482C39.1321 45.5431 38.2121 43.4434 38.2017 41.3393Z"
	// Eye centres (blink/gaze anchors), logo-local.
	waEyeLCx = 17.47
	waEyeRCx = 41.53
	waEyeCy  = 45.38
)

var (
	waYellow    = [3]int{0xFD, 0xE0, 0x21} // brand yellow
	waSleepGrey = [3]int{0x5E, 0x6A, 0x76} // dozing box tint
	waZzzGrey   = [3]int{0x9A, 0xA9, 0xB7}
	waWarmWhite = [3]int{0xFF, 0xF6, 0xAE} // sparkle / bar-pulse tint
	waBarGrey   = [3]int{0x62, 0x74, 0x82} // bar background (matches old bar)
)

// ---------------------------------------------------------------------------
// small math helpers

func waClamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// waSmooth is smoothstep of t across [a, b].
func waSmooth(a, b, t float64) float64 {
	u := waClamp01((t - a) / (b - a))
	return u * u * (3 - 2*u)
}

func waEaseInOutCubic(u float64) float64 {
	u = waClamp01(u)
	if u < 0.5 {
		return 4 * u * u * u
	}
	v := -2*u + 2
	return 1 - v*v*v/2
}

func waEaseInOutSine(u float64) float64 {
	u = waClamp01(u)
	return -(math.Cos(math.Pi*u) - 1) / 2
}

// waEaseOutBack overshoots its target (~10%) before settling — used for the
// eyes popping open.
func waEaseOutBack(u float64) float64 {
	u = waClamp01(u)
	const c1 = 1.70158
	const c3 = c1 + 1
	v := u - 1
	return 1 + c3*v*v*v + c1*v*v
}

func waHex(c [3]int) string {
	return fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2])
}

func waMixColor(a, b [3]int, k float64) string {
	k = waClamp01(k)
	return fmt.Sprintf("#%02X%02X%02X",
		int(float64(a[0])+(float64(b[0])-float64(a[0]))*k+0.5),
		int(float64(a[1])+(float64(b[1])-float64(a[1]))*k+0.5),
		int(float64(a[2])+(float64(b[2])-float64(a[2]))*k+0.5))
}

// ---------------------------------------------------------------------------
// 2D affine matrices, emitted as SVG transform="matrix(...)" so no reliance
// on nested-group transform semantics is needed.

type waMat struct{ a, b, c, d, e, f float64 }

func waMatID() waMat { return waMat{1, 0, 0, 1, 0, 0} }

// mul composes m∘n: n is applied to the point first, then m.
func (m waMat) mul(n waMat) waMat {
	return waMat{
		a: m.a*n.a + m.c*n.b,
		b: m.b*n.a + m.d*n.b,
		c: m.a*n.c + m.c*n.d,
		d: m.b*n.c + m.d*n.d,
		e: m.a*n.e + m.c*n.f + m.e,
		f: m.b*n.e + m.d*n.f + m.f,
	}
}

func (m waMat) translate(tx, ty float64) waMat {
	return m.mul(waMat{1, 0, 0, 1, tx, ty})
}

func (m waMat) scaleAbout(sx, sy, cx, cy float64) waMat {
	return m.mul(waMat{sx, 0, 0, sy, cx - sx*cx, cy - sy*cy})
}

func (m waMat) rotateAbout(deg, cx, cy float64) waMat {
	r := deg * math.Pi / 180
	s, c := math.Sin(r), math.Cos(r)
	return m.mul(waMat{c, s, -s, c, cx - c*cx + s*cy, cy - s*cx - c*cy})
}

func (m waMat) attr() string {
	return fmt.Sprintf(`transform="matrix(%.4f %.4f %.4f %.4f %.4f %.4f)"`, m.a, m.b, m.c, m.d, m.e, m.f)
}

// ---------------------------------------------------------------------------
// layout + pose

// waLayout mirrors the static showWelcome layout so the animation lands
// exactly where the old static screen used to be.
type waLayout struct {
	cx, cy                 float64 // resting logo centre
	logoX, logoY           float64 // resting logo top-left
	barX, barY, barW, barH float64
}

func waComputeLayout(width, height int) waLayout {
	const spaceBetweenLogoAndBar = 28
	const barW, barH = 82, 8
	logoY := float64(height/2 - (int(waLogoH)+spaceBetweenLogoAndBar+barH)/2)
	logoX := float64(width/2 - int(waLogoW)/2)
	return waLayout{
		cx:    logoX + waLogoW/2,
		cy:    logoY + waLogoH/2,
		logoX: logoX,
		logoY: logoY,
		barX:  float64(width/2 - barW/2),
		barY:  logoY + spaceBetweenLogoAndBar + waLogoH,
		barW:  barW,
		barH:  barH,
	}
}

type waPose struct {
	fade               float64 // global fade-in
	col                string  // box + left-eye colour (grey → yellow)
	scale              float64 // base scale about the rest centre
	rot                float64 // degrees about the rest centre
	stretchX, stretchY float64 // squash & stretch about the rest bottom
	eyeOpen            float64 // 0 closed .. 1 open (may overshoot)
	gaze               float64 // horizontal eye offset, logo units
	glow               float64 // halo opacity
	zzz                float64 // Zzz-train envelope
	barA               float64 // bar opacity
	barFrac            float64 // bar fill fraction
	barPulse           float64 // completion flash
}

func waPoseAt(t float64) waPose {
	var p waPose
	p.fade = waSmooth(0, 0.35, t)
	p.col = waMixColor(waSleepGrey, waYellow, waSmooth(2.05, 2.55, t))

	// Base scale: sleeps large (1.30), settles to 1.0 while landing.
	breath := 0.022 * math.Sin(2*math.Pi*t/1.9-math.Pi/2) * (1 - waSmooth(1.75, 2.05, t))
	pop := 0.035 * math.Sin(math.Pi*waClamp01((t-2.18)/0.30)) // accent as the eyes open
	p.scale = (1.30 - 0.30*waEaseInOutCubic((t-4.08)/0.50)) * (1 + breath + pop)

	// Rotation: stir shake, then a head tilt that follows the gaze.
	if t > 1.80 && t < 2.30 {
		u := t - 1.80
		p.rot = 3.0 * math.Sin(2*math.Pi*3.4*u) * math.Exp(-6.5*u)
	}

	// Gaze: centre → left → right → centre.
	var pos float64
	switch {
	case t < 3.05:
		pos = 0
	case t < 3.25:
		pos = -waEaseInOutCubic((t - 3.05) / 0.20)
	case t < 3.50:
		pos = -1
	case t < 3.78:
		pos = -1 + 2*waEaseInOutCubic((t-3.50)/0.28)
	case t < 4.00:
		pos = 1
	case t < 4.20:
		pos = 1 - waEaseInOutCubic((t-4.00)/0.20)
	default:
		pos = 0
	}
	p.gaze = 2.0 * pos
	p.rot += 2.4 * pos

	// Eyes: closed slits while asleep, pop open with overshoot, then blinks.
	open := 0.07
	if t >= 2.18 {
		open = 0.07 + 0.93*waEaseOutBack((t-2.18)/0.26)
	}
	open *= waBlinkDip(t, 2.58, 2.76) * waBlinkDip(t, 2.80, 2.98) * waBlinkDip(t, 4.55, 4.72)
	p.eyeOpen = open

	// Halo: blooms as the cat wakes, relaxes to a faint idle glow.
	p.glow = 0.28*waSmooth(2.15, 2.55, t) - 0.18*waSmooth(2.90, 3.60, t)
	p.glow -= 0.06 * waSmooth(4.55, 4.95, t)
	if p.glow < 0 {
		p.glow = 0
	}
	p.glow *= p.fade

	p.zzz = p.fade * (1 - waSmooth(1.70, 2.00, t))

	// Squash & stretch landing bounce, fully decayed before the end.
	p.stretchX, p.stretchY = 1, 1
	if u := t - 4.32; u > 0 {
		sy := 0.085 * math.Sin(2*math.Pi*1.7*u) * math.Exp(-4.0*u) * (1 - waSmooth(4.86, 4.98, t))
		p.stretchY = 1 + sy
		p.stretchX = 1 - 0.55*sy
	}

	// Boot bar: fades in early, fills across the whole boot, flashes at 100%.
	p.barA = waSmooth(0.40, 0.75, t) * p.fade
	p.barFrac = waEaseInOutSine(waClamp01((t - 0.55) / (4.80 - 0.55)))
	p.barPulse = math.Sin(math.Pi * waClamp01((t-4.82)/0.18))
	return p
}

// waBlinkDip multiplies the eye opening down to ~0.06 and back across [a, b].
func waBlinkDip(t, a, b float64) float64 {
	if t <= a || t >= b {
		return 1
	}
	return 1 - 0.94*math.Sin(math.Pi*(t-a)/(b-a))
}

// ---------------------------------------------------------------------------
// SVG frame generation

// waSpark positions are relative to the logo centre.
var waSparks = []struct{ dx, dy, r, t0 float64 }{
	{0, -53.5, 4.6, 2.30},
	{42.5, -29.5, 3.4, 2.50},
	{-40.5, -24.5, 3.9, 2.68},
	{37.5, 29.5, 3.2, 2.86},
	{-33.5, 33.5, 3.6, 3.04},
}

// welcomeAnimSVG builds the full-screen SVG document for time t (seconds).
func welcomeAnimSVG(t float64, width, height int) []byte {
	l := waComputeLayout(width, height)
	p := waPoseAt(t)
	restBottom := l.logoY + waLogoH

	var b bytes.Buffer
	b.Grow(4096)
	fmt.Fprintf(&b, `<svg width="%d" height="%d" viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg">`, width, height, width, height)
	fmt.Fprintf(&b, `<rect x="0" y="0" width="%d" height="%d" fill="#000000"/>`, width, height)

	// Waking halo behind the box.
	if p.glow > 0.005 {
		fmt.Fprintf(&b, `<defs><radialGradient id="wg" gradientUnits="userSpaceOnUse" cx="%.2f" cy="%.2f" r="70">`+
			`<stop offset="0" stop-color="#FDE021" stop-opacity="%.3f"/>`+
			`<stop offset="0.55" stop-color="#FDE021" stop-opacity="%.3f"/>`+
			`<stop offset="1" stop-color="#FDE021" stop-opacity="0"/></radialGradient></defs>`,
			l.cx, l.cy, p.glow, p.glow*0.35)
		fmt.Fprintf(&b, `<circle cx="%.2f" cy="%.2f" r="70" fill="url(#wg)"/>`, l.cx, l.cy)
	}

	// Logo transform: local 59x71 → screen, with scale, tilt and bounce.
	logoM := waMatID().
		scaleAbout(p.stretchX, p.stretchY, l.cx, restBottom).
		rotateAbout(p.rot, l.cx, l.cy).
		scaleAbout(p.scale, p.scale, l.cx, l.cy).
		translate(l.cx-waLogoW/2, l.cy-waLogoH/2)

	fmt.Fprintf(&b, `<path d="%s" fill="%s" fill-rule="evenodd" fill-opacity="%.3f" %s/>`,
		waBoxPath, p.col, p.fade, logoM.attr())

	// Eyes: gaze shift plus vertical blink scale about each eye centre.
	eyeL := logoM.translate(p.gaze, 0).scaleAbout(1, p.eyeOpen, waEyeLCx, waEyeCy)
	eyeR := logoM.translate(p.gaze, 0).scaleAbout(1, p.eyeOpen, waEyeRCx, waEyeCy)
	fmt.Fprintf(&b, `<path d="%s" fill="%s" fill-opacity="%.3f" %s/>`, waEyeLPath, p.col, p.fade, eyeL.attr())
	fmt.Fprintf(&b, `<path d="%s" fill="#000000" fill-opacity="%.3f" %s/>`, waEyeRPath, p.fade, eyeR.attr())

	// Zzz train drifting up from the box while asleep.
	if p.zzz > 0.01 {
		zc := waHex(waZzzGrey)
		for i := 0; i < 3; i++ {
			ph := math.Mod(t/1.25+float64(i)*0.36, 1)
			size := 6.0 + 1.9*float64(i)
			x := l.cx + 21 + 9*ph + 2.6*math.Sin(2*math.Pi*1.1*ph+float64(i)*2.0)
			y := l.cy - 42 - 30*ph - 3*float64(i)
			op := p.zzz * math.Sin(math.Pi*ph)
			if op < 0.01 {
				continue
			}
			m := waMatID().translate(x, y).rotateAbout(14*math.Sin(2*math.Pi*ph+float64(i)*1.7), 0, 0).scaleAbout(size/2, size/2, 0, 0)
			fmt.Fprintf(&b, `<path d="M-1 -1 L1 -1 L-1 1 L1 1" fill="none" stroke="%s" stroke-opacity="%.3f" stroke-width="%.3f" stroke-linecap="round" stroke-linejoin="round" %s/>`,
				zc, op, 3.4/size, m.attr())
		}
	}

	// Sparkles popping around the box as it wakes.
	for _, s := range waSparks {
		u := (t - s.t0) / 0.55
		if u <= 0 || u >= 1 {
			continue
		}
		env := math.Sin(math.Pi * u)
		r := s.r * 1.45 * env
		m := waMatID().translate(l.cx+s.dx, l.cy+s.dy).rotateAbout(15+40*u, 0, 0).scaleAbout(r, r, 0, 0)
		col := waMixColor(waWarmWhite, waYellow, 0.1)
		fmt.Fprintf(&b, `<path d="M0 -1 L0.5 0 L0 1 L-0.5 0 Z" fill="%s" fill-opacity="%.3f" %s/>`, col, env, m.attr())
		m2 := m.rotateAbout(90, 0, 0).scaleAbout(0.7, 0.7, 0, 0)
		fmt.Fprintf(&b, `<path d="M0 -1 L0.5 0 L0 1 L-0.5 0 Z" fill="%s" fill-opacity="%.3f" %s/>`, col, env, m2.attr())
	}

	// Boot bar.
	if p.barA > 0.01 {
		fmt.Fprintf(&b, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="5" ry="5" fill="%s" fill-opacity="%.3f"/>`,
			l.barX, l.barY, l.barW, l.barH, waHex(waBarGrey), p.barA)
		fw := l.barW * p.barFrac
		if fw >= 1 {
			rx := 5.0
			if fw < 2*rx {
				rx = fw / 2
			}
			fillCol := waMixColor(waYellow, waWarmWhite, p.barPulse)
			fmt.Fprintf(&b, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="%.2f" ry="%.2f" fill="%s" fill-opacity="%.3f"/>`,
				l.barX, l.barY, fw, l.barH, rx, rx, fillCol, p.barA)
		}
	}

	b.WriteString(`</svg>`)
	return b.Bytes()
}

// ---------------------------------------------------------------------------
// rasterization

// welcomeAnimFrameInto rasterizes the animation at time t into buf (allocated
// when nil or wrongly sized) and returns the frame ready for sendFull. The
// opaque background rect covers every pixel, so a reused buffer never needs
// clearing. Callers own their buffers; the function keeps no state, which
// lets showWelcome double-buffer renders against the SPI push.
func welcomeAnimFrameInto(buf *image.RGBA, t float64, width, height int) (*image.RGBA, error) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(welcomeAnimSVG(t, width, height)))
	if err != nil {
		return nil, err
	}
	if buf == nil || buf.Bounds().Dx() != width || buf.Bounds().Dy() != height {
		buf = image.NewRGBA(image.Rect(0, 0, width, height))
	}
	icon.SetTarget(0, 0, float64(width), float64(height))
	scanner := rasterx.NewScannerGV(width, height, buf, buf.Bounds())
	dasher := rasterx.NewDasher(width, height, scanner)
	icon.Draw(dasher, 1.0)
	return buf, nil
}
