package main

import (
	"image"
	"image/color"
	"testing"

	"golang.org/x/image/font/basicfont"
)

func TestDrawText(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	face := basicfont.Face7x13
	text := "Hello World"
	clr := color.RGBA{255, 255, 255, 255}
	
	// Test non-centered text
	finishX, finishY := drawText(img, text, 10, 10, face, clr, false)
	
	if finishX <= 10 {
		t.Error("drawText should advance X position")
	}
	if finishY <= 0 {
		t.Error("drawText should return valid Y finish position")
	}
	
	// Test centered text
	centerX, centerY := 100, 50
	finishX2, _ := drawText(img, text, centerX, centerY, face, clr, true)
	
	// For centered text, finish position should be different
	if finishX2 == finishX {
		t.Error("Centered text should have different finish X than non-centered")
	}
	
	// Test with empty text
	finishX3, _ := drawText(img, "", 20, 20, face, clr, false)
	if finishX3 != 20 {
		t.Error("Empty text should not advance X position")
	}
}

func TestLoadImage(t *testing.T) {
	// Test with non-existent file
	_, _, _, err := loadImage("/nonexistent/file.png")
	if err == nil {
		t.Error("loadImage should return error for non-existent file")
	}
	
	// Test with invalid extension
	_, _, _, err = loadImage("test.invalid")
	if err == nil {
		t.Error("loadImage should return error for unsupported format")
	}
	
	// Test supported extensions detection
	supportedExts := []string{".png", ".jpg", ".jpeg", ".gif", ".svg"}
	for _, ext := range supportedExts {
		filename := "test" + ext
		_, _, _, err := loadImage(filename)
		// Error is expected since files don't exist, but shouldn't be format error
		if err != nil && err.Error() == "unsupported image format: "+ext {
			t.Errorf("Extension %s should be supported", ext)
		}
	}
}

func TestCopyImageToImageAt(t *testing.T) {
	// Create source and destination images
	src := image.NewRGBA(image.Rect(0, 0, 50, 50))
	dst := image.NewRGBA(image.Rect(0, 0, 100, 100))
	
	// Fill source with a specific color
	srcColor := color.RGBA{255, 0, 0, 255}
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			src.Set(x, y, srcColor)
		}
	}
	
	// Test copying at offset
	err := copyImageToImageAt(dst, src, 25, 25)
	if err != nil {
		t.Errorf("copyImageToImageAt returned error: %v", err)
	}
	
	// Check that the color was copied correctly
	copiedColor := dst.RGBAAt(30, 30)
	if copiedColor != srcColor {
		t.Errorf("Color not copied correctly: got %v, want %v", copiedColor, srcColor)
	}
	
	// Check that area outside copy region is unchanged
	originalColor := dst.RGBAAt(10, 10)
	if originalColor.R != 0 || originalColor.G != 0 || originalColor.B != 0 {
		t.Error("Area outside copy region should be unchanged")
	}
	
	// Test with nil images
	err = copyImageToImageAt(nil, src, 0, 0)
	if err == nil {
		t.Error("copyImageToImageAt should return error with nil destination")
	}
	
	err = copyImageToImageAt(dst, nil, 0, 0)
	if err == nil {
		t.Error("copyImageToImageAt should return error with nil source")
	}
	
	// Test with negative coordinates
	err = copyImageToImageAt(dst, src, -1, -1)
	if err == nil {
		t.Error("copyImageToImageAt should return error with negative coordinates")
	}
}

func TestIsFullyOpaque(t *testing.T) {
	// Test fully opaque image
	opaqueImg := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			opaqueImg.Set(x, y, color.RGBA{100, 100, 100, 255})
		}
	}
	
	if !isFullyOpaque(opaqueImg) {
		t.Error("Image with all alpha=255 should be fully opaque")
	}
	
	// Test partially transparent image
	transparentImg := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			if x == 5 && y == 5 {
				transparentImg.Set(x, y, color.RGBA{100, 100, 100, 128}) // Semi-transparent
			} else {
				transparentImg.Set(x, y, color.RGBA{100, 100, 100, 255}) // Fully opaque
			}
		}
	}
	
	if isFullyOpaque(transparentImg) {
		t.Error("Image with any alpha<255 should not be fully opaque")
	}
	
	// Test fully transparent image
	fullyTransparentImg := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			fullyTransparentImg.Set(x, y, color.RGBA{100, 100, 100, 0})
		}
	}
	
	if isFullyOpaque(fullyTransparentImg) {
		t.Error("Fully transparent image should not be fully opaque")
	}
}

