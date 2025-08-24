package main

import (
	"image"
	"image/color"
	"testing"
	"time"
)

func TestGetFrameBuffer(t *testing.T) {
	width, height := 100, 50
	
	// Test getting a new buffer
	buf1 := GetFrameBuffer(width, height)
	if buf1 == nil {
		t.Fatal("GetFrameBuffer returned nil")
	}
	
	// Check dimensions
	bounds := buf1.Bounds()
	if bounds.Dx() != width || bounds.Dy() != height {
		t.Errorf("Buffer dimensions: got %dx%d, want %dx%d", 
			bounds.Dx(), bounds.Dy(), width, height)
	}
	
	// Test getting another buffer with different dimensions
	width2, height2 := 200, 100
	buf2 := GetFrameBuffer(width2, height2)
	if buf2 == nil {
		t.Fatal("GetFrameBuffer returned nil for different dimensions")
	}
	
	bounds2 := buf2.Bounds()
	if bounds2.Dx() != width2 || bounds2.Dy() != height2 {
		t.Errorf("Buffer 2 dimensions: got %dx%d, want %dx%d", 
			bounds2.Dx(), bounds2.Dy(), width2, height2)
	}
	
	// Buffers should be different objects
	if buf1 == buf2 {
		t.Error("Different sized buffers should be different objects")
	}
	
	// Test with zero dimensions (edge case)
	buf3 := GetFrameBuffer(0, 0)
	if buf3 == nil {
		t.Error("GetFrameBuffer should handle zero dimensions")
	}
}

func TestReturnFrameBuffer(t *testing.T) {
	width, height := 50, 25
	
	// Get a buffer and modify it
	buf := GetFrameBuffer(width, height)
	buf.Set(10, 10, image.Black)
	
	// Return it to the pool
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ReturnFrameBuffer panicked: %v", r)
		}
	}()
	
	ReturnFrameBuffer(buf)
	
	// Get another buffer (might be the same one from pool)
	buf2 := GetFrameBuffer(width, height)
	if buf2 == nil {
		t.Fatal("GetFrameBuffer returned nil after ReturnFrameBuffer")
	}
	
	// Check that the returned buffer was cleared
	pixel := buf2.RGBAAt(10, 10)
	if pixel.R != 0 || pixel.G != 0 || pixel.B != 0 || pixel.A != 255 {
		t.Error("Returned buffer should be cleared to black")
	}
}

func TestFrameBufferPool(t *testing.T) {
	// Test that the pool works correctly
	width, height := PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT
	
	// Get multiple buffers
	buffers := make([]*image.RGBA, 5)
	for i := range buffers {
		buffers[i] = GetFrameBuffer(width, height)
		if buffers[i] == nil {
			t.Fatalf("GetFrameBuffer returned nil at index %d", i)
		}
	}
	
	// Return all buffers
	for _, buf := range buffers {
		ReturnFrameBuffer(buf)
	}
	
	// Get buffers again - should reuse from pool
	for i := range buffers {
		buffers[i] = GetFrameBuffer(width, height)
		if buffers[i] == nil {
			t.Fatalf("GetFrameBuffer returned nil after pool return at index %d", i)
		}
	}
}

func TestImageBuffer(t *testing.T) {
	// Test ImageBuffer struct
	width, height := 10, 5
	ib := ImageBuffer{
		buffer: make([]color.RGBA, width*height),
		width:  width,
		height: height,
		loaded: false,
	}
	
	if ib.width != width {
		t.Errorf("ImageBuffer width: got %d, want %d", ib.width, width)
	}
	
	if ib.height != height {
		t.Errorf("ImageBuffer height: got %d, want %d", ib.height, height)
	}
	
	if ib.loaded != false {
		t.Error("ImageBuffer should be initially not loaded")
	}
	
	if len(ib.buffer) != width*height {
		t.Errorf("ImageBuffer buffer length: got %d, want %d", len(ib.buffer), width*height)
	}
}

func TestPosition(t *testing.T) {
	// Test Position struct
	pos := Position{X: 10, Y: 20}
	
	if pos.X != 10 {
		t.Errorf("Position X: got %d, want 10", pos.X)
	}
	
	if pos.Y != 20 {
		t.Errorf("Position Y: got %d, want 20", pos.Y)
	}
}

