package main

// drawCov2_test.go — coverage for draw.go's composed surfaces: the top bar,
// middle page renderer, footer, battery glyph, bar charts, the send* pipeline
// and the boot/shutdown screens. The display handle is a zero gc9307.Device:
// its Size() is 0x0, so the driver rejects every transfer with a bounds error
// before touching SPI/GPIO — no hardware is ever involved.

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gc9307 "github.com/photonicat/periph.io-gc9307"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
)

// drawCovZeroDisplay is a hardware-free display handle (see file comment).
func drawCovZeroDisplay() gc9307.Device { return gc9307.Device{} }

// drawCovSaveSendState snapshots the globals the send*/render* paths mutate
// and restores them on cleanup, keeping the suite order-independent.
func drawCovSaveSendState(t *testing.T) {
	t.Helper()
	oldWrapper := displayWrapper
	oldTop, oldMid, oldFoot := webLastTop, webLastMiddle, webLastFooter
	webSnapMu.Lock()
	oldSnap, oldCompose := webSnapshot, webSnapCompose
	webSnapMu.Unlock()
	oldLast := lastMiddleFrame
	oldTopStr, oldTopFrame := cacheTopBarStr, cacheTopBar
	oldFootStr, oldFootFrame := cacheFooterStr, cacheFooter
	t.Cleanup(func() {
		displayWrapper = oldWrapper
		webLastTop, webLastMiddle, webLastFooter = oldTop, oldMid, oldFoot
		webSnapMu.Lock()
		webSnapshot, webSnapCompose = oldSnap, oldCompose
		webSnapMu.Unlock()
		lastMiddleFrame = oldLast
		cacheTopBarStr, cacheTopBar = oldTopStr, oldTopFrame
		cacheFooterStr, cacheFooter = oldFootStr, oldFootFrame
	})
}

// drawCovNoFonts empties the font table and face cache for one test so the
// font-load error paths run, restoring both maps afterwards.
func drawCovNoFonts(t *testing.T) {
	t.Helper()
	oldFonts := fonts
	fontCacheMu.Lock()
	oldCache := fontCache
	fontCache = make(map[string]struct {
		face       font.Face
		fontHeight int
	})
	fontCacheMu.Unlock()
	fonts = map[string]FontConfig{}
	t.Cleanup(func() {
		fonts = oldFonts
		fontCacheMu.Lock()
		fontCache = oldCache
		fontCacheMu.Unlock()
	})
}

func TestDrawCovSendFunctions(t *testing.T) {
	drawCovSaveSendState(t)
	display := drawCovZeroDisplay()
	frame := image.NewRGBA(image.Rect(0, 0, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT))
	fillRect(frame, 0, 0, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT, color.RGBA{9, 9, 9, 255})
	empty := image.NewRGBA(image.Rect(0, 0, 0, 0))

	for _, wrapped := range []bool{false, true} {
		if wrapped {
			displayWrapper = NewDisplayWrapper(display)
		} else {
			displayWrapper = nil
		}
		sendTopBar(display, nil)
		sendTopBar(display, empty)
		sendTopBar(display, frame)

		sendFooter(display, nil)
		sendFooter(display, empty)
		sendFooter(display, frame)

		sendMiddle(display, nil)
		sendMiddle(display, empty)
		sendMiddle(display, frame)

		sendFull(display, nil)
		sendFull(display, empty)
		sendFull(display, frame)

		// Partial: transparent frame → nothing to send; content → crop+send.
		blank := image.NewRGBA(image.Rect(0, 0, 40, 40))
		sendMiddlePartial(display, blank)
		content := image.NewRGBA(image.Rect(0, 0, 40, 40))
		fillRect(content, 10, 10, 5, 5, color.RGBA{255, 255, 255, 255})
		sendMiddlePartial(display, content)
	}
}

