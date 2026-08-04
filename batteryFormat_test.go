package main

// Formatting for the battery runtime estimate and the WAN speed readout. Both
// feed fixed-width slots on a 172px panel, so an unexpected extra digit is a
// layout bug, and a stray "0:00" is a lie about the battery.

import (
	"strings"
	"sync"
	"testing"
)

// weAreRunning is the shutdown flag every background collector polls while the
// SIGTERM handler and the /makeItRun endpoint write it. It was a plain bool,
// which `go test -race` flagged; this pins the concurrent-access contract so
// it does not regress to a plain global.
func TestWeAreRunningIsSafeUnderConcurrency(t *testing.T) {
	saved := weAreRunning()
	t.Cleanup(func() { setWeAreRunning(saved) })

	setWeAreRunning(true)
	if !weAreRunning() {
		t.Fatal("setWeAreRunning(true) did not take effect")
	}
	setWeAreRunning(false)
	if weAreRunning() {
		t.Fatal("setWeAreRunning(false) did not take effect")
	}

	// Readers and writers in parallel; under -race this is the actual assertion.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				if i%2 == 0 {
					setWeAreRunning(n%2 == 0)
				} else {
					_ = weAreRunning()
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestFormatRemainingTime(t *testing.T) {
	tests := []struct {
		name  string
		hours float64
		want  string
	}{
		{"two_hours_forty", 2.0 + 40.0/60.0, "2:40"},
		{"exactly_one_hour", 1.0, "1:00"},
		{"under_an_hour", 0.5, "0:30"},
		{"single_minute", 1.0 / 60.0, "0:01"},
		{"rounds_to_nearest_minute", 1.0 + 29.6/60.0, "1:30"},
		{"long_runtime", 13.75, "13:45"},
		{"very_long_runtime", 100.25, "100:15"},
		// A value that rounds to zero minutes yields "" so the slot draws
		// nothing rather than a meaningless "0:00".
		{"zero", 0, ""},
		{"rounds_to_zero", 0.004, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatRemainingTime(tt.hours); got != tt.want {
				t.Errorf("formatRemainingTime(%g) = %q, want %q", tt.hours, got, tt.want)
			}
		})
	}
}

// Minutes must always be zero-padded to two digits — "2:5" would misread as
// two hours five, and would also be the wrong width for the slot.
func TestFormatRemainingTimeAlwaysPadsMinutes(t *testing.T) {
	for m := 1; m < 60; m++ {
		got := formatRemainingTime(1.0 + float64(m)/60.0)
		parts := strings.SplitN(got, ":", 2)
		if len(parts) != 2 {
			t.Fatalf("formatRemainingTime produced %q, want H:MM", got)
		}
		if len(parts[1]) != 2 {
			t.Errorf("minutes not zero-padded in %q (minute %d)", got, m)
		}
	}
}

func TestNormalizeRemainingTime(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "2:40", "2:40"},
		{"strips_less_than", "< 0:10", "0:10"},
		{"strips_less_than_no_space", "<0:10", "0:10"},
		{"trims_whitespace", "  3:15  ", "3:15"},
		{"trims_and_strips", "  < 1:05 ", "1:05"},
		// Zero and placeholder values blank the slot entirely.
		{"empty", "", ""},
		{"whitespace_only", "   ", ""},
		{"dash_placeholder", "-", ""},
		{"dash_with_space", "  -  ", ""},
		{"zero", "0:00", ""},
		{"zero_padded", "00:00", ""},
		{"zero_short", "0:0", ""},
		{"less_than_zero", "< 0:00", ""},
		// Non-zero values that merely start with 0 must survive.
		{"zero_hours_nonzero_minutes", "0:05", "0:05"},
		{"zero_hours_ten_minutes", "0:10", "0:10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeRemainingTime(tt.in); got != tt.want {
				t.Errorf("normalizeRemainingTime(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// The remaining-time unit slot is deliberately kept blank: the ">90%" suffix
// made the row overflow 172px. Pin it so it does not creep back.
func TestApplyRemainingTimeUnitClearsTheSlot(t *testing.T) {
	globalData.Store("RemainingTime_Unit", ">90%")

	applyRemainingTimeUnit()

	if got, _ := globalData.Load("RemainingTime_Unit"); got != "" {
		t.Errorf("RemainingTime_Unit = %v, want \"\" — the suffix overflows the row", got)
	}
}

// The clamp boundary: 100000 is in range, anything above is not.
// (The main formatSpeed table lives in processData_test.go.)
func TestFormatSpeedClampBoundary(t *testing.T) {
	if got, _ := formatSpeed(100000); got == "0.00" {
		t.Error("formatSpeed(100000) was clamped; the limit is exclusive above 100000")
	}
	if got, _ := formatSpeed(100000.1); got != "0.00" {
		t.Errorf("formatSpeed(100000.1) = %q, want clamped \"0.00\"", got)
	}
}

// %.3g switches to scientific notation above 999, so a gigabit-class link
// renders as "1.23e+03" in a slot sized for a short number. Pinned as known
// behaviour: if the formatter is ever reworked for wide links, this is the
// case to revisit.
func TestFormatSpeedLargeValuesUseScientificNotation(t *testing.T) {
	for _, tt := range []struct {
		mbps float64
		want string
	}{
		{999, "999"},
		{1000, "1e+03"},
		{1234, "1.23e+03"},
		{99999, "1e+05"},
	} {
		if got, _ := formatSpeed(tt.mbps); got != tt.want {
			t.Errorf("formatSpeed(%g) = %q, want %q", tt.mbps, got, tt.want)
		}
	}
}

// storeWANSpeed must publish all four keys the top bar reads; a missing unit
// key leaves a stale unit next to a fresh number.
func TestStoreWANSpeedPublishesAllKeys(t *testing.T) {
	storeWANSpeed(12.5, 0.25)

	want := map[string]string{
		"WanUP":        "12.5",
		"WanUP_Unit":   "Mbps",
		"WanDOWN":      "0.25",
		"WanDOWN_Unit": "Mbps",
	}
	for key, exp := range want {
		got, ok := globalData.Load(key)
		if !ok {
			t.Errorf("%s was not stored", key)
			continue
		}
		if got != exp {
			t.Errorf("%s = %v, want %v", key, got, exp)
		}
	}
}
