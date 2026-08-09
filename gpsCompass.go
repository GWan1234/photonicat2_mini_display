package main

// PUBG/Squad-style heading tape for the GPS page.
//
// Instead of printing "Heading: 132° SE" as text, the compass draws a strip of
// the bearing circle centered on the current course: the tape slides under a
// fixed center pointer as the device turns, so the heading is read the way it
// is in a game HUD — cardinal letters and tick marks moving past a caret. The
// numeric bearing breaks into the baseline itself (the line parts around the
// number) so the exact value is still available without a label or extra row.
//
// Rendering is deliberately plain pixel work (drawLine/fillRect/drawText) to
// match drawCpuBars and drawPowerGraph; no rasterizer state is kept between
// frames, so the 1 Hz GPS refresh redraws it from scratch.

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"golang.org/x/image/font"
)

// compassDegPerPx sets the tape scale: how many bearing degrees one horizontal
// pixel covers. At 0.75 a 172px-wide screen shows ~129° of arc, so two cardinal
// points (45° apart) are always visible and motion is legible without the tape
// sliding faster than the eye can track.
const compassDegPerPx = 0.75

// compassLabels are the 8 cardinal/intercardinal points drawn on the tape.
var compassLabels = [8]string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}

// normalizeDeg folds an arbitrary bearing into [0,360).
//
// Course values arrive from a modem via JSON and are not trusted: NaN/Inf would
// poison every subsequent pixel calculation (and math.Mod propagates NaN), so
// they collapse to 0 rather than producing out-of-bounds draw coordinates.
func normalizeDeg(deg float64) float64 {
	if math.IsNaN(deg) || math.IsInf(deg, 0) {
		return 0
	}
	deg = math.Mod(deg, 360)
	if deg < 0 {
		deg += 360
	}
	return deg
}

// compassDelta returns the signed shortest angular distance from `from` to `to`,
// in [-180,180]. This is what decides whether a tick sits left or right of the
// caret: without the wrap correction a heading of 350° would place the 10° tick
// 340 pixels off-screen to the left instead of a few pixels to the right.
//
// The exact-antipode case (|d| == 180) is a genuine tie — that bearing is
// equally far in both directions and lands off the visible tape either way, so
// which sign comes back does not matter.
func compassDelta(from, to float64) float64 {
	d := math.Mod(to-from+540, 360) - 180
	return d
}

