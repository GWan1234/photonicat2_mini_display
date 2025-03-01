package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io/ioutil"
	"log"
	"os"
	"time"
	"math/rand"

	st7789 "photonicat2_display/periph.io-st7789"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/conn/v3/physic"
	"periph.io/x/conn/v3/spi"
	"periph.io/x/conn/v3/spi/spireg"
	"periph.io/x/host/v3"
)

const (
	RST_PIN          = "GPIO122"
	DC_PIN           = "GPIO121"
	CS_PIN           = "GPIO13"
	BL_PIN           = "GPIO117"
	PCAT2_LCD_WIDTH  = 172
	PCAT2_LCD_HEIGHT = 320
	PCAT2_X_OFFSET   = 34
	PCAT2_L_MARGIN   = 10
	PCAT2_R_MARGIN   = 10
	PCAT2_T_MARGIN   = 10
	PCAT2_B_MARGIN   = 10
)

// ImageBuffer holds a 1D slice of pixels for the display area.
type ImageBuffer struct {
	buffer []color.RGBA
	width  int
	height int
	loaded bool
}

//---------------- Config and Display Element Structs ----------------

// Position defines X and Y coordinates.
type Position struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Size defines width and height.
type Size struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// DisplayElement represents one UI element to render.
type DisplayElement struct {
	Type      string   `json:"type"`
	Label     string   `json:"label"`
	Position  Position `json:"position"`
	Font      string   `json:"font,omitempty"`
	Color     []int    `json:"color,omitempty"`
	Units     string   `json:"units,omitempty"`
	DataKey   string   `json:"data_key,omitempty"`
	UnitsFont string   `json:"units_font,omitempty"`
	IconPath  string   `json:"icon_path,omitempty"`
	Enable    int      `json:"enable,omitempty"`
	Size      *Size    `json:"size,omitempty"` // for icons, if provided
	_Size     *Size    `json:"_size,omitempty"` // sometimes provided as _size
}

// DisplayTemplate holds pages of elements.
type DisplayTemplate struct {
	Elements map[string][]DisplayElement `json:"elements"`
}

// Config represents the overall config JSON.
type Config struct {
	NumPages        int             `json:"num_pages"`
	Site0           string          `json:"site0"`
	Site1           string          `json:"site1"`
	DisplayTemplate DisplayTemplate `json:"display_template"`
}

// loadConfig reads and unmarshals the config file.
func loadConfig(path string) (Config, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	err = json.Unmarshal(data, &cfg)
	return cfg, err
}

//---------------- Font Config and Loader ----------------

// FontConfig holds parameters for a font.
type FontConfig struct {
	FontPath string  // path to TTF file
	FontSize float64 // in points
}

// For demonstration, we create a mapping from font names to font configurations.
var fonts = map[string]FontConfig{
	"big":       {FontPath: "./big.ttf", FontSize: 24},
	"small_reg": {FontPath: "./small.ttf", FontSize: 12},
	"huge":      {FontPath: "./huge.ttf", FontSize: 32},
}

