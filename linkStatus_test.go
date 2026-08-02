package main

import "testing"

func TestFlushPublicIPs(t *testing.T) {
	globalData.Store("PUBLIC_IP", "203.0.113.1")
	globalData.Store("PublicIP", "203.0.113.1")
	globalData.Store("PublicIPv6", "2001:db8::1")
	publicIPMu.Lock()
	publicIPWanBasis = "192.168.1.1"
	publicIPEgress = "wan"
	publicIPHadOnline = true
	publicIPLastFetch = publicIPLastFetch.Add(0) // keep whatever
	publicIPMu.Unlock()

	flushPublicIPs()

	if v, _ := globalData.Load("PUBLIC_IP"); v != "N/A" {
		t.Errorf("PUBLIC_IP = %v, want N/A", v)
	}
	if v, _ := globalData.Load("PublicIPv6"); v != "0.0.0.0" {
		t.Errorf("PublicIPv6 = %v, want 0.0.0.0", v)
	}
	publicIPMu.Lock()
	if publicIPHadOnline || publicIPWanBasis != "" || publicIPEgress != "" {
		t.Errorf("cache not cleared: online=%v basis=%q egress=%q",
			publicIPHadOnline, publicIPWanBasis, publicIPEgress)
	}
	publicIPMu.Unlock()
}

func TestApplyWebPublicIP_OfflineFlushes(t *testing.T) {
	globalData.Store("PUBLIC_IP", "203.0.113.1")
	publicIPMu.Lock()
	publicIPHadOnline = true
	publicIPWanBasis = "10.0.0.1"
	publicIPEgress = "wan"
	publicIPMu.Unlock()

	applyWebPublicIP("", "", "", "") // no egress

	if v, _ := globalData.Load("PUBLIC_IP"); v != "N/A" {
		t.Errorf("PUBLIC_IP after offline web payload = %v, want N/A", v)
	}
}

func TestApplyWebPublicIP_AdoptsWanIP(t *testing.T) {
	applyWebPublicIP("198.51.100.9", "2001:db8::9", "10.0.0.5", "wan")
	if v, _ := globalData.Load("PUBLIC_IP"); v != "198.51.100.9" {
		t.Errorf("PUBLIC_IP = %v, want 198.51.100.9", v)
	}
	if v, _ := globalData.Load("WAN_IP"); v != "10.0.0.5" {
		t.Errorf("WAN_IP = %v, want 10.0.0.5", v)
	}
	if v, _ := globalData.Load("PublicIPv6"); v != "2001:db8::9" {
		t.Errorf("PublicIPv6 = %v, want 2001:db8::9", v)
	}
}
