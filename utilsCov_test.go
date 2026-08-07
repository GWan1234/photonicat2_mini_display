package main

// Coverage tests for utils.go and timezone.go. Everything hardware- or
// filesystem-shaped goes through the utilCov* seams (package-level path vars
// defaulting to the real /sys and /etc locations), pointed at t.TempDir()
// fixtures here — the real system paths are never written.
//
// Every mutated global is saved and restored via t.Cleanup so the suite stays
// order-independent under -shuffle and clean under -race.

import (
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// shared fixtures

// utilCovSetBacklightFile points the backlight seam at a temp file with the
// given initial content and returns its path.
func utilCovSetBacklightFile(t *testing.T, initial string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "brightness")
	if err := os.WriteFile(p, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := utilCovBacklightPath
	utilCovBacklightPath = p
	t.Cleanup(func() { utilCovBacklightPath = saved })
	return p
}

// utilCovSaveBacklightState snapshots the brightness bookkeeping shared with
// setBacklight and restores it on cleanup, stopping any timer the test left.
func utilCovSaveBacklightState(t *testing.T) {
	t.Helper()
	mu.Lock()
	savedLast, savedTimer := lastLogical, offTimer
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		if offTimer != nil && offTimer != savedTimer {
			offTimer.Stop()
		}
		lastLogical, offTimer = savedLast, savedTimer
		mu.Unlock()
	})
}

// utilCovSaveCfg snapshots the merged/default/user configs and the derived
// globals mergeConfigs touches.
func utilCovSaveCfg(t *testing.T) {
	t.Helper()
	savedCfg, savedDft, savedUser := cfg, dftCfg, userCfg
	savedNumPages, savedTotal := cfgNumPages, totalNumPages
	savedLocalExists := localConfigExists
	savedBg, savedTop, savedFooter := screenBgColor, cacheTopBarStr, cacheFooterStr
	t.Cleanup(func() {
		cfg, dftCfg, userCfg = savedCfg, savedDft, savedUser
		cfgNumPages, totalNumPages = savedNumPages, savedTotal
		localConfigExists = savedLocalExists
		screenBgColor, cacheTopBarStr, cacheFooterStr = savedBg, savedTop, savedFooter
	})
}

// utilCovSaveFonts swaps in a private font table and restores the original.
func utilCovSaveFonts(t *testing.T, table map[string]FontConfig) {
	t.Helper()
	saved := fonts
	fonts = table
	t.Cleanup(func() { fonts = saved })
}

func utilCovReadFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// backlight

func TestUtilCovSetBacklightWritesAndClamps(t *testing.T) {
	p := utilCovSetBacklightFile(t, "0")
	utilCovSaveBacklightState(t)
	utilCovSaveCfg(t)
	cfg.ScreenMinBrightness = 5
	cfg.ScreenMaxBrightness = 80

	mu.Lock()
	lastLogical = -1 // impossible value so the first write always happens
	offTimer = nil
	mu.Unlock()

	setBacklight(50)
	if got := utilCovReadFile(t, p); got != "50" {
		t.Errorf("brightness file = %q, want \"50\"", got)
	}

	// Same value again: early return, no rewrite (coverage of the短 path).
	setBacklight(50)

	// Above max clamps to the effective ceiling.
	setBacklight(200)
	if got := utilCovReadFile(t, p); got != "80" {
		t.Errorf("brightness after over-max = %q, want \"80\"", got)
	}

	// Below min clamps up to screen_min_brightness.
	setBacklight(-10)
	if got := utilCovReadFile(t, p); got != "5" {
		t.Errorf("brightness after under-min = %q, want \"5\"", got)
	}
}

func TestUtilCovSetBacklightZeroArmsOffTimerAndWakeCancelsIt(t *testing.T) {
	p := utilCovSetBacklightFile(t, "0")
	utilCovSaveBacklightState(t)
	utilCovSaveCfg(t)
	cfg.ScreenMinBrightness = 0
	cfg.ScreenMaxBrightness = 100

	mu.Lock()
	lastLogical = -1
	offTimer = nil
	mu.Unlock()

	setBacklight(0)
	if got := utilCovReadFile(t, p); got != "0" {
		t.Errorf("brightness = %q, want \"0\"", got)
	}
	mu.Lock()
	armed := offTimer != nil
	mu.Unlock()
	if !armed {
		t.Fatal("logical 0 did not arm the confirm-off timer")
	}

	// Going bright again must cancel the pending off-timer.
	setBacklight(70)
	mu.Lock()
	cleared := offTimer == nil
	mu.Unlock()
	if !cleared {
		t.Error("wake did not cancel the pending off-timer")
	}
	if got := utilCovReadFile(t, p); got != "70" {
		t.Errorf("brightness = %q, want \"70\"", got)
	}
}

