package main

// linuxFallback.go collects the data normally provided by pcat-manager-web
// straight from Linux (sysfs, ip route, ModemManager, vnstat, lease files).
// It is used as a backup plan when pcat-manager-web is not running — the
// normal situation on a plain Debian install. Every collector stores a value
// only when it actually obtained one, so a temporarily-down web service never
// gets its last good values clobbered with placeholders.

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// pcatWebUp reflects whether the last dashboard.json poll succeeded. Other
// collectors (e.g. collectWANNetworkSpeed on OpenWrt) consult it to decide
// between web-provided numbers and direct Linux measurement.
// pcatWebStateKnown is only touched by the getInfoFromPcatWeb goroutine and
// keeps the up/down transition logs to one line per change.
var (
	pcatWebUp         atomic.Bool
	pcatWebStateKnown bool
)

func haveCmd(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// collectLinuxFallbackData fills the globalData keys that getInfoFromPcatWeb
// would normally populate, using only local system sources.
func collectLinuxFallbackData() {
	// The web's remaining-time estimate is gone; let collectBatteryData()
	// compute its own from the battery counters again.
	globalData.Store("RemainingTimeFromWeb", false)

	// Active egress / top-bar network icon from the default route.
	dev := getDefaultRouteDev()
	egress, conn, label := classifyEgress(dev)
	globalData.Store("ActiveEgress", egress)
	globalData.Store("GatewayDevice", conn)
	// We cannot know the configured "Internet via" mode without
	// pcat-manager-web, so show the egress actually in use instead.
	globalData.Store("NetworkModeLabel", label)

	if osVersion := getOSVersionFromOSRelease(); osVersion != "" {
		globalData.Store("OSVersion", osVersion)
	}
	if model := getDeviceTreeModel(); model != "" {
		globalData.Store("Model", model)
	}
	// When the PMU data source (pcatPmu.go) is running it delivers the real
	// MCU board temperature — don't overwrite it with an approximation.
	if !pmuActive() {
		if temp, ok := getBoardTemperatureC(); ok {
			globalData.Store("BoardTemperature", temp)
		}
	}
	globalData.Store("SdState", sdCardState())

	if n, err := countDHCPLeases(); err == nil {
		globalData.Store("DHCPClientsCount", n)
	}
	if n, err := countWifiStations(); err == nil {
		globalData.Store("WiFiClientsCount", n)
	}

	// collectNetworkData() already fetches the public IP under PUBLIC_IP;
	// mirror it under the web's key so both consumers agree.
	if v, ok := globalData.Load("PUBLIC_IP"); ok {
		if s, ok2 := v.(string); ok2 && s != "" && s != "N/A" {
			globalData.Store("PublicIP", s)
		}
	}

	collectModemInfoFromMMCLI()
	collectVnstatUsage(dev)
}

// getDefaultRouteDev returns the interface carrying the default route ("" when
// there is none).
func getDefaultRouteDev() string {
	out, err := secureExecCommand("ip", "route", "show", "default")
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

// classifyEgress maps a default-route interface name onto the egress/
// connection vocabulary used by pcat-manager-web ("wan"/"wifi"/"mobile",
// "wired"/"wifi"/"mobile") plus a short human label for the WAN row.
func classifyEgress(dev string) (egress, conn, label string) {
	switch {
	case dev == "":
		return "", "", "-"
	case strings.HasPrefix(dev, "wwan"), strings.HasPrefix(dev, "usb"),
		strings.HasPrefix(dev, "ppp"), strings.HasPrefix(dev, "wwx"):
		return "mobile", "mobile", "Cell"
	case strings.HasPrefix(dev, "wl"):
		return "wifi", "wifi", "WiFi"
	default:
		return "wan", "wired", "Eth"
	}
}

func getOSVersionFromOSRelease() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			v := strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
			// "Debian GNU/Linux 12 (bookworm)" → "Debian 12 (bookworm)"
			return strings.ReplaceAll(v, "GNU/Linux ", "")
		}
	}
	return ""
}