func TestCopyImageRegion(t *testing.T) {
	// Create source and destination images
	src := image.NewRGBA(image.Rect(0, 0, 100, 100))
	dst := image.NewRGBA(image.Rect(0, 0, 50, 50))
	
	// Fill source with different colors in different regions
	srcColor1 := color.RGBA{255, 0, 0, 255}
	srcColor2 := color.RGBA{0, 255, 0, 255}
	
	// Fill top-left quadrant with red
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			src.Set(x, y, srcColor1)
		}
	}
	
	// Fill bottom-right quadrant with green
	for y := 50; y < 100; y++ {
		for x := 50; x < 100; x++ {
			src.Set(x, y, srcColor2)
		}
	}
	
	// Copy top-left region
	copyImageRegion(dst, src, 0, 0, 50, 50)
	
	// Check that correct color was copied
	copiedColor := dst.RGBAAt(25, 25)
	if copiedColor != srcColor1 {
		t.Errorf("Region copy failed: got %v, want %v", copiedColor, srcColor1)
	}
	
	// Test with invalid region (should not crash)
	copyImageRegion(dst, src, -10, -10, 20, 20)
	copyImageRegion(dst, src, 200, 200, 50, 50) // Out of bounds
	copyImageRegion(dst, src, 0, 0, 0, 0)       // Zero dimensions
}

func TestCropImageAt(t *testing.T) {
	// Create source image
	src := image.NewRGBA(image.Rect(0, 0, 100, 100))
	
	// Fill with a pattern
	testColor := color.RGBA{128, 128, 128, 255}
	for y := 25; y < 75; y++ {
		for x := 25; x < 75; x++ {
			src.Set(x, y, testColor)
		}
	}
	
	// Crop a region
	cropped := cropImageAt(src, 20, 20, 60, 60)
	
	// Check dimensions
	if cropped.Bounds().Dx() != 60 || cropped.Bounds().Dy() != 60 {
		t.Errorf("Cropped image dimensions: got %dx%d, want 60x60",
			cropped.Bounds().Dx(), cropped.Bounds().Dy())
	}
	
	// Check that bounds start at (0,0)
	bounds := cropped.Bounds()
	if bounds.Min.X != 0 || bounds.Min.Y != 0 {
		t.Errorf("Cropped image bounds should start at (0,0), got (%d,%d)",
			bounds.Min.X, bounds.Min.Y)
	}
	
	// Test with out-of-bounds crop (should be clamped)
	cropped2 := cropImageAt(src, 90, 90, 50, 50)
	if cropped2.Bounds().Dx() > 10 || cropped2.Bounds().Dy() > 10 {
		t.Error("Out-of-bounds crop should be clamped to available area")
	}
}

func TestDrawRect(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	rectColor := color.RGBA{255, 0, 0, 255}
	
	// Draw a rectangle
	drawRect(img, 10, 10, 20, 30, rectColor)
	
	// Check that pixels inside rectangle have correct color
	insideColor := img.RGBAAt(15, 15)
	if insideColor != rectColor {
		t.Errorf("Rectangle color incorrect: got %v, want %v", insideColor, rectColor)
	}
	
	// Check that pixels outside rectangle are unchanged
	outsideColor := img.RGBAAt(5, 5)
	expectedOutsideColor := color.RGBA{0, 0, 0, 0}
	if outsideColor != expectedOutsideColor {
		t.Error("Area outside rectangle should be unchanged")
	}
	
	// Test with zero dimensions (should not crash)
	drawRect(img, 50, 50, 0, 0, rectColor)
}

