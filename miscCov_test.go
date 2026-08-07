package main

// Coverage for powerGraph.go persistence, the SMS fetch/render pipeline in
// processSms.go, and the small testable helpers in main.go. Every new
// top-level identifier is MiscCov/miscCov prefixed so concurrently developed
// test files merge into this package without collisions.
//
// All mutated globals are restored via t.Cleanup; file writes go only to
// t.TempDir() (power data via the powerDataFilePath seam, SMS PNGs via
// t.Chdir); HTTP traffic stays on the loopback httptest server through the
// shared redirectLocalHTTP seam.

import (
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
)

// miscCovSetPowerDataFile points the power-data persistence at a temp file and
// restores the original path (and the in-memory samples) on cleanup.
func miscCovSetPowerDataFile(t *testing.T) string {
	t.Helper()
	savedPath := powerDataFilePath
	powerData.mu.Lock()
	savedSamples := powerData.Samples
	savedTimeFrame := powerData.TimeFrameMins
	powerData.mu.Unlock()
	t.Cleanup(func() {
		powerDataFilePath = savedPath
		powerData.mu.Lock()
		powerData.Samples = savedSamples
		powerData.TimeFrameMins = savedTimeFrame
		powerData.mu.Unlock()
	})
	powerDataFilePath = filepath.Join(t.TempDir(), "power.json")
	return powerDataFilePath
}

func TestMiscCovPowerDataSaveLoadRoundTrip(t *testing.T) {
	path := miscCovSetPowerDataFile(t)

	now := time.Now()
	powerData.mu.Lock()
	powerData.TimeFrameMins = 15
	powerData.Samples = []PowerSample{
		{Timestamp: now.Add(-20 * time.Minute), Wattage: 9.9}, // beyond the 15m frame
		{Timestamp: now.Add(-2 * time.Minute), Wattage: 1.25},
		{Timestamp: now.Add(-1 * time.Minute), Wattage: -0.5},
	}
	powerData.mu.Unlock()

	savePowerData()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("savePowerData wrote nothing: %v", err)
	}

	// Wipe the in-memory state and reload from disk: the two recent samples
	// come back and the stale one is trimmed by the load-time cleanup.
	powerData.mu.Lock()
	powerData.Samples = nil
	powerData.mu.Unlock()

	loadPowerData()

	powerData.mu.RLock()
	defer powerData.mu.RUnlock()
	if len(powerData.Samples) != 2 {
		t.Fatalf("loaded %d samples, want 2 (stale sample must be trimmed)", len(powerData.Samples))
	}
	if powerData.Samples[0].Wattage != 1.25 || powerData.Samples[1].Wattage != -0.5 {
		t.Errorf("loaded wattages = %v, %v; want 1.25, -0.5",
			powerData.Samples[0].Wattage, powerData.Samples[1].Wattage)
	}
	if powerData.TimeFrameMins != 15 {
		t.Errorf("TimeFrameMins = %d, want 15", powerData.TimeFrameMins)
	}
}

func TestMiscCovLoadPowerDataMissingAndInvalid(t *testing.T) {
	path := miscCovSetPowerDataFile(t)

	powerData.mu.Lock()
	powerData.Samples = []PowerSample{{Timestamp: time.Now(), Wattage: 3.0}}
	powerData.mu.Unlock()

	// Missing file: state untouched.
	loadPowerData()
	powerData.mu.RLock()
	n := len(powerData.Samples)
	powerData.mu.RUnlock()
	if n != 1 {
		t.Errorf("samples after missing-file load = %d, want the untouched 1", n)
	}

	// Invalid JSON: decode error path, no panic.
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	loadPowerData()
}

func TestMiscCovSavePowerDataSnapshotCreateError(t *testing.T) {
	savedPath := powerDataFilePath
	t.Cleanup(func() { powerDataFilePath = savedPath })
	powerDataFilePath = filepath.Join(t.TempDir(), "no-such-dir", "power.json")

	// os.Create fails; the function must log and return, not panic.
	savePowerDataSnapshot([]PowerSample{{Timestamp: time.Now(), Wattage: 1}}, 15)
}

