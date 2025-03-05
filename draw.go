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
	st7789 "photonicat2_display/periph.io-st7789"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"

	"github.com/llgcode/draw2d/draw2dimg"
)

//---------------- Drawing Functions ----------------

// drawText draws a string onto an *image.RGBA at (x,y) using the specified font face and color.
func drawText(img *image.RGBA, text string, x, y int, face font.Face, clr color.Color) (finishX, finishY int) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(clr),
		Face: face,
		// Adjust Y by ascent so that text draws at the baseline.
		Dot: fixed.P(x, y+int(face.Metrics().Ascent.Round())),
	}
	d.DrawString(text)
	
	// Measure the text width.
	textWidth := d.MeasureString(text).Round()
	// Calculate the total text height using ascent and descent.
	textHeight := face.Metrics().Ascent.Round() + face.Metrics().Descent.Round()
	
	// Determine finishing coordinates.
	finishX = x + textWidth
	finishY = y + textHeight
	return
}

func drawTextOnFrame2(frame *image.RGBA, text string, centerX, centerY int, face font.Face, clr color.Color) {
    d := &font.Drawer{
        Dst:  frame,
        Src:  image.NewUniform(clr),
        Face: face,
    }
    // Measure the width of the text.
    textWidth := d.MeasureString(text).Round()
    // Calculate the starting X position so that the text is horizontally centered.
    x := centerX - textWidth/2

    // For vertical centering, consider the font's ascent and descent.
    metrics := face.Metrics()
    textHeight := (metrics.Ascent + metrics.Descent).Round()
    // Adjust the Y so that the text's center aligns with centerY.
    y := centerY - textHeight/2 + metrics.Ascent.Round()

    d.Dot = fixed.P(x, y)
    d.DrawString(text)
}

func loadImage(filePath string) (*image.RGBA, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	
	// Open the file.
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var img image.Image

	switch ext {
	case ".png":
		img, err = png.Decode(f)
		if err != nil {
			return nil, err
		}
	case ".jpg", ".jpeg":
		img, err = jpeg.Decode(f)
		if err != nil {
			return nil, err
		}
	case ".gif":
		img, err = gif.Decode(f)
		if err != nil {
			return nil, err
		}
	case ".svg":
		// Read the entire SVG file.
		svgData, err := ioutil.ReadAll(f)
		if err != nil {
			return nil, err
		}
		// Decode the SVG using oksvg.
		icon, err := oksvg.ReadIconStream(bytes.NewReader(svgData))
		if err != nil {
			return nil, err
		}
		// Determine intrinsic dimensions.
		w := int(icon.ViewBox.W)
		h := int(icon.ViewBox.H)
		// Create an RGBA image to serve as the rendering canvas.
		rgba := image.NewRGBA(image.Rect(0, 0, w, h))
		// Set the target dimensions.
		icon.SetTarget(0, 0, float64(w), float64(h))
		// Create a scanner and dasher for rendering.
		scanner := rasterx.NewScannerGV(w, h, rgba, rgba.Bounds())
		dasher := rasterx.NewDasher(w, h, scanner)
		// Render the SVG onto the RGBA image.
		icon.Draw(dasher, 1.0)
		return rgba, nil
	default:
		return nil, fmt.Errorf("unsupported image format: %s", ext)
	}

	// Convert the decoded image to RGBA if needed.
	bounds := img.Bounds()
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)
	return rgba, nil
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

func sendTopBar(display st7789.Device, frame *image.RGBA) {
	display.FillRectangleWithImage(0, 0, PCAT2_LCD_WIDTH, PCAT2_TOP_BAR_HEIGHT, frame)
}

