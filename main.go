package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io/ioutil"
	"log"
	"time"
	"math/rand"
	"strconv"
	"sync"
	"math"

	gc9307 "github.com/photonicat/periph.io-gc9307"

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
	PCAT2_L_MARGIN   = 8
	PCAT2_R_MARGIN   = 7
	PCAT2_T_MARGIN   = 10
	PCAT2_B_MARGIN   = 7
	PCAT2_TOP_BAR_HEIGHT = 26
	PCAT2_FOOTER_HEIGHT = 22
)

var (
	PCAT_YELLOW     = color.RGBA{255, 229, 0, 255}
	PCAT_WHITE      = color.RGBA{255, 255, 255, 255}
	PCAT_RED        = color.RGBA{226, 72, 38, 255}
	PCAT_GREY       = color.RGBA{98, 116, 130, 255}
	PCAT_GREEN      = color.RGBA{70, 235, 145, 255}
	PCAT_BLACK      = color.RGBA{0, 0, 0, 255}
	svgCache 		= make(map[string]*image.RGBA)

    frameMutex   sync.RWMutex
    currFrame 	*image.RGBA
	lastFrame 	*image.RGBA
	topBarFramebuffers 	[]*image.RGBA
	topBarFrame 		*image.RGBA
	middleFramebuffers 	[]*image.RGBA
	middleFrame 		*image.RGBA
	footerFramebuffers 	[]*image.RGBA
	footerFrame 		*image.RGBA
	frames 		int
    dataMutex    sync.RWMutex
    dynamicData  map[string]string
	imageCache 	map[string]*image.RGBA
	cfg 			Config	
	currPageIdx	 	int
	globalData 	map[string]interface{}
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
	"clock": 	     {FontPath: "assets/fonts/Orbitron-Medium.ttf", FontSize: 16},
	"clockBold": 	     {FontPath: "assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 16},
	//"small_text": 	 {FontPath: "assets/fonts/Orbitron-Medium.ttf", FontSize: 17},
	"reg": 	 {FontPath: "assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 17},
	"big": 	 {FontPath: "assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 25},
	"unit": 	 {FontPath: "assets/fonts/Orbitron-Medium.ttf", FontSize: 15},
	"huge":      {FontPath: "assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 34},
	"gigantic":  {FontPath: "assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 48},
}

// getFontFace loads the font based on our mapping.
func getFontFace(fontName string) (font.Face, int, error) {
	cfg, ok := fonts[fontName]
	if !ok {
		return nil, 0, fmt.Errorf("font %s not found in mapping", fontName)
	}
	fontBytes, err := ioutil.ReadFile(cfg.FontPath)
	if err != nil {
		return nil, 0, fmt.Errorf("error reading font file: %v", err)
	}
	ttfFont, err := opentype.Parse(fontBytes)
	if err != nil {
		return nil, 0, fmt.Errorf("error parsing font: %v", err)
	}
	face, err := opentype.NewFace(ttfFont, &opentype.FaceOptions{
		Size:    cfg.FontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, 0, err
	}
	
	// Calculate font height using the ascent and descent metrics.
	metrics := face.Metrics()
	fontHeight := metrics.Ascent.Round() + metrics.Descent.Round()
	
	return face, fontHeight, nil
}

func clearFrame(frame *image.RGBA, width int, height int) {
	for i := 0; i < width * height * 4; i += 4 { //clear framebuffer
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

	imageCache = make(map[string]*image.RGBA)

	// Setup display.
	display := gc9307.New(conn,
		gpioreg.ByName(RST_PIN),
		gpioreg.ByName(DC_PIN),
		gpioreg.ByName("GPIO0"), // placeholder for CS if unused
		gpioreg.ByName(BL_PIN))
	display.Configure(gc9307.Config{
		Width:        PCAT2_LCD_WIDTH,
		Height:       PCAT2_LCD_HEIGHT,
		Rotation:     gc9307.ROTATION_180,
		RowOffset:    0,
		ColumnOffset: PCAT2_X_OFFSET,
		FrameRate:    gc9307.FRAMERATE_60,
		VSyncLines:   gc9307.MAX_VSYNC_SCANLINES,
		UseCS:        false,
	})
	display.EnableBacklight(false)

	// Load our configuration file (adjust the path as needed).
	cfg, err := loadConfig("config.json")

	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	
	globalData = make(map[string]interface{})

	collectTopBarData() //essential data for top bar, blocking


	//collect data for middle and footer, non-blocking
	go func() {
		for {
			collectData(cfg)
			time.Sleep(10 * time.Second)
		}
	}()

	go func() {
		for {
			collectTopBarData()
			time.Sleep(2 * time.Second)
		}
	}()

	go func() {
		for {
			collectWANNetworkSpeed()
		}
	}()

	go httpServer()
	// Define frame dimensions (display area excluding margins).
	topBarFrameWidth := PCAT2_LCD_WIDTH
	topBarFrameHeight := PCAT2_TOP_BAR_HEIGHT
	
	middleFrameWidth := PCAT2_LCD_WIDTH
	middleFrameHeight := PCAT2_LCD_HEIGHT - PCAT2_TOP_BAR_HEIGHT - PCAT2_FOOTER_HEIGHT

	footerFrameWidth := PCAT2_LCD_WIDTH
	footerFrameHeight := PCAT2_FOOTER_HEIGHT


	middleFramebuffers = append(middleFramebuffers, image.NewRGBA(image.Rect(0, 0, middleFrameWidth, middleFrameHeight)))
	middleFramebuffers = append(middleFramebuffers, image.NewRGBA(image.Rect(0, 0, middleFrameWidth, middleFrameHeight)))
	clearFrame(middleFramebuffers[0], middleFrameWidth, middleFrameHeight)
	clearFrame(middleFramebuffers[1], middleFrameWidth, middleFrameHeight)

	
	topBarFramebuffers = append(topBarFramebuffers, image.NewRGBA(image.Rect(0, 0, topBarFrameWidth, topBarFrameHeight)))
	topBarFramebuffers = append(topBarFramebuffers, image.NewRGBA(image.Rect(0, 0, topBarFrameWidth, topBarFrameHeight)))
	clearFrame(topBarFramebuffers[0], topBarFrameWidth, topBarFrameHeight)
	clearFrame(topBarFramebuffers[1], topBarFrameWidth, topBarFrameHeight)

	footerFramebuffers = append(footerFramebuffers, image.NewRGBA(image.Rect(0, 0, footerFrameWidth, footerFrameHeight)))
	footerFramebuffers = append(footerFramebuffers, image.NewRGBA(image.Rect(0, 0, footerFrameWidth, footerFrameHeight)))
	clearFrame(footerFramebuffers[0], footerFrameWidth, footerFrameHeight)
	clearFrame(footerFramebuffers[1], footerFrameWidth, footerFrameHeight)
	
	


	// Create an image.RGBA to draw our page.
	pageImg := image.NewRGBA(image.Rect(0, 0, middleFrameWidth, middleFrameHeight))

	draw.Draw(pageImg, pageImg.Bounds(), &image.Uniform{color.Black}, image.Point{}, draw.Src)

	log.Println("CFG: READ SUCCESS")

	var fps float64
	lastUpdate := time.Now()
	topFrames := 0
	middleFrames := 0
	stitchedFrames := 0

	face, _, err := getFontFace("clock")
	if err != nil {
		log.Fatalf("Failed to load font: %v", err)
	}
	// Main loop: you could update dynamic data and re-render pages as needed.
	var changePageTriggered bool
	var 
	nextPageIdxFrameBuffer *image.RGBA
	changePageTriggered = false
	currPageIdx = 0
	stitchedFrame := image.NewRGBA(image.Rect(0, 0, middleFrameWidth * 2, middleFrameHeight))
	for {
		if changePageTriggered {
			
			nextPageIdx := (currPageIdx + 1) % cfg.NumPages
			log.Println("Change Page!: Current Page:", currPageIdx, "Next Page:", nextPageIdx)
			
			
			nextPageIdxFrameBuffer = image.NewRGBA(image.Rect(0, 0, middleFrameWidth, middleFrameHeight))
			clearFrame(nextPageIdxFrameBuffer, middleFrameWidth, middleFrameHeight)
			renderMiddle(nextPageIdxFrameBuffer, &cfg, nextPageIdx)
			
			clearFrame(middleFramebuffers[(middleFrames+1)%2], middleFrameWidth, middleFrameHeight)
			renderMiddle(middleFramebuffers[(middleFrames+1)%2], &cfg, currPageIdx)
			copyImageToImageAt(stitchedFrame, middleFramebuffers[(middleFrames+1)%2], 0, 0)
			copyImageToImageAt(stitchedFrame, nextPageIdxFrameBuffer, middleFrameWidth, 0)
			numIntermediatePages := 20

			for i := 0; i < numIntermediatePages; i++ {
				if i <= numIntermediatePages / 2 {
					currPageIdx = nextPageIdx
				}

				drawFooter(display, footerFramebuffers[middleFrames%2], currPageIdx, cfg.NumPages)
				
				t := float64(i) / float64(numIntermediatePages)      // t goes from 0 to 1
				easeT := 0.5 * (1 - math.Cos(math.Pi * t))            // easeInOut: starts slow, speeds up, then slows down
				xPos := int(easeT * float64(middleFrameWidth))

				croppedFrame := cropImageAt(stitchedFrame, xPos, 0, middleFrameWidth, middleFrameHeight)

				now := time.Now()
				fps = 1 / now.Sub(lastUpdate).Seconds()
				lastUpdate = now

				drawText(croppedFrame, "FPS:" + strconv.Itoa(int(fps)) + ", " + strconv.Itoa(middleFrames), 10, 240, face, PCAT_YELLOW, false)
				sendMiddle(display, croppedFrame)
				middleFrames++
				stitchedFrames++
			}
			changePageTriggered = false
		}else{
			drawTopBar(display, topBarFramebuffers[topFrames%2])
			drawFooter(display, footerFramebuffers[middleFrames%2], currPageIdx, cfg.NumPages)
			//draw middle
			clearFrame(middleFramebuffers[middleFrames%2], middleFrameWidth, middleFrameHeight)
			renderMiddle(middleFramebuffers[middleFrames%2], &cfg, currPageIdx)
			//draw fps
			drawText(middleFramebuffers[middleFrames%2], "FPS:" + strconv.Itoa(int(fps)) + ", " + strconv.Itoa(middleFrames), 10, 240, face, PCAT_YELLOW, false)
			sendMiddle(display, middleFramebuffers[middleFrames%2])
			middleFrames++	
		}


		if middleFrames % 100 == 0 {
			changePageTriggered = true
			now := time.Now()
			fps = 100 / now.Sub(lastUpdate).Seconds()
			log.Printf("FPS: %0.1f, Total Frames: %d\n", fps, middleFrames)
			lastUpdate = now
		}
		//time.Sleep(16 * time.Millisecond)
	}
}
