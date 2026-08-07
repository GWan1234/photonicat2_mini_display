package main

// Coverage tests for processData.go. Every top-level identifier here is
// prefixed pdCov/PDCov (tests: TestPDCov*) so concurrent test-writing agents
// merging into this package cannot collide.
//
// The production seams (pdCov* path vars and pdCovExecOutput in
// processData.go) default to the historical literals; tests point them at
// t.TempDir() fixtures / canned exec output and restore them via t.Cleanup.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- helpers -------------------------------------------------------------

// pdCovSetStr swaps a string seam for the duration of the test.
func pdCovSetStr(t *testing.T, target *string, val string) {
	t.Helper()
	saved := *target
	*target = val
	t.Cleanup(func() { *target = saved })
}

// pdCovStubExec swaps the exec seam for the duration of the test.
func pdCovStubExec(t *testing.T, fn func(name string, args ...string) ([]byte, error)) {
	t.Helper()
	saved := pdCovExecOutput
	pdCovExecOutput = fn
	t.Cleanup(func() { pdCovExecOutput = saved })
}

// pdCovExecCanned installs an exec stub that answers from a canned map keyed
// by "name|arg1 arg2 ..."; any command not in the map fails. When calls is
// non-nil each key is appended to it.
func pdCovExecCanned(t *testing.T, canned map[string]string, calls *[]string) {
	t.Helper()
	pdCovStubExec(t, func(name string, args ...string) ([]byte, error) {
		key := name + "|" + strings.Join(args, " ")
		if calls != nil {
			*calls = append(*calls, key)
		}
		out, ok := canned[key]
		if !ok {
			return nil, fmt.Errorf("pdCov canned exec: no entry for %q", key)
		}
		return []byte(out), nil
	})
}

// pdCovWriteFile writes content to dir/name (creating parents) and returns
// the full path.
func pdCovWriteFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// pdCovBatteryFixture points pdCovBatteryDir at a temp dir populated with the
// given sysfs node files and returns the dir.
func pdCovBatteryFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		pdCovWriteFile(t, dir, name, content)
	}
	pdCovSetStr(t, &pdCovBatteryDir, dir)
	return dir
}

// pdCovSavePublicIPState snapshots and restores the public-IP cache globals.
func pdCovSavePublicIPState(t *testing.T) {
	t.Helper()
	publicIPMu.Lock()
	lf, wb, eg := publicIPLastFetch, publicIPWanBasis, publicIPEgress
	fe, ho := publicIPFetching, publicIPHadOnline
	publicIPMu.Unlock()
	t.Cleanup(func() {
		publicIPMu.Lock()
		publicIPLastFetch, publicIPWanBasis, publicIPEgress = lf, wb, eg
		publicIPFetching, publicIPHadOnline = fe, ho
		publicIPMu.Unlock()
	})
}

// pdCovSaveWebState snapshots and restores the pcat-manager-web up/probed
// flags mutated by getInfoFromPcatWeb.
func pdCovSaveWebState(t *testing.T) {
	t.Helper()
	up, probed, known := pcatWebUp.Load(), pcatWebProbed.Load(), pcatWebStateKnown
	t.Cleanup(func() {
		pcatWebUp.Store(up)
		pcatWebProbed.Store(probed)
		pcatWebStateKnown = known
	})
}

// pdCovSaveBatteryGlobals snapshots and restores the mutable globals touched
// by collectBatteryData.
func pdCovSaveBatteryGlobals(t *testing.T) {
	t.Helper()
	soc, chg, last := battSOC, battChargingStatus, lastChargingStatus
	it, is := idleTimeout, idleState
	dc, batt := cfg.ScreenDimmerTimeOnDCSeconds, cfg.ScreenDimmerTimeOnBatterySeconds
	lastActivityMu.Lock()
	la := lastActivity
	lastActivityMu.Unlock()
	t.Cleanup(func() {
		battSOC, battChargingStatus, lastChargingStatus = soc, chg, last
		idleTimeout, idleState = it, is
		cfg.ScreenDimmerTimeOnDCSeconds, cfg.ScreenDimmerTimeOnBatterySeconds = dc, batt
		lastActivityMu.Lock()
		lastActivity = la
		lastActivityMu.Unlock()
	})
}

// pdCovSaveCPUPrev snapshots and restores the /proc/stat delta baseline.
func pdCovSaveCPUPrev(t *testing.T) {
	t.Helper()
	prevCPUStatsMu.Lock()
	saved := prevCPUStats
	prevCPUStatsMu.Unlock()
	t.Cleanup(func() {
		prevCPUStatsMu.Lock()
		prevCPUStats = saved
		prevCPUStatsMu.Unlock()
	})
}