func TestDrawCovSendMiddleOptimized(t *testing.T) {
	drawCovSaveSendState(t)
	display := drawCovZeroDisplay()
	displayWrapper = nil
	lastMiddleFrame = nil

	sendMiddleOptimized(display, nil)
	sendMiddleOptimized(display, image.NewRGBA(image.Rect(0, 0, 0, 0)))

	f1 := image.NewRGBA(image.Rect(0, 0, 30, 20))
	fillRect(f1, 0, 0, 30, 20, color.RGBA{1, 2, 3, 255})
	sendMiddleOptimized(display, f1)
	if lastMiddleFrame == nil {
		t.Fatal("first send did not store the comparison frame")
	}

	// Identical frame → skip branch (stored copy stays byte-equal).
	f2 := image.NewRGBA(image.Rect(0, 0, 30, 20))
	copy(f2.Pix, f1.Pix)
	sendMiddleOptimized(display, f2)

	// Changed pixel → send again.
	f2.SetRGBA(5, 5, color.RGBA{200, 0, 0, 255})
	sendMiddleOptimized(display, f2)
	if lastMiddleFrame.RGBAAt(5, 5) != (color.RGBA{200, 0, 0, 255}) {
		t.Error("changed frame was not stored for the next comparison")
	}

	// Different bounds → the stored frame is reallocated.
	f3 := image.NewRGBA(image.Rect(0, 0, 10, 10))
	fillRect(f3, 0, 0, 10, 10, color.RGBA{7, 7, 7, 255})
	sendMiddleOptimized(display, f3)
	if !lastMiddleFrame.Bounds().Eq(f3.Bounds()) {
		t.Error("stored frame bounds not updated for a resized frame")
	}
}

func TestDrawCovTestClock(t *testing.T) {
	drawCovSetup(t)
	frame := image.NewRGBA(image.Rect(0, 0, 172, 64))
	testClock(frame)
	if !drawCovHasInk(frame) {
		t.Error("testClock drew nothing")
	}

	// Font table empty → error path.
	drawCovNoFonts(t)
	frame2 := image.NewRGBA(image.Rect(0, 0, 172, 64))
	testClock(frame2) // must not panic
}

// drawCovStashTopBarData installs a full set of top-bar globalData inputs.
func drawCovStashTopBarData(t *testing.T, egress, gateway, cellGen string, sigPct, soc int, charging bool) {
	t.Helper()
	drawCovStashGlobal(t, "ActiveEgress", egress)
	drawCovStashGlobal(t, "GatewayDevice", gateway)
	drawCovStashGlobal(t, "CellGeneration", cellGen)
	drawCovStashGlobal(t, "ModemSignalStrength", sigPct)
	drawCovStashGlobal(t, "BatterySoc", soc)
	drawCovStashGlobal(t, "BatteryCharging", charging)
}

func TestDrawCovRenderTopBar(t *testing.T) {
	drawCovSetup(t)
	drawCovSaveSendState(t)

	oldLoc := displayLoc.Load()
	t.Cleanup(func() { displayLoc.Store(oldLoc) })
	displayLoc.Store(time.UTC)

	frame := image.NewRGBA(image.Rect(0, 0, PCAT2_LCD_WIDTH, PCAT2_TOP_BAR_HEIGHT))

	cases := []struct {
		name                     string
		egress, gateway, cellGen string
		sig, soc                 int
		charging                 bool
	}{
		{"wifi_egress", "wifi", "", "", 50, 55, false},
		{"wan_egress", "wan", "", "", 50, 55, true},
		{"cellular_5g", "mobile", "", "5", 80, 15, true}, // low SOC + bolt
		{"cellular_unknown_gen", "mobile", "mobile", "", 30, 100, true},
		{"wired_gateway", "", "wired", "", 0, 55, false},
		{"no_wan", "", "", "", 0, 55, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			drawCovStashTopBarData(t, tc.egress, tc.gateway, tc.cellGen, tc.sig, tc.soc, tc.charging)
			cacheTopBarStr = ""
			if !renderTopBar(frame) {
				t.Fatal("first render should draw")
			}
			if !drawCovHasInk(frame) {
				t.Error("top bar drew nothing")
			}
			if renderTopBar(frame) {
				t.Error("identical state should hit the cache")
			}
		})
	}

	t.Run("unknown_timezone_placeholder", func(t *testing.T) {
		displayLoc.Store(nil)
		t.Cleanup(func() { displayLoc.Store(time.UTC) })
		drawCovStashTopBarData(t, "wan", "", "", 0, 40, false)
		cacheTopBarStr = ""
		if !renderTopBar(frame) {
			t.Fatal("render with unknown timezone should still draw")
		}
	})

	t.Run("drawTopBar_wrapper", func(t *testing.T) {
		drawCovStashTopBarData(t, "wan", "", "", 0, 42, false)
		cacheTopBarStr = ""
		drawTopBar(drawCovZeroDisplay(), frame) // renders + safely fails the SPI push
		drawTopBar(drawCovZeroDisplay(), frame) // cache-hit path: no send
	})

	t.Run("font_error", func(t *testing.T) {
		drawCovNoFonts(t)
		drawCovStashTopBarData(t, "wan", "", "", 0, 43, false)
		cacheTopBarStr = ""
		if renderTopBar(frame) {
			t.Error("missing fonts should abort the render")
		}
	})
}

