package main

import "testing"

func TestClassifyEgress(t *testing.T) {
	cases := []struct {
		dev, egress, conn, label string
	}{
		{"wwan0", "mobile", "mobile", "Cell"},
		{"usb0", "mobile", "mobile", "Cell"},
		{"ppp0", "mobile", "mobile", "Cell"},
		{"wlan0", "wifi", "wifi", "WiFi"},
		{"wlp3s0", "wifi", "wifi", "WiFi"},
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