// getFontFace loads the font based on our mapping.
func getFontFace(fontName string) (font.Face, error) {
	cfg, ok := fonts[fontName]
	if !ok {
		return nil, fmt.Errorf("font %s not found in mapping", fontName)
	}
	fontBytes, err := ioutil.ReadFile(cfg.FontPath)
	if err != nil {
		return nil, fmt.Errorf("error reading font file: %v", err)
	}
	ttfFont, err := opentype.Parse(fontBytes)
	if err != nil {
		return nil, fmt.Errorf("error parsing font: %v", err)
	}
	face, err := opentype.NewFace(ttfFont, &opentype.FaceOptions{
		Size:    cfg.FontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	return face, err
}

//---------------- Drawing Helpers ----------------

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

// writeFrame sends a frame (as a 1D slice) to the display.
func writeFrame(display st7789.Device, frame []color.RGBA) {
	display.FillRectangleWithBuffer(PCAT2_L_MARGIN, PCAT2_T_MARGIN,
		PCAT2_LCD_WIDTH-PCAT2_L_MARGIN-PCAT2_R_MARGIN,
		PCAT2_LCD_HEIGHT-PCAT2_T_MARGIN-PCAT2_B_MARGIN,
		frame)
}

// Function to display time on frame buffer
func testClock(frame []color.RGBA) {
	frameWidth := PCAT2_LCD_WIDTH - PCAT2_L_MARGIN - PCAT2_R_MARGIN
	frameHeight := PCAT2_LCD_HEIGHT - PCAT2_T_MARGIN - PCAT2_B_MARGIN
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

	// Create an image from the frame buffer
	img := image.NewRGBA(image.Rect(0, 0, frameWidth, frameHeight))

	// Clear the frame to black (optional, or use a background color)
	draw.Draw(img, img.Bounds(), &image.Uniform{color.Black}, image.Point{}, draw.Src)

	// Set the text color to white
	textColor := color.RGBA{255, 229, 0, 255}
	randomColor := color.RGBA{ R: uint8(rand.Intn(256)), G: uint8(rand.Intn(256)), B: uint8(rand.Intn(256)), A: uint8(rand.Intn(256))}

	// Draw the formatted time string onto the image
	drawText(img, dateStr, 0, 0, face, textColor) 
	drawText(img, timeStr, 0, 30, face, randomColor) 
	
	// Copy the drawn image into the frame buffer (1D slice)
	for y := 0; y < frameHeight; y++ {
		for x := 0; x < frameWidth; x++ {
			idx := y*frameWidth + x
			frame[idx] = img.RGBAAt(x, y)
		}
	}
}

//---------------- Main ----------------

func main() {
	// Initialize board.
	if _, err := host.Init(); err != nil {
		log.Fatal(err)
	}
	rand.Seed(time.Now().UnixNano())
	// Open SPI.
	spiPort, err := spireg.Open("SPI1.0")
	if err != nil {
		log.Fatal(err)
	}
	defer spiPort.Close()

	conn, err := spiPort.Connect(100000*physic.KiloHertz, spi.Mode0, 8)
	if err != nil {
		log.Fatal(err)
	}

	// Setup display.
	display := st7789.New(conn,
		gpioreg.ByName(RST_PIN),
		gpioreg.ByName(DC_PIN),
		gpioreg.ByName("GPIO0"), // placeholder for CS if unused
		gpioreg.ByName(BL_PIN))
	display.Configure(st7789.Config{
		Width:        PCAT2_LCD_WIDTH,
		Height:       PCAT2_LCD_HEIGHT,
		Rotation:     st7789.ROTATION_180,
		RowOffset:    0,
		ColumnOffset: PCAT2_X_OFFSET,
		FrameRate:    st7789.FRAMERATE_60,
		VSyncLines:   st7789.MAX_VSYNC_SCANLINES,
		UseCS:        false,
	})
	display.EnableBacklight(false)

	// Load our configuration file (adjust the path as needed).
	cfg, err := loadConfig("config.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Define frame dimensions (display area excluding margins).
	frameWidth := PCAT2_LCD_WIDTH - PCAT2_L_MARGIN - PCAT2_R_MARGIN
	frameHeight := PCAT2_LCD_HEIGHT - PCAT2_T_MARGIN - PCAT2_B_MARGIN

	// Create two framebuffers.
	framebuffers := make([][]color.RGBA, 2)
	framebuffers[0] = make([]color.RGBA, frameWidth*frameHeight)
	framebuffers[1] = make([]color.RGBA, frameWidth*frameHeight)

	// Create an image.RGBA to draw our page.
	pageImg := image.NewRGBA(image.Rect(0, 0, frameWidth, frameHeight))
	// Optionally, clear it to a background color.
	draw.Draw(pageImg, pageImg.Bounds(), &image.Uniform{color.Black}, image.Point{}, draw.Src)

	log.Println("CFG:", cfg)
	// Simulated dynamic data (you could update this periodically).
	/*dynamicData := map[string]string{
		"network_speed_up":   "50",
		"network_speed_down": "45",
		"ping0":              "10",
		"ping1":              "12",
		"mobo_temp":          "65",
		"batt_watt":          "120",
		"batt_volt":          "12",
		"hour_left":          "3",
		"lan_ip":             "192.168.1.2",
		"public_ip":          "8.8.8.8",
		"wifi_ip":            "192.168.1.3",
	}

	// For demonstration, render page0.
	pageElements, ok := cfg.DisplayTemplate.Elements["page0"]
	if !ok {
		log.Fatalf("Page0 not found in config")
	}*/
	//renderPage(pageElements, pageImg, dynamicData)

	// Copy the drawn page into the first framebuffer.
	//copyImageToFrameBuffer(pageImg, framebuffers[0])

	fps := 0
	frames := 0
	startTime := time.Now()

	// Main loop: you could update dynamic data and re-render pages as needed.
	for {
		// Alternate between framebuffers.
		currFrame := framebuffers[frames%2]
		// For simplicity, we re-use the same framebuffer content.
		testClock(currFrame)
		writeFrame(display, currFrame)

		

		frames++
		// Calculate and print FPS every 10 frames.
		if frames%10 == 0 {
			elapsedTime := time.Since(startTime)
			fps = int(float64(frames) / elapsedTime.Seconds())
			fmt.Printf("FPS: %d, Frames: %d\n", fps, frames)
		}
		//time.Sleep(16 * time.Millisecond)
	}
}