func sendFooter(display st7789.Device, frame *image.RGBA) {
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


func sendMiddlePartial(display st7789.Device, frame *image.RGBA) {
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

func sendMiddle(display st7789.Device, frame *image.RGBA) {
	//crop some frame to save data transfer
	display.FillRectangleWithImage(0, PCAT2_TOP_BAR_HEIGHT, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT-PCAT2_TOP_BAR_HEIGHT-PCAT2_FOOTER_HEIGHT, frame)
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
    drawText(frame, dateStr, 0, 0, face, textColor)
    drawText(frame, timeStr, 0, 30, face, randomColor)
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
//copyImageToImageAt copies an image to an image at a specified offset. frame is the destination image, img is the source image. 
//x0, y0 is the offset.
func copyImageToImageAt(frame *image.RGBA, img *image.RGBA, x0, y0 int) error {
    targetWidth := img.Bounds().Dx()
    targetHeight := img.Bounds().Dy()
    
    // Validate input parameters
    if frame == nil || img == nil {
        return fmt.Errorf("nil image provided")
    }
    
    // Check bounds
    if x0 < 0 || y0 < 0 {
		return fmt.Errorf("x, y is negative: %d,%d", x0, y0)
	}

    // Copy pixels
    for y := 0; y < targetHeight; y++ {
        for x := 0; x < targetWidth; x++ {
            frame.SetRGBA(x0+x, y0+y, img.RGBAAt(x, y))
        }
    }
    
    return nil
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

func drawBattery(w, h int, soc float64, onBattery bool, x0, y0 int) *image.RGBA {
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
		if onBattery {
			colorMain = PCAT_WHITE
		}else{
			colorMain = PCAT_GREEN
		}
	}
	colorShaded = PCAT_GREY
	
	drawRect(img, 0, 0, w-3, h, colorMain) //main battery part
	drawRect(img, w-3, h/2-3, 3, 6, colorMain) //terminal part
	
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
		{w-3-1, 0},
		{0, h-1},
		{w-3-1, h-1},
		{w-1, h/2-3},
		{w-1, h/2+3-1},
	}
	
	for _, coord := range cornerCroods {
		origColor := img.RGBAAt(coord.X, coord.Y)
		newColor := color.RGBA{uint8(float64(origColor.R) *0.6), uint8(float64(origColor.G) * 0.6), uint8(float64(origColor.B) *0.6), 255}
		img.SetRGBA(coord.X, coord.Y, newColor)
	}
	
	//draw text
	if soc == 100 {
		drawText(img, "100", 2, -2, face, PCAT_BLACK)
	}else{
		drawText(img, strconv.Itoa(int(soc)), 4, -2, face, PCAT_BLACK)
	}

	return img
}

func drawTopBar(frame *image.RGBA) {
	x0 := PCAT2_L_MARGIN
	y0 := PCAT2_T_MARGIN
	var timeStr string
	faceClock, _, err := getFontFace("clock")
	faceClockBold, _, err := getFontFace("clockBold")
	if err != nil {
		fmt.Println("Error loading font:", err)
		return
	}

	//clock
	currDateTime := time.Now()
    currHour := currDateTime.Hour()
    currMinute := currDateTime.Minute()
	currYear := currDateTime.Year()

	if currYear < 2025 {
		timeStr = "--:--"
	} else {
		timeStr = fmt.Sprintf("%02d:%02d", currHour, currMinute)
	}
	networkStr := "5G"

	drawText(frame, timeStr, x0+2, y0-2, faceClock, PCAT_WHITE)	

	//draw network
	drawText(frame, networkStr, x0+78, y0-2, faceClockBold, PCAT_WHITE)

	//draw Battery
	randomSoc := rand.Intn(100)
	randomChargingBool := rand.Intn(2) == 0
	img := drawBattery(40, 16, float64(randomSoc), randomChargingBool, x0, y0)
	copyImageToImageAt(frame, img, x0+118, y0)
	
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

func renderMiddle(frame *image.RGBA, cfg *Config) {
	// Get the elements for page0 from the configuration.
	page := cfg.DisplayTemplate.Elements["page0"]

	// Simulated dynamic data for rendering.
	data := map[string]string{
		"network_speed_up":   "50.8",
		"network_speed_down": "30.3",
		"ping0":              "10",
		"ping1":              "1200",
		"mobo_temp":          "50",
		"batt_watt":          "+45",
		"batt_volt":          "8.12",
		"hour_left":          "15",
		"dc_v":               "20",
	}

	// Process each element.
	for _, element := range page {
		// Check if the element is enabled.
		if element.Enable == 0 {
			continue
		}

		switch element.Type {
		case "text":
			// Get the font face for the main text.
			face, _, err := getFontFace(element.Font)
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

			// Determine the text to display.
			textToDisplay, _ := data[element.DataKey]

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

			// Draw the main text.
			// The drawText function uses the provided y plus the font ascent as the baseline.
			mainAscent := face.Metrics().Ascent.Round()
			// element.Position.Y acts as the top of the text area.
			mainBaseline := element.Position.Y + mainAscent
			xMain, _ := drawText(frame, textToDisplay, element.Position.X, element.Position.Y, face, clr)

			// Calculate the y position for the units text so that its baseline aligns with the main text.
			unitAscent := unitFace.Metrics().Ascent.Round()
			unitY := mainBaseline - unitAscent

			// Draw the units text slightly to the right of the main text.
			drawText(frame, element.Units, xMain+1, unitY, unitFace, clr)
		
		case "icon":
			var iconImg *image.RGBA
			var err error
			iconImg, err = loadImage(element.IconPath)
			if err != nil {
				log.Printf("Error loading icon from %s: %v", element.IconPath, err)
				continue
			}

			// Determine the size for the icon.
			var sz Size
			if element.Size != nil {
				sz = *element.Size
			} else if element._Size != nil {
				sz = *element._Size
			} else {
				sz = Size{Width: iconImg.Bounds().Dx(), Height: iconImg.Bounds().Dy()}
			}

			// Define the destination rectangle for the icon.
			pt := image.Pt(element.Position.X, element.Position.Y)
			rect := image.Rect(pt.X, pt.Y, pt.X+sz.Width, pt.Y+sz.Height)
			draw.Draw(frame, rect, iconImg, image.Point{}, draw.Over)

		default:
			log.Printf("Unknown element type: %s", element.Type)
		}
	}
}