// pdCovSetCPUPrev sets the /proc/stat baseline directly (nil = force priming).
func pdCovSetCPUPrev(stats []CPUStats) {
	prevCPUStatsMu.Lock()
	prevCPUStats = stats
	prevCPUStatsMu.Unlock()
}

// pdCovSaveWanInterface snapshots and restores the wanInterface global.
func pdCovSaveWanInterface(t *testing.T) {
	t.Helper()
	saved := wanInterface
	t.Cleanup(func() { wanInterface = saved })
}

// pdCovLoad returns globalData[key] (nil when absent).
func pdCovLoad(key string) interface{} {
	v, _ := globalData.Load(key)
	return v
}

// pdCovMissingPath returns a path that does not exist.
func pdCovMissingPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "does-not-exist")
}

// ---- battery sysfs readers -----------------------------------------------

func TestPDCovGetBatterySoc(t *testing.T) {
	pdCovBatteryFixture(t, map[string]string{"capacity": " 85\n"})
	if soc, err := getBatterySoc(); err != nil || soc != 85 {
		t.Errorf("getBatterySoc() = %d, %v; want 85, nil", soc, err)
	}

	pdCovBatteryFixture(t, map[string]string{"capacity": "not-a-number"})
	if soc, err := getBatterySoc(); err == nil || soc != -1 {
		t.Errorf("garbage capacity: got %d, %v; want -1, error", soc, err)
	}

	pdCovSetStr(t, &pdCovBatteryDir, pdCovMissingPath(t))
	if soc, err := getBatterySoc(); err == nil || soc != -1 {
		t.Errorf("missing capacity: got %d, %v; want -1, error", soc, err)
	}
}

func TestPDCovGetBatteryCharging(t *testing.T) {
	cases := []struct {
		content string
		want    bool
	}{
		{"Charging\n", true},
		{"Full\n", true},
		{"Discharging\n", false},
		{"Not charging\n", false},
	}
	for _, tc := range cases {
		pdCovBatteryFixture(t, map[string]string{"status": tc.content})
		got, err := getBatteryCharging()
		if err != nil || got != tc.want {
			t.Errorf("status %q: got %v, %v; want %v, nil", tc.content, got, err, tc.want)
		}
	}

	pdCovSetStr(t, &pdCovBatteryDir, pdCovMissingPath(t))
	if _, err := getBatteryCharging(); err == nil {
		t.Error("missing status file: want error")
	}
}

func TestPDCovGetBatteryVoltageAndCurrent(t *testing.T) {
	pdCovBatteryFixture(t, map[string]string{
		"voltage_now": "8400000\n",
		"current_now": "-1500000\n",
	})
	if v, err := getBatteryVoltageUV(); err != nil || v != 8400000 {
		t.Errorf("voltage = %v, %v; want 8400000, nil", v, err)
	}
	if c, err := getBatteryCurrentUA(); err != nil || c != -1500000 {
		t.Errorf("current = %v, %v; want -1500000, nil", c, err)
	}

	pdCovBatteryFixture(t, map[string]string{"voltage_now": "x", "current_now": "y"})
	if _, err := getBatteryVoltageUV(); err == nil {
		t.Error("garbage voltage: want error")
	}
	if _, err := getBatteryCurrentUA(); err == nil {
		t.Error("garbage current: want error")
	}

	pdCovSetStr(t, &pdCovBatteryDir, pdCovMissingPath(t))
	if _, err := getBatteryVoltageUV(); err == nil {
		t.Error("missing voltage: want error")
	}
	if _, err := getBatteryCurrentUA(); err == nil {
		t.Error("missing current: want error")
	}
}

func TestPDCovReadBatterySysfsFloat(t *testing.T) {
	pdCovBatteryFixture(t, map[string]string{"energy_full": " 36000000 \n"})
	if v, err := readBatterySysfsFloat("energy_full"); err != nil || v != 36000000 {
		t.Errorf("readBatterySysfsFloat = %v, %v; want 36000000, nil", v, err)
	}
	if _, err := readBatterySysfsFloat("missing_node"); err == nil {
		t.Error("missing node: want error")
	}
}

