package main

// Coverage tests for processData.go, part 2: the exec-backed readers (via the
// pdCovExecOutput seam), the pcat-manager-web HTTP path (via the existing
// redirectLocalHTTP helper) and the top-level collect* sweeps.

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---- secureExecCommand -----------------------------------------------------

func TestPDCovSecureExecCommand(t *testing.T) {
	// Default seam: run a real, universally-present binary.
	out, err := secureExecCommand("echo", "hello")
	if err != nil || strings.TrimSpace(string(out)) != "hello" {
		t.Errorf("echo hello = %q, %v; want hello, nil", out, err)
	}

	if _, err := secureExecCommand("bad;cmd"); err == nil {
		t.Error("shell metacharacters in command: want error")
	}
	if _, err := secureExecCommand(""); err == nil {
		t.Error("empty command: want error")
	}
	if _, err := secureExecCommand("echo", "bad arg"); err == nil {
		t.Error("space in argument: want error")
	}

	// Argument filtering: empties dropped, special forms passed through.
	var calls []string
	pdCovExecCanned(t, map[string]string{
		"fake|default --json wireless.@wifi-iface[0].ssid /dev/null -z plain": "ok",
	}, &calls)
	out, err = secureExecCommand("fake",
		"", "default", "--json", "wireless.@wifi-iface[0].ssid", "/dev/null", "-z", "plain")
	if err != nil || string(out) != "ok" {
		t.Fatalf("filtered exec = %q, %v; want ok, nil (calls: %v)", out, err, calls)
	}
}

// ---- exec-backed readers ---------------------------------------------------

func TestPDCovGetSN(t *testing.T) {
	const headKey = "head|-c 10000 /dev/mmcblk0boot1"

	pdCovExecCanned(t, map[string]string{headKey: `{"sn":"PCAT123"}` + "\x00leftover-junk"}, nil)
	if sn, err := getSN(); err != nil || sn != "PCAT123" {
		t.Errorf("sn key: got %q, %v; want PCAT123, nil", sn, err)
	}

	pdCovExecCanned(t, map[string]string{headKey: `{"machine_sn":"MSN99"}`}, nil)
	if sn, err := getSN(); err != nil || sn != "MSN99" {
		t.Errorf("machine_sn fallback: got %q, %v", sn, err)
	}

	pdCovExecCanned(t, map[string]string{headKey: `{"sn":"","machine_sn":"M2"}`}, nil)
	if sn, err := getSN(); err != nil || sn != "M2" {
		t.Errorf("empty sn falls back to machine_sn: got %q, %v", sn, err)
	}

	pdCovExecCanned(t, map[string]string{headKey: `{"foo":"bar"}`}, nil)
	if _, err := getSN(); err == nil {
		t.Error("no sn keys: want error")
	}

	pdCovExecCanned(t, map[string]string{headKey: "not-json"}, nil)
	if _, err := getSN(); err == nil {
		t.Error("garbage partition: want error")
	}

	pdCovExecCanned(t, map[string]string{}, nil)
	if _, err := getSN(); err == nil {
		t.Error("read failure: want error")
	}
}

func TestPDCovGetKernelDate(t *testing.T) {
	// Debian shape (9 fields, TZ present).
	pdCovExecCanned(t, map[string]string{
		"uname|-v": "#15 SMP PREEMPT Wed Apr 30 17:23:30 JST 2025\n",
	}, nil)
	if d, err := getKernelDate(); err != nil || d != "2025-Apr-30" {
		t.Errorf("debian uname: got %q, %v; want 2025-Apr-30", d, err)
	}

	// OpenWrt shape (8 fields, no TZ).
	pdCovExecCanned(t, map[string]string{
		"uname|-v": "#0 SMP PREEMPT Wed May 14 09:34:38 2025\n",
	}, nil)
	if d, err := getKernelDate(); err != nil || d != "2025-May-14" {
		t.Errorf("openwrt uname: got %q, %v; want 2025-May-14", d, err)
	}

	// Unrecognised shape and exec failure both fall back to the placeholder.
	pdCovExecCanned(t, map[string]string{"uname|-v": "Darwin"}, nil)
	if d, _ := getKernelDate(); d != "unknown-date" {
		t.Errorf("short uname: got %q, want unknown-date", d)
	}
	pdCovExecCanned(t, map[string]string{}, nil)
	if d, err := getKernelDate(); err != nil || d != "unknown-date" {
		t.Errorf("uname failure: got %q, %v; want unknown-date, nil", d, err)
	}
}

const pdCovIwinfoClient = `phy0-ap0  ESSID: "MainAP"
          Mode: Master  Channel: 6
phy1-sta0  ESSID: "Upstream"
          Mode: Client  Channel: 36
`

const pdCovIwinfoIdleSta = `phy1-sta0  ESSID: unknown
          Mode: Client  Channel: 36
`

const pdCovIwinfoClientFirst = `phy1-sta0  ESSID: "Upstream"
          Mode: Client  Channel: 36
phy0-ap0  ESSID: "MainAP"
          Mode: Master  Channel: 6
`

func TestPDCovLiveStaSSID(t *testing.T) {
	pdCovExecCanned(t, map[string]string{"iwinfo|": pdCovIwinfoClient}, nil)
	if got := liveStaSSID(); got != "Upstream" {
		t.Errorf("client last block: got %q, want Upstream", got)
	}

	pdCovExecCanned(t, map[string]string{"iwinfo|": pdCovIwinfoClientFirst}, nil)
	if got := liveStaSSID(); got != "Upstream" {
		t.Errorf("client first block: got %q, want Upstream", got)
	}

	pdCovExecCanned(t, map[string]string{"iwinfo|": pdCovIwinfoIdleSta}, nil)
	if got := liveStaSSID(); got != "" {
		t.Errorf("unassociated sta: got %q, want empty", got)
	}

	pdCovExecCanned(t, map[string]string{}, nil)
	if got := liveStaSSID(); got != "" {
		t.Errorf("iwinfo failure: got %q, want empty", got)
	}
}

func TestPDCovGetOpenWrtStaSSID(t *testing.T) {
	pdCovExecCanned(t, map[string]string{
		"uci|-q show wireless": "wireless.radio0=wifi-device\nwireless.default_radio0.mode='sta'\n",
		"iwinfo|":              pdCovIwinfoClient,
	}, nil)
	if ssid, configured := getOpenWrtStaSSID(); !configured || ssid != "Upstream" {
		t.Errorf("associated sta: got %q, %v; want Upstream, true", ssid, configured)
	}

	pdCovExecCanned(t, map[string]string{
		"uci|-q show wireless": "wireless.default_radio0.mode='ap'\n",
	}, nil)
	if _, configured := getOpenWrtStaSSID(); configured {
		t.Error("no sta iface: want configured=false")
	}

	pdCovExecCanned(t, map[string]string{}, nil)
	if _, configured := getOpenWrtStaSSID(); configured {
		t.Error("uci failure: want configured=false")
	}
}