func TestSize(t *testing.T) {
	// Test Size struct
	size := Size{Width: 100, Height: 200}
	
	if size.Width != 100 {
		t.Errorf("Size Width: got %d, want 100", size.Width)
	}
	
	if size.Height != 200 {
		t.Errorf("Size Height: got %d, want 200", size.Height)
	}
}

func TestDisplayElement(t *testing.T) {
	// Test DisplayElement struct
	pos := Position{X: 5, Y: 10}
	size := Size{Width: 50, Height: 25}
	
	element := DisplayElement{
		Type:     "text",
		Label:    "Test Label",
		Position: pos,
		Font:     "reg",
		Color:    []int{255, 255, 255},
		Units:    "V",
		DataKey:  "voltage",
		Enable:   1,
		Size:     &size,
	}
	
	if element.Type != "text" {
		t.Error("DisplayElement Type not set correctly")
	}
	
	if element.Position.X != 5 || element.Position.Y != 10 {
		t.Error("DisplayElement Position not set correctly")
	}
	
	if element.Size.Width != 50 || element.Size.Height != 25 {
		t.Error("DisplayElement Size not set correctly")
	}
	
	if len(element.Color) != 3 {
		t.Error("DisplayElement Color should have 3 components")
	}
}

func TestGraphConfig(t *testing.T) {
	// Test GraphConfig struct
	gc := GraphConfig{
		GraphType:     "power",
		TimeFrameMins: 30,
	}
	
	if gc.GraphType != "power" {
		t.Errorf("GraphConfig GraphType: got %s, want power", gc.GraphType)
	}
	
	if gc.TimeFrameMins != 30 {
		t.Errorf("GraphConfig TimeFrameMins: got %d, want 30", gc.TimeFrameMins)
	}
}

func TestDisplayTemplate(t *testing.T) {
	// Test DisplayTemplate struct
	element := DisplayElement{
		Type: "text",
		Label: "Test",
	}
	
	template := DisplayTemplate{
		Elements: map[string][]DisplayElement{
			"page0": {element},
		},
	}
	
	if len(template.Elements) != 1 {
		t.Error("DisplayTemplate should have 1 page")
	}
	
	if len(template.Elements["page0"]) != 1 {
		t.Error("Page0 should have 1 element")
	}
	
	if template.Elements["page0"][0].Type != "text" {
		t.Error("Element type not preserved in DisplayTemplate")
	}
}

func TestConfig(t *testing.T) {
	// Test Config struct
	template := DisplayTemplate{
		Elements: make(map[string][]DisplayElement),
	}
	
	config := Config{
		ScreenDimmerTimeOnBatterySeconds: 30,
		ScreenDimmerTimeOnDCSeconds:      300,
		ScreenMaxBrightness:              100,
		ScreenMinBrightness:              10,
		PingSite0:                        "8.8.8.8",
		PingSite1:                        "1.1.1.1",
		DisplayTemplate:                  template,
		ShowSms:                          true,
	}
	
	if config.ScreenMaxBrightness != 100 {
		t.Error("Config ScreenMaxBrightness not set correctly")
	}
	
	if config.PingSite0 != "8.8.8.8" {
		t.Error("Config PingSite0 not set correctly")
	}
	
	if !config.ShowSms {
		t.Error("Config ShowSms not set correctly")
	}
}

func TestFontConfig(t *testing.T) {
	// Test FontConfig struct
	fc := FontConfig{
		FontPath: "/path/to/font.ttf",
		FontSize: 14.0,
	}
	
	if fc.FontPath != "/path/to/font.ttf" {
		t.Error("FontConfig FontPath not set correctly")
	}
	
	if fc.FontSize != 14.0 {
		t.Error("FontConfig FontSize not set correctly")
	}
}

