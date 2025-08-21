package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"image/jpeg"
	"image/gif"
	"io/ioutil"
	"log"
	"os"
	"time"
	"bytes"
	"math"
	"math/rand"
	"strings"
	"strconv"
	"path/filepath"
	"regexp"
	gc9307 "github.com/photonicat/periph.io-gc9307"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"github.com/ajstarks/svgo"
	"github.com/llgcode/draw2d/draw2dimg"
)

var (
	cacheTopBarStr string
	cacheTopBar *image.RGBA
	cacheFooterStr string
	cacheFooter *image.RGBA
)

//---------------- Drawing Functions ----------------
func drawText(img *image.RGBA, text string, posX, posY int, face font.Face, clr color.Color, center bool) (finishX, finishY int) {
    d := &font.Drawer{
        Dst:  img,
        Src:  image.NewUniform(clr),
        Face: face,
    }

    // Get font metrics once.
    metrics := face.Metrics()

    // Calculate text dimensions.
    textWidth := d.MeasureString(text).Round()
    textHeight := (metrics.Ascent + metrics.Descent).Round()
    var x, y int
    if center {
        // Center horizontally: shift x left by half the text width.
        x = posX - textWidth/2
        // Center vertically: shift y up by half the text height, then add ascent for baseline.
        y = posY - textHeight/2 - metrics.Ascent.Round() 
		//we still use the same y
		//y = posY
    } else {
        x = posX
    }
	y = posY + metrics.Ascent.Round()

    // Set drawing position and draw the text.
    d.Dot = fixed.P(x, y)
    d.DrawString(text)

    // Calculate finishing coordinates.
    finishX = x + textWidth
    finishY = y - metrics.Ascent.Round() + textHeight // Bottom of the text block.

    return
}

func loadImage(filePath string) (*image.RGBA, int, int, error) {
	// Check if image is in cache.
	if cachedImg, ok := imageCache[filePath]; ok {
		bounds := cachedImg.Bounds()
		return cachedImg, bounds.Dx(), bounds.Dy(), nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))

	// Open the file.
	f, err := os.Open(filePath)
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()

	var img image.Image

	switch ext {
	case ".png":
		img, err = png.Decode(f)
		if err != nil {
			return nil, 0, 0, err
		}
	case ".jpg", ".jpeg":
		img, err = jpeg.Decode(f)
		if err != nil {
			return nil, 0, 0, err
		}
	case ".gif":
		img, err = gif.Decode(f)
		if err != nil {
			return nil, 0, 0, err
		}
	case ".svg":
		// Check if SVG is already cached as rendered image
		cacheKey := filePath + "_rendered"
		if cachedImg, ok := imageCache[cacheKey]; ok {
			bounds := cachedImg.Bounds()
			return cachedImg, bounds.Dx(), bounds.Dy(), nil
		}
		
		// Read the entire SVG file.
		svgData, err := ioutil.ReadAll(f)
		if err != nil {
			return nil, 0, 0, err
		}
		// Decode the SVG using oksvg.
		icon, err := oksvg.ReadIconStream(bytes.NewReader(svgData))
		if err != nil {
			return nil, 0, 0, err
		}
		// Determine intrinsic dimensions.
		w := int(icon.ViewBox.W)
		h := int(icon.ViewBox.H)
		// Create an RGBA image to serve as the rendering canvas.
		rgba := image.NewRGBA(image.Rect(0, 0, w, h))
		// Clear the canvas with a fully transparent color.
		draw.Draw(rgba, rgba.Bounds(), image.NewUniform(color.RGBA{0, 0, 0, 0}), image.Point{}, draw.Src)
		// Set the target dimensions.
		icon.SetTarget(0, 0, float64(w), float64(h))
		// Create a scanner and dasher for rendering.
		scanner := rasterx.NewScannerGV(w, h, rgba, rgba.Bounds())
		dasher := rasterx.NewDasher(w, h, scanner)
		// Render the SVG onto the RGBA image.
		icon.Draw(dasher, 1.0)
		// Cache and return the rendered image.
		imageCache[cacheKey] = rgba
		imageCache[filePath] = rgba // Also cache with original path for fast lookup
		return rgba, w, h, nil
	default:
		return nil, 0, 0, fmt.Errorf("unsupported image format: %s", ext)
	}

	// Convert the decoded image to RGBA if needed.
	bounds := img.Bounds()
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)
	// Cache the image.
	imageCache[filePath] = rgba
	return rgba, bounds.Dx(), bounds.Dy(), nil
}


// copyImageToFrameBuffer converts an image.RGBA to a 1D []color.RGBA slice.
func copyImageToFrameBuffer(img *image.RGBA, frame []color.RGBA) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := y*width + x
			frame[idx] = img.RGBAAt(x, y)
		}
	}
}

func sendTopBar(display gc9307.Device, frame *image.RGBA) {
	display.FillRectangleWithImage(0, 0, PCAT2_LCD_WIDTH, PCAT2_TOP_BAR_HEIGHT, frame)
}

func sendFooter(display gc9307.Device, frame *image.RGBA) {
	display.FillRectangleWithImage(0, PCAT2_LCD_HEIGHT-PCAT2_FOOTER_HEIGHT, PCAT2_LCD_WIDTH, PCAT2_FOOTER_HEIGHT, frame)
}