func TestMiscCovRecordPowerSampleTrimAndPeriodicSave(t *testing.T) {
	path := miscCovSetPowerDataFile(t)
	savedWattage, hadWattage := globalData.Load("BatteryWattage")
	t.Cleanup(func() {
		if hadWattage {
			globalData.Store("BatteryWattage", savedWattage)
		} else {
			globalData.Delete("BatteryWattage")
		}
	})
	globalData.Store("BatteryWattage", "2.0")

	now := time.Now()

	// Stale-sample cleanup: one sample beyond the time frame is dropped.
	powerData.mu.Lock()
	powerData.TimeFrameMins = 15
	powerData.Samples = []PowerSample{
		{Timestamp: now.Add(-30 * time.Minute), Wattage: 8},
		{Timestamp: now.Add(-1 * time.Minute), Wattage: 1},
	}
	powerData.mu.Unlock()

	recordPowerSample()

	powerData.mu.RLock()
	n := len(powerData.Samples)
	powerData.mu.RUnlock()
	if n != 2 {
		t.Errorf("samples after trim = %d, want 2 (old one dropped, new one appended)", n)
	}

	// MAX_POWER_SAMPLES cap + every-10th-sample save: 905 recent samples grow
	// to 906, get capped at 900, and 900%%10==0 triggers the snapshot write.
	os.Remove(path)
	prefill := make([]PowerSample, 0, 905)
	for i := 0; i < 905; i++ {
		prefill = append(prefill, PowerSample{
			Timestamp: now.Add(-time.Duration(905-i) * 500 * time.Millisecond),
			Wattage:   1,
		})
	}
	powerData.mu.Lock()
	powerData.Samples = prefill
	powerData.mu.Unlock()

	recordPowerSample()

	powerData.mu.RLock()
	n = len(powerData.Samples)
	powerData.mu.RUnlock()
	if n != MAX_POWER_SAMPLES {
		t.Errorf("samples after cap = %d, want %d", n, MAX_POWER_SAMPLES)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("periodic save did not write the data file: %v", err)
	}
}

// One tick of the recorder goroutine: it computes an interval, waits it out,
// sees the cleared shutdown flag and exits — covering the loop body without
// leaving anything running (recordPowerSample itself is covered separately).
// The shutdown flag is cleared while the goroutine is inside its timer wait,
// so the tick deterministically ends in the early return.
//
// Synchronization: the goroutine's only unsynchronized read is idleState. It
// is set to STATE_IDLE here so displayAsleep() proceeds to lock `mu` right
// after that read; lastLogical is left non-zero so the interval stays 1s (the
// screen is dim-idle, not fully dark). Once the goroutine has exited, the test
// takes `mu` itself, which orders every later idleState write after the
// goroutine's read — race-free under -race for this test and all that follow.
func TestMiscCovInitPowerDataRecordingOneTick(t *testing.T) {
	miscCovSetPowerDataFile(t)

	savedRunning := weAreRunning()
	savedIdle := idleState
	mu.Lock()
	savedLogical := lastLogical
	lastLogical = 50 // backlight logically on: displayAsleep() = false, 1s tick
	mu.Unlock()
	idleState = STATE_IDLE
	setWeAreRunning(true)

	initPowerDataRecording()

	// Let the goroutine pass its loop check and enter the 1s timer wait, then
	// clear the flag so the tick ends in `return` instead of a sample.
	time.Sleep(150 * time.Millisecond)
	setWeAreRunning(false)
	// Wait out the timer plus a wide margin so the goroutine has exited before
	// the flag is restored (a live goroutine seeing true again would loop).
	time.Sleep(1300 * time.Millisecond)

	// Acquire the lock displayAsleep released to establish the happens-before
	// edge over the goroutine's idleState read, then restore everything.
	mu.Lock()
	lastLogical = savedLogical
	mu.Unlock()
	idleState = savedIdle
	setWeAreRunning(savedRunning)
}

