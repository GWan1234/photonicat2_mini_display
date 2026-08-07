package main

// pcatPmu.go talks to the photonicat power-management MCU (PMU) to obtain the
// true board temperature, the G-sensor X/Y/Z readings and to request power
// off. It is a Go port of the C implementation in pcat-manager
// (mods/photonicat-pm.c and src/pmu-manager.c).
//
// Two data paths, picked automatically:
//
//  1. sysfs mode — when the photonicat-pm kernel driver is loaded it owns the
//     PMU UART (serdev) and re-exports everything we need:
//     /sys/kernel/photonicat-pm/gs_{x,y,z} and the pcat_pm_hwmon_temp_mb
//     hwmon node. Power off is just the system "poweroff" command: the
//     driver's sys-off handler performs the MCU handshake.
//
//  2. direct UART mode — on a plain Debian install without the kernel driver
//     the PMU UART is a free /dev/ttySx. We locate it via the device tree
//     (the serial node carrying a "photonicat-pm" child), wait a settle
//     period after startup, verify twice that no other process has the port
//     open, then claim it exclusively (flock + TIOCEXCL) and speak the frame
//     protocol ourselves: 1 s heartbeats out, status reports in.
//
// Frame layout (both directions), little-endian, from photonicat-pm.c:
//
//	0xA5 | src | dst | frame#(2) | len(2, =extra+3) | cmd(2) | extra… |
//	needAck(1) | crc16(2, over src..needAck) | 0x5A

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	pmuCmdHeartbeat              = 0x01
	pmuCmdStatusReport           = 0x07
	pmuCmdPmuRequestShutdown     = 0x0D
	pmuCmdHostRequestShutdown    = 0x0F
	pmuCmdHostRequestShutdownAck = 0x10
)

const (
	// "waiting for a while" before touching the UART: give pcat-manager or
	// the kernel driver every chance to claim the port first after power up.
	pmuUartSettleDelay  = 30 * time.Second
	pmuUartRecheckDelay = 10 * time.Second
	pmuUartRetryDelay   = 60 * time.Second

	pmuSysfsPollInterval = 2 * time.Second
	pmuDefaultBaud       = 115200
)

type pmuUartConn struct {
	mu       sync.Mutex
	port     *os.File
	frameNum uint16
}

var (
	pmuUart          pmuUartConn
	pmuUartActive    atomic.Bool
	pmuSysfsActive   atomic.Bool
	pmuLastFanRPM    atomic.Int64
	pmuHasFanReading atomic.Bool
	pmuPoweroffAck   = make(chan struct{}, 1)
)

// Filesystem locations used by the PMU detection/sysfs scans. Package-level
// vars (defaulted to the real paths) purely so tests can point them at
// fixture directories; production behavior is identical.
var (
	pmuCovSysKernelPMPath = "/sys/kernel/photonicat-pm"
	pmuCovDevPMCtlPath    = "/dev/pcat-pm-ctl"
	pmuCovDTAliasGlob     = "/sys/firmware/devicetree/base/aliases/serial*"
	pmuCovDTBasePath      = "/sys/firmware/devicetree/base"
	pmuCovProcFdGlob      = "/proc/[0-9]*/fd/*"
	pmuCovProcPrefix      = "/proc/"
	pmuCovHwmonGlob       = "/sys/class/hwmon/hwmon*"
)

// pmuActive reports whether either PMU data path is delivering board
// temperature (used by the Linux fallback sweep to avoid overwriting the
// real MCU reading with a CPU-temperature approximation).
func pmuActive() bool {
	return pmuSysfsActive.Load() || pmuUartActive.Load()
}

// pmuUartFanRPM returns the fan speed from the last UART status report.
func pmuUartFanRPM() (int, bool) {
	if !pmuUartActive.Load() || !pmuHasFanReading.Load() {
		return 0, false
	}
	return int(pmuLastFanRPM.Load()), true
}

// ---- protocol: CRC / frame build / frame parse ---------------------------

