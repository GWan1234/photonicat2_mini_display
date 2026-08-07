package main

// Coverage tests for linuxFallback.go. The /sys, /etc and lease-file readers
// are pointed at t.TempDir() fixtures via the pmuCov* path vars; the mmcli /
// vnstat / iw / ip callers resolve stub shell scripts through a test-local
// PATH. Everything is restored on cleanup, so the existing full-environment
// sweep test keeps seeing the real system.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pmuCovBinDir creates a directory suitable as a lone PATH entry.
func pmuCovBinDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// pmuCovCatScript writes an executable script named name into dir that cats
// the given fixture file, ignoring its arguments.
func pmuCovCatScript(t *testing.T, dir, name, fixture string) {
	t.Helper()
	pmuCovScript(t, dir, name, "#!/bin/sh\ncat "+fixture+"\n")
}

func TestPmuCovNetdevHasCarrier(t *testing.T) {
	root := t.TempDir()
	net := filepath.Join(root, "net") + string(os.PathSeparator)
	pmuCovSwap(t, &pmuCovSysClassNetPath, net)
	pmuCovWriteFile(t, filepath.Join(net, "eth0", "carrier"), "1\n")
	pmuCovWriteFile(t, filepath.Join(net, "eth1", "carrier"), "0\n")

	cases := []struct {
		dev  string
		want bool
	}{
		{"", false},
		{"eth0", true},
		{"eth1", false},
		{"eth9", false}, // carrier file missing → treated as no link
		{"wlan0", true}, // non-eth devices are never filtered
		{"ppp0", true},  // PPPoE checks eth0 underneath
	}
	for _, c := range cases {
		if got := netdevHasCarrier(c.dev); got != c.want {
			t.Errorf("netdevHasCarrier(%q) = %v; want %v", c.dev, got, c.want)
		}
	}
}

func TestPmuCovGetDefaultRouteDev(t *testing.T) {
	root := t.TempDir()
	net := filepath.Join(root, "net") + string(os.PathSeparator)
	pmuCovSwap(t, &pmuCovSysClassNetPath, net)
	pmuCovWriteFile(t, filepath.Join(net, "eth0", "carrier"), "1\n")
	pmuCovWriteFile(t, filepath.Join(net, "eth1", "carrier"), "0\n")

	routes := func(t *testing.T, lines string) {
		t.Helper()
		bin := pmuCovBinDir(t)
		fixture := filepath.Join(bin, "routes.txt")
		pmuCovWriteFile(t, fixture, lines)
		pmuCovCatScript(t, bin, "ip", fixture)
		pmuCovSetPath(t, bin)
	}

	t.Run("ip missing", func(t *testing.T) {
		t.Setenv("PATH", pmuCovBinDir(t))
		if got := getDefaultRouteDev(); got != "" {
			t.Errorf("got %q; want \"\"", got)
		}
	})

	t.Run("lowest metric with carrier wins", func(t *testing.T) {
		routes(t, `default via 10.0.0.1 dev eth1 metric 5
default dev wwan0 metric 700
default via 10.0.0.1 dev eth0 metric 100
default via 10.0.0.1
default dev eth1 metric abc
not a route line

`)
		if got := getDefaultRouteDev(); got != "eth0" {
			t.Errorf("got %q; want eth0 (eth1 has no carrier, wwan0 metric higher)", got)
		}
	})

	t.Run("no usable route", func(t *testing.T) {
		routes(t, "default via 10.0.0.1 dev eth1 metric 5\n")
		if got := getDefaultRouteDev(); got != "" {
			t.Errorf("got %q; want \"\" (only a carrier-less eth route)", got)
		}
	})

	t.Run("bare default dev", func(t *testing.T) {
		routes(t, "default dev usb0\n")
		if got := getDefaultRouteDev(); got != "usb0" {
			t.Errorf("got %q; want usb0", got)
		}
	})
}

func TestPmuCovClassifyEgressDriverBranch(t *testing.T) {
	root := t.TempDir()
	net := filepath.Join(root, "net") + string(os.PathSeparator)
	pmuCovSwap(t, &pmuCovSysClassNetPath, net)

	link := func(dev, driver string) {
		dir := filepath.Join(net, dev, "device")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../../bus/usb/drivers/"+driver, filepath.Join(dir, "driver")); err != nil {
			t.Fatal(err)
		}
	}
	link("eth2", "rndis_host")
	link("eth3", "cdc_ncm")
	link("eth4", "r8169")

	cases := []struct {
		dev, egress, conn, label string
	}{
		{"eth2", "mobile", "mobile", "Cell"}, // USB rndis gadget is the modem
		{"eth3", "mobile", "mobile", "Cell"},
		{"eth4", "wan", "wired", "Eth"}, // real NIC driver stays wired
		{"eth5", "wan", "wired", "Eth"}, // no driver link at all
	}
	for _, c := range cases {
		e, conn, l := classifyEgress(c.dev)
		if e != c.egress || conn != c.conn || l != c.label {
			t.Errorf("classifyEgress(%q) = %q,%q,%q; want %q,%q,%q",
				c.dev, e, conn, l, c.egress, c.conn, c.label)
		}
	}
}