// cropToContent scans the given frame and returns a sub-image that contains only non-background pixels.
func cropToContent(frame *image.RGBA, bgColor color.Color) *image.RGBA {
	bounds := frame.Bounds()
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y

	// Loop over all pixels in the image.
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if !isBackground(frame.At(x, y), bgColor) {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}

	// No content found? Return an empty image.
	if minX > maxX || minY > maxY {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}

	// Create the cropping rectangle.
	cropRect := image.Rect(minX, minY, maxX+1, maxY+1)
	// Use SubImage to create a new image containing only the cropped area.
	return frame.SubImage(cropRect).(*image.RGBA)
}

// isBackground compares a pixel to the given background color.
func isBackground(c color.Color, bg color.Color) bool {
	_, _, _, a1 := c.RGBA()
	//r2, g2, b2, a2 := bg.RGBA()
	return a1 == 0
}


func sendMiddlePartial(display gc9307.Device, frame *image.RGBA) {
	// Crop the frame to the region with content.
	croppedFrame := cropToContent(frame, color.Black) // assuming black is the background
	if croppedFrame.Bounds().Empty() {
		// Nothing to send.
		return
	}

	// Send the cropped frame to the display.
	// Here we use the cropped image's dimensions.
	display.FillRectangleWithImage(
		int16(croppedFrame.Bounds().Min.X),
		int16(croppedFrame.Bounds().Min.Y),
		int16(croppedFrame.Bounds().Dx()),
		int16(croppedFrame.Bounds().Dy()),
		croppedFrame,
	)
}

func sendMiddle(display gc9307.Device, frame *image.RGBA) {
	//crop some frame to save data transfer
	display.FillRectangleWithImage(0, PCAT2_TOP_BAR_HEIGHT, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT-PCAT2_TOP_BAR_HEIGHT-PCAT2_FOOTER_HEIGHT, frame)
}

func sendFull(display gc9307.Device, frame *image.RGBA) {
	display.FillRectangleWithImage(0, 0, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT, frame)
}

// Function to display time on frame buffer
func testClock(frame *image.RGBA) {
    
    // Get current time and format it
    currDateTime := time.Now()
    currHour := currDateTime.Hour()
    currMinute := currDateTime.Minute()
    currSecond := currDateTime.Second()
    currMilli := currDateTime.Nanosecond() / 1000000 // Convert nanoseconds to milliseconds
    currDay := currDateTime.Day()
    currMonth := currDateTime.Month()
    currYear := currDateTime.Year()

    // Format the time as hh:mm:ss:SSS
    timeStr := fmt.Sprintf("%02d:%02d:%02d:%03d", currHour, currMinute, currSecond, currMilli)
    dateStr := fmt.Sprintf("%04d-%02d-%02d", currYear, currMonth, currDay)
    
    // Get font face for big time display
    face, _, err := getFontFace("big")
    if err != nil {
        fmt.Println("Error loading font:", err)
        return
    }

    // Clear the frame to black (optional, or use a background color)
    draw.Draw(frame, frame.Bounds(), &image.Uniform{color.Black}, image.Point{}, draw.Src)

    // Set the text color to white
    textColor := color.RGBA{255, 229, 0, 255}
    randomColor := color.RGBA{
        R: uint8(rand.Intn(256)),
        G: uint8(rand.Intn(256)),
        B: uint8(rand.Intn(256)),
        A: uint8(rand.Intn(256)),
    }

    // Draw the formatted time string onto the image
    drawText(frame, dateStr, 0, 0, face, textColor, false)
    drawText(frame, timeStr, 0, 30, face, randomColor, false)
}

func drawSVG(frame *image.RGBA, svgPath string, x0, y0, targetWidth, targetHeight int) error {
	// If target dimensions are zero, we need to load the SVG to obtain its intrinsic size.
	if targetWidth == 0 || targetHeight == 0 {
		svgFile, err := os.Open(svgPath)
		if err != nil {
			return err
		}
		defer svgFile.Close()
		
		svgData, err := ioutil.ReadAll(svgFile)
		if err != nil {
			return err
		}
		
		icon, err := oksvg.ReadIconStream(bytes.NewReader(svgData))
		if err != nil {
			return err
		}
		if targetWidth == 0 {
			targetWidth = int(icon.ViewBox.W)
		}
		if targetHeight == 0 {
			targetHeight = int(icon.ViewBox.H)
		}
	}
	
	// Build a cache key: svgPath + "_" + targetWidth + "_" + targetHeight.
	cacheKey := fmt.Sprintf("%s_%d_%d", svgPath, targetWidth, targetHeight)
	
	// Check if we already have a cached rendered image.
	if cachedImg, ok := svgCache[cacheKey]; ok {
		copyImageToImageAt(frame, cachedImg, x0, y0)
		return nil
	}

	// Not in cache, so load and render the SVG.
	svgFile, err := os.Open(svgPath)
	if err != nil {
		return err
	}
	defer svgFile.Close()

	svgData, err := ioutil.ReadAll(svgFile)
	if err != nil {
		return err
	}

	icon, err := oksvg.ReadIconStream(bytes.NewReader(svgData))
	if err != nil {
		return err
	}

	// Set the target dimensions for the SVG rendering.
	icon.SetTarget(0, 0, float64(targetWidth), float64(targetHeight))

	// Create an RGBA image to serve as the rendering canvas.
	img := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))

	// Set up the rasterizer context.
	scanner := rasterx.NewScannerGV(targetWidth, targetHeight, img, img.Bounds())
	dasher := rasterx.NewDasher(targetWidth, targetHeight, scanner)

	// Render the SVG onto the image.
	icon.Draw(dasher, 1.0)

	// Cache the rendered image.
	svgCache[cacheKey] = img

	// Copy the rendered image into the frame buffer at the specified offset.
	copyImageToImageAt(frame, img, x0, y0)

	return nil
}