func getDeviceTreeModel() string {
	data, err := os.ReadFile("/sys/firmware/devicetree/base/model")
	if err != nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(string(data)), "\x00")
}

// getBoardTemperatureC approximates the web's board_temperature. The MCU
// reading pcat-manager-web uses is not reachable from Linux, so probe the
// closest local sensors instead: the battery NTC, then a board-ish hwmon
// node, then the SoC thermal zone.
func getBoardTemperatureC() (int, bool) {
	// The photonicat-pm kernel driver exposes the true MCU reading — same
	// hwmon scan pcat-manager's pmu-manager.c performs.
	if p := pmuFindTempMbHwmon(); p != "" {
		if milli, err := readSysfsInt(p); err == nil {
			return milli / 1000, true
		}
	}
	if data, err := os.ReadFile("/sys/class/power_supply/battery/temp"); err == nil {
		if v, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64); err == nil {
			return int(v / 10), true // sysfs reports tenths of °C
		}
	}
	hwmons, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, h := range hwmons {
		nameB, err := os.ReadFile(filepath.Join(h, "name"))
		if err != nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(string(nameB)))
		if strings.Contains(name, "board") || strings.Contains(name, "ntc") {
			if data, err := os.ReadFile(filepath.Join(h, "temp1_input")); err == nil {
				if v, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64); err == nil {
					return int(v / 1000), true
				}
			}
		}
	}
	if t, err := getCpuTemp(); err == nil {
		return int(t / 1000), true
	}
	return 0, false
}

// sdCardState reports "Yes" when a real SD card (not the eMMC) is present.
func sdCardState() string {
	types, _ := filepath.Glob("/sys/block/mmcblk*/device/type")
	for _, p := range types {
		if data, err := os.ReadFile(p); err == nil &&
			strings.TrimSpace(string(data)) == "SD" {
			return "Yes"
		}
	}
	return "No"
}

// countDHCPLeases counts active leases from the common dnsmasq / ISC dhcpd
// lease files found on Debian and OpenWrt.
func countDHCPLeases() (int, error) {
	files := []string{
		"/var/lib/misc/dnsmasq.leases",
		"/tmp/dhcp.leases",
	}
	if extra, err := filepath.Glob("/var/lib/NetworkManager/dnsmasq-*.leases"); err == nil {
		files = append(files, extra...)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		n := 0
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) != "" {
				n++
			}
		}
		return n, nil
	}
	// ISC dhcpd uses a block format instead of one lease per line.
	if data, err := os.ReadFile("/var/lib/dhcp/dhcpd.leases"); err == nil {
		seen := map[string]bool{}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "lease" {
				seen[fields[1]] = true
			}
		}
		return len(seen), nil
	}
	return 0, fmt.Errorf("no DHCP lease file found")
}

// countWifiStations sums associated stations over all local AP-mode
// interfaces using `iw`.
func countWifiStations() (int, error) {
	if !haveCmd("iw") {
		return 0, fmt.Errorf("iw not available")
	}
	dirs, _ := filepath.Glob("/sys/class/net/*/wireless")
	total := 0
	found := false
	for _, d := range dirs {
		iface := filepath.Base(filepath.Dir(d))
		info, err := secureExecCommand("iw", "dev", iface, "info")
		if err != nil || !strings.Contains(string(info), "type AP") {
			continue // only APs have clients; a STA "station dump" lists its AP
		}
		out, err := secureExecCommand("iw", "dev", iface, "station", "dump")
		if err != nil {
			continue
		}
		found = true
		total += strings.Count(string(out), "Station ")
	}
	if !found {
		return 0, fmt.Errorf("no AP-mode wireless interface found")
	}
	return total, nil
}

// ---- ModemManager (mmcli) ----------------------------------------------