func TestPmuCovGetOSVersionFromOSRelease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "os-release")
	pmuCovWriteFile(t, path, "ID=debian\nVERSION_ID=\"13\"\n")
	pmuCovSwap(t, &pmuCovOSReleasePath, path)
	if got := getOSVersionFromOSRelease(); got != "Debian 13" {
		t.Errorf("got %q; want Debian 13", got)
	}
	pmuCovSwap(t, &pmuCovOSReleasePath, filepath.Join(dir, "missing"))
	if got := getOSVersionFromOSRelease(); got != "" {
		t.Errorf("missing file: got %q; want \"\"", got)
	}
}

func TestPmuCovFormatOSVersionMoreShapes(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"openwrt VERSION only", "ID=openwrt\nVERSION=\"25.02.0\"\n", "R25.02"},
		{"openwrt nothing else", "ID=openwrt\n", ""},
		{"generic NAME and VERSION_ID", "NAME=\"Foo\"\nVERSION_ID=\"9\"\nID=foo\n", "Foo 9"},
		{"generic NAME only", "NAME=\"Foo\"\nID=foo\n", "Foo"},
		{"debian no VERSION_ID falls to PRETTY_NAME",
			"ID=debian\nPRETTY_NAME=\"Debian GNU/Linux sid\"\n", "Debian sid"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := formatOSVersionFromOSRelease(c.in); got != c.want {
			t.Errorf("%s: got %q; want %q", c.name, got, c.want)
		}
	}
	// formatOpenWrtVersionLabel: a slash shape with an empty half is returned raw.
	if got := formatOpenWrtVersionLabel("/ r7748"); got != "/ r7748" {
		t.Errorf("formatOpenWrtVersionLabel(\"/ r7748\") = %q; want it unchanged", got)
	}
}

func TestPmuCovGetDeviceTreeModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model")
	pmuCovWriteFile(t, path, "Photonicat Test\x00\x00")
	pmuCovSwap(t, &pmuCovDTModelPath, path)
	if got := getDeviceTreeModel(); got != "Photonicat Test" {
		t.Errorf("got %q; want Photonicat Test", got)
	}
	pmuCovSwap(t, &pmuCovDTModelPath, filepath.Join(dir, "missing"))
	if got := getDeviceTreeModel(); got != "" {
		t.Errorf("missing file: got %q; want \"\"", got)
	}
}

func TestPmuCovGetBoardTemperatureC(t *testing.T) {
	t.Run("pmu hwmon wins", func(t *testing.T) {
		root := t.TempDir()
		pmuCovSwap(t, &pmuCovHwmonGlob, filepath.Join(root, "hwmon*"))
		pmuCovSwap(t, &pmuCovBatteryTempPath, filepath.Join(root, "no-batt"))
		pmuCovWriteFile(t, filepath.Join(root, "hwmon0", "name"), "pcat_pm_hwmon_temp_mb\n")
		pmuCovWriteFile(t, filepath.Join(root, "hwmon0", "temp1_input"), "42500\n")
		if v, ok := getBoardTemperatureC(); !ok || v != 42 {
			t.Errorf("got %d,%v; want 42,true", v, ok)
		}
	})

	t.Run("battery NTC", func(t *testing.T) {
		root := t.TempDir()
		pmuCovSwap(t, &pmuCovHwmonGlob, filepath.Join(root, "hwmon*"))
		// A pcat hwmon whose temp file is unreadable must not stop the probe.
		pmuCovWriteFile(t, filepath.Join(root, "hwmon0", "name"), "pcat_pm_hwmon_temp_mb\n")
		batt := filepath.Join(root, "battery-temp")
		pmuCovWriteFile(t, batt, "305\n") // tenths of °C
		pmuCovSwap(t, &pmuCovBatteryTempPath, batt)
		if v, ok := getBoardTemperatureC(); !ok || v != 30 {
			t.Errorf("got %d,%v; want 30,true", v, ok)
		}
	})

	t.Run("boardish hwmon node", func(t *testing.T) {
		root := t.TempDir()
		pmuCovSwap(t, &pmuCovHwmonGlob, filepath.Join(root, "hwmon*"))
		batt := filepath.Join(root, "battery-temp")
		pmuCovWriteFile(t, batt, "not-a-number\n") // battery parse fails → next source
		pmuCovSwap(t, &pmuCovBatteryTempPath, batt)
		if err := os.MkdirAll(filepath.Join(root, "hwmon0"), 0o755); err != nil {
			t.Fatal(err) // unreadable name → skipped
		}
		pmuCovWriteFile(t, filepath.Join(root, "hwmon1", "name"), "ntc_adc\n")
		pmuCovWriteFile(t, filepath.Join(root, "hwmon1", "temp1_input"), "41000\n")
		if v, ok := getBoardTemperatureC(); !ok || v != 41 {
			t.Errorf("got %d,%v; want 41,true", v, ok)
		}
	})

	t.Run("falls through to cpu temp", func(t *testing.T) {
		root := t.TempDir()
		pmuCovSwap(t, &pmuCovHwmonGlob, filepath.Join(root, "hwmon*"))
		pmuCovSwap(t, &pmuCovBatteryTempPath, filepath.Join(root, "no-batt"))
		// A board-named hwmon whose temp file is missing exercises that inner
		// error branch too.
		pmuCovWriteFile(t, filepath.Join(root, "hwmon0", "name"), "board_sensor\n")
		v, ok := getBoardTemperatureC()
		if cpu, err := getCpuTemp(); err == nil {
			if !ok || v != int(cpu/1000) {
				t.Errorf("got %d,%v; want %d,true from the CPU thermal zone", v, ok, int(cpu/1000))
			}
		} else if ok {
			t.Errorf("got %d,%v; want 0,false with no sensor available", v, ok)
		}
	})
}