// A failing sysfs write is logged, never fatal — the daemon must survive a
// missing backlight node.
func TestUtilCovSetBacklightWriteFailureIsNonFatal(t *testing.T) {
	utilCovSaveBacklightState(t)
	utilCovSaveCfg(t)
	cfg.ScreenMinBrightness = 0
	cfg.ScreenMaxBrightness = 100

	saved := utilCovBacklightPath
	utilCovBacklightPath = filepath.Join(t.TempDir(), "no-such-dir", "brightness")
	t.Cleanup(func() { utilCovBacklightPath = saved })

	mu.Lock()
	lastLogical = -1
	offTimer = nil
	mu.Unlock()

	setBacklight(40) // must not panic; error path is logged
	mu.Lock()
	got := lastLogical
	mu.Unlock()
	if got != 40 {
		t.Errorf("lastLogical = %d, want 40 even when the write fails", got)
	}
}

func TestUtilCovGetBacklight(t *testing.T) {
	utilCovSetBacklightFile(t, "42\n")
	if got := getBacklight(); got != 42 {
		t.Errorf("getBacklight() = %d, want 42", got)
	}

	utilCovSetBacklightFile(t, "not-a-number")
	if got := getBacklight(); got != 0 {
		t.Errorf("getBacklight() on garbage = %d, want 0", got)
	}

	saved := utilCovBacklightPath
	utilCovBacklightPath = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { utilCovBacklightPath = saved })
	if got := getBacklight(); got != 0 {
		t.Errorf("getBacklight() on missing file = %d, want 0", got)
	}
}

// utilCovFadeSetup prepares an isolated fade: temp brightness file, clean
// bookkeeping, permissive limits, and a fresh cancel channel.
func utilCovFadeSetup(t *testing.T, initial string) string {
	t.Helper()
	p := utilCovSetBacklightFile(t, initial)
	utilCovSaveBacklightState(t)
	utilCovSaveCfg(t)
	cfg.ScreenMinBrightness = 0
	cfg.ScreenMaxBrightness = 100

	mu.Lock()
	lastLogical = -1
	offTimer = nil
	mu.Unlock()

	fadeMu.Lock()
	savedCancel := fadeCancel
	fadeCancel = make(chan struct{})
	fadeMu.Unlock()
	t.Cleanup(func() {
		fadeMu.Lock()
		fadeCancel = savedCancel
		fadeMu.Unlock()
	})
	return p
}

func TestUtilCovFadeBacklightImmediatePaths(t *testing.T) {
	p := utilCovFadeSetup(t, "10")

	// Zero period → jump straight to the target.
	fadeBacklight(30, 0)
	if got := utilCovReadFile(t, p); got != "30" {
		t.Errorf("after zero-period fade file = %q, want \"30\"", got)
	}

	// Already at the target → single set, no ticking.
	mu.Lock()
	lastLogical = -1
	mu.Unlock()
	fadeBacklight(30, time.Second)
	if got := utilCovReadFile(t, p); got != "30" {
		t.Errorf("no-op fade rewrote file to %q", got)
	}

	// Period shorter than one 40ms step → direct set.
	fadeBacklight(55, 10*time.Millisecond)
	if got := utilCovReadFile(t, p); got != "55" {
		t.Errorf("sub-step fade file = %q, want \"55\"", got)
	}
}

func TestUtilCovFadeBacklightRampsToTarget(t *testing.T) {
	p := utilCovFadeSetup(t, "10")

	fadeBacklight(20, 120*time.Millisecond) // 3 steps of 40ms
	if got := utilCovReadFile(t, p); got != "20" {
		t.Errorf("after ramp file = %q, want \"20\"", got)
	}
	mu.Lock()
	final := lastLogical
	mu.Unlock()
	if final != 20 {
		t.Errorf("lastLogical = %d, want 20", final)
	}
}

func TestUtilCovFadeBacklightCancel(t *testing.T) {
	utilCovFadeSetup(t, "0")

	done := make(chan struct{})
	go func() {
		defer close(done)
		fadeBacklight(100, 2*time.Second)
	}()

	time.Sleep(60 * time.Millisecond) // let at least one step land
	fadeMu.Lock()
	close(fadeCancel)
	fadeMu.Unlock()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("fadeBacklight did not stop after cancel")
	}
	mu.Lock()
	stopped := lastLogical
	mu.Unlock()
	if stopped >= 100 {
		t.Errorf("fade reached %d despite cancel", stopped)
	}
}

// ---------------------------------------------------------------------------
// displayWake / monitors / idle helpers

func TestUtilCovDisplayWake(t *testing.T) {
	// Contain the collector goroutines displayWake kicks off: local web calls
	// go to a stub server, and the public-IP fetcher is held in its "already
	// fetching" state so nothing ever leaves the process.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	redirectLocalHTTP(t, srv)

	publicIPMu.Lock()
	savedFetching := publicIPFetching
	publicIPFetching = true
	publicIPMu.Unlock()

	utilCovSaveCfg(t)
	cfg.ScreenDimmerTimeOnBatterySeconds = 30
	cfg.ScreenDimmerTimeOnDCSeconds = 300

	savedIdleTimeout := idleTimeout
	savedForceTop, savedForceMiddle := forceTopBarRedraw, forceMiddleRedraw
	savedTopStr := cacheTopBarStr
	lastActivityMu.Lock()
	savedActivity := lastActivity
	lastActivityMu.Unlock()

	forceTopBarRedraw, forceMiddleRedraw = false, false
	cacheTopBarStr = "stale"

	displayWake()

	if cacheTopBarStr != "" {
		t.Error("displayWake did not invalidate the top-bar cache")
	}
	if !forceTopBarRedraw || !forceMiddleRedraw {
		t.Errorf("displayWake redraw flags = top:%v middle:%v, want both true",
			forceTopBarRedraw, forceMiddleRedraw)
	}

	// Let the fire-and-forget collectors finish against the stub before the
	// transport is restored.
	time.Sleep(300 * time.Millisecond)

	publicIPMu.Lock()
	publicIPFetching = savedFetching
	publicIPMu.Unlock()
	idleTimeout = savedIdleTimeout
	forceTopBarRedraw, forceMiddleRedraw = savedForceTop, savedForceMiddle
	cacheTopBarStr = savedTopStr
	lastActivityMu.Lock()
	lastActivity = savedActivity
	lastActivityMu.Unlock()
}