// drawGpsCompass renders the heading tape into the box at (x0,y0) sized w×h.
//
// Layout inside the box, top to bottom: the caret, the tick/label tape, and the
// numeric bearing. `course` is the current heading in degrees; hasFix false
// draws the tape greyed at north with the bearing replaced by "--", so the
// widget keeps its footprint (no layout jump) while the fix is pending.
func drawGpsCompass(frame *image.RGBA, x0, y0, w, h int, course float64, hasFix bool, fill color.RGBA) {
	if frame == nil || w <= 0 || h <= 0 {
		return
	}

	tapeColor := fill
	dim := color.RGBA{
		R: uint8(int(fill.R) * 45 / 100),
		G: uint8(int(fill.G) * 45 / 100),
		B: uint8(int(fill.B) * 45 / 100),
		A: 255,
	}
	if !hasFix {
		tapeColor = PCAT_GREY
	}

	course = normalizeDeg(course)
	centerX := x0 + w/2

	// Vertical bands, top to bottom: cardinal letters, tick marks, baseline.
	// The caret sits over the letters (game HUDs put the pointer above the
	// tape, not below it) so the letter it is selecting is never occluded.
	// The numeric bearing no longer gets its own row: it breaks INTO the
	// baseline, HUD-style, sitting in a gap at the center of the line.
	caretY := y0
	labelY := y0 + 7   // top of the cardinal letter row
	tickTop := y0 + 21 // ticks hang below the letters
	tickH := 9
	baselineY := tickTop + tickH

	// Caret: a filled triangle pointing down at the tape, drawn as shrinking
	// horizontal runs so it stays crisp at this size (a rasterized SVG would
	// soften the apex to grey at 5px tall).
	for i := 0; i < 6; i++ {
		runW := 11 - 2*i
		if runW <= 0 {
			break
		}
		fillRect(frame, centerX-runW/2, caretY+i, runW, 1, tapeColor)
	}

	// Numeric bearing, break-the-line style: the number sits inside a gap in
	// the baseline, vertically centered on the line, so the line appears to
	// pass through the text. Twice the size of the old bottom-row readout
	// (which fit "micro" at 10px): "clock" is the 20px face, with smaller
	// fallbacks so a shorter box degrades instead of overflowing.
	bearingText := "--"
	if hasFix {
		bearingText = fmt.Sprintf("%03d°", int(math.Round(course))%360)
	}
	var bearFace font.Face
	bearTop, gapHalf := 0, 0
	for _, name := range []string{"clock", "reg", "unit", "tiny", "micro"} {
		face, _, err := getFontFace(name)
		if err != nil {
			continue
		}
		m := face.Metrics()
		fh := (m.Ascent + m.Descent).Round()
		// Digits occupy [baseline-capHeight, baseline] — the descent is empty
		// for numerals — so centering the line box on the tape line leaves the
		// glyphs hanging below it. Place the text baseline capHeight/2 under
		// the tape line instead: the visual middle of the digits lands on it.
		cap := m.CapHeight.Round()
		if cap <= 0 {
			cap = m.Ascent.Round() * 7 / 10
		}
		top := baselineY + cap/2 - m.Ascent.Round()
		if top >= y0 && top+fh <= y0+h {
			bearFace = face
			bearTop = top
			gapHalf = font.MeasureString(face, bearingText).Round()/2 + 6
			break
		}
	}

	// Baseline under the tape gives the ticks something to hang from; drawn
	// in two segments so the bearing text sits in a real gap, not on top of
	// the line.
	if bearFace != nil {
		leftW := centerX - gapHalf - x0
		if leftW > 0 {
			fillRect(frame, x0, baselineY, leftW, 1, dim)
		}
		rightX := centerX + gapHalf
		if rightW := x0 + w - rightX; rightW > 0 {
			fillRect(frame, rightX, baselineY, rightW, 1, dim)
		}
		drawText(frame, bearingText, centerX, bearTop, bearFace, tapeColor, true)
	} else {
		fillRect(frame, x0, baselineY, w, 1, dim)
	}

	// Ticks every 15°, cardinal labels every 45°. Sweeping a fixed window of
	// offsets around the current course (rather than all 360°) keeps the work
	// bounded and guarantees the wrap at N is handled by compassDelta.
	halfSpanDeg := float64(w) / 2 * compassDegPerPx
	startTick := int(math.Floor((course-halfSpanDeg)/15)) * 15
	endTick := int(math.Ceil((course + halfSpanDeg) / 15))*15 + 15

	faceTape, _, errTape := getFontFace("tiny")

	for t := startTick; t <= endTick; t += 15 {
		bearing := normalizeDeg(float64(t))
		px := centerX + int(math.Round(compassDelta(course, bearing)/compassDegPerPx))
		if px < x0 || px >= x0+w {
			continue
		}

		// Ticks vanish inside the bearing gap (the letters above it stay: the
		// number is below the letter row, so the cardinal under the caret
		// keeps its label while its tick yields to the text).
		inGap := bearFace != nil && px > centerX-gapHalf-1 && px < centerX+gapHalf+1

		isCardinal := int(math.Round(bearing))%45 == 0
		if isCardinal {
			// Cardinal: tall tick plus its letter. N is drawn in the fill color
			// at full strength so it reads as the anchor point of the circle.
			labelIdx := (int(math.Round(bearing)) / 45) % 8
			label := compassLabels[labelIdx]
			letterClr := tapeColor
			if label != "N" {
				letterClr = dim
				if hasFix {
					letterClr = color.RGBA{
						R: uint8(int(fill.R) * 78 / 100),
						G: uint8(int(fill.G) * 78 / 100),
						B: uint8(int(fill.B) * 78 / 100),
						A: 255,
					}
				}
			}
			if !inGap {
				fillRect(frame, px, tickTop, 1, tickH, letterClr)
			}
			if errTape == nil {
				drawText(frame, label, px, labelY, faceTape, letterClr, true)
			}
		} else if !inGap {
			// Minor tick: short stub, dimmed so the cardinals dominate.
			fillRect(frame, px, baselineY-4, 1, 4, dim)
		}
	}
}