func TestPDCovGetSSIDOpenWrt(t *testing.T) {
	pdCovSetStr(t, &pdCovOpenwrtReleasePath, pdCovWriteFile(t, t.TempDir(), "rel", "x"))

	// STA relaying an upstream hotspot: report the live SSID.
	pdCovExecCanned(t, map[string]string{
		"uci|-q show wireless": "wireless.default_radio1.mode='sta'\n",
		"iwinfo|":              pdCovIwinfoClient,
	}, nil)
	if ssid, err := getSSID(); err != nil || ssid != "Upstream" {
		t.Errorf("sta associated: got %q, %v; want Upstream", ssid, err)
	}

	// STA configured but not associated: Standby.
	pdCovExecCanned(t, map[string]string{
		"uci|-q show wireless": "wireless.default_radio1.mode='sta'\n",
		"iwinfo|":              pdCovIwinfoIdleSta,
	}, nil)
	if ssid, err := getSSID(); err != nil || ssid != "Standby" {
		t.Errorf("sta standby: got %q, %v; want Standby", ssid, err)
	}

	// Plain AP: read the configured SSID.
	pdCovExecCanned(t, map[string]string{
		"uci|get wireless.@wifi-iface[0].ssid": "MyHomeAP\n",
	}, nil)
	if ssid, err := getSSID(); err != nil || ssid != "MyHomeAP" {
		t.Errorf("plain AP: got %q, %v; want MyHomeAP", ssid, err)
	}

	// uci entirely broken.
	pdCovExecCanned(t, map[string]string{}, nil)
	if _, err := getSSID(); err == nil {
		t.Error("uci failure: want error")
	}
}

func TestPDCovGetSSIDDebian(t *testing.T) {
	pdCovSetStr(t, &pdCovOpenwrtReleasePath, pdCovMissingPath(t))

	pdCovExecCanned(t, map[string]string{"iwgetid|-r": "HomeWifi\n"}, nil)
	if ssid, err := getSSID(); err != nil || ssid != "HomeWifi" {
		t.Errorf("iwgetid: got %q, %v; want HomeWifi", ssid, err)
	}

	// iwgetid empty -> iwconfig fallback.
	pdCovExecCanned(t, map[string]string{
		"iwgetid|-r": "\n",
		"iwconfig|":  "wlan0  IEEE 802.11  ESSID:\"CafeNet\"\n",
	}, nil)
	if ssid, err := getSSID(); err != nil || ssid != "CafeNet" {
		t.Errorf("iwconfig fallback: got %q, %v; want CafeNet", ssid, err)
	}

	// iwconfig reports off/any -> undeterminable.
	pdCovExecCanned(t, map[string]string{
		"iwconfig|": "wlan0  IEEE 802.11  ESSID:\"off/any\"\n",
	}, nil)
	if _, err := getSSID(); err == nil {
		t.Error("off/any: want error")
	}

	pdCovExecCanned(t, map[string]string{}, nil)
	if _, err := getSSID(); err == nil {
		t.Error("no wireless tooling: want error")
	}
}

func TestPDCovGetSSID2(t *testing.T) {
	openwrt := pdCovWriteFile(t, t.TempDir(), "rel", "x")

	// Second iface is the upstream STA and is associated.
	pdCovSetStr(t, &pdCovOpenwrtReleasePath, openwrt)
	pdCovExecCanned(t, map[string]string{
		"uci|-q get wireless.@wifi-iface[1].mode": "sta\n",
		"iwinfo|": pdCovIwinfoClient,
	}, nil)
	if ssid, err := getSSID2(); err != nil || ssid != "Upstream" {
		t.Errorf("sta associated: got %q, %v; want Upstream", ssid, err)
	}

	// STA but idle -> Standby.
	pdCovExecCanned(t, map[string]string{
		"uci|-q get wireless.@wifi-iface[1].mode": "sta\n",
	}, nil)
	if ssid, err := getSSID2(); err != nil || ssid != "Standby" {
		t.Errorf("sta standby: got %q, %v; want Standby", ssid, err)
	}

	// Regular second AP.
	pdCovExecCanned(t, map[string]string{
		"uci|-q get wireless.@wifi-iface[1].mode": "ap\n",
		"uci|get wireless.@wifi-iface[1].ssid":    "SecondAP\n",
	}, nil)
	if ssid, err := getSSID2(); err != nil || ssid != "SecondAP" {
		t.Errorf("second AP: got %q, %v; want SecondAP", ssid, err)
	}

	pdCovExecCanned(t, map[string]string{}, nil)
	if _, err := getSSID2(); err == nil {
		t.Error("uci failure: want error")
	}

	// Debian shape.
	pdCovSetStr(t, &pdCovOpenwrtReleasePath, pdCovMissingPath(t))
	pdCovExecCanned(t, map[string]string{"iwgetid|-r": "HomeWifi\n"}, nil)
	if ssid, err := getSSID2(); err != nil || ssid != "HomeWifi" {
		t.Errorf("debian: got %q, %v; want HomeWifi", ssid, err)
	}
	pdCovExecCanned(t, map[string]string{
		"iwconfig|": "wlan0  IEEE 802.11  ESSID:\"CafeNet\"\n",
	}, nil)
	if ssid, err := getSSID2(); err != nil || ssid != "CafeNet" {
		t.Errorf("debian iwconfig: got %q, %v; want CafeNet", ssid, err)
	}
	pdCovExecCanned(t, map[string]string{}, nil)
	if _, err := getSSID2(); err == nil {
		t.Error("debian, no tooling: want error")
	}
}

func pdCovVnstatJSON(iface string) string {
	now := time.Now()
	return fmt.Sprintf(`{"interfaces":[{"name":%q,"traffic":{"month":[`+
		`{"date":{"year":2000,"month":1},"rx":5,"tx":5},`+
		`{"date":{"year":%d,"month":%d},"rx":1073741824,"tx":1073741824}]}}]}`,
		iface, now.Year(), int(now.Month()))
}

func TestPDCovGetDataUsageMonthlyGB(t *testing.T) {
	// Direct hit on the requested interface: 2 GiB this month.
	pdCovExecCanned(t, map[string]string{
		"vnstat|-i pdcovwan --json": pdCovVnstatJSON("pdcovwan"),
	}, nil)
	if gb, err := getDataUsageMonthlyGB("pdcovwan"); err != nil || gb != 2.0 {
		t.Errorf("direct: got %v, %v; want 2.0, nil", gb, err)
	}

	// First interface unknown to vnstat -> falls back to wwan0.
	pdCovExecCanned(t, map[string]string{
		"vnstat|-i wwan0 --json": pdCovVnstatJSON("wwan0"),
	}, nil)
	if gb, err := getDataUsageMonthlyGB("pdcovnope"); err != nil || gb != 2.0 {
		t.Errorf("wwan0 fallback: got %v, %v; want 2.0, nil", gb, err)
	}

	// Second fallback: br-lan.
	pdCovExecCanned(t, map[string]string{
		"vnstat|-i br-lan --json": pdCovVnstatJSON("br-lan"),
	}, nil)
	if gb, err := getDataUsageMonthlyGB("pdcovnope"); err != nil || gb != 2.0 {
		t.Errorf("br-lan fallback: got %v, %v; want 2.0, nil", gb, err)
	}

	// vnstat completely unavailable.
	pdCovExecCanned(t, map[string]string{}, nil)
	if _, err := getDataUsageMonthlyGB("pdcovnope"); err == nil {
		t.Error("vnstat failure: want error")
	}

	// Unparsable JSON.
	pdCovExecCanned(t, map[string]string{"vnstat|-i pdcovwan --json": "{"}, nil)
	if _, err := getDataUsageMonthlyGB("pdcovwan"); err == nil {
		t.Error("bad JSON: want error")
	}

	// JSON answers but for a different interface name.
	pdCovExecCanned(t, map[string]string{
		"vnstat|-i pdcovwan --json": pdCovVnstatJSON("other0"),
	}, nil)
	if _, err := getDataUsageMonthlyGB("pdcovwan"); err == nil {
		t.Error("interface missing from output: want error")
	}

	// No entry for the current month.
	pdCovExecCanned(t, map[string]string{
		"vnstat|-i pdcovwan --json": `{"interfaces":[{"name":"pdcovwan","traffic":{"month":[` +
			`{"date":{"year":2000,"month":1},"rx":5,"tx":5}]}}]}`,
	}, nil)
	if _, err := getDataUsageMonthlyGB("pdcovwan"); err == nil {
		t.Error("no current-month data: want error")
	}
}

