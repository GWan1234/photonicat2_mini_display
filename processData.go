package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-ping/ping"
)

// NetworkSpeed represents upload/download in bytes per second
type NetworkSpeed struct {
	UploadMbps   float64
	DownloadMbps float64
}

func collectTopBarData() {
	if soc, err := getBatterySoc(); err != nil {
		fmt.Printf("Could not get battery soc: %v\n", err)
		globalData.Store("BatterySoc", -9999)
	} else {
		globalData.Store("BatterySoc", soc)
	}

	if charging, err := getBatteryCharging(); err != nil {
		fmt.Printf("Could not get battery charging: %v\n", err)
		globalData.Store("BatteryCharging", false)
	} else {
		globalData.Store("BatteryCharging", charging)
	}
}

// formatSpeed formats speed into value and units as Mbps
func formatSpeed(mbps float64) (string, string) {
	if mbps >= 1.0 {
		// For speeds ≥1 Mbps, use 3 significant digits
		return fmt.Sprintf("%.3g", mbps), "Mb/s"
	}
	// For speeds <1 Mbps, keep up to 3 digits after decimal point
	return fmt.Sprintf("%.2f", mbps), "Mb/s"
}

func getWANInterface() (string, error) {
	cmd := exec.Command("ip", "route", "show", "default")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}

	fields := strings.Fields(out.String())
	for i, field := range fields {
		if field == "dev" && (i+1) < len(fields) {
			return fields[i+1], nil
		}
	}

	return "", fmt.Errorf("WAN interface not found")
}

func collectWANNetworkSpeed() {
	wanInterface, err := getWANInterface()
	if err != nil {
		globalData.Store("WanUP", "N/A")
		globalData.Store("WanDOWN", "N/A")
		return
	}
	netData, err := getNetworkSpeed(wanInterface)
	if err != nil {
		globalData.Store("WanUP", "0")
		globalData.Store("WanDOWN", "0")
		return
	}
	wanUPVal, wanUPUnit := formatSpeed(netData.UploadMbps)
	wanDOWNVal, wanDOWNUnit := formatSpeed(netData.DownloadMbps)
	globalData.Store("WanUP", wanUPVal)
	globalData.Store("WanDOWN", wanDOWNVal)
	globalData.Store("WanUP_Unit", wanUPUnit)
	globalData.Store("WanDOWN_Unit", wanDOWNUnit)
}