func TestMiscCovDrawPowerGraphRanges(t *testing.T) {
	powerData.mu.Lock()
	savedSamples := powerData.Samples
	powerData.mu.Unlock()
	t.Cleanup(func() {
		powerData.mu.Lock()
		powerData.Samples = savedSamples
		powerData.mu.Unlock()
	})

	now := time.Now()
	tests := []struct {
		name    string
		samples []PowerSample
	}{
		{"all_positive_discharge", []PowerSample{
			{Timestamp: now.Add(-2 * time.Minute), Wattage: 4.0},
			{Timestamp: now.Add(-1 * time.Minute), Wattage: 5.5},
			{Timestamp: now, Wattage: 6.0},
		}},
		{"all_negative_charge", []PowerSample{
			{Timestamp: now.Add(-2 * time.Minute), Wattage: -4.0},
			{Timestamp: now.Add(-1 * time.Minute), Wattage: -5.5},
			{Timestamp: now, Wattage: -6.0},
		}},
		{"narrow_range_expanded", []PowerSample{
			{Timestamp: now.Add(-1 * time.Minute), Wattage: 0.05},
			{Timestamp: now, Wattage: -0.05},
		}},
		{"identical_timestamps", []PowerSample{
			{Timestamp: now, Wattage: 1.0},
			{Timestamp: now, Wattage: 2.0},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			powerData.mu.Lock()
			powerData.Samples = tt.samples
			powerData.mu.Unlock()

			img := image.NewRGBA(image.Rect(0, 0, 120, 80))
			drawPowerGraph(img, 5, 5, 100, 60)

			// The semi-transparent background alone guarantees some pixel moved.
			changed := false
			for x := 5; x < 105 && !changed; x++ {
				for y := 5; y < 65 && !changed; y++ {
					if img.RGBAAt(x, y) != (image.NewRGBA(image.Rect(0, 0, 1, 1)).RGBAAt(0, 0)) {
						changed = true
					}
				}
			}
			if !changed {
				t.Error("drawPowerGraph drew nothing")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// processSms.go
// ---------------------------------------------------------------------------

// miscCovSaveSmsState snapshots and restores the SMS render cache globals that
// collectAndDrawSms/getJsonContent mutate.
func miscCovSaveSmsState(t *testing.T) {
	t.Helper()
	savedJson := lastSmsJsonContent
	savedPages := lastNumPages
	savedVersion := lastSmsConfigVersion
	savedSuccess := lastSuccessfulSmsJsonContent
	savedImages := smsPagesImages
	t.Cleanup(func() {
		lastSmsJsonContent = savedJson
		lastNumPages = savedPages
		lastSmsConfigVersion = savedVersion
		lastSuccessfulSmsJsonContent = savedSuccess
		smsPagesImages = savedImages
	})
}

func TestMiscCovGetJsonContentSuccess(t *testing.T) {
	miscCovSaveSmsState(t)

	body := `{"msg":[{"sender":"+8613800000000","timestamp":"2026-08-07 10:00:00","content":"hello misccov"}]}`
	var gotN string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotN = r.URL.Query().Get("n")
		w.Write([]byte(body))
	}))
	defer server.Close()
	redirectLocalHTTP(t, server)

	lastSuccessfulSmsJsonContent = ""
	cfg := &Config{SmsLimitForScreen: 7}
	if got := getJsonContent(cfg); got != body {
		t.Errorf("getJsonContent = %q, want the served body", got)
	}
	if gotN != "7" {
		t.Errorf("requested n=%s, want the configured 7", gotN)
	}
	if lastSuccessfulSmsJsonContent != body {
		t.Error("a successful fetch was not cached")
	}

	// nil config falls back to the default limit of 10.
	if got := getJsonContent(nil); got != body {
		t.Errorf("getJsonContent(nil) = %q, want the served body", got)
	}
	if gotN != "10" {
		t.Errorf("requested n=%s with nil config, want the default 10", gotN)
	}
}

func TestMiscCovGetJsonContentFallbacks(t *testing.T) {
	miscCovSaveSmsState(t)
	const cached = `{"msg":[{"sender":"cache","timestamp":"2026-08-07 09:00:00","content":"cached"}]}`

	t.Run("http_error_uses_cache", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "down", http.StatusBadGateway)
		}))
		defer server.Close()
		restore := redirectLocalHTTP(t, server)
		defer restore()

		lastSuccessfulSmsJsonContent = cached
		if got := getJsonContent(&Config{}); got != cached {
			t.Errorf("getJsonContent on HTTP 502 = %q, want the cached payload", got)
		}
	})

	t.Run("http_error_no_cache_is_empty", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "down", http.StatusBadGateway)
		}))
		defer server.Close()
		restore := redirectLocalHTTP(t, server)
		defer restore()

		lastSuccessfulSmsJsonContent = ""
		if got := getJsonContent(&Config{}); got != "" {
			t.Errorf("getJsonContent with no cache = %q, want \"\"", got)
		}
	})

	t.Run("request_error_uses_cache", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := server.URL
		server.Close() // guaranteed connection refused
		restore := redirectLocalHTTPURL(t, url)
		defer restore()

		lastSuccessfulSmsJsonContent = cached
		if got := getJsonContent(&Config{}); got != cached {
			t.Errorf("getJsonContent on refused connection = %q, want the cached payload", got)
		}
		lastSuccessfulSmsJsonContent = ""
		if got := getJsonContent(&Config{}); got != "" {
			t.Errorf("getJsonContent on refused connection with no cache = %q, want \"\"", got)
		}
	})
}

