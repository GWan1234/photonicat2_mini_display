package main

import (
	"strings"
	"testing"
	"time"
)

func TestFormatSpeed(t *testing.T) {
	tests := []struct {
		mbps         float64
		expectedVal  string
		expectedUnit string
	}{
		{0.0, "0.00", "Mbps"},
		{0.5, "0.50", "Mbps"},
		{0.123, "0.12", "Mbps"},
		{1.0, "1", "Mbps"},
		{1.23, "1.23", "Mbps"},
		{10.5, "10.5", "Mbps"},
		{100.0, "100", "Mbps"},
		{123.456, "123", "Mbps"},
		{-1.0, "0.00", "Mbps"}, // Negative should be clamped to 0
		{100001.0, "0.00", "Mbps"}, // Too large should be clamped to 0
	}
	
	for _, tt := range tests {
		val, unit := formatSpeed(tt.mbps)
		if val != tt.expectedVal || unit != tt.expectedUnit {
			t.Errorf("formatSpeed(%f) = (%s, %s); want (%s, %s)", 
				tt.mbps, val, unit, tt.expectedVal, tt.expectedUnit)
		}
	}
}

func TestSanitizeCommandArg(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"valid_arg", "valid_arg"},
		{"path/to/file.txt", "path/to/file.txt"},
		{"file-name.ext", "file-name.ext"},
		{"123abc", "123abc"},
		{"arg;dangerous", ""}, // Contains semicolon
		{"rm -rf /", ""}, // Contains spaces
		{"", ""},
		{"arg|pipe", ""}, // Contains pipe
		{"arg&background", ""}, // Contains ampersand
		{"/valid/path", "/valid/path"},
		{"arg with spaces", ""}, // Contains spaces
	}
	
	for _, tt := range tests {
		result := sanitizeCommandArg(tt.input)
		if result != tt.expected {
			t.Errorf("sanitizeCommandArg(%q) = %q; want %q", tt.input, result, tt.expected)
		}
	}
}

func TestGetUptimeSeconds(t *testing.T) {
	// This function reads from /proc/uptime which might not exist in test environment
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("getUptimeSeconds() panicked: %v", r)
		}
	}()
	
	uptime, err := getUptimeSeconds()
	
	// In a real system, uptime should be positive
	// In test environment, it might fail, which is acceptable
	if err == nil && uptime < 0 {
		t.Errorf("Uptime should be non-negative, got %f", uptime)
	}
}

func TestGetUptime(t *testing.T) {
	// This function reads system files, test it doesn't crash
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("getUptime() panicked: %v", r)
		}
	}()
	
	uptimeStr, err := getUptime()
	
	// If successful, should return a non-empty string
	if err == nil && uptimeStr == "" {
		t.Error("Expected non-empty uptime string when no error")
	}
	
	// If successful, should contain time units
	if err == nil && !strings.Contains(uptimeStr, "s") {
		t.Error("Expected uptime string to contain time units")
	}
}

func TestIsOpenWRT(t *testing.T) {
	// This function checks for file existence, test it doesn't crash
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("isOpenWRT() panicked: %v", r)
		}
	}()
	
	result := isOpenWRT()
	
	// Should return a boolean without crashing
	_ = result
}

func TestGetSessionDataUsageGB(t *testing.T) {
	// Test with a likely non-existent interface
	iface := "nonexistent_interface"
	
	_, err := getSessionDataUsageGB(iface)
	
	// Should return an error for non-existent interface
	if err == nil {
		t.Error("Expected error for non-existent interface")
	}
}

func TestReadCPUStats(t *testing.T) {
	// This reads from /proc/stat, test it doesn't crash
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("readCPUStats() panicked: %v", r)
		}
	}()
	
	stats, err := readCPUStats()
	
	// If successful, should return some stats
	if err == nil && len(stats) == 0 {
		t.Error("Expected some CPU stats when no error")
	}
	
	// If successful, stats should have reasonable values
	if err == nil && len(stats) > 0 {
		for i, stat := range stats {
			total := stat.User + stat.Nice + stat.System + stat.Idle + 
					 stat.Iowait + stat.Irq + stat.Softirq + stat.Steal
			if total == 0 {
				t.Errorf("CPU stat %d has zero total time", i)
			}
		}
	}
}

func TestGetWANInterface(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("getWANInterface() panicked: %v", r)
		}
	}()
	
	iface, err := getWANInterface()
	
	// If successful, should return non-empty string
	if err == nil && iface == "" {
		t.Error("Expected non-empty interface name when no error")
	}
}

func TestGetInterfaceBytes(t *testing.T) {
	// Test with loopback interface which usually exists
	iface := "lo"
	
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("getInterfaceBytes() panicked: %v", r)
		}
	}()
	
	rx, tx, err := getInterfaceBytes(iface)
	
	// If successful, bytes should be non-negative
	if err == nil {
		if rx < 0 || tx < 0 {
			t.Errorf("Interface bytes should be non-negative, got rx=%d, tx=%d", rx, tx)
		}
	}
}

func TestGetFanSpeed(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("getFanSpeed() panicked: %v", r)
		}
	}()
	
	speed, err := getFanSpeed()
	
	// If successful, speed should be non-negative
	if err == nil && speed < 0 {
		t.Errorf("Fan speed should be non-negative, got %d", speed)
	}
}

func TestAbsProcessData(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{5, 5},
		{-5, 5},
		{0, 0},
		{-100, 100},
		{100, 100},
		{-1, 1},
	}
	
	for _, tt := range tests {
		result := abs(tt.input)
		if result != tt.expected {
			t.Errorf("abs(%d) = %d; want %d", tt.input, result, tt.expected)
		}
	}
}

func TestGetNetworkSpeed(t *testing.T) {
	// Test with loopback interface
	iface := "lo"
	
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("getNetworkSpeed() panicked: %v", r)
		}
	}()
	
	// This function sleeps for ~1 second, so we test it briefly
	start := time.Now()
	speed, err := getNetworkSpeed(iface)
	elapsed := time.Since(start)
	
	// Should take approximately 1 second
	if err == nil && elapsed < 500*time.Millisecond {
		t.Error("getNetworkSpeed should take at least 500ms")
	}
	
	// If successful, speeds should be non-negative
	if err == nil {
		if speed.UploadMbps < 0 || speed.DownloadMbps < 0 {
			t.Error("Network speeds should be non-negative")
		}
	}
}

func TestGetSN(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("getSN() panicked: %v", r)
		}
	}()
	
	// This reads from hardware-specific files, likely to fail in test environment
	_, err := getSN()
	
	// We just test it doesn't crash, error is expected in test environment
	_ = err
}