func TestPDCovDHCPClients(t *testing.T) {
	// OpenWrt lease file: "*" and blank lines skipped.
	pdCovExecCanned(t, map[string]string{
		"cat|/tmp/dhcp.leases": "1700000000 aa:bb:cc:dd:ee:ff 192.168.1.100 laptop *\n" +
			"1700000001 aa:bb:cc:dd:ee:00 * ghost *\n" +
			"\n" +
			"1700000002 aa:bb:cc:dd:ee:01 192.168.1.101 phone *\n",
	}, nil)
	clients, err := getDHCPClients()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clients) != 2 || clients[0] != "192.168.1.100" || clients[1] != "192.168.1.101" {
		t.Errorf("openwrt leases: got %v", clients)
	}

	// OpenWrt file missing -> ISC dhcpd blocks on Debian.
	pdCovExecCanned(t, map[string]string{
		"cat|/var/lib/dhcp/dhcpd.leases": "lease 192.168.1.50 {\n  starts 4;\n}\nlease 192.168.1.51 {\n",
	}, nil)
	clients, err = getDHCPClients()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clients) != 2 || clients[0] != "192.168.1.50" {
		t.Errorf("debian leases: got %v", clients)
	}

	// Nothing readable anywhere -> documented dummy fallback.
	pdCovExecCanned(t, map[string]string{}, nil)
	clients, err = getDHCPClients()
	if err != nil || len(clients) != 2 || clients[0] != "192.168.1.100" {
		t.Errorf("dummy fallback: got %v, %v", clients, err)
	}
}

func TestPDCovWifiClients(t *testing.T) {
	// Two radios, one duplicate MAC across them.
	pdCovExecCanned(t, map[string]string{
		"iwinfo|wlan0 assoclist": "00:11:22:33:44:55  -55 dBm / -95 dBm (SNR 40)  0 ms ago\n" +
			"\tRX: 65.0 MBit/s\n" +
			"00:11:22:33:44:66  -60 dBm / -95 dBm (SNR 35)  10 ms ago\n",
		"iwinfo|wlan1 assoclist": "00:11:22:33:44:55  -70 dBm / -95 dBm (SNR 25)  5 ms ago\n",
	}, nil)
	got, err := getWifiClients()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "00:11:22:33:44:55,00:11:22:33:44:66" {
		t.Errorf("assoclist: got %q", got)
	}

	// No associated stations anywhere -> Debian iwconfig AP marker.
	pdCovExecCanned(t, map[string]string{
		"iwconfig|": "wlan0  IEEE 802.11  ESSID:\"X\"\n          Access Point: 00:11:22:33:44:55\n",
	}, nil)
	if got, err := getWifiClients(); err != nil || got != "DEBIAN_WIFI_CLIENT" {
		t.Errorf("iwconfig AP: got %q, %v", got, err)
	}

	// iwconfig without AP -> nmcli active row.
	pdCovExecCanned(t, map[string]string{
		"iwconfig|":         "lo  no wireless extensions.\n",
		"nmcli|device wifi": "*  SSID  ...\n",
	}, nil)
	if got, err := getWifiClients(); err != nil || got != "NMCLI_CONNECTED" {
		t.Errorf("nmcli: got %q, %v", got, err)
	}

	// Everything failing -> documented fallback marker.
	pdCovExecCanned(t, map[string]string{}, nil)
	if got, err := getWifiClients(); err != nil || got != "DEBIAN_FALLBACK" {
		t.Errorf("fallback: got %q, %v", got, err)
	}
}

// ---- WAN / public IP -------------------------------------------------------

const pdCovRouteShowDefault = "default via 10.0.0.1 dev pdcovgw0 metric 100\n"
const pdCovRouteGet = "1.1.1.1 via 10.0.0.1 dev pdcovgw0 src 192.0.2.10 uid 1000\n"

func TestPDCovGetWanIPv4(t *testing.T) {
	// No usable default route.
	pdCovExecCanned(t, map[string]string{}, nil)
	if ip, err := getWanIPv4(); err == nil || ip != "N/A" {
		t.Errorf("offline: got %q, %v; want N/A, error", ip, err)
	}

	// Route exists on a fake netdev -> `ip route get` src fallback.
	pdCovExecCanned(t, map[string]string{
		"ip|route show default": pdCovRouteShowDefault,
		"ip|route get 1.1.1.1":  pdCovRouteGet,
	}, nil)
	if ip, err := getWanIPv4(); err != nil || ip != "192.0.2.10" {
		t.Errorf("src fallback: got %q, %v; want 192.0.2.10", ip, err)
	}

	// Fallback output carries no src.
	pdCovExecCanned(t, map[string]string{
		"ip|route show default": pdCovRouteShowDefault,
		"ip|route get 1.1.1.1":  "1.1.1.1 via 10.0.0.1 dev pdcovgw0\n",
	}, nil)
	if ip, err := getWanIPv4(); err == nil || ip != "N/A" {
		t.Errorf("no src: got %q, %v; want N/A, error", ip, err)
	}

	// Fallback command fails outright.
	pdCovExecCanned(t, map[string]string{
		"ip|route show default": pdCovRouteShowDefault,
	}, nil)
	if ip, err := getWanIPv4(); err == nil || ip != "N/A" {
		t.Errorf("route get failure: got %q, %v; want N/A, error", ip, err)
	}
}