func TestMiscCovCollectAndDrawSmsRenderAndCache(t *testing.T) {
	if getSmsFont() == nil {
		t.Skip("SMS font asset unavailable")
	}
	miscCovSaveSmsState(t)

	body := `{"msg":[` +
		`{"sender":"+8613800000000","timestamp":"2026-08-07 10:00:00","content":"first message misccov"},` +
		`{"sender":"me","to":"+8613900001111","status":"SENT","timestamp":"2026-08-07 09:30:00","content":"reply from me"}` +
		`]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer server.Close()
	redirectLocalHTTP(t, server)

	// Force a render: pretend nothing was rendered yet.
	lastSmsJsonContent = ""
	lastSmsConfigVersion = -1
	lastSuccessfulSmsJsonContent = ""

	cfgLocal := &Config{ShowSms: true}
	pages := collectAndDrawSms(cfgLocal)
	if pages < 1 {
		t.Fatalf("collectAndDrawSms rendered %d pages, want >= 1", pages)
	}
	if len(smsPagesImages) != pages {
		t.Errorf("smsPagesImages holds %d pages, reported %d", len(smsPagesImages), pages)
	}

	// Same content and config version: the cached page count comes back
	// without a re-render.
	if again := collectAndDrawSms(cfgLocal); again != pages {
		t.Errorf("cached collectAndDrawSms = %d, want %d", again, pages)
	}
}

func TestMiscCovCollectAndDrawSmsDummyMessage(t *testing.T) {
	if getSmsFont() == nil {
		t.Skip("SMS font asset unavailable")
	}
	miscCovSaveSmsState(t)

	// A too-short payload (< 50 bytes) is replaced by the "No SMS" dummy.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"msg":[]}`))
	}))
	defer server.Close()
	redirectLocalHTTP(t, server)

	lastSmsJsonContent = ""
	lastSmsConfigVersion = -1
	lastSuccessfulSmsJsonContent = ""

	if pages := collectAndDrawSms(&Config{ShowSms: true}); pages != 1 {
		t.Errorf("dummy 'No SMS' message rendered %d pages, want 1", pages)
	}
}

func TestMiscCovDrawSmsFrJsonRendersPagesAndFiles(t *testing.T) {
	// Parse the font before changing directory: the font path is relative to
	// assetsPrefix (".") and cached behind a sync.Once.
	if getSmsFont() == nil {
		t.Skip("SMS font asset unavailable")
	}
	t.Chdir(t.TempDir()) // savePng writes page_N.png into the cwd

	long := strings.Repeat("longcontent misccov filler words to overflow one sms page ", 20)
	cjk := strings.Repeat("中文换行测试内容", 12)
	body := fmt.Sprintf(`{"msg":[`+
		`{"sender":"+8613800000000","timestamp":"2026-08-07 10:00:00","content":"%s"},`+
		`{"sender":"me","to":"+8613900001111","status":"SENT","timestamp":"2026-08-06 09:30:00","content":"sent misccov"},`+
		`{"sender":"OldYearSenderVeryLongName","timestamp":"2020-01-02 03:04:05","content":"%s"},`+
		`{"sender":"NoStamp","timestamp":"","content":"Supercalifragilisticexpialidociousmisccov"}`+
		`]}`, long, cjk)

	imgs, err := drawSmsFrJson(body, true, true)
	if err != nil {
		t.Fatalf("drawSmsFrJson: %v", err)
	}
	if len(imgs) < 2 {
		t.Fatalf("rendered %d pages, want >= 2 (content overflows one page)", len(imgs))
	}
	for i := range imgs {
		if _, err := os.Stat(fmt.Sprintf("page_%d.png", i)); err != nil {
			t.Errorf("savePng did not write page_%d.png: %v", i, err)
		}
	}

	// Return the pooled page images so later renders can reuse them.
	for _, im := range imgs {
		if rgba, ok := im.(*image.RGBA); ok {
			smsImagePool.Put(rgba)
		}
	}
}

