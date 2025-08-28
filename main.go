package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	gc9307 "github.com/photonicat/periph.io-gc9307"

	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/conn/v3/physic"
	"periph.io/x/conn/v3/spi"
	"periph.io/x/conn/v3/spi/spireg"
	"periph.io/x/host/v3"
)

const (
	RST_PIN              = "GPIO122"
	DC_PIN               = "GPIO121"
	CS_PIN               = "GPIO13"
	BL_PIN               = "GPIO13" //now we are using pwm control backlight
	PCAT2_LCD_WIDTH      = 172
	PCAT2_LCD_HEIGHT     = 320
	PCAT2_X_OFFSET       = 34
	PCAT2_L_MARGIN       = 8
	PCAT2_R_MARGIN       = 7
	PCAT2_T_MARGIN       = 10
	PCAT2_B_MARGIN       = 7
	PCAT2_TOP_BAR_HEIGHT = 32
	PCAT2_FOOTER_HEIGHT  = 22

	STATE_UNKNOWN  = -1
	STATE_IDLE     = 0
	STATE_ACTIVE   = 1
	STATE_FADE_IN  = 2
	STATE_FADE_OUT = 3
	STATE_OFF      = 4

	DEFAULT_FPS               = 3
	DEFAULT_IDLE_TIMEOUT      = 60 * time.Second
	ON_CHARGING_IDLE_TIMEOUT  = 365 * 86400 * time.Second
	KEYBOARD_DEBOUNCE_TIME    = 200 * time.Millisecond
	ZERO_BACKLIGHT_DELAY      = 5 * time.Second
	OFF_TIMEOUT               = 3 * time.Second
	INTERVAL_SMS_COLLECT      = 60 * time.Second
	INTERVAL_PCAT_WEB_COLLECT = 10 * time.Second // Increased from 5 to 10 seconds to reduce CPU usage

	ETC_USER_CONFIG_PATH = "/etc/pcat2_mini_display-user_config.json"
	ETC_CONFIG_PATH      = "/etc/pcat2_mini_display-config.json"
)