// cropImageAt crops the given src image starting at (x0, y0) with the specified width and height.
// It returns a new *image.RGBA whose bounds begin at (0,0).
func cropImageAt(src *image.RGBA, x0, y0, width, height int) *image.RGBA {
	// Get source image bounds.
	srcBounds := src.Bounds()
	// Optionally, clamp the cropping rectangle if it exceeds src bounds.
	if x0 < srcBounds.Min.X {
		x0 = srcBounds.Min.X
	}
	if y0 < srcBounds.Min.Y {
		y0 = srcBounds.Min.Y
	}
	if x0+width > srcBounds.Max.X {
		width = srcBounds.Max.X - x0
	}
	if y0+height > srcBounds.Max.Y {
		height = srcBounds.Max.Y - y0
	}
	// Define the source rectangle to crop.
	srcRect := image.Rect(x0, y0, x0+width, y0+height)
	// Create a new RGBA image with bounds starting at (0,0).
	cropped := image.NewRGBA(image.Rect(0, 0, width, height))
	// Copy the source rectangle into the new image.
	draw.Draw(cropped, cropped.Bounds(), src, srcRect.Min, draw.Src)
	return cropped
}

// copyImageToImageAt copies an image to an image at a specified offset. frame is the destination image, img is the source image. x0, y0 is the offset.
func copyImageToImageAt(frame *image.RGBA, img *image.RGBA, x0, y0 int) error {
	targetWidth := img.Bounds().Dx()
	targetHeight := img.Bounds().Dy()

	// Validate input parameters.
	if frame == nil || img == nil {
		return fmt.Errorf("nil image provided")
	}

	// Check bounds.
	if x0 < 0 || y0 < 0 {
		return fmt.Errorf("x, y is negative: %d,%d", x0, y0)
	}

	// Use optimized copying for fully opaque images
	imgBounds := img.Bounds()
	frameBounds := frame.Bounds()
	
	// Fast path for fully opaque images
	if isFullyOpaque(img) {
		// Use direct memory copy for better performance
		for y := 0; y < targetHeight; y++ {
			srcY := imgBounds.Min.Y + y
			dstY := y0 + y
			
			if dstY >= frameBounds.Min.Y && dstY < frameBounds.Max.Y {
				for x := 0; x < targetWidth; x++ {
					srcX := imgBounds.Min.X + x
					dstX := x0 + x
					
					if srcX >= imgBounds.Min.X && srcX < imgBounds.Max.X &&
					   dstX >= frameBounds.Min.X && dstX < frameBounds.Max.X {
						// Direct pixel copy for opaque images
						frame.SetRGBA(dstX, dstY, img.RGBAAt(srcX, srcY))
					}
				}
			}
		}
		return nil
	}

	// Iterate over each pixel with alpha blending for transparent images.
	for y := 0; y < targetHeight; y++ {
		for x := 0; x < targetWidth; x++ {
			sample := img.RGBAAt(x, y)
			// Skip fully transparent pixels.
			if sample.A == 0 {
				continue
			}

			// Get the destination pixel.
			dst := frame.RGBAAt(x0+x, y0+y)
			if sample.A == 255 {
				// Fully opaque: copy sample pixel directly.
				frame.SetRGBA(x0+x, y0+y, sample)
			} else {
				// Mix sample and destination pixels.
				a := uint16(sample.A)
				invA := uint16(255 - sample.A)
				outR := (uint16(sample.R)*a + uint16(dst.R)*invA) / 255
				outG := (uint16(sample.G)*a + uint16(dst.G)*invA) / 255
				outB := (uint16(sample.B)*a + uint16(dst.B)*invA) / 255
				// For the alpha channel, use the over operator:
				// outA = sample.A + dst.A*(255-sample.A)/255
				outA := uint8(uint16(sample.A) + (uint16(dst.A)*invA)/255)
				frame.SetRGBA(x0+x, y0+y, color.RGBA{
					R: uint8(outR),
					G: uint8(outG),
					B: uint8(outB),
					A: outA,
				})
			}
		}
	}

	return nil
}

// isFullyOpaque checks if an image is fully opaque (no transparency)
func isFullyOpaque(img *image.RGBA) bool {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if img.RGBAAt(x, y).A < 255 {
				return false
			}
		}
	}
	return true
}