func TestPDCovBatteryEnergyState(t *testing.T) {
	// 1) energy_now/energy_full/power_now.
	pdCovBatteryFixture(t, map[string]string{
		"energy_now": "18000000", "energy_full": "36000000", "power_now": "6000000",
	})
	level, full, rate, ok := batteryEnergyState()
	if !ok || level != 18000000 || full != 36000000 || rate != 6000000 {
		t.Errorf("energy pair: got %v %v %v %v", level, full, rate, ok)
	}

	// 2) charge_now/charge_full/current_now.
	pdCovBatteryFixture(t, map[string]string{
		"charge_now": "2000000", "charge_full": "4000000", "current_now": "500000",
	})
	level, full, rate, ok = batteryEnergyState()
	if !ok || level != 2000000 || full != 4000000 || rate != 500000 {
		t.Errorf("charge pair: got %v %v %v %v", level, full, rate, ok)
	}

	// 3) capacity x energy_full (photonicat2 layout).
	pdCovBatteryFixture(t, map[string]string{
		"capacity": "50", "energy_full": "36000000", "power_now": "9000000",
	})
	level, full, rate, ok = batteryEnergyState()
	if !ok || level != 18000000 || full != 36000000 || rate != 9000000 {
		t.Errorf("capacity x energy_full: got %v %v %v %v", level, full, rate, ok)
	}

	// 4) capacity x charge_full.
	pdCovBatteryFixture(t, map[string]string{
		"capacity": "25", "charge_full": "4000000", "current_now": "1000000",
	})
	level, full, rate, ok = batteryEnergyState()
	if !ok || level != 1000000 || full != 4000000 || rate != 1000000 {
		t.Errorf("capacity x charge_full: got %v %v %v %v", level, full, rate, ok)
	}

	// No usable pair.
	pdCovBatteryFixture(t, map[string]string{"capacity": "25"})
	if _, _, _, ok := batteryEnergyState(); ok {
		t.Error("capacity alone must not produce a state")
	}
	pdCovSetStr(t, &pdCovBatteryDir, pdCovMissingPath(t))
	if _, _, _, ok := batteryEnergyState(); ok {
		t.Error("empty sysfs must not produce a state")
	}
}

func TestPDCovComputeRemainingTimeHours(t *testing.T) {
	// Discharging: 18 Wh left at 6 W -> 3h.
	pdCovBatteryFixture(t, map[string]string{
		"energy_now": "18000000", "energy_full": "36000000", "power_now": "6000000",
	})
	if h, ok := computeRemainingTimeHours(false); !ok || math.Abs(h-3.0) > 1e-9 {
		t.Errorf("discharge: got %v, %v; want 3h, true", h, ok)
	}

	// Charging to 90%: target 32.4 Wh, level 18 Wh, rate 6 W -> 2.4h.
	if h, ok := computeRemainingTimeHours(true); !ok || math.Abs(h-2.4) > 1e-9 {
		t.Errorf("charge: got %v, %v; want 2.4h, true", h, ok)
	}

	// Charging while already above target -> no estimate.
	pdCovBatteryFixture(t, map[string]string{
		"energy_now": "35000000", "energy_full": "36000000", "power_now": "6000000",
	})
	if _, ok := computeRemainingTimeHours(true); ok {
		t.Error("above target: want ok=false")
	}

	// Negative rate uses magnitude.
	pdCovBatteryFixture(t, map[string]string{
		"energy_now": "12000000", "energy_full": "36000000", "power_now": "-6000000",
	})
	if h, ok := computeRemainingTimeHours(false); !ok || math.Abs(h-2.0) > 1e-9 {
		t.Errorf("negative rate: got %v, %v; want 2h, true", h, ok)
	}

	// Effectively idle rate -> no estimate.
	pdCovBatteryFixture(t, map[string]string{
		"energy_now": "12000000", "energy_full": "36000000", "power_now": "0",
	})
	if _, ok := computeRemainingTimeHours(false); ok {
		t.Error("idle rate: want ok=false")
	}

	// Empty battery discharging -> nothing to estimate.
	pdCovBatteryFixture(t, map[string]string{
		"energy_now": "0", "energy_full": "36000000", "power_now": "6000000",
	})
	if _, ok := computeRemainingTimeHours(false); ok {
		t.Error("empty battery: want ok=false")
	}

	// No counters at all.
	pdCovSetStr(t, &pdCovBatteryDir, pdCovMissingPath(t))
	if _, ok := computeRemainingTimeHours(false); ok {
		t.Error("no counters: want ok=false")
	}
}

// ---- DC rail / thermal / mem / uptime ------------------------------------