func TestMiscCovFitSenderWidth(t *testing.T) {
	drawer := &font.Drawer{Face: basicfont.Face7x13}
	width := func(s string) int { return int(drawer.MeasureString(s) >> 6) }

	t.Run("non_positive_width", func(t *testing.T) {
		if got := fitSenderWidth("anyone", drawer, 0); got != "" {
			t.Errorf("fitSenderWidth with width 0 = %q, want \"\"", got)
		}
	})
	t.Run("fits_untouched", func(t *testing.T) {
		if got := fitSenderWidth("abc", drawer, 100); got != "abc" {
			t.Errorf("fitSenderWidth = %q, want unchanged \"abc\"", got)
		}
	})
	t.Run("short_sender_trims_tail", func(t *testing.T) {
		got := fitSenderWidth("abcd", drawer, width("ab"))
		if got != "ab" {
			t.Errorf("fitSenderWidth short = %q, want \"ab\"", got)
		}
	})
	t.Run("short_sender_impossible_width", func(t *testing.T) {
		if got := fitSenderWidth("abcd", drawer, 1); got != "" {
			t.Errorf("fitSenderWidth at width 1 = %q, want \"\"", got)
		}
	})
	t.Run("long_sender_elides_middle", func(t *testing.T) {
		sender := "+8613800001234"
		maxW := width("+8613800**34")
		got := fitSenderWidth(sender, drawer, maxW)
		if !strings.Contains(got, "**") || !strings.HasSuffix(got, "34") {
			t.Errorf("fitSenderWidth long = %q, want head**34 form", got)
		}
		if width(got) > maxW {
			t.Errorf("fitSenderWidth result %q wider than %d", got, maxW)
		}
	})
	t.Run("long_sender_impossible_width", func(t *testing.T) {
		if got := fitSenderWidth("+8613800001234", drawer, 1); got != "" {
			t.Errorf("fitSenderWidth long at width 1 = %q, want \"\"", got)
		}
	})
}

func TestMiscCovWrapTextTokenBoundaries(t *testing.T) {
	face := basicfont.Face7x13 // 7px per glyph

	// CJK adjacent to latin gets no separating space.
	lines := wrapText("abc中def", 200, face)
	if len(lines) != 1 || lines[0] != "abc中def" {
		t.Errorf("wrapText mixed = %v, want one unspaced line", lines)
	}

	// Words separated by spaces keep single spaces when they fit.
	lines = wrapText("aa bb", 200, face)
	if len(lines) != 1 || lines[0] != "aa bb" {
		t.Errorf("wrapText latin = %v, want [\"aa bb\"]", lines)
	}

	// A word that fits alone starts the next line after an overflow.
	lines = wrapText("aaaa bbbb", 7*5, face)
	if len(lines) != 2 || lines[0] != "aaaa" || lines[1] != "bbbb" {
		t.Errorf("wrapText overflow = %v, want [\"aaaa\" \"bbbb\"]", lines)
	}

	// An oversized latin word is hyphenated.
	lines = wrapText("abcdefghij", 7*5, face)
	if len(lines) < 2 || !strings.HasSuffix(lines[0], "-") {
		t.Errorf("wrapText hyphenation = %v, want a hyphenated first line", lines)
	}

	// CJK breaks per rune with no hyphen.
	lines = wrapText("中文测试", 15, face)
	if len(lines) < 2 || strings.Contains(strings.Join(lines, ""), "-") {
		t.Errorf("wrapText CJK = %v, want per-rune breaks without hyphens", lines)
	}
}

// ---------------------------------------------------------------------------
// main.go helpers
// ---------------------------------------------------------------------------

// miscCovDrain empties a buffered signal channel and reports how many tokens
// it held.
func miscCovDrain(ch chan struct{}) int {
	n := 0
	for {
		select {
		case <-ch:
			n++
		default:
			return n
		}
	}
}

// miscCovDrainAllReschedules leaves every collector wake channel empty.
func miscCovDrainAllReschedules() {
	for _, ch := range pingReschedule {
		miscCovDrain(ch)
	}
	for _, ch := range page0Reschedule {
		miscCovDrain(ch)
	}
	miscCovDrain(linuxReschedule)
	miscCovDrain(linkReschedule)
	miscCovDrain(gpsReschedule)
	miscCovDrain(pageChangeSignal)
}

