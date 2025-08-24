package main

import (
	"image"
	"image/color"
	"testing"
)

func TestClearFrame(t *testing.T) {
	width, height := 172, 32
	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	
	// Set a non-black pixel first
	frame.Set(10, 10, color.RGBA{255, 255, 255, 255})
	
	clearFrame(frame, width, height)
	
	// Check that the frame is cleared to black
	c := frame.RGBAAt(10, 10)
	if c.R != 0 || c.G != 0 || c.B != 0 || c.A != 255 {
		t.Errorf("Expected black pixel (0,0,0,255), got (%d,%d,%d,%d)", c.R, c.G, c.B, c.A)
	}
}

func TestMax(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{5, 3, 5},
		{2, 8, 8},
		{-5, -3, -3},
		{0, 0, 0},
		{100, -50, 100},
	}
	
	for _, tt := range tests {
		result := max(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("max(%d, %d) = %d; want %d", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestSetBacklight(t *testing.T) {
	// Test that the function doesn't panic with valid inputs
	testCases := []int{0, 50, 100, 150, -10}
	
	for _, brightness := range testCases {
		// This function writes to system files, so we just test it doesn't panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("setBacklight(%d) panicked: %v", brightness, r)
			}
		}()
		
		// Note: This will likely fail in test environment without the actual hardware files
		// but we test the logic doesn't crash
		setBacklight(brightness)
	}
}

func TestStateName(t *testing.T) {
	tests := []struct {
		state    int
		expected string
	}{
		{STATE_FADE_IN, "FADE_IN"},
		{STATE_ACTIVE, "ACTIVE"},
		{STATE_FADE_OUT, "FADE_OUT"},
		{STATE_IDLE, "IDLE"},
		{STATE_OFF, "OFF"},
		{-999, "UNKNOWN"},
		{999, "UNKNOWN"},
	}
	
	for _, tt := range tests {
		result := stateName(tt.state)
		if result != tt.expected {
			t.Errorf("stateName(%d) = %s; want %s", tt.state, result, tt.expected)
		}
	}
}

func TestContainsChinese(t *testing.T) {
	tests := []struct {
		text     string
		expected bool
	}{
		{"Hello World", false},
		{"测试", true},
		{"Test 测试 Text", true},
		{"こんにちは", true}, // Japanese Hiragana
		{"안녕하세요", true},   // Korean
		{"", false},
		{"123456", false},
		{"English中文", true},
	}
	
	for _, tt := range tests {
		result := containsChinese(tt.text)
		if result != tt.expected {
			t.Errorf("containsChinese(%s) = %t; want %t", tt.text, result, tt.expected)
		}
	}
}

func TestPreCalculateEasing(t *testing.T) {
	numFrames := 10
	frameWidth := 100
	
	result := preCalculateEasing(numFrames, frameWidth)
	
	if len(result) != numFrames {
		t.Errorf("Expected %d values, got %d", numFrames, len(result))
	}
	
	// Check that values are monotonically increasing
	for i := 1; i < len(result); i++ {
		if result[i] < result[i-1] {
			t.Errorf("Values should be monotonically increasing, but result[%d]=%d < result[%d]=%d", 
				i, result[i], i-1, result[i-1])
		}
	}
	
	// First value should be 0, last should be close to frameWidth
	if result[0] != 0 {
		t.Errorf("First value should be 0, got %d", result[0])
	}
	
	if result[numFrames-1] < frameWidth-10 {
		t.Errorf("Last value should be close to %d, got %d", frameWidth, result[numFrames-1])
	}
}

func TestHasShowSmsInUserConfig(t *testing.T) {
	// This function reads from file system, so we just test it doesn't crash
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("hasShowSmsInUserConfig() panicked: %v", r)
		}
	}()
	
	result := hasShowSmsInUserConfig()
	
	// The result can be true or false depending on file existence/content
	// We just verify the function returns a boolean without crashing
	_ = result
}

func TestGetFrameBufferUtils(t *testing.T) {
	width, height := 100, 50
	
	buf1 := GetFrameBuffer(width, height)
	if buf1 == nil {
		t.Error("GetFrameBuffer returned nil")
	}
	
	if buf1.Bounds().Dx() != width || buf1.Bounds().Dy() != height {
		t.Errorf("Buffer dimensions: got %dx%d, want %dx%d", 
			buf1.Bounds().Dx(), buf1.Bounds().Dy(), width, height)
	}
	
	// Return buffer and get another one
	ReturnFrameBuffer(buf1)
	
	buf2 := GetFrameBuffer(width, height)
	if buf2 == nil {
		t.Error("GetFrameBuffer returned nil after ReturnFrameBuffer")
	}
}

func TestReturnFrameBufferUtils(t *testing.T) {
	width, height := 50, 25
	
	buf := GetFrameBuffer(width, height)
	
	// Set a pixel to verify clearing
	buf.Set(10, 10, color.RGBA{255, 0, 0, 255})
	
	// This should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ReturnFrameBuffer panicked: %v", r)
		}
	}()
	
	ReturnFrameBuffer(buf)
}