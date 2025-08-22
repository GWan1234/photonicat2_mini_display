package main

import (
	"encoding/json"
	"image"
	"image/color"
	"log"
	"math"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	POWER_DATA_FILE         = "/tmp/pcat2_power_data.json"
	DEFAULT_TIME_FRAME_MINS = 15
	MAX_POWER_SAMPLES       = 900 // 15 minutes at 1 second intervals
	GRAPH_WIDTH             = 80  // Width in pixels
	GRAPH_HEIGHT            = 40  // Height in pixels
)

// PowerSample represents a single power measurement
type PowerSample struct {
	Timestamp time.Time `json:"timestamp"`
	Wattage   float64   `json:"wattage"`
}

// PowerData holds the power history
type PowerData struct {
	Samples       []PowerSample `json:"samples"`
	TimeFrameMins int           `json:"time_frame_mins"`
	mu            sync.RWMutex
}

var (
	powerData = &PowerData{
		Samples:       make([]PowerSample, 0, MAX_POWER_SAMPLES),
		TimeFrameMins: DEFAULT_TIME_FRAME_MINS,
	}
)

// initPowerDataRecording starts the power data recording goroutine
func initPowerDataRecording() {
	// Load existing data if available
	loadPowerData()
	
	// Start recording goroutine
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		
		for weAreRunning {
			select {
			case <-ticker.C:
				recordPowerSample()
			}
		}
	}()
}

// recordPowerSample records current power reading
func recordPowerSample() {
	// Get current battery wattage from global data
	wattageInterface, exists := globalData.Load("BatteryWattage")
	if !exists {
		return
	}
	
	wattageStr, ok := wattageInterface.(string)
	if !ok {
		return
	}
	
	wattage, err := strconv.ParseFloat(wattageStr, 64)
	if err != nil {
		log.Printf("Failed to parse battery wattage: %v", err)
		return
	}
	
	powerData.mu.Lock()
	defer powerData.mu.Unlock()
	
	// Add new sample
	sample := PowerSample{
		Timestamp: time.Now(),
		Wattage:   wattage,
	}
	powerData.Samples = append(powerData.Samples, sample)
	
	// Clean old samples (keep only samples within time frame)
	cutoffTime := time.Now().Add(-time.Duration(powerData.TimeFrameMins) * time.Minute)
	cleanIndex := 0
	for i, s := range powerData.Samples {
		if s.Timestamp.After(cutoffTime) {
			cleanIndex = i
			break
		}
	}
	
	if cleanIndex > 0 {
		powerData.Samples = powerData.Samples[cleanIndex:]
	}
	
	// Limit to MAX_POWER_SAMPLES
	if len(powerData.Samples) > MAX_POWER_SAMPLES {
		powerData.Samples = powerData.Samples[len(powerData.Samples)-MAX_POWER_SAMPLES:]
	}
	
	// Save data periodically (every 10 samples to reduce I/O)
	if len(powerData.Samples)%10 == 0 {
		savePowerData()
	}
}

// loadPowerData loads power data from file
func loadPowerData() {
	file, err := os.Open(POWER_DATA_FILE)
	if err != nil {
		log.Printf("No existing power data file, starting fresh")
		return
	}
	defer file.Close()
	
	decoder := json.NewDecoder(file)
	powerData.mu.Lock()
	defer powerData.mu.Unlock()
	
	if err := decoder.Decode(powerData); err != nil {
		log.Printf("Failed to decode power data: %v", err)
		return
	}
	
	// Clean old samples on load
	cutoffTime := time.Now().Add(-time.Duration(powerData.TimeFrameMins) * time.Minute)
	cleanIndex := 0
	for i, s := range powerData.Samples {
		if s.Timestamp.After(cutoffTime) {
			cleanIndex = i
			break
		}
	}
	
	if cleanIndex > 0 {
		powerData.Samples = powerData.Samples[cleanIndex:]
	}
	
	log.Printf("Loaded %d power samples from cache", len(powerData.Samples))
}

// savePowerData saves power data to file
func savePowerData() {
	file, err := os.Create(POWER_DATA_FILE)
	if err != nil {
		log.Printf("Failed to create power data file: %v", err)
		return
	}
	defer file.Close()
	
	encoder := json.NewEncoder(file)
	powerData.mu.RLock()
	defer powerData.mu.RUnlock()
	
	if err := encoder.Encode(powerData); err != nil {
		log.Printf("Failed to encode power data: %v", err)
	}
}