// pdCovIPServer serves a fixed body and counts hits.
func pdCovIPServer(t *testing.T, body string, status int, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// pdCovPointIPLookup points cfg.PublicIPLookup at the given servers.
func pdCovPointIPLookup(t *testing.T, v4URL, v6URL string) {
	t.Helper()
	saved := cfg.PublicIPLookup
	cfg.PublicIPLookup = PublicIPLookup{
		IPv4: []PublicIPSource{{URL: v4URL, Parser: "stdout"}},
		IPv6: []PublicIPSource{{URL: v6URL, Parser: "stdout"}},
	}
	t.Cleanup(func() { cfg.PublicIPLookup = saved })
}

func TestPDCovUpdatePublicIPs(t *testing.T) {
	pdCovSavePublicIPState(t)
	var v4hits atomic.Int32
	v4 := pdCovIPServer(t, "198.51.100.7", http.StatusOK, &v4hits)
	v6 := pdCovIPServer(t, "2001:db8::7", http.StatusOK, nil)
	pdCovPointIPLookup(t, v4.URL, v6.URL)

	flushPublicIPs()
	updatePublicIPs("10.0.0.2", "wan")
	if v := pdCovLoad("PUBLIC_IP"); v != "198.51.100.7" {
		t.Errorf("PUBLIC_IP = %v, want 198.51.100.7", v)
	}
	if v := pdCovLoad("PublicIPv6"); v != "2001:db8::7" {
		t.Errorf("PublicIPv6 = %v, want 2001:db8::7", v)
	}
	if n := v4hits.Load(); n != 1 {
		t.Fatalf("first update: %d fetches, want 1", n)
	}

	// Same basis, fresh cache: no refetch.
	updatePublicIPs("10.0.0.2", "wan")
	if n := v4hits.Load(); n != 1 {
		t.Errorf("cached update refetched (%d hits)", n)
	}

	// WAN IP changed under us: refetch.
	updatePublicIPs("10.0.0.3", "wan")
	if n := v4hits.Load(); n != 2 {
		t.Errorf("basis change: %d hits, want 2", n)
	}

	// A concurrent fetch already in flight: skip.
	publicIPMu.Lock()
	publicIPFetching = true
	publicIPMu.Unlock()
	updatePublicIPs("10.0.0.4", "wan")
	publicIPMu.Lock()
	publicIPFetching = false
	publicIPMu.Unlock()
	if n := v4hits.Load(); n != 2 {
		t.Errorf("in-flight guard: %d hits, want 2", n)
	}

	// Going offline flushes.
	updatePublicIPs("", "")
	if v := pdCovLoad("PUBLIC_IP"); v != "N/A" {
		t.Errorf("offline: PUBLIC_IP = %v, want N/A", v)
	}
}

func TestPDCovUpdatePublicIPsFetchFailure(t *testing.T) {
	pdCovSavePublicIPState(t)
	v4 := pdCovIPServer(t, "oops", http.StatusInternalServerError, nil)
	v6 := pdCovIPServer(t, "oops", http.StatusInternalServerError, nil)
	pdCovPointIPLookup(t, v4.URL, v6.URL)

	flushPublicIPs()
	updatePublicIPs("10.0.0.2", "wan")
	if v := pdCovLoad("PUBLIC_IP"); v != "N/A" {
		t.Errorf("failed fetch: PUBLIC_IP = %v, want N/A", v)
	}
	if v := pdCovLoad("PublicIPv6"); v != "0.0.0.0" {
		t.Errorf("failed fetch: PublicIPv6 = %v, want 0.0.0.0", v)
	}
	// Failure shortens the retry window: lastFetch is backdated so the next
	// attempt is allowed ~30s out instead of the full 15 minutes.
	publicIPMu.Lock()
	last := publicIPLastFetch
	publicIPMu.Unlock()
	if time.Since(last) < 10*time.Minute {
		t.Errorf("retry backoff not applied; lastFetch age %v", time.Since(last))
	}
}

func TestPDCovApplyLocalNetworkIPsOfflineAndLinkStatus(t *testing.T) {
	pdCovSavePublicIPState(t)
	pdCovExecCanned(t, map[string]string{}, nil) // no default route resolvable

	globalData.Store("WAN_IP", "10.9.9.9")
	globalData.Store("PUBLIC_IP", "203.0.113.9")
	collectLinkStatus()

	if v := pdCovLoad("WAN_IP"); v != "N/A" {
		t.Errorf("WAN_IP = %v, want N/A", v)
	}
	if v := pdCovLoad("PUBLIC_IP"); v != "N/A" {
		t.Errorf("PUBLIC_IP = %v, want N/A", v)
	}
	if v := pdCovLoad("ActiveEgress"); v != "" {
		t.Errorf("ActiveEgress = %v, want empty", v)
	}
}

func TestPDCovApplyLocalNetworkIPsOnline(t *testing.T) {
	pdCovSavePublicIPState(t)
	pdCovExecCanned(t, map[string]string{
		"ip|route show default": pdCovRouteShowDefault,
		"ip|route get 1.1.1.1":  pdCovRouteGet,
	}, nil)
	v4 := pdCovIPServer(t, "198.51.100.8", http.StatusOK, nil)
	v6 := pdCovIPServer(t, "2001:db8::8", http.StatusOK, nil)
	pdCovPointIPLookup(t, v4.URL, v6.URL)

	flushPublicIPs()
	applyLocalNetworkIPs()

	if v := pdCovLoad("WAN_IP"); v != "192.0.2.10" {
		t.Errorf("WAN_IP = %v, want 192.0.2.10", v)
	}
	if v := pdCovLoad("PUBLIC_IP"); v != "198.51.100.8" {
		t.Errorf("PUBLIC_IP = %v, want 198.51.100.8", v)
	}
	if v := pdCovLoad("ActiveEgress"); v != "wan" {
		t.Errorf("ActiveEgress = %v, want wan", v)
	}
	if v := pdCovLoad("GatewayDevice"); v != "wired" {
		t.Errorf("GatewayDevice = %v, want wired", v)
	}
}

// ---- pcat-manager-web ------------------------------------------------------

const pdCovDashboardJSON = `{
  "battery_remaining_time": "< 2:05",
  "board_temperature": 42,
  "carrier": "Other",
  "modem_mode": "5G",
  "connection": "wired",
  "active_egress": "wan",
  "network_mode": "auto",
  "network_mode_label": "Eth/5G",
  "wifi_signal_percent": 77,
  "dhcp_clients_count": 3,
  "up_speed": 123.5,
  "down_speed": 456.5,
  "firmware_version": "1.2.3",
  "isp_name": "TestISP",
  "model": "photonicat2",
  "modem_model": "RM500Q-GL",
  "modem_signal_strength": 31,
  "on_charging": true,
  "openwrt_version": "R26.04.1 / r7760-f45d919e58",
  "sd_state": 1,
  "server_location": "JP",
  "sim_state": "ready",
  "sim_number": "12345",
  "uptime": "1d",
  "voltage": 12,
  "wan_ip": "203.0.113.5",
  "wan_ipv6": "2001:db8::5",
  "local_wan_ip": "10.0.0.2",
  "wan_carrier": true,
  "public_ip": "",
  "wifi_clients_count": 2,
  "wifi_interfaces": [
    {"device_type": "Onboard", "ssid": "MainAP", "band": "2g", "exist": true},
    {"device_type": "PCIE", "ssid": "FastAP", "wifi_wan": true,
     "wifi_wan_ssid": "Upstream", "wifi_wan_associated": true}
  ]
}`

const pdCovStatsJSON = `{"today_used": 1073741824, "week_used": 2147483648,
  "month_used": 3221225472, "last_month_used": 4294967296}`

const pdCovBasicJSON = `{"cell_carrier_info": "TestCell", "firmware_version": "MFW1",
  "imei_num": "8675309", "modem_cell_id": "abc", "modem_cell_info": "info",
  "modem_cell_signals": "sig", "modem_isp_details": "details",
  "modem_network_info": "NR5G BAND 41", "modem_roam_pref": "any",
  "modem_serving_info": "serving", "modem_serving_quality": "good",
  "modem_usb_speed": "USB3", "modem_usbnet_mode": "rndis", "modem_valid": true,
  "policy_lte_bands": "1-2", "policy_nr5g_bands": "41", "selected_lte_bands": "1",
  "selected_nr5g_bands": "41", "sms_check_interval": 60, "sms_forward": true,
  "sms_forward_to": "+123", "modem_temperature": {"cpu": 40}}`

// pdCovWebServer serves the three pcat-manager-web endpoints.
func pdCovWebServer(t *testing.T, dashboard, stats, basic string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/dashboard.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(dashboard))
	})
	mux.HandleFunc("/api/v1/data_stats.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(stats))
	})
	mux.HandleFunc("/api/v1/modem/basic.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(basic))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPDCovGetInfoFromPcatWebSuccess(t *testing.T) {
	pdCovSaveWebState(t)
	pdCovSavePublicIPState(t)
	redirectLocalHTTP(t, pdCovWebServer(t, pdCovDashboardJSON, pdCovStatsJSON, pdCovBasicJSON))

	// Cover the "web is back" transition log and the set-once branches.
	pcatWebStateKnown = true
	pcatWebUp.Store(false)
	for _, k := range []string{"FirmwareVersion", "Model", "ModemModel",
		"ModemFirmwareVer", "IMEINum"} {
		globalData.Delete(k)
	}

	getInfoFromPcatWeb()

	checks := map[string]interface{}{
		"BoardTemperature":     42,
		"RemainingTime":        "2:05",
		"RemainingTimeFromWeb": true,
		"CellGeneration":       "5",
		"ModemMode":            "5G",
		"ActiveEgress":         "wan",
		"NetworkModeLabel":     "Eth/5G",
		"WifiSignalPercent":    77,
		"DHCPClientsCount":     3,
		"FirmwareVersion":      "1.2.3",
		"Model":                "photonicat2",
		"ModemModel":           "RM500Q-GL",
		"SdState":              "Yes",
		"SimState":             "Yes",
		"WiFiClientsCount":     2,
		"SSID":                 "MainAP",
		"SSID2":                "Upstream",
		"OSVersion":            "R26.04.1 / r7760",
		"PUBLIC_IP":            "203.0.113.5",
		"WAN_IP":               "10.0.0.2",
		"PublicIPv6":           "2001:db8::5",
		"UpSpeedBps":           123.5,
		"DownSpeedBps":         456.5,
		"DailyDataUsage":       "1.00",
		"WeeklyDataUsage":      "2.00",
		"MonthlyDataUsage":     "3.00",
		"LastMonthUsage":       "4.00",
		"ModemFirmwareVer":     "MFW1",
		"IMEINum":              "8675309",
		"ModemNetworkInfo":     "NR5G B.41",
		"SMSCheckInterval":     60,
		"SMSForward":           true,
	}
	for key, want := range checks {
		if got := pdCovLoad(key); got != want {
			t.Errorf("%s = %v (%T), want %v", key, got, got, want)
		}
	}
	if !pcatWebUp.Load() || !pcatWebProbed.Load() {
		t.Error("web flags not marked up/probed after success")
	}

	// Second pass exercises the value-already-set branches.
	getInfoFromPcatWeb()
	if got := pdCovLoad("Model"); got != "photonicat2" {
		t.Errorf("Model after second pass = %v", got)
	}
}