// collectData gathers several pieces of system and network information and stores them in globalData.
func collectData(cfg Config) {
	// Battery voltage.
	voltageUV, err := getBatteryVoltageUV()
	if err != nil {
		fmt.Printf("Could not get battery voltage: %v\n", err)
		globalData.Store("BatteryVoltage", "N/A")
	} else {
		voltage_2digit := fmt.Sprintf("%0.2f", voltageUV/1000/1000)
		globalData.Store("BatteryVoltage", voltage_2digit)
	}

	// Battery current.
	currentUA, err := getBatteryCurrentUA()
	if err != nil {
		fmt.Printf("Could not get battery current: %v\n", err)
		globalData.Store("BatteryCurrent", -9999)
	} else {
		current_2digit := fmt.Sprintf("%0.2f", currentUA/1000/1000)
		globalData.Store("BatteryCurrent", current_2digit)
	}

	// Battery wattage.
	wattage := float64(voltageUV) * float64(currentUA) / 1000 / 1000 / 1000 / 1000
	globalData.Store("BatteryWattage", fmt.Sprintf("%0.1f", wattage))

	// DC voltage.
	dcVoltageUV, err := getDCVoltageUV()
	if err != nil {
		fmt.Printf("Could not get DC voltage: %v\n", err)
		globalData.Store("DCVoltage", -9999)
	} else {
		globalData.Store("DCVoltage", fmt.Sprintf("%0.1f", dcVoltageUV/1000/1000))
	}

	// CPU temperature.
	if cpuTemp, err := getCpuTemp(); err != nil {
		fmt.Printf("Could not get CPU temperature: %v\n", err)
		globalData.Store("CpuTemp", -9999)
	} else {
		cpuTemp_1digit := fmt.Sprintf("%0.1f", cpuTemp/1000)
		globalData.Store("CpuTemp", cpuTemp_1digit)
	}

	// CPU usage.
	cpuUsage, err := getCPUUsage()
	if err != nil {
		fmt.Printf("Could not get CPU usage: %v\n", err)
		globalData.Store("CpuUsage", 0)
	} else {
		cpuUsageInt := int(cpuUsage)
		globalData.Store("CpuUsage", cpuUsageInt)
	}

	// Memory usage.
	if memUsed, memTotal, err := getMemUsedAndTotalGB(); err != nil {
		fmt.Printf("Could not get memory usage: %v\n", err)
		globalData.Store("MemUsage", nil)
	} else {
		memUsed_1digit := fmt.Sprintf("%0.1f", memUsed)
		memTotal_ceilInt := int(math.Ceil(memTotal))
		memString := fmt.Sprintf("%s/%d", memUsed_1digit, memTotal_ceilInt)
		globalData.Store("MemUsage", memString)
	}

	// Disk usage.
	if diskData, err := getDiskUsage(); err != nil {
		fmt.Printf("Could not get disk usage: %v\n", err)
		globalData.Store("DiskData", nil)
	} else {
		globalData.Store("DiskData", diskData)
	}

	// Local IP address.
	if localIP, err := getLocalIPv4(); err != nil {
		fmt.Printf("Could not get local IP: %v\n", err)
		globalData.Store("LAN_IP", "N/A")
	} else {
		globalData.Store("LAN_IP", localIP)
	}

	// Public IP address.
	if publicIP, err := getPublicIPv4(); err != nil {
		fmt.Printf("Could not get public IP: %v\n", err)
		globalData.Store("WAN_IP", "N/A")
	} else {
		globalData.Store("WAN_IP", publicIP)
	}

	// SSID.
	if ssid, err := getSSID(); err != nil {
		fmt.Printf("Could not get SSID: %v\n", err)
		globalData.Store("SSID", "N/A")
	} else {
		globalData.Store("SSID", ssid)
	}

	// DHCP clients (OpenWrt).
	if dhcpClients, err := getDHCPClients(); err != nil {
		fmt.Printf("Could not get DHCP clients: %v\n", err)
		globalData.Store("DHCPClients", nil)
	} else {
		globalData.Store("DHCPClients", dhcpClients)
	}

	// WiFi clients (OpenWrt).
	if wifiClients, err := getWifiClients(); err != nil {
		fmt.Printf("Could not get WiFi clients: %v\n", err)
		globalData.Store("WifiClients", nil)
	} else {
		globalData.Store("WifiClients", wifiClients)
	}

	// Ping Site0 using ICMP.
	if ping0, err := pingICMP(cfg.Site0); err != nil {
		fmt.Printf("ICMP ping to %s failed: %v\n", cfg.Site0, err)
		globalData.Store("Ping0", -1) // using -1 to indicate an error
	} else {
		globalData.Store("Ping0", ping0)
	}

	// Ping Site1 using ICMP.
	if ping1, err := pingICMP(cfg.Site1); err != nil {
		fmt.Printf("ICMP ping to %s failed: %v\n", cfg.Site1, err)
		globalData.Store("Ping1", -1)
	} else {
		globalData.Store("Ping1", ping1)
	}

	// Country based on public IP geolocation.
	if country, err := getCountry(); err != nil {
		fmt.Printf("Could not get country: %v\n", err)
		globalData.Store("Country", "Unknown")
	} else {
		globalData.Store("Country", country)
	}

	// IPv6 public IP.
	if ipv6, err := getIPv6Public(); err != nil {
		fmt.Printf("Could not get IPv6 public IP: %v\n", err)
		globalData.Store("PublicIPv6", "0.0.0.0")
	} else {
		globalData.Store("PublicIPv6", ipv6)
	}

	log.Println("Collected global data:")
	// You can range over globalData if needed.
}

// getDCVoltageUV reads DC voltage from the system.
func getDCVoltageUV() (float64, error) {
	file, err := os.Open("/sys/class/power_supply/charger/voltage_now")
	if err != nil {
		return 0, err
	}
	defer file.Close()
	content, err := ioutil.ReadAll(file)
	rawUV, err := strconv.ParseFloat(strings.TrimSpace(string(content)), 64)
	if err != nil {
		return 0.0, err
	}
	if rawUV < 1*1000*1000 {
		return 0.0, nil
	}
	return rawUV, nil
}

// getInterfaceBytes reads rx and tx bytes for a given interface.
func getInterfaceBytes(iface string) (rxBytes, txBytes uint64, err error) {
	basePath := "/sys/class/net/" + iface + "/statistics/"
	rxPath := basePath + "rx_bytes"
	txPath := basePath + "tx_bytes"

	readBytes := func(path string) (uint64, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		val, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		return val, err
	}

	rxBytes, err = readBytes(rxPath)
	if err != nil {
		return
	}
	txBytes, err = readBytes(txPath)
	return
}

