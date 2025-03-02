package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io/ioutil"
	"log"
	"os"
	"time"
	"bytes"
	"math/rand"

	st7789 "photonicat2_display/periph.io-st7789"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

//---------------- Drawing Functions ----------------

// drawText draws a string onto an *image.RGBA at (x,y) using the specified font face and color.
func drawText(img *image.RGBA, text string, x, y int, face font.Face, clr color.Color) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(clr),
		Face: face,
		// Adjust Y by ascent so that text draws at the baseline.
		Dot: fixed.P(x, y+int(face.Metrics().Ascent.Round())),
	}
	d.DrawString(text)
}

func drawTextOnFrame(frame *image.RGBA, text string, x, y int, face font.Face, clr color.Color, frameWidth, frameHeight int) {
    d := &font.Drawer{
        Dst:  frame,
        Src:  image.NewUniform(clr),
        Face: face,
        // Adjust Y by the font's ascent to align the baseline.
        Dot: fixed.P(x, y+int(face.Metrics().Ascent.Round())),
    }
    d.DrawString(text)
}

// renderPage renders all elements for a given page onto an image.RGBA (which represents our framebuffer).
// For text elements, it uses the font specified in the config and renders the label along with a simulated value.
func renderPage(elements []DisplayElement, img *image.RGBA, data map[string]string) {
	// For each element in the page...
	for _, el := range elements {
		switch el.Type {
		case "text":
			// Get the font face based on the element's font setting.
			face, err := getFontFace(el.Font)
			if err != nil {
				log.Printf("Error getting font face for %s: %v", el.Font, err)
				continue
			}
			// Create the text to display.
			// For example, you can display label and the dynamic value (if any).
			val, exists := data[el.DataKey]
			if !exists {
				// If no dynamic data, fall back to label.
				val = el.Label
			} else {
				// Append units if provided.
				if el.Units != "" {
					val = fmt.Sprintf("%s %s", val, el.Units)
				}
			}
			// Convert the color array (assumed [R, G, B]) to a color.RGBA.
			var clr color.RGBA
			if len(el.Color) >= 3 {
				clr = color.RGBA{R: uint8(el.Color[0]), G: uint8(el.Color[1]), B: uint8(el.Color[2]), A: 255}
			} else {
				// Default to white.
				clr = color.RGBA{255, 255, 255, 255}
			}
			// Draw the text at the specified position.
			drawText(img, val, el.Position.X, el.Position.Y, face, clr)
		case "icon":
			// For icons, you could load an image file (PNG, etc.) and draw it at el.Position.
			// Here we add a placeholder. (Note: your config shows .svg so you may need an SVG renderer.)
			if el.Enable != 0 {
				iconImg, err := loadIconImage(el.IconPath)
				if err != nil {
					log.Printf("Error loading icon %s: %v", el.IconPath, err)
					continue
				}
				// Determine icon size.
				var sz Size
				if el.Size != nil {
					sz = *el.Size
				} else if el._Size != nil {
					sz = *el._Size
				} else {
					sz = Size{Width: iconImg.Bounds().Dx(), Height: iconImg.Bounds().Dy()}
				}
				// Resize or use as is (for simplicity, we assume iconImg is already the desired size).
				pt := image.Pt(el.Position.X, el.Position.Y)
				r := image.Rectangle{Min: pt, Max: pt.Add(image.Pt(sz.Width, sz.Height))}
				draw.Draw(img, r, iconImg, image.Point{}, draw.Over)
			}
		default:
			log.Printf("Unknown element type: %s", el.Type)
		}
	}
}

// loadIconImage loads an image from filePath. For simplicity, assume PNG.
// (If your assets are SVG, you will need to convert them or use an SVG library.)
func loadIconImage(filePath string) (image.Image, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	return img, err
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

// sendFrame sends a frame (as a 1D slice) to the display.
func sendFrame(display st7789.Device, frame []color.RGBA) {
	display.FillRectangleWithBuffer(PCAT2_L_MARGIN, PCAT2_T_MARGIN,
		PCAT2_LCD_WIDTH-PCAT2_L_MARGIN-PCAT2_R_MARGIN,
		PCAT2_LCD_HEIGHT-PCAT2_T_MARGIN-PCAT2_B_MARGIN,
		frame)
}

func sendFrameImage(display st7789.Device, frame *image.RGBA) {
	display.FillRectangleWithImage(PCAT2_L_MARGIN, PCAT2_T_MARGIN,
		PCAT2_LCD_WIDTH-PCAT2_L_MARGIN-PCAT2_R_MARGIN,
		PCAT2_LCD_HEIGHT-PCAT2_T_MARGIN-PCAT2_B_MARGIN,
		frame)
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
    face, err := getFontFace("big")
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

func copyImageToImageAt(frame *image.RGBA, img *image.RGBA, x0, y0 int) error {
    frameBufferWidth := PCAT2_LCD_WIDTH - PCAT2_L_MARGIN - PCAT2_R_MARGIN
    
    targetWidth := img.Bounds().Dx()
    targetHeight := img.Bounds().Dy()
    
    // Validate input parameters
    if frame == nil || img == nil {
        return fmt.Errorf("nil image provided")
    }
    
    // Check bounds
    if x0 < 0 || y0 < 0 || 
       x0+targetWidth > frameBufferWidth || 
       y0+targetHeight > frame.Bounds().Dy() {
        return fmt.Errorf("image placement out of bounds: (%d,%d) with size %dx%d exceeds frame %dx%d",
            x0, y0, targetWidth, targetHeight, frameBufferWidth, frame.Bounds().Dy())
    }

    // Copy pixels
    for y := 0; y < targetHeight; y++ {
        for x := 0; x < targetWidth; x++ {
            frame.SetRGBA(x0+x, y0+y, img.RGBAAt(x, y))
        }
    }
    
    return nil
}