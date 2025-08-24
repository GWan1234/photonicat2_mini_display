package main

import (
	"image"
	"image/color"
	"os"
	"testing"
	"time"
)

func TestAbs(t *testing.T) {
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
		{1, 1},
	}
	
	for _, tt := range tests {
		result := abs(tt.input)
		if result != tt.expected {
			t.Errorf("abs(%d) = %d; want %d", tt.input, result, tt.expected)
		}
	}
}

func TestBlendColors(t *testing.T) {
	tests := []struct {
		name     string
		bg       color.RGBA
		fg       color.RGBA
		expected color.RGBA
	}{
		{
			name:     "fully opaque foreground",
			bg:       color.RGBA{100, 100, 100, 255},
			fg:       color.RGBA{200, 50, 75, 255},
			expected: color.RGBA{200, 50, 75, 255},
		},
		{
			name:     "fully transparent foreground",
			bg:       color.RGBA{100, 100, 100, 255},
			fg:       color.RGBA{200, 50, 75, 0},
			expected: color.RGBA{100, 100, 100, 255},
		},
		{
			name:     "semi-transparent foreground",
			bg:       color.RGBA{0, 0, 0, 255},
			fg:       color.RGBA{255, 255, 255, 128},
			expected: color.RGBA{127, 127, 127, 255}, // Approximately 50% blend
		},
		{
			name:     "both transparent",
			bg:       color.RGBA{100, 100, 100, 128},
			fg:       color.RGBA{200, 200, 200, 128},
			expected: color.RGBA{150, 150, 150, 128}, // Alpha should be max of both
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := blendColors(tt.bg, tt.fg)
			
			// Allow small tolerance due to rounding
			tolerance := uint8(2)
			if abs8(result.R-tt.expected.R) > tolerance ||
				abs8(result.G-tt.expected.G) > tolerance ||
				abs8(result.B-tt.expected.B) > tolerance ||
				abs8(result.A-tt.expected.A) > tolerance {
				t.Errorf("blendColors() = %v, want %v (tolerance: %d)", result, tt.expected, tolerance)
			}
		})
	}
}

func TestSetPowerGraphTimeFrame(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{15, 15},     // Normal case
		{0, 1},       // Below minimum
		{-5, 1},      // Negative
		{30, 30},     // Normal case
		{60, 60},     // Maximum
		{100, 60},    // Above maximum
	}
	
	for _, tt := range tests {
		setPowerGraphTimeFrame(tt.input)
		
		powerData.mu.RLock()
		result := powerData.TimeFrameMins
		powerData.mu.RUnlock()
		
		if result != tt.expected {
			t.Errorf("setPowerGraphTimeFrame(%d): got %d, want %d", tt.input, result, tt.expected)
		}
	}
}

func TestRecordPowerSample(t *testing.T) {
	// Setup test data in globalData
	globalData.Store("BatteryWattage", "1.5")
	
	// Record initial sample count
	powerData.mu.RLock()
	initialCount := len(powerData.Samples)
	powerData.mu.RUnlock()
	
	// Record a sample
	recordPowerSample()
	
	// Check that sample was added
	powerData.mu.RLock()
	newCount := len(powerData.Samples)
	if newCount <= initialCount {
		t.Error("recordPowerSample should add a sample")
	}
	
	// Check the latest sample
	if newCount > 0 {
		latestSample := powerData.Samples[newCount-1]
		if latestSample.Wattage != 1.5 {
			t.Errorf("Latest sample wattage: got %f, want 1.5", latestSample.Wattage)
		}
		
		// Check timestamp is recent
		if time.Since(latestSample.Timestamp) > time.Second {
			t.Error("Sample timestamp should be recent")
		}
	}
	powerData.mu.RUnlock()
	
	// Test with invalid wattage data
	globalData.Store("BatteryWattage", "invalid")
	initialCount = newCount
	
	recordPowerSample()
	
	powerData.mu.RLock()
	finalCount := len(powerData.Samples)
	powerData.mu.RUnlock()
	
	if finalCount != initialCount {
		t.Error("recordPowerSample should not add sample with invalid data")
	}
	
	// Test with missing data
	globalData.Delete("BatteryWattage")
	recordPowerSample()
	
	powerData.mu.RLock()
	countAfterMissing := len(powerData.Samples)
	powerData.mu.RUnlock()
	
	if countAfterMissing != finalCount {
		t.Error("recordPowerSample should not add sample with missing data")
	}
}

func TestLoadPowerData(t *testing.T) {
	// This function reads from file system
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("loadPowerData() panicked: %v", r)
		}
	}()
	
	// Remove test file if it exists
	os.Remove(POWER_DATA_FILE)
	
	loadPowerData()
	
	// Should not crash with missing file
	
	// Test with invalid JSON file
	invalidFile, err := os.Create(POWER_DATA_FILE)
	if err == nil {
		invalidFile.WriteString("invalid json content")
		invalidFile.Close()
		
		loadPowerData() // Should not crash with invalid JSON
		
		// Clean up
		os.Remove(POWER_DATA_FILE)
	}
}