// With fonts loaded but the assets directory empty, the top bar aborts on its
// network icon and the battery glyph silently drops its charging bolt.
func TestDrawCovRenderTopBarMissingAssets(t *testing.T) {
	drawCovSetup(t)
	drawCovSaveSendState(t)
	old := assetsPrefix
	assetsPrefix = t.TempDir()
	t.Cleanup(func() { assetsPrefix = old })

	oldLoc := displayLoc.Load()
	t.Cleanup(func() { displayLoc.Store(oldLoc) })
	displayLoc.Store(time.UTC)

	frame := image.NewRGBA(image.Rect(0, 0, PCAT2_LCD_WIDTH, PCAT2_TOP_BAR_HEIGHT))

	drawCovStashTopBarData(t, "wan", "", "", 0, 50, false)
	cacheTopBarStr = ""
	if renderTopBar(frame) {
		t.Error("missing eth.svg should abort the render")
	}
	drawCovStashTopBarData(t, "wifi", "", "", 0, 50, false)
	cacheTopBarStr = ""
	if renderTopBar(frame) {
		t.Error("missing wifi.svg should abort the render")
	}

	// Bolt SVGs unavailable: both tint variants fall back to no bolt.
	if img := drawBattery(47, 20, 15, true, 0, 0); img == nil {
		t.Error("low-SOC battery without bolt asset returned nil")
	}
	if img := drawBattery(47, 20, 65, true, 0, 0); img == nil {
		t.Error("mid-SOC battery without bolt asset returned nil")
	}
}

func TestDrawCovRenderFooter(t *testing.T) {
	drawCovSetup(t)
	drawCovSaveSendState(t)
	frame := image.NewRGBA(image.Rect(0, 0, PCAT2_LCD_WIDTH, PCAT2_FOOTER_HEIGHT))

	t.Run("sms_footer", func(t *testing.T) {
		cacheFooterStr = ""
		if !renderFooter(frame, 1, 4, true) {
			t.Fatal("SMS footer should draw")
		}
		if !drawCovHasInk(frame) {
			t.Error("SMS footer drew nothing")
		}
		if renderFooter(frame, 1, 4, true) {
			t.Error("identical SMS footer should hit the cache")
		}
	})

	t.Run("missing_assets", func(t *testing.T) {
		old := assetsPrefix
		assetsPrefix = t.TempDir()
		t.Cleanup(func() { assetsPrefix = old })
		cacheFooterStr = ""
		if renderFooter(frame, 0, 3, false) {
			t.Error("missing dot assets should abort the render")
		}
	})

	t.Run("missing_fonts", func(t *testing.T) {
		drawCovNoFonts(t)
		cacheFooterStr = ""
		if renderFooter(frame, 0, 3, false) {
			t.Error("missing fonts should abort the render")
		}
	})

	t.Run("drawFooter_wrapper", func(t *testing.T) {
		cacheFooterStr = ""
		drawFooter(drawCovZeroDisplay(), frame, 0, 3, false)
		drawFooter(drawCovZeroDisplay(), frame, 0, 3, false) // cache hit: no send
	})
}

// drawCovMiddleFrame allocates a cleared middle-area frame.
func drawCovMiddleFrame() *image.RGBA {
	frame := image.NewRGBA(image.Rect(0, 0, middleFrameWidth, middleFrameHeight))
	clearFrame(frame, middleFrameWidth, middleFrameHeight)
	return frame
}

// drawCovStoreRealPageData populates globalData for every key config.json's
// pages reference, so a render exercises the full element list.
func drawCovStoreRealPageData(t *testing.T) {
	t.Helper()
	vals := map[string]interface{}{
		"WanUP": "1.2", "WanUP_Unit": "MB/s", "WanDOWN": "0.3",
		"DailyDataUsage": "1.5", "MonthlyDataUsage": "20.4",
		"BatteryWattage": "5.2", "BatteryVoltage": "8.4",
		"DCVoltage": "12.1", "RemainingTime": "2:30",
		"Ping0": int64(23), "Ping1": -2, "Ping0Rate": "100", "Ping1Rate": "98",
		"LAN_IP": "192.168.1.1", "WAN_IP": "10.0.0.2", "PUBLIC_IP": "1.2.3.4",
		"SSID": "pcat2", "SSID2": "pcat2-5g",
		"CpuUsages":       []float64{0, 25.5, 101, -3, 88},
		"MemUsagePercent": 37.5, "MemUsage": "3.2/16",
		"DiskUsagePercent": 41.0, "DiskNvmePresent": true, "DiskNvmePercent": 12.0,
		"DiskSDPresent": true, "DiskSDPercent": 80.0,
		"Uptime": "1d 2h", "OSVersion": "v1.0", "SN": "ABC123",
		"ISPName": "TestISP", "ModemNetworkInfo": "LTE B3", "SimNumber": "ERROR", // sentinel
		"ModemModel": "RM500Q", "NetworkModeLabel": "NAT", "SimState": "Ready",
		"WiFiClientsCount": 3, "DHCPClientsCount": 5, "SdState": "-", // dash placeholder
		"FanRPM": 4200, "BoardTemperature": "42.5",
		"GpsSpeed": "32", "GpsCourse": "132° SE", "GpsCourseDeg": 132.0,
		"GpsAlt": "412", "GpsAccuracy": "±4", "GpsSats": "7/12",
		"GpsFix": "3D", "GpsLat": "31.2304° N", "GpsLon": "121.4737° E",
	}
	for k, v := range vals {
		drawCovStashGlobal(t, k, v)
	}
}