// getSSID returns connected SSID on Debian or broadcasting SSID on OpenWrt.
func getSSID() (string, error) {
	// OpenWrt detection
	if _, err := os.Stat("/etc/openwrt_release"); err == nil {
		// OpenWrt: Use uci command
		out, err := exec.Command("uci", "get", "wireless.@wifi-iface[0].ssid").Output()
		if err != nil {
			return "", fmt.Errorf("failed to get OpenWrt SSID: %v", err)
		}
		return strings.TrimSpace(string(out)), nil
	}

	// Debian/Ubuntu: Try iwgetid first
	if out, err := exec.Command("iwgetid", "-r").Output(); err == nil {
		ssid := strings.TrimSpace(string(out))
		if ssid != "" {
			return ssid, nil
		}
	}

	// Fallback 1: iwconfig
	if out, err := exec.Command("iwconfig").Output(); err == nil {
		re := regexp.MustCompile(`ESSID:"(.*?)"`)
		matches := re.FindSubmatch(out)
		if len(matches) >= 2 {
			ssid := string(matches[1])
			if ssid != "" && ssid != "off/any" {
				return ssid, nil
			}
		}
	}

	// Fallback 2: nmcli (NetworkManager)
	if out, err := exec.Command("nmcli", "-t", "-f", "active,ssid", "dev", "wifi").Output(); err == nil {
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			fields := strings.Split(line, ":")
			if len(fields) == 2 && fields[0] == "yes" && fields[1] != "" {
				return fields[1], nil
			}
		}
	}

	return "", fmt.Errorf("SSID could not be determined")
}

// getNetworkSpeed calculates instant network speed for the specified interface.
func getNetworkSpeed(iface string) (NetworkSpeed, error) {
	rx1, tx1, err := getInterfaceBytes(iface)
	if err != nil {
		return NetworkSpeed{}, err
	}

	// Sampling interval
	time.Sleep(1 * time.Second)

	rx2, tx2, err := getInterfaceBytes(iface)
	if err != nil {
		return NetworkSpeed{}, err
	}

	downloadMbps := float64(rx2-rx1) / 1024 / 128
	uploadMbps := float64(tx2-tx1) / 1024 / 128

	return NetworkSpeed{
		UploadMbps:   uploadMbps,
		DownloadMbps: downloadMbps,
	}, nil
}

// CPUStats represents a CPU usage snapshot.
type CPUStats struct {
	User, Nice, System, Idle, Iowait, Irq, Softirq, Steal uint64
}

func readCPUStats() ([]CPUStats, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, err
	}

	var stats []CPUStats
	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		if strings.HasPrefix(line, "cpu") && len(line) > 3 && line[3] >= '0' && line[3] <= '9' {
			fields := strings.Fields(line)
			if len(fields) < 8 {
				continue
			}
			var stat CPUStats
			stat.User, _ = strconv.ParseUint(fields[1], 10, 64)
			stat.Nice, _ = strconv.ParseUint(fields[2], 10, 64)
			stat.System, _ = strconv.ParseUint(fields[3], 10, 64)
			stat.Idle, _ = strconv.ParseUint(fields[4], 10, 64)
			stat.Iowait, _ = strconv.ParseUint(fields[5], 10, 64)
			stat.Irq, _ = strconv.ParseUint(fields[6], 10, 64)
			stat.Softirq, _ = strconv.ParseUint(fields[7], 10, 64)
			if len(fields) > 8 {
				stat.Steal, _ = strconv.ParseUint(fields[8], 10, 64)
			}
			stats = append(stats, stat)
		}
	}

	return stats, nil
}

func getCPUUsage() (float64, error) {
	cpus, err := getCpuUsages()
	if err != nil {
		return 0, err
	}
	total := 0.0
	for _, cpu := range cpus {
		total += cpu
	}
	return total / float64(len(cpus)), nil
}

func getCpuUsages() ([]float64, error) {
	stats1, err := readCPUStats()
	if err != nil {
		return nil, err
	}

	time.Sleep(500 * time.Millisecond)

	stats2, err := readCPUStats()
	if err != nil {
		return nil, err
	}

	var usages []float64
	for i := 0; i < len(stats1) && i < len(stats2); i++ {
		idle1 := stats1[i].Idle + stats1[i].Iowait
		idle2 := stats2[i].Idle + stats2[i].Iowait

		nonIdle1 := stats1[i].User + stats1[i].Nice + stats1[i].System +
			stats1[i].Irq + stats1[i].Softirq + stats1[i].Steal

		nonIdle2 := stats2[i].User + stats2[i].Nice + stats2[i].System +
			stats2[i].Irq + stats2[i].Softirq + stats2[i].Steal

		total1 := idle1 + nonIdle1
		total2 := idle2 + nonIdle2

		totalDelta := float64(total2 - total1)
		idleDelta := float64(idle2 - idle1)

		cpuPercentage := (totalDelta - idleDelta) / totalDelta * 100
		usages = append(usages, cpuPercentage)
	}

	return usages, nil
}