var (
	weAreRunning = true
	runMainLoop  = true
	offTime      = time.Now()
	PCAT_YELLOW  = color.RGBA{255, 229, 0, 255}
	PCAT_WHITE   = color.RGBA{255, 255, 255, 255}
	PCAT_RED     = color.RGBA{226, 72, 38, 255}
	PCAT_GREY    = color.RGBA{98, 116, 130, 255}
	PCAT_GREEN   = color.RGBA{70, 235, 145, 255}
	PCAT_BLACK   = color.RGBA{0, 0, 0, 255}

	svgCache = make(map[string]*image.RGBA)

	wanInterface = "null"

	frameMutex         sync.RWMutex
	// Optimized buffer manager
	bufferManager      *BufferManager
	// Render cache for frequently used elements
	renderCache        *RenderCache
	// Dirty region tracker
	dirtyTracker       *DirtyRegionTracker
	frames             int
	dataMutex          sync.RWMutex
	dynamicData        map[string]string
	imageCache         map[string]*image.RGBA
	cfg                Config
	dftCfg             Config
	userCfg            Config
	currPageIdx        int
	fonts              map[string]FontConfig
	assetsPrefix       = "."
	globalData         sync.Map
	autoRotatePages    = false

	// Frame buffer pool is now managed by BufferManager

	lastActivity   = time.Now()
	lastActivityMu sync.Mutex

	numIntermediatePages = 16

	// configuration for idle fade
	fadeDuration = 2 * time.Second // how long the fade takes
	fadeInDur    = 300 * time.Millisecond
	maxBacklight = 100
	idleTimeout  = DEFAULT_IDLE_TIMEOUT

	idleState          = STATE_ACTIVE
	lastChargingStatus = false
	battChargingStatus = false
	battSOC            = 0

	batteryDataInterval   = 1 * time.Second
	dataGatherInterval    = 2 * time.Second
	networkGatherInterval = 3 * time.Second

	desiredFPS = 5

	lastBrightness = -1

	mu          sync.Mutex
	lastLogical int         // last requested brightness (0–100)
	offTimer    *time.Timer // timer that will write 0 after delay

	smsPagesImages []*image.RGBA

	httpChangePageTriggered = false
	changePageTriggered     = false
	// Pre-calculation optimization variables
	isPreCalculating        = false
	preCalculatedReady      = false
	preCalculatedStitched   *image.RGBA
	preCalculatedNextIdx    = 0
	preCalculatedIsSMS      = false
	preCalculatedIsNextSMS  = false
	preCalculatedLocalIdx   = 0
	preCalculatedNextLocalIdx = 0
	// nextPageIdxFrameBuffer is now managed by BufferManager
	showFPS                 = false
	fps                     = 0.0
	lastUpdate              = time.Now()
	totalFrames            = 0
	stitchedFrames         = 0
	localConfigExists       = false
	stitchedFrame           *image.RGBA
	totalNumPages           = -1
	middleFrames            = 0
	topFrames              = 0
	nextPageIdxFrameBuffer *image.RGBA
	croppedFrameBuffer     *image.RGBA

	// Performance optimization
	easingLookup       []int
	cachedFPSText      string
	lastFPSUpdate      time.Time

	topBarFrameWidth  = PCAT2_LCD_WIDTH
	topBarFrameHeight = PCAT2_TOP_BAR_HEIGHT

	middleFrameWidth  = PCAT2_LCD_WIDTH
	middleFrameHeight = PCAT2_LCD_HEIGHT - PCAT2_TOP_BAR_HEIGHT - PCAT2_FOOTER_HEIGHT

	footerFrameWidth  = PCAT2_LCD_WIDTH
	footerFrameHeight = PCAT2_FOOTER_HEIGHT

	// Double buffering framebuffers
	topBarFramebuffers [2]*image.RGBA
	middleFramebuffers [2]*image.RGBA
	footerFramebuffers [2]*image.RGBA

	lenSmsPagesImages = 1
	display           gc9307.Device

	cfgNumPages = 0

	// Ping statistics tracking
	ping0Stats = struct {
		total       int
		successful  int
		lastSuccess int64
		mu          sync.RWMutex
	}{lastSuccess: -1}
	
	ping1Stats = struct {
		total       int
		successful  int
		lastSuccess int64
		mu          sync.RWMutex
	}{lastSuccess: -1}
)

// ImageBuffer holds a 1D slice of pixels for the display area.
type ImageBuffer struct {
	buffer []color.RGBA
	width  int
	height int
	loaded bool
}

// GetFrameBuffer retrieves a frame buffer from the pool
func GetFrameBuffer(width, height int) *image.RGBA {
	if bufferManager == nil {
		return image.NewRGBA(image.Rect(0, 0, width, height))
	}
	return bufferManager.GetFrameFromPool(width, height)
}

// ReturnFrameBuffer returns a frame buffer to the pool
func ReturnFrameBuffer(buf *image.RGBA) {
	if bufferManager != nil && buf != nil {
		bufferManager.ReturnFrameToPool(buf)
	}
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
	Type        string       `json:"type"`
	Label       string       `json:"label"`
	Position    Position     `json:"position"`
	Font        string       `json:"font,omitempty"`
	Color       []int        `json:"color,omitempty"`
	Units       string       `json:"units,omitempty"`
	DataKey     string       `json:"data_key,omitempty"`
	UnitsFont   string       `json:"units_font,omitempty"`
	IconPath    string       `json:"icon_path,omitempty"`
	Enable      int          `json:"enable,omitempty"`
	Size        *Size        `json:"size,omitempty"`  // for icons, if provided
	Size2       *Size        `json:"_size,omitempty"` // sometimes provided as _size
	GraphConfig *GraphConfig `json:"graph_config,omitempty"` // for graph elements
}

