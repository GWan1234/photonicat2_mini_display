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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-ping/ping"
)

// WiFiInterface mirrors each element of "wifi_interfaces" in the JSON.
type WiFiInterface struct {
	Band       string `json:"band"`
	Device     string `json:"device"`
	DeviceType string `json:"device_type"`
	Enabled    bool   `json:"enabled"`
	Encryption string `json:"encryption"`
	Exist      bool   `json:"exist"`
	Hidden     string `json:"hidden"`
	Htmode     string `json:"htmode"`
	Password   string `json:"password"`
	SSID       string `json:"ssid"`
	Frequency  string `json:"frequency,omitempty"`
}

// DashboardInfo matches the top‐level keys in your sample JSON.
type DashboardInfo struct {
	BatteryCurrent      float64         `json:"battery_current"`
	BatteryWattage      float64         `json:"battery_wattage"`
	BoardTemperature    int             `json:"board_temperature"`
	Carrier             string          `json:"carrier"`
	ChargePercent       int             `json:"charge_percent"`
	ChargeVoltage       int             `json:"charge_voltage"`
	Connection          string          `json:"connection"`
	DHCPClientsCount    int             `json:"dhcp_clients_count"`
	UpSpeedBps          float64         `json:"up_speed"`
	DownSpeedBps        float64         `json:"down_speed"`
	FirmwareVersion     string          `json:"firmware_version"`
	Hostname            string          `json:"hostname"`
	ISPName             string          `json:"isp_name"`
	Kernel              string          `json:"kernel"`
	Model               string          `json:"model"`
	ModemModel          string          `json:"modem_model"`
	ModemSignalStrength int             `json:"modem_signal_strength"`
	OnCharging          bool            `json:"on_charging"`
	OpenWRTVersion      string          `json:"openwrt_version"`
	SdState             int             `json:"sd_state"`
	ServerLocation      string          `json:"server_location"`
	SimState            string          `json:"sim_state"`
	SimNumber           string          `json:"sim_number"`
	Uptime              string          `json:"uptime"`
	Voltage             int             `json:"voltage"`
	PublicIP            string          `json:"public_ip"`
	WiFiClientsCount    int             `json:"wifi_clients_count"`
	WiFiInterfaces      []WiFiInterface `json:"wifi_interfaces"`
}

type ModemBasicInfo struct {
	CellCarrierInfo     string         `json:"cell_carrier_info"`
	FirmwareVersion     string         `json:"firmware_version"`
	IMEINum             string         `json:"imei_num"`
	Messages            []interface{}  `json:"messages"`
	ModemCellID         string         `json:"modem_cell_id"`
	ModemCellInfo       string         `json:"modem_cell_info"`
	ModemCellSignals    string         `json:"modem_cell_signals"`
	ModemCPIN           string         `json:"modem_cpin"`
	ModemIspDetails     string         `json:"modem_isp_details"`
	ModemModel          string         `json:"modem_model"`
	ModemNetworkInfo    string         `json:"modem_network_info"`
	ModemRoamPref       string         `json:"modem_roam_pref"`
	ModemServingInfo    string         `json:"modem_serving_info"`
	ModemServingQuality string         `json:"modem_serving_quality"`
	ModemTemperature    map[string]int `json:"modem_temperature"`
	ModemUSBSpeed       string         `json:"modem_usb_speed"`
	ModemUSBNetMode     string         `json:"modem_usbnet_mode"`
	ModemValid          bool           `json:"modem_valid"`
	PolicyLTEBands      string         `json:"policy_lte_bands"`
	PolicyNR5GBands     string         `json:"policy_nr5g_bands"`
	SelectedLTEBands    string         `json:"selected_lte_bands"`
	SelectedNR5GBands   string         `json:"selected_nr5g_bands"`
	SimNumber           string         `json:"sim_number"`
	SimState            string         `json:"sim_state"`
	SMSCheckInterval    int            `json:"sms_check_interval"`
	SMSForward          bool           `json:"sms_forward"`
	SMSForwardTo        string         `json:"sms_forward_to"`
}