func TestPDCovGetInfoFromPcatWebMinimalPayload(t *testing.T) {
	pdCovSaveWebState(t)
	pdCovSavePublicIPState(t)
	redirectLocalHTTP(t, pdCovWebServer(t,
		`{"sd_state":0,"sim_state":"absent","wifi_signal_percent":null,"openwrt_version":""}`,
		"{bad-stats-json", "{bad-basic-json"))

	getInfoFromPcatWeb()

	if v := pdCovLoad("SdState"); v != "No" {
		t.Errorf("SdState = %v, want No", v)
	}
	if v := pdCovLoad("SimState"); v != "No" {
		t.Errorf("SimState = %v, want No", v)
	}
	if v := pdCovLoad("WifiSignalPercent"); v != -1 {
		t.Errorf("WifiSignalPercent = %v, want -1", v)
	}
	if v := pdCovLoad("RemainingTimeFromWeb"); v != false {
		t.Errorf("RemainingTimeFromWeb = %v, want false", v)
	}
	if !pcatWebUp.Load() {
		t.Error("dashboard alone succeeded; web must still count as up")
	}
}

func TestPDCovGetInfoFromPcatWebUnavailable(t *testing.T) {
	pdCovSaveWebState(t)
	pdCovSavePublicIPState(t)
	pdCovExecCanned(t, map[string]string{}, nil) // keep the fallback sweep inert

	// Connection refused: server closed before the call.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	redirectLocalHTTPURL(t, url)

	pcatWebStateKnown = false
	getInfoFromPcatWeb()
	if pcatWebUp.Load() || !pcatWebProbed.Load() || !pcatWebStateKnown {
		t.Error("refused connection must mark the web down and probed")
	}

	// Non-200 answer (some other daemon on port 80).
	redirectLocalHTTP(t, pdCovIPServer(t, "not-pcat-web", http.StatusServiceUnavailable, nil))
	pcatWebUp.Store(true) // force the down-transition log branch
	getInfoFromPcatWeb()
	if pcatWebUp.Load() {
		t.Error("503 must mark the web down")
	}

	// Unparsable dashboard body.
	redirectLocalHTTP(t, pdCovIPServer(t, "{nope", http.StatusOK, nil))
	getInfoFromPcatWeb()
	if pcatWebUp.Load() {
		t.Error("bad dashboard JSON must mark the web down")
	}
}

// ---- collectors ------------------------------------------------------------

func TestPDCovCollectBatteryDataChargingFromWeb(t *testing.T) {
	pdCovSaveBatteryGlobals(t)
	pdCovBatteryFixture(t, map[string]string{
		"capacity":    "85\n",
		"status":      "Charging\n",
		"voltage_now": "8400000\n",
		"current_now": "1500000\n",
	})
	pdCovSetStr(t, &pdCovChargerVoltagePath,
		pdCovWriteFile(t, t.TempDir(), "voltage_now", "12100000\n"))

	globalData.Store("RemainingTimeFromWeb", true)
	globalData.Store("RemainingTime", "2:05")
	lastChargingStatus = false
	idleState = STATE_ACTIVE
	cfg.ScreenDimmerTimeOnDCSeconds = 123
	cfg.ScreenDimmerTimeOnBatterySeconds = 45

	collectBatteryData()

	if v := pdCovLoad("BatterySoc"); v != 85 {
		t.Errorf("BatterySoc = %v, want 85", v)
	}
	if v := pdCovLoad("BatteryCharging"); v != true {
		t.Errorf("BatteryCharging = %v, want true", v)
	}
	if idleTimeout != 123*time.Second {
		t.Errorf("idleTimeout = %v, want 123s (DC)", idleTimeout)
	}
	if !lastChargingStatus {
		t.Error("lastChargingStatus not updated on transition")
	}
	if v := pdCovLoad("RemainingTime"); v != "2:05" {
		t.Errorf("web-provided RemainingTime clobbered: %v", v)
	}
	if v := pdCovLoad("BatteryVoltage"); v != "8.40" {
		t.Errorf("BatteryVoltage = %v, want 8.40", v)
	}
	if v := pdCovLoad("BatteryWattage"); v != "12.6" {
		t.Errorf("BatteryWattage = %v, want 12.6", v)
	}
	if v := pdCovLoad("DCVoltage"); v != "12.1" {
		t.Errorf("DCVoltage = %v, want 12.1", v)
	}
}

func TestPDCovCollectBatteryDataAllMissing(t *testing.T) {
	pdCovSaveBatteryGlobals(t)
	missing := pdCovMissingPath(t)
	pdCovSetStr(t, &pdCovBatteryDir, missing)
	pdCovSetStr(t, &pdCovChargerVoltagePath, missing+"/voltage_now")

	globalData.Store("RemainingTimeFromWeb", false)
	lastChargingStatus = true // transition (charging -> not), non-active screen
	idleState = STATE_IDLE
	cfg.ScreenDimmerTimeOnBatterySeconds = 45

	collectBatteryData()

	if v := pdCovLoad("BatterySoc"); v != -1 {
		t.Errorf("BatterySoc = %v, want -1", v)
	}
	if v := pdCovLoad("BatteryCharging"); v != false {
		t.Errorf("BatteryCharging = %v, want false", v)
	}
	if idleTimeout != 45*time.Second {
		t.Errorf("idleTimeout = %v, want 45s (battery)", idleTimeout)
	}
	if v := pdCovLoad("RemainingTime"); v != "" {
		t.Errorf("RemainingTime = %v, want blank slot", v)
	}
	if v := pdCovLoad("BatteryVoltage"); v != "N/A" {
		t.Errorf("BatteryVoltage = %v, want N/A", v)
	}
	if v := pdCovLoad("BatteryCurrent"); v != -9999 {
		t.Errorf("BatteryCurrent = %v, want -9999", v)
	}
	if v := pdCovLoad("DCVoltage"); v != -9999 {
		t.Errorf("DCVoltage = %v, want -9999", v)
	}
}