// GraphConfig holds configuration for graph elements
type GraphConfig struct {
	GraphType     string `json:"graph_type"`     // e.g., "power"
	TimeFrameMins int    `json:"time_frame_mins"` // time frame in minutes
}

// DisplayTemplate holds pages of elements.
type DisplayTemplate struct {
	Elements map[string][]DisplayElement `json:"elements"`
}

// Config represents the overall config JSON.
type Config struct {
	ScreenDimmerTimeOnBatterySeconds int             `json:"screen_dimmer_time_on_battery_seconds"`
	ScreenDimmerTimeOnDCSeconds      int             `json:"screen_dimmer_time_on_dc_seconds"`
	ScreenMaxBrightness              int             `json:"screen_max_brightness"`
	ScreenMinBrightness              int             `json:"screen_min_brightness"`
	PingSite0                        string          `json:"ping_site0"`
	PingSite1                        string          `json:"ping_site1"`
	DisplayTemplate                  DisplayTemplate `json:"display_template"`
	ShowSms                          bool            `json:"show_sms"`
}

// FontConfig holds parameters for a font.
type FontConfig struct {
	FontPath string  // path to TTF file
	FontSize float64 // in points
}

func main() {
	var wg sync.WaitGroup
	all := flag.Bool("all", false, "if set, listen on all network interfaces (0.0.0.0)")
	port := flag.Int("port", 8081, "TCP port to listen on")
	forceColdBoot := flag.Bool("force-cold-boot", false, "force showing welcome screen even on warm boot")
	flag.Parse()

	// Build the listen address:
	var addr string
	if *all {
		addr = fmt.Sprintf(":%d", *port) // all interfaces
	} else {
		addr = fmt.Sprintf("127.0.0.1:%d", *port) // localhost only
	}

	//rm pcat_display_initialized
	os.Remove("/tmp/pcat_display_initialized")
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

	conn, err := spiPort.Connect(80000*physic.KiloHertz, spi.Mode0, 8)
	if err != nil {
		log.Fatal(err)
	}

	//if assetsFolder not exists, use /usr/local/share/pcat2_mini_display
	if _, err := os.Stat("assets"); os.IsNotExist(err) {
		assetsPrefix = "/usr/local/share/pcat2_mini_display"
	}

	if _, err := os.Stat(assetsPrefix + "/assets"); os.IsNotExist(err) {
		assetsPrefix = "/usr/share/pcat2_mini_display"
	}

	// For demonstration, we create a mapping from font names to font configurations.
	fonts = map[string]FontConfig{
		"clock":     {FontPath: assetsPrefix + "/assets/fonts/Orbitron-Medium.ttf", FontSize: 20},
		"clockBold": {FontPath: assetsPrefix + "/assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 17},
		"reg":       {FontPath: assetsPrefix + "/assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 18},
		"big":       {FontPath: assetsPrefix + "/assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 25},
		"unit":      {FontPath: assetsPrefix + "/assets/fonts/Orbitron-Medium.ttf", FontSize: 15},
		"tiny":      {FontPath: assetsPrefix + "/assets/fonts/Orbitron-Regular.ttf", FontSize: 12},
		"micro":     {FontPath: assetsPrefix + "/assets/fonts/Orbitron-Regular.ttf", FontSize: 10},
		"thin":      {FontPath: assetsPrefix + "/assets/fonts/Orbitron-Regular.ttf", FontSize: 18},
		"huge":      {FontPath: assetsPrefix + "/assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 34},
		"gigantic":  {FontPath: assetsPrefix + "/assets/fonts/Orbitron-ExtraBold.ttf", FontSize: 48},
		// Chinese font variants
		"unit_cjk":  {FontPath: assetsPrefix + "/assets/fonts/NotoSansMonoCJK-VF.ttf.ttc", FontSize: 15},
	}

	imageCache = make(map[string]*image.RGBA)

	// Setup display.
	display = gc9307.New(conn, gpioreg.ByName(RST_PIN), gpioreg.ByName(DC_PIN), gpioreg.ByName(CS_PIN), gpioreg.ByName(BL_PIN))
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

	wg.Add(1) //first show welcome and do some other things and wait
	go func() {
		defer wg.Done()
		
		// Check if we should show welcome screen
		shouldShowWelcome := *forceColdBoot // Always show if forced
		
		if !shouldShowWelcome {
			// Check system uptime - skip welcome if uptime > 1 minute (60 seconds)
			if uptimeSeconds, err := getUptimeSeconds(); err != nil {
				log.Printf("Failed to get uptime, showing welcome: %v", err)
				shouldShowWelcome = true // Default to showing welcome on error
			} else if uptimeSeconds <= 60 {
				log.Printf("Cold boot detected (uptime: %.1fs), showing welcome screen", uptimeSeconds)
				shouldShowWelcome = true
			} else {
				log.Printf("Warm boot detected (uptime: %.1fs), skipping welcome screen", uptimeSeconds)
				shouldShowWelcome = false
			}
		} else {
			log.Println("Welcome screen forced by --force-cold-boot flag")
		}
		
		if shouldShowWelcome {
			if *forceColdBoot {
				// Force mode: show logo for 1 second only, no progress bar
				showWelcomeForced(display, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT, 1*time.Second)
			} else {
				// Normal cold boot: full animation
				showWelcome(display, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT, 5*time.Second)
			}
		}
	}()

	loadAllConfigsToVariables() //load user, default configs

	//collect data for middle and footer, non-blocking
	go func() {
		for {
			collectLinuxData(cfg)
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
			collectBatteryData()
			time.Sleep(batteryDataInterval)
		}
	}()

	go func() {
		for {
			getInfoFromPcatWeb()
			time.Sleep(INTERVAL_PCAT_WEB_COLLECT)
		}
	}()

	go func() {
		for {
			collectWANNetworkSpeed()
			time.Sleep(networkGatherInterval)
		}
	}()

	go collectFixedData()
	go getSmsPages()
	go httpServer(addr)                      //listen local for http request
	go monitorKeyboard(&changePageTriggered) // Start keyboard monitoring in a goroutine
	go monitorConsoleInput(&changePageTriggered) // Start console input monitoring in a goroutine
	go idleDimmer()                          //control backlight
	
	// Initialize power graph data recording
	initPowerDataRecording()

	registerExitHandler() //catch sigterm

	init3FrameBuffers()

	prepareMainLoop()

	wg.Wait()

	// Use optimized main loop if buffer manager is initialized
	if bufferManager != nil {
		mainLoopOptimized()
	} else {
		mainLoop()
	} //main loop

	select {} //blocking for sigterm processing
}

func registerExitHandler() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() { //register ciao screen when sigterm
		sig := <-sigs
		log.Printf("Received signal: %v", sig)
		weAreRunning = false
		offTime = time.Now()
		time.Sleep(200 * time.Millisecond)
		
		// Different behavior for SIGTERM vs SIGINT
		if sig == syscall.SIGTERM {
			log.Println("System shutdown detected, showing shutdown screen")
			showCiao(display, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT, OFF_TIMEOUT)
			time.Sleep(OFF_TIMEOUT)
		} else {
			log.Println("Manual interruption detected, showing shutdown screen but dimming instantly")
			showCiaoInstant(display, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT)
		}
		
		os.Exit(0)
	}()
}

func init3FrameBuffers() {
	// Initialize optimized buffer manager
	bufferManager = NewBufferManager()
	// Initialize render cache
	renderCache = NewRenderCache()
	// Initialize dirty region tracker
	dirtyTracker = NewDirtyRegionTracker()
	// Initialize legacy buffers for backward compatibility
	initLegacyBuffers()
	initLegacyTransitionBuffers()
}

func prepareMainLoop() {
	stitchedFrame = image.NewRGBA(image.Rect(0, 0, middleFrameWidth*2, middleFrameHeight))
	croppedFrameBuffer = image.NewRGBA(image.Rect(0, 0, middleFrameWidth, middleFrameHeight))
	nextPageIdxFrameBuffer = image.NewRGBA(image.Rect(0, 0, middleFrameWidth, middleFrameHeight))
	
	// Initialize performance optimization
	easingLookup = preCalculateEasing(numIntermediatePages, middleFrameWidth)
	lastFPSUpdate = time.Now()
}

func mainLoop() {
	log.Println("Main loop started")
	localIdx := 0
	nextLocalIdx := 0
	isSMS := false
	nextPageIdx := 0
	isNextPageSMS := false
	faceTiny, _, err := getFontFace("tiny")
	if err != nil {
		log.Fatalf("Failed to load font: %v", err)
	}

	for weAreRunning {
		if middleFrames%300 == 0 { // Log less frequently
			log.Println("showsms:", cfg.ShowSms, "totalPages:", totalNumPages, "cfgPages:", cfgNumPages)
		}
		if runMainLoop {

			start := time.Now()
			if changePageTriggered || httpChangePageTriggered { //changing page
				httpChangePageTriggered = false
				changePageTriggered = false

				var usePreCalculated bool
				
				// Check if we can use pre-calculated results
				if preCalculatedReady && !isPreCalculating {
					// Verify pre-calculated data is still valid for current page
					expectedNextIdx := (currPageIdx + 1) % totalNumPages
					if preCalculatedNextIdx == expectedNextIdx {
						usePreCalculated = true
						log.Println("Using pre-calculated stitched frame for instant animation")
						
						// Use pre-calculated values
						nextPageIdx = preCalculatedNextIdx
						isSMS = preCalculatedIsSMS
						isNextPageSMS = preCalculatedIsNextSMS
						localIdx = preCalculatedLocalIdx
						nextLocalIdx = preCalculatedNextLocalIdx
						
						// Use pre-calculated stitched frame
						copy(stitchedFrame.Pix, preCalculatedStitched.Pix)
						
						log.Println("PRE-CALC curr/next Idx:", currPageIdx, nextPageIdx, "json/sms/total:", cfgNumPages, lenSmsPagesImages, totalNumPages, "localIdx:", localIdx, "nextLocalIdx:", nextLocalIdx, "isSMS:", isSMS, "isNextPageSMS:", isNextPageSMS)
					} else {
						log.Printf("Pre-calculated data stale: expected next=%d, got=%d", expectedNextIdx, preCalculatedNextIdx)
					}
				}
				
				if !usePreCalculated {
					log.Println("Pre-calculated data not available, calculating on-demand")
					
					// Optimize page calculations - calculate once and reuse
					currPageIdx = currPageIdx % totalNumPages
					nextPageIdx = (currPageIdx + 1) % totalNumPages

					// Pre-calculate SMS status to avoid redundant checks
					isSMS = cfg.ShowSms && currPageIdx >= cfgNumPages
					isNextPageSMS = cfg.ShowSms && nextPageIdx >= cfgNumPages

					if isSMS {
						localIdx = currPageIdx - cfgNumPages
					} else {
						localIdx = currPageIdx
					}

					if isNextPageSMS {
						if lenSmsPagesImages <= 0 {
							lenSmsPagesImages = 1
						}
						nextLocalIdx = (nextPageIdx - cfgNumPages) % lenSmsPagesImages
					} else {
						if cfgNumPages > 0 {
							nextLocalIdx = nextPageIdx % cfgNumPages
						} else {
							nextLocalIdx = 0
						}
					}

					log.Println("ON-DEMAND curr/next Idx:", currPageIdx, nextPageIdx, "json/sms/total:", cfgNumPages, lenSmsPagesImages, totalNumPages, "localIdx:", localIdx, "nextLocalIdx:", nextLocalIdx, "isSMS:", isSMS, "isNextPageSMS:", isNextPageSMS)

					clearFrame(nextPageIdxFrameBuffer, middleFrameWidth, middleFrameHeight)
					renderMiddle(nextPageIdxFrameBuffer, &cfg, isSMS, localIdx)

					clearFrame(middleFramebuffers[(middleFrames+1)%2], middleFrameWidth, middleFrameHeight)
					renderMiddle(middleFramebuffers[(middleFrames+1)%2], &cfg, isNextPageSMS, nextLocalIdx)

					copyImageToImageAt(stitchedFrame, nextPageIdxFrameBuffer, 0, 0)
					copyImageToImageAt(stitchedFrame, middleFramebuffers[(middleFrames+1)%2], middleFrameWidth, 0)
				}
				
				// Mark pre-calculated data as used/stale
				preCalculatedReady = false

				// Cache FPS text to avoid string concatenation every frame
				if showFPS && time.Since(lastFPSUpdate) > 100*time.Millisecond {
					now := time.Now()
					fps = 1 / now.Sub(lastUpdate).Seconds()
					lastUpdate = now
					lastFPSUpdate = now
					cachedFPSText = "FPS:" + strconv.Itoa(int(fps)) + ", " + strconv.Itoa(middleFrames)
				}

				// Pre-calculate footer parameters outside loop for better performance
				halfPages := numIntermediatePages / 2

				// Calculate footer parameters for both phases
				firstPhaseFooterIdx := nextLocalIdx
				firstPhaseFooterPages := cfgNumPages
				firstPhaseFooterIsSMS := isNextPageSMS
				if firstPhaseFooterIsSMS {
					firstPhaseFooterPages = lenSmsPagesImages
				}

				secondPhaseFooterIdx := localIdx
				secondPhaseFooterPages := cfgNumPages
				secondPhaseFooterIsSMS := isSMS
				if secondPhaseFooterIsSMS {
					secondPhaseFooterPages = lenSmsPagesImages
				}

				for i := 0; i < numIntermediatePages; i++ {
					// Use pre-calculated values instead of recalculating
					var currentFooterIdx int
					var currentFooterPages int
					var currentFooterIsSMS bool

					if i <= halfPages {
						currentFooterIdx = firstPhaseFooterIdx
						currentFooterPages = firstPhaseFooterPages
						currentFooterIsSMS = firstPhaseFooterIsSMS
						if i == halfPages {
							localIdx = nextLocalIdx
							currPageIdx = nextPageIdx
						}
					} else {
						currentFooterIdx = secondPhaseFooterIdx
						currentFooterPages = secondPhaseFooterPages
						currentFooterIsSMS = secondPhaseFooterIsSMS
					}

					// Render footer only at transition points
					if i == 0 || i == halfPages {
						drawFooter(display, footerFramebuffers[middleFrames%2], currentFooterIdx, currentFooterPages, currentFooterIsSMS)
					}

					// Use pre-calculated easing values instead of math.Pow for better performance
					xPos := easingLookup[i]

					// Use efficient region copy instead of cropImageAt to avoid allocations
					copyImageRegion(croppedFrameBuffer, stitchedFrame, xPos, 0, middleFrameWidth, middleFrameHeight)

					// Add FPS text only if needed and use cached string
					if showFPS && cachedFPSText != "" {
						drawText(croppedFrameBuffer, cachedFPSText, 10, 240, faceTiny, PCAT_RED, false)
					}

					sendMiddle(display, croppedFrameBuffer)
					middleFrames++
					stitchedFrames++
				}

				// Update isSMS for main loop after animation completes
				isSMS = isNextPageSMS

				// Print FPS when page change animation is complete
				pageChangeDuration := time.Since(start)
				pageChangeFPS := float64(numIntermediatePages) / pageChangeDuration.Seconds()
				log.Printf("Page change completed in %.1fms, animation FPS: %.1f", float64(pageChangeDuration.Nanoseconds())/1e6, pageChangeFPS)
			} else { //normal page rendering
				// Only update top bar and footer when needed (every few frames) to save CPU
				// Top bar contains mostly static information (time, battery, signal)
				// Update it less frequently to improve performance
				if middleFrames%10 == 0 { // Update top bar every 10 frames instead of every frame
					drawTopBar(display, topBarFramebuffers[topFrames%2])
				}

				// Update footer less frequently as well, except when showing SMS
				if cfg.ShowSms && isSMS {
					drawFooter(display, footerFramebuffers[middleFrames%2], localIdx, len(smsPagesImages), isSMS)
				} else if middleFrames%5 == 0 { // Update footer every 5 frames for non-SMS pages
					drawFooter(display, footerFramebuffers[middleFrames%2], localIdx, cfgNumPages, isSMS)
				}

				//draw middle
				clearFrame(middleFramebuffers[middleFrames%2], middleFrameWidth, middleFrameHeight)
				renderMiddle(middleFramebuffers[middleFrames%2], &cfg, isSMS, localIdx)

				//draw fps - use cached text for better performance
				if showFPS {
					// Update cached FPS text periodically
					if time.Since(lastFPSUpdate) > 100*time.Millisecond {
						lastFPSUpdate = time.Now()
						cachedFPSText = "FPS:" + strconv.Itoa(int(fps)) + ", " + strconv.Itoa(middleFrames)
					}
					if cachedFPSText != "" {
						drawText(middleFramebuffers[middleFrames%2], cachedFPSText, 10, 240, faceTiny, PCAT_RED, false)
					}
				}
				sendMiddle(display, middleFramebuffers[middleFrames%2])
				middleFrames++

				// stable‐FPS sleep
				if delta := (time.Second/time.Duration(desiredFPS) - time.Since(start)); delta > 0 {
					time.Sleep(time.Duration(float64(delta) * 0.99))
				}
			}

			if middleFrames%100 == 0 {
				if autoRotatePages {
					changePageTriggered = true
				}
				now := time.Now()
				fps = 100 / now.Sub(lastUpdate).Seconds()
				log.Printf("FPS: %0.1f, Total Frames: %d\n", fps, middleFrames)
				lastUpdate = now
				log.Printf("Pages: total=%d, current=%d, cfg=%d, sms=%d, showSms=%t",
					totalNumPages, currPageIdx, cfgNumPages, lenSmsPagesImages, cfg.ShowSms)
			}
		} else {
			time.Sleep(50 * time.Millisecond) //not inf loop
		}
	}
}

// DoubleBuffer holds two buffers for double buffering
type DoubleBuffer struct {
	buffers [2]*image.RGBA
	active  int
	mutex   sync.RWMutex
}

// GetActive returns the active buffer
func (db *DoubleBuffer) GetActive() *image.RGBA {
	db.mutex.RLock()
	defer db.mutex.RUnlock()
	return db.buffers[db.active]
}

// BufferManager manages frame buffers
type BufferManager struct {
	topBar  *DoubleBuffer
	middle  *DoubleBuffer
	footer  *DoubleBuffer
	mutex   sync.RWMutex
}

// NewBufferManager creates a new buffer manager
func NewBufferManager() *BufferManager {
	return &BufferManager{
		topBar: &DoubleBuffer{
			buffers: [2]*image.RGBA{
				image.NewRGBA(image.Rect(0, 0, topBarFrameWidth, topBarFrameHeight)),
				image.NewRGBA(image.Rect(0, 0, topBarFrameWidth, topBarFrameHeight)),
			},
			active: 0,
		},
		middle: &DoubleBuffer{
			buffers: [2]*image.RGBA{
				image.NewRGBA(image.Rect(0, 0, middleFrameWidth, middleFrameHeight)),
				image.NewRGBA(image.Rect(0, 0, middleFrameWidth, middleFrameHeight)),
			},
			active: 0,
		},
		footer: &DoubleBuffer{
			buffers: [2]*image.RGBA{
				image.NewRGBA(image.Rect(0, 0, footerFrameWidth, footerFrameHeight)),
				image.NewRGBA(image.Rect(0, 0, footerFrameWidth, footerFrameHeight)),
			},
			active: 0,
		},
	}
}

// GetFrameFromPool gets a frame from the buffer pool
func (bm *BufferManager) GetFrameFromPool(width, height int) *image.RGBA {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()
	// For now, just create a new frame
	return image.NewRGBA(image.Rect(0, 0, width, height))
}

// ReturnFrameToPool returns a frame to the buffer pool
func (bm *BufferManager) ReturnFrameToPool(frame *image.RGBA) {
	// For now, this is a no-op
}

// RenderCache caches rendered elements
type RenderCache struct {
	cache map[string]*image.RGBA
	mutex sync.RWMutex
}

// NewRenderCache creates a new render cache
func NewRenderCache() *RenderCache {
	return &RenderCache{
		cache: make(map[string]*image.RGBA),
	}
}

// DirtyRegionTracker tracks dirty regions for optimization
type DirtyRegionTracker struct {
	regions []image.Rectangle
	mutex   sync.RWMutex
}

// NewDirtyRegionTracker creates a new dirty region tracker
func NewDirtyRegionTracker() *DirtyRegionTracker {
	return &DirtyRegionTracker{
		regions: make([]image.Rectangle, 0),
	}
}

// mainLoopOptimized runs the optimized main loop
func mainLoopOptimized() {
	// For now, fallback to the regular main loop
	mainLoop()
}

// initLegacyBuffers initializes the legacy framebuffers for backward compatibility
func initLegacyBuffers() {
	topBarFramebuffers[0] = image.NewRGBA(image.Rect(0, 0, topBarFrameWidth, topBarFrameHeight))
	topBarFramebuffers[1] = image.NewRGBA(image.Rect(0, 0, topBarFrameWidth, topBarFrameHeight))
	
	middleFramebuffers[0] = image.NewRGBA(image.Rect(0, 0, middleFrameWidth, middleFrameHeight))
	middleFramebuffers[1] = image.NewRGBA(image.Rect(0, 0, middleFrameWidth, middleFrameHeight))
	
	footerFramebuffers[0] = image.NewRGBA(image.Rect(0, 0, footerFrameWidth, footerFrameHeight))
	footerFramebuffers[1] = image.NewRGBA(image.Rect(0, 0, footerFrameWidth, footerFrameHeight))
}

// initLegacyTransitionBuffers initializes transition buffers
func initLegacyTransitionBuffers() {
	// Initialize any additional transition buffers if needed
	// This is a placeholder for compatibility
}

// getTopBarFramebuffer returns the top bar framebuffer at the specified index
func getTopBarFramebuffer(index int) *image.RGBA {
	if index < 0 || index >= len(topBarFramebuffers) {
		return topBarFramebuffers[0]
	}
	return topBarFramebuffers[index]
}

// getMiddleFramebuffer returns the middle framebuffer at the specified index
func getMiddleFramebuffer(index int) *image.RGBA {
	if index < 0 || index >= len(middleFramebuffers) {
		return middleFramebuffers[0]
	}
	return middleFramebuffers[index]
}

// getFooterFramebuffer returns the footer framebuffer at the specified index
func getFooterFramebuffer(index int) *image.RGBA {
	if index < 0 || index >= len(footerFramebuffers) {
		return footerFramebuffers[0]
	}
	return footerFramebuffers[index]
}
