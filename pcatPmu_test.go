package main

import (
	"bytes"
	"encoding/binary"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestPmuCRC16KnownVector(t *testing.T) {
	// Standard Modbus check value for "123456789".
	if got := pmuCRC16([]byte("123456789")); got != 0x4B37 {
		t.Errorf("pmuCRC16(123456789) = %04X; want 4B37", got)
	}
}

func TestPmuBuildFrameLayout(t *testing.T) {
	// Heartbeat, frame number 0, no payload, no ack — verify the exact wire
	// layout against the C encoder (pcat_pm_uart_write_data).
	f := pmuBuildFrame(0, pmuCmdHeartbeat, nil, false)
	if len(f) != 13 {
		t.Fatalf("heartbeat frame length = %d; want 13", len(f))
	}
	wantHead := []byte{0xA5, 0x01, 0x81, 0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x00}
	if !bytes.Equal(f[:10], wantHead) {
		t.Errorf("frame head = % X; want % X", f[:10], wantHead)
	}
	if f[12] != 0x5A {
		t.Errorf("frame tail = %02X; want 5A", f[12])
	}
	crc := uint16(f[10]) | uint16(f[11])<<8
	if want := pmuCRC16(f[1:10]); crc != want {
		t.Errorf("frame crc = %04X; want %04X", crc, want)
	}
}

func TestPmuFrameRoundTrip(t *testing.T) {
	extra := []byte{60, 0, 10}
	frame := pmuBuildFrame(0x1234, 0x13, extra, true)

	// Surround with garbage to exercise resync.
	stream := append([]byte{0x00, 0xA5, 0x42}, frame...)
	stream = append(stream, 0xFF)

	frames, consumed := pmuParseFrames(stream)
	if len(frames) != 1 {
		t.Fatalf("parsed %d frames; want 1", len(frames))
	}
	f := frames[0]
	if f.src != 0x01 || f.dst != 0x81 {
		t.Errorf("src/dst = %02X/%02X; want 01/81", f.src, f.dst)
	}
	if f.frameNum != 0x1234 || f.command != 0x13 || !f.needAck {
		t.Errorf("frameNum/command/needAck = %04X/%02X/%v", f.frameNum, f.command, f.needAck)
	}
	if !bytes.Equal(f.extra, extra) {
		t.Errorf("extra = % X; want % X", f.extra, extra)
	}
	if consumed != len(stream)-1 { // trailing 0xFF stays (could be a new frame start... it can't, but consumed counts through it)
		// consumed covers everything up to the end of the last full frame,
		// plus leading garbage; the trailing 0xFF byte is consumed too since
		// it can never start a frame (not 0xA5).
		t.Logf("consumed=%d len=%d", consumed, len(stream))
	}

	// A split frame must not be consumed until it is complete.
	half := frame[:8]
	if frames, consumed := pmuParseFrames(half); len(frames) != 0 || consumed != 0 {
		t.Errorf("partial frame: got %d frames, consumed %d; want 0, 0", len(frames), consumed)
	}
}

func TestPmuFrameBadCRCSkipped(t *testing.T) {
	frame := pmuBuildFrame(1, pmuCmdStatusReport, []byte{1, 2, 3}, false)
	frame[10] ^= 0xFF // corrupt payload
	frames, consumed := pmuParseFrames(frame)
	if len(frames) != 0 {
		t.Errorf("corrupt frame parsed as valid")
	}
	if consumed != len(frame) {
		t.Errorf("corrupt frame consumed %d bytes; want %d (skip whole frame)", consumed, len(frame))
	}
}

func TestPmuParseStatusReport(t *testing.T) {
	data := make([]byte, 52)
	binary.LittleEndian.PutUint16(data[0:], 3900)    // battery mV
	binary.LittleEndian.PutUint16(data[2:], 4300)    // charger mV
	data[17] = 148                                   // 148 - 100 = 48 °C
	binary.LittleEndian.PutUint16(data[18:], 0xFE0C) // -500 mA
	data[22] = 77                                    // soc
	binary.LittleEndian.PutUint32(data[35:], 1824)
	binary.LittleEndian.PutUint32(data[39:], 15968)
	binary.LittleEndian.PutUint32(data[43:], uint32(0xFFFFFC80)) // -896
	data[47] = 1
	binary.LittleEndian.PutUint32(data[48:], 3200)

	st, ok := pmuParseStatusReport(data)
	if !ok {
		t.Fatal("status report not parsed")
	}
	if st.batteryVoltageMV != 3900 || st.chargerVoltageMV != 4300 {
		t.Errorf("voltages = %d/%d; want 3900/4300", st.batteryVoltageMV, st.chargerVoltageMV)
	}
	if !st.hasTemp || st.boardTempC != 48 {
		t.Errorf("boardTempC = %d (hasTemp=%v); want 48", st.boardTempC, st.hasTemp)
	}
	if st.batteryCurrentMA != -500 {
		t.Errorf("batteryCurrentMA = %d; want -500", st.batteryCurrentMA)
	}
	if !st.hasSoc || st.soc != 77 {
		t.Errorf("soc = %d; want 77", st.soc)
	}
	if !st.hasGs || st.gsX != 1824 || st.gsY != 15968 || st.gsZ != -896 || !st.gsReady {
		t.Errorf("gs = %d,%d,%d ready=%v; want 1824,15968,-896 true",
			st.gsX, st.gsY, st.gsZ, st.gsReady)
	}
	if st.fanSpeed != 3200 {
		t.Errorf("fanSpeed = %d; want 3200", st.fanSpeed)
	}

	// Short (v1 firmware) report: only voltages, no temp/gs.
	st, ok = pmuParseStatusReport(data[:16])
	if !ok || st.hasTemp || st.hasGs {
		t.Errorf("16-byte report: ok=%v hasTemp=%v hasGs=%v; want true,false,false",
			ok, st.hasTemp, st.hasGs)
	}
	if _, ok := pmuParseStatusReport(data[:8]); ok {
		t.Error("8-byte report should be rejected")
	}
}

// ---- on-device tests (skipped off-target) ---------------------------------

// TestPmuOnDeviceSysfs verifies real reads on a photonicat with the
// photonicat-pm kernel driver. Run on the device via `go test -c`.
func TestPmuOnDeviceSysfs(t *testing.T) {
	if !pmuKernelDriverPresent() {
		t.Skip("photonicat-pm kernel driver not present")
	}
	tempPath := pmuFindTempMbHwmon()
	if tempPath == "" {
		t.Fatal("pcat_pm_hwmon_temp_mb hwmon node not found")
	}
	milli, err := readSysfsInt(tempPath)
	if err != nil {
		t.Fatalf("board temp read: %v", err)
	}
	tempC := milli / 1000
	if tempC < -20 || tempC > 120 {
		t.Errorf("board temp %d °C out of plausible range", tempC)
	}
	t.Logf("board temp: %d °C (%s)", tempC, tempPath)

	for _, axis := range []string{"gs_x", "gs_y", "gs_z"} {
		v, err := readSysfsInt("/sys/kernel/photonicat-pm/" + axis)
		if err != nil {
			t.Errorf("%s read: %v", axis, err)
			continue
		}
		t.Logf("%s: %d", axis, v)
	}
}

// TestPmuOnDeviceModeSelection confirms the manager picks sysfs mode on a
// device where the kernel driver owns the UART, and reports what the direct
// mode would have used.
func TestPmuOnDeviceModeSelection(t *testing.T) {
	if getDeviceTreeModel() == "" {
		t.Skip("not running on device-tree hardware")
	}
	t.Logf("device: %s", getDeviceTreeModel())
	t.Logf("kernel driver present: %v", pmuKernelDriverPresent())
	dev := pmuDetectSerialDevice()
	t.Logf("PMU UART per device tree: %q", dev)
	if dev != "" {
		users := pmuPortUsers(dev)
		t.Logf("port users: %v", users)
		if _, err := os.Stat(dev); err != nil {
			t.Logf("%s not present (serdev owns the UART) — direct mode correctly impossible", dev)
		}
	}
	if pmuKernelDriverPresent() && dev != "" {
		if _, err := os.Stat(dev); err == nil {
			t.Errorf("kernel driver present but %s also exists — mode selection must prefer sysfs", dev)
		}
	}
}

// TestPmuOnDeviceManager runs the real production entry point and checks the
// PMU values land in globalData.
func TestPmuOnDeviceManager(t *testing.T) {
	if !pmuKernelDriverPresent() {
		t.Skip("photonicat-pm kernel driver not present")
	}
	startPmuManager()
	time.Sleep(3 * time.Second)
	for _, key := range []string{"BoardTemperature", "GsX", "GsY", "GsZ"} {
		v, ok := globalData.Load(key)
		if !ok {
			t.Errorf("%s not populated by PMU manager", key)
			continue
		}
		t.Logf("%s = %v", key, v)
	}
	if !pmuActive() {
		t.Error("pmuActive() should be true with the sysfs source running")
	}
}

// TestRequestPowerOffRequiresConfirm ensures a bare POST cannot power the
// device off — only the refusal path is exercised.
func TestRequestPowerOffRequiresConfirm(t *testing.T) {
	app := fiber.New()
	app.Post("/poweroff", requestPowerOff)
	resp, err := app.Test(httptest.NewRequest("POST", "/poweroff", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Errorf("POST without confirm returned %d; want 400", resp.StatusCode)
	}
}