func TestMiscCovIntervalHelpers(t *testing.T) {
	savedIdle, savedPage := idleState, currPageIdx
	t.Cleanup(func() { idleState, currPageIdx = savedIdle, savedPage })

	idleState = STATE_IDLE
	if got := currentPage0Interval(); got != INTERVAL_PAGE0_IDLE {
		t.Errorf("idle currentPage0Interval = %v, want %v", got, INTERVAL_PAGE0_IDLE)
	}
	if got := currentLinkInterval(); got != INTERVAL_LINK_IDLE {
		t.Errorf("idle currentLinkInterval = %v, want %v", got, INTERVAL_LINK_IDLE)
	}

	idleState = STATE_ACTIVE
	currPageIdx = 0
	if got := currentPage0Interval(); got != INTERVAL_PAGE0_ACTIVE {
		t.Errorf("active page0 currentPage0Interval = %v, want %v", got, INTERVAL_PAGE0_ACTIVE)
	}
	if got := currentLinkInterval(); got != INTERVAL_LINK_ACTIVE {
		t.Errorf("active currentLinkInterval = %v, want %v", got, INTERVAL_LINK_ACTIVE)
	}

	currPageIdx = 3
	if got := currentPage0Interval(); got != INTERVAL_PAGE0_IDLE {
		t.Errorf("active page3 currentPage0Interval = %v, want %v", got, INTERVAL_PAGE0_IDLE)
	}

	if !isPage0(0) {
		t.Error("isPage0(0) = false, want true")
	}
	if isPage0(2) {
		t.Error("isPage0(2) = true, want false")
	}
}

func TestMiscCovUpdateIntervals(t *testing.T) {
	savedIdle, savedPage := idleState, currPageIdx
	savedDesired, savedBase := desiredFPS, baseFPS
	savedBattery, savedNetwork := batteryDataInterval, networkGatherInterval
	savedData, savedPing := dataGatherInterval, pingGatherInterval
	t.Cleanup(func() {
		idleState, currPageIdx = savedIdle, savedPage
		desiredFPS, baseFPS = savedDesired, savedBase
		batteryDataInterval, networkGatherInterval = savedBattery, savedNetwork
		dataGatherInterval, pingGatherInterval = savedData, savedPing
		miscCovDrainAllReschedules()
	})

	baseFPS = DEFAULT_FPS

	idleState = STATE_IDLE
	updateIntervals()
	if desiredFPS != 1 {
		t.Errorf("idle desiredFPS = %d, want 1", desiredFPS)
	}
	if batteryDataInterval != INTERVAL_PAGE0_IDLE {
		t.Errorf("idle batteryDataInterval = %v, want %v", batteryDataInterval, INTERVAL_PAGE0_IDLE)
	}

	idleState = STATE_ACTIVE
	currPageIdx = 0
	updateIntervals()
	if desiredFPS != baseFPS {
		t.Errorf("active desiredFPS = %d, want baseFPS %d", desiredFPS, baseFPS)
	}
	if batteryDataInterval != INTERVAL_PAGE0_ACTIVE {
		t.Errorf("active batteryDataInterval = %v, want %v", batteryDataInterval, INTERVAL_PAGE0_ACTIVE)
	}

	// Every collector wake channel got a token (coalesced across both calls).
	for i, ch := range page0Reschedule {
		if miscCovDrain(ch) == 0 {
			t.Errorf("page0Reschedule[%d] was not signalled", i)
		}
	}
	if miscCovDrain(linkReschedule) == 0 {
		t.Error("linkReschedule was not signalled")
	}
}

func TestMiscCovSignalHelpers(t *testing.T) {
	t.Cleanup(miscCovDrainAllReschedules)
	miscCovDrainAllReschedules()

	// Two signals in a row: the second lands on a full buffer and must not
	// block; exactly one token remains.
	signalLinuxReschedule()
	signalLinuxReschedule()
	if n := miscCovDrain(linuxReschedule); n != 1 {
		t.Errorf("linuxReschedule held %d tokens, want the coalesced 1", n)
	}

	signalLinkReschedule()
	signalLinkReschedule()
	if n := miscCovDrain(linkReschedule); n != 1 {
		t.Errorf("linkReschedule held %d tokens, want 1", n)
	}

	signalPage0Reschedule()
	signalPage0Reschedule()
	for i, ch := range page0Reschedule {
		if n := miscCovDrain(ch); n != 1 {
			t.Errorf("page0Reschedule[%d] held %d tokens, want 1", i, n)
		}
	}

	signalPingReschedule()
	signalPingReschedule()
	for i, ch := range pingReschedule {
		if n := miscCovDrain(ch); n != 1 {
			t.Errorf("pingReschedule[%d] held %d tokens, want 1", i, n)
		}
	}

	signalPageChange()
	signalPageChange()
	if n := miscCovDrain(pageChangeSignal); n != 1 {
		t.Errorf("pageChangeSignal held %d tokens, want 1", n)
	}
}