func TestDrawCovRenderMiddleRealConfig(t *testing.T) {
	drawCovSetup(t)
	drawCovSaveScrollState(t)
	drawCovStoreRealPageData(t)

	c, err := loadConfig("config.json")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	for page := 0; page < 5; page++ {
		frame := drawCovMiddleFrame()
		renderMiddle(frame, &c, false, page)
		if !drawCovHasInk(frame) {
			t.Errorf("page %d rendered nothing", page)
		}
	}

	// A value wider than the panel turns its element into a ticker.
	t.Run("overflow_ticker", func(t *testing.T) {
		drawCovStashGlobal(t, "SSID", strings.Repeat("VeryLongNetworkName", 6))
		frame := drawCovMiddleFrame()
		renderMiddle(frame, &c, false, 1)
		if !anyTextScrolling {
			t.Error("an overflowing SSID should start the ticker")
		}
	})
}

func TestDrawCovRenderMiddleSMSAndDegenerate(t *testing.T) {
	drawCovSetup(t)
	oldSms := smsPagesImages
	t.Cleanup(func() { smsPagesImages = oldSms })

	page := image.NewRGBA(image.Rect(0, 0, middleFrameWidth, middleFrameHeight))
	fillRect(page, 0, 0, 50, 50, color.RGBA{255, 255, 255, 255})
	smsPagesImages = []*image.RGBA{page, nil}

	frame := drawCovMiddleFrame()
	renderMiddle(frame, nil, true, 0)
	if !drawCovHasInk(frame) {
		t.Error("SMS page not blitted")
	}
	renderMiddle(frame, nil, true, 1)  // nil entry → invalid-index branch
	renderMiddle(frame, nil, true, -1) // out of range
	renderMiddle(frame, nil, true, 99) // out of range

	renderMiddle(nil, nil, true, 0)                                   // nil frame
	renderMiddle(image.NewRGBA(image.Rect(0, 0, 0, 0)), nil, true, 0) // empty frame
}

