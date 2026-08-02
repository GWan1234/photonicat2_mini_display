package main

import "testing"

func TestClassifyEgress(t *testing.T) {
	cases := []struct {
		dev, egress, conn, label string
	}{
		{"wwan0", "mobile", "mobile", "Cell"},
		{"usb0", "mobile", "mobile", "Cell"},
		// PPPoE over the wired WAN is still the WAN slot (matches
		// pcat-manager-web get_active_egress), not cellular.
		{"ppp0", "wan", "wired", "Eth"},
		{"pppoe-wan", "wan", "wired", "Eth"},
		{"wlan0", "wifi", "wifi", "WiFi"},
		{"wlp3s0", "wifi", "wifi", "WiFi"},
		{"phy0-sta0", "wifi", "wifi", "WiFi"},
		{"eth0", "wan", "wired", "Eth"},
		{"end1", "wan", "wired", "Eth"},
		{"br-lan", "wan", "wired", "Eth"},
		{"", "", "", "-"},
	}
	for _, c := range cases {
		e, conn, l := classifyEgress(c.dev)
		if e != c.egress || conn != c.conn || l != c.label {
			t.Errorf("classifyEgress(%q) = %q,%q,%q; want %q,%q,%q",
				c.dev, e, conn, l, c.egress, c.conn, c.label)
		}
	}
}

func TestAccessTechsToGeneration(t *testing.T) {
	cases := []struct {
		techs []string
		want  string
	}{
		{[]string{"5gnr"}, "5G"},
		{[]string{"lte", "5gnr"}, "5G"},
		{[]string{"lte"}, "4G"},
		{[]string{"hspa", "umts"}, "3G"},
		{[]string{"edge"}, "2G"},
		{[]string{"pots"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := accessTechsToGeneration(c.techs); got != c.want {
			t.Errorf("accessTechsToGeneration(%v) = %q; want %q", c.techs, got, c.want)
		}
	}
}

// TestCollectLinuxFallbackDataNoPanic makes sure a full fallback sweep
// degrades gracefully on machines without pcat hardware, mmcli or vnstat.
func TestCollectLinuxFallbackDataNoPanic(t *testing.T) {
	collectLinuxFallbackData()
	if _, ok := globalData.Load("SdState"); !ok {
		t.Error("SdState should always be set by the fallback sweep")
	}
}

func TestFormatOpenWrtVersionLabel(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"R26.04.1 / r7760-f45d919e58", "R26.04.1 / r7760"},
		{"R25.02.0 / r7748-d1ccd1687", "R25.02 / r7748"},
		{"R25.02 / r7748", "R25.02 / r7748"},
		{"photonicatWrt 26.04.1", "photonicatWrt 26.04.1"}, // no slash shape
		{"", ""},
		{"  R26.04.1 / r7760-abc  ", "R26.04.1 / r7760"},
	}
	for _, c := range cases {
		if got := formatOpenWrtVersionLabel(c.in); got != c.want {
			t.Errorf("formatOpenWrtVersionLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatOSVersionFromOSRelease(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			"debian 13",
			`PRETTY_NAME="Debian GNU/Linux 13 (trixie)"
NAME="Debian GNU/Linux"
VERSION_ID="13"
ID=debian
`,
			"Debian 13",
		},
		{
			"debian 12",
			`PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
VERSION_ID="12"
ID=debian
`,
			"Debian 12",
		},
		{
			"debian 13.1 major only",
			`ID=debian
VERSION_ID="13.1"
`,
			"Debian 13",
		},
		{
			"photonicatWrt with BUILD_ID",
			`NAME="photonicatWrt"
VERSION="26.04.1"
ID="photonicatwrt"
ID_LIKE="lede openwrt"
PRETTY_NAME="photonicatWrt 26.04.1"
VERSION_ID="26.04.1"
BUILD_ID="r7760-f45d919e58"
OPENWRT_RELEASE="photonicatWrt 26.04.1 r7760-f45d919e58"
`,
			"R26.04.1 / r7760",
		},
		{
			"openwrt R-prefixed VERSION_ID",
			`ID=openwrt
VERSION_ID="R25.02.0"
BUILD_ID="r7748-d1ccd1687"
PRETTY_NAME="OpenWrt 25.02.0"
`,
			"R25.02 / r7748",
		},
		{
			"ubuntu pretty name",
			`PRETTY_NAME="Ubuntu 24.04.1 LTS"
ID=ubuntu
VERSION_ID="24.04"
`,
			"Ubuntu 24.04.1 LTS",
		},
	}
	for _, c := range cases {
		if got := formatOSVersionFromOSRelease(c.in); got != c.want {
			t.Errorf("%s: formatOSVersionFromOSRelease(...) = %q, want %q", c.name, got, c.want)
		}
	}
}
