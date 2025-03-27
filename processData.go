package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"time"
	"os"
	"bufio"
	"strings"
	"strconv"
	"syscall"
	"github.com/go-ping/ping"
)




func collectTopBarData() {
	if soc, err := getBatterySoc(); err != nil {
		fmt.Printf("Could not get battery soc: %v\n", err)
		globalData["BatterySoc"] = -9999
	} else {
		globalData["BatterySoc"] = soc
	}

	if charging, err := getBatteryCharging(); err != nil {
		fmt.Printf("Could not get battery charging: %v\n", err)
		globalData["BatteryCharging"] = false
	} else {
		globalData["BatteryCharging"] = charging
	}
}


// collectData gathers several pieces of system and network information and stores them in globalData.
func collectData(cfg Config) {
	// Initialize the global hashtable.


	// Ping Site0 using ICMP.
	if ping0, err := pingICMP(cfg.Site0); err != nil {
		fmt.Printf("ICMP ping to %s failed: %v\n", cfg.Site0, err)
		globalData["PingSite0"] = -1 // using -1 to indicate an error
	} else {
		globalData["PingSite0"] = ping0
	}

	// Ping Site1 using ICMP.
	if ping1, err := pingICMP(cfg.Site1); err != nil {
		fmt.Printf("ICMP ping to %s failed: %v\n", cfg.Site1, err)
		globalData["PingSite1"] = -1
	} else {
		globalData["PingSite1"] = ping1
	}

	// Get country based on public IP geolocation.
	if country, err := getCountry(); err != nil {
		fmt.Printf("Could not get country: %v\n", err)
		globalData["Country"] = "Unknown"
	} else {
		globalData["Country"] = country
	}

	// Get local IP address.
	if localIP, err := getLocalIP(); err != nil {
		fmt.Printf("Could not get local IP: %v\n", err)
		globalData["LocalIP"] = "0.0.0.0"
	} else {
		globalData["LocalIP"] = localIP
	}

	// Get public IP address.
	if publicIP, err := getPublicIP(); err != nil {
		fmt.Printf("Could not get public IP: %v\n", err)
		globalData["PublicIP"] = "0.0.0.0"
	} else {
		globalData["PublicIP"] = publicIP
	}

	// Get IPv6 public IP.
	if ipv6, err := getIPv6Public(); err != nil {
		fmt.Printf("Could not get IPv6 public IP: %v\n", err)
		globalData["PublicIPv6"] = "0.0.0.0"
	} else {
		globalData["PublicIPv6"] = ipv6
	}

	// Get CPU usage and temperature.
	if cpuData, err := getCpuUsage(); err != nil {
		fmt.Printf("Could not get CPU usage: %v\n", err)
		globalData["CPUData"] = nil
	} else {
		globalData["CPUData"] = cpuData
	}

	// Get memory usage.
	if memData, err := getMemoryUsage(); err != nil {
		fmt.Printf("Could not get memory usage: %v\n", err)
		globalData["MemoryData"] = nil
	} else {
		globalData["MemoryData"] = memData
	}

	// Get disk usage.
	if diskData, err := getDiskUsage(); err != nil {
		fmt.Printf("Could not get disk usage: %v\n", err)
		globalData["DiskData"] = nil
	} else {
		globalData["DiskData"] = diskData
	}

	// Get network usage (tx, rx data).
	if netData, err := getCurrNetworkSpeedMbps(); err != nil {
		fmt.Printf("Could not get network usage: %v\n", err)
		globalData["NetworkData"] = nil
	} else {
		globalData["NetworkData"] = netData
	}

	// Get DHCP clients if OpenWRT.
	if dhcpClients, err := getDHCPClients(); err != nil {
		fmt.Printf("Could not get DHCP clients: %v\n", err)
		globalData["DHCPClients"] = nil
	} else {
		globalData["DHCPClients"] = dhcpClients
	}

	// Get WiFi clients if OpenWRT.
	if wifiClients, err := getWifiClients(); err != nil {
		fmt.Printf("Could not get WiFi clients: %v\n", err)
		globalData["WifiClients"] = nil
	} else {
		globalData["WifiClients"] = wifiClients
	}

	voltage, err := getBatteryVoltage(); 
	if err != nil {
		fmt.Printf("Could not get battery voltage: %v\n", err)
		globalData["BatteryVoltage"] = "N/A"
	} else {
		voltage_2digit := fmt.Sprintf("%0.2f", voltage/1000/1000)
		globalData["BatteryVoltage"] = voltage_2digit
	}

	current, err := getBatteryCurrent();
	if err != nil {
		fmt.Printf("Could not get battery current: %v\n", err)
		globalData["BatteryCurrent"] = -9999
	} else {
		current_2digit := fmt.Sprintf("%0.2f", current/1000/1000)
		globalData["BatteryCurrent"] = current_2digit
	}	

	wattage := float64(voltage) * float64(current) / 1000 / 1000 / 1000 / 1000
	globalData["BatteryWattage"] = fmt.Sprintf("%0.1f", wattage)

	log.Println("Collected global data:")
	//log.Println(globalData)
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

// getBatterySoc returns the battery soc from /sys/class/power_supply/battery/capacity
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

// getBatteryCharging returns the battery charging status from /sys/class/power_supply/battery/charging
func getBatteryCharging() (bool, error) {
	file, err := os.Open("/sys/class/power_supply/battery/status")
	if err != nil {
		return false, err
	}
	defer file.Close()
	// if file content is "charging" return true, else false
	content, err := ioutil.ReadAll(file)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(content)) == "Charging", nil
}

