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
)

func gpsPreviewFonts() {
	fonts = map[string]FontConfig{
		"clock":     {FontPath: "./assets/fonts/Orbitron-Medium.ttf", FontSize: 20},
		"clockBold": {FontPath: "./assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 17},
		"reg":       {FontPath: "./assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 18},
		"big":       {FontPath: "./assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 25},
		"unit":      {FontPath: "./assets/fonts/Orbitron-Medium.ttf", FontSize: 15},
		"tiny":      {FontPath: "./assets/fonts/Orbitron-Regular.ttf", FontSize: 12},
		"micro":     {FontPath: "./assets/fonts/Orbitron-Regular.ttf", FontSize: 10},
		"thin":      {FontPath: "./assets/fonts/Orbitron-Regular.ttf", FontSize: 18},
		"huge":      {FontPath: "./assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 34},
		"gigantic":  {FontPath: "./assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 48},
		"unit_cjk":  {FontPath: "./assets/fonts/NotoSansMonoCJK-VF.ttf.ttc", FontSize: 15},
	}
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
	case "north":
		globalData.Store("GpsSpeed", "8.4")
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