type mmModemInfo struct {
	Modem struct {
		Generic struct {
			Model               string   `json:"model"`
			Revision            string   `json:"revision"`
			EquipmentIdentifier string   `json:"equipment-identifier"`
			AccessTechnologies  []string `json:"access-technologies"`
			OwnNumbers          []string `json:"own-numbers"`
			Sim                 string   `json:"sim"`
			State               string   `json:"state"`
			SignalQuality       struct {
				Value string `json:"value"`
			} `json:"signal-quality"`
		} `json:"generic"`
		ThreeGPP struct {
			OperatorName string `json:"operator-name"`
		} `json:"3gpp"`
	} `json:"modem"`
}

func mmcliFirstModem() (string, bool) {
	out, err := secureExecCommand("mmcli", "-L", "-J")
	if err != nil {
		return "", false
	}
	var list struct {
		Modems []string `json:"modem-list"`
	}
	if err := secureUnmarshal(out, &list); err != nil || len(list.Modems) == 0 {
		return "", false
	}
	return list.Modems[0], true
}

// accessTechsToGeneration reduces mmcli access technologies to the "5G"/"4G"/
// "3G"/"2G" vocabulary the top bar and carrier field expect.
func accessTechsToGeneration(techs []string) string {
	best := ""
	rank := func(g string) int {
		switch g {
		case "5G":
			return 4
		case "4G":
			return 3
		case "3G":
			return 2
		case "2G":
			return 1
		}
		return 0
	}
	for _, t := range techs {
		var g string
		switch strings.ToLower(t) {
		case "5gnr":
			g = "5G"
		case "lte":
			g = "4G"
		case "umts", "hsdpa", "hsupa", "hspa", "hspa-plus", "wcdma":
			g = "3G"
		case "gsm", "gprs", "edge":
			g = "2G"
		}
		if rank(g) > rank(best) {
			best = g
		}
	}
	return best
}

// collectModemInfoFromMMCLI fills the modem-related keys via ModemManager,
// the stock way a Debian system manages the cellular modem.
func collectModemInfoFromMMCLI() {
	if !haveCmd("mmcli") {
		return
	}
	modemPath, ok := mmcliFirstModem()
	if !ok {
		return
	}
	out, err := secureExecCommand("mmcli", "-m", modemPath, "-J")
	if err != nil {
		return
	}
	var mm mmModemInfo
	if err := secureUnmarshal(out, &mm); err != nil {
		return
	}
	g := mm.Modem.Generic

	if g.Model != "" {
		globalData.Store("ModemModel", g.Model)
		globalData.Store("ModemValid", true)
	}
	if g.Revision != "" {
		globalData.Store("ModemFirmwareVer", g.Revision)
	}
	if g.EquipmentIdentifier != "" {
		globalData.Store("IMEINum", g.EquipmentIdentifier)
	}
	if v, err := strconv.Atoi(g.SignalQuality.Value); err == nil {
		globalData.Store("ModemSignalStrength", v)
	}
	if carrier := accessTechsToGeneration(g.AccessTechnologies); carrier != "" {
		globalData.Store("Carrier", carrier)
	}
	if len(g.AccessTechnologies) > 0 {
		globalData.Store("ModemNetworkInfo",
			strings.ToUpper(strings.Join(g.AccessTechnologies, "/")))
	}
	if op := mm.Modem.ThreeGPP.OperatorName; op != "" {
		globalData.Store("ISPName", op)
		globalData.Store("CellCarrierInfo", op)
	}
	if len(g.OwnNumbers) > 0 && g.OwnNumbers[0] != "" {
		globalData.Store("SimNumber", g.OwnNumbers[0])
	}
	if g.Sim != "" && g.Sim != "--" {
		globalData.Store("SimState", "Yes")
	} else {
		globalData.Store("SimState", "No")
	}
}