// NetworkStats matches the keys returned by /api/v1/data_stats.json?network_type=mobile
type NetworkStats struct {
	TodayUsed     float64 `json:"today_used"`
	WeekUsed      float64 `json:"week_used"`
	MonthUsed     float64 `json:"month_used"`
	LastMonthUsed float64 `json:"last_month_used"`
}

// NetworkSpeed represents upload/download in bytes per second
type NetworkSpeed struct {
	UploadMbps   float64
	DownloadMbps float64
}

func collectBatteryData() {
	var err error
	if battSOC, err = getBatterySoc(); err != nil {
		fmt.Printf("Could not get battery soc: %v\n", err)
		globalData.Store("BatterySoc", -1)
	} else {
		globalData.Store("BatterySoc", battSOC)
	}

	if battChargingStatus, err = getBatteryCharging(); err != nil {
		fmt.Printf("Could not get battery charging: %v\n", err)
		globalData.Store("BatteryCharging", false)
	} else {
		globalData.Store("BatteryCharging", battChargingStatus)
	}

	//if charging status change, we trigger lastActivity
	if battChargingStatus != lastChargingStatus {
		log.Println("Battery charging status changed to: ", battChargingStatus)
		if idleState == STATE_ACTIVE {
			lastActivity = time.Now().Add(-fadeInDur) //reset lastActivity for screen to stay on, - fadeInDur to send state to active
		} else {
			lastActivity = time.Now() //bring back screen with some fade in
		}
		lastChargingStatus = battChargingStatus

		log.Printf("idleTimeout: %v", idleTimeout)
	}
	if battChargingStatus {
		idleTimeout = time.Duration(cfg.ScreenDimmerTimeOnDCSeconds) * time.Second
	} else {
		idleTimeout = time.Duration(cfg.ScreenDimmerTimeOnBatterySeconds) * time.Second
	}
}