func TestSavePowerData(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("savePowerData() panicked: %v", r)
		}
	}()
	
	// Add some test data
	powerData.mu.Lock()
	powerData.Samples = []PowerSample{
		{Timestamp: time.Now(), Wattage: 1.0},
		{Timestamp: time.Now().Add(-time.Minute), Wattage: 2.0},
	}
	powerData.mu.Unlock()
	
	savePowerData()
	
	// Should not crash
	// Clean up
	os.Remove(POWER_DATA_FILE)
}

func TestDrawLine(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	lineColor := color.RGBA{255, 0, 0, 255}
	
	// Test horizontal line
	drawLine(img, 10, 50, 90, 50, lineColor)
	
	// Check that some pixels along the line have the correct color
	midPoint := img.RGBAAt(50, 50)
	if midPoint != lineColor {
		t.Error("Horizontal line should be drawn with correct color")
	}
	
	// Test vertical line
	drawLine(img, 20, 10, 20, 90, lineColor)
	
	verticalPoint := img.RGBAAt(20, 50)
	if verticalPoint != lineColor {
		t.Error("Vertical line should be drawn with correct color")
	}
	
	// Test diagonal line
	drawLine(img, 0, 0, 50, 50, lineColor)
	
	diagonalPoint := img.RGBAAt(25, 25)
	if diagonalPoint != lineColor {
		t.Error("Diagonal line should be drawn with correct color")
	}
	
	// Test single point (start == end)
	drawLine(img, 75, 75, 75, 75, lineColor)
	
	singlePoint := img.RGBAAt(75, 75)
	if singlePoint != lineColor {
		t.Error("Single point line should be drawn")
	}
}

func TestDrawPowerGraph(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 150))
	
	// Test with no data
	powerData.mu.Lock()
	powerData.Samples = []PowerSample{}
	powerData.mu.Unlock()
	
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("drawPowerGraph() with no data panicked: %v", r)
		}
	}()
	
	drawPowerGraph(img, 50, 50, 80, 40)
	
	// Test with insufficient data (less than 2 samples)
	powerData.mu.Lock()
	powerData.Samples = []PowerSample{
		{Timestamp: time.Now(), Wattage: 1.0},
	}
	powerData.mu.Unlock()
	
	drawPowerGraph(img, 50, 50, 80, 40)
	
	// Test with valid data
	now := time.Now()
	powerData.mu.Lock()
	powerData.Samples = []PowerSample{
		{Timestamp: now.Add(-3 * time.Minute), Wattage: -1.0}, // Charging
		{Timestamp: now.Add(-2 * time.Minute), Wattage: 0.5},  // Discharging
		{Timestamp: now.Add(-1 * time.Minute), Wattage: -0.5}, // Charging
		{Timestamp: now, Wattage: 1.5},                        // Discharging
	}
	powerData.mu.Unlock()
	
	drawPowerGraph(img, 10, 10, 100, 60)
	
	// Should complete without crashing
	
	// Test with zero dimensions
	drawPowerGraph(img, 50, 50, 0, 0)
	drawPowerGraph(img, 50, 50, -10, -10)
}

func TestDrawPowerGraphPlaceholder(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("drawPowerGraphPlaceholder() panicked: %v", r)
		}
	}()
	
	drawPowerGraphPlaceholder(img, 10, 10, 50, 30)
	
	// Should complete without error
	// Could verify that some pixels were drawn, but that's hard to test accurately
}

func TestInitPowerDataRecording(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("initPowerDataRecording() panicked: %v", r)
		}
	}()
	
	// This function starts a goroutine, test it doesn't crash immediately
	initPowerDataRecording()
	
	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)
	
	// Should not crash
}

func TestPowerSampleStruct(t *testing.T) {
	// Test PowerSample struct creation
	now := time.Now()
	sample := PowerSample{
		Timestamp: now,
		Wattage:   2.5,
	}
	
	if sample.Timestamp != now {
		t.Error("PowerSample timestamp not set correctly")
	}
	
	if sample.Wattage != 2.5 {
		t.Error("PowerSample wattage not set correctly")
	}
}

func TestPowerDataStruct(t *testing.T) {
	// Test PowerData struct
	pd := &PowerData{
		Samples:       make([]PowerSample, 0),
		TimeFrameMins: 30,
	}
	
	if pd.TimeFrameMins != 30 {
		t.Error("PowerData TimeFrameMins not set correctly")
	}
	
	if len(pd.Samples) != 0 {
		t.Error("PowerData Samples should be empty initially")
	}
	
	// Test adding samples
	sample := PowerSample{
		Timestamp: time.Now(),
		Wattage:   1.0,
	}
	pd.Samples = append(pd.Samples, sample)
	
	if len(pd.Samples) != 1 {
		t.Error("PowerData should have 1 sample after append")
	}
}

// Helper function for uint8 absolute difference
func abs8(x uint8) uint8 {
	return x // Since uint8 is always positive, this is for comparison tolerance
}