func TestPmuCovCountDHCPLeases(t *testing.T) {
	t.Run("dnsmasq", func(t *testing.T) {
		root := t.TempDir()
		lease := filepath.Join(root, "dnsmasq.leases")
		pmuCovWriteFile(t, lease, "1 aa host1\n2 bb host2\n3 cc host3\n\n")
		pmuCovSwap(t, &pmuCovDHCPLeaseFiles, []string{filepath.Join(root, "missing"), lease})
		pmuCovSwap(t, &pmuCovNMLeaseGlob, filepath.Join(root, "nm-*.leases"))
		pmuCovSwap(t, &pmuCovDhcpdLeasePath, filepath.Join(root, "no-dhcpd"))
		if n, err := countDHCPLeases(); err != nil || n != 3 {
			t.Errorf("got %d,%v; want 3,nil", n, err)
		}
	})

	t.Run("networkmanager glob", func(t *testing.T) {
		root := t.TempDir()
		nm := filepath.Join(root, "nm-lan.leases")
		pmuCovWriteFile(t, nm, "1 aa host1\n2 bb host2\n")
		pmuCovSwap(t, &pmuCovDHCPLeaseFiles, []string{filepath.Join(root, "missing")})
		pmuCovSwap(t, &pmuCovNMLeaseGlob, filepath.Join(root, "nm-*.leases"))
		pmuCovSwap(t, &pmuCovDhcpdLeasePath, filepath.Join(root, "no-dhcpd"))
		if n, err := countDHCPLeases(); err != nil || n != 2 {
			t.Errorf("got %d,%v; want 2,nil", n, err)
		}
	})

	t.Run("isc dhcpd blocks", func(t *testing.T) {
		root := t.TempDir()
		dhcpd := filepath.Join(root, "dhcpd.leases")
		pmuCovWriteFile(t, dhcpd, `lease 10.0.0.5 {
  ends never;
}
lease 10.0.0.6 {
}
lease 10.0.0.5 {
}
`)
		pmuCovSwap(t, &pmuCovDHCPLeaseFiles, []string{filepath.Join(root, "missing")})
		pmuCovSwap(t, &pmuCovNMLeaseGlob, filepath.Join(root, "nm-*.leases"))
		pmuCovSwap(t, &pmuCovDhcpdLeasePath, dhcpd)
		if n, err := countDHCPLeases(); err != nil || n != 2 {
			t.Errorf("got %d,%v; want 2 unique leases,nil", n, err)
		}
	})

	t.Run("nothing found", func(t *testing.T) {
		root := t.TempDir()
		pmuCovSwap(t, &pmuCovDHCPLeaseFiles, []string{filepath.Join(root, "missing")})
		pmuCovSwap(t, &pmuCovNMLeaseGlob, filepath.Join(root, "nm-*.leases"))
		pmuCovSwap(t, &pmuCovDhcpdLeasePath, filepath.Join(root, "no-dhcpd"))
		if _, err := countDHCPLeases(); err == nil {
			t.Error("expected an error with no lease files present")
		}
	})
}

// pmuCovIwScript is an iw stub: ap0 is an AP with two stations, ap1 is an AP
// whose station dump fails, everything else is a managed STA.
const pmuCovIwScript = `#!/bin/sh
if [ "$3" = "info" ]; then
  case "$2" in
    ap0|ap1) echo "	type AP" ;;
    *) echo "	type managed" ;;
  esac
else
  [ "$2" = "ap1" ] && exit 1
  printf 'Station aa:bb:cc:dd:ee:f0 (on ap0)\nStation aa:bb:cc:dd:ee:f1 (on ap0)\n'
fi
`