func TestIsBackground(t *testing.T) {
	bgColor := color.RGBA{0, 0, 0, 0} // Transparent black
	
	// Test transparent pixel (should be background)
	transparentPixel := color.RGBA{255, 255, 255, 0}
	if !isBackground(transparentPixel, bgColor) {
		t.Error("Transparent pixel should be considered background")
	}
	
	// Test opaque pixel (should not be background)
	opaquePixel := color.RGBA{255, 255, 255, 255}
	if isBackground(opaquePixel, bgColor) {
		t.Error("Opaque pixel should not be considered background")
	}
	
	// Test semi-transparent pixel (should not be background)
	semiTransparent := color.RGBA{255, 255, 255, 128}
	if isBackground(semiTransparent, bgColor) {
		t.Error("Semi-transparent pixel should not be considered background")
	}
}

func TestCropToContent(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 100, 100))
	bgColor := color.RGBA{0, 0, 0, 0}
	
	// Add some content in the middle
	contentColor := color.RGBA{255, 255, 255, 255}
	for y := 40; y < 60; y++ {
		for x := 40; x < 60; x++ {
			frame.Set(x, y, contentColor)
		}
	}
	
	cropped := cropToContent(frame, bgColor)
	
	// Check that cropped area contains the content
	bounds := cropped.Bounds()
	if bounds.Dx() != 20 || bounds.Dy() != 20 {
		t.Errorf("Cropped content dimensions: got %dx%d, want 20x20",
			bounds.Dx(), bounds.Dy())
	}
	
	// Test with empty frame (no content)
	emptyFrame := image.NewRGBA(image.Rect(0, 0, 50, 50))
	emptyCropped := cropToContent(emptyFrame, bgColor)
	
	if !emptyCropped.Bounds().Empty() {
		t.Error("Empty frame should result in empty cropped image")
	}
}

func TestDrawBattery(t *testing.T) {
	tests := []struct {
		w, h       int
		soc        float64
		isCharging bool
	}{
		{30, 15, 50.0, false},
		{30, 15, 20.0, false}, // Low battery
		{30, 15, 100.0, true}, // Full and charging
		{30, 15, 10.0, true},  // Low and charging
		{50, 20, 75.0, false}, // Different size
	}
	
	for _, tt := range tests {
		result := drawBattery(tt.w, tt.h, tt.soc, tt.isCharging, 0, 0)
		
		if result == nil {
			t.Errorf("drawBattery(%d, %d, %f, %t) returned nil",
				tt.w, tt.h, tt.soc, tt.isCharging)
			continue
		}
		
		bounds := result.Bounds()
		if bounds.Dx() != tt.w || bounds.Dy() != tt.h {
			t.Errorf("drawBattery dimensions: got %dx%d, want %dx%d",
				bounds.Dx(), bounds.Dy(), tt.w, tt.h)
		}
		
		// Check that some pixels are not transparent (battery outline should exist)
		hasContent := false
		for y := 0; y < tt.h && !hasContent; y++ {
			for x := 0; x < tt.w && !hasContent; x++ {
				pixel := result.RGBAAt(x, y)
				if pixel.A > 0 {
					hasContent = true
				}
			}
		}
		
		if !hasContent {
			t.Error("drawBattery should produce visible content")
		}
	}
}

func TestDrawSignalStrength(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 100, 100))
	
	tests := []float64{0.0, 0.25, 0.5, 0.75, 1.0}
	
	for _, strength := range tests {
		// This function creates temporary SVG files, test it doesn't crash
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("drawSignalStrength(%f) panicked: %v", strength, r)
			}
		}()
		
		drawSignalStrength(frame, 10, 10, strength)
		
		// Function should complete without error
		// Actual drawing depends on SVG library and file system access
	}
}

func TestSaveFrameToPng(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 50, 50))
	filename := "/tmp/test_frame.png"
	
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("saveFrameToPng() panicked: %v", r)
		}
	}()
	
	// This function writes to file system, test it doesn't crash
	saveFrameToPng(frame, filename)
	
	// Could verify file exists, but that depends on file system permissions
}