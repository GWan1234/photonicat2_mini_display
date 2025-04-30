package main

import (
	"image"
	"image/color"
	//"image/draw"
	"log"
	"time"
	"math/rand"
	"strconv"
	"sync"
	"math"
	"os"

	gc9307 "github.com/photonicat/periph.io-gc9307"

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
	BL_PIN           = "GPIO13" //now we are using pwm control backlight
	PCAT2_LCD_WIDTH  = 172
	PCAT2_LCD_HEIGHT = 320
	PCAT2_X_OFFSET   = 34
	PCAT2_L_MARGIN   = 8
	PCAT2_R_MARGIN   = 7
	PCAT2_T_MARGIN   = 10
	PCAT2_B_MARGIN   = 7
	PCAT2_TOP_BAR_HEIGHT = 32
	PCAT2_FOOTER_HEIGHT = 22

	STATE_UNKNOWN = -1
	STATE_IDLE = 0
	STATE_ACTIVE = 1
	STATE_FADE_IN = 2
	STATE_FADE_OUT = 3
	DEFAULT_FPS = 5
	DEFAULT_IDLE_TIMEOUT = 10 * time.Second
	ON_CHARGING_IDLE_TIMEOUT = 365 * 86400 * time.Second
	KEYBOARD_DEBOUNCE_TIME = 500 * time.Millisecond
	ZERO_BACKLIGHT_DELAY = 5 * time.Second
	
)

var (
	PCAT_YELLOW     = color.RGBA{255, 229, 0, 255}
	PCAT_WHITE      = color.RGBA{255, 255, 255, 255}
	PCAT_RED        = color.RGBA{226, 72, 38, 255}
	PCAT_GREY       = color.RGBA{98, 116, 130, 255}
	PCAT_GREEN      = color.RGBA{70, 235, 145, 255}
	PCAT_BLACK      = color.RGBA{0, 0, 0, 255}

	svgCache 		= make(map[string]*image.RGBA)

	wanInterface = "null"

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
	//globalData 	map[string]interface{}
	fonts 		map[string]FontConfig
	assetsPrefix ="."
	globalData sync.Map
	autoRotatePages = false
	
    lastActivity   = time.Now()
    lastActivityMu sync.Mutex

	//flip page
	numIntermediatePages = 16

    // configuration for idle fade
    idleTimeout  = DEFAULT_IDLE_TIMEOUT  // how long until we start fading
    fadeDuration = 2 * time.Second    // how long the fade takes
	fadeInDur 	 = 300 * time.Millisecond
    maxBacklight = 100

	idleState = STATE_ACTIVE
	lastChargingStatus = false
	battChargingStatus = false
	battSOC = 0

	bateryDetectInterval = 200 * time.Millisecond
	dataGatherInterval = 2 * time.Second

	desiredFPS = 5

	lastBrightness = -1

	mu          sync.Mutex
    lastLogical int         // last requested brightness (0–100)
    offTimer    *time.Timer // timer that will write 0 after delay
	
)

// ImageBuffer holds a 1D slice of pixels for the display area.
type ImageBuffer struct {
	buffer []color.RGBA
	width  int
	height int
	loaded bool
}

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

// FontConfig holds parameters for a font.
type FontConfig struct {
	FontPath string  // path to TTF file
	FontSize float64 // in points
}