func TestPDCovGetDCVoltageUV(t *testing.T) {
	dir := t.TempDir()
	p := pdCovWriteFile(t, dir, "voltage_now", "12100000\n")
	pdCovSetStr(t, &pdCovChargerVoltagePath, p)
	if v, err := getDCVoltageUV(); err != nil || v != 12100000 {
		t.Errorf("got %v, %v; want 12100000, nil", v, err)
	}

	// Below 1V is reported as 0 (rail off).
	pdCovSetStr(t, &pdCovChargerVoltagePath, pdCovWriteFile(t, dir, "low", "500000"))
	if v, err := getDCVoltageUV(); err != nil || v != 0 {
		t.Errorf("sub-1V: got %v, %v; want 0, nil", v, err)
	}

	pdCovSetStr(t, &pdCovChargerVoltagePath, pdCovWriteFile(t, dir, "bad", "zap"))
	if _, err := getDCVoltageUV(); err == nil {
		t.Error("garbage: want error")
	}

	pdCovSetStr(t, &pdCovChargerVoltagePath, pdCovMissingPath(t))
	if _, err := getDCVoltageUV(); err == nil {
		t.Error("missing: want error")
	}
}

func TestPDCovGetCpuTemp(t *testing.T) {
	dir := t.TempDir()
	pdCovSetStr(t, &pdCovThermalZonePath, pdCovWriteFile(t, dir, "temp", "55123\n"))
	if v, err := getCpuTemp(); err != nil || v != 55123 {
		t.Errorf("got %v, %v; want 55123, nil", v, err)
	}
	pdCovSetStr(t, &pdCovThermalZonePath, pdCovWriteFile(t, dir, "bad", "hot"))
	if _, err := getCpuTemp(); err == nil {
		t.Error("garbage: want error")
	}
	pdCovSetStr(t, &pdCovThermalZonePath, pdCovMissingPath(t))
	if _, err := getCpuTemp(); err == nil {
		t.Error("missing: want error")
	}
}

func TestPDCovGetMemUsedAndTotalGB(t *testing.T) {
	dir := t.TempDir()
	meminfo := "MemTotal:       4194304 kB\nMemFree:         102400 kB\nMemAvailable:   2097152 kB\n"
	pdCovSetStr(t, &pdCovProcMeminfoPath, pdCovWriteFile(t, dir, "meminfo", meminfo))
	used, total, err := getMemUsedAndTotalGB()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(total-4.0) > 1e-9 || math.Abs(used-2.0) > 1e-9 {
		t.Errorf("got used=%v total=%v; want 2, 4", used, total)
	}

	pdCovSetStr(t, &pdCovProcMeminfoPath, pdCovWriteFile(t, dir, "nototal", "MemFree: 1 kB\n"))
	if _, _, err := getMemUsedAndTotalGB(); err == nil {
		t.Error("missing MemTotal: want error")
	}

	pdCovSetStr(t, &pdCovProcMeminfoPath, pdCovWriteFile(t, dir, "badtotal", "MemTotal: abc kB\n"))
	if _, _, err := getMemUsedAndTotalGB(); err == nil {
		t.Error("garbage MemTotal: want error")
	}

	pdCovSetStr(t, &pdCovProcMeminfoPath,
		pdCovWriteFile(t, dir, "badavail", "MemTotal: 1024 kB\nMemAvailable: x kB\n"))
	if _, _, err := getMemUsedAndTotalGB(); err == nil {
		t.Error("garbage MemAvailable: want error")
	}

	pdCovSetStr(t, &pdCovProcMeminfoPath, pdCovMissingPath(t))
	if _, _, err := getMemUsedAndTotalGB(); err == nil {
		t.Error("missing file: want error")
	}
}

func TestPDCovGetUptimeSecondsAndFormat(t *testing.T) {
	dir := t.TempDir()
	set := func(content string) {
		pdCovSetStr(t, &pdCovProcUptimePath, pdCovWriteFile(t, dir,
			fmt.Sprintf("uptime%d", len(content)), content))
	}

	set("90061.27 180000.00\n")
	if s, err := getUptimeSeconds(); err != nil || math.Abs(s-90061.27) > 1e-6 {
		t.Errorf("seconds = %v, %v; want 90061.27", s, err)
	}
	if u, err := getUptime(); err != nil || u != "1d 1h 1m 1s" {
		t.Errorf("uptime = %q, %v; want \"1d 1h 1m 1s\"", u, err)
	}

	set("3600.0 1.0\n")
	if u, err := getUptime(); err != nil || u != "1h" {
		t.Errorf("exact hour = %q, %v; want \"1h\"", u, err)
	}

	set("0.4 1\n")
	if u, err := getUptime(); err != nil || u != "0s" {
		t.Errorf("sub-second = %q, %v; want \"0s\"", u, err)
	}

	set("nonsense here\n")
	if _, err := getUptimeSeconds(); err == nil {
		t.Error("garbage: want parse error")
	}

	set("\n")
	if _, err := getUptimeSeconds(); err == nil {
		t.Error("empty: want invalid-data error")
	}

	pdCovSetStr(t, &pdCovProcUptimePath, pdCovMissingPath(t))
	if _, err := getUptimeSeconds(); err == nil {
		t.Error("missing file: want error")
	}
	if _, err := getUptime(); err == nil {
		t.Error("getUptime with missing file: want error")
	}
}