func TestDrawCovRenderMiddleSyntheticElements(t *testing.T) {
	drawCovSetup(t)
	drawCovSaveScrollState(t)

	// Assets in a private prefix: a PNG and an SVG icon.
	dir := t.TempDir()
	drawCovWriteImageFile(t, dir, "icon.png")
	if err := os.WriteFile(filepath.Join(dir, "icon.svg"), []byte(drawCovSVG), 0o644); err != nil {
		t.Fatal(err)
	}
	oldPrefix := assetsPrefix
	assetsPrefix = dir
	t.Cleanup(func() { assetsPrefix = oldPrefix })

	// Power graph time frame is mutated by the graph element; restore it.
	powerData.mu.Lock()
	oldTF := powerData.TimeFrameMins
	powerData.mu.Unlock()
	t.Cleanup(func() {
		powerData.mu.Lock()
		powerData.TimeFrameMins = oldTF
		powerData.mu.Unlock()
	})

	drawCovStashGlobal(t, "DrawCovNil", nil)
	drawCovStashGlobal(t, "DrawCovDash", " - ")
	drawCovStashGlobal(t, "DrawCovSentinel", "ERROR")
	drawCovStashGlobal(t, "DrawCovText", "42")
	drawCovStashGlobal(t, "DrawCovText_Unit", "V")
	drawCovStashGlobal(t, "Ping0", "not-a-number") // invalid ping type
	drawCovStashGlobal(t, "Ping1", int64(-7))      // unexpected negative → X
	drawCovStashGlobal(t, "DrawCovAnchorDash", "-")
	drawCovStashGlobal(t, "DrawCovAnchor", "0:10")
	drawCovStashGlobal(t, "DrawCovPct", 63)
	drawCovStashGlobal(t, "DrawCovLabel", 3.5) // non-string label branch
	drawCovStashGlobal(t, "DrawCovCores", []float64{10, 90})
	globalData.Delete("GpsCourseDeg") // gps_compass no-fix branch
	drawCovStashGlobal(t, "GpsCourseDeg", nil)

	sz := func(w, h int) *Size { return &Size{Width: w, Height: h} }
	els := []DisplayElement{
		{Enable: 0, Type: "text", DataKey: "DrawCovText", Font: "reg"}, // disabled
		{Enable: 1, Type: "text", DataKey: "DrawCovMissingKey", Font: "reg", UnitsFont: "unit"},
		{Enable: 1, Type: "text", DataKey: "DrawCovNil", Font: "reg", UnitsFont: "unit"},
		{Enable: 1, Type: "text", DataKey: "DrawCovDash", Font: "reg", UnitsFont: "unit"},
		{Enable: 1, Type: "text", DataKey: "DrawCovSentinel", Font: "reg", UnitsFont: "unit"},
		{Enable: 1, Type: "text", DataKey: "DrawCovText", Font: "nope", UnitsFont: "unit"}, // main font error
		{Enable: 1, Type: "text", DataKey: "DrawCovText", Font: "reg", UnitsFont: "nope"},  // units font error
		{Enable: 1, Type: "text", DataKey: "Ping0", Font: "reg", UnitsFont: "unit", Position: Position{X: 10, Y: 10}},
		{Enable: 1, Type: "text", DataKey: "Ping1", Font: "reg", UnitsFont: "unit", Position: Position{X: 10, Y: 40}, Color: []int{1, 2, 3}},
		{Enable: 1, Type: "text", DataKey: "DrawCovText", Font: "reg", UnitsFont: "unit",
			Position: Position{X: 10, Y: 70}, Units: "W", Size2: sz(80, 20)},
		{Enable: 1, Type: "icon", IconPath: "icon.svg", Position: Position{X: 5, Y: 100}, Size: sz(12, 12)},
		{Enable: 1, Type: "icon", IconPath: "icon.svg", AnchorAfterDataKey: "DrawCovMissingAnchor"},
		{Enable: 1, Type: "icon", IconPath: "icon.svg", AnchorAfterDataKey: "DrawCovAnchorDash"},
		{Enable: 1, Type: "icon", IconPath: "icon.svg", AnchorAfterDataKey: "DrawCovAnchor",
			AnchorGap: 4, Position: Position{X: 5, Y: 120}, Size: sz(10, 10)},
		{Enable: 1, Type: "icon", IconPath: "icon.png", Position: Position{X: 40, Y: 100},
			Color: []int{200, 30, 30}}, // tinted PNG (first: renders tint)
		{Enable: 1, Type: "icon", IconPath: "icon.png", Position: Position{X: 60, Y: 100},
			Color: []int{200, 30, 30}}, // tinted PNG (second: tint cache)
		{Enable: 1, Type: "icon", IconPath: "missing.png", Position: Position{X: 5, Y: 5}},
		{Enable: 1, Type: "fixed_text", Label: "ping [ping_site0]/[ping_site1] [unknown]",
			Font: "tiny", Position: Position{X: 5, Y: 140}, Color: []int{9, 9, 9}},
		{Enable: 1, Type: "fixed_text", Label: "x", Font: "nope"},
		{Enable: 1, Type: "vtext", Label: "CPU", Font: "tiny", Position: Position{X: 3, Y: 150}, Size: sz(12, 14)},
		{Enable: 1, Type: "vtext", Label: "x", Font: "nope"},
		{Enable: 1, Type: "graph"}, // missing graph_config
		{Enable: 1, Type: "graph", GraphConfig: &GraphConfig{GraphType: "power", TimeFrameMins: 5},
			Position: Position{X: 5, Y: 170}, Size: sz(60, 25)},
		{Enable: 1, Type: "graph", GraphConfig: &GraphConfig{GraphType: "gps_compass"},
			Position: Position{X: 70, Y: 170}, Size2: sz(60, 25)},
		{Enable: 1, Type: "graph", GraphConfig: &GraphConfig{GraphType: "bogus"}}, // default size + unknown type
		{Enable: 1, Type: "cpu_bars", DataKey: "DrawCovCores", Position: Position{X: 5, Y: 200}, Size2: sz(60, 20)},
		{Enable: 1, Type: "cpu_bars", DataKey: "DrawCovNoCores", Position: Position{X: 70, Y: 200}}, // default 8×0%
		{Enable: 1, Type: "hbar", DataKey: "DrawCovPct", LabelDataKey: "DrawCovLabel", Units: "GB",
			Position: Position{X: 5, Y: 225}, Size2: sz(100, 14)},
		{Enable: 1, Type: "disk_bars", Position: Position{X: 5, Y: 245}, Size2: sz(120, 18)},
		{Enable: 1, Type: "unknown_kind"},
	}
	c := Config{
		PingSite0: "a.com", PingSite1: "b.com",
		DisplayTemplate: DisplayTemplate{Elements: map[string][]DisplayElement{"page0": els}},
	}

	frame := drawCovMiddleFrame()
	renderMiddle(frame, &c, false, 0)
	if !drawCovHasInk(frame) {
		t.Error("synthetic page rendered nothing")
	}

	powerData.mu.Lock()
	tf := powerData.TimeFrameMins
	powerData.mu.Unlock()
	if tf != 5 {
		t.Errorf("graph element should set the power time frame: got %d", tf)
	}
}

