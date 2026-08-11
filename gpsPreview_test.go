package main

// Renders the GPS page to a PNG so the layout can be eyeballed without
// flashing the device. Skipped unless GPS_PREVIEW=1 is set, so it never
// slows the normal suite or writes files in CI.
//
//   GPS_PREVIEW=1 go test -run TestGpsPagePreview ./...

import (
	"image"
	"image/color"
	"os"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/image/font"
)

// gpsPreviewFonts installs the real font table against the repo's assets dir,
// so a font added to buildFontTable is available here without a second edit.
func gpsPreviewFonts() {
	fonts = buildFontTable(".")
}

func TestGpsPagePreview(t *testing.T) {
	if os.Getenv("GPS_PREVIEW") != "1" {
		t.Skip("set GPS_PREVIEW=1 to render the GPS page preview")
	}
	gpsPreviewFonts()
	assetsPrefix = "."

	src := os.Getenv("GPS_PREVIEW_CONFIG")
	if src == "" {
		src = "config.json"
	}
	c, err := loadConfig(src)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	// GPS_PREVIEW_STATE picks the scenario: the default themed fix, a
	// north-wrap heading (the tape's tricky case), or no fix at all.
	switch os.Getenv("GPS_PREVIEW_STATE") {
	case "nofix":
		globalData.Store("GpsSpeed", "--")
		globalData.Store("GpsCourse", "--")
		globalData.Delete("GpsCourseDeg")
		globalData.Store("GpsAlt", "--")
		globalData.Store("GpsAccuracy", "--")
		globalData.Store("GpsSats", "0/3")
		globalData.Store("GpsFix", "No Fix")
		globalData.Store("GpsLat", "-")
		globalData.Store("GpsLon", "-")
	case "fast":
		// Widest realistic case: three speed digits and four altitude digits
		// at the page's largest fonts.
		globalData.Store("GpsSpeed", "188")
		globalData.Store("GpsCourse", "271° W")
		globalData.Store("GpsCourseDeg", 271.0)
		globalData.Store("GpsAlt", "1284")
		globalData.Store("GpsAccuracy", "±3")
		globalData.Store("GpsSats", "11/18")
		globalData.Store("GpsFix", "3D")
		globalData.Store("GpsLat", "31.2304° N")
		globalData.Store("GpsLon", "121.4737° E")
	case "north":
		globalData.Store("GpsSpeed", "8")
		globalData.Store("GpsCourse", "3° N")
		globalData.Store("GpsCourseDeg", 3.0)
		globalData.Store("GpsAlt", "1284")
		globalData.Store("GpsAccuracy", "±12")
		globalData.Store("GpsSats", "11/18")
		globalData.Store("GpsFix", "3D")
		globalData.Store("GpsLat", "31.2304° N")
		globalData.Store("GpsLon", "121.4737° E")
	default:
		globalData.Store("GpsSpeed", "32")
		globalData.Store("GpsCourse", "132° SE")
		globalData.Store("GpsCourseDeg", 132.0)
		globalData.Store("GpsAlt", "412")
		globalData.Store("GpsAccuracy", "±4")
		globalData.Store("GpsSats", "7/12")
		globalData.Store("GpsFix", "3D")
		globalData.Store("GpsLat", "31.2304° N")
		globalData.Store("GpsLon", "121.4737° E")
	}

	frame := image.NewRGBA(image.Rect(0, 0, PCAT2_LCD_WIDTH, middleFrameHeight))
	for y := frame.Bounds().Min.Y; y < frame.Bounds().Max.Y; y++ {
		for x := frame.Bounds().Min.X; x < frame.Bounds().Max.X; x++ {
			frame.Set(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	renderMiddle(frame, &c, false, 4)
	out := os.Getenv("GPS_PREVIEW_OUT")
	if out == "" {
		out = "/tmp/gps_preview.png"
	}
	saveFrameToPng(frame, out)
}

// "km/h" sits on its own line under the digits, and the gap between them is
// the layout's units_below, in real rendered pixels. Trailing units land 1px
// past the value's advance width, which at 62px parked the 12px unit inside
// the digits' own bottom-right corner. This renders the page and measures the
// blank rows between the two inks, because that is the thing being promised —
// the arithmetic behind it (ink boxes, 26.6 rounding, the baseline clamp)
// has more than one place to lose a pixel.
func TestGpsSpeedUnitStacksBelow(t *testing.T) {
	fonts = buildFontTable(".")
	assetsPrefix = "."

	c, err := loadConfig("config.json")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	pageIdx, el := gpsSpeedElement(t, &c)
	if el.UnitsBelow <= 0 {
		t.Fatalf("speed element is not stacked: units_below=%d", el.UnitsBelow)
	}

	// The band the speed block owns: below the compass tape, above the first
	// icon row. Ink outside it belongs to other elements.
	const bandTop, bandBottom = 52, 124

	// "--" is the no-fix placeholder: all mid-height ink, no baseline contact.
	// It must not drag the unit's line up the panel — the line would then jump
	// the moment a fix turns the dashes into digits. So the 5px is measured
	// against the digits, and the dashes only have to leave the unit put.
	unitLine := map[string]inkRow{}

	for _, speed := range []string{"32", "188", "--"} {
		globalData.Store("GpsSpeed", speed)

		frame := image.NewRGBA(image.Rect(0, 0, PCAT2_LCD_WIDTH, middleFrameHeight))
		renderMiddle(frame, &c, false, pageIdx)

		runs := inkRows(frame, bandTop, bandBottom)
		if len(runs) != 2 {
			t.Fatalf("speed %q: want 2 lines of ink (value, unit), got %d: %v", speed, len(runs), runs)
		}
		value, unit := runs[0], runs[1]
		unitLine[speed] = unit

		// Antialiasing puts a fractional row at each ink edge, so the measured
		// gap is allowed to land a pixel either side of the configured one.
		if speed != "--" {
			if gap := unit.top - value.bottom - 1; gap < el.UnitsBelow-1 || gap > el.UnitsBelow+1 {
				t.Errorf("speed %q: %dpx between digits (end y=%d) and unit (start y=%d), want %dpx",
					speed, gap, value.bottom, unit.top, el.UnitsBelow)
			}
		}
		// The block has to stay inside its band, or it collides with the
		// compass tape above or the altitude row below instead.
		if value.top <= bandTop {
			t.Errorf("speed %q: digits reach y=%d, up into the compass tape", speed, value.top)
		}
		if unit.bottom >= bandBottom-1 {
			t.Errorf("speed %q: unit reaches y=%d, down into the altitude row", speed, unit.bottom)
		}
		// Both lines are centered on the same axis. Advance widths include the
		// glyphs' side bearings ("1" carries a lot), so the ink can sit a
		// couple of px off the true centre — but not more, and never in
		// opposite directions.
		for what, line := range map[string]inkRow{"value": value, "unit": unit} {
			box := inkBounds(frame, 0, PCAT2_LCD_WIDTH, line.top, line.bottom+1)
			mid := (box.Min.X + box.Max.X) / 2
			if mid < el.Position.X-3 || mid > el.Position.X+3 {
				t.Errorf("speed %q: %s ink centres on x=%d, want the panel's centre line x=%d",
					speed, what, mid, el.Position.X)
			}
		}
	}

	for speed, line := range unitLine {
		if line != unitLine["32"] {
			t.Errorf("speed %q: unit line at rows %d-%d, but %q puts it at %d-%d — it must not move",
				speed, line.top, line.bottom, "32", unitLine["32"].top, unitLine["32"].bottom)
		}
	}
}

// The accuracy readout says "±4m", and Orbitron has no U+00B1 in any weight —
// font.Drawer would silently draw the notdef box, which on this panel is a
// convincing little tofu rectangle rather than an obvious blank. drawText
// composes the sign from the face's own "+" and "-" instead; this checks the
// result really is a plus stacked over a bar (two bands of ink with a clear
// row between them, not one solid outline) and that it stays inside the
// advance the caller measured for it.
func TestPlusMinusIsComposedNotTofu(t *testing.T) {
	fonts = buildFontTable(".")

	face, _, err := getFontFace("reg")
	if err != nil {
		t.Fatalf("getFontFace: %v", err)
	}

	const x0, y0 = 4, 4
	frame := image.NewRGBA(image.Rect(0, 0, PCAT2_LCD_WIDTH, 40))
	finishX, _ := drawText(frame, string(plusMinus), x0, y0, face, color.RGBA{255, 255, 255, 255}, false)

	bands := inkRows(frame, 0, 40)
	if len(bands) != 2 {
		t.Fatalf("want 2 bands of ink (plus, then bar), got %d: %v — a single band is the tofu box",
			len(bands), bands)
	}
	plus := inkBounds(frame, 0, PCAT2_LCD_WIDTH, bands[0].top, bands[0].bottom+1)
	bar := inkBounds(frame, 0, PCAT2_LCD_WIDTH, bands[1].top, bands[1].bottom+1)

	// The bar carries the sign's full width; the plus is at most a pixel wider
	// or narrower, since both come from the same face at the same size.
	if d := (bar.Max.X - bar.Min.X) - (plus.Max.X - plus.Min.X); d < -1 || d > 1 {
		t.Errorf("bar is %dpx wide, plus is %dpx — they should match",
			bar.Max.X-bar.Min.X, plus.Max.X-plus.Min.X)
	}
	if bar.Min.X < plus.Min.X-1 || bar.Min.X > plus.Min.X+1 {
		t.Errorf("bar starts at x=%d, plus at x=%d — they should line up", bar.Min.X, plus.Min.X)
	}
	// Composing must not spill outside the cell the caller measured, or every
	// centering and overflow calculation upstream is off by the difference.
	if plus.Min.X < x0 || bar.Min.X < x0 || plus.Max.X > finishX || bar.Max.X > finishX {
		t.Errorf("sign spans x=%d..%d, outside the measured cell x=%d..%d",
			min(plus.Min.X, bar.Min.X), max(plus.Max.X, bar.Max.X), x0, finishX)
	}
}

// inkRow is one horizontal band of lit rows: top and bottom are inclusive.
type inkRow struct{ top, bottom int }

// inkRows splits [y0,y1) into the bands of rows that have any ink in them.
func inkRows(frame *image.RGBA, y0, y1 int) []inkRow {
	var runs []inkRow
	open := false
	for y := y0; y < y1; y++ {
		lit := !inkBounds(frame, 0, PCAT2_LCD_WIDTH, y, y+1).Empty()
		switch {
		case lit && !open:
			runs = append(runs, inkRow{top: y, bottom: y})
			open = true
		case lit:
			runs[len(runs)-1].bottom = y
		default:
			open = false
		}
	}
	return runs
}

// gpsSpeedElement returns the GPS page's index and its speed element.
func gpsSpeedElement(t *testing.T, c *Config) (int, DisplayElement) {
	t.Helper()
	for page, els := range c.DisplayTemplate.Elements {
		for _, el := range els {
			if el.DataKey != "GpsSpeed" {
				continue
			}
			idx, err := strconv.Atoi(strings.TrimPrefix(page, "page"))
			if err != nil {
				t.Fatalf("page key %q: %v", page, err)
			}
			return idx, el
		}
	}
	t.Fatal("no GpsSpeed element in config.json")
	return 0, DisplayElement{}
}

// inkBounds is the bounding box of the lit pixels inside the given window.
func inkBounds(frame *image.RGBA, x0, x1, y0, y1 int) image.Rectangle {
	box := image.Rectangle{}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, _ := frame.At(x, y).RGBA()
			if (r+g+b)>>8 < 90 {
				continue
			}
			p := image.Rect(x, y, x+1, y+1)
			if box.Empty() {
				box = p
			} else {
				box = box.Union(p)
			}
		}
	}
	return box
}

// The speed readout and its unit must both fit the panel at the widest value
// the layout has to render. This is not hypothetical: at "188" the colossal
// digits measure 128px and a trailing "km/h" another 42px, which from x=10 ran
// 9px off the 172px panel and clipped the unit to "km/". The draw path's
// overflow check only measures the value, so nothing else catches this.
func TestGpsSpeedPlusUnitFitsPanel(t *testing.T) {
	fonts = buildFontTable(".")

	for _, name := range []string{"config.json", "config_debian.json"} {
		c, err := loadConfig(name)
		if err != nil {
			t.Fatalf("loadConfig(%s): %v", name, err)
		}
		for page, els := range c.DisplayTemplate.Elements {
			for _, el := range els {
				if el.DataKey != "GpsSpeed" {
					continue
				}
				face, _, err := getFontFaceForText(el.Font, "188")
				if err != nil {
					t.Fatalf("%s %s: font %q: %v", name, page, el.Font, err)
				}
				unitFace, _, err := getFontFace(el.UnitsFont)
				if err != nil {
					t.Fatalf("%s %s: units font %q: %v", name, page, el.UnitsFont, err)
				}
				// "188" is the widest speed the page renders: three digits, and
				// collectGpsData formats speed with no decimal point.
				valueW := font.MeasureString(face, "188").Round()
				unitW := font.MeasureString(unitFace, el.Units).Round()

				// Where each line actually starts: Position.X is the centre
				// when the element is centered, the left edge otherwise, and a
				// trailing unit hangs 1px off the end of the value.
				valueX := el.Position.X
				unitX := el.Position.X
				if el.UnitsBelow == 0 {
					unitX = valueX + valueW + 1
				}
				if el.Align == "center" {
					valueX -= valueW / 2
					if el.UnitsBelow > 0 {
						unitX -= unitW / 2
					}
				}

				for what, span := range map[string][2]int{
					"value": {valueX, valueX + valueW},
					"unit":  {unitX, unitX + unitW},
				} {
					if span[0] < 0 || span[1] > PCAT2_LCD_WIDTH {
						t.Errorf("%s %s: speed %q %s spans x=%d..%d, outside the %dpx panel",
							name, page, "188", what, span[0], span[1], PCAT2_LCD_WIDTH)
					}
				}
			}
		}
	}
}

// Every "font"/"units_font" a shipped config names must exist in the font
// table. A missing name is silent: getFontFace errors, the draw case logs and
// `continue`s, and the element just does not appear — which is exactly how the
// speed readout vanished when "colossal" was added to one table but not the
// other.
func TestConfigFontsAllRegistered(t *testing.T) {
	table := buildFontTable(".")

	for _, name := range []string{"config.json", "config_debian.json"} {
		c, err := loadConfig(name)
		if err != nil {
			t.Fatalf("loadConfig(%s): %v", name, err)
		}
		for page, els := range c.DisplayTemplate.Elements {
			for i, el := range els {
				for field, want := range map[string]string{
					"font":       el.Font,
					"units_font": el.UnitsFont,
				} {
					// Only text-bearing elements need a font; blank means
					// "not applicable" (icons, graphs, bars).
					if want == "" {
						continue
					}
					if _, ok := table[want]; !ok {
						t.Errorf("%s %s[%d] %s=%q is not in the font table",
							name, page, i, field, want)
					}
				}
			}
		}
	}
}