// ---- CPU stats ------------------------------------------------------------

const pdCovStatV1 = `cpu  200 0 200 1600 10 5 5 0
cpu0 100 0 100 800 0 0 0 0 0
cpu1 100 0 100 800 10 5 5 0
cpu2 1 2
intr 12345
`

const pdCovStatV2 = `cpu  400 0 300 2400 10 5 5 0
cpu0 200 0 150 1200 0 0 0 0 0
cpu1 200 0 150 1200 10 5 5 0
intr 12345
`

func TestPDCovReadCPUStats(t *testing.T) {
	dir := t.TempDir()
	pdCovSetStr(t, &pdCovProcStatPath, pdCovWriteFile(t, dir, "stat", pdCovStatV1))
	stats, err := readCPUStats()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Aggregate "cpu " line and short "cpu2" line are skipped.
	if len(stats) != 2 {
		t.Fatalf("got %d per-core stats, want 2", len(stats))
	}
	if stats[0].User != 100 || stats[0].Idle != 800 || stats[0].Steal != 0 {
		t.Errorf("cpu0 parsed wrong: %+v", stats[0])
	}
	if stats[1].Iowait != 10 || stats[1].Irq != 5 || stats[1].Softirq != 5 {
		t.Errorf("cpu1 parsed wrong: %+v", stats[1])
	}

	pdCovSetStr(t, &pdCovProcStatPath, pdCovMissingPath(t))
	if _, err := readCPUStats(); err == nil {
		t.Error("missing file: want error")
	}
}

func TestPDCovGetCpuUsages(t *testing.T) {
	pdCovSaveCPUPrev(t)
	dir := t.TempDir()
	statPath := pdCovWriteFile(t, dir, "stat", pdCovStatV1)
	pdCovSetStr(t, &pdCovProcStatPath, statPath)

	// Priming call: baseline nil -> snapshot + short window over identical
	// content -> zero deltas reported as 0 (not NaN).
	pdCovSetCPUPrev(nil)
	usages, err := getCpuUsages()
	if err != nil {
		t.Fatalf("priming call: %v", err)
	}
	for i, u := range usages {
		if u != 0 {
			t.Errorf("priming cpu%d = %v, want 0 (zero window)", i, u)
		}
	}

	// Real delta: cpu0 busy 150 idle 400 -> 27.27%; cpu1 likewise.
	if err := os.WriteFile(statPath, []byte(pdCovStatV2), 0o644); err != nil {
		t.Fatal(err)
	}
	usages, err = getCpuUsages()
	if err != nil {
		t.Fatalf("delta call: %v", err)
	}
	if len(usages) != 2 {
		t.Fatalf("got %d usages, want 2", len(usages))
	}
	want := 150.0 / 550.0 * 100
	for i, u := range usages {
		if math.Abs(u-want) > 1e-6 {
			t.Errorf("cpu%d = %v, want %v", i, u, want)
		}
	}

	// getCPUUsage aggregates a fresh window (same file -> zero delta -> 0).
	if avg, err := getCPUUsage(); err != nil || avg != 0 {
		t.Errorf("getCPUUsage zero-window = %v, %v; want 0, nil", avg, err)
	}

	// Error paths: missing file with and without a baseline.
	pdCovSetStr(t, &pdCovProcStatPath, pdCovMissingPath(t))
	if _, err := getCpuUsages(); err == nil {
		t.Error("missing stat with baseline: want error")
	}
	pdCovSetCPUPrev(nil)
	if _, err := getCpuUsages(); err == nil {
		t.Error("missing stat while priming: want error")
	}
	if _, err := getCPUUsage(); err == nil {
		t.Error("getCPUUsage with missing stat: want error")
	}
}

// ---- net interfaces / route ----------------------------------------------