func TestDrawCovMiddlePageFingerprint(t *testing.T) {
	// SMS form.
	if fp := middlePageFingerprint(nil, true, 2); !strings.HasPrefix(fp, "sms:2:") {
		t.Errorf("sms fingerprint = %q", fp)
	}
	// Nil config.
	if fp := middlePageFingerprint(nil, false, 0); fp != "nil" {
		t.Errorf("nil config fingerprint = %q, want nil", fp)
	}

	// Graph pixels depend on the recorded samples; add one, restore after.
	powerData.mu.Lock()
	oldSamples := powerData.Samples
	powerData.Samples = append([]PowerSample{}, PowerSample{Timestamp: time.Now(), Wattage: 4.2})
	powerData.mu.Unlock()
	t.Cleanup(func() {
		powerData.mu.Lock()
		powerData.Samples = oldSamples
		powerData.mu.Unlock()
	})

	drawCovStashGlobal(t, "DrawCovFPKey", "v1")
	drawCovStashGlobal(t, "DrawCovFPKey_Unit", "W")
	drawCovStashGlobal(t, "DrawCovFPLabel", "lbl")

	c := &Config{DisplayTemplate: DisplayTemplate{Elements: map[string][]DisplayElement{
		"page0": {
			{Enable: 0, Type: "text", DataKey: "DrawCovFPKey"}, // disabled → skipped
			{Enable: 1, Type: "text", DataKey: "DrawCovFPKey", LabelDataKey: "DrawCovFPLabel", AnchorAfterDataKey: "DrawCovFPKey"},
			{Enable: 1, Type: "icon", IconPath: "x.svg", AnchorAfterDataKey: "DrawCovFPKey"},
			{Enable: 1, Type: "disk_bars"},
			{Enable: 1, Type: "graph"},
			{Enable: 1, Type: "fixed_text", Label: "static"},
			{Enable: 1, Type: "vtext", Label: "CPU"},
		},
	}}}

	fp1 := middlePageFingerprint(c, false, 0)
	if !strings.Contains(fp1, "DrawCovFPKey=v1") || !strings.Contains(fp1, "i:x.svg") || !strings.Contains(fp1, "|g:1:4.200") {
		t.Errorf("fingerprint missing expected parts: %q", fp1)
	}
	// Changing a data key must move the fingerprint.
	globalData.Store("DrawCovFPKey", "v2")
	if fp2 := middlePageFingerprint(c, false, 0); fp2 == fp1 {
		t.Error("fingerprint did not change with the data")
	}
}

func TestDrawCovPageHasOverflowTextMore(t *testing.T) {
	drawCovSetup(t)
	long := strings.Repeat("wide-value-", 30)
	drawCovStashGlobal(t, "DrawCovOverflow", long)
	drawCovStashGlobal(t, "DrawCovShort", "ok")

	mk := func(el DisplayElement) *Config {
		return &Config{DisplayTemplate: DisplayTemplate{
			Elements: map[string][]DisplayElement{"page0": {el}},
		}}
	}

	if !pageHasOverflowText(mk(DisplayElement{Enable: 1, Type: "text", DataKey: "DrawCovOverflow", Font: "reg"}), 0) {
		t.Error("wide value did not report overflow")
	}
	if !pageHasOverflowText(mk(DisplayElement{Enable: 1, Type: "text", DataKey: "DrawCovShort", Font: "reg",
		Size: &Size{Width: 1}}), 0) {
		t.Error("pinned 1px slot did not report overflow")
	}
	if !pageHasOverflowText(mk(DisplayElement{Enable: 1, Type: "text", DataKey: "DrawCovShort", Font: "reg",
		Size2: &Size{Width: 1}}), 0) {
		t.Error("pinned 1px _size slot did not report overflow")
	}
	if pageHasOverflowText(mk(DisplayElement{Enable: 1, Type: "text", DataKey: "DrawCovOverflow", Font: "nope"}), 0) {
		t.Error("an unloadable font must not report overflow")
	}
	if pageHasOverflowText(mk(DisplayElement{Enable: 1, Type: "text", DataKey: "DrawCovShort", Font: "reg"}), 0) {
		t.Error("a fitting value reported overflow")
	}
}

