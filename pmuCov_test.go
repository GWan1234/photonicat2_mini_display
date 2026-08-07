package main

// Coverage tests for pcatPmu.go and pcatPmuSerial_other.go. All fixtures live
// under t.TempDir(); the sysfs/procfs/devicetree scans are pointed there via
// the pmuCov* path vars and restored afterwards, so nothing here ever touches
// real hardware. Every helper is prefixed pmuCov to avoid clashing with tests
// added by other worktrees.

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// pmuCovSwap sets *p to v for the duration of the test and restores the old
// value on cleanup.
func pmuCovSwap[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// pmuCovStashKeys snapshots the given globalData keys and restores them
// (including "absent") on cleanup. It does not delete them; callers that need
// a key gone call globalData.Delete themselves.
func pmuCovStashKeys(t *testing.T, keys ...string) {
	t.Helper()
	type entry struct {
		val any
		ok  bool
	}
	saved := make(map[string]entry, len(keys))
	for _, k := range keys {
		v, ok := globalData.Load(k)
		saved[k] = entry{v, ok}
	}
	t.Cleanup(func() {
		for k, e := range saved {
			if e.ok {
				globalData.Store(k, e.val)
			} else {
				globalData.Delete(k)
			}
		}
	})
}

// pmuCovStashPmuState snapshots the PMU atomics and UART connection state.
func pmuCovStashPmuState(t *testing.T) {
	t.Helper()
	uartActive := pmuUartActive.Load()
	sysfsActive := pmuSysfsActive.Load()
	fan := pmuLastFanRPM.Load()
	hasFan := pmuHasFanReading.Load()
	pmuUart.mu.Lock()
	port, frameNum := pmuUart.port, pmuUart.frameNum
	pmuUart.mu.Unlock()
	t.Cleanup(func() {
		pmuUartActive.Store(uartActive)
		pmuSysfsActive.Store(sysfsActive)
		pmuLastFanRPM.Store(fan)
		pmuHasFanReading.Store(hasFan)
		pmuUart.mu.Lock()
		pmuUart.port, pmuUart.frameNum = port, frameNum
		pmuUart.mu.Unlock()
		// Drain any ack token a test left behind.
		select {
		case <-pmuPoweroffAck:
		default:
		}
	})
}

// pmuCovWriteFile writes content to path, creating parent directories.
func pmuCovWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// pmuCovScript drops an executable shell script named name into dir.
func pmuCovScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script %s: %v", path, err)
	}
	return path
}

// pmuCovSetPath makes bin the first PATH entry, keeping the standard system
// dirs behind it so the stub scripts can still use cat & co. Restored by
// t.Setenv on cleanup.
func pmuCovSetPath(t *testing.T, bin string) {
	t.Helper()
	t.Setenv("PATH", bin+":/usr/bin:/bin")
}

// pmuCovPoweroffStub installs a harmless poweroff command in its own PATH-only
// dir (no system dirs — a real poweroff must be unreachable) and returns the
// marker file it creates when run.
func pmuCovPoweroffStub(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	marker := filepath.Join(bin, "poweroff.ran")
	// Only shell builtins, since PATH holds nothing but this directory.
	pmuCovScript(t, bin, "poweroff", "#!/bin/sh\n: > "+marker+"\n")
	t.Setenv("PATH", bin)
	return marker
}

// pmuCovWaitForFile polls until path exists or the deadline passes.
func pmuCovWaitForFile(t *testing.T, path string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("file %s never appeared", path)
}

// pmuCovMCUFrame builds an MCU→host frame (src 0x81) with an arbitrary
// destination, reusing pmuBuildFrame's layout and re-signing the CRC.
func pmuCovMCUFrame(frameNum, command uint16, extra []byte, needAck bool, dst byte) []byte {
	f := pmuBuildFrame(frameNum, command, extra, needAck)
	f[1] = 0x81
	f[2] = dst
	dp := len(extra) + 3
	crc := pmuCRC16(f[1 : 7+dp])
	f[7+dp] = byte(crc)
	f[8+dp] = byte(crc >> 8)
	return f
}

// pmuCovStatusPayload returns a 52-byte STATUS_REPORT payload with known
// values (48 °C, gs 10/-20/30 ready, fan 4321 rpm).
func pmuCovStatusPayload() []byte {
	data := make([]byte, 52)
	binary.LittleEndian.PutUint16(data[0:], 4000)
	binary.LittleEndian.PutUint16(data[2:], 5000)
	data[17] = 148 // 48 °C
	binary.LittleEndian.PutUint16(data[18:], 100)
	data[22] = 55
	binary.LittleEndian.PutUint32(data[35:], 10)
	binary.LittleEndian.PutUint32(data[39:], uint32(0xFFFFFFEC)) // -20
	binary.LittleEndian.PutUint32(data[43:], 30)
	data[47] = 1
	binary.LittleEndian.PutUint32(data[48:], 4321)
	return data
}