func TestPDCovGetInterfaceBytes(t *testing.T) {
	netDir := t.TempDir()
	pdCovSetStr(t, &pdCovSysClassNetDir, netDir)
	pdCovWriteFile(t, netDir, "pdcov0/statistics/rx_bytes", "1000\n")
	pdCovWriteFile(t, netDir, "pdcov0/statistics/tx_bytes", "2000\n")

	rx, tx, err := getInterfaceBytes("pdcov0")
	if err != nil || rx != 1000 || tx != 2000 {
		t.Errorf("got rx=%d tx=%d err=%v; want 1000, 2000, nil", rx, tx, err)
	}

	if _, _, err := getInterfaceBytes("absent0"); err == nil {
		t.Error("missing iface: want error")
	}

	pdCovWriteFile(t, netDir, "pdcov1/statistics/rx_bytes", "10\n")
	if _, _, err := getInterfaceBytes("pdcov1"); err == nil {
		t.Error("missing tx_bytes: want error")
	}

	pdCovWriteFile(t, netDir, "pdcov2/statistics/rx_bytes", "junk\n")
	pdCovWriteFile(t, netDir, "pdcov2/statistics/tx_bytes", "1\n")
	if _, _, err := getInterfaceBytes("pdcov2"); err == nil {
		t.Error("garbage rx_bytes: want error")
	}
}

func TestPDCovGetSessionDataUsageGB(t *testing.T) {
	netDir := t.TempDir()
	pdCovSetStr(t, &pdCovSysClassNetDir, netDir)
	// 1 GiB down + 1 GiB up -> 2.0 GB session usage.
	pdCovWriteFile(t, netDir, "pdcovses0/statistics/rx_bytes", "1073741824")
	pdCovWriteFile(t, netDir, "pdcovses0/statistics/tx_bytes", "1073741824")

	if gb, err := getSessionDataUsageGB("pdcovses0"); err != nil || math.Abs(gb-2.0) > 1e-9 {
		t.Errorf("got %v, %v; want 2.0, nil", gb, err)
	}

	pdCovWriteFile(t, netDir, "pdcovses1/statistics/rx_bytes", "x")
	pdCovWriteFile(t, netDir, "pdcovses1/statistics/tx_bytes", "1")
	if _, err := getSessionDataUsageGB("pdcovses1"); err == nil {
		t.Error("garbage counter: want error")
	}
	if _, err := getSessionDataUsageGB("absent1"); err == nil {
		t.Error("missing iface: want error")
	}
}

const pdCovRouteHeader = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"

func TestPDCovGetWANInterface(t *testing.T) {
	dir := t.TempDir()

	// Lowest-metric default route wins.
	routes := pdCovRouteHeader +
		"pdceth0\t00000000\t0102A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"pdcwl0\t00000000\t0102A8C0\t0003\t0\t0\t50\t00000000\t0\t0\t0\n" +
		"pdceth1\t0000FEA9\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"
	pdCovSetStr(t, &pdCovProcNetRoutePath, pdCovWriteFile(t, dir, "route", routes))
	pdCovSetStr(t, &pdCovOpenwrtReleasePath, pdCovMissingPath(t))
	if dev, err := getWANInterface(); err != nil || dev != "pdcwl0" {
		t.Errorf("got %q, %v; want pdcwl0, nil", dev, err)
	}

	// No default route on OpenWrt -> br-lan fallback.
	pdCovSetStr(t, &pdCovProcNetRoutePath, pdCovWriteFile(t, dir, "nodefault",
		pdCovRouteHeader+"pdceth1\t0000FEA9\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"))
	pdCovSetStr(t, &pdCovOpenwrtReleasePath, pdCovWriteFile(t, dir, "openwrt_release", "yes"))
	if dev, err := getWANInterface(); err != nil || dev != "br-lan" {
		t.Errorf("openwrt fallback: got %q, %v; want br-lan, nil", dev, err)
	}

	// No default route, not OpenWrt -> error.
	pdCovSetStr(t, &pdCovOpenwrtReleasePath, pdCovMissingPath(t))
	if _, err := getWANInterface(); err == nil {
		t.Error("no default route on non-OpenWrt: want error")
	}

	// Route table unreadable, not OpenWrt -> error.
	pdCovSetStr(t, &pdCovProcNetRoutePath, pdCovMissingPath(t))
	if _, err := getWANInterface(); err == nil {
		t.Error("missing route table: want error")
	}
}