// pmuCRC16 is the Modbus CRC-16 used by the PMU protocol (poly 0xA001,
// init 0xFFFF), a direct port of pcat_pm_compute_crc16().
func pmuCRC16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for j := 0; j < 8; j++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// pmuBuildFrame serializes one host→MCU frame (src 0x01, dst 0x81), a port
// of pcat_pm_uart_write_data().
func pmuBuildFrame(frameNum, command uint16, extra []byte, needAck bool) []byte {
	dp := len(extra) + 3
	buf := make([]byte, 0, dp+10)
	buf = append(buf, 0xA5, 0x01, 0x81)
	buf = append(buf, byte(frameNum), byte(frameNum>>8))
	buf = append(buf, byte(dp), byte(dp>>8))
	buf = append(buf, byte(command), byte(command>>8))
	buf = append(buf, extra...)
	if needAck {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	crc := pmuCRC16(buf[1 : 7+dp])
	buf = append(buf, byte(crc), byte(crc>>8))
	buf = append(buf, 0x5A)
	return buf
}

type pmuFrame struct {
	src, dst byte
	frameNum uint16
	command  uint16
	extra    []byte
	needAck  bool
}

// pmuParseFrames extracts complete frames from buf and reports how many
// bytes were consumed (callers keep the tail for the next read). Port of
// pcat_pm_uart_receive_parse().
func pmuParseFrames(buf []byte) (frames []pmuFrame, consumed int) {
	i := 0
	used := 0
	for i < len(buf) {
		if buf[i] != 0xA5 {
			i++
			used = i
			continue
		}
		p := buf[i:]
		if len(p) < 13 {
			break // incomplete header, wait for more data
		}
		expectLen := int(p[5]) | int(p[6])<<8
		if expectLen < 3 || expectLen > 515 {
			i++
			used = i
			continue
		}
		if expectLen+10 > len(p) {
			break // incomplete body
		}
		if p[9+expectLen] != 0x5A {
			i++
			used = i
			continue
		}
		checksum := uint16(p[7+expectLen]) | uint16(p[8+expectLen])<<8
		if checksum != pmuCRC16(p[1:7+expectLen]) {
			i += 10 + expectLen
			used = i
			continue
		}
		f := pmuFrame{
			src:      p[1],
			dst:      p[2],
			frameNum: uint16(p[3]) | uint16(p[4])<<8,
			command:  uint16(p[7]) | uint16(p[8])<<8,
			needAck:  p[6+expectLen] != 0,
		}
		if expectLen > 3 {
			f.extra = append([]byte(nil), p[9:9+expectLen-3]...)
		}
		frames = append(frames, f)
		i += 10 + expectLen
		used = i
	}
	return frames, used
}

// pmuStatus is the decoded STATUS_REPORT payload (pcat_pm_status_report_parse).
type pmuStatus struct {
	batteryVoltageMV int
	chargerVoltageMV int
	boardTempC       int
	batteryCurrentMA int
	soc              int
	gsX, gsY, gsZ    int32
	gsReady          bool
	fanSpeed         uint32
	hasTemp          bool
	hasGs            bool
	hasSoc           bool
}

func pmuParseStatusReport(data []byte) (pmuStatus, bool) {
	var st pmuStatus
	if len(data) < 16 {
		return st, false
	}
	le16 := func(off int) int { return int(data[off]) | int(data[off+1])<<8 }
	le32 := func(off int) uint32 {
		return uint32(data[off]) | uint32(data[off+1])<<8 |
			uint32(data[off+2])<<16 | uint32(data[off+3])<<24
	}

	st.batteryVoltageMV = le16(0)
	st.chargerVoltageMV = le16(2)
	if len(data) >= 20 {
		st.boardTempC = int(data[17]) - 100
		st.batteryCurrentMA = int(int16(uint16(le16(18))))
		st.hasTemp = true
	}
	if len(data) >= 31 {
		st.soc = int(data[22])
		st.hasSoc = true
	}
	if len(data) >= 52 {
		st.gsX = int32(le32(35))
		st.gsY = int32(le32(39))
		st.gsZ = int32(le32(43))
		st.gsReady = data[47] != 0
		st.fanSpeed = le32(48)
		st.hasGs = true
	}
	return st, true
}

func pmuApplyStatus(st pmuStatus) {
	if st.hasTemp {
		globalData.Store("BoardTemperature", st.boardTempC)
	}
	if st.hasGs {
		globalData.Store("GsX", int(st.gsX))
		globalData.Store("GsY", int(st.gsY))
		globalData.Store("GsZ", int(st.gsZ))
		globalData.Store("GsReady", st.gsReady)
		pmuLastFanRPM.Store(int64(st.fanSpeed))
		pmuHasFanReading.Store(true)
	}
}

// ---- mode selection -------------------------------------------------------

func pmuKernelDriverPresent() bool {
	if _, err := os.Stat(pmuCovSysKernelPMPath); err == nil {
		return true
	}
	if _, err := os.Stat(pmuCovDevPMCtlPath); err == nil {
		return true
	}
	return false
}

// pmuDetectSerialDevice finds the UART wired to the PMU by scanning the
// device-tree serial aliases for the node with a "photonicat-pm" child
// (e.g. photonicat2: serial10 → /dev/ttyS10; photonicat1 used ttyS4).
func pmuDetectSerialDevice() string {
	aliases, _ := filepath.Glob(pmuCovDTAliasGlob)
	for _, alias := range aliases {
		target, err := os.ReadFile(alias)
		if err != nil {
			continue
		}
		node := strings.TrimRight(strings.TrimSpace(string(target)), "\x00")
		compat, err := os.ReadFile(pmuCovDTBasePath + node +
			"/pcat-pm/compatible")
		if err != nil {
			continue
		}
		if !strings.Contains(string(compat), "photonicat-pm") {
			continue
		}
		n := strings.TrimPrefix(filepath.Base(alias), "serial")
		if _, err := strconv.Atoi(n); err != nil {
			continue
		}
		return "/dev/ttyS" + n
	}
	return ""
}

// pmuPortUsers lists PIDs (other than ours) holding the device open, by
// scanning /proc/*/fd. Needs root to see other users' processes — which is
// how the display service runs on the photonicat.
func pmuPortUsers(dev string) []string {
	var users []string
	self := strconv.Itoa(os.Getpid())
	fds, _ := filepath.Glob(pmuCovProcFdGlob)
	for _, fd := range fds {
		target, err := os.Readlink(fd)
		if err != nil || target != dev {
			continue
		}
		pid := strings.Split(strings.TrimPrefix(fd, pmuCovProcPrefix), "/")[0]
		if pid == self {
			continue
		}
		users = append(users, pid)
	}
	return users
}

// startPmuManager launches the PMU data source in the background. Safe to
// call on any machine: it exits quietly when no photonicat PMU is present.
func startPmuManager() {
	if pmuKernelDriverPresent() {
		log.Println("PMU: photonicat-pm kernel driver present, using sysfs data source")
		pmuSysfsActive.Store(true)
		go pmuSysfsLoop()
		return
	}
	dev := pmuDetectSerialDevice()
	if dev == "" {
		return // not a photonicat, or no PMU UART described in the device tree
	}
	go pmuUartManager(dev)
}

// ---- sysfs mode -----------------------------------------------------------

// pmuFindTempMbHwmon resolves the hwmon node registered by the kernel driver
// (name prefix pcat_pm_hwmon_temp_mb), the same scan pmu-manager.c performs.
func pmuFindTempMbHwmon() string {
	hwmons, _ := filepath.Glob(pmuCovHwmonGlob)
	for _, h := range hwmons {
		name, err := os.ReadFile(filepath.Join(h, "name"))
		if err != nil {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(string(name)), "pcat_pm_hwmon_temp_mb") {
			return filepath.Join(h, "temp1_input")
		}
	}
	return ""
}

func readSysfsInt(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func pmuSysfsLoop() {
	tempPath := pmuFindTempMbHwmon()
	for {
		if tempPath == "" {
			tempPath = pmuFindTempMbHwmon()
		}
		if tempPath != "" {
			if milli, err := readSysfsInt(tempPath); err == nil {
				globalData.Store("BoardTemperature", milli/1000)
			}
		}
		if x, err := readSysfsInt("/sys/kernel/photonicat-pm/gs_x"); err == nil {
			globalData.Store("GsX", x)
		}
		if y, err := readSysfsInt("/sys/kernel/photonicat-pm/gs_y"); err == nil {
			globalData.Store("GsY", y)
		}
		if z, err := readSysfsInt("/sys/kernel/photonicat-pm/gs_z"); err == nil {
			globalData.Store("GsZ", z)
		}
		time.Sleep(pmuSysfsPollInterval)
	}
}

// ---- direct UART mode -----------------------------------------------------
//
// pmuOpenSerial and pmuBaudConstants live in pcatPmuSerial_linux.go — the
// termios ioctls they need are Linux-only.

// pmuUartManager waits until the PMU UART is provably idle, then claims it
// and runs the protocol loop, reconnecting on errors.
func pmuUartManager(dev string) {
	log.Printf("PMU: no kernel driver; will watch %s and use it directly once free", dev)
	time.Sleep(pmuUartSettleDelay)

	for {
		if users := pmuPortUsers(dev); len(users) > 0 {
			log.Printf("PMU: %s in use by PID(s) %s, backing off", dev,
				strings.Join(users, ","))
			time.Sleep(pmuUartRetryDelay)
			continue
		}
		// The port looks free — wait a little longer and confirm nobody
		// grabbed it in between before claiming it.
		time.Sleep(pmuUartRecheckDelay)
		if users := pmuPortUsers(dev); len(users) > 0 {
			time.Sleep(pmuUartRetryDelay)
			continue
		}

		port, err := pmuOpenSerial(dev, pmuDefaultBaud)
		if err != nil {
			log.Printf("PMU: cannot claim %s: %v", dev, err)
			time.Sleep(pmuUartRetryDelay)
			continue
		}

		log.Printf("PMU: claimed %s, speaking PMU protocol directly", dev)
		pmuUart.mu.Lock()
		pmuUart.port = port
		pmuUart.mu.Unlock()
		pmuUartActive.Store(true)

		stopHeartbeat := make(chan struct{})
		go pmuHeartbeatLoop(stopHeartbeat)
		pmuUartReadLoop(port) // blocks until read error
		close(stopHeartbeat)

		pmuUartActive.Store(false)
		pmuUart.mu.Lock()
		pmuUart.port = nil
		pmuUart.mu.Unlock()
		port.Close()
		log.Printf("PMU: lost %s, will retry", dev)
		time.Sleep(pmuUartRetryDelay)
	}
}

// pmuUartSend serializes and writes one frame.
func pmuUartSend(command uint16, extra []byte, needAck bool) error {
	pmuUart.mu.Lock()
	defer pmuUart.mu.Unlock()
	if pmuUart.port == nil {
		return fmt.Errorf("PMU UART not connected")
	}
	frame := pmuBuildFrame(pmuUart.frameNum, command, extra, needAck)
	pmuUart.frameNum++
	_, err := pmuUart.port.Write(frame)
	return err
}

// pmuHeartbeatLoop mirrors the kernel driver's 1 s heartbeat. We deliberately
// never send WATCHDOG_TIMEOUT_SET: enabling the MCU watchdog from a userspace
// process that might die would let the MCU cut power to a live system.
func pmuHeartbeatLoop(stop chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := pmuUartSend(pmuCmdHeartbeat, nil, false); err != nil {
				return
			}
		case <-stop:
			return
		}
	}
}