// ---- pure logic ----------------------------------------------------------

func TestPmuCovUartFanRPM(t *testing.T) {
	pmuCovStashPmuState(t)

	pmuUartActive.Store(false)
	pmuHasFanReading.Store(true)
	pmuLastFanRPM.Store(1200)
	if _, ok := pmuUartFanRPM(); ok {
		t.Error("fan RPM reported while UART inactive")
	}

	pmuUartActive.Store(true)
	pmuHasFanReading.Store(false)
	if _, ok := pmuUartFanRPM(); ok {
		t.Error("fan RPM reported before any reading")
	}

	pmuHasFanReading.Store(true)
	if rpm, ok := pmuUartFanRPM(); !ok || rpm != 1200 {
		t.Errorf("pmuUartFanRPM() = %d,%v; want 1200,true", rpm, ok)
	}
}

func TestPmuCovParseFramesEdgeCases(t *testing.T) {
	pad := func(b []byte, n int) []byte {
		for len(b) < n {
			b = append(b, 0x11)
		}
		return b
	}

	t.Run("length too small", func(t *testing.T) {
		buf := pad([]byte{0xA5, 0x01, 0x01, 0, 0, 0x02, 0x00}, 13)
		frames, consumed := pmuParseFrames(buf)
		if len(frames) != 0 || consumed != len(buf) {
			t.Errorf("got %d frames, consumed %d; want 0, %d", len(frames), consumed, len(buf))
		}
	})

	t.Run("length too large", func(t *testing.T) {
		buf := pad([]byte{0xA5, 0x01, 0x01, 0, 0, 0x04, 0x02}, 13) // 516 > 515
		frames, consumed := pmuParseFrames(buf)
		if len(frames) != 0 || consumed != len(buf) {
			t.Errorf("got %d frames, consumed %d; want 0, %d", len(frames), consumed, len(buf))
		}
	})

	t.Run("incomplete body after garbage", func(t *testing.T) {
		buf := append([]byte{0xFF}, pad([]byte{0xA5, 0x81, 0x01, 0, 0, 50, 0, 0x07, 0x00}, 14)...)
		frames, consumed := pmuParseFrames(buf)
		if len(frames) != 0 || consumed != 1 {
			t.Errorf("got %d frames, consumed %d; want 0 frames, 1 (only the garbage byte)", len(frames), consumed)
		}
	})

	t.Run("bad tail byte", func(t *testing.T) {
		frame := pmuBuildFrame(7, pmuCmdHeartbeat, nil, false)
		frame[len(frame)-1] = 0x00
		frames, _ := pmuParseFrames(frame)
		if len(frames) != 0 {
			t.Errorf("frame with bad tail parsed as valid")
		}
	})

	t.Run("two frames back to back", func(t *testing.T) {
		a := pmuBuildFrame(1, pmuCmdHeartbeat, nil, false)
		b := pmuBuildFrame(2, pmuCmdStatusReport, []byte{9, 8, 7}, true)
		stream := append(append([]byte{}, a...), b...)
		frames, consumed := pmuParseFrames(stream)
		if len(frames) != 2 || consumed != len(stream) {
			t.Fatalf("got %d frames, consumed %d; want 2, %d", len(frames), consumed, len(stream))
		}
		if frames[0].frameNum != 1 || frames[1].frameNum != 2 {
			t.Errorf("frame numbers = %d,%d; want 1,2", frames[0].frameNum, frames[1].frameNum)
		}
		if !bytes.Equal(frames[1].extra, []byte{9, 8, 7}) {
			t.Errorf("second frame extra = % X; want 09 08 07", frames[1].extra)
		}
	})
}