func TestPmuCovCountWifiStations(t *testing.T) {
	t.Run("iw missing", func(t *testing.T) {
		t.Setenv("PATH", pmuCovBinDir(t))
		if _, err := countWifiStations(); err == nil {
			t.Error("expected an error without iw installed")
		}
	})

	t.Run("counts AP stations", func(t *testing.T) {
		root := t.TempDir()
		net := filepath.Join(root, "net") + string(os.PathSeparator)
		pmuCovSwap(t, &pmuCovSysClassNetPath, net)
		for _, iface := range []string{"ap0", "ap1", "sta0"} {
			if err := os.MkdirAll(filepath.Join(net, iface, "wireless"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		bin := pmuCovBinDir(t)
		pmuCovScript(t, bin, "iw", pmuCovIwScript)
		pmuCovSetPath(t, bin)
		if n, err := countWifiStations(); err != nil || n != 2 {
			t.Errorf("got %d,%v; want 2,nil", n, err)
		}
	})

	t.Run("no AP interfaces", func(t *testing.T) {
		root := t.TempDir()
		net := filepath.Join(root, "net") + string(os.PathSeparator)
		pmuCovSwap(t, &pmuCovSysClassNetPath, net)
		if err := os.MkdirAll(filepath.Join(net, "sta0", "wireless"), 0o755); err != nil {
			t.Fatal(err)
		}
		bin := pmuCovBinDir(t)
		pmuCovScript(t, bin, "iw", pmuCovIwScript)
		pmuCovSetPath(t, bin)
		if _, err := countWifiStations(); err == nil {
			t.Error("expected an error when only STA interfaces exist")
		}
	})
}

// ---- mmcli fixtures -------------------------------------------------------

const pmuCovModemPath = "/org/freedesktop/ModemManager1/Modem/0"

// pmuCovMmcliDir builds a stub mmcli responding from fixture files. Fixtures
// maps a case-glob (matched against "$*") to file content; matching order
// follows the order slice.
func pmuCovMmcliDir(t *testing.T, order []string, fixtures map[string]string) string {
	t.Helper()
	bin := pmuCovBinDir(t)
	script := "#!/bin/sh\ncase \"$*\" in\n"
	for i, pattern := range order {
		fix := filepath.Join(bin, fmt.Sprintf("fixture%d.json", i))
		body := fixtures[pattern]
		if body == "FAIL" {
			script += "  " + pattern + ") exit 1 ;;\n"
			continue
		}
		pmuCovWriteFile(t, fix, body)
		script += "  " + pattern + ") cat " + fix + " ;;\n"
	}
	script += "esac\n"
	pmuCovScript(t, bin, "mmcli", script)
	return bin
}

const pmuCovModemJSON = `{"modem":{"generic":{
  "model":"FM350-GL","revision":"81600.0000.00.29",
  "equipment-identifier":"123456789012345",
  "access-technologies":["lte","5gnr"],
  "own-numbers":["+15551234567"],
  "sim":"/org/freedesktop/ModemManager1/SIM/0",
  "state":"connected","signal-quality":{"value":"78"}},
  "3gpp":{"operator-name":"TestOp"}}}`

func TestPmuCovMmcliFirstModem(t *testing.T) {
	t.Run("no mmcli", func(t *testing.T) {
		t.Setenv("PATH", pmuCovBinDir(t))
		if p, ok := mmcliFirstModem(); ok || p != "" {
			t.Errorf("got %q,%v; want \"\",false", p, ok)
		}
	})
	t.Run("finds first modem", func(t *testing.T) {
		bin := pmuCovMmcliDir(t, []string{"-L*"}, map[string]string{
			"-L*": `{"modem-list":["` + pmuCovModemPath + `"]}`,
		})
		pmuCovSetPath(t, bin)
		if p, ok := mmcliFirstModem(); !ok || p != pmuCovModemPath {
			t.Errorf("got %q,%v; want %q,true", p, ok, pmuCovModemPath)
		}
	})
	t.Run("empty list", func(t *testing.T) {
		bin := pmuCovMmcliDir(t, []string{"-L*"}, map[string]string{
			"-L*": `{"modem-list":[]}`,
		})
		pmuCovSetPath(t, bin)
		if _, ok := mmcliFirstModem(); ok {
			t.Error("empty modem list reported a modem")
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		bin := pmuCovMmcliDir(t, []string{"-L*"}, map[string]string{
			"-L*": `not json`,
		})
		pmuCovSetPath(t, bin)
		if _, ok := mmcliFirstModem(); ok {
			t.Error("invalid JSON reported a modem")
		}
	})
}

func TestPmuCovCollectModemInfoFromMMCLI(t *testing.T) {
	keys := []string{"ModemModel", "ModemValid", "ModemFirmwareVer", "IMEINum",
		"ModemSignalStrength", "Carrier", "CellGeneration", "ModemNetworkInfo",
		"ISPName", "CellCarrierInfo", "SimNumber", "SimState"}

	t.Run("no mmcli", func(t *testing.T) {
		pmuCovStashKeys(t, keys...)
		globalData.Delete("ModemModel")
		t.Setenv("PATH", pmuCovBinDir(t))
		collectModemInfoFromMMCLI()
		if _, ok := globalData.Load("ModemModel"); ok {
			t.Error("ModemModel set although mmcli is unavailable")
		}
	})

	t.Run("no modems", func(t *testing.T) {
		pmuCovStashKeys(t, keys...)
		globalData.Delete("SimState")
		bin := pmuCovMmcliDir(t, []string{"-L*"}, map[string]string{
			"-L*": `{"modem-list":[]}`,
		})
		pmuCovSetPath(t, bin)
		collectModemInfoFromMMCLI()
		if _, ok := globalData.Load("SimState"); ok {
			t.Error("SimState stored although no modem is present")
		}
	})

	t.Run("full modem", func(t *testing.T) {
		pmuCovStashKeys(t, keys...)
		bin := pmuCovMmcliDir(t, []string{"-L*", "*"}, map[string]string{
			"-L*": `{"modem-list":["` + pmuCovModemPath + `"]}`,
			"*":   pmuCovModemJSON,
		})
		pmuCovSetPath(t, bin)
		collectModemInfoFromMMCLI()
		want := map[string]any{
			"ModemModel":          "FM350-GL",
			"ModemValid":          true,
			"ModemFirmwareVer":    "81600.0000.00.29",
			"IMEINum":             "123456789012345",
			"ModemSignalStrength": 78,
			"Carrier":             "5G",
			"ModemNetworkInfo":    "LTE/5GNR",
			"ISPName":             "TestOp",
			"CellCarrierInfo":     "TestOp",
			"SimNumber":           "+15551234567",
			"SimState":            "Yes",
		}
		for k, v := range want {
			if got, _ := globalData.Load(k); got != v {
				t.Errorf("%s = %v; want %v", k, got, v)
			}
		}
		if _, ok := globalData.Load("CellGeneration"); !ok {
			t.Error("CellGeneration not stored")
		}
	})

	t.Run("bare modem no sim", func(t *testing.T) {
		pmuCovStashKeys(t, keys...)
		globalData.Delete("ModemModel")
		bin := pmuCovMmcliDir(t, []string{"-L*", "*"}, map[string]string{
			"-L*": `{"modem-list":["` + pmuCovModemPath + `"]}`,
			"*":   `{"modem":{"generic":{"sim":"--","signal-quality":{"value":""}},"3gpp":{}}}`,
		})
		pmuCovSetPath(t, bin)
		collectModemInfoFromMMCLI()
		if v, _ := globalData.Load("SimState"); v != "No" {
			t.Errorf("SimState = %v; want No", v)
		}
		if _, ok := globalData.Load("ModemModel"); ok {
			t.Error("ModemModel stored although the report had no model")
		}
	})

	t.Run("modem query returns junk", func(t *testing.T) {
		pmuCovStashKeys(t, keys...)
		globalData.Delete("SimState")
		bin := pmuCovMmcliDir(t, []string{"-L*", "*"}, map[string]string{
			"-L*": `{"modem-list":["` + pmuCovModemPath + `"]}`,
			"*":   "not json",
		})
		pmuCovSetPath(t, bin)
		collectModemInfoFromMMCLI()
		if _, ok := globalData.Load("SimState"); ok {
			t.Error("SimState stored although the modem report was unparsable")
		}
	})

	t.Run("modem query fails", func(t *testing.T) {
		pmuCovStashKeys(t, keys...)
		globalData.Delete("SimState")
		bin := pmuCovMmcliDir(t, []string{"-L*", "*"}, map[string]string{
			"-L*": `{"modem-list":["` + pmuCovModemPath + `"]}`,
			"*":   "FAIL",
		})
		pmuCovSetPath(t, bin)
		collectModemInfoFromMMCLI()
		if _, ok := globalData.Load("SimState"); ok {
			t.Error("SimState stored although the modem query failed")
		}
	})
}

func TestPmuCovGetSmsJsonFromModemManager(t *testing.T) {
	smsBase := "/org/freedesktop/ModemManager1/SMS/"
	deliver := func(number, text, ts string) string {
		return `{"sms":{"content":{"number":"` + number + `","text":"` + text +
			`"},"properties":{"pdu-type":"deliver","timestamp":"` + ts + `"}}}`
	}
	fullDir := func(t *testing.T) string {
		return pmuCovMmcliDir(t,
			[]string{"*--messaging-list-sms*", "*SMS/12*", "*SMS/7*", "*SMS/3*", "*SMS/5*", "*SMS/9*", "-L*", "*"},
			map[string]string{
				"*--messaging-list-sms*": `{"modem.messaging.sms":["` + smsBase + `12","` +
					smsBase + `7","` + smsBase + `3","` + smsBase + `5","` + smsBase + `9"]}`,
				"*SMS/12*": deliver("+1555", "hello", "2026-08-01T10:00:00+08:00"),
				"*SMS/7*": `{"sms":{"content":{"number":"+1555","text":"draft"},` +
					`"properties":{"pdu-type":"submit","timestamp":"2026-08-03T10:00:00+08:00"}}}`,
				"*SMS/3*": deliver("+1666", "second", "2026-08-02 09:00:00"), // non-RFC3339, kept raw
				"*SMS/5*": "FAIL",
				"*SMS/9*": "not json",
				"-L*":     `{"modem-list":["` + pmuCovModemPath + `"]}`,
				"*":       pmuCovModemJSON,
			})
	}

	t.Run("no mmcli", func(t *testing.T) {
		t.Setenv("PATH", pmuCovBinDir(t))
		if got := getSmsJsonFromModemManager(0); got != "" {
			t.Errorf("got %q; want \"\"", got)
		}
	})

	t.Run("no modems", func(t *testing.T) {
		bin := pmuCovMmcliDir(t, []string{"-L*"}, map[string]string{
			"-L*": `{"modem-list":[]}`,
		})
		pmuCovSetPath(t, bin)
		if got := getSmsJsonFromModemManager(0); got != "" {
			t.Errorf("got %q; want \"\"", got)
		}
	})

	t.Run("messaging list fails", func(t *testing.T) {
		bin := pmuCovMmcliDir(t, []string{"*--messaging-list-sms*", "-L*"}, map[string]string{
			"*--messaging-list-sms*": "FAIL",
			"-L*":                    `{"modem-list":["` + pmuCovModemPath + `"]}`,
		})
		pmuCovSetPath(t, bin)
		if got := getSmsJsonFromModemManager(0); got != "" {
			t.Errorf("got %q; want \"\"", got)
		}
	})

	t.Run("empty sms list", func(t *testing.T) {
		bin := pmuCovMmcliDir(t, []string{"*--messaging-list-sms*", "-L*"}, map[string]string{
			"*--messaging-list-sms*": `{"modem.messaging.sms":[]}`,
			"-L*":                    `{"modem-list":["` + pmuCovModemPath + `"]}`,
		})
		pmuCovSetPath(t, bin)
		if got := getSmsJsonFromModemManager(0); got != "" {
			t.Errorf("got %q; want \"\"", got)
		}
	})

	t.Run("only skippable messages", func(t *testing.T) {
		bin := pmuCovMmcliDir(t,
			[]string{"*--messaging-list-sms*", "*SMS/7*", "-L*", "*"},
			map[string]string{
				"*--messaging-list-sms*": `{"modem.messaging.sms":["` + smsBase + `7"]}`,
				"*SMS/7*": `{"sms":{"content":{"number":"+1555","text":"draft"},` +
					`"properties":{"pdu-type":"submit","timestamp":"2026-08-03T10:00:00+08:00"}}}`,
				"-L*": `{"modem-list":["` + pmuCovModemPath + `"]}`,
				"*":   pmuCovModemJSON,
			})
		pmuCovSetPath(t, bin)
		if got := getSmsJsonFromModemManager(0); got != "" {
			t.Errorf("got %q; want \"\" when every stored message is a draft", got)
		}
	})

	t.Run("builds sorted payload", func(t *testing.T) {
		pmuCovSetPath(t, fullDir(t))
		raw := getSmsJsonFromModemManager(0)
		if raw == "" {
			t.Fatal("no payload built")
		}
		var payload struct {
			Msg []SMS `json:"msg"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("payload is not JSON: %v", err)
		}
		if len(payload.Msg) != 2 {
			t.Fatalf("got %d messages; want 2 (submit/broken ones skipped)", len(payload.Msg))
		}
		// Newest first: the raw-timestamp Aug 2 message sorts above Aug 1.
		if payload.Msg[0].Index != 3 || payload.Msg[0].Content != "second" {
			t.Errorf("first message = %+v; want index 3 / \"second\"", payload.Msg[0])
		}
		if payload.Msg[1].Index != 12 || payload.Msg[1].Timestamp != "2026-08-01 10:00:00" {
			t.Errorf("second message = %+v; want index 12 with reformatted timestamp", payload.Msg[1])
		}
	})

	t.Run("limit trims", func(t *testing.T) {
		pmuCovSetPath(t, fullDir(t))
		var payload struct {
			Msg []SMS `json:"msg"`
		}
		if err := json.Unmarshal([]byte(getSmsJsonFromModemManager(1)), &payload); err != nil {
			t.Fatalf("payload is not JSON: %v", err)
		}
		if len(payload.Msg) != 1 {
			t.Errorf("limit 1 returned %d messages", len(payload.Msg))
		}
	})
}

// ---- vnstat ---------------------------------------------------------------

// pmuCovVnstatJSON builds a vnstat --json fixture for eth0 with 1 GiB today,
// 2 GiB yesterday, 7 GiB ten days ago and 5 GiB last month.
func pmuCovVnstatJSON() string {
	now := time.Now()
	day := func(t time.Time, rx, tx uint64) string {
		return fmt.Sprintf(`{"date":{"year":%d,"month":%d,"day":%d},"rx":%d,"tx":%d}`,
			t.Year(), int(t.Month()), t.Day(), rx, tx)
	}
	month := func(t time.Time, rx, tx uint64) string {
		return fmt.Sprintf(`{"date":{"year":%d,"month":%d},"rx":%d,"tx":%d}`,
			t.Year(), int(t.Month()), rx, tx)
	}
	const gib = 1 << 30
	days := day(now, gib/2, gib/2) + "," +
		day(now.AddDate(0, 0, -1), gib, gib) + "," +
		day(now.AddDate(0, 0, -10), 7*gib, 0)
	months := month(now.AddDate(0, -1, 0), 5*gib, 0) + "," + month(now, 9*gib, 0)
	iface := func(name string) string {
		return `{"name":"` + name + `","traffic":{"day":[` + days + `],"month":[` + months + `]}}`
	}
	return `{"interfaces":[` + iface("wlan9") + `,` + iface("eth0") + `]}`
}

func TestPmuCovCollectVnstatUsage(t *testing.T) {
	keys := []string{"DailyDataUsage", "WeeklyDataUsage", "LastMonthUsage"}

	t.Run("empty iface", func(t *testing.T) {
		pmuCovStashKeys(t, keys...)
		globalData.Delete("DailyDataUsage")
		collectVnstatUsage("")
		if _, ok := globalData.Load("DailyDataUsage"); ok {
			t.Error("usage stored for an empty interface")
		}
	})

	t.Run("vnstat missing", func(t *testing.T) {
		pmuCovStashKeys(t, keys...)
		globalData.Delete("DailyDataUsage")
		t.Setenv("PATH", pmuCovBinDir(t))
		collectVnstatUsage("eth0")
		if _, ok := globalData.Load("DailyDataUsage"); ok {
			t.Error("usage stored without vnstat installed")
		}
	})

	t.Run("vnstat fails", func(t *testing.T) {
		pmuCovStashKeys(t, keys...)
		globalData.Delete("DailyDataUsage")
		bin := pmuCovBinDir(t)
		pmuCovScript(t, bin, "vnstat", "#!/bin/sh\nexit 1\n")
		pmuCovSetPath(t, bin)
		collectVnstatUsage("eth0")
		if _, ok := globalData.Load("DailyDataUsage"); ok {
			t.Error("usage stored although vnstat failed")
		}
	})

	t.Run("bad json", func(t *testing.T) {
		pmuCovStashKeys(t, keys...)
		globalData.Delete("DailyDataUsage")
		bin := pmuCovBinDir(t)
		fixture := filepath.Join(bin, "vnstat.json")
		pmuCovWriteFile(t, fixture, "not json")
		pmuCovCatScript(t, bin, "vnstat", fixture)
		pmuCovSetPath(t, bin)
		collectVnstatUsage("eth0")
		if _, ok := globalData.Load("DailyDataUsage"); ok {
			t.Error("usage stored from invalid vnstat output")
		}
	})

	t.Run("interface not in output", func(t *testing.T) {
		pmuCovStashKeys(t, keys...)
		globalData.Delete("DailyDataUsage")
		bin := pmuCovBinDir(t)
		fixture := filepath.Join(bin, "vnstat.json")
		pmuCovWriteFile(t, fixture, pmuCovVnstatJSON())
		pmuCovCatScript(t, bin, "vnstat", fixture)
		pmuCovSetPath(t, bin)
		collectVnstatUsage("eth7")
		if _, ok := globalData.Load("DailyDataUsage"); ok {
			t.Error("usage stored for an interface vnstat does not know")
		}
	})

	t.Run("full accounting", func(t *testing.T) {
		pmuCovStashKeys(t, keys...)
		bin := pmuCovBinDir(t)
		fixture := filepath.Join(bin, "vnstat.json")
		pmuCovWriteFile(t, fixture, pmuCovVnstatJSON())
		pmuCovCatScript(t, bin, "vnstat", fixture)
		pmuCovSetPath(t, bin)
		collectVnstatUsage("eth0")
		want := map[string]string{
			"DailyDataUsage":  "1.00",
			"WeeklyDataUsage": "3.00", // today + yesterday; the 10-day-old entry is outside the window
			"LastMonthUsage":  "5.00",
		}
		for k, v := range want {
			if got, _ := globalData.Load(k); got != v {
				t.Errorf("%s = %v; want %v", k, got, v)
			}
		}
	})
}

// ---- the full sweep -------------------------------------------------------

func TestPmuCovCollectLinuxFallbackDataStubbed(t *testing.T) {
	root := t.TempDir()
	net := filepath.Join(root, "net") + string(os.PathSeparator)

	// sysfs fixtures
	pmuCovSwap(t, &pmuCovSysClassNetPath, net)
	pmuCovWriteFile(t, filepath.Join(net, "eth0", "carrier"), "1\n")
	if err := os.MkdirAll(filepath.Join(net, "ap0", "wireless"), 0o755); err != nil {
		t.Fatal(err)
	}
	osRelease := filepath.Join(root, "os-release")
	pmuCovWriteFile(t, osRelease, "ID=debian\nVERSION_ID=\"13\"\n")
	pmuCovSwap(t, &pmuCovOSReleasePath, osRelease)
	model := filepath.Join(root, "model")
	pmuCovWriteFile(t, model, "Photonicat Test\x00")
	pmuCovSwap(t, &pmuCovDTModelPath, model)
	pmuCovSwap(t, &pmuCovHwmonGlob, filepath.Join(root, "hwmon*"))
	pmuCovWriteFile(t, filepath.Join(root, "hwmon0", "name"), "pcat_pm_hwmon_temp_mb\n")
	pmuCovWriteFile(t, filepath.Join(root, "hwmon0", "temp1_input"), "42500\n")
	pmuCovSwap(t, &pmuCovBatteryTempPath, filepath.Join(root, "no-batt"))
	lease := filepath.Join(root, "dnsmasq.leases")
	pmuCovWriteFile(t, lease, "1 a h1\n2 b h2\n3 c h3\n")
	pmuCovSwap(t, &pmuCovDHCPLeaseFiles, []string{lease})
	pmuCovSwap(t, &pmuCovNMLeaseGlob, filepath.Join(root, "nm-*.leases"))
	pmuCovSwap(t, &pmuCovDhcpdLeasePath, filepath.Join(root, "no-dhcpd"))

	// command stubs
	bin := pmuCovBinDir(t)
	routeFix := filepath.Join(bin, "routes.txt")
	pmuCovWriteFile(t, routeFix, "default via 10.0.0.1 dev eth0 metric 100\n")
	pmuCovCatScript(t, bin, "ip", routeFix)
	pmuCovScript(t, bin, "iw", pmuCovIwScript)
	vnstatFix := filepath.Join(bin, "vnstat.json")
	pmuCovWriteFile(t, vnstatFix, pmuCovVnstatJSON())
	pmuCovCatScript(t, bin, "vnstat", vnstatFix)
	listFix := filepath.Join(bin, "list.json")
	pmuCovWriteFile(t, listFix, `{"modem-list":["`+pmuCovModemPath+`"]}`)
	modemFix := filepath.Join(bin, "modem.json")
	pmuCovWriteFile(t, modemFix, pmuCovModemJSON)
	pmuCovScript(t, bin, "mmcli",
		"#!/bin/sh\ncase \"$*\" in\n  -L*) cat "+listFix+" ;;\n  *) cat "+modemFix+" ;;\nesac\n")
	pmuCovSetPath(t, bin)

	keys := []string{"RemainingTimeFromWeb", "ActiveEgress", "GatewayDevice",
		"NetworkModeLabel", "OSVersion", "Model", "BoardTemperature", "SdState",
		"DHCPClientsCount", "WiFiClientsCount", "PublicIP", "PUBLIC_IP",
		"ModemModel", "ModemValid", "ModemFirmwareVer", "IMEINum",
		"ModemSignalStrength", "Carrier", "CellGeneration", "ModemNetworkInfo",
		"ISPName", "CellCarrierInfo", "SimNumber", "SimState",
		"DailyDataUsage", "WeeklyDataUsage", "LastMonthUsage"}
	pmuCovStashKeys(t, keys...)
	pmuCovStashPmuState(t)
	globalData.Delete("OSVersion")
	globalData.Delete("Model")
	globalData.Store("PUBLIC_IP", "9.9.9.9")
	pmuSysfsActive.Store(false)
	pmuUartActive.Store(false)

	collectLinuxFallbackData()

	want := map[string]any{
		"RemainingTimeFromWeb": false,
		"ActiveEgress":         "wan",
		"GatewayDevice":        "wired",
		"NetworkModeLabel":     "Eth",
		"OSVersion":            "Debian 13",
		"Model":                "Photonicat Test",
		"BoardTemperature":     42,
		"DHCPClientsCount":     3,
		"WiFiClientsCount":     2,
		"PublicIP":             "9.9.9.9",
		"ModemModel":           "FM350-GL",
		"SimState":             "Yes",
		"DailyDataUsage":       "1.00",
	}
	for k, v := range want {
		if got, _ := globalData.Load(k); got != v {
			t.Errorf("%s = %v; want %v", k, got, v)
		}
	}
	if v, _ := globalData.Load("SdState"); v != "Yes" && v != "No" {
		t.Errorf("SdState = %v; want Yes or No", v)
	}

	// Second sweep with the PMU active: the MCU owns BoardTemperature, so the
	// fallback must leave it alone; the already-set identity keys are kept.
	pmuSysfsActive.Store(true)
	globalData.Delete("BoardTemperature")
	globalData.Store("OSVersion", "keep-me")
	collectLinuxFallbackData()
	if _, ok := globalData.Load("BoardTemperature"); ok {
		t.Error("fallback overwrote BoardTemperature while the PMU source is active")
	}
	if v, _ := globalData.Load("OSVersion"); v != "keep-me" {
		t.Errorf("OSVersion = %v; the fallback must not replace an existing value", v)
	}
}