// pingICMP uses github.com/go-ping/ping to perform an ICMP ping.
// Note: raw ICMP ping usually requires root privileges.
func pingICMP(host string) (int64, error) {
	pinger, err := ping.NewPinger(host)
	if err != nil {
		return 0, err
	}
	// Set privileged mode if possible; otherwise, false will use UDP.
	pinger.SetPrivileged(true)
	pinger.Count = 1
	pinger.Timeout = 2 * time.Second

	// Run the ping (blocking).
	err = pinger.Run()
	if err != nil {
		return 0, err
	}
	stats := pinger.Statistics()
	// Return average round-trip time in milliseconds.
	return int64(stats.AvgRtt / time.Millisecond), nil
}

// getBatterySoc returns the battery soc from /sys/class/power_supply/battery/capacity.
func getBatterySoc() (int, error) {
	file, err := os.Open("/sys/class/power_supply/battery/capacity")
	if err != nil {
		return -1, err
	}
	defer file.Close()
	content, err := ioutil.ReadAll(file)
	if err != nil {
		return -1, err
	}
	socInt, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		return -1, err
	}
	return socInt, nil
}

// getBatteryCharging returns the battery charging status from /sys/class/power_supply/battery/status.
func getBatteryCharging() (bool, error) {
	var determineChargingByCurrent bool = true
	if determineChargingByCurrent {
		current, err := getBatteryCurrentUA()
		if err != nil {
			return false, err
		}
		return current > 0, nil
	}else{
		file, err := os.Open("/sys/class/power_supply/battery/status")
		if err != nil {
			return false, err
		}
		defer file.Close()
		content, err := ioutil.ReadAll(file)
		if err != nil {
			return false, err
		}
		return strings.TrimSpace(string(content)) == "Charging", nil
	}
}

func getBatteryVoltageUV() (float64, error) {
	file, err := os.Open("/sys/class/power_supply/battery/voltage_now")
	if err != nil {
		return 0, err
	}
	defer file.Close()
	content, err := ioutil.ReadAll(file)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(content)), 64)
}

func getBatteryCurrentUA() (float64, error) {
	file, err := os.Open("/sys/class/power_supply/battery/current_now")
	if err != nil {
		return 0, err
	}
	defer file.Close()
	content, err := ioutil.ReadAll(file)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(content)), 64)
}