// copyImageRegion efficiently copies a region from src to dst
func copyImageRegion(dst *image.RGBA, src *image.RGBA, x0, y0, width, height int) {
	srcBounds := src.Bounds()
	dstBounds := dst.Bounds()
	
	// Clamp to valid bounds
	if x0 < srcBounds.Min.X {
		width -= srcBounds.Min.X - x0
		x0 = srcBounds.Min.X
	}
	if y0 < srcBounds.Min.Y {
		height -= srcBounds.Min.Y - y0
		y0 = srcBounds.Min.Y
	}
	
	if x0+width > srcBounds.Max.X {
		width = srcBounds.Max.X - x0
	}
	if y0+height > srcBounds.Max.Y {
		height = srcBounds.Max.Y - y0
	}
	
	// Early return if invalid region
	if width <= 0 || height <= 0 {
		return
	}
	
	// Use direct memory copying for better performance
	for y := 0; y < height; y++ {
		srcY := y0 + y
		dstY := y
		
		if srcY >= srcBounds.Min.Y && srcY < srcBounds.Max.Y && 
		   dstY >= dstBounds.Min.Y && dstY < dstBounds.Max.Y {
			for x := 0; x < width; x++ {
				srcX := x0 + x
				dstX := x
				
				if srcX >= srcBounds.Min.X && srcX < srcBounds.Max.X &&
				   dstX >= dstBounds.Min.X && dstX < dstBounds.Max.X {
					dst.SetRGBA(dstX, dstY, src.RGBAAt(srcX, srcY))
				}
			}
		}
	}
}

func drawRoundedRect(gc *draw2dimg.GraphicContext, x, y, w, h, r float64) {
	// Start at the top-left corner, offset by the radius.
	gc.MoveTo(x+r, y)
	// Draw top edge.
	gc.LineTo(x+w-r, y)
	// Top-right arc.
	gc.ArcTo(x+w-r, y+r, r, r, -90, 90)
	// Right edge.
	gc.LineTo(x+w, y+h-r)
	// Bottom-right arc.
	gc.ArcTo(x+w-r, y+h-r, r, r, 0, 90)
	// Bottom edge.
	gc.LineTo(x+r, y+h)
	// Bottom-left arc.
	gc.ArcTo(x+r, y+h-r, r, r, 90, 90)
	// Left edge.
	gc.LineTo(x, y+r)
	// Top-left arc.
	gc.ArcTo(x+r, y+r, r, r, 180, 90)
	gc.Close()
}

func drawRect(img *image.RGBA, x0, y0, width, height int, c color.Color) {
    // Convert the color.Color to a color.RGBA.
    r, g, b, a := c.RGBA()
    // The RGBA() method returns values in the range [0, 65535],
    // so we need to shift them to [0, 255].
    col := color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}

    for x := x0; x < x0+width; x++ {
        for y := y0; y < y0+height; y++ {
            img.SetRGBA(x, y, col)
        }
    }
}

func drawSignalStrength(frame *image.RGBA, x0, y0 int, strength float64) {
	xBarSize := 5
	yBarSize := 15
	barSpace := 1
	numBars := 4
	yMinHeight := 6
	strengthInt := int(math.Ceil(strength * 4))
	fn := "/tmp/strength-"+strconv.Itoa(strengthInt)+".svg"

	if _, err := os.Stat(fn); err == nil {	//if file exists, serve the file from disk
		//do nothing
	}else{
		var buf bytes.Buffer
		canvas := svg.New(&buf)
		canvas.Start(xBarSize*numBars+barSpace*(numBars-1), yBarSize+yMinHeight)

		for i := 0; i < numBars; i++ {
			if i < strengthInt {
				canvas.Roundrect(i*xBarSize+i*barSpace, yBarSize/4*(4-i), xBarSize, yBarSize/4*i+yMinHeight, 2, 2, "fill:white")
			}else{
				fillColorHex := fmt.Sprintf("#%02X%02X%02X", PCAT_GREY.R, PCAT_GREY.G, PCAT_GREY.B)
				canvas.Roundrect(i*xBarSize+i*barSpace, yBarSize/4*(4-i), xBarSize, yBarSize/4*i+yMinHeight, 2, 2, "fill:" + fillColorHex)
			}
		}
		canvas.End()
		
		svgFile, err := os.Create(fn)
		if err != nil {
			panic(err)
		}
		_, err = svgFile.Write(buf.Bytes())
		if err != nil {
			panic(err)
		}
		svgFile.Close()
	}

	img, _, _, err := loadImage(fn)
	if err != nil {
		panic(err)
	}
	copyImageToImageAt(frame, img, x0, y0)
}

