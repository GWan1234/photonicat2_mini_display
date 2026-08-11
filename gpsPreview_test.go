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
		globalData.Store("GpsAccuracy", "+-3")
		globalData.Store("GpsSats", "11/18")
		globalData.Store("GpsFix", "3D")
		globalData.Store("GpsLat", "31.2304° N")
		globalData.Store("GpsLon", "121.4737° E")
	case "north":
		globalData.Store("GpsSpeed", "8")
		globalData.Store("GpsCourse", "3° N")
		globalData.Store("GpsCourseDeg", 3.0)
		globalData.Store("GpsAlt", "1284")
		globalData.Store("GpsAccuracy", "+-12")
		globalData.Store("GpsSats", "11/18")
		globalData.Store("GpsFix", "3D")
		globalData.Store("GpsLat", "31.2304° N")
		globalData.Store("GpsLon", "121.4737° E")
	default:
		globalData.Store("GpsSpeed", "32")
		globalData.Store("GpsCourse", "132° SE")
		globalData.Store("GpsCourseDeg", 132.0)
		globalData.Store("GpsAlt", "412")
		globalData.Store("GpsAccuracy", "+-4")
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

// The speed readout plus its trailing unit must fit the panel at the widest
// value the layout has to render. This is not hypothetical: at "188" the
// colossal digits measure 128px and "km/h" another 42px, which from x=10 ran
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
				end := el.Position.X +
					font.MeasureString(face, "188").Round() + 1 +
					font.MeasureString(unitFace, el.Units).Round()
				if end > PCAT2_LCD_WIDTH {
					t.Errorf("%s %s: speed %q + unit %q ends at x=%d, past the %dpx panel",
						name, page, "188", el.Units, end, PCAT2_LCD_WIDTH)
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
