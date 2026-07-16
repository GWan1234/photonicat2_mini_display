package main

import (
	"math"
	"os"
	"testing"
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