func TestPDCovCollectBatteryDataComputedFallback(t *testing.T) {
	pdCovSaveBatteryGlobals(t)
	// charge_full pair only: 50% of 6 Ah at 1 A discharge -> 3:00.
	pdCovBatteryFixture(t, map[string]string{
		"capacity":    "50\n",
		"status":      "Discharging\n",
		"voltage_now": "7400000\n",
		"current_now": "1000000\n",
		"charge_full": "6000000\n",
	})
	pdCovSetStr(t, &pdCovChargerVoltagePath,
		pdCovWriteFile(t, t.TempDir(), "voltage_now", "12000000\n"))

	globalData.Store("RemainingTimeFromWeb", false)
	lastChargingStatus = false // no transition branch this time
	cfg.ScreenDimmerTimeOnBatterySeconds = 45

	collectBatteryData()

	if v := pdCovLoad("RemainingTime"); v != "3:00" {
		t.Errorf("RemainingTime = %v, want 3:00", v)
	}
	if v := pdCovLoad("RemainingTime_Unit"); v != "" {
		t.Errorf("RemainingTime_Unit = %v, want empty", v)
	}
}

func TestPDCovCollectPowerDataHighWattage(t *testing.T) {
	pdCovBatteryFixture(t, map[string]string{
		"voltage_now": "20000000\n",
		"current_now": "2500000\n",
	})
	pdCovSetStr(t, &pdCovChargerVoltagePath,
		pdCovWriteFile(t, t.TempDir(), "voltage_now", "12000000\n"))

	collectPowerData()

	if v := pdCovLoad("BatteryWattage"); v != "50" {
		t.Errorf("BatteryWattage = %v, want 50 (no decimal above 20W)", v)
	}
	if v := pdCovLoad("BatteryCurrent"); v != "2.50" {
		t.Errorf("BatteryCurrent = %v, want 2.50", v)
	}
}

func TestPDCovCollectFixedData(t *testing.T) {
	pdCovExecCanned(t, map[string]string{
		"uname|-v":                        "#15 SMP PREEMPT Wed Apr 30 17:23:30 JST 2025\n",
		"head|-c 10000 /dev/mmcblk0boot1": `{"sn":"PCAT777"}`,
	}, nil)
	globalData.Delete("Model")

	collectFixedData()

	if v := pdCovLoad("Kernel"); v != "2025-Apr-30" {
		t.Errorf("Kernel = %v, want 2025-Apr-30", v)
	}
	if v := pdCovLoad("SN"); v != "PCAT777" {
		t.Errorf("SN = %v, want PCAT777", v)
	}

	// With Model already known the device-tree probe is skipped.
	globalData.Store("Model", "photonicat2")
	collectFixedData()
	if v := pdCovLoad("Model"); v != "photonicat2" {
		t.Errorf("Model = %v, want photonicat2", v)
	}
}

func TestPDCovCollectLinuxDataAllGood(t *testing.T) {
	pdCovSaveCPUPrev(t)
	dir := t.TempDir()

	pdCovSetStr(t, &pdCovProcUptimePath, pdCovWriteFile(t, dir, "uptime", "90061.27 1.0\n"))
	pdCovBatteryFixture(t, map[string]string{
		"voltage_now": "8000000\n", "current_now": "1000000\n",
	})
	pdCovSetStr(t, &pdCovChargerVoltagePath, pdCovWriteFile(t, dir, "dc", "12000000\n"))
	pdCovSetStr(t, &pdCovThermalZonePath, pdCovWriteFile(t, dir, "temp", "55123\n"))
	statPath := pdCovWriteFile(t, dir, "stat", pdCovStatV1)
	pdCovSetStr(t, &pdCovProcStatPath, statPath)
	if prev, err := readCPUStats(); err == nil {
		pdCovSetCPUPrev(prev) // skip the priming sleep
	}
	pdCovSetStr(t, &pdCovProcMeminfoPath, pdCovWriteFile(t, dir, "meminfo",
		"MemTotal:       4194304 kB\nMemAvailable:   2097152 kB\n"))
	pdCovWriteFile(t, dir, "hwmon0/fan1_input", "4200\n")
	pdCovSetStr(t, &pdCovHwmonFanGlob, dir+"/hwmon*/fan1_input")
	pdCovSetStr(t, &pdCovProcMountsPath, pdCovWriteFile(t, dir, "mounts",
		"/dev/mmcblk0p7 / ext4 rw 0 0\n"))
	pdCovSetStr(t, &pdCovMmcTypeGlob, dir+"/sys/block/mmcblk*/device/type")

	collectLinuxData(cfg)

	if v := pdCovLoad("Uptime"); v != "1d 1h 1m 1s" {
		t.Errorf("Uptime = %v", v)
	}
	if v := pdCovLoad("CpuTemp"); v != "55.1" {
		t.Errorf("CpuTemp = %v, want 55.1", v)
	}
	if v := pdCovLoad("MemUsage"); v != "2.0/4" {
		t.Errorf("MemUsage = %v, want 2.0/4", v)
	}
	if v, ok := pdCovLoad("MemUsagePercent").(float64); !ok || v < 49 || v > 51 {
		t.Errorf("MemUsagePercent = %v, want ~50", pdCovLoad("MemUsagePercent"))
	}
	if v := pdCovLoad("FanRPM"); v != 4200 {
		t.Errorf("FanRPM = %v, want 4200", v)
	}
	if v := pdCovLoad("DiskData"); v == nil {
		t.Error("DiskData not stored")
	}
	if v := pdCovLoad("CpuUsages"); v == nil {
		t.Error("CpuUsages not stored")
	}
}

func TestPDCovCollectLinuxDataAllBroken(t *testing.T) {
	pdCovSaveCPUPrev(t)
	missing := pdCovMissingPath(t)

	pdCovSetStr(t, &pdCovProcUptimePath, missing)
	pdCovSetStr(t, &pdCovBatteryDir, missing)
	pdCovSetStr(t, &pdCovChargerVoltagePath, missing)
	pdCovSetStr(t, &pdCovThermalZonePath, missing)
	pdCovSetStr(t, &pdCovProcStatPath, missing)
	pdCovSetCPUPrev([]CPUStats{{User: 1, Idle: 1}})
	pdCovSetStr(t, &pdCovProcMeminfoPath, missing)
	pdCovSetStr(t, &pdCovHwmonFanGlob, missing+"/hwmon*/fan1_input")
	pdCovSetStr(t, &pdCovProcMountsPath, missing)
	pdCovSetStr(t, &pdCovMmcTypeGlob, missing+"/mmcblk*/type")

	collectLinuxData(cfg)

	if v := pdCovLoad("Uptime"); v != "N/A" {
		t.Errorf("Uptime = %v, want N/A", v)
	}
	if v := pdCovLoad("CpuTemp"); v != -9999 {
		t.Errorf("CpuTemp = %v, want -9999", v)
	}
	if v := pdCovLoad("CpuUsage"); v != 0 {
		t.Errorf("CpuUsage = %v, want 0", v)
	}
	if v := pdCovLoad("MemUsage"); v != nil {
		t.Errorf("MemUsage = %v, want nil", v)
	}
	if v := pdCovLoad("FanRPM"); v != "N/A" {
		t.Errorf("FanRPM = %v, want N/A (no hwmon, no PMU)", v)
	}
}