func TestMiscCovTimingHelpers(t *testing.T) {
	if got := durationToMs(1500 * time.Millisecond); got != 1500.0 {
		t.Errorf("durationToMs(1.5s) = %v, want 1500.0", got)
	}
	if got := durationToMs(0); got != 0.0 {
		t.Errorf("durationToMs(0) = %v, want 0.0", got)
	}
	if got := formatTiming(1234 * time.Microsecond); got != "1.2ms" {
		t.Errorf("formatTiming(1234us) = %q, want \"1.2ms\"", got)
	}
	if got := formatTiming(2500 * time.Millisecond); got != "2500.0ms" {
		t.Errorf("formatTiming(2.5s) = %q, want \"2500.0ms\"", got)
	}
}

func TestMiscCovDoubleBufferAndPool(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 2, 2))
	b := image.NewRGBA(image.Rect(0, 0, 2, 2))
	db := &DoubleBuffer{buffers: [2]*image.RGBA{a, b}, active: 1}
	if got := db.GetActive(); got != b {
		t.Error("GetActive did not return the active buffer")
	}
	db.active = 0
	if got := db.GetActive(); got != a {
		t.Error("GetActive did not follow the active index")
	}

	bm := NewBufferManager()
	frame := bm.GetFrameFromPool(10, 20)
	if frame.Bounds().Dx() != 10 || frame.Bounds().Dy() != 20 {
		t.Errorf("GetFrameFromPool bounds = %v, want 10x20", frame.Bounds())
	}
	// ReturnFrameToPool is a deliberate no-op; it must accept any input.
	bm.ReturnFrameToPool(frame)
	bm.ReturnFrameToPool(nil)
}

func TestMiscCovFramebufferGetters(t *testing.T) {
	// The getters index package-level arrays populated by initLegacyBuffers;
	// run it so the slots are non-nil regardless of test order.
	initLegacyBuffers()
	initLegacyTransitionBuffers() // compatibility no-op, still part of init

	tests := []struct {
		name string
		get  func(int) *image.RGBA
		arr  *[2]*image.RGBA
	}{
		{"topBar", getTopBarFramebuffer, &topBarFramebuffers},
		{"middle", getMiddleFramebuffer, &middleFramebuffers},
		{"footer", getFooterFramebuffer, &footerFramebuffers},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.get(0); got != tt.arr[0] {
				t.Error("index 0 did not return the first buffer")
			}
			if got := tt.get(1); got != tt.arr[1] {
				t.Error("index 1 did not return the second buffer")
			}
			if got := tt.get(-1); got != tt.arr[0] {
				t.Error("negative index did not fall back to buffer 0")
			}
			if got := tt.get(99); got != tt.arr[0] {
				t.Error("out-of-range index did not fall back to buffer 0")
			}
		})
	}
}

func TestMiscCovCheckDMAAvailabilityWrapper(t *testing.T) {
	const txPath = "/sys/devices/platform/soc/2ad00000.spi/dma:tx"
	err := checkDMAAvailability()
	if _, statErr := os.Stat(txPath); os.IsNotExist(statErr) {
		if err == nil {
			t.Errorf("checkDMAAvailability = nil although %s does not exist", txPath)
		}
	} else if err != nil {
		t.Errorf("checkDMAAvailability = %v although the TX channel exists", err)
	}
}

func TestMiscCovGetSPIConfigAndLogSPIMode(t *testing.T) {
	saved := spiTransferOptimized
	t.Cleanup(func() { spiTransferOptimized = saved })

	spiTransferOptimized = true
	cfg := getSPIConfig()
	if cfg.MaxTransferSize != 65536 || cfg.UseChunking || cfg.BufferStrategy != "dma_optimized" {
		t.Errorf("optimized SPIConfig = %+v", cfg)
	}
	logSPIMode()

	spiTransferOptimized = false
	cfg = getSPIConfig()
	if cfg.MaxTransferSize != 4096 || !cfg.UseChunking || cfg.ChunkSize != 1024 || cfg.BufferStrategy != "interrupt_driven" {
		t.Errorf("non-optimized SPIConfig = %+v", cfg)
	}
	logSPIMode()
}