// drawPowerGraph draws a power graph on the given image at specified position
func drawPowerGraph(img *image.RGBA, x, y, width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	
	powerData.mu.RLock()
	samples := make([]PowerSample, len(powerData.Samples))
	copy(samples, powerData.Samples)
	powerData.mu.RUnlock()
	
	if len(samples) < 2 {
		// Not enough data, draw placeholder
		drawPowerGraphPlaceholder(img, x, y, width, height)
		return
	}
	
	// Calculate data range
	minPower := samples[0].Wattage
	maxPower := samples[0].Wattage
	for _, sample := range samples {
		if sample.Wattage < minPower {
			minPower = sample.Wattage
		}
		if sample.Wattage > maxPower {
			maxPower = sample.Wattage
		}
	}
	
	// Ensure we have a reasonable range including zero
	if minPower > 0 {
		minPower = math.Min(minPower-0.5, -1.0)
	}
	if maxPower < 0 {
		maxPower = math.Max(maxPower+0.5, 1.0)
	}
	powerRange := maxPower - minPower
	if powerRange < 2.0 {
		// Expand range around zero
		center := (maxPower + minPower) / 2
		minPower = center - 1.0
		maxPower = center + 1.0
		powerRange = 2.0
	}
	
	// Draw background with semi-transparent overlay
	bgColor := color.RGBA{0, 0, 0, 80} // Semi-transparent black
	for dy := 0; dy < height; dy++ {
		for dx := 0; dx < width; dx++ {
			if x+dx < img.Bounds().Max.X && y+dy < img.Bounds().Max.Y {
				// Alpha blending
				existing := img.RGBAAt(x+dx, y+dy)
				blended := blendColors(existing, bgColor)
				img.Set(x+dx, y+dy, blended)
			}
		}
	}
	
	// Draw zero axis line
	zeroY := y + int(float64(height)*(maxPower/(powerRange)))
	if zeroY >= y && zeroY < y+height {
		axisColor := color.RGBA{128, 128, 128, 160} // Semi-transparent gray
		for dx := 0; dx < width; dx++ {
			if x+dx < img.Bounds().Max.X {
				img.Set(x+dx, zeroY, axisColor)
			}
		}
	}
	
	// Draw power curve
	timeRange := samples[len(samples)-1].Timestamp.Sub(samples[0].Timestamp)
	if timeRange == 0 {
		timeRange = time.Second
	}
	
	for i := 1; i < len(samples); i++ {
		// Calculate positions for line segment
		t1 := samples[i-1].Timestamp.Sub(samples[0].Timestamp)
		t2 := samples[i].Timestamp.Sub(samples[0].Timestamp)
		
		x1 := x + int(float64(width)*float64(t1)/float64(timeRange))
		x2 := x + int(float64(width)*float64(t2)/float64(timeRange))
		
		y1 := y + int(float64(height)*(maxPower-samples[i-1].Wattage)/powerRange)
		y2 := y + int(float64(height)*(maxPower-samples[i].Wattage)/powerRange)
		
		// Choose color based on power direction
		var lineColor color.RGBA
		avgPower := (samples[i-1].Wattage + samples[i].Wattage) / 2
		if avgPower > 0.1 {
			lineColor = color.RGBA{255, 100, 100, 200} // Red for discharge
		} else if avgPower < -0.1 {
			lineColor = color.RGBA{100, 255, 100, 200} // Green for charge
		} else {
			lineColor = color.RGBA{255, 255, 100, 200} // Yellow for near zero
		}
		
		// Draw line segment
		drawLine(img, x1, y1, x2, y2, lineColor)
	}
}

// drawPowerGraphPlaceholder draws a placeholder when no data is available
func drawPowerGraphPlaceholder(img *image.RGBA, x, y, width, height int) {
	bgColor := color.RGBA{0, 0, 0, 60}
	zeroLineColor := color.RGBA{80, 80, 80, 120}
	
	// Fill background
	for dy := 0; dy < height; dy++ {
		for dx := 0; dx < width; dx++ {
			if x+dx < img.Bounds().Max.X && y+dy < img.Bounds().Max.Y {
				existing := img.RGBAAt(x+dx, y+dy)
				blended := blendColors(existing, bgColor)
				img.Set(x+dx, y+dy, blended)
			}
		}
	}
	
	// Draw centered zero line
	zeroY := y + height/2
	for dx := 0; dx < width; dx++ {
		if x+dx < img.Bounds().Max.X && zeroY < img.Bounds().Max.Y {
			img.Set(x+dx, zeroY, zeroLineColor)
		}
	}
}

// drawLine draws a line between two points using Bresenham's algorithm
func drawLine(img *image.RGBA, x0, y0, x1, y1 int, clr color.RGBA) {
	dx := abs(x1 - x0)
	dy := abs(y1 - y0)
	sx := -1
	if x0 < x1 {
		sx = 1
	}
	sy := -1
	if y0 < y1 {
		sy = 1
	}
	err := dx - dy
	
	for {
		if x0 >= 0 && y0 >= 0 && x0 < img.Bounds().Max.X && y0 < img.Bounds().Max.Y {
			img.Set(x0, y0, clr)
		}
		
		if x0 == x1 && y0 == y1 {
			break
		}
		
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

// blendColors performs alpha blending between two colors
func blendColors(bg, fg color.RGBA) color.RGBA {
	alpha := float64(fg.A) / 255.0
	invAlpha := 1.0 - alpha
	
	return color.RGBA{
		R: uint8(float64(fg.R)*alpha + float64(bg.R)*invAlpha),
		G: uint8(float64(fg.G)*alpha + float64(bg.G)*invAlpha),
		B: uint8(float64(fg.B)*alpha + float64(bg.B)*invAlpha),
		A: uint8(math.Max(float64(bg.A), float64(fg.A))),
	}
}

// abs returns absolute value of an integer
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// setPowerGraphTimeFrame sets the time frame for power data collection
func setPowerGraphTimeFrame(minutes int) {
	if minutes < 1 {
		minutes = 1
	}
	if minutes > 60 {
		minutes = 60
	}
	
	powerData.mu.Lock()
	powerData.TimeFrameMins = minutes
	powerData.mu.Unlock()
	
	log.Printf("Power graph time frame set to %d minutes", minutes)
}