func TestDrawCovDrawBatteryVariants(t *testing.T) {
	drawCovSetup(t)

	cases := []struct {
		name       string
		w, h       int
		soc        float64
		isCharging bool
	}{
		{"clamp_negative_soc", 49, 20, -5, false},
		{"clamp_over_100", 49, 20, 130, false},
		{"low_charging_white_bolt", 49, 20, 12, true},
		{"mid_charging_black_bolt", 49, 20, 60, true},
		{"full_charging_no_bolt", 49, 20, 100, true},
		{"tiny_no_terminal", 3, 8, 50, false},
		{"very_short_body", 10, 2, 50, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img := drawBattery(tc.w, tc.h, tc.soc, tc.isCharging, 0, 0)
			if img == nil {
				t.Fatal("nil battery image")
			}
			// Cached second call returns the identical bitmap.
			if again := drawBattery(tc.w, tc.h, tc.soc, tc.isCharging, 0, 0); again != img {
				t.Error("second call did not hit the cache")
			}
		})
	}

	if img := drawBattery(0, 10, 50, false, 0, 0); img.Bounds().Dx() != 1 {
		t.Error("degenerate size should return the 1x1 placeholder")
	}

	t.Run("font_missing", func(t *testing.T) {
		drawCovNoFonts(t)
		img := drawBattery(48, 19, 33, false, 0, 0) // unique size → fresh key
		if img == nil {
			t.Fatal("nil battery image without fonts")
		}
	})
}

func TestDrawCovDrawCpuBars(t *testing.T) {
	drawCovSetup(t)
	frame := image.NewRGBA(image.Rect(0, 0, 172, 100))

	drawCpuBars(frame, 0, 0, 80, 40, nil, PCAT_YELLOW)         // no cores → no-op
	drawCpuBars(frame, 0, 0, 0, 40, []float64{1}, PCAT_YELLOW) // w<=0 → no-op
	if drawCovHasInk(frame) {
		t.Error("degenerate cpu bars painted pixels")
	}

	usages := []float64{0, 15, 50, 101, -4, 99.9, 33, 66}
	drawCpuBars(frame, 5, 5, 80, 40, usages, PCAT_YELLOW)
	if !drawCovHasInk(frame) {
		t.Error("cpu bars drew nothing")
	}
	// Same bucketed values → cache hit.
	drawCpuBars(frame, 5, 50, 80, 40, usages, PCAT_YELLOW)

	// Too narrow for gaps: the avail fallback plus barW<1 skips.
	drawCpuBars(frame, 100, 5, 4, 20, usages, color.RGBA{0, 128, 255, 255})
}

func TestDrawCovDrawHBarAndLabels(t *testing.T) {
	drawCovSetup(t)
	frame := image.NewRGBA(image.Rect(0, 0, 172, 120))

	drawHBar(frame, 0, 0, 0, 10, 50, "x", PCAT_YELLOW) // w<=0 no-op
	drawHBar(frame, 5, 5, 120, 20, -10, "", PCAT_YELLOW)
	drawHBar(frame, 5, 30, 120, 20, 250, "9.9/16GB", PCAT_YELLOW) // clamped full + label
	drawHBar(frame, 5, 55, 120, 20, 50, "3.2/16GB", color.RGBA{0, 200, 100, 255})
	drawHBar(frame, 5, 80, 120, 20, 50, "3.2/16GB", color.RGBA{0, 200, 100, 255}) // cache hit
	if !drawCovHasInk(frame) {
		t.Error("hbar drew nothing")
	}
}