func TestPmuCovApplyStatus(t *testing.T) {
	keys := []string{"BoardTemperature", "GsX", "GsY", "GsZ", "GsReady"}
	pmuCovStashKeys(t, keys...)
	pmuCovStashPmuState(t)
	for _, k := range keys {
		globalData.Delete(k)
	}

	// Empty status stores nothing.
	pmuHasFanReading.Store(false)
	pmuApplyStatus(pmuStatus{})
	for _, k := range keys {
		if _, ok := globalData.Load(k); ok {
			t.Errorf("empty status stored %s", k)
		}
	}
	if pmuHasFanReading.Load() {
		t.Error("empty status set the fan-reading flag")
	}

	st, ok := pmuParseStatusReport(pmuCovStatusPayload())
	if !ok {
		t.Fatal("synthetic status payload rejected")
	}
	pmuApplyStatus(st)
	if v, _ := globalData.Load("BoardTemperature"); v != 48 {
		t.Errorf("BoardTemperature = %v; want 48", v)
	}
	if x, _ := globalData.Load("GsX"); x != 10 {
		t.Errorf("GsX = %v; want 10", x)
	}
	if y, _ := globalData.Load("GsY"); y != -20 {
		t.Errorf("GsY = %v; want -20", y)
	}
	if z, _ := globalData.Load("GsZ"); z != 30 {
		t.Errorf("GsZ = %v; want 30", z)
	}
	if r, _ := globalData.Load("GsReady"); r != true {
		t.Errorf("GsReady = %v; want true", r)
	}
	if !pmuHasFanReading.Load() || pmuLastFanRPM.Load() != 4321 {
		t.Errorf("fan = %d (has=%v); want 4321,true", pmuLastFanRPM.Load(), pmuHasFanReading.Load())
	}
}

// ---- mode selection with fixture sysfs -----------------------------------

func TestPmuCovKernelDriverPresent(t *testing.T) {
	dir := t.TempDir()
	sysPath := filepath.Join(dir, "photonicat-pm")
	ctlPath := filepath.Join(dir, "pcat-pm-ctl")
	pmuCovSwap(t, &pmuCovSysKernelPMPath, sysPath)
	pmuCovSwap(t, &pmuCovDevPMCtlPath, ctlPath)

	if pmuKernelDriverPresent() {
		t.Error("driver reported present with neither node existing")
	}
	pmuCovWriteFile(t, ctlPath, "")
	if !pmuKernelDriverPresent() {
		t.Error("driver not detected via the ctl device node")
	}
	if err := os.Remove(ctlPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sysPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if !pmuKernelDriverPresent() {
		t.Error("driver not detected via the sysfs directory")
	}
}

func TestPmuCovDetectSerialDevice(t *testing.T) {
	root := t.TempDir()
	aliasDir := filepath.Join(root, "aliases")
	dtBase := filepath.Join(root, "dt")
	pmuCovSwap(t, &pmuCovDTAliasGlob, filepath.Join(aliasDir, "serial*"))
	pmuCovSwap(t, &pmuCovDTBasePath, dtBase)

	// Device-tree nodes.
	pmuCovWriteFile(t, filepath.Join(dtBase, "serial@fe650000", "pcat-pm", "compatible"),
		"photonicat-pm\x00")
	pmuCovWriteFile(t, filepath.Join(dtBase, "other", "pcat-pm", "compatible"),
		"some-other-driver\x00")

	// Aliases, in glob (lexical) order:
	//   serial-x  → valid node but non-numeric suffix (skipped)
	//   serial0   → dangling node (compatible unreadable)
	//   serial00  → a directory, alias itself unreadable
	//   serial1   → node with the wrong compatible
	//   serial10  → the real PMU UART
	pmuCovWriteFile(t, filepath.Join(aliasDir, "serial-x"), "/serial@fe650000\x00")
	pmuCovWriteFile(t, filepath.Join(aliasDir, "serial0"), "/nonexistent\x00")
	if err := os.MkdirAll(filepath.Join(aliasDir, "serial00"), 0o755); err != nil {
		t.Fatal(err)
	}
	pmuCovWriteFile(t, filepath.Join(aliasDir, "serial1"), "/other\x00")
	pmuCovWriteFile(t, filepath.Join(aliasDir, "serial10"), "/serial@fe650000\x00")

	if got := pmuDetectSerialDevice(); got != "/dev/ttyS10" {
		t.Errorf("pmuDetectSerialDevice() = %q; want /dev/ttyS10", got)
	}

	pmuCovSwap(t, &pmuCovDTAliasGlob, filepath.Join(root, "empty", "serial*"))
	if got := pmuDetectSerialDevice(); got != "" {
		t.Errorf("pmuDetectSerialDevice() with no aliases = %q; want \"\"", got)
	}
}

func TestPmuCovPortUsers(t *testing.T) {
	root := t.TempDir()
	dev := filepath.Join(root, "ttyFake")
	other := filepath.Join(root, "ttyOther")
	pmuCovWriteFile(t, dev, "")
	pmuCovWriteFile(t, other, "")

	link := func(pid, fd, target string) {
		dir := filepath.Join(root, pid, "fd")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, fd)); err != nil {
			t.Fatal(err)
		}
	}
	link("123", "4", dev)   // a real user of the port
	link("456", "9", other) // holds a different device
	self := strconv.Itoa(os.Getpid())
	link(self, "7", dev) // ourselves — must be skipped
	// A plain file where a symlink should be: Readlink fails, skipped.
	pmuCovWriteFile(t, filepath.Join(root, "789", "fd", "3"), "not a link")

	pmuCovSwap(t, &pmuCovProcFdGlob, filepath.Join(root, "[0-9]*", "fd", "*"))
	pmuCovSwap(t, &pmuCovProcPrefix, root+string(os.PathSeparator))

	users := pmuPortUsers(dev)
	if len(users) != 1 || users[0] != "123" {
		t.Errorf("pmuPortUsers() = %v; want [123]", users)
	}
}

