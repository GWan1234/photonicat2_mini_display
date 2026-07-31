package main

import (
	"testing"
	"time"
)

// Each ping row collects on its own goroutine, so the guarantee tested here is
// per-row: a probe that hangs must not hold up that row past its deadline, and
// must not leave a queue of probes behind it.
func TestPingRowHangingProbeHitsDeadline(t *testing.T) {
	release := make(chan struct{})
	calls := make(chan struct{}, 8)

	origProbe, origDeadline := pingProbe, pingProbeDeadline
	defer func() { pingProbe, pingProbeDeadline = origProbe, origDeadline }()
	pingProbe = func(host string) (int64, error) {
		calls <- struct{}{}
		<-release
		return 42, nil
	}
	pingProbeDeadline = 50 * time.Millisecond

	row := &pingRow{valueKey: "TestPingValue", rateKey: "TestPingRate", lastSuccess: -1}

	start := time.Now()
	row.collect("stuck.example")
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("collect() waited %v on a hung probe, want ~%v", elapsed, pingProbeDeadline)
	}
	if v, _ := globalData.Load("TestPingValue"); v != int64(-2) {
		t.Errorf("value after deadline = %v, want -2 (timeout marker)", v)
	}
	if len(calls) != 1 {
		t.Fatalf("probe started %d times, want 1", len(calls))
	}

	// The first probe is still stuck: the next tick must not stack another one
	// behind it, and must still return promptly.
	start = time.Now()
	row.collect("stuck.example")
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("second collect() took %v, want an immediate skip", elapsed)
	}
	if len(calls) != 1 {
		t.Errorf("probe started %d times while one was in flight, want 1", len(calls))
	}

	// Once the stuck probe finishes, the row resumes probing.
	close(release)
	deadline := time.Now().Add(time.Second)
	for len(calls) < 2 && time.Now().Before(deadline) {
		row.collect("stuck.example")
		time.Sleep(5 * time.Millisecond)
	}
	if len(calls) < 2 {
		t.Error("row never probed again after the stuck probe returned")
	}
}

func TestPingRowPublish(t *testing.T) {
	row := &pingRow{valueKey: "TestPublishValue", rateKey: "TestPublishRate", lastSuccess: -1}

	// No successful ping yet: a soft failure has nothing to fall back to.
	row.publish(-1)
	if v, _ := globalData.Load("TestPublishValue"); v != int64(-1) {
		t.Errorf("value = %v, want -1 before any success", v)
	}
	if v, _ := globalData.Load("TestPublishRate"); v != "0" {
		t.Errorf("rate = %v, want 0", v)
	}

	row.publish(20)
	if v, _ := globalData.Load("TestPublishValue"); v != int64(20) {
		t.Errorf("value = %v, want 20", v)
	}
	if v, _ := globalData.Load("TestPublishRate"); v != "50" {
		t.Errorf("rate = %v, want 50 (1 of 2)", v)
	}

	// A soft failure keeps the last good value on screen.
	row.publish(-1)
	if v, _ := globalData.Load("TestPublishValue"); v != int64(20) {
		t.Errorf("value = %v, want the last successful 20", v)
	}

	// A timeout shows the red-X marker instead.
	row.publish(-2)
	if v, _ := globalData.Load("TestPublishValue"); v != int64(-2) {
		t.Errorf("value = %v, want -2", v)
	}
	if v, _ := globalData.Load("TestPublishRate"); v != "25" {
		t.Errorf("rate = %v, want 25 (1 of 4)", v)
	}
}

// Both rows are signalled together; each needs its own channel or one row's
// wake is swallowed by the other.
func TestSignalPingRescheduleWakesEveryRow(t *testing.T) {
	for _, ch := range pingReschedule {
		select {
		case <-ch:
		default:
		}
	}

	signalPingReschedule()
	for i, ch := range pingReschedule {
		select {
		case <-ch:
		default:
			t.Errorf("ping row %d was not signalled", i)
		}
	}
}