// On a machine without the rk805 pwrkey evdev device the keyboard monitor must
// log and return instead of hanging or panicking.
func TestUtilCovMonitorKeyboardNoDeviceReturns(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		var triggered bool
		monitorKeyboard(&triggered)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Skip("an input device answered; cannot exercise the no-device path here")
	}
}

func utilCovDrainPageChange() {
	select {
	case <-pageChangeSignal:
	default:
	}
}

func TestUtilCovConsoleEnter(t *testing.T) {
	savedState := idleState
	savedIdleFlag := wasConsoleScreenIdle
	savedSwipe := swippingScreen
	lastActivityMu.Lock()
	savedActivity := lastActivity
	lastActivityMu.Unlock()
	t.Cleanup(func() {
		idleState = savedState
		wasConsoleScreenIdle = savedIdleFlag
		swippingScreen = savedSwipe
		lastActivityMu.Lock()
		lastActivity = savedActivity
		lastActivityMu.Unlock()
		utilCovDrainPageChange()
	})

	// Idle screen: ENTER wakes without a page change.
	idleState = STATE_IDLE
	wasConsoleScreenIdle = false
	swippingScreen = false
	triggered := false
	utilCovConsoleEnter(&triggered)
	if !wasConsoleScreenIdle || triggered {
		t.Errorf("idle wake: wasConsoleScreenIdle=%v triggered=%v, want true/false",
			wasConsoleScreenIdle, triggered)
	}

	// Active screen right after a wake: the flag swallows the page change once.
	idleState = STATE_ACTIVE
	utilCovConsoleEnter(&triggered)
	if wasConsoleScreenIdle || triggered {
		t.Errorf("post-wake ENTER: wasConsoleScreenIdle=%v triggered=%v, want false/false",
			wasConsoleScreenIdle, triggered)
	}

	// Active screen, no pending wake: page change fires.
	utilCovDrainPageChange()
	utilCovConsoleEnter(&triggered)
	if !triggered || !swippingScreen {
		t.Errorf("active ENTER: triggered=%v swipping=%v, want true/true", triggered, swippingScreen)
	}
	select {
	case <-pageChangeSignal:
	default:
		t.Error("page change was not signalled")
	}

	// An unknown state only refreshes the activity clock.
	idleState = STATE_UNKNOWN
	lastActivityMu.Lock()
	lastActivity = time.Now().Add(-time.Hour)
	lastActivityMu.Unlock()
	triggered = false
	utilCovConsoleEnter(&triggered)
	if triggered {
		t.Error("unknown state must not change pages")
	}
	lastActivityMu.Lock()
	fresh := time.Since(lastActivity) < time.Minute
	lastActivityMu.Unlock()
	if !fresh {
		t.Error("lastActivity was not refreshed")
	}
}

func TestUtilCovIdleStateFor(t *testing.T) {
	savedSwipe := swippingScreen
	savedRunning := weAreRunning()
	savedTimeout := idleTimeout
	t.Cleanup(func() {
		swippingScreen = savedSwipe
		setWeAreRunning(savedRunning)
		idleTimeout = savedTimeout
	})

	setWeAreRunning(true)
	idleTimeout = 10 * time.Second // fadeInDur=300ms, fadeDuration=2s

	swippingScreen = true
	if got := utilCovIdleStateFor(0); got != STATE_ACTIVE {
		t.Errorf("fresh activity while swiping = %s, want ACTIVE", stateName(got))
	}
	swippingScreen = false
	if got := utilCovIdleStateFor(0); got != STATE_FADE_IN {
		t.Errorf("fresh activity = %s, want FADE_IN", stateName(got))
	}

	swippingScreen = true
	if got := utilCovIdleStateFor(5 * time.Second); got != STATE_ACTIVE {
		t.Errorf("mid-idle = %s, want ACTIVE", stateName(got))
	}
	if swippingScreen {
		t.Error("ACTIVE window must clear swippingScreen")
	}

	if got := utilCovIdleStateFor(11 * time.Second); got != STATE_FADE_OUT {
		t.Errorf("past timeout = %s, want FADE_OUT", stateName(got))
	}
	if got := utilCovIdleStateFor(time.Minute); got != STATE_IDLE {
		t.Errorf("long idle = %s, want IDLE", stateName(got))
	}

	setWeAreRunning(false)
	if got := utilCovIdleStateFor(0); got != STATE_OFF {
		t.Errorf("shutting down = %s, want OFF", stateName(got))
	}
}