func drawBattery(w, h int, soc float64, isCharging bool, x0, y0 int) *image.RGBA {
	terminalWidth := 3
	face, _, err := getFontFace("clock")
	if err != nil {
		fmt.Println("Error loading font:", err)
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var colorMain, colorShaded color.RGBA
	if soc < 20 {
		colorMain = PCAT_RED
	}else{
		if isCharging {
			colorMain = PCAT_GREEN
		}else{
			colorMain = PCAT_WHITE
		}
	}
	colorShaded = PCAT_GREY
	
	drawRect(img, 0, 0, w-terminalWidth, h, colorMain) //main battery part
	drawRect(img, w-terminalWidth, h/2-3, terminalWidth, 6, colorMain) //terminal part
	
	//soc shade
	startShadeX := int(math.Round((soc / 100.0) * float64(w)))
	if startShadeX < w {
		for x := startShadeX; x < w-3; x++ { 
			for y := 0; y < h; y++ { 
				img.SetRGBA(x, y, colorShaded)
			}
		}
		var terminalX int
		if startShadeX > w-3{
			terminalX = startShadeX
		}else{
			terminalX = w-3
		}
		for x := terminalX; x < w; x++ { 
			for y := h/2-3; y < h/2+3; y++ { 
				img.SetRGBA(x, y, colorShaded)
			}
		}
	}

	//draw corners
	cornerCroods := []struct {X, Y int}{
		{0, 0},
		{w-terminalWidth-1, 0},
		{0, h-1},
		{w-terminalWidth-1, h-1},
		{w-1, h/2-3},
		{w-1, h/2+3-1},
	}
	
	for _, coord := range cornerCroods {
		origColor := img.RGBAAt(coord.X, coord.Y)
		newColor := color.RGBA{uint8(float64(origColor.R) *0.6), uint8(float64(origColor.G) * 0.6), uint8(float64(origColor.B) *0.6), 255}
		img.SetRGBA(coord.X, coord.Y, newColor)
	}
	
	textColor := PCAT_BLACK
	chargingBlotWidth := 10
	//draw text
	if soc < 20 {
		textColor = PCAT_WHITE
	}else{
		textColor = PCAT_BLACK
	}
	batteryText := strconv.Itoa(int(soc))
	drawChargingBlot := true
	if !isCharging || soc == 100 {
		chargingBlotWidth = 0
		drawChargingBlot = false
	}
	//drawText(img, batteryText, (w-terminalWidth)/2, -3, face, textColor, true)
	x, _ := drawText(img, batteryText, (w-terminalWidth-chargingBlotWidth)/2+1, -4, face, textColor, true)
	if drawChargingBlot {
		var chargingBolt *image.RGBA
		var err error
		if soc < 20 {
			chargingBolt, _, _, err = loadImage(assetsPrefix+"/assets/svg/blotWhite.svg")
		}else{
			chargingBolt, _, _, err = loadImage(assetsPrefix+"/assets/svg/blotBlack.svg")
		}
		if err != nil {
			fmt.Println("Error loading charging bolt:", err)
			return nil
		}
		copyImageToImageAt(img, chargingBolt, x, 1)
	}
	return img
}


func drawTopBar(display gc9307.Device, frame *image.RGBA) {
	var timeStr string
	var networkStr string
	currDateTime := time.Now()

	if currDateTime.Year() < 2025 {
		timeStr = "--:--"
	} else {
		timeStr = fmt.Sprintf("%02d:%02d", currDateTime.Hour(), currDateTime.Minute())
	}

	gatewayDevice, _ := globalData.Load("GatewayDevice")
	carrier, _ := globalData.Load("Carrier")
	
	if gatewayDevice == "mobile"{
		if carrier == "5G"{
			networkStr = "5"
		}else if carrier == "4G"{
			networkStr = "4"
		}
	}else if gatewayDevice == "wired"{
		networkStr = "w"
	}else{
		networkStr = "w"  // Default to ethernet when network status is unknown
	}
	signalStrength := 0.43
	magicStr := timeStr + " " + strconv.Itoa(int(signalStrength*100)) + " " + networkStr + " " + strconv.Itoa(int(battSOC)) + " " + strconv.FormatBool(battChargingStatus)

	if cacheTopBarStr == magicStr {
		return //no need to refresh
	}

	topBarFrameWidth := PCAT2_LCD_WIDTH
	topBarFrameHeight := PCAT2_TOP_BAR_HEIGHT

	clearFrame(frame, topBarFrameWidth, topBarFrameHeight)
	
	faceClock, _, err := getFontFace("clock")
	faceTiny, _, err := getFontFace("tiny")
	if err != nil {
		fmt.Println("Error loading font:", err)
		return
	}
	fiveGonTop :=true


	x0 := PCAT2_L_MARGIN
	y0 := PCAT2_T_MARGIN

	//draw time
	drawText(frame, timeStr, x0+2, y0-3, faceClock, PCAT_WHITE, false)	

	if networkStr == "w"{
		//draw wired
		eth, _, _, err := loadImage(assetsPrefix+"/assets/svg/eth.svg")
		if err != nil {
			fmt.Println("Error loading eth:", err)
			return
		}
		copyImageToImageAt(frame, eth, x0+80, y0+2)

	}else if networkStr == "4" || networkStr == "5" || networkStr == "3" {
		signalStrengthInt, ok := globalData.Load("ModemSignalStrength")
		if !ok {
			fmt.Println("ModemSignalStrength not found, use default 0")
			signalStrength = 0.0
		}

		fmt.Println("ModemSignalStrength:", signalStrengthInt)

		signalStrength = float64(signalStrengthInt.(int)) / 100.0
		//draw signal strength
		if fiveGonTop {
			drawSignalStrength(frame, x0+80, y0, signalStrength)
			drawText(frame, networkStr, x0+78, y0-6, faceTiny, PCAT_WHITE, false)
		}else{
			drawSignalStrength(frame, x0+70, y0, signalStrength)
			drawText(frame, networkStr, x0+94, y0-3, faceTiny, PCAT_WHITE, false)
		}
	}else if networkStr == "u"{
		nolink, _, _, err := loadImage(assetsPrefix+"/assets/svg/nolink.svg")
		if err != nil {
			fmt.Println("Error loading nolink:", err)
			return
		}
		copyImageToImageAt(frame, nolink, x0+80, y0+2)
	}

	//draw Battery
	socValue, _ := globalData.Load("BatterySoc") //it is int
	socInt, _ := socValue.(int)
	socFloat := float64(socInt) // now convert int to float64
	charging, _ := globalData.Load("BatteryCharging")
	chargingBool, ok := charging.(bool) // Type assertion to bool
	if !ok {
		chargingBool = false // Default value if assertion fails
	}
	if fiveGonTop {
		img := drawBattery(50, 19, socFloat, chargingBool, x0, y0)
		copyImageToImageAt(frame, img, x0+108, y0)
	}else{
		img := drawBattery(45, 18, socFloat, chargingBool, x0, y0)
		copyImageToImageAt(frame, img, x0+113, y0)
	}
	cacheTopBar = frame
	cacheTopBarStr = magicStr
	sendTopBar(display, frame)
}

func saveFrameToPng(frame *image.RGBA, filename string) {
	outFile, err := os.Create(filename)
	if err != nil {
		panic(err)
	}
	defer outFile.Close()
	png.Encode(outFile, frame)
	fmt.Println("Frame saved to", filename)
}

func renderMiddle(frame *image.RGBA, cfg *Config, isSMS bool, pageIdx int) {
	var placeholderRe = regexp.MustCompile(`\[(\w+)\]`)

	if isSMS {
		copyImageToImageAt(frame, smsPagesImages[pageIdx], 0, 0)
		return
	}

	page := cfg.DisplayTemplate.Elements["page"+strconv.Itoa(pageIdx)]

	// Process each element.
	for _, element := range page {
		// Check if the element is enabled.
		if element.Enable == 0 {
			continue
		}

		switch element.Type {
		case "text":
			// Determine the text to display first (moved up to use for font selection)
			textValue, exists := globalData.Load(element.DataKey)
			var textToDisplay string
			var isPingTimeout bool = false
			
			if exists {
				if textValue == nil {
					textToDisplay = "-"
				} else {
					// Special handling for ping timeout (-2) and errors
					if (element.DataKey == "Ping0" || element.DataKey == "Ping1") {
						// Handle both int64 and int types for ping values
						var pingVal int64
						var validPingValue bool
						
						if val, ok := textValue.(int64); ok {
							pingVal = val
							validPingValue = true
						} else if val, ok := textValue.(int); ok {
							pingVal = int64(val)
							validPingValue = true
						}
						
						if validPingValue {
							if pingVal == -2 || pingVal == -1 {
								// Both timeout (-2) and other failures (-1) show red X
								textToDisplay = "X"
								isPingTimeout = true
							} else if pingVal >= 0 {
								textToDisplay = fmt.Sprintf("%d", pingVal)
							} else {
								// Any other negative value should not happen, but show as red X
								textToDisplay = "X"
								isPingTimeout = true
							}
						} else {
							// If not a numeric type, show as error
							textToDisplay = "-"
						}
					} else {
						textToDisplay = fmt.Sprintf("%v", textValue)
					}
				}
			} else {
				textToDisplay = "-" // or any default value you prefer
			}
			
			// Get the font face for the main text (choose Chinese font if needed)
			face, _, err := getFontFaceForText(element.Font, textToDisplay)
			if err != nil {
				log.Printf("Error getting font face for %s: %v", element.Font, err)
				continue
			}
			// Get the font face for the units.
			unitFace, _, err := getFontFace(element.UnitsFont)
			if err != nil {
				log.Printf("Error getting font face for %s: %v", element.UnitsFont, err)
				continue
			}

			// Convert the color array (assumed to be [R,G,B]) to a color.RGBA.
			var clr color.RGBA
			if len(element.Color) >= 3 {
				clr = color.RGBA{
					R: uint8(element.Color[0]),
					G: uint8(element.Color[1]),
					B: uint8(element.Color[2]),
					A: 255,
				}
			} else {
				// Default color: white.
				clr = color.RGBA{255, 255, 255, 255}
			}

			// Use red color for ping timeouts
			if isPingTimeout {
				clr = PCAT_RED
			}

			// Draw the main text.
			// The drawText function uses the provided y plus the font ascent as the baseline.
			mainAscent := face.Metrics().Ascent.Round()
			// element.Position.Y acts as the top of the text area.
			mainBaseline := element.Position.Y + mainAscent
			xMain, _ := drawText(frame, textToDisplay, element.Position.X, element.Position.Y, face, clr, false)

			// Calculate the y position for the units text so that its baseline aligns with the main text.
			unitAscent := unitFace.Metrics().Ascent.Round()
			unitY := mainBaseline - unitAscent

			// Draw the units text slightly to the right of the main text (skip units for timeout)
			if !isPingTimeout {
				unitText := element.Units
				//check if there is a override unit
				theKey := element.DataKey + "_Unit"
				if _, ok := globalData.Load(theKey); ok {
					unitTextVal, _ := globalData.Load(theKey)
					unitText = unitTextVal.(string)
				}
				drawText(frame, unitText, xMain+1, unitY, unitFace, clr, false)
			}
		
		case "icon":
			var iconImg *image.RGBA
			var err error
			iconImg, _, _, err = loadImage(assetsPrefix + "/" + element.IconPath)
			if err != nil {
				log.Printf("Error loading icon from %s: %v", element.IconPath, err)
				continue
			}

			// Determine the size for the icon.
			var sz Size
			if element.Size != nil {
				sz = *element.Size
			} else if element.Size2 != nil {
				sz = *element.Size2
			} else {
				sz = Size{Width: iconImg.Bounds().Dx(), Height: iconImg.Bounds().Dy()}
			}

			// Define the destination rectangle for the icon.
			pt := image.Pt(element.Position.X, element.Position.Y)
			rect := image.Rect(pt.X, pt.Y, pt.X+sz.Width, pt.Y+sz.Height)
			draw.Draw(frame, rect, iconImg, image.Point{}, draw.Over)
		case "fixed_text":
			face, _, err := getFontFace(element.Font)
			if err != nil {
				log.Printf("Error getting font face for %s: %v", element.Font, err)
				continue
			}
			// pick a default white if no color
			var clr color.RGBA
			if len(element.Color) >= 3 {
				clr = color.RGBA{uint8(element.Color[0]), uint8(element.Color[1]), uint8(element.Color[2]), 255}
			} else {
				clr = color.RGBA{255, 255, 255, 255}
			}
		
			// replace [key] → cfg.Field
			label := placeholderRe.ReplaceAllStringFunc(element.Label, func(tok string) string {
				key := tok[1 : len(tok)-1] // strip brackets
				switch key {
				case "ping_site0":
					return cfg.PingSite0
				case "ping_site1":
					return cfg.PingSite1
				/*case "screen_dimmer_time_on_battery_seconds":
					return strconv.Itoa(cfg.ScreenDimmerTimeOnBatterySeconds)
				case "screen_dimmer_time_on_dc_seconds":
					return strconv.Itoa(cfg.ScreenDimmerTimeOnDCSeconds)*/
				// add more fields here if you ever parameterize them in fixed_text
				default:
					return tok
				}
			})
		

			drawText(frame, label, element.Position.X, element.Position.Y, face, clr, false)

		default:
			log.Printf("Unknown element type: %s", element.Type)
		}
	}
}

func drawFooter(display gc9307.Device, frame *image.RGBA, currPage int, numOfPages int, isSMS bool) {
	magicStr:= strconv.Itoa(currPage) + " " + strconv.Itoa(numOfPages) + " " + strconv.FormatBool(isSMS)
	if cacheFooterStr == magicStr {
		return //no need to refresh
	}
	faceMicro, _, err := getFontFace("micro")
	if err != nil {
		log.Printf("Error getting font face for %s: %v", "tiny", err)
		return
	}

	footerFrameWidth := PCAT2_LCD_WIDTH
	footerFrameHeight := PCAT2_FOOTER_HEIGHT
	clearFrame(frame, footerFrameWidth, footerFrameHeight)

	if isSMS {
		log.Printf("Drawing SMS footer")
		footerText := "SMS: " + strconv.Itoa(currPage+1) + "/" + strconv.Itoa(numOfPages)
		drawText(frame, footerText, 172/2, 2, faceMicro, PCAT_WHITE, true)

	}else{
		log.Printf("Drawing normal footer")
		cir, _, _, err := loadImage(assetsPrefix+"/assets/svg/dotCircle.svg")
		if err != nil {
			log.Printf("Error loading circle_dot from %s: %v", "assets/svg/dotCircle.svg", err)
			return
		}
		dot, _, _, err := loadImage(assetsPrefix+"/assets/svg/dotSolid.svg")
		if err != nil {
			log.Printf("Error loading dot from %s: %v", "assets/svg/dotSolid.svg", err)
			return
		}

		whiteDotRadius := 8
		greyDotRadius := 4
		xPart := 10 + whiteDotRadius * 2
		yOffset := 2
		x0 := (PCAT2_LCD_WIDTH - (numOfPages-1)*xPart) / 2  - whiteDotRadius

		for i := 0; i < numOfPages; i++ {
			if i == currPage {
				copyImageToImageAt(frame, cir, x0+i*xPart, yOffset)
			}else{
				copyImageToImageAt(frame, dot, x0+i*xPart + greyDotRadius, yOffset + greyDotRadius)
			}
		}
	}
	//make a frame cache
	cacheFooter = frame
	cacheFooterStr = magicStr
	sendFooter(display, frame)
}

func showWelcome(display gc9307.Device, width, height int, duration time.Duration) {
	radiusBarCorner := 5
	spaceBetweenLogoAndBar := 28
	barWidth := 82
    barX := width/2 - barWidth/2
	barHeight := 8
	fnBase:="/tmp/barBackground.svg"
	fnProgressPart:="/tmp/barProgress_"

	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	clearFrame(frame, width, height)
	welcomeLogo, w, h, err := loadImage(assetsPrefix+"/assets/svg/welcome.svg")
	if err != nil {
		log.Printf("Error loading welcome logo from %s: %v", "assets/svg/welcome.svg", err)
		return
	}
	logoY := height/2 - (h+spaceBetweenLogoAndBar+barHeight)/2
	x0 := width/2 - w/2
	y0 := logoY
	log.Printf("Welcome logo at: x0: %d, y0: %d, w: %d, h: %d", x0, y0, w, h)
	copyImageToImageAt(frame, welcomeLogo, x0, y0 ) 
	//save this frame to png
	saveFrameToPng(frame, "/tmp/welcome.png")
	copyImageToImageAt(frame, welcomeLogo, x0, y0 ) 

	var bufBack bytes.Buffer
	canvas := svg.New(&bufBack)
	canvas.Start(barWidth, barHeight)
	canvas.Roundrect(0, 0, barWidth, barHeight, radiusBarCorner, radiusBarCorner, "fill:#627482")
	canvas.End()
	svgFile, err := os.Create(fnBase)
	if err != nil {
		panic(err)
	}
	_, err = svgFile.Write(bufBack.Bytes())
	if err != nil {
		panic(err)
	}
	svgFile.Close()
	barBackground, _, _, err := loadImage(fnBase)
	if err != nil {
		log.Printf("Error loading bar background from %s: %v", fnBase, err)
		return
	}
	barY := logoY + spaceBetweenLogoAndBar + h
	copyImageToImageAt(frame, barBackground, barX, barY)
	sendFull(display, frame)

	var bufProgress bytes.Buffer
	var progressBar *image.RGBA

    for i := 1; i <= barWidth; i++ {
		fnProgress := fnProgressPart+strconv.Itoa(i)+".svg"
		bufProgress.Reset()
		canvasProgress := svg.New(&bufProgress)
		canvasProgress.Start(barWidth, barHeight)
		canvasProgress.Roundrect(0, 0, i, barHeight, radiusBarCorner, radiusBarCorner, "fill:#FDE021")
		canvasProgress.End()
		svgFile, err := os.Create(fnProgress)
		if err != nil {
			panic(err)
		}
		_, err = svgFile.Write(bufProgress.Bytes())
		if err != nil {
			panic(err)
		}
		svgFile.Close()
		progressBar, _, _, err = loadImage(fnProgress)
		
		if err != nil {
			log.Printf("Error loading bar background from %s: %v", fnBase, err)
			return
		}
		copyImageToImageAt(frame, progressBar, barX, barY)
		sendFull(display, frame)
      
		//time.Sleep(sleepPerPixel)
    }
}

func showWelcomeForced(display gc9307.Device, width, height int, duration time.Duration) {
	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	clearFrame(frame, width, height)
	
	// Load and display welcome logo only
	welcomeLogo, w, h, err := loadImage(assetsPrefix+"/assets/svg/welcome.svg")
	if err != nil {
		log.Printf("Error loading welcome logo from %s: %v", "assets/svg/welcome.svg", err)
		return
	}
	
	// Center the logo
	x0 := width/2 - w/2
	y0 := height/2 - h/2
	copyImageToImageAt(frame, welcomeLogo, x0, y0)
	
	// Send to display and wait for specified duration
	sendFull(display, frame)
	time.Sleep(duration)
}



func showCiao(display gc9307.Device, width, height int, duration time.Duration) {
	spaceBetweenLogoAndText := 28
	textHeight := 12
	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	clearFrame(frame, width, height)
	//clear display
	sendFull(display, frame)
	ciaoLogo, w, h, err := loadImage(assetsPrefix+"/assets/svg/ciao.svg")
	if err != nil {
		log.Printf("Error loading ciao logo from %s: %v", "assets/svg/ciao.svg", err)
		return
	}
	logoY := height/2 - (h+spaceBetweenLogoAndText+textHeight)/2
	copyImageToImageAt(frame, ciaoLogo, width/2 - w/2, logoY ) 
	text := "Powering Off..."
	faceUnit, _, err := getFontFace("unit")
	if err != nil {
		log.Printf("Error getting font face for %s: %v", "unit", err)
		return
	}
	drawText(frame, text, width/2, logoY + h + spaceBetweenLogoAndText, faceUnit, PCAT_WHITE, true)
	sendFull(display, frame)

}

func showCiaoInstant(display gc9307.Device, width, height int) {
	spaceBetweenLogoAndText := 28
	textHeight := 12
	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	clearFrame(frame, width, height)
	
	// Load and display shutdown screen
	ciaoLogo, w, h, err := loadImage(assetsPrefix+"/assets/svg/ciao.svg")
	if err != nil {
		log.Printf("Error loading ciao logo from %s: %v", "assets/svg/ciao.svg", err)
		return
	}
	logoY := height/2 - (h+spaceBetweenLogoAndText+textHeight)/2
	copyImageToImageAt(frame, ciaoLogo, width/2 - w/2, logoY)
	text := "Powering Off..."
	faceUnit, _, err := getFontFace("unit")
	if err != nil {
		log.Printf("Error getting font face for %s: %v", "unit", err)
		return
	}
	drawText(frame, text, width/2, logoY + h + spaceBetweenLogoAndText, faceUnit, PCAT_WHITE, true)
	sendFull(display, frame)
	
	// Instantly dim to minimum brightness
	setBacklight(0)
}