func TestConstants(t *testing.T) {
	// Test that constants are defined with expected values
	if PCAT2_LCD_WIDTH != 172 {
		t.Errorf("PCAT2_LCD_WIDTH: got %d, want 172", PCAT2_LCD_WIDTH)
	}
	
	if PCAT2_LCD_HEIGHT != 320 {
		t.Errorf("PCAT2_LCD_HEIGHT: got %d, want 320", PCAT2_LCD_HEIGHT)
	}
	
	if PCAT2_TOP_BAR_HEIGHT != 32 {
		t.Errorf("PCAT2_TOP_BAR_HEIGHT: got %d, want 32", PCAT2_TOP_BAR_HEIGHT)
	}
	
	if PCAT2_FOOTER_HEIGHT != 22 {
		t.Errorf("PCAT2_FOOTER_HEIGHT: got %d, want 22", PCAT2_FOOTER_HEIGHT)
	}
	
	// Test state constants
	if STATE_IDLE != 0 {
		t.Errorf("STATE_IDLE: got %d, want 0", STATE_IDLE)
	}
	
	if STATE_ACTIVE != 1 {
		t.Errorf("STATE_ACTIVE: got %d, want 1", STATE_ACTIVE)
	}
}

func TestTimeConstants(t *testing.T) {
	// Test time constants
	if DEFAULT_IDLE_TIMEOUT != 60*time.Second {
		t.Error("DEFAULT_IDLE_TIMEOUT should be 60 seconds")
	}
	
	if KEYBOARD_DEBOUNCE_TIME != 200*time.Millisecond {
		t.Error("KEYBOARD_DEBOUNCE_TIME should be 200ms")
	}
	
	if OFF_TIMEOUT != 3*time.Second {
		t.Error("OFF_TIMEOUT should be 3 seconds")
	}
}

func TestColorConstants(t *testing.T) {
	// Test that color constants are properly defined
	if PCAT_YELLOW.R != 255 || PCAT_YELLOW.G != 229 || PCAT_YELLOW.B != 0 {
		t.Error("PCAT_YELLOW color not defined correctly")
	}
	
	if PCAT_WHITE.R != 255 || PCAT_WHITE.G != 255 || PCAT_WHITE.B != 255 {
		t.Error("PCAT_WHITE color not defined correctly")
	}
	
	if PCAT_BLACK.R != 0 || PCAT_BLACK.G != 0 || PCAT_BLACK.B != 0 {
		t.Error("PCAT_BLACK color not defined correctly")
	}
	
	// Check alpha values
	if PCAT_YELLOW.A != 255 {
		t.Error("PCAT_YELLOW should be fully opaque")
	}
}

func TestGlobalVariables(t *testing.T) {
	// Test that global variables are initialized properly
	if frameBufferPool.New == nil {
		t.Error("frameBufferPool.New function should be set")
	}
	
	// Test that the pool New function works
	obj := frameBufferPool.New()
	if rgba, ok := obj.(*image.RGBA); !ok {
		t.Error("frameBufferPool.New should return *image.RGBA")
	} else {
		bounds := rgba.Bounds()
		if bounds.Dx() != PCAT2_LCD_WIDTH || bounds.Dy() != PCAT2_LCD_HEIGHT {
			t.Error("frameBufferPool.New should return image with correct dimensions")
		}
	}
}

func TestInit3FrameBuffers(t *testing.T) {
	// Clear existing frame buffers for clean test
	topBarFramebuffers = nil
	middleFramebuffers = nil
	footerFramebuffers = nil
	
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("init3FrameBuffers() panicked: %v", r)
		}
	}()
	
	init3FrameBuffers()
	
	// Check that frame buffers were created
	if len(topBarFramebuffers) != 2 {
		t.Errorf("Should have 2 top bar framebuffers, got %d", len(topBarFramebuffers))
	}
	
	if len(middleFramebuffers) != 2 {
		t.Errorf("Should have 2 middle framebuffers, got %d", len(middleFramebuffers))
	}
	
	if len(footerFramebuffers) != 2 {
		t.Errorf("Should have 2 footer framebuffers, got %d", len(footerFramebuffers))
	}
	
	// Check dimensions
	if topBarFramebuffers[0] != nil {
		bounds := topBarFramebuffers[0].Bounds()
		if bounds.Dx() != topBarFrameWidth || bounds.Dy() != topBarFrameHeight {
			t.Error("Top bar framebuffer has wrong dimensions")
		}
	}
}

func TestRegisterExitHandler(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("registerExitHandler() panicked: %v", r)
		}
	}()
	
	// This function sets up signal handlers, test it doesn't crash
	registerExitHandler()
	
	// Should complete without error
	// We can't easily test signal handling in unit tests
}