func TestUtilCovMovementKick(t *testing.T) {
	savedPath := utilCovMovementTriggerPath
	lastActivityMu.Lock()
	savedActivity := lastActivity
	lastActivityMu.Unlock()
	t.Cleanup(func() {
		utilCovMovementTriggerPath = savedPath
		lastActivityMu.Lock()
		lastActivity = savedActivity
		lastActivityMu.Unlock()
	})

	setActivity := func(tm time.Time) {
		lastActivityMu.Lock()
		lastActivity = tm
		lastActivityMu.Unlock()
	}
	getActivity := func() time.Time {
		lastActivityMu.Lock()
		defer lastActivityMu.Unlock()
		return lastActivity
	}

	dir := t.TempDir()
	trigger := filepath.Join(dir, "movement_trigger")

	// Missing file: nothing happens.
	utilCovMovementTriggerPath = filepath.Join(dir, "missing")
	old := time.Now().Add(-time.Hour)
	setActivity(old)
	utilCovMovementKick()
	if !getActivity().Equal(old) {
		t.Error("missing trigger file changed lastActivity")
	}

	// "0": no movement, nothing happens.
	utilCovMovementTriggerPath = trigger
	if err := os.WriteFile(trigger, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	utilCovMovementKick()
	if !getActivity().Equal(old) {
		t.Error("trigger \"0\" changed lastActivity")
	}

	// "1" with a stale timer: pulled up to ~now-2s.
	if err := os.WriteFile(trigger, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	utilCovMovementKick()
	if age := time.Since(getActivity()); age > 3*time.Second {
		t.Errorf("movement did not refresh lastActivity (age %v)", age)
	}

	// "1" with fresh activity: the timer never moves backwards.
	fresh := time.Now()
	setActivity(fresh)
	utilCovMovementKick()
	if getActivity().Before(fresh) {
		t.Error("movement moved lastActivity backwards")
	}
}

// ---------------------------------------------------------------------------
// fonts

func TestUtilCovParseFontFile(t *testing.T) {
	// Single .ttf parses and is cached by path (same pointer on re-read).
	f1, err := parseFontFile("assets/fonts/Orbitron-Regular.ttf")
	if err != nil {
		t.Fatalf("parse ttf: %v", err)
	}
	f2, err := parseFontFile("assets/fonts/Orbitron-Regular.ttf")
	if err != nil {
		t.Fatalf("re-parse ttf: %v", err)
	}
	if f1 != f2 {
		t.Error("parsed font was not served from the path cache")
	}

	// TrueType collection (.ttc) takes the collection branch.
	if _, err := parseFontFile("assets/fonts/NotoSansMonoCJK-VF.ttf.ttc"); err != nil {
		t.Fatalf("parse ttc: %v", err)
	}

	// Missing file.
	if _, err := parseFontFile(filepath.Join(t.TempDir(), "nope.ttf")); err == nil {
		t.Error("missing font file did not error")
	}

	// Garbage bytes: both the single-font and collection parsers must reject.
	dir := t.TempDir()
	badTTF := filepath.Join(dir, "bad.ttf")
	badTTC := filepath.Join(dir, "bad.ttc")
	for _, p := range []string{badTTF, badTTC} {
		if err := os.WriteFile(p, []byte("this is not a font"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := parseFontFile(badTTF); err == nil {
		t.Error("garbage .ttf did not error")
	}
	if _, err := parseFontFile(badTTC); err == nil {
		t.Error("garbage .ttc did not error")
	}
}

func TestUtilCovGetFontFace(t *testing.T) {
	utilCovSaveFonts(t, map[string]FontConfig{
		"utilCovReg": {FontPath: "assets/fonts/Orbitron-Regular.ttf", FontSize: 18},
		"utilCovBad": {FontPath: filepath.Join(t.TempDir(), "missing.ttf"), FontSize: 12},
	})

	face, height, err := getFontFace("utilCovReg")
	if err != nil {
		t.Fatalf("getFontFace: %v", err)
	}
	if face == nil || height <= 0 {
		t.Fatalf("face=%v height=%d, want a usable face", face, height)
	}

	// Second call is the cache hit.
	face2, height2, err := getFontFace("utilCovReg")
	if err != nil {
		t.Fatalf("cached getFontFace: %v", err)
	}
	if face2 != face || height2 != height {
		t.Error("cache returned a different face")
	}

	if _, _, err := getFontFace("utilCovNotRegistered"); err == nil {
		t.Error("unknown font name did not error")
	}
	if _, _, err := getFontFace("utilCovBad"); err == nil {
		t.Error("unreadable font path did not error")
	}
}

func TestUtilCovGetFontFaceForText(t *testing.T) {
	// Give the CJK variant a different size so the two faces are
	// distinguishable by height.
	utilCovSaveFonts(t, map[string]FontConfig{
		"utilCovTxt":     {FontPath: "assets/fonts/Orbitron-Regular.ttf", FontSize: 18},
		"utilCovTxt_cjk": {FontPath: "assets/fonts/Orbitron-Regular.ttf", FontSize: 36},
	})

	_, hLatin, err := getFontFaceForText("utilCovTxt", "Hello")
	if err != nil {
		t.Fatalf("latin: %v", err)
	}
	_, hCJK, err := getFontFaceForText("utilCovTxt", "网络测试")
	if err != nil {
		t.Fatalf("cjk: %v", err)
	}
	if hCJK <= hLatin {
		t.Errorf("CJK text did not select the _cjk face: hLatin=%d hCJK=%d", hLatin, hCJK)
	}

	// A CJK string with no _cjk variant registered errors.
	if _, _, err := getFontFaceForText("utilCovOnlyLatin", "中文"); err == nil {
		t.Error("missing _cjk variant did not error")
	}
}

func TestUtilCovPreloadFonts(t *testing.T) {
	utilCovSaveFonts(t, map[string]FontConfig{
		"utilCovWarm":   {FontPath: "assets/fonts/Orbitron-Medium.ttf", FontSize: 20},
		"utilCovBroken": {FontPath: filepath.Join(t.TempDir(), "gone.ttf"), FontSize: 20},
	})

	preloadFonts() // must warm the good face and only log the broken one

	fontCacheMu.Lock()
	_, warmed := fontCache["utilCovWarm"]
	_, broken := fontCache["utilCovBroken"]
	fontCacheMu.Unlock()
	if !warmed {
		t.Error("preloadFonts did not warm the loadable face")
	}
	if broken {
		t.Error("preloadFonts cached a face that failed to load")
	}
}

// ---------------------------------------------------------------------------
// frame clearing

func TestUtilCovClearFrameGrowsShortPixBuffer(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 4, 4))
	frame.Pix = frame.Pix[:8] // simulate an undersized buffer
	clearFrame(frame, 4, 4)
	if len(frame.Pix) < 4*4*4 {
		t.Fatalf("clearFrame left Pix at %d bytes, want >= %d", len(frame.Pix), 4*4*4)
	}
	c := frame.RGBAAt(3, 3)
	if c != screenBgColor {
		t.Errorf("pixel = %+v, want %+v", c, screenBgColor)
	}
}

func TestUtilCovClearFrameFollowsBgColorChange(t *testing.T) {
	savedBg := screenBgColor
	savedTop, savedFooter := cacheTopBarStr, cacheFooterStr
	t.Cleanup(func() {
		screenBgColor = savedBg
		cacheTopBarStr, cacheFooterStr = savedTop, savedFooter
	})

	setScreenBgColor([]int{10, 20, 30})
	frame := image.NewRGBA(image.Rect(0, 0, 8, 8))
	clearFrame(frame, 8, 8)
	c := frame.RGBAAt(2, 2)
	if c.R != 10 || c.G != 20 || c.B != 30 || c.A != 255 {
		t.Errorf("pixel = %+v, want (10,20,30,255)", c)
	}

	// Anything other than [R,G,B] resets to black.
	setScreenBgColor(nil)
	clearFrame(frame, 8, 8)
	c = frame.RGBAAt(2, 2)
	if c.R != 0 || c.G != 0 || c.B != 0 {
		t.Errorf("pixel after reset = %+v, want black", c)
	}
}

// ---------------------------------------------------------------------------
// config loading

func TestUtilCovLoadConfig(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	os.WriteFile(good, []byte(`{"show_sms": true, "ping_site0": "1.2.3.4"}`), 0o644)

	c, err := loadConfig(good)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if !c.ShowSms || c.PingSite0 != "1.2.3.4" {
		t.Errorf("parsed config = %+v", c)
	}

	if _, err := loadConfig(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("missing file did not error")
	}

	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{nope"), 0o644)
	if _, err := loadConfig(bad); err == nil {
		t.Error("malformed JSON did not error")
	}
}

func TestUtilCovLoadConfigFromBytes(t *testing.T) {
	c, err := loadConfigFromBytes([]byte(`{"screen_max_brightness": 77}`))
	if err != nil {
		t.Fatalf("loadConfigFromBytes: %v", err)
	}
	if c.ScreenMaxBrightness != 77 {
		t.Errorf("ScreenMaxBrightness = %d, want 77", c.ScreenMaxBrightness)
	}
	if _, err := loadConfigFromBytes([]byte("not json")); err == nil {
		t.Error("garbage bytes did not error")
	}
}

func TestUtilCovEmbeddedDefaultConfigJSON(t *testing.T) {
	saved := utilCovIsOpenWRT
	t.Cleanup(func() { utilCovIsOpenWRT = saved })

	utilCovIsOpenWRT = func() bool { return false }
	debian := embeddedDefaultConfigJSON()
	if len(debian) == 0 {
		t.Fatal("embedded Debian config is empty")
	}
	if _, err := loadConfigFromBytes(debian); err != nil {
		t.Errorf("embedded Debian config does not parse: %v", err)
	}

	utilCovIsOpenWRT = func() bool { return true }
	openwrt := embeddedDefaultConfigJSON()
	if len(openwrt) == 0 {
		t.Fatal("embedded OpenWrt config is empty")
	}
	if _, err := loadConfigFromBytes(openwrt); err != nil {
		t.Errorf("embedded OpenWrt config does not parse: %v", err)
	}
}

func TestUtilCovDefaultConfigDiskPath(t *testing.T) {
	saved := utilCovIsOpenWRT
	t.Cleanup(func() { utilCovIsOpenWRT = saved })

	utilCovIsOpenWRT = func() bool { return false }
	if got := defaultConfigDiskPath(); got != utilCovEtcDebianConfigPath {
		t.Errorf("debian path = %q, want %q", got, utilCovEtcDebianConfigPath)
	}
	utilCovIsOpenWRT = func() bool { return true }
	if got := defaultConfigDiskPath(); got != utilCovEtcConfigPath {
		t.Errorf("openwrt path = %q, want %q", got, utilCovEtcConfigPath)
	}
}

// utilCovSetUserConfigPaths points both user-config seams (cwd-local and /etc)
// into a temp dir and returns the two paths.
func utilCovSetUserConfigPaths(t *testing.T) (local, etc string) {
	t.Helper()
	dir := t.TempDir()
	local = filepath.Join(dir, "user_config.json")
	etc = filepath.Join(dir, "etc-user_config.json")
	savedLocal, savedEtc := utilCovLocalUserConfigPath, utilCovEtcUserConfigPath
	utilCovLocalUserConfigPath, utilCovEtcUserConfigPath = local, etc
	t.Cleanup(func() {
		utilCovLocalUserConfigPath, utilCovEtcUserConfigPath = savedLocal, savedEtc
	})
	return local, etc
}

func TestUtilCovHasShowSmsInUserConfig(t *testing.T) {
	local, etc := utilCovSetUserConfigPaths(t)

	// Local file with the key present (value irrelevant).
	os.WriteFile(local, []byte(`{"show_sms": false}`), 0o644)
	if !hasShowSmsInUserConfig() {
		t.Error("show_sms present in local config but not detected")
	}

	// Local file without the key.
	os.WriteFile(local, []byte(`{"other": 1}`), 0o644)
	if hasShowSmsInUserConfig() {
		t.Error("show_sms absent but reported present")
	}

	// Local file malformed → false, not a crash.
	os.WriteFile(local, []byte("{bad"), 0o644)
	if hasShowSmsInUserConfig() {
		t.Error("malformed local config reported show_sms present")
	}

	// No local file → falls back to the /etc seam.
	os.Remove(local)
	os.WriteFile(etc, []byte(`{"show_sms": true}`), 0o644)
	if !hasShowSmsInUserConfig() {
		t.Error("show_sms present in etc config but not detected")
	}

	// Neither file readable → false.
	os.Remove(etc)
	if hasShowSmsInUserConfig() {
		t.Error("no config anywhere but show_sms reported present")
	}
}

// utilCovSetupLoadAll wires every config seam into temp files for a
// loadAllConfigsToVariables run and snapshots the globals it rewrites.
func utilCovSetupLoadAll(t *testing.T) (local, etc, diskDefault string) {
	t.Helper()
	utilCovSaveCfg(t)
	local, etc = utilCovSetUserConfigPaths(t)
	diskDefault = filepath.Join(t.TempDir(), "default-config.json")
	savedDisk := utilCovEtcDebianConfigPath
	savedIsOW := utilCovIsOpenWRT
	utilCovEtcDebianConfigPath = diskDefault
	utilCovIsOpenWRT = func() bool { return false }
	t.Cleanup(func() {
		utilCovEtcDebianConfigPath = savedDisk
		utilCovIsOpenWRT = savedIsOW
	})
	return local, etc, diskDefault
}

func TestUtilCovLoadAllConfigsWithLocalUserOverride(t *testing.T) {
	local, _, diskDefault := utilCovSetupLoadAll(t)
	os.WriteFile(local, []byte(`{"ping_site0": "9.9.9.9"}`), 0o644)

	loadAllConfigsToVariables()

	if cfg.PingSite0 != "9.9.9.9" {
		t.Errorf("user override lost: PingSite0 = %q", cfg.PingSite0)
	}
	if !localConfigExists {
		t.Error("localConfigExists not set")
	}
	// The embedded default must have been mirrored to the disk seam.
	if _, err := os.Stat(diskDefault); err != nil {
		t.Errorf("embedded default was not mirrored to disk: %v", err)
	}
	if dftCfg.ScreenMaxBrightness == 0 {
		t.Error("dftCfg looks unpopulated")
	}
}

func TestUtilCovLoadAllConfigsCreatesEmptyEtcUserConfig(t *testing.T) {
	_, etc, _ := utilCovSetupLoadAll(t)

	loadAllConfigsToVariables()

	got, err := os.ReadFile(etc)
	if err != nil {
		t.Fatalf("empty etc user config was not created: %v", err)
	}
	if strings.TrimSpace(string(got)) != "{}" {
		t.Errorf("etc user config = %q, want {}", got)
	}
	// With no overrides the merged config equals the embedded default plus
	// post-merge fallbacks; spot-check one shipped field survived.
	if cfg.ScreenMaxBrightness != dftCfg.ScreenMaxBrightness {
		t.Errorf("cfg.ScreenMaxBrightness = %d, want default %d",
			cfg.ScreenMaxBrightness, dftCfg.ScreenMaxBrightness)
	}
}

func TestUtilCovLoadAllConfigsSurvivesUnreadableUserAndDiskFailure(t *testing.T) {
	local, etc, _ := utilCovSetupLoadAll(t)

	// Disk mirror fails (directory does not exist) — must be non-fatal.
	savedDisk := utilCovEtcDebianConfigPath
	utilCovEtcDebianConfigPath = filepath.Join(t.TempDir(), "no-such-dir", "cfg.json")
	t.Cleanup(func() { utilCovEtcDebianConfigPath = savedDisk })

	// Pre-existing etc user config that is malformed → empty overrides branch.
	os.Remove(local)
	os.WriteFile(etc, []byte("{malformed"), 0o644)

	loadAllConfigsToVariables()

	if !reflect.DeepEqual(userCfg, Config{}) {
		t.Error("malformed user config should leave empty overrides")
	}
	if cfg.PingSite0 == "" {
		t.Error("merged config missing post-merge ping fallback")
	}
}

func TestUtilCovLoadAllConfigsUncreatableEtcUserConfig(t *testing.T) {
	utilCovSetupLoadAll(t)
	// Neither user config exists and the /etc seam cannot be created — the
	// loader must still come up on the embedded default with empty overrides.
	savedEtc := utilCovEtcUserConfigPath
	utilCovEtcUserConfigPath = filepath.Join(t.TempDir(), "no-such-dir", "user.json")
	t.Cleanup(func() { utilCovEtcUserConfigPath = savedEtc })

	loadAllConfigsToVariables()

	if !reflect.DeepEqual(userCfg, Config{}) {
		t.Error("unreadable user config should leave empty overrides")
	}
	if dftCfg.ScreenMaxBrightness == 0 {
		t.Error("dftCfg looks unpopulated")
	}
}

func TestUtilCovLoadAllConfigsFallsBackToDefaultOnMergeFailure(t *testing.T) {
	local, _, _ := utilCovSetupLoadAll(t)
	// A negative dimmer timeout survives the merge and then fails validation,
	// forcing the cfg = dftCfg fallback.
	os.WriteFile(local, []byte(`{"screen_dimmer_time_on_battery_seconds": -1}`), 0o644)

	loadAllConfigsToVariables()

	if !reflect.DeepEqual(cfg, dftCfg) {
		t.Error("merge failure should fall back to the package default config")
	}
}

// ---------------------------------------------------------------------------
// mergeConfigs validation branches not pinned elsewhere

func TestUtilCovMergeConfigsValidation(t *testing.T) {
	utilCovSaveCfg(t)

	base := Config{ScreenMaxBrightness: 100, ScreenMinBrightness: 10,
		PingSite0: "1.1.1.1", PingSite1: "photonicat.com"}

	cases := []struct {
		name    string
		user    Config
		wantErr bool
	}{
		{"min_over_100", Config{ScreenMinBrightness: 101, ScreenMaxBrightness: 100}, true},
		{"max_over_100", Config{ScreenMaxBrightness: 150}, true},
		{"min_exceeds_max", Config{ScreenMinBrightness: 90, ScreenMaxBrightness: 50}, true},
		{"valid_pair", Config{ScreenMinBrightness: 20, ScreenMaxBrightness: 90}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dftCfg = base
			userCfg = tc.user
			err := mergeConfigs()
			if tc.wantErr && err == nil {
				t.Error("mergeConfigs accepted an invalid brightness config")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("mergeConfigs rejected a valid config: %v", err)
			}
		})
	}
}

func TestUtilCovMergeConfigsShowSmsReservesAPage(t *testing.T) {
	utilCovSaveCfg(t)

	dftCfg = Config{ScreenMaxBrightness: 100, PingSite0: "1.1.1.1", PingSite1: "photonicat.com",
		DisplayTemplate: DisplayTemplate{Elements: map[string][]DisplayElement{
			"page0": {{Type: "text", DataKey: "x"}},
			"page1": {{Type: "text", DataKey: "y"}},
		}}}
	userCfg = Config{ShowSms: true}

	if err := mergeConfigs(); err != nil {
		t.Fatalf("mergeConfigs: %v", err)
	}
	if cfgNumPages != 2 {
		t.Fatalf("cfgNumPages = %d, want 2", cfgNumPages)
	}
	if totalNumPages != 3 {
		t.Errorf("totalNumPages with SMS = %d, want cfgNumPages+1 = 3", totalNumPages)
	}

	userCfg = Config{}
	if err := mergeConfigs(); err != nil {
		t.Fatalf("mergeConfigs: %v", err)
	}
	if totalNumPages != 2 {
		t.Errorf("totalNumPages without SMS = %d, want 2", totalNumPages)
	}
}

// ---------------------------------------------------------------------------
// timezone.go

// utilCovSetTZPaths points the timezone resolver seams into a temp dir. The
// returned paths do not exist yet; tests create the ones a scenario needs.
func utilCovSetTZPaths(t *testing.T) (localtime, tz1, tz2 string) {
	t.Helper()
	dir := t.TempDir()
	localtime = filepath.Join(dir, "localtime")
	tz1 = filepath.Join(dir, "TZ1")
	tz2 = filepath.Join(dir, "TZ2")
	savedLocal, savedPosix := utilCovLocaltimePath, utilCovPosixTZPaths
	utilCovLocaltimePath = localtime
	utilCovPosixTZPaths = []string{tz1, tz2}
	t.Cleanup(func() {
		utilCovLocaltimePath, utilCovPosixTZPaths = savedLocal, savedPosix
	})
	return localtime, tz1, tz2
}

// utilCovZoneinfoBytes finds real TZif bytes on the host, or skips.
func utilCovZoneinfoBytes(t *testing.T) []byte {
	t.Helper()
	for _, p := range []string{
		"/usr/share/zoneinfo/Asia/Shanghai",
		"/var/db/timezone/zoneinfo/Asia/Shanghai",
	} {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return b
		}
	}
	t.Skip("no zoneinfo database on this host")
	return nil
}

func utilCovOffsetOf(t *testing.T, loc *time.Location) int {
	t.Helper()
	ref := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	_, off := ref.In(loc).Zone()
	return off
}

func TestUtilCovResolveTimezoneTZifWins(t *testing.T) {
	localtime, tz1, _ := utilCovSetTZPaths(t)
	os.WriteFile(localtime, utilCovZoneinfoBytes(t), 0o644)
	// A POSIX file that disagrees must lose to the TZif database.
	os.WriteFile(tz1, []byte("PST8"), 0o644)

	loc := resolveTimezone()
	if loc == nil {
		t.Fatal("resolveTimezone returned nil with a valid TZif file")
	}
	if off := utilCovOffsetOf(t, loc); off != 8*3600 {
		t.Errorf("offset = %d, want +8h from the TZif file", off)
	}
}

func TestUtilCovResolveTimezonePosixFallback(t *testing.T) {
	localtime, tz1, tz2 := utilCovSetTZPaths(t)
	t.Setenv("TZ", "") // keep the env tail out of this scenario

	// Corrupt TZif falls through; first POSIX file is unparseable ("XX-99"
	// has hours out of range), second one carries the answer.
	os.WriteFile(localtime, []byte("not a tzfile"), 0o644)
	os.WriteFile(tz1, []byte("XX-99"), 0o644)
	os.WriteFile(tz2, []byte("CST-8\n"), 0o644)

	loc := resolveTimezone()
	if loc == nil {
		t.Fatal("resolveTimezone returned nil, want the POSIX fallback")
	}
	if off := utilCovOffsetOf(t, loc); off != 8*3600 {
		t.Errorf("offset = %d, want +8h from the POSIX file", off)
	}
}

func TestUtilCovResolveTimezoneEmptyTZifIsIgnored(t *testing.T) {
	localtime, tz1, _ := utilCovSetTZPaths(t)
	t.Setenv("TZ", "")
	os.WriteFile(localtime, nil, 0o644) // zero bytes: the len(b) > 0 guard
	os.WriteFile(tz1, []byte("UTC"), 0o644)

	loc := resolveTimezone()
	if loc == nil {
		t.Fatal("resolveTimezone returned nil")
	}
	if off := utilCovOffsetOf(t, loc); off != 0 {
		t.Errorf("offset = %d, want 0 (UTC)", off)
	}
}

func TestUtilCovResolveTimezoneEnvTail(t *testing.T) {
	utilCovSetTZPaths(t) // nothing exists on disk
	t.Setenv("TZ", "CST-8")

	loc := resolveTimezone()
	if loc == nil {
		t.Fatal("resolveTimezone returned nil with $TZ set")
	}
	if off := utilCovOffsetOf(t, loc); off != 8*3600 {
		t.Errorf("offset = %d, want +8h from $TZ", off)
	}
}

func TestUtilCovResolveTimezoneNothingResolves(t *testing.T) {
	utilCovSetTZPaths(t)
	t.Setenv("TZ", "")
	if loc := resolveTimezone(); loc != nil {
		t.Errorf("resolveTimezone = %v, want nil so the clock draws --:--", loc)
	}
}

func TestUtilCovTimezoneKeeperStep(t *testing.T) {
	saved := displayLoc.Load()
	t.Cleanup(func() { displayLoc.Store(saved) })

	_, tz1, _ := utilCovSetTZPaths(t)
	t.Setenv("TZ", "")

	// Unresolvable: fast retry, nothing published.
	displayLoc.Store(nil)
	if d := utilCovTimezoneKeeperStep(); d != 2*time.Second {
		t.Errorf("unresolved step = %v, want 2s retry", d)
	}
	if displayLoc.Load() != nil {
		t.Error("unresolved step published a timezone")
	}

	// Resolvable: publish and settle to the 30s cadence.
	os.WriteFile(tz1, []byte("CST-8"), 0o644)
	if d := utilCovTimezoneKeeperStep(); d != 30*time.Second {
		t.Errorf("resolved step = %v, want 30s cadence", d)
	}
	loc := displayLoc.Load()
	if loc == nil {
		t.Fatal("resolved step did not publish the timezone")
	}
	if off := utilCovOffsetOf(t, loc); off != 8*3600 {
		t.Errorf("published offset = %d, want +8h", off)
	}
}