func getBatteryVoltage() (float64, error) {
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

func getBatteryCurrent() (float64, error) {
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

// getLocalIP returns the first non-loopback IPv4 address found on the system.
func getLocalIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				return ip4.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no local IP found")
}

// getPublicIP makes an HTTP request to a public API to fetch the external IPv4 address.
func getPublicIP() (string, error) {
	resp, err := http.Get("https://api.ipify.org?format=text")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	ip, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(ip), nil
}


// getIPv6Public fetches the public IPv6 address.
func getIPv6Public() (string, error) {
	resp, err := http.Get("https://api6.ipify.org?format=text")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	ip, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(ip), nil
}

// getCpuUsage returns dummy CPU usage and temperature data.
func getCpuUsage() (map[string]interface{}, error) {
	data := map[string]interface{}{
		"Usage": 20.5, // CPU usage in percentage
		"Temp":  45.0, // CPU temperature in Celsius
	}

	return data, nil
}

// getMemoryUsage returns memory usage data from /proc/meminfo in MB.
func getMemoryUsage() (map[string]interface{}, error) {
	// Open /proc/meminfo
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, fmt.Errorf("failed to open /proc/meminfo: %v", err)
	}
	defer file.Close()

	// Initialize result map
	data := make(map[string]interface{})

	// Scan the file line by line
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		// Extract key and value
		key := strings.TrimSuffix(fields[0], ":")
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue // Skip if value isn't a number
		}

		// Convert from kB (default unit in /proc/meminfo) to MB
		valueMB := value / 1024

		// Store relevant fields
		switch key {
		case "MemTotal":
			data["Total"] = valueMB
		case "MemFree":
			data["Free"] = valueMB
		case "MemAvailable":
			data["Available"] = valueMB
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading /proc/meminfo: %v", err)
	}

	// Calculate Used memory (Total - Available)
	if total, ok := data["Total"].(uint64); ok {
		if available, ok := data["Available"].(uint64); ok {
			data["Used"] = total - available
		}
	}

	return data, nil
}

// getDiskUsage returns dummy disk usage data.
// getDiskUsage returns disk usage stats (total and free space in MB) for the current partition.
func getDiskUsage() (map[string]interface{}, error) {
	// Stat the root directory ("/") to get the current partition's stats
	var stat syscall.Statfs_t
	err := syscall.Statfs("/", &stat)
	if err != nil {
		return nil, fmt.Errorf("failed to stat filesystem: %v", err)
	}

	// Calculate total and free space
	// Bsize is block size in bytes, Blocks is total blocks, Bfree is free blocks
	totalMB := (uint64(stat.Bsize) * stat.Blocks) / (1024 * 1024) // Bytes to MB
	freeMB := (uint64(stat.Bsize) * stat.Bfree) / (1024 * 1024)   // Bytes to MB

	// Prepare data
	data := map[string]interface{}{
		"Total": totalMB,
		"Free":  freeMB,
		"Used":  totalMB - freeMB, // Optional: calculate used space
	}

	return data, nil
}

// getCurrNetworkSpeedMbps returns current network speed in Mbps for all interfaces.
func getCurrNetworkSpeedMbps() (map[string]interface{}, error) {
	// Read initial stats
	startStats, err := readNetworkStats()
	if err != nil {
		return nil, err
	}

	// Wait 1 second to measure difference
	time.Sleep(1 * time.Second)

	// Read stats again
	endStats, err := readNetworkStats()
	if err != nil {
		return nil, err
	}

	// Calculate speed in Mbps
	data := make(map[string]interface{})
	for iface, end := range endStats {
		if start, ok := startStats[iface]; ok {
			// Bytes difference over 1 second
			rxBytes := end.rxBytes - start.rxBytes
			txBytes := end.txBytes - start.txBytes

			// Convert bytes/sec to Mbps (1 byte = 8 bits, 1 Mb = 1e6 bits)
			rxMbps := float64(rxBytes) * 8 / 1e6
			txMbps := float64(txBytes) * 8 / 1e6

			data[iface] = map[string]float64{
				"download": rxMbps, // Receive speed
				"upload":   txMbps, // Transmit speed
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

	// Skip header lines
	for i := 0; i < 2 && scanner.Scan(); i++ {
	}

	// Parse each interface line
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