func getCountry() (string, error) {
	resp, err := http.Get("http://ip-api.com/json/")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Country string `json:"country"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Country, nil
}

// getLocalIPv4 returns eth0 IP on OpenWrt or WAN IP (default route) on Debian.
func getLocalIPv4() (string, error) {
	// Check if running on OpenWrt by existence of "/etc/openwrt_release"
	if _, err := os.Stat("/etc/openwrt_release"); err == nil {
		// OpenWrt: return eth0 IP explicitly
		iface, err := net.InterfaceByName("eth0")
		if err != nil {
			return "", fmt.Errorf("eth0 not found: %v", err)
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return "", err
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return ipnet.IP.String(), nil
			}
		}
		return "", fmt.Errorf("eth0 has no IPv4 address")
	}

	// Debian/Ubuntu: Find WAN interface from default route.
	cmd := exec.Command("ip", "route", "show", "default")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	fields := strings.Fields(out.String())
	var ifaceName string
	for i, field := range fields {
		if field == "dev" && (i+1) < len(fields) {
			ifaceName = fields[i+1]
			break
		}
	}
	if ifaceName == "" {
		return "", fmt.Errorf("WAN interface not found")
	}
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return "", err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			return ipnet.IP.String(), nil
		}
	}
	return "", fmt.Errorf("WAN interface (%s) has no IPv4 address", ifaceName)
}

// getPublicIPv4 makes an HTTP request to a public API to fetch the external IPv4 address.
func getPublicIPv4() (string, error) {
	resp, err := http.Get("https://4.photonicat.com/ip.php")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Trim any whitespace or newlines.
	ipStr := strings.TrimSpace(string(ip))

	// Optional: Basic validation that it looks like an IPv4 address.
	if net.ParseIP(ipStr) == nil || net.ParseIP(ipStr).To4() == nil {
		return "", fmt.Errorf("invalid IPv4 address received: %s", ipStr)
	}

	return ipStr, nil
}

// getIPv6Public fetches the public IPv6 address.
func getIPv6Public() (string, error) {
	resp, err := http.Get("https://6.photonicat.com/ip.php")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Trim any whitespace or newlines.
	ipStr := strings.TrimSpace(string(ip))

	// Optional: Basic validation that it looks like an IPv6 address.
	if net.ParseIP(ipStr) == nil || net.ParseIP(ipStr).To4() != nil {
		return "", fmt.Errorf("invalid IPv6 address received: %s", ipStr)
	}

	return ipStr, nil
}

// getCpuTemp returns CPU temperature from /sys/class/thermal/thermal_zone0/temp.
func getCpuTemp() (float64, error) {
	file, err := os.Open("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0, err
	}
	defer file.Close()
	content, err := ioutil.ReadAll(file)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(content)), 64)
}

// getMemUsedAndTotalGB returns used and total memory in GB.
func getMemUsedAndTotalGB() (usedGB float64, totalGB float64, err error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}

	var memTotal, memAvailable float64

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key, value := fields[0], fields[1]
		switch key {
		case "MemTotal:":
			memTotal, err = strconv.ParseFloat(value, 64)
			if err != nil {
				return 0, 0, err
			}
		case "MemAvailable:":
			memAvailable, err = strconv.ParseFloat(value, 64)
			if err != nil {
				return 0, 0, err
			}
		}
		if memTotal > 0 && memAvailable > 0 {
			break
		}
	}

	if memTotal == 0 {
		return 0, 0, fmt.Errorf("failed to read MemTotal")
	}

	usedKB := memTotal - memAvailable
	usedGB = usedKB / 1024 / 1024
	totalGB = memTotal / 1024 / 1024

	return usedGB, totalGB, nil
}

// getDiskUsage returns disk usage stats (total and free space in MB) for the current partition.
func getDiskUsage() (map[string]interface{}, error) {
	var stat syscall.Statfs_t
	err := syscall.Statfs("/", &stat)
	if err != nil {
		return nil, fmt.Errorf("failed to stat filesystem: %v", err)
	}

	totalMB := (uint64(stat.Bsize) * stat.Blocks) / (1024 * 1024)
	freeMB := (uint64(stat.Bsize) * stat.Bfree) / (1024 * 1024)

	data := map[string]interface{}{
		"Total": totalMB,
		"Free":  freeMB,
		"Used":  totalMB - freeMB,
	}

	return data, nil
}

// getCurrNetworkSpeedMbps returns current network speed in Mbps for all interfaces.
func getCurrNetworkSpeedMbps() (map[string]interface{}, error) {
	startStats, err := readNetworkStats()
	if err != nil {
		return nil, err
	}

	time.Sleep(1 * time.Second)

	endStats, err := readNetworkStats()
	if err != nil {
		return nil, err
	}

	data := make(map[string]interface{})
	for iface, end := range endStats {
		if start, ok := startStats[iface]; ok {
			rxBytes := end.rxBytes - start.rxBytes
			txBytes := end.txBytes - start.txBytes

			rxMbps := float64(rxBytes) * 8 / 1e6
			txMbps := float64(txBytes) * 8 / 1e6

			data[iface] = map[string]float64{
				"download": rxMbps,
				"upload":   txMbps,
			}
		}
	}

	return data, nil
}

// networkStats holds RX and TX bytes for an interface.
type networkStats struct {
	rxBytes uint64
	txBytes uint64
}

// readNetworkStats reads current network stats from /proc/net/dev.
func readNetworkStats() (map[string]networkStats, error) {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil, fmt.Errorf("failed to open /proc/net/dev: %v", err)
	}
	defer file.Close()

	stats := make(map[string]networkStats)
	scanner := bufio.NewScanner(file)

	// Skip header lines.
	for i := 0; i < 2 && scanner.Scan(); i++ {
	}

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}

		iface := strings.TrimSuffix(fields[0], ":")
		rxBytes, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		txBytes, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			continue
		}

		stats[iface] = networkStats{
			rxBytes: rxBytes,
			txBytes: txBytes,
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading /proc/net/dev: %v", err)
	}

	return stats, nil
}

// getDHCPClients returns dummy DHCP clients for OpenWRT.
func getDHCPClients() ([]string, error) {
	clients := []string{"192.168.1.100", "192.168.1.101"}
	return clients, nil
}

// getWifiClients returns dummy WiFi client MAC addresses for OpenWRT.
func getWifiClients() ([]string, error) {
	clients := []string{"AA:BB:CC:DD:EE:FF", "11:22:33:44:55:66"}
	return clients, nil
}