func pmuUartReadLoop(port *os.File) {
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 1024)
	for {
		n, err := port.Read(chunk)
		if err != nil {
			return
		}
		if n == 0 {
			continue // VTIME timeout, keep listening
		}
		buf = append(buf, chunk[:n]...)
		frames, consumed := pmuParseFrames(buf)
		if consumed > 0 {
			buf = append(buf[:0], buf[consumed:]...)
		}
		if len(buf) > 4096 { // runaway garbage, resync
			buf = buf[:0]
		}
		for _, f := range frames {
			pmuHandleFrame(f)
		}
	}
}

func pmuHandleFrame(f pmuFrame) {
	// Same destination filter as the kernel driver.
	if f.dst != 0x01 && f.dst != 0x80 && f.dst != 0xFF {
		return
	}
	needAck := f.needAck
	switch f.command {
	case pmuCmdStatusReport:
		if st, ok := pmuParseStatusReport(f.extra); ok {
			pmuApplyStatus(st)
		}
	case pmuCmdHostRequestShutdownAck:
		select {
		case pmuPoweroffAck <- struct{}{}:
		default:
		}
		needAck = false
	case pmuCmdPmuRequestShutdown:
		// Power-button shutdown request from the MCU: ack it and shut the
		// system down cleanly (port of pcat_pmu_poweroff_request()).
		log.Println("PMU requested shutdown (power button), powering off system")
		if needAck {
			pmuUartSend(f.command+1, nil, false)
			needAck = false
		}
		go exec.Command("poweroff").Run()
	}
	if needAck {
		pmuUartSend(f.command+1, nil, false)
	}
}

// ---- power off ------------------------------------------------------------

// pmuPowerOff shuts the machine down. In direct UART mode it first tells the
// PMU (HOST_REQUEST_SHUTDOWN, retried up to 3× like the C code) so the MCU
// cuts power once the system halts; with the kernel driver present the
// sys-off handler does that handshake itself.
func pmuPowerOff() error {
	if pmuUartActive.Load() {
		acked := false
		for try := 0; try < 3 && !acked; try++ {
			select {
			case <-pmuPoweroffAck: // drain stale ack
			default:
			}
			if err := pmuUartSend(pmuCmdHostRequestShutdown, nil, true); err != nil {
				return err
			}
			select {
			case <-pmuPoweroffAck:
				acked = true
			case <-time.After(1 * time.Second):
			}
		}
		if acked {
			log.Println("PMU acknowledged shutdown request")
		} else {
			log.Println("PMU shutdown request not acknowledged, powering off anyway")
		}
	}
	return exec.Command("poweroff").Run()
}