func getInfoFromPcatWeb() {
	dashbarodURL := "http://localhost:80/api/v1/dashboard.json"
	networkStatsURL := "http://localhost:80/api/v1/data_stats.json?network_type=mobile"
	basicURL := "http://localhost:80/api/v1/modem/basic.json"

	var info DashboardInfo

	// === 1) Fetch dashboard.json ===
	resp, err := http.Get(dashbarodURL)
	if err != nil {
		fmt.Println("Could not get dashboard info:", err)
	} else {
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Println("Failed to read dashboard response body:", err)
		} else {
			if err2 := json.Unmarshal(body, &info); err2 != nil {
				fmt.Println("Could not unmarshal dashboard info:", err2)
			} else {
				// Store each field into globalData under a sensible key.
				globalData.Store("BoardTemperature", info.BoardTemperature)
				globalData.Store("Carrier", info.Carrier)
				globalData.Store("GatewayDevice", info.Connection)
				globalData.Store("DHCPClientsCount", info.DHCPClientsCount)
				globalData.Store("FirmwareVersion", info.FirmwareVersion)
				globalData.Store("ISPName", info.ISPName)
				globalData.Store("Model", info.Model)
				globalData.Store("ModemModel", info.ModemModel)
				globalData.Store("ModemSignalStrength", info.ModemSignalStrength)
				if info.SdState == 0 {
					globalData.Store("SdState", "No")
				} else {
					globalData.Store("SdState", "Yes")
				}
				globalData.Store("ServerLocation", info.ServerLocation)
				globalData.Store("SimNumber", info.SimNumber)

				if info.SimState == "ready" {
					globalData.Store("SimState", "Yes")
				} else {
					globalData.Store("SimState", "No")
				}

				globalData.Store("WiFiClientsCount", info.WiFiClientsCount)
				globalData.Store("WiFiInterfaces", info.WiFiInterfaces)
				globalData.Store("PublicIP", info.PublicIP)
				globalData.Store("UpSpeedBps", info.UpSpeedBps)
				globalData.Store("DownSpeedBps", info.DownSpeedBps)
				theOS := ""
				raw := info.OpenWRTVersion // e.g. "R25.02.0 / r7465-d1ccd1687"
				parts := strings.SplitN(raw, "/", 2)
				if len(parts) == 2 {
					ver := strings.TrimSpace(parts[0])    // "R25.02.0"
					commit := strings.TrimSpace(parts[1]) // "r7465-d1ccd1687"

					// remove trailing ".0" from version
					ver = strings.TrimSuffix(ver, ".0") // "R25.02"

					// keep only up to the first dash in commit
					commit = strings.SplitN(commit, "-", 2)[0] // "r7465"

					theOS = fmt.Sprintf("%s / %s", ver, commit) // "R25.02 / r7465"
				} else {
					theOS = raw
				}
				globalData.Store("OSVersion", theOS)

				// Build a slice of SSIDs for convenience
				var ssids []string
				for _, iface := range info.WiFiInterfaces {
					ssids = append(ssids, iface.SSID)
				}
				globalData.Store("WiFiSSIDs", ssids)
			}
		}
	}

	// === 2) Fetch data_stats.json ===
	resp2, err := http.Get(networkStatsURL)
	if err != nil {
		fmt.Println("Could not get network stats:", err)
	} else {
		defer resp2.Body.Close()
		body2, err := io.ReadAll(resp2.Body)
		if err != nil {
			fmt.Println("Failed to read network stats body:", err)
		} else {
			var stats NetworkStats
			if err3 := json.Unmarshal(body2, &stats); err3 != nil {
				fmt.Println("Could not unmarshal network stats:", err3)
			} else {
				// Now store exactly the fields you want:
				strTodayUsed := fmt.Sprintf("%0.2f", stats.TodayUsed/1024/1024/1024)
				strWeekUsed := fmt.Sprintf("%0.2f", stats.WeekUsed/1024/1024/1024)
				strMonthUsed := fmt.Sprintf("%0.2f", stats.MonthUsed/1024/1024/1024)
				strLastMonthUsed := fmt.Sprintf("%0.2f", stats.LastMonthUsed/1024/1024/1024)

				globalData.Store("DailyDataUsage", strTodayUsed)
				globalData.Store("WeeklyDataUsage", strWeekUsed)
				globalData.Store("MonthlyDataUsage", strMonthUsed)
				globalData.Store("LastMonthUsage", strLastMonthUsed)
			}
		}
	}

	// 3) Modem basic
	if resp, err := http.Get(basicURL); err != nil {
		fmt.Println("Could not get modem basic info:", err)
	} else {
		defer resp.Body.Close()
		if body, err := io.ReadAll(resp.Body); err != nil {
			fmt.Println("Failed to read modem basic body:", err)
		} else {
			var mb ModemBasicInfo
			if err := json.Unmarshal(body, &mb); err != nil {
				fmt.Println("Could not unmarshal modem basic info:", err)
			} else {
				globalData.Store("CellCarrierInfo", mb.CellCarrierInfo)
				globalData.Store("ModemFirmwareVer", mb.FirmwareVersion)
				globalData.Store("IMEINum", mb.IMEINum)
				globalData.Store("ModemCellID", mb.ModemCellID)
				globalData.Store("ModemCellInfo", mb.ModemCellInfo)
				globalData.Store("ModemSignals", mb.ModemCellSignals)
				globalData.Store("ModemISPDetails", mb.ModemIspDetails)

				networkInfo := mb.ModemNetworkInfo
				if strings.Contains(networkInfo, "BAND ") {
					networkInfo = strings.ReplaceAll(networkInfo, "BAND ", "B.")
				}

				globalData.Store("ModemNetworkInfo", networkInfo)

				globalData.Store("ModemRoamPref", mb.ModemRoamPref)
				globalData.Store("ModemServingInfo", mb.ModemServingInfo)
				globalData.Store("ModemServingQual", mb.ModemServingQuality)
				globalData.Store("ModemUSBSpeed", mb.ModemUSBSpeed)
				globalData.Store("ModemUSBNetMode", mb.ModemUSBNetMode)
				globalData.Store("ModemValid", mb.ModemValid)
				globalData.Store("PolicyLTEBands", mb.PolicyLTEBands)
				globalData.Store("PolicyNR5GBands", mb.PolicyNR5GBands)
				globalData.Store("SelectedLTEBands", mb.SelectedLTEBands)
				globalData.Store("SelectedNR5GBands", mb.SelectedNR5GBands)
				globalData.Store("SMSCheckInterval", mb.SMSCheckInterval)
				globalData.Store("SMSForward", mb.SMSForward)
				globalData.Store("SMSForwardTo", mb.SMSForwardTo)
				globalData.Store("ModemTemperature", mb.ModemTemperature)
			}
		}
	}
}

