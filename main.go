package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	//"image/png"
	"io/ioutil"
	"log"
	"time"
	"math/rand"

	st7789 "photonicat2_display/periph.io-st7789"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	

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

var PCAT_YELLOW      = color.RGBA{255, 229, 0, 255}
var PCAT_WHITE       = color.RGBA{255, 255, 255, 255}
var PCAT_RED         = color.RGBA{226, 72, 38, 255}
var PCAT_GREY        = color.RGBA{98, 116, 130, 255}
var PCAT_GREEN       = color.RGBA{70, 235, 145, 255}

var svgCache = make(map[string]*image.RGBA)

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
	"small_thin": 	 {FontPath: "./big.ttf", FontSize: 16},
	"small": 	 {FontPath: "./big.ttf", FontSize: 18},
	"mid":       {FontPath: "./big.ttf", FontSize: 23},
	"big":       {FontPath: "./big.ttf", FontSize: 28},
	"huge":      {FontPath: "./big.ttf", FontSize: 33},
	"gigantic":  {FontPath: "./big.ttf", FontSize: 48},
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

func clearFrame(frame *image.RGBA) {
	for i := 0; i < len(frame.Pix); i += 4 { //clear framebuffer
		frame.Pix[i] = 0       // R
		frame.Pix[i+1] = 0     // G
		frame.Pix[i+2] = 0     // B
		frame.Pix[i+3] = 255   // A (opaque black)
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

	var framebuffers []*image.RGBA
	framebuffers = append(framebuffers, image.NewRGBA(image.Rect(0, 0, frameWidth, frameHeight)))
	framebuffers = append(framebuffers, image.NewRGBA(image.Rect(0, 0, frameWidth, frameHeight)))
	clearFrame(framebuffers[0])
	clearFrame(framebuffers[1])


	// Create an image.RGBA to draw our page.
	pageImg := image.NewRGBA(image.Rect(0, 0, frameWidth, frameHeight))

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
		//nextFrame := framebuffers[(frames+1)%2]
		
		clearFrame(currFrame)
		//go clearFrame(nextFrame)
		/*
		testClock(currFrame)

		x := frames % (PCAT2_LCD_WIDTH - 40)
		y := frames / (PCAT2_LCD_HEIGHT - 40) + 50
		
		drawTextOnFrame(currFrame, "line1", x, y+30, face, color.RGBA{255, 0, 0, 255}, 0, 0)

		drawTextOnFrame(currFrame, "line2", x, y+55, face, color.RGBA{0, 255, 0, 255}, 0, 0)

		drawTextOnFrame(currFrame, "line3", x, y+80, face, color.RGBA{0, 0, 255, 255}, 0, 0)

		drawTextOnFrame(currFrame, "line4", x, y+105, face, color.RGBA{127, 0, 255, 255}, 0, 0)

		drawTextOnFrame(currFrame, "line5", x, y+130, face, color.RGBA{0, 127, 255, 255}, 0, 0)

		drawTextOnFrame(currFrame, "line6", x, y+155, face, color.RGBA{255, 0, 127, 255}, 0, 0)

		drawTextOnFrame(currFrame, "line7", x, y+180, face, color.RGBA{255, 127, 0, 255}, 0, 0)

		drawTextOnFrame(currFrame, "line8", x, y+205, face, color.RGBA{127, 255, 0, 255}, 0, 0)

		drawSVG(currFrame, "5G.svg", x, y, 0, 0)*/

		drawTopBar(currFrame)
		sendFrameImage(display, currFrame)
		

		frames++
		if frames%10 == 0 {
			elapsedTime := time.Since(startTime)
			fps = int(float64(frames) / elapsedTime.Seconds())
			fmt.Printf("FPS: %d, Frames: %d\n", fps, frames)
		}
		//time.Sleep(16 * time.Millisecond)
	}
}
