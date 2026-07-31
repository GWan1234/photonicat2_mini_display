package main

import (
	"math"
	"testing"
	"time"
)

func mbps(bytes float64, over time.Duration) float64 {
	return bytes * 8 / 1e6 / over.Seconds()
}

func TestNetSpeedSamplerWindowing(t *testing.T) {
	var s netSpeedSampler
	t0 := time.Unix(1700000000, 0)

	if _, ok := s.record("eth0", 1000, 500, t0); ok {
		t.Error("first sample reported a speed; it has no window to divide by")
	}

	// 1s later: measured against the t0 sample.
	speed, ok := s.record("eth0", 1000+1_000_000, 500+200_000, t0.Add(time.Second))
	if !ok {
		t.Fatal("no speed after a full window")
	}
	if want := mbps(1_000_000, time.Second); math.Abs(speed.DownloadMbps-want) > 1e-9 {
		t.Errorf("download = %v, want %v", speed.DownloadMbps, want)
	}
	if want := mbps(200_000, time.Second); math.Abs(speed.UploadMbps-want) > 1e-9 {
		t.Errorf("upload = %v, want %v", speed.UploadMbps, want)
	}

	// At the 2 Hz page-0 cadence every tick still publishes, measured over the
	// last ~1s rather than a jumpy half second.
	speed, ok = s.record("eth0", 1000+1_500_000, 500+300_000, t0.Add(1500*time.Millisecond))
	if !ok {
		t.Fatal("half-second tick published nothing")
	}
	if want := mbps(1_500_000, 1500*time.Millisecond); math.Abs(speed.DownloadMbps-want) > 1e-9 {
		t.Errorf("download = %v, want %v (window back to t0)", speed.DownloadMbps, want)
	}
	// Samples older than the window are dropped, so history stays tiny.
	if len(s.history) > 4 {
		t.Errorf("history grew to %d samples", len(s.history))
	}
}

// A tick that lands right on top of the previous one has no usable window; the
// caller keeps the value already on screen instead of dividing by ~nothing.
func TestNetSpeedSamplerIgnoresTinyWindow(t *testing.T) {
	var s netSpeedSampler
	t0 := time.Unix(1700000000, 0)

	s.record("eth0", 1000, 500, t0)
	if _, ok := s.record("eth0", 1100, 550, t0.Add(50*time.Millisecond)); ok {
		t.Error("sample inside netSpeedMinWindow reported a speed")
	}
}

func TestNetSpeedSamplerResetsOnCounterWrapAndFailover(t *testing.T) {
	var s netSpeedSampler
	t0 := time.Unix(1700000000, 0)

	s.record("eth0", 5_000_000, 5_000_000, t0)
	if _, ok := s.record("eth0", 1_000, 1_000, t0.Add(time.Second)); ok {
		t.Error("counters going backwards produced a speed instead of restarting")
	}
	if _, ok := s.record("eth0", 1_000+128*1024, 1_000, t0.Add(2*time.Second)); !ok {
		t.Error("sampler did not recover after the counter reset")
	}

	// A failover to another netdev must not diff against the old one's counters.
	if _, ok := s.record("wwan0", 900_000_000, 900_000_000, t0.Add(3*time.Second)); ok {
		t.Error("interface change produced a speed from unrelated counters")
	}
}

func TestNetSpeedSamplerDoesNotBlock(t *testing.T) {
	var s netSpeedSampler
	start := time.Now()
	// "lo" exists on the target; on a host without it the read error path is
	// just as immediate, which is the property under test.
	s.sample("lo")
	s.sample("lo")
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("two samples took %v; sampling must not sleep through a window", elapsed)
	}
}