func TestPDCovCollectNetworkDataDebian(t *testing.T) {
	pdCovSaveWebState(t)
	pdCovSavePublicIPState(t)
	pdCovSaveWanInterface(t)

	pdCovSetStr(t, &pdCovOpenwrtReleasePath, pdCovMissingPath(t))
	netDir := t.TempDir()
	pdCovSetStr(t, &pdCovSysClassNetDir, netDir)
	pdCovWriteFile(t, netDir, "pdcovnet1/statistics/rx_bytes", "1073741824")
	pdCovWriteFile(t, netDir, "pdcovnet1/statistics/tx_bytes", "1073741824")
	wanInterface = "pdcovnet1"

	pcatWebUp.Store(false)
	pcatWebProbed.Store(false)

	pdCovExecCanned(t, map[string]string{
		"vnstat|-i pdcovnet1 --json": pdCovVnstatJSON("pdcovnet1"),
		"iwgetid|-r":                 "HomeWifi\n",
		"cat|/tmp/dhcp.leases":       "1 aa:bb:cc:dd:ee:ff 192.168.1.100 host *\n",
		"iwinfo|wlan0 assoclist":     "00:11:22:33:44:55  -55 dBm\n",
		// "ip|route show default" intentionally absent -> offline IP path.
	}, nil)

	collectNetworkData(cfg)

	if v := pdCovLoad("SessionDataUsage"); v != "2.0" {
		t.Errorf("SessionDataUsage = %v, want 2.0", v)
	}
	if v := pdCovLoad("MonthlyDataUsage"); v != "2.0" {
		t.Errorf("MonthlyDataUsage = %v, want 2.0", v)
	}
	if v := pdCovLoad("SSID"); v != "HomeWifi" {
		t.Errorf("SSID = %v, want HomeWifi", v)
	}
	if v := pdCovLoad("SSID2"); v != "HomeWifi" {
		t.Errorf("SSID2 = %v, want HomeWifi", v)
	}
	if v, ok := pdCovLoad("LAN_IP").(string); !ok || v == "" {
		t.Errorf("LAN_IP = %v, want a non-empty string", pdCovLoad("LAN_IP"))
	}
	if v, ok := pdCovLoad("DHCPClients").([]string); !ok || len(v) != 1 || v[0] != "192.168.1.100" {
		t.Errorf("DHCPClients = %v", pdCovLoad("DHCPClients"))
	}
	if v := pdCovLoad("WifiClients"); v != "00:11:22:33:44:55" {
		t.Errorf("WifiClients = %v", v)
	}

	// Broken sources: usage errors and SSID fallbacks store the N/A shapes.
	wanInterface = "pdcovgone9"
	pdCovExecCanned(t, map[string]string{}, nil)
	collectNetworkData(cfg)
	if v := pdCovLoad("SessionDataUsage"); v != nil {
		t.Errorf("broken SessionDataUsage = %v, want nil", v)
	}
	if v := pdCovLoad("SSID"); v != "N/A" {
		t.Errorf("broken SSID = %v, want N/A", v)
	}
	if v := pdCovLoad("SSID2"); v != "N/A" {
		t.Errorf("broken SSID2 = %v, want N/A", v)
	}
}

func TestPDCovCollectNetworkDataOpenWrtWebUp(t *testing.T) {
	pdCovSaveWebState(t)
	pdCovSavePublicIPState(t)

	pdCovSetStr(t, &pdCovOpenwrtReleasePath, pdCovWriteFile(t, t.TempDir(), "rel", "x"))
	pcatWebUp.Store(true)
	pcatWebProbed.Store(true)
	pdCovExecCanned(t, map[string]string{}, nil)

	globalData.Store("SessionDataUsage", "sentinel")
	globalData.Store("SSID", "web-ssid")

	collectNetworkData(cfg)

	// OpenWrt + web up: usage comes from the web poller and the SSIDs must not
	// be clobbered by the coarse uci fallback.
	if v := pdCovLoad("SessionDataUsage"); v != "sentinel" {
		t.Errorf("SessionDataUsage = %v, want untouched sentinel", v)
	}
	if v := pdCovLoad("SSID"); v != "web-ssid" {
		t.Errorf("SSID = %v, want untouched web-ssid", v)
	}
}

func TestPDCovCollectWANNetworkSpeed(t *testing.T) {
	pdCovSaveWanInterface(t)
	dir := t.TempDir()
	netDir := t.TempDir()
	pdCovSetStr(t, &pdCovSysClassNetDir, netDir)

	// No WAN interface at all: row is zeroed.
	pdCovSetStr(t, &pdCovProcNetRoutePath, pdCovMissingPath(t))
	pdCovSetStr(t, &pdCovOpenwrtReleasePath, pdCovMissingPath(t))
	collectWANNetworkSpeed()
	if v := pdCovLoad("WanUP"); v != "0.00" {
		t.Errorf("no WAN: WanUP = %v, want 0.00", v)
	}

	// Interface resolves but its counters are unreadable: row is zeroed.
	routes := pdCovRouteHeader +
		"pdcovspd0\t00000000\t0102A8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"
	pdCovSetStr(t, &pdCovProcNetRoutePath, pdCovWriteFile(t, dir, "route", routes))
	collectWANNetworkSpeed()
	if v := pdCovLoad("WanUP"); v != "0.00" {
		t.Errorf("unreadable counters: WanUP = %v, want 0.00", v)
	}
	if wanInterface != "pdcovspd0" {
		t.Errorf("wanInterface = %q, want pdcovspd0", wanInterface)
	}

	// First good sample has no window: seeds zeros only when the key is unset.
	rx := pdCovWriteFile(t, netDir, "pdcovspd0/statistics/rx_bytes", "1000\n")
	tx := pdCovWriteFile(t, netDir, "pdcovspd0/statistics/tx_bytes", "2000\n")
	globalData.Delete("WanUP")
	collectWANNetworkSpeed()
	if v := pdCovLoad("WanUP"); v != "0.00" {
		t.Errorf("seed: WanUP = %v, want 0.00", v)
	}

	// Second sample over a real window publishes a real speed.
	time.Sleep(250 * time.Millisecond)
	pdCovWriteFile(t, netDir, "pdcovspd0/statistics/rx_bytes", "251000\n")
	pdCovWriteFile(t, netDir, "pdcovspd0/statistics/tx_bytes", "127000\n")
	collectWANNetworkSpeed()
	if v, _ := pdCovLoad("WanDOWN").(string); v == "0.00" || v == "" {
		t.Errorf("measured window: WanDOWN = %q, want non-zero", v)
	}
	_ = rx
	_ = tx
}

// ---- residual branches -----------------------------------------------------

// pdCovFindHostV4 returns the name and first IPv4 of a real up interface on
// the test host (loopback allowed only when allowLoopback), or "".
func pdCovFindHostV4(allowLoopback bool) (string, string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", ""
	}
	// Prefer non-eth names: netdevHasCarrier consults sysfs for eth* devices.
	for _, preferNonEth := range []bool{true, false} {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 {
				continue
			}
			if preferNonEth && strings.HasPrefix(iface.Name, "eth") {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				ipnet, ok := addr.(*net.IPNet)
				if !ok {
					continue
				}
				ip4 := ipnet.IP.To4()
				if ip4 == nil {
					continue
				}
				if !allowLoopback && ip4.IsLoopback() {
					continue
				}
				return iface.Name, ip4.String()
			}
		}
	}
	return "", ""
}