func TestPDCovIsOpenWRT(t *testing.T) {
	pdCovSetStr(t, &pdCovOpenwrtReleasePath, pdCovWriteFile(t, t.TempDir(), "rel", "x"))
	if !isOpenWRT() {
		t.Error("marker present: want true")
	}
	pdCovSetStr(t, &pdCovOpenwrtReleasePath, pdCovMissingPath(t))
	if isOpenWRT() {
		t.Error("marker absent: want false")
	}
}

func TestPDCovNetSpeedSamplerErrorPath(t *testing.T) {
	pdCovSetStr(t, &pdCovSysClassNetDir, t.TempDir())
	var s netSpeedSampler
	if _, _, err := s.sample("nosuchdev0"); err == nil {
		t.Error("sample on missing device: want error")
	}
}

func TestPDCovReadNetworkStats(t *testing.T) {
	dir := t.TempDir()
	content := "Inter-|   Receive                                                |  Transmit\n" +
		" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n" +
		"  eth0: 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0\n" +
		"  badrx: x 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0\n" +
		"  badtx: 100 10 0 0 0 0 0 0 y 20 0 0 0 0 0 0\n" +
		"  short: 1 2\n"
	pdCovSetStr(t, &pdCovProcNetDevPath, pdCovWriteFile(t, dir, "netdev", content))

	stats, err := readNetworkStats()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("got %d entries, want 1 (bad lines skipped): %v", len(stats), stats)
	}
	if s := stats["eth0"]; s.rxBytes != 1000 || s.txBytes != 2000 {
		t.Errorf("eth0 = %+v; want rx 1000 tx 2000", s)
	}

	pdCovSetStr(t, &pdCovProcNetDevPath, pdCovMissingPath(t))
	if _, err := readNetworkStats(); err == nil {
		t.Error("missing file: want error")
	}
}

// ---- fan / sd / disks ------------------------------------------------------

func TestPDCovGetFanSpeed(t *testing.T) {
	dir := t.TempDir()
	// hwmon0/fan1_input is a directory (unreadable), hwmon1 empty, hwmon2
	// garbage, hwmon3 valid -> 4200.
	if err := os.MkdirAll(filepath.Join(dir, "hwmon0", "fan1_input"), 0o755); err != nil {
		t.Fatal(err)
	}
	pdCovWriteFile(t, dir, "hwmon1/fan1_input", "\n")
	pdCovWriteFile(t, dir, "hwmon2/fan1_input", "fast\n")
	pdCovWriteFile(t, dir, "hwmon3/fan1_input", "4200\n")
	pdCovSetStr(t, &pdCovHwmonFanGlob, filepath.Join(dir, "hwmon*", "fan1_input"))

	if rpm, err := getFanSpeed(); err != nil || rpm != 4200 {
		t.Errorf("got %d, %v; want 4200, nil", rpm, err)
	}

	pdCovSetStr(t, &pdCovHwmonFanGlob, filepath.Join(t.TempDir(), "hwmon*", "fan1_input"))
	if _, err := getFanSpeed(); err == nil {
		t.Error("no fan nodes: want error")
	}

	pdCovSetStr(t, &pdCovHwmonFanGlob, "[") // malformed pattern
	if _, err := getFanSpeed(); err == nil {
		t.Error("bad glob pattern: want error")
	}
}

func TestPDCovSdCardDisks(t *testing.T) {
	dir := t.TempDir()
	pdCovWriteFile(t, dir, "sys/block/mmcblk0/device/type", "MMC\n")
	pdCovWriteFile(t, dir, "sys/block/mmcblk1/device/type", "SD\n")
	pdCovSetStr(t, &pdCovMmcTypeGlob, filepath.Join(dir, "sys/block/mmcblk*/device/type"))

	disks := sdCardDisks()
	if len(disks) != 1 || !disks["/dev/mmcblk1"] {
		t.Errorf("got %v; want only /dev/mmcblk1 (eMMC filtered)", disks)
	}

	pdCovSetStr(t, &pdCovMmcTypeGlob, filepath.Join(t.TempDir(), "sys/block/mmcblk*/device/type"))
	if disks := sdCardDisks(); len(disks) != 0 {
		t.Errorf("empty slot: got %v; want none", disks)
	}
}

func TestPDCovDiskStatfs(t *testing.T) {
	if used, total, pct, ok := diskStatfs(t.TempDir()); !ok {
		t.Error("statfs on real dir: want ok")
	} else if total <= 0 || used < 0 || pct < 0 || pct > 100 {
		t.Errorf("implausible statfs: used=%v total=%v pct=%v", used, total, pct)
	}
	if _, _, _, ok := diskStatfs(pdCovMissingPath(t)); ok {
		t.Error("statfs on missing path: want ok=false")
	}
}