func TestPmuCovStartPmuManagerNoHardware(t *testing.T) {
	root := t.TempDir()
	pmuCovStashPmuState(t)
	pmuCovSwap(t, &pmuCovSysKernelPMPath, filepath.Join(root, "nokernel"))
	pmuCovSwap(t, &pmuCovDevPMCtlPath, filepath.Join(root, "noctl"))
	pmuCovSwap(t, &pmuCovDTAliasGlob, filepath.Join(root, "aliases", "serial*"))

	pmuSysfsActive.Store(false)
	startPmuManager() // must return without starting anything
	if pmuSysfsActive.Load() {
		t.Error("startPmuManager activated sysfs mode with no driver present")
	}
	if pmuUartActive.Load() {
		t.Error("startPmuManager activated UART mode with no device-tree node")
	}
}

func TestPmuCovFindTempMbHwmon(t *testing.T) {
	root := t.TempDir()
	pmuCovSwap(t, &pmuCovHwmonGlob, filepath.Join(root, "hwmon*"))

	if got := pmuFindTempMbHwmon(); got != "" {
		t.Errorf("empty hwmon dir returned %q", got)
	}

	if err := os.MkdirAll(filepath.Join(root, "hwmon0"), 0o755); err != nil {
		t.Fatal(err) // no name file → skipped
	}
	pmuCovWriteFile(t, filepath.Join(root, "hwmon1", "name"), "cpu_thermal\n")
	pmuCovWriteFile(t, filepath.Join(root, "hwmon2", "name"), "pcat_pm_hwmon_temp_mb\n")

	want := filepath.Join(root, "hwmon2", "temp1_input")
	if got := pmuFindTempMbHwmon(); got != want {
		t.Errorf("pmuFindTempMbHwmon() = %q; want %q", got, want)
	}
}

func TestPmuCovReadSysfsInt(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	bad := filepath.Join(dir, "bad")
	pmuCovWriteFile(t, good, " 42500\n")
	pmuCovWriteFile(t, bad, "not-a-number\n")

	if v, err := readSysfsInt(good); err != nil || v != 42500 {
		t.Errorf("readSysfsInt(good) = %d, %v; want 42500, nil", v, err)
	}
	if _, err := readSysfsInt(bad); err == nil {
		t.Error("readSysfsInt(bad) succeeded on garbage")
	}
	if _, err := readSysfsInt(filepath.Join(dir, "missing")); err == nil {
		t.Error("readSysfsInt(missing) succeeded on a missing file")
	}
}

// ---- UART loops against in-memory pipes ----------------------------------

func TestPmuCovUartSendNoPort(t *testing.T) {
	pmuCovStashPmuState(t)
	pmuUart.mu.Lock()
	pmuUart.port = nil
	pmuUart.mu.Unlock()
	if err := pmuUartSend(pmuCmdHeartbeat, nil, false); err == nil {
		t.Error("pmuUartSend succeeded with no port connected")
	}
}

func TestPmuCovHeartbeatLoopStop(t *testing.T) {
	pmuCovStashPmuState(t)
	pmuUart.mu.Lock()
	pmuUart.port = nil
	pmuUart.mu.Unlock()

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		pmuHeartbeatLoop(stop)
		close(done)
	}()
	close(stop)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("heartbeat loop did not exit on stop")
	}
}

func TestPmuCovHeartbeatLoopSendError(t *testing.T) {
	pmuCovStashPmuState(t)
	pmuUart.mu.Lock()
	pmuUart.port = nil // first tick's send fails → loop must exit
	pmuUart.mu.Unlock()

	stop := make(chan struct{})
	defer close(stop)
	done := make(chan struct{})
	go func() {
		pmuHeartbeatLoop(stop)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("heartbeat loop did not exit after a send error")
	}
}

