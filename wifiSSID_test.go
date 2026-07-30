package main

import "testing"

// radioDisplaySSID maps pcat-manager-web's per-radio wifi_interfaces to the
// page-3 "SSID - Onboard" / "SSID - PCIE" rows. The WAN priority table lets
// either radio carry the upstream STA, so the row must follow device_type, and
// a relaying radio must show its live upstream (or "Standby"), never the stale
// configured name.
func TestRadioDisplaySSID(t *testing.T) {
	onboardAP := WiFiInterface{DeviceType: "Onboard", SSID: "photonicat-2g"}
	builtinAP := WiFiInterface{DeviceType: "Builtin", SSID: "photonicat-builtin"}
	pcieAP := WiFiInterface{DeviceType: "PCIE", SSID: "photonicat-5g"}

	onboardRelayUp := WiFiInterface{
		DeviceType: "Onboard", SSID: "photonicat-2g",
		WiFiWan: true, WiFiWanAssociated: true, WiFiWanSSID: "HomeHotspot",
	}
	onboardRelayStandby := WiFiInterface{
		DeviceType: "Onboard", SSID: "photonicat-2g",
		WiFiWan: true, WiFiWanAssociated: false, WiFiWanSSID: "HomeHotspot",
	}
	pcieRelayUp := WiFiInterface{
		DeviceType: "PCIE", SSID: "photonicat-5g",
		WiFiWan: true, WiFiWanAssociated: true, WiFiWanSSID: "CafeWiFi",
	}
	pcieRelayStandby := WiFiInterface{
		DeviceType: "PCIE", SSID: "photonicat-5g",
		WiFiWan: true, WiFiWanAssociated: false, WiFiWanSSID: "CafeWiFi",
	}

	cases := []struct {
		name    string
		ifaces  []WiFiInterface
		wantPCI bool
		want    string
	}{
		{"onboard AP", []WiFiInterface{onboardAP, pcieAP}, false, "photonicat-2g"},
		{"pcie AP", []WiFiInterface{onboardAP, pcieAP}, true, "photonicat-5g"},
		{"builtin counts as onboard", []WiFiInterface{builtinAP, pcieAP}, false, "photonicat-builtin"},
		{"onboard relaying, associated shows upstream", []WiFiInterface{onboardRelayUp, pcieAP}, false, "HomeHotspot"},
		{"onboard relaying, not associated shows standby", []WiFiInterface{onboardRelayStandby, pcieAP}, false, "Standby"},
		{"pcie relaying, associated shows upstream", []WiFiInterface{onboardAP, pcieRelayUp}, true, "CafeWiFi"},
		{"pcie relaying, not associated shows standby", []WiFiInterface{onboardAP, pcieRelayStandby}, true, "Standby"},
		{"onboard row unaffected when pcie relays", []WiFiInterface{onboardAP, pcieRelayStandby}, false, "photonicat-2g"},
		{"missing onboard radio", []WiFiInterface{pcieAP}, false, "N/A"},
		{"missing pcie radio", []WiFiInterface{onboardAP}, true, "N/A"},
		{"empty list", nil, false, "N/A"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := radioDisplaySSID(tc.ifaces, tc.wantPCI)
			if got != tc.want {
				t.Errorf("radioDisplaySSID(wantPCIe=%v) = %q, want %q", tc.wantPCI, got, tc.want)
			}
		})
	}
}