func TestPDCovGetDiskUsage(t *testing.T) {
	data, err := getDiskUsage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	total, ok1 := data["Total"].(uint64)
	free, ok2 := data["Free"].(uint64)
	used, ok3 := data["Used"].(uint64)
	if !ok1 || !ok2 || !ok3 || total == 0 || used != total-free {
		t.Errorf("implausible disk data: %v", data)
	}
}

func TestPDCovCollectDiskUsage(t *testing.T) {
	dir := t.TempDir()
	nvmeMp := t.TempDir()
	sdMp := t.TempDir()

	pdCovWriteFile(t, dir, "sys/block/mmcblk1/device/type", "SD\n")
	pdCovSetStr(t, &pdCovMmcTypeGlob, filepath.Join(dir, "sys/block/mmcblk*/device/type"))

	mounts := "/dev/mmcblk0p7 / ext4 rw 0 0\n" +
		"/dev/nvme0n1p1 " + nvmeMp + " ext4 rw 0 0\n" +
		"/dev/mmcblk1p1 " + sdMp + " vfat rw 0 0\n" +
		"proc /proc proc rw 0 0\n"
	pdCovSetStr(t, &pdCovProcMountsPath, pdCovWriteFile(t, dir, "mounts", mounts))

	collectDiskUsage()

	if v := pdCovLoad("DiskNvmePresent"); v != true {
		t.Errorf("DiskNvmePresent = %v, want true", v)
	}
	if v := pdCovLoad("DiskSDPresent"); v != true {
		t.Errorf("DiskSDPresent = %v, want true", v)
	}
	if v, _ := pdCovLoad("DiskNvme").(string); v == "-" || v == "" || !strings.Contains(v, "/") {
		t.Errorf("DiskNvme = %q, want used/total string", v)
	}
	if v, _ := pdCovLoad("DiskUsage").(string); v == "-" || v == "" {
		t.Errorf("DiskUsage = %q, want used/total string", v)
	}

	// No mounts table and no card in the slot -> absent markers.
	pdCovSetStr(t, &pdCovProcMountsPath, pdCovMissingPath(t))
	pdCovSetStr(t, &pdCovMmcTypeGlob, filepath.Join(t.TempDir(), "none*/type"))
	collectDiskUsage()
	if v := pdCovLoad("DiskNvme"); v != "-" {
		t.Errorf("DiskNvme without mounts = %v, want \"-\"", v)
	}
	if v := pdCovLoad("DiskSDPresent"); v != false {
		t.Errorf("DiskSDPresent without card = %v, want false", v)
	}
}

// ---- misc ------------------------------------------------------------------

func TestPDCovGetLocalIPv4(t *testing.T) {
	ip, err := getLocalIPv4()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip == "" {
		t.Error("want non-empty result (address or LINK DOWN marker)")
	}
}

func TestPDCovPingICMPErrorPaths(t *testing.T) {
	// Empty target: resolver rejects it without touching the network.
	if ms, err := pingICMP(""); err == nil || ms != -1 {
		t.Errorf("empty host: got %d, %v; want -1, error", ms, err)
	}

	// Loopback: privileged raw sockets are unavailable to a normal test run,
	// so this exercises the Run() error path without emitting external ICMP.
	// (If the suite ever runs as root the probe legitimately succeeds.)
	ms, err := pingICMP("127.0.0.1")
	if err != nil {
		if ms != -1 {
			t.Errorf("failed loopback probe: ms = %d, want -1", ms)
		}
	} else if ms < -2 || ms > 3000 {
		t.Errorf("loopback probe out of range: %d", ms)
	}
}

func TestPDCovStoreWANSpeed(t *testing.T) {
	storeWANSpeed(12.5, 0.25)
	if v := pdCovLoad("WanUP"); v != "12.5" {
		t.Errorf("WanUP = %v, want 12.5", v)
	}
	if v := pdCovLoad("WanDOWN"); v != "0.25" {
		t.Errorf("WanDOWN = %v, want 0.25", v)
	}
	if v := pdCovLoad("WanUP_Unit"); v != "Mbps" {
		t.Errorf("WanUP_Unit = %v, want Mbps", v)
	}
}

func TestPDCovApplyRemainingTimeUnit(t *testing.T) {
	globalData.Store("RemainingTime_Unit", "h")
	applyRemainingTimeUnit()
	if v := pdCovLoad("RemainingTime_Unit"); v != "" {
		t.Errorf("RemainingTime_Unit = %v, want empty", v)
	}
}