// formatSpeed formats speed into value and units as Mbps
func formatSpeed(mbps float64) (string, string) {
	if mbps > 100000 || mbps < 0.0 { //clamping
		mbps = 0.0
	}

	if mbps >= 1.0 {
		// For speeds ≥1 Mbps, use 3 significant digits
		return fmt.Sprintf("%.3g", mbps), "Mbps"
	}
	// For speeds <1 Mbps, keep up to 3 digits after decimal point
	return fmt.Sprintf("%.2f", mbps), "Mbps"
}

func getWANInterface() (string, error) {
	if isOpenWRT() {
		return "br-lan", nil
	}
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
	var err error
	if isOpenWRT() {
		upSpeed, ok1 := globalData.Load("UpSpeedBps")
		downSpeed, ok2 := globalData.Load("DownSpeedBps")
		if !ok1 || !ok2 {
			log.Printf("Could not get WAN network speed data\n")
			globalData.Store("WanUP", "-")
			globalData.Store("WanDOWN", "-")
			globalData.Store("WanUP_Unit", "")
			globalData.Store("WanDOWN_Unit", "")
		} else {
			wanUPVal, wanUPUnit := formatSpeed(upSpeed.(float64) / 1024 / 1024 * 8)
			wanDOWNVal, wanDOWNUnit := formatSpeed(downSpeed.(float64) / 1024 / 1024 * 8)

			globalData.Store("WanUP", wanUPVal)
			globalData.Store("WanDOWN", wanDOWNVal)
			globalData.Store("WanUP_Unit", wanUPUnit)
			globalData.Store("WanDOWN_Unit", wanDOWNUnit)
		}
	} else {
		wanInterface, err = getWANInterface()
		if err != nil {
			log.Printf("Could not get WAN interface: %v\n", err)
			globalData.Store("WanUP", "0")
			globalData.Store("WanDOWN", "0")
			time.Sleep(5 * time.Second) // prevent infinite loop
			return
		}
		netData, err := getNetworkSpeed(wanInterface)
		if err != nil {
			log.Printf("Could not get network speed: %v\n", err)
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
}

func collectFixedData() {
	kernelDate, _ := getKernelDate()
	globalData.Store("Kernel", kernelDate)
	sn, _ := getSN()
	globalData.Store("SN", sn)
}

// collectData gathers several pieces of system and network information and stores them in globalData.
func collectLinuxData(cfg Config) {
	if uptime, err := getUptime(); err != nil {
		fmt.Printf("Could not get uptime: %v\n", err)
		globalData.Store("Uptime", "N/A")
	} else {
		globalData.Store("Uptime", uptime)
	}

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

	//Fan speed
	fanSpeed, err := getFanSpeed()
	if err != nil {
		fmt.Printf("Could not get fan speed: %v\n", err)
		globalData.Store("FanRPM", "N/A")
	} else {
		globalData.Store("FanRPM", fanSpeed)
	}
}

// getFanSpeed scans /sys/class/hwmon/hwmon*/fan1_input and returns the first
// valid integer it reads.
func getFanSpeed() (int, error) {
	// Glob all fan1_input files in hwmon directories
	paths, err := filepath.Glob("/sys/class/hwmon/hwmon*/fan1_input")
	if err != nil {
		return 0, fmt.Errorf("failed to glob hwmon paths: %w", err)
	}

	for _, p := range paths {
		data, err := ioutil.ReadFile(p)
		if err != nil {
			// skip files we can't read
			continue
		}
		s := strings.TrimSpace(string(data))
		if s == "" {
			continue
		}

		speed, err := strconv.Atoi(s)
		if err != nil {
			// skip non-integer contents
			continue
		}
		return speed, nil
	}
	return 0, fmt.Errorf("no valid fan1_input found under /sys/class/hwmon")
}

func collectNetworkData(cfg Config) {
	if isOpenWRT() {
		//we have aonther func to get data from pcat-manager-web
	} else {
		if sessionDataUsage, err := getSessionDataUsageGB(wanInterface); err != nil {
			fmt.Printf("Could not get session data usage: %v\n", err)
			globalData.Store("SessionDataUsage", nil)
		} else {
			sessionDataUsage_1digit := fmt.Sprintf("%0.1f", sessionDataUsage)
			globalData.Store("SessionDataUsage", sessionDataUsage_1digit)
		}

		if monthlyDataUsage, err := getDataUsageMonthlyGB(wanInterface); err != nil {
			fmt.Printf("Could not get monthly data usage: %v\n", err)
			globalData.Store("MonthlyDataUsage", nil)
		} else {
			monthlyDataUsage_1digit := fmt.Sprintf("%0.1f", monthlyDataUsage)
			globalData.Store("MonthlyDataUsage", monthlyDataUsage_1digit)
		}
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
		//fmt.Printf("Could not get SSID: %v\n", err)
		globalData.Store("SSID", "N/A")
	} else {
		globalData.Store("SSID", ssid)
	}

	// SSID.
	if ssid2, err := getSSID2(); err != nil {
		//fmt.Printf("Could not get SSID: %v\n", err)
		globalData.Store("SSID2", "N/A")
	} else {
		globalData.Store("SSID2", ssid2)
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
	if ping0, err := pingICMP(cfg.PingSite0); err != nil {
		fmt.Printf("ICMP ping to %s failed: %v\n", cfg.PingSite0, err)
		globalData.Store("Ping0", -1) // using -1 to indicate an error
	} else {
		globalData.Store("Ping0", ping0)
	}

	// Ping Site1 using ICMP.
	if ping1, err := pingICMP(cfg.PingSite1); err != nil {
		fmt.Printf("ICMP ping to %s failed: %v\n", cfg.PingSite1, err)
		globalData.Store("Ping1", -1)
	} else {
		globalData.Store("Ping1", ping1)
	}

	/*
		// Country based on public IP geolocation.
		if country, err := getCountry(); err != nil {
			fmt.Printf("Could not get country: %v\n", err)
			globalData.Store("Country", "Unknown")
		} else {
			globalData.Store("Country", country)
		}*/

	// IPv6 public IP.
	if ipv6, err := getIPv6Public(); err != nil {
		//fmt.Printf("Could not get IPv6 public IP: %v\n", err)
		globalData.Store("PublicIPv6", "0.0.0.0")
	} else {
		globalData.Store("PublicIPv6", ipv6)
	}
}

func getSN() (string, error) {
	// Read first 500 bytes
	out, err := exec.Command("head", "-c", "10000", "/dev/mmcblk0boot1").Output()
	if err != nil {
		return "", fmt.Errorf("read partition: %w", err)
	}

	// Truncate at first 0 byte
	if idx := bytes.IndexByte(out, 0); idx != -1 {
		out = out[:idx]
	}

	// Parse JSON
	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		return "", fmt.Errorf("unmarshal JSON: %w", err)
	}

	// Extract "sn" or fallback to "machine_sn"
	var sn string
	if v, ok := payload["sn"]; ok {
		if s, ok2 := v.(string); ok2 && s != "" {
			sn = s
		}
	}
	if sn == "" {
		if v, ok := payload["machine_sn"]; ok {
			if s, ok2 := v.(string); ok2 && s != "" {
				sn = s
			}
		}
	}
	if sn == "" {
		return "", fmt.Errorf(`key "sn" or "machine_sn" not found or not a non-empty string`)
	}

	return sn, nil
}

func getUptime() (string, error) {
	// Read /proc/uptime
	data, err := ioutil.ReadFile("/proc/uptime")
	if err != nil {
		return "", fmt.Errorf("error reading /proc/uptime: %v", err)
	}

	// Parse the first value (uptime in seconds)
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return "", fmt.Errorf("invalid uptime data")
	}

	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "", fmt.Errorf("error parsing uptime: %v", err)
	}

	// Convert seconds to time.Duration
	uptime := time.Duration(seconds) * time.Second

	// Calculate days, hours, minutes, and seconds
	days := int(uptime.Hours()) / 24
	hours := int(uptime.Hours()) % 24
	minutes := int(uptime.Minutes()) % 60
	secs := int(uptime.Seconds()) % 60

	// Build human-readable string, omitting zero values
	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if secs > 0 || len(parts) == 0 { // Include seconds if zero to avoid empty string
		parts = append(parts, fmt.Sprintf("%ds", secs))
	}

	return strings.Join(parts, " "), nil
}