func TestPmuCovUartReadLoopHandlesFrames(t *testing.T) {
	marker := pmuCovPoweroffStub(t) // any "poweroff" resolves to the harmless stub

	pmuCovStashKeys(t, "BoardTemperature", "GsX", "GsY", "GsZ", "GsReady")
	pmuCovStashPmuState(t)
	pmuUart.mu.Lock()
	pmuUart.port = nil // acks fail silently instead of touching hardware
	pmuUart.mu.Unlock()
	select {
	case <-pmuPoweroffAck:
	default:
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })

	done := make(chan struct{})
	go func() {
		pmuUartReadLoop(r)
		close(done)
	}()

	status := pmuCovStatusPayload()
	write := func(b []byte) {
		if _, err := w.Write(b); err != nil {
			t.Errorf("pipe write: %v", err)
		}
	}
	write([]byte{0x00, 0x11}) // leading garbage
	write(pmuCovMCUFrame(1, pmuCmdStatusReport, status, false, 0x01))
	write(pmuCovMCUFrame(2, 0x42, nil, true, 0x01)) // unknown cmd, generic ack path
	write(pmuCovMCUFrame(3, pmuCmdHostRequestShutdownAck, nil, true, 0x80))
	write(pmuCovMCUFrame(4, pmuCmdStatusReport, status, false, 0x55)) // filtered dst
	write(pmuCovMCUFrame(5, pmuCmdPmuRequestShutdown, nil, true, 0xFF))
	// Runaway garbage: an incomplete giant frame followed by filler must trip
	// the 4096-byte resync without wedging the loop.
	write([]byte{0xA5, 0x81, 0x01, 0, 0, 0xFF, 0x01})
	write(make([]byte, 5000))
	w.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("read loop did not exit when the pipe closed")
	}

	if v, _ := globalData.Load("BoardTemperature"); v != 48 {
		t.Errorf("BoardTemperature = %v; want 48 (status frame not applied)", v)
	}
	if v, _ := globalData.Load("GsX"); v != 10 {
		t.Errorf("GsX = %v; want 10", v)
	}
	select {
	case <-pmuPoweroffAck:
	default:
		t.Error("shutdown-ack frame did not signal pmuPoweroffAck")
	}
	// The MCU shutdown request must have run our stub poweroff, not a real one.
	pmuCovWaitForFile(t, marker, 3*time.Second)
}

// ---- power off (stubbed poweroff binary only) ----------------------------

func TestPmuCovPowerOffNoUart(t *testing.T) {
	marker := pmuCovPoweroffStub(t)

	pmuCovStashPmuState(t)
	pmuUartActive.Store(false)

	if err := pmuPowerOff(); err != nil {
		t.Fatalf("pmuPowerOff() = %v; want nil (stub poweroff exits 0)", err)
	}
	pmuCovWaitForFile(t, marker, 3*time.Second)
}

func TestPmuCovPowerOffUartSendError(t *testing.T) {
	pmuCovPoweroffStub(t) // defense in depth; this path must never exec

	pmuCovStashPmuState(t)
	pmuUartActive.Store(true)
	pmuUart.mu.Lock()
	pmuUart.port = nil
	pmuUart.mu.Unlock()

	if err := pmuPowerOff(); err == nil {
		t.Error("pmuPowerOff() succeeded although the UART send failed")
	}
}

func TestPmuCovPowerOffAcked(t *testing.T) {
	marker := pmuCovPoweroffStub(t)

	pmuCovStashPmuState(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	pmuUart.mu.Lock()
	pmuUart.port = w
	pmuUart.frameNum = 0
	pmuUart.mu.Unlock()
	pmuUartActive.Store(true)

	// Fake MCU: as soon as the shutdown-request frame arrives, ack it.
	go func() {
		buf := make([]byte, 64)
		if _, err := r.Read(buf); err == nil {
			pmuPoweroffAck <- struct{}{}
		}
	}()

	if err := pmuPowerOff(); err != nil {
		t.Fatalf("pmuPowerOff() = %v; want nil", err)
	}
	pmuCovWaitForFile(t, marker, 3*time.Second)
}

// ---- non-Linux serial stub -----------------------------------------------

func TestPmuCovOpenSerialStubOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("real termios implementation on Linux; the stub only exists elsewhere")
	}
	port, err := pmuOpenSerial("/dev/null", pmuDefaultBaud)
	if err == nil || port != nil {
		t.Errorf("pmuOpenSerial stub = %v, %v; want nil, error", port, err)
	}
	if len(pmuBaudConstants) != 0 {
		t.Errorf("pmuBaudConstants should be empty off-Linux, got %v", pmuBaudConstants)
	}
}
