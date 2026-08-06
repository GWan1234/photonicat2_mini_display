package main

// recordPowerSample used to call savePowerData() while holding powerData.mu
// for writing. savePowerData takes the same mutex for reading, and Go's
// RWMutex is not reentrant, so every 10th sample self-deadlocked and left the
// mutex held forever.
//
// The visible symptom was the display freezing: middlePageFingerprint reads
// powerData for any "graph" element, so once the GPS page (whose compass is a
// graph) or the power page was on screen, the render loop blocked on RLock and
// stopped repainting — the panel hung and button presses did nothing.
//
// These tests run the save path with a watchdog so a regression fails the
// suite instead of hanging it.

import (
	"os"

	"testing"
	"time"
)

// withTempPowerFile points POWER_DATA_FILE's directory at t.TempDir so the
// tests never clobber a real cache, and restores the sample set afterwards.
func withPowerSamples(t *testing.T, n int) {
	t.Helper()

	powerData.mu.Lock()
	oldSamples := powerData.Samples
	oldFrame := powerData.TimeFrameMins
	samples := make([]PowerSample, n)
	for i := range samples {
		samples[i] = PowerSample{Timestamp: time.Now(), Wattage: float64(i)}
	}
	powerData.Samples = samples
	powerData.mu.Unlock()

	t.Cleanup(func() {
		powerData.mu.Lock()
		powerData.Samples = oldSamples
		powerData.TimeFrameMins = oldFrame
		powerData.mu.Unlock()
	})
}

// runWithWatchdog fails (rather than hangs) if fn does not finish in time.
func runWithWatchdog(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s deadlocked (held powerData.mu across a re-lock)", what)
	}
}

// The exact regression: a sample count that is a multiple of 10 triggers the
// periodic save from inside the write-locked section.
func TestRecordPowerSampleDoesNotDeadlockOnSave(t *testing.T) {
	// 9 existing samples: the sample this call appends makes 10, hitting the
	// %10 == 0 save branch.
	withPowerSamples(t, 9)
	globalData.Store("BatteryWattage", "5.5")

	runWithWatchdog(t, "recordPowerSample", recordPowerSample)

	powerData.mu.Lock()
	got := len(powerData.Samples)
	powerData.mu.Unlock()
	if got != 10 {
		t.Errorf("sample count = %d, want 10", got)
	}
}

// After the save path runs, the mutex must be free — a leaked lock is what
// froze the render loop, and it outlives the call that caused it.
func TestPowerMutexIsFreeAfterSave(t *testing.T) {
	withPowerSamples(t, 19)
	globalData.Store("BatteryWattage", "6.25")

	runWithWatchdog(t, "recordPowerSample", recordPowerSample)

	// middlePageFingerprint's graph branch is the reader that hung; model it.
	runWithWatchdog(t, "reader after save", func() {
		powerData.mu.RLock()
		_ = len(powerData.Samples)
		powerData.mu.RUnlock()
	})

	// A writer must be able to take it too.
	runWithWatchdog(t, "writer after save", func() {
		powerData.mu.Lock()
		powerData.mu.Unlock()
	})
}

// savePowerData is the public entry point and takes the lock itself, so it
// must be called WITHOUT holding it. Pin that it completes on its own.
func TestSavePowerDataTakesItsOwnLock(t *testing.T) {
	withPowerSamples(t, 5)
	runWithWatchdog(t, "savePowerData", savePowerData)
}

// Many samples in a row cross several save boundaries; none may wedge.
func TestRecordPowerSampleRepeatedCrossesSaveBoundaries(t *testing.T) {
	withPowerSamples(t, 0)
	globalData.Store("BatteryWattage", "4.0")

	runWithWatchdog(t, "25 samples", func() {
		for i := 0; i < 25; i++ {
			recordPowerSample()
		}
	})
}

// The snapshot writer must round-trip through loadPowerData's format, so a
// saved file is still readable — the fix changed how the payload is built.
func TestSavePowerDataSnapshotWritesLoadableFile(t *testing.T) {
	// POWER_DATA_FILE is a const, so this exercises the real path. Preserve
	// and restore whatever was there so a dev box keeps its cache.
	path := POWER_DATA_FILE
	if prev, err := os.ReadFile(path); err == nil {
		t.Cleanup(func() { os.WriteFile(path, prev, 0644) })
	} else {
		t.Cleanup(func() { os.Remove(path) })
	}

	samples := []PowerSample{
		{Timestamp: time.Now(), Wattage: 1.5},
		{Timestamp: time.Now(), Wattage: 2.5},
	}
	savePowerDataSnapshot(samples, 30)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("saved power data file is empty")
	}

	// loadPowerData decodes into powerData; check it accepts what we wrote.
	withPowerSamples(t, 0)
	runWithWatchdog(t, "loadPowerData", loadPowerData)

	powerData.mu.RLock()
	got := len(powerData.Samples)
	frame := powerData.TimeFrameMins
	powerData.mu.RUnlock()

	if got != len(samples) {
		t.Errorf("loaded %d samples, want %d", got, len(samples))
	}
	if frame != 30 {
		t.Errorf("loaded time frame = %d, want 30", frame)
	}
}