func TestPDCovGetLocalIPv4Candidates(t *testing.T) {
	saved := pdCovLocalIPv4Candidates
	t.Cleanup(func() { pdCovLocalIPv4Candidates = saved })

	name, ip := pdCovFindHostV4(true)
	if name == "" {
		t.Skip("no up interface with an IPv4 on this host")
	}
	pdCovLocalIPv4Candidates = []string{"pdcovmissing0", name}
	if got, err := getLocalIPv4(); err != nil || got != ip {
		t.Errorf("getLocalIPv4() = %q, %v; want %q from %s", got, err, ip, name)
	}

	pdCovLocalIPv4Candidates = []string{"pdcovmissing0"}
	if got, err := getLocalIPv4(); err != nil || got != "LINK DOWN" {
		t.Errorf("no candidates present: got %q, %v; want LINK DOWN", got, err)
	}
}

func TestPDCovGetWanIPv4FromRealInterface(t *testing.T) {
	name, ip := pdCovFindHostV4(false)
	if name == "" {
		t.Skip("no up interface with a non-loopback IPv4 on this host")
	}
	pdCovExecCanned(t, map[string]string{
		"ip|route show default": "default via 10.0.0.1 dev " + name + " metric 1\n",
	}, nil)
	got, err := getWanIPv4()
	if err != nil || got != ip {
		t.Errorf("getWanIPv4() = %q, %v; want %q from %s", got, err, ip, name)
	}
}

func TestPDCovGetPublicIPDefaultSources(t *testing.T) {
	// Empty config falls back to the built-in source lists; swap those for
	// loopback servers so no external lookup happens.
	savedLookup := cfg.PublicIPLookup
	saved4, saved6 := defaultPublicIPv4Sources, defaultPublicIPv6Sources
	t.Cleanup(func() {
		cfg.PublicIPLookup = savedLookup
		defaultPublicIPv4Sources, defaultPublicIPv6Sources = saved4, saved6
	})

	v4 := pdCovIPServer(t, "192.0.2.77", http.StatusOK, nil)
	v6 := pdCovIPServer(t, "2001:db8::77", http.StatusOK, nil)
	cfg.PublicIPLookup = PublicIPLookup{}
	defaultPublicIPv4Sources = []PublicIPSource{{URL: v4.URL, Parser: "stdout"}}
	defaultPublicIPv6Sources = []PublicIPSource{{URL: v6.URL, Parser: "stdout"}}

	if got, err := getPublicIPv4(); err != nil || got != "192.0.2.77" {
		t.Errorf("getPublicIPv4() = %q, %v; want 192.0.2.77", got, err)
	}
	if got, err := getIPv6Public(); err != nil || got != "2001:db8::77" {
		t.Errorf("getIPv6Public() = %q, %v; want 2001:db8::77", got, err)
	}
}

func TestPDCovFetchPublicIPEdgeCases(t *testing.T) {
	// Malformed URL: request construction fails before any I/O.
	if _, err := fetchOnePublicIP(PublicIPSource{URL: "://nope"}, "", false); err == nil {
		t.Error("malformed URL: want error")
	}
	// Unreachable loopback port: transport error path.
	if _, err := fetchOnePublicIP(PublicIPSource{URL: "http://127.0.0.1:1/ip"}, "", false); err == nil {
		t.Error("unreachable server: want error")
	}
	// Blank URLs are skipped, later sources still consulted.
	good := pdCovIPServer(t, "192.0.2.88", http.StatusOK, nil)
	sources := []PublicIPSource{{URL: "  "}, {URL: good.URL, Parser: "stdout"}}
	if got, err := fetchPublicIP(sources, "", false); err != nil || got != "192.0.2.88" {
		t.Errorf("blank source skipped: got %q, %v; want 192.0.2.88", got, err)
	}
}

func TestPDCovCollectLinuxDataPmuFanFallback(t *testing.T) {
	pdCovSaveCPUPrev(t)
	missing := pdCovMissingPath(t)
	pdCovSetStr(t, &pdCovProcUptimePath, missing)
	pdCovSetStr(t, &pdCovBatteryDir, missing)
	pdCovSetStr(t, &pdCovChargerVoltagePath, missing)
	pdCovSetStr(t, &pdCovThermalZonePath, missing)
	pdCovSetStr(t, &pdCovProcStatPath, missing)
	pdCovSetCPUPrev([]CPUStats{{User: 1, Idle: 1}})
	pdCovSetStr(t, &pdCovProcMeminfoPath, missing)
	pdCovSetStr(t, &pdCovHwmonFanGlob, missing+"/hwmon*/fan1_input")
	pdCovSetStr(t, &pdCovProcMountsPath, missing)
	pdCovSetStr(t, &pdCovMmcTypeGlob, missing+"/mmcblk*/type")

	// No hwmon fan node, but the direct PMU UART path has a reading.
	savedActive, savedHas := pmuUartActive.Load(), pmuHasFanReading.Load()
	savedRPM := pmuLastFanRPM.Load()
	t.Cleanup(func() {
		pmuUartActive.Store(savedActive)
		pmuHasFanReading.Store(savedHas)
		pmuLastFanRPM.Store(savedRPM)
	})
	pmuUartActive.Store(true)
	pmuHasFanReading.Store(true)
	pmuLastFanRPM.Store(3210)

	collectLinuxData(cfg)

	if v := pdCovLoad("FanRPM"); v != 3210 {
		t.Errorf("FanRPM = %v, want 3210 from the PMU UART fallback", v)
	}
}

// An empty modem_model must not stick. dashboard.json's model/firmware fields
// are "set once" because they are fixed for a running process, but the guard
// keyed on presence rather than on a non-empty value: one early poll answered
// before pcat-manager-web had probed the modem would store "" permanently, and
// the page-3 celldev row then stayed blank for the whole process lifetime
// (draw skips empty values). On OpenWrt this reproduces every boot -- the
// display starts several seconds before pcat-manager-web, and the mmcli
// fallback that would otherwise fill ModemModel does not exist there.
func TestPDCovModemModelEmptyDoesNotStick(t *testing.T) {
	pdCovSaveWebState(t)
	pdCovSavePublicIPState(t)

	blank := strings.Replace(pdCovDashboardJSON,
		`"modem_model": "RM500Q-GL",`, `"modem_model": "",`, 1)
	if blank == pdCovDashboardJSON {
		t.Fatal("fixture no longer contains modem_model; update this test")
	}

	globalData.Delete("ModemModel")

	// Poll 1: the modem is not probed yet, so the web reports an empty model.
	redirectLocalHTTP(t, pdCovWebServer(t, blank, pdCovStatsJSON, pdCovBasicJSON))
	getInfoFromPcatWeb()

	// Poll 2: the modem is up and the web reports it. The display must adopt it.
	redirectLocalHTTP(t, pdCovWebServer(t, pdCovDashboardJSON, pdCovStatsJSON, pdCovBasicJSON))
	getInfoFromPcatWeb()

	got, _ := globalData.Load("ModemModel")
	if got != "RM500Q-GL" {
		t.Errorf("ModemModel = %q, want %q (an empty early value stuck)", got, "RM500Q-GL")
	}
}