// NewDisplayWrapper and GetTransferStats only snapshot the SPI config around
// a device value — neither touches the panel, so they are safe off-hardware.
// (FillRectangleWithImageOptimized/fillRectangleChunked DO write to the device
// and stay untested here.)
func TestMiscCovDisplayWrapperStats(t *testing.T) {
	saved := spiTransferOptimized
	t.Cleanup(func() { spiTransferOptimized = saved })

	spiTransferOptimized = false
	dw := NewDisplayWrapper(display)
	stats := dw.GetTransferStats()
	if stats["transfer_strategy"] != "interrupt_driven" {
		t.Errorf("transfer_strategy = %v, want interrupt_driven", stats["transfer_strategy"])
	}
	if stats["use_chunking"] != true || stats["chunk_size"] != 1024 {
		t.Errorf("chunking stats = %v/%v, want true/1024", stats["use_chunking"], stats["chunk_size"])
	}

	spiTransferOptimized = true
	dw = NewDisplayWrapper(display)
	stats = dw.GetTransferStats()
	if stats["transfer_strategy"] != "dma_optimized" || stats["max_transfer_size"] != 65536 {
		t.Errorf("dma stats = %v/%v, want dma_optimized/65536",
			stats["transfer_strategy"], stats["max_transfer_size"])
	}
}

func TestMiscCovPageHasMatchBounds(t *testing.T) {
	configMutex.Lock()
	savedElements := cfg.DisplayTemplate.Elements
	savedNumPages := cfgNumPages
	cfg.DisplayTemplate.Elements = map[string][]DisplayElement{
		"page0": {
			{Type: "text", DataKey: "Ping0"},
			{Type: "cpu_bars"},
		},
		// page1 deliberately absent although cfgNumPages says it exists.
	}
	cfgNumPages = 2
	configMutex.Unlock()
	t.Cleanup(func() {
		configMutex.Lock()
		cfg.DisplayTemplate.Elements = savedElements
		cfgNumPages = savedNumPages
		configMutex.Unlock()
	})

	if !pageShowsPing(0) {
		t.Error("pageShowsPing(0) = false, want true for a Ping0 element")
	}
	if !pageShowsCpuBars(0) {
		t.Error("pageShowsCpuBars(0) = false, want true for a cpu_bars element")
	}
	if pageShowsPing(1) {
		t.Error("pageShowsPing(1) = true for a page with no element map")
	}
	if pageHasMatch(-1, func(DisplayElement) bool { return true }) {
		t.Error("pageHasMatch(-1) = true, want false out of range")
	}
	if pageHasMatch(5, func(DisplayElement) bool { return true }) {
		t.Error("pageHasMatch(5) = true, want false beyond cfgNumPages")
	}
	if pageHasMatch(0, func(DisplayElement) bool { return false }) {
		t.Error("pageHasMatch with a never-matching predicate returned true")
	}
}

func TestMiscCovCalculateTransitionFramesAsync(t *testing.T) {
	// Fresh buffers and a fresh easing table sized for the current dimensions.
	initTransitionFrameBuffers()
	easing := preCalculateEasing(numIntermediatePages, middleFrameWidth)

	transitionMutex.Lock()
	savedReady, savedCalculating := transitionFramesReady, transitionCalculating
	transitionFramesReady, transitionCalculating = false, false
	transitionMutex.Unlock()
	t.Cleanup(func() {
		transitionMutex.Lock()
		transitionFramesReady, transitionCalculating = savedReady, savedCalculating
		transitionMutex.Unlock()
		for {
			select {
			case <-transitionFrameChannel:
				continue
			default:
			}
			break
		}
		for {
			select {
			case <-transitionCompleteChannel:
				continue
			default:
			}
			break
		}
	})

	// Invalid parameters: both early returns, no goroutine spawned.
	calculateTransitionFramesAsync(nil, easing)
	calculateTransitionFramesAsync(image.NewRGBA(image.Rect(0, 0, 1, 1)), []int{1, 2})

	// Already-calculating guard: the call returns without starting a new run.
	transitionMutex.Lock()
	transitionCalculating = true
	transitionMutex.Unlock()
	calculateTransitionFramesAsync(image.NewRGBA(image.Rect(0, 0, middleFrameWidth*2, middleFrameHeight)), easing)
	transitionMutex.Lock()
	transitionCalculating = false
	transitionMutex.Unlock()

	// Real run: a stitched double-width frame is sliced into transition frames
	// and completion is flagged.
	stitched := image.NewRGBA(image.Rect(0, 0, middleFrameWidth*2, middleFrameHeight))
	for i := range stitched.Pix {
		stitched.Pix[i] = byte(i)
	}
	calculateTransitionFramesAsync(stitched, easing)

	deadline := time.Now().Add(5 * time.Second)
	for {
		transitionMutex.RLock()
		ready := transitionFramesReady
		transitionMutex.RUnlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("transition frames never became ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