func main() {
	var (
		changePageTriggered = false
		nextPageIdxFrameBuffer *image.RGBA
		currPageIdx = 0
		showFPS = false
		fps float64
		lastUpdate = time.Now()
		topFrames = 0
		middleFrames = 0
		stitchedFrames = 0
		localConfigExists = false
		cfg Config
	)

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

	//if assetsFolder not exists, use /usr/local/share/pcat2_mini_display
	if _, err := os.Stat("assets"); os.IsNotExist(err) {
		assetsPrefix = "/usr/local/share/pcat2_mini_display"
	}


	// For demonstration, we create a mapping from font names to font configurations.
	fonts = map[string]FontConfig{
		"clock": 	     {FontPath: assetsPrefix + "/assets/fonts/Orbitron-Medium.ttf", FontSize: 20},
		"clockBold": 	     {FontPath: assetsPrefix + "/assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 17},
		//"small_text": 	 {FontPath: "assets/fonts/Orbitron-Medium.ttf", FontSize: 17},
		"reg": 	 {FontPath: assetsPrefix + "/assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 18},
		"big": 	 {FontPath: assetsPrefix + "/assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 25},
		"unit": 	 {FontPath: assetsPrefix + "/assets/fonts/Orbitron-Medium.ttf", FontSize: 15},
		"tiny": 	 {FontPath: assetsPrefix + "/assets/fonts/Orbitron-Regular.ttf", FontSize: 12},
		"thin": 	 {FontPath: assetsPrefix + "/assets/fonts/Orbitron-Regular.ttf", FontSize: 18},
		"huge":      {FontPath: assetsPrefix + "/assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 34},
		"gigantic":  {FontPath: assetsPrefix + "/assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 48},
	}

	imageCache = make(map[string]*image.RGBA)

	// Setup display.
	display := gc9307.New(conn, gpioreg.ByName(RST_PIN), gpioreg.ByName(DC_PIN), gpioreg.ByName(CS_PIN), gpioreg.ByName(BL_PIN))
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

	//load json config
	if _, err := os.Stat("config.json"); err == nil {
		localConfigExists = true
	}
	
	if localConfigExists {
		cfg, err = loadConfig("config.json")
	}else{
		cfg, err = loadConfig("/etc/pcat2_mini_display-config.json")
	}

	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}else{
		log.Println("CFG: READ SUCCESS")
	}

	//collect data for middle and footer, non-blocking
	go func() {
		for {
			collectData(cfg)
			time.Sleep(dataGatherInterval)
		}
	}()

	go func() {
		for {
			collectNetworkData(cfg)
			time.Sleep(dataGatherInterval)
		}
	}()

	go func() {
		for {
			collectTopBarData()
			time.Sleep(bateryDetectInterval)
		}
	}()

	go func() {
		for {
			collectWANNetworkSpeed()
		}
	}()
	
	go collectFixedData() 
	go httpServer() //listen local for http request
    go monitorKeyboard(&changePageTriggered) // Start keyboard monitoring in a goroutine
	go idleDimmer() //control backlight

	// init 3 framebuffers, top, middle, footer
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
	
	
	faceTiny, _, err := getFontFace("tiny")
	if err != nil {
		log.Fatalf("Failed to load font: %v", err)
	}
	// Main loop: you could update dynamic data and re-render pages as needed.

	stitchedFrame := image.NewRGBA(image.Rect(0, 0, middleFrameWidth * 2, middleFrameHeight))

	//main loop
	for {
		start := time.Now()
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

			for i := 0; i < numIntermediatePages; i++ {
				if i <= numIntermediatePages / 2 {
					currPageIdx = nextPageIdx
				}

				drawFooter(display, footerFramebuffers[middleFrames%2], currPageIdx, cfg.NumPages)
				
				//page transition
				t := float64(i) / float64(numIntermediatePages)      // 0 -> 1
				/*
				et0 := 0.5 * (1 - math.Cos(math.Pi * t))            // cosine
				et1 := t*(2 - t) // easeOutQuad
				et2 := 0.0 //quintic 
				if t < 0.5 {
					et2 = 16*math.Pow(t, 5)
				} else {
					et2 = 1 + 16*math.Pow(t-1, 5)
				}
				*/
				et3 := 1 - math.Pow(1-t, 4) //use quartic

				//log.Println("EaseT, t0, t1, t2, t3:",  et0, et1, et2, et3)
				xPos  := int(et3 * float64(middleFrameWidth))

				croppedFrame := cropImageAt(stitchedFrame, xPos, 0, middleFrameWidth, middleFrameHeight)
				if showFPS {	
					now := time.Now()
					fps = 1 / now.Sub(lastUpdate).Seconds()
					lastUpdate = now

					drawText(croppedFrame, "FPS:" + strconv.Itoa(int(fps)) + ", " + strconv.Itoa(middleFrames), 10, 240, faceTiny, PCAT_RED, false)
				}
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
			if showFPS {
				drawText(middleFramebuffers[middleFrames%2], "FPS:" + strconv.Itoa(int(fps)) + ", " + strconv.Itoa(middleFrames), 10, 240, faceTiny, PCAT_RED, false)
			}
			sendMiddle(display, middleFramebuffers[middleFrames%2])
			middleFrames++	

			// stable‐FPS sleep
			if delta := (time.Second / time.Duration(desiredFPS) - time.Since(start)); delta > 0 {
				time.Sleep(time.Duration(float64(delta) * 0.99))
			}
		}
		
		if middleFrames % 100 == 0 {
			if autoRotatePages {
				changePageTriggered = true
			}
			now := time.Now()
			fps = 100 / now.Sub(lastUpdate).Seconds()
			log.Printf("FPS: %0.1f, Total Frames: %d\n", fps, middleFrames)
			lastUpdate = now
		}
	}
}
