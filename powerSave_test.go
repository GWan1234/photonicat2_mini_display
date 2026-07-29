package main

import (
	"math"
	"os"
	"testing"
	"time"
)

// Two back-to-back calls give the delta sampler a near-zero measurement
// window; values must stay real numbers in [0,100], never NaN.
func TestGetCpuUsagesZeroWindow(t *testing.T) {
	if _, err := os.Stat("/proc/stat"); err != nil {
		t.Skip("no /proc/stat on this platform")
	}
	if _, err := getCpuUsages(); err != nil {
		t.Fatalf("first call: %v", err)
	}
	usages, err := getCpuUsages()
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	for i, u := range usages {
		if math.IsNaN(u) || u < 0 || u > 100 {
			t.Errorf("cpu%d usage = %v, want a real number in [0,100]", i, u)
		}
	}
}

// displayAsleep must be false whenever the app is not settled in STATE_IDLE,
// regardless of the backlight value (e.g. during boot before the first
// setBacklight call, when lastLogical is still the zero value).
func TestDisplayAsleepRequiresIdleState(t *testing.T) {
	origState := idleState
	origLogical := lastLogical
	defer func() {
		idleState = origState
		lastLogical = origLogical
	}()

	lastLogical = 0
	for _, s := range []int{STATE_ACTIVE, STATE_FADE_IN, STATE_FADE_OUT} {
		idleState = s
		if displayAsleep() {
			t.Errorf("displayAsleep() = true in state %s, want false", stateName(s))
		}
	}

	idleState = STATE_IDLE
	if !displayAsleep() {
		t.Error("displayAsleep() = false in STATE_IDLE with backlight 0, want true")
	}
	lastLogical = 30 // visibly dim (screen_min_brightness > 0): not asleep
	if displayAsleep() {
		t.Error("displayAsleep() = true with backlight 30, want false")
	}
}

// Ping cadence is 1s only while awake on a page that shows Ping0/Ping1;
// every other case (idle, non-ping page, SMS) stays on the 1-minute interval.
func TestCurrentPingInterval(t *testing.T) {
	origState := idleState
	origPage := currPageIdx
	origCfg := cfg
	origNum := cfgNumPages
	defer func() {
		idleState = origState
		currPageIdx = origPage
		cfg = origCfg
		cfgNumPages = origNum
	}()

	cfg.DisplayTemplate.Elements = map[string][]DisplayElement{
		"page0": {{Type: "text", DataKey: "WanUP"}},
		"page1": {{Type: "text", DataKey: "Ping0"}, {Type: "text", DataKey: "Ping1"}},
	}
	cfgNumPages = 2

	idleState = STATE_ACTIVE
	currPageIdx = 1
	if got := currentPingInterval(); got != INTERVAL_PING_ACTIVE {
		t.Errorf("awake on ping page: got %v, want %v", got, INTERVAL_PING_ACTIVE)
	}

	currPageIdx = 0
	if got := currentPingInterval(); got != INTERVAL_PING_IDLE {
		t.Errorf("awake on non-ping page: got %v, want %v", got, INTERVAL_PING_IDLE)
	}

	currPageIdx = 1
	idleState = STATE_IDLE
	if got := currentPingInterval(); got != INTERVAL_PING_IDLE {
		t.Errorf("idle on ping page: got %v, want %v", got, INTERVAL_PING_IDLE)
	}

	if !pageShowsPing(1) {
		t.Error("pageShowsPing(1) = false, want true")
	}
	if pageShowsPing(0) {
		t.Error("pageShowsPing(0) = true, want false")
	}
	if pageShowsPing(99) {
		t.Error("pageShowsPing(99) = true, want false for out-of-range")
	}

	// Sanity: the active interval really is 1s (1 Hz) and idle is 1m.
	if INTERVAL_PING_ACTIVE != 1*time.Second {
		t.Errorf("INTERVAL_PING_ACTIVE = %v, want 1s", INTERVAL_PING_ACTIVE)
	}
	if INTERVAL_PING_IDLE != time.Minute {
		t.Errorf("INTERVAL_PING_IDLE = %v, want 1m", INTERVAL_PING_IDLE)
	}
}

// Linux/CPU cadence is 500ms (2 Hz) only while awake on a page with cpu_bars;
// otherwise 1m.
func TestCurrentLinuxInterval(t *testing.T) {
	origState := idleState
	origPage := currPageIdx
	origCfg := cfg
	origNum := cfgNumPages
	defer func() {
		idleState = origState
		currPageIdx = origPage
		cfg = origCfg
		cfgNumPages = origNum
	}()

	cfg.DisplayTemplate.Elements = map[string][]DisplayElement{
		"page0": {{Type: "text", DataKey: "WanUP"}},
		"page2": {{Type: "cpu_bars", DataKey: "CpuUsages"}, {Type: "hbar", DataKey: "MemUsagePercent"}},
	}
	cfgNumPages = 3

	idleState = STATE_ACTIVE
	currPageIdx = 2
	if got := currentLinuxInterval(); got != INTERVAL_LINUX_ACTIVE {
		t.Errorf("awake on cpu page: got %v, want %v", got, INTERVAL_LINUX_ACTIVE)
	}
	if !pageShowsCpuBars(2) {
		t.Error("pageShowsCpuBars(2) = false, want true")
	}

	currPageIdx = 0
	if got := currentLinuxInterval(); got != INTERVAL_LINUX_IDLE {
		t.Errorf("awake on non-cpu page: got %v, want %v", got, INTERVAL_LINUX_IDLE)
	}
	if pageShowsCpuBars(0) {
		t.Error("pageShowsCpuBars(0) = true, want false")
	}

	currPageIdx = 2
	idleState = STATE_IDLE
	if got := currentLinuxInterval(); got != INTERVAL_LINUX_IDLE {
		t.Errorf("idle on cpu page: got %v, want %v", got, INTERVAL_LINUX_IDLE)
	}

	if INTERVAL_LINUX_ACTIVE != 500*time.Millisecond {
		t.Errorf("INTERVAL_LINUX_ACTIVE = %v, want 500ms", INTERVAL_LINUX_ACTIVE)
	}
}