func getKernelDate() (string, error) {
	// get kernel version (release)
	buildOut, err := exec.Command("uname", "-v").Output()
	display_date_str := "unknown-date"
	if err == nil {
		raw := strings.TrimSpace(string(buildOut))
		parts := strings.Split(raw, " ")
		if len(parts) >= 9 { //#15 SMP PREEMPT Wed Apr 30 17:23:30 JST 2025 //debian
			display_date_str = fmt.Sprintf("%s-%s-%s", parts[8], parts[4], parts[5])
		} else if len(parts) >= 8 { //#0 SMP PREEMPT Wed May 14 09:34:38 2025 //openwrt
			display_date_str = fmt.Sprintf("%s-%s-%s", parts[7], parts[4], parts[5])
		}
	}

	return fmt.Sprintf("%s", display_date_str), nil
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

func isOpenWRT() bool {
	if _, err := os.Stat("/etc/openwrt_release"); err == nil {
		return true
	}
	return false
}

// getSSID returns connected SSID on Debian or broadcasting SSID on OpenWrt.
func getSSID() (string, error) {
	// OpenWrt detection
	if isOpenWRT() {
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

// getSSID returns connected SSID on Debian or broadcasting SSID on OpenWrt.
func getSSID2() (string, error) {
	// OpenWrt detection
	if _, err := os.Stat("/etc/openwrt_release"); err == nil {
		// OpenWrt: Use uci command
		out, err := exec.Command("uci", "get", "wireless.@wifi-iface[1].ssid").Output()
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

	// Sampling interval every 2 seconds
	time.Sleep(1999 * time.Millisecond)

	rx2, tx2, err := getInterfaceBytes(iface)
	if err != nil {
		return NetworkSpeed{}, err
	}

	downloadMbps := float64(rx2-rx1) / 1024 / 128 / 2
	uploadMbps := float64(tx2-tx1) / 1024 / 128 / 2

	return NetworkSpeed{
		UploadMbps:   uploadMbps,
		DownloadMbps: downloadMbps,
	}, nil
}

func getSessionDataUsageGB(iface string) (float64, error) {
	stats := []string{"rx_bytes", "tx_bytes"}
	var totalBytes uint64

	for _, stat := range stats {
		// build path: /sys/class/net/<iface>/statistics/<stat>
		path := filepath.Join("/sys/class/net", iface, "statistics", stat)

		// read the file
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, fmt.Errorf("failed to read %s: %w", path, err)
		}

		// parse it as uint64
		s := strings.TrimSpace(string(data))
		val, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse %s: %w", path, err)
		}

		totalBytes += val
	}

	// convert bytes → MiB
	return float64(totalBytes) / 1024.0 / 1024.0 / 1024.0, nil
}

type vnstatJSON struct {
	Interfaces []struct {
		Name    string `json:"name"`
		Traffic struct {
			// 对应 JSON 中 "traffic":"month":[…]
			Month []struct {
				Date struct {
					Year  int `json:"year"`
					Month int `json:"month"`
				} `json:"date"`
				Rx uint64 `json:"rx"`
				Tx uint64 `json:"tx"`
			} `json:"month"`
		} `json:"traffic"`
	} `json:"interfaces"`
}

// getDataUsageMonthlyGB returns the total (rx+tx) traffic for the current calendar
// month on the given interface, as reported by vnStat, in GiB.
func getDataUsageMonthlyGB(iface string) (float64, error) {
	// 1. 调用 vnstat 获取 JSON
	out, err := exec.Command("vnstat", "-i", iface, "--json").Output()
	if err != nil {
		fmt.Printf("failed to run vnstat with default interface: %s, %w", iface, err)
		iface = "wwan0"
		out, err = exec.Command("vnstat", "-i", iface, "--json").Output()
		if err != nil {
			fmt.Printf("failed to run vnstat with default interface: %s, %w", iface, err)
			iface = "br-lan"
			out, err = exec.Command("vnstat", "-i", iface, "--json").Output()
			if err != nil {
				fmt.Printf("failed to run vnstat with default interface: %s, %w", iface, err)
				return 0, fmt.Errorf("failed to run vnstat with iface: %s, %w", iface, err)
			}
		}
	}

	// 2. 解析 JSON
	var data vnstatJSON
	if err := json.Unmarshal(out, &data); err != nil {
		return 0, fmt.Errorf("failed to parse vnstat JSON: %w", err)
	}

	// 3. 找到对应接口
	var ifaceData *vnstatJSON
	var entryIdx int
	for i, entry := range data.Interfaces {
		if entry.Name == iface {
			ifaceData = &data
			entryIdx = i
			break
		}
	}
	if ifaceData == nil {
		return 0, fmt.Errorf("interface %q not found in vnstat output", iface)
	}

	// 4. 确定当前年/月
	now := time.Now()
	cy, cm := now.Year(), int(now.Month())
	cmStr := fmt.Sprintf("%02d", cm)

	// 5. 在 traffic.month 数组里找当月条目
	for _, m := range data.Interfaces[entryIdx].Traffic.Month {
		if m.Date.Year == cy && m.Date.Month == cm {
			usedBytes := m.Rx + m.Tx
			return float64(usedBytes) / (1 << 30), nil // GiB
		}
	}

	return 0, fmt.Errorf("no data for %04d-%s in vnstat output", cy, cmStr)
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
	var determineChargingByCurrent bool = false
	if determineChargingByCurrent {
		current, err := getBatteryCurrentUA()
		if err != nil {
			return false, err
		}
		return current > 0, nil
	} else {
		file, err := os.Open("/sys/class/power_supply/battery/status")
		if err != nil {
			return false, err
		}
		defer file.Close()
		content, err := ioutil.ReadAll(file)
		if err != nil {
			return false, err
		}

		battContent := strings.TrimSpace(string(content))

		if battContent == "Charging" || battContent == "Full" {
			return true, nil
		}
		return false, nil
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
	candidates := []string{"eth1", "end1", "end0", "br-lan"}

	for _, name := range candidates {
		iface, err := net.InterfaceByName(name)
		if err != nil {
			// interface doesn't exist
			continue
		}
		// skip if interface is down
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if ip4 := ipnet.IP.To4(); ip4 != nil {
					return ip4.String(), nil
				}
			}
		}
	}

	// none of the candidates had a usable IPv4
	return "LINK DOWN", nil
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
func getWifiClients() (string, error) {
	clients := "TEST"
	return clients, nil
}