// getSmsJsonFromModemManager rebuilds the pcat-manager-web
// /api/v2/sms/list.json payload from ModemManager's stored messages so the
// SMS pages keep working without the web service.
func getSmsJsonFromModemManager(limit int) string {
	if !haveCmd("mmcli") {
		return ""
	}
	modemPath, ok := mmcliFirstModem()
	if !ok {
		return ""
	}
	out, err := secureExecCommand("mmcli", "-m", modemPath, "--messaging-list-sms", "-J")
	if err != nil {
		return ""
	}
	var list struct {
		SMS []string `json:"modem.messaging.sms"`
	}
	if err := secureUnmarshal(out, &list); err != nil || len(list.SMS) == 0 {
		return ""
	}

	type mmSms struct {
		SMS struct {
			Content struct {
				Number string `json:"number"`
				Text   string `json:"text"`
			} `json:"content"`
			Properties struct {
				PduType   string `json:"pdu-type"`
				Timestamp string `json:"timestamp"`
			} `json:"properties"`
		} `json:"sms"`
	}

	var msgs []SMS
	for _, p := range list.SMS {
		raw, err := secureExecCommand("mmcli", "-s", p, "-J")
		if err != nil {
			continue
		}
		var m mmSms
		if err := secureUnmarshal(raw, &m); err != nil {
			continue
		}
		// Only received messages; skip drafts/sent ("submit").
		if m.SMS.Properties.PduType != "" && m.SMS.Properties.PduType != "deliver" {
			continue
		}
		ts := m.SMS.Properties.Timestamp
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			ts = t.Format("2006-01-02 15:04:05")
		}
		idx := 0
		if i := strings.LastIndex(p, "/"); i != -1 {
			idx, _ = strconv.Atoi(p[i+1:])
		}
		msgs = append(msgs, SMS{
			Index:     idx,
			Sender:    m.SMS.Content.Number,
			Timestamp: ts,
			Content:   m.SMS.Content.Text,
		})
	}
	if len(msgs) == 0 {
		return ""
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Timestamp > msgs[j].Timestamp })
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[:limit]
	}
	payload := struct {
		Msg []SMS `json:"msg"`
	}{msgs}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	log.Printf("SMS fallback: built %d message(s) from ModemManager", len(msgs))
	return string(b)
}

// ---- vnstat data usage ---------------------------------------------------

type vnstatFallbackJSON struct {
	Interfaces []struct {
		Name    string `json:"name"`
		Traffic struct {
			Day []struct {
				Date struct {
					Year  int `json:"year"`
					Month int `json:"month"`
					Day   int `json:"day"`
				} `json:"date"`
				Rx uint64 `json:"rx"`
				Tx uint64 `json:"tx"`
			} `json:"day"`
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

// collectVnstatUsage fills today/week/last-month usage from vnstat. (The
// current month is already handled by collectNetworkData on Debian, so it is
// left alone to avoid two writers formatting the same key differently.)
func collectVnstatUsage(iface string) {
	if iface == "" || !haveCmd("vnstat") {
		return
	}
	out, err := secureExecCommand("vnstat", "-i", iface, "--json")
	if err != nil {
		return
	}
	var data vnstatFallbackJSON
	if err := secureUnmarshal(out, &data); err != nil {
		return
	}
	for _, entry := range data.Interfaces {
		if entry.Name != iface {
			continue
		}
		now := time.Now()
		today := now.Format("2006-01-02")
		weekStart := now.AddDate(0, 0, -6).Format("2006-01-02")
		var dayGB, weekGB float64
		for _, d := range entry.Traffic.Day {
			date := fmt.Sprintf("%04d-%02d-%02d", d.Date.Year, d.Date.Month, d.Date.Day)
			gb := float64(d.Rx+d.Tx) / (1 << 30)
			if date == today {
				dayGB = gb
			}
			if date >= weekStart && date <= today {
				weekGB += gb
			}
		}
		globalData.Store("DailyDataUsage", fmt.Sprintf("%0.2f", dayGB))
		globalData.Store("WeeklyDataUsage", fmt.Sprintf("%0.2f", weekGB))

		lastMonth := now.AddDate(0, -1, 0)
		for _, m := range entry.Traffic.Month {
			if m.Date.Year == lastMonth.Year() && m.Date.Month == int(lastMonth.Month()) {
				globalData.Store("LastMonthUsage",
					fmt.Sprintf("%0.2f", float64(m.Rx+m.Tx)/(1<<30)))
			}
		}
		return
	}
}