func TestDrawCovPickBarLabelFaceAndAura(t *testing.T) {
	drawCovSetup(t)

	// Wide slots: the first (largest) candidate fits.
	if _, ok := pickBarLabelFace([]string{"eMMC"}, []int{150}, 30); !ok {
		t.Error("wide slot found no face")
	}
	// Narrow slots: falls through to smaller faces, still ok (fallback).
	if _, ok := pickBarLabelFace([]string{"eMMC", "NVMe"}, []int{18, 18}, 30); !ok {
		t.Error("narrow slot found no face at all")
	}
	// Short bar: height check fails for the larger faces.
	if _, ok := pickBarLabelFace([]string{"SD"}, []int{60}, 5); !ok {
		t.Error("short bar found no face")
	}

	t.Run("no_fonts", func(t *testing.T) {
		drawCovNoFonts(t)
		if _, ok := pickBarLabelFace([]string{"eMMC"}, []int{100}, 30); ok {
			t.Error("empty font table should report no face")
		}
	})

	face := basicfont.Face7x13
	frame := image.NewRGBA(image.Rect(0, 0, 60, 30))
	drawTinyBarLabel(nil, 10, 10, "x", face)
	drawTinyBarLabel(frame, 10, 10, "", face)
	drawTinyBarLabel(frame, 10, 10, "x", nil)
	if drawCovHasInk(frame) {
		t.Error("degenerate label calls painted pixels")
	}
	drawTinyBarLabel(frame, 30, 15, "SD", face)
	if !drawCovHasInk(frame) {
		t.Error("bar label drew nothing")
	}

	frame2 := image.NewRGBA(image.Rect(0, 0, 60, 30))
	drawTextWithAura(nil, "x", 5, 5, face, PCAT_WHITE, PCAT_BLACK)
	drawTextWithAura(frame2, "", 5, 5, face, PCAT_WHITE, PCAT_BLACK)
	drawTextWithAura(frame2, "x", 5, 5, nil, PCAT_WHITE, PCAT_BLACK)
	drawTextWithAura(frame2, "Hi", 30, 15, face, color.RGBA{255, 255, 255, 255}, color.RGBA{0, 0, 0, 255})
	if !drawCovHasInk(frame2) {
		t.Error("aura text drew nothing")
	}

	frame3 := image.NewRGBA(image.Rect(0, 0, 60, 30))
	drawTextAtBaseline(nil, "x", 0, 10, face, color.White)
	drawTextAtBaseline(frame3, "", 0, 10, face, color.White)
	drawTextAtBaseline(frame3, "x", 0, 10, nil, color.White)
	drawTextAtBaseline(frame3, "Base", 2, 15, face, color.RGBA{255, 255, 255, 255})
	if !drawCovHasInk(frame3) {
		t.Error("baseline text drew nothing")
	}
}

func TestDrawCovShowCiao(t *testing.T) {
	drawCovSetup(t)
	drawCovSaveSendState(t)
	display := drawCovZeroDisplay()

	showCiao(display, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT, time.Millisecond)

	// showCiaoInstant additionally forces the backlight to 0; restore the
	// logical-brightness state it mutates.
	mu.Lock()
	oldLogical := lastLogical
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		lastLogical = oldLogical
		if offTimer != nil {
			offTimer.Stop()
			offTimer = nil
		}
		mu.Unlock()
	})
	showCiaoInstant(display, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT)

	t.Run("missing_assets", func(t *testing.T) {
		old := assetsPrefix
		assetsPrefix = t.TempDir()
		t.Cleanup(func() { assetsPrefix = old })
		showCiao(display, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT, time.Millisecond)
		showCiaoInstant(display, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT)
	})

	t.Run("missing_fonts", func(t *testing.T) {
		drawCovNoFonts(t)
		showCiao(display, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT, time.Millisecond)
		showCiaoInstant(display, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT)
	})
}

func TestDrawCovShowWelcomeStaticMissingAssets(t *testing.T) {
	drawCovSetup(t)
	old := assetsPrefix
	assetsPrefix = t.TempDir() // welcome.svg missing → early return, no sweep
	t.Cleanup(func() { assetsPrefix = old })
	showWelcomeStatic(drawCovZeroDisplay(), PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT)
}

func TestDrawCovShowWelcomeAndTransition(t *testing.T) {
	drawCovSetup(t)
	drawCovSaveSendState(t)
	drawCovSaveScrollState(t)
	drawCovStoreRealPageData(t)

	oldCfg := cfg
	oldPages := cfgNumPages
	t.Cleanup(func() {
		cfg = oldCfg
		cfgNumPages = oldPages
	})
	c, err := loadConfig("config.json")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	cfg = c
	cfgNumPages = 5

	oldLoc := displayLoc.Load()
	t.Cleanup(func() { displayLoc.Store(oldLoc) })
	displayLoc.Store(time.UTC)

	// A ~30ms animation window: the pipeline starts, renders at most a couple
	// of frames, then lands on the resting pose and runs the page transition.
	showWelcome(drawCovZeroDisplay(), PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT, 30*time.Millisecond)
}
