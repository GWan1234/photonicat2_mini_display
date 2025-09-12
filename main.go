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
	KEYBOARD_DEBOUNCE_TIME    = 40 * time.Millisecond
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

	// Shutdown monitoring
	//shutdownMonitor *ShutdownMonitor
	PCAT_YELLOW = color.RGBA{255, 229, 0, 255}
	PCAT_WHITE  = color.RGBA{255, 255, 255, 255}
	PCAT_RED    = color.RGBA{226, 72, 38, 255}
	PCAT_GREY   = color.RGBA{98, 116, 130, 255}
	PCAT_GREEN  = color.RGBA{70, 235, 145, 255}
	PCAT_BLACK  = color.RGBA{0, 0, 0, 255}

	svgCache = make(map[string]*image.RGBA)

	wanInterface = "null"

	// SPI DMA mode control
	dmaMode              = true
	spiTransferOptimized = true

	dirtyTracker *DirtyRegionTracker
	renderCache  *RenderCache

	frameMutex sync.RWMutex
	// Optimized buffer manager
	bufferManager   *BufferManager
	frames          int
	dataMutex       sync.RWMutex
	dynamicData     map[string]string
	imageCache      map[string]*image.RGBA
	cfg             Config
	dftCfg          Config
	userCfg         Config
	currPageIdx     int
	fonts           map[string]FontConfig
	assetsPrefix    = "."
	globalData      sync.Map
	autoRotatePages = false

	// Frame buffer pool is now managed by BufferManager

	lastActivity   = time.Now()
	lastActivityMu sync.Mutex

	numIntermediatePages = 10

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

	desiredFPS = DEFAULT_FPS

	lastBrightness = -1

	mu          sync.Mutex
	lastLogical int         // last requested brightness (0–100)
	offTimer    *time.Timer // timer that will write 0 after delay

	smsPagesImages []*image.RGBA

	httpChangePageTriggered = false
	changePageTriggered     = false
	lastButtonPress         = time.Time{}
	buttonDebounceDelay     = 40 * time.Millisecond
	buttonPressInProgress   = false
	// Signal channel for interrupting FPS sleep on page changes
	pageChangeSignal = make(chan struct{}, 1)
	// Button timing tracking
	buttonKeydownTime = time.Time{}
	buttonKeyupTime   = time.Time{}
	stitchStartTime   = time.Time{}
	// Pre-calculation optimization variables
	isPreCalculating          = false
	preCalculatedReady        = false
	preCalculatedStitched     *image.RGBA
	preCalculatedNextIdx      = 0
	preCalculatedIsSMS        = false
	preCalculatedIsNextSMS    = false
	preCalculatedLocalIdx     = 0
	preCalculatedNextLocalIdx = 0
	preCalculationMutex       sync.RWMutex

	// Pre-allocated transition frame buffers
	transitionFrames          []*image.RGBA
	transitionFramesReady     = false
	transitionCalculating     = false
	transitionMutex           sync.RWMutex
	transitionFrameChannel    = make(chan int, numIntermediatePages)
	transitionCompleteChannel = make(chan bool, 1)
	// nextPageIdxFrameBuffer is now managed by BufferManager
	showFPS                = false
	showDetailedTiming     = true // Toggle for detailed timing output
	fps                    = 0.0
	lastUpdate             = time.Now()
	totalFrames            = 0
	stitchedFrames         = 0
	localConfigExists      = false
	stitchedFrame          *image.RGBA
	totalNumPages          = -1
	middleFrames           = 0
	topFrames              = 0
	nextPageIdxFrameBuffer *image.RGBA
	croppedFrameBuffer     *image.RGBA

	// Performance optimization
	easingLookup  []int
	cachedFPSText string
	lastFPSUpdate time.Time

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
	displayWrapper    *DisplayWrapper

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
	Size        *Size        `json:"size,omitempty"`         // for icons, if provided
	Size2       *Size        `json:"_size,omitempty"`        // sometimes provided as _size
	GraphConfig *GraphConfig `json:"graph_config,omitempty"` // for graph elements
}

// GraphConfig holds configuration for graph elements
type GraphConfig struct {
	GraphType     string `json:"graph_type"`      // e.g., "power"
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

// checkDMAAvailability checks if SPI DMA channels are available
func checkDMAAvailability() error {
	// Check for DMA channel files
	dmaRxPath := "/sys/devices/platform/soc/2ad00000.spi/dma:rx"
	dmaTxPath := "/sys/devices/platform/soc/2ad00000.spi/dma:tx"

	if _, err := os.Stat(dmaRxPath); os.IsNotExist(err) {
		return fmt.Errorf("DMA RX channel not found at %s", dmaRxPath)
	}

	if _, err := os.Stat(dmaTxPath); os.IsNotExist(err) {
		return fmt.Errorf("DMA TX channel not found at %s", dmaTxPath)
	}

	log.Printf("DMA channels found: RX=%s, TX=%s", dmaRxPath, dmaTxPath)
	return nil
}

// SPIConfig holds SPI configuration based on DMA mode
type SPIConfig struct {
	MaxTransferSize int
	UseChunking     bool
	ChunkSize       int
	BufferStrategy  string
}

// getSPIConfig returns optimized SPI configuration based on DMA availability
func getSPIConfig() SPIConfig {
	if spiTransferOptimized {
		return SPIConfig{
			MaxTransferSize: 65536, // 64KB for DMA transfers
			UseChunking:     false,
			ChunkSize:       0,
			BufferStrategy:  "dma_optimized",
		}
	} else {
		return SPIConfig{
			MaxTransferSize: 4096, // 4KB for non-DMA transfers
			UseChunking:     true,
			ChunkSize:       1024, // 1KB chunks
			BufferStrategy:  "interrupt_driven",
		}
	}
}

// logSPIMode logs the current SPI transfer mode
func logSPIMode() {
	config := getSPIConfig()
	log.Printf("SPI Transfer Mode: %s, Max Transfer: %d bytes, Chunking: %v",
		config.BufferStrategy, config.MaxTransferSize, config.UseChunking)
	if config.UseChunking {
		log.Printf("Chunk Size: %d bytes", config.ChunkSize)
	}
}

// DisplayWrapper provides optimized display operations based on DMA mode
type DisplayWrapper struct {
	device gc9307.Device
	config SPIConfig
}

// NewDisplayWrapper creates a new display wrapper with DMA optimization
func NewDisplayWrapper(device gc9307.Device) *DisplayWrapper {
	return &DisplayWrapper{
		device: device,
		config: getSPIConfig(),
	}
}

// FillRectangleWithImageOptimized optimizes image transfers based on DMA availability
func (dw *DisplayWrapper) FillRectangleWithImageOptimized(x, y, width, height int16, img *image.RGBA) {
	if dw.config.UseChunking {
		// Non-DMA mode: use smaller chunks to avoid blocking
		dw.fillRectangleChunked(x, y, width, height, img)
	} else {
		// DMA mode: send larger buffers for optimal DMA utilization
		dw.device.FillRectangleWithImage(x, y, width, height, img)
	}
}

// fillRectangleChunked breaks large transfers into smaller chunks for non-DMA mode
func (dw *DisplayWrapper) fillRectangleChunked(x, y, width, height int16, img *image.RGBA) {
	// For non-DMA mode, we can break the image into horizontal strips
	// This reduces the amount of data in each SPI transaction
	chunkHeight := int16(dw.config.ChunkSize / (int(width) * 3)) // 3 bytes per pixel (RGB)
	if chunkHeight < 1 {
		chunkHeight = 1
	}
	if chunkHeight > height {
		chunkHeight = height
	}

	for currentY := y; currentY < y+height; currentY += chunkHeight {
		remainingHeight := y + height - currentY
		if remainingHeight < chunkHeight {
			chunkHeight = remainingHeight
		}

		// Create a sub-image for this chunk
		chunkBounds := image.Rect(0, int(currentY-y), int(width), int(currentY-y+chunkHeight))
		chunkImg := img.SubImage(chunkBounds).(*image.RGBA)

		// Send this chunk
		dw.device.FillRectangleWithImage(x, currentY, width, chunkHeight, chunkImg)
	}
}

// GetTransferStats returns statistics about the current transfer mode
func (dw *DisplayWrapper) GetTransferStats() map[string]interface{} {
	return map[string]interface{}{
		"dma_enabled":       dmaMode,
		"transfer_strategy": dw.config.BufferStrategy,
		"max_transfer_size": dw.config.MaxTransferSize,
		"use_chunking":      dw.config.UseChunking,
		"chunk_size":        dw.config.ChunkSize,
	}
}

func main() {
	var wg sync.WaitGroup
	all := flag.Bool("all", false, "if set, listen on all network interfaces (0.0.0.0)")
	port := flag.Int("port", 8081, "TCP port to listen on")
	forceColdBoot := flag.Bool("force-cold-boot", false, "force showing welcome screen even on warm boot")
	useDMA := flag.Bool("dma", true, "enable DMA mode for SPI transfers (default: true)")
	flag.Parse()

	// Build the listen address:
	var addr string
	if *all {
		addr = fmt.Sprintf(":%d", *port) // all interfaces
	} else {
		addr = fmt.Sprintf("127.0.0.1:%d", *port) // localhost only
	}

	// Set DMA mode from command line flag
	dmaMode = *useDMA
	if dmaMode {
		log.Println("\033[32mSPI DMA mode: ENABLED\033[0m")
	} else {
		log.Println("\033[31mSPI DMA mode: DISABLED\033[0m")
	}

	// Check if DMA channels are available
	if err := checkDMAAvailability(); err != nil {
		log.Printf("\033[31mDMA not available, falling back to non-DMA mode: %v\033[0m", err)
		dmaMode = false
		spiTransferOptimized = false
	} else {
		spiTransferOptimized = dmaMode
		if dmaMode {
			log.Println("\033[32mDMA channels detected and enabled for optimized transfers\033[0m")
		}
	}

	// Log the current SPI configuration
	logSPIMode()

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

	conn, err := spiPort.Connect(120000*physic.KiloHertz, spi.Mode0, 8)
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
		"unit_cjk": {FontPath: assetsPrefix + "/assets/fonts/NotoSansMonoCJK-VF.ttf.ttc", FontSize: 15},
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

	// Initialize display wrapper with DMA optimization
	displayWrapper = NewDisplayWrapper(display)
	log.Printf("Display wrapper initialized with transfer stats: %+v", displayWrapper.GetTransferStats())

	// Initialize shutdown monitoring (temporarily disabled for testing)
	// This monitors system shutdown/restart before SIGTERM is sent
	// EnableDBusLogind: monitors D-Bus logind PrepareForShutdown/PrepareForSleep signals
	// EnableProcComm: monitors /proc/1/comm for init process changes
	// Both methods detect shutdown earlier than SIGTERM for instant shutdown screen display
	/*
		shutdownConfig := ShutdownMonitorConfig{
			EnableDBusLogind: true,  // Monitor D-Bus logind signals (recommended)
			EnableProcComm:   true,  // Monitor /proc/1/comm changes (backup method)
		}

		if shutdownConfig.EnableDBusLogind || shutdownConfig.EnableProcComm {
			shutdownMonitor = NewShutdownMonitor(shutdownConfig)
			if err := shutdownMonitor.Start(); err != nil {
				log.Printf("Failed to start shutdown monitoring: %v", err)
			} else {
				log.Printf("Shutdown monitoring started (D-Bus: %v, /proc/1/comm: %v)",
					shutdownConfig.EnableDBusLogind, shutdownConfig.EnableProcComm)
			}
		}
	*/

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
	go httpServer(addr)                          //listen local for http request
	go monitorKeyboard(&changePageTriggered)     // Start keyboard monitoring in a goroutine
	go monitorConsoleInput(&changePageTriggered) // Start console input monitoring in a goroutine
	go idleDimmer()                              //control backlight

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

		// Stop shutdown monitoring (temporarily disabled)
		/*
			if shutdownMonitor != nil {
				shutdownMonitor.Stop()
			}
		*/

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
	// Initialize pre-allocated transition frame buffers
	initTransitionFrameBuffers()
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

	// Track frame-by-frame performance during transition
	frameTimestamps := make([]time.Time, numIntermediatePages+1)
	copyTimings := make([]int, numIntermediatePages)
	sendTimings := make([]int, numIntermediatePages)

	if err != nil {
		log.Fatalf("Failed to load font: %v", err)
	}

	for weAreRunning {
		if middleFrames%300 == 0 { // Log less frequently
			log.Println("showsms:", cfg.ShowSms, "totalPages:", totalNumPages, "cfgPages:", cfgNumPages)
		}
		if runMainLoop {
			start := time.Now()
			if changePageTriggered || httpChangePageTriggered { //CHANGE PAGE
				if buttonPressInProgress { // Too soon, skip this press
					changePageTriggered = false
					httpChangePageTriggered = false
					continue
				}

				log.Printf("🔄 Page change called")
				now := time.Now()

				// Mark button press in progress and immediately set activity
				buttonPressInProgress = true
				lastButtonPress = now
				lastActivityMu.Lock()
				lastActivity = now
				lastActivityMu.Unlock()

				renderStart := time.Now()
				log.Printf("⚡ Rendering starts +%dus", int(renderStart.Sub(start).Microseconds()))

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

				log.Println("curr/next Idx:", currPageIdx, nextPageIdx, "json/sms/total:", cfgNumPages, lenSmsPagesImages, totalNumPages, "localIdx:", localIdx, "nextLocalIdx:", nextLocalIdx, "isSMS:", isSMS, "isNextPageSMS:", isNextPageSMS)

				clearFrame(nextPageIdxFrameBuffer, middleFrameWidth, middleFrameHeight)

				// Time the next frame rendering
				nextFrameRenderStart := time.Now()
				renderMiddle(nextPageIdxFrameBuffer, &cfg, isNextPageSMS, nextLocalIdx)
				nextFrameRenderEnd := time.Now()
				nextFrameRenderDuration := nextFrameRenderEnd.Sub(nextFrameRenderStart)

				log.Printf("📱 Render next frame took: %.1fms (page %d)",
					float64(nextFrameRenderDuration.Nanoseconds())/1000000.0, nextPageIdx)

				stitchStart := time.Now()
				stitchStartTime = stitchStart // Record stitch start for button timing

				copyImageToImageAt(stitchedFrame, middleFramebuffers[(middleFrames+1)%2], 0, 0) //current frame
				copyImageToImageAt(stitchedFrame, nextPageIdxFrameBuffer, middleFrameWidth, 0)  //next frame

				stitchEnd := time.Now()

				// Calculate timing from button events to stitch completion
				stitchDuration := stitchEnd.Sub(stitchStart)
				var keydownToStitchMs, keyupToStitchMs float64
				if !buttonKeydownTime.IsZero() {
					keydownToStitchMs = durationToMs(stitchStart.Sub(buttonKeydownTime))
				}
				if !buttonKeyupTime.IsZero() {
					keyupToStitchMs = durationToMs(stitchStart.Sub(buttonKeyupTime))
				}

				if showDetailedTiming {
					log.Printf("🔧 stitch took %.1fms | Keydown→Stitch: %.1fms, Keyup→Stitch: %.1fms",
						durationToMs(stitchDuration), keydownToStitchMs, keyupToStitchMs)
				}

				calculateTransitionFramesAsync(stitchedFrame, easingLookup)

				// Initialize frame timing tracking
				frameTimestamps[0] = time.Now() // Start of transition

				// Process all frames - use pre-calculated if ready, otherwise calculate on-demand
				for i := 1; i < numIntermediatePages; i++ {
					// Update page indices at the halfway point
					if i == numIntermediatePages/2 {
						localIdx = nextLocalIdx
						currPageIdx = nextPageIdx
						isSMS = isNextPageSMS
						nextPageLength := 0
						if isNextPageSMS {
							nextPageLength = lenSmsPagesImages
						} else {
							nextPageLength = cfgNumPages
						}
						drawFooter(display, footerFramebuffers[middleFrames%2], nextPageIdx, nextPageLength, isNextPageSMS)
					}

					// Try to use pre-calculated frame, fallback to real-time calculation
					var frameToSend *image.RGBA
					frameReady := false

					// Check if this frame is pre-calculated (non-blocking)
					transitionMutex.RLock()
					if transitionFramesReady {
						// All frames are ready
						frameToSend = transitionFrames[i]
						frameReady = true
						copyTimings[i] = 0                // Mark as pre-calculated
						if showDetailedTiming && i <= 3 { // Log first few successful async uses
							log.Printf("✅ Using pre-calculated frame %d", i)
						}
					} else {
						// Check channel for ready frames (non-blocking)
						select {
						case readyIndex := <-transitionFrameChannel:
							if readyIndex >= i {
								frameToSend = transitionFrames[i]
								frameReady = true
								copyTimings[i] = 0 // Mark as pre-calculated
								if showDetailedTiming && i <= 3 {
									log.Printf("📨 Got frame %d from async channel", i)
								}
								// Put the signal back for later frames
								select {
								case transitionFrameChannel <- readyIndex:
								default:
								}
							} else {
								// Put back and use fallback
								select {
								case transitionFrameChannel <- readyIndex:
								default:
								}
							}
						default:
							// Channel empty, use fallback
						}
					}
					transitionMutex.RUnlock()

					if !frameReady {
						// Fallback: calculate frame on-demand if not pre-calculated
						log.Printf("🔨 Frame not ready")
						copyStart := time.Now()
						xPos := easingLookup[i]
						copyImageRegion(croppedFrameBuffer, stitchedFrame, xPos, 0, middleFrameWidth, middleFrameHeight)
						copyEnd := time.Now()
						copyTimings[i] = int(copyEnd.Sub(copyStart).Microseconds())
						frameToSend = croppedFrameBuffer
					}

					// Send frame immediately
					sendStart := time.Now()
					sendMiddle(display, frameToSend)
					sendEnd := time.Now()
					sendTimings[i] = int(sendEnd.Sub(sendStart).Microseconds())

					middleFrames++
					stitchedFrames++
					frameTimestamps[i] = time.Now() // Record end time of this frame
				}
				// Record final timestamp for the last frame duration calculation
				frameTimestamps[numIntermediatePages] = time.Now()

				// Print total transition time
				transitionEnd := time.Now()
				totalTransitionDuration := transitionEnd.Sub(frameTimestamps[0])
				log.Printf("🎬 All transition frames rendered and sent in: %.1fms (%d frames)",
					float64(totalTransitionDuration.Nanoseconds())/1000000.0, numIntermediatePages-1)

				//=============== begin of performance printing ===============
				// Print detailed timing when page change animation is complete
				pageChangeEnd := time.Now()
				pageChangeDuration := pageChangeEnd.Sub(start)
				renderingDuration := pageChangeEnd.Sub(renderStart)
				setupDuration := renderStart.Sub(start)
				pageChangeFPS := float64(numIntermediatePages) / pageChangeDuration.Seconds()

				// Calculate frame-by-frame timing statistics
				var frameDurations []int
				var minFrameTime, maxFrameTime, totalFrameTime int
				var totalCopyTime, totalSendTime int
				minFrameTime = int(^uint(0) >> 1) // Max int value

				for i := 1; i < len(frameTimestamps); i++ {
					// Validate timestamps before calculating duration
					if frameTimestamps[i].IsZero() || frameTimestamps[i-1].IsZero() {
						continue
					}

					duration := frameTimestamps[i].Sub(frameTimestamps[i-1])
					frameDuration := int(duration.Microseconds())

					// Validate duration is reasonable (between 1μs and 1 second)
					if frameDuration < 1 || frameDuration > 1000000 {
						continue
					}

					frameDurations = append(frameDurations, frameDuration)
					totalFrameTime += frameDuration
					if frameDuration < minFrameTime {
						minFrameTime = frameDuration
					}
					if frameDuration > maxFrameTime {
						maxFrameTime = frameDuration
					}
				}

				// Calculate sub-operation totals
				for i := 0; i < len(copyTimings); i++ {
					totalCopyTime += copyTimings[i]
					totalSendTime += sendTimings[i]
				}

				// Handle case where no valid frames were processed
				var avgFrameTime, avgCopyTime, avgSendTime int
				if len(frameDurations) > 0 {
					avgFrameTime = totalFrameTime / len(frameDurations)
				}
				if len(copyTimings) > 0 {
					avgCopyTime = totalCopyTime / len(copyTimings)
				}
				if len(sendTimings) > 0 {
					avgSendTime = totalSendTime / len(sendTimings)
				}

				// Fix min/max values if no valid frames were processed
				if len(frameDurations) == 0 {
					minFrameTime = 0
					maxFrameTime = 0
				}

				log.Printf("✅ Page change completed +%.1fms | Setup: %.1fms, Rendering: %.1fms | FPS: %d",
					durationToMs(pageChangeDuration),
					durationToMs(setupDuration),
					durationToMs(renderingDuration),
					int(pageChangeFPS))

				log.Printf("📊 Frame timing: Avg=%.1fms, Min=%.1fms, Max=%.1fms, Total=%.1fms",
					float64(avgFrameTime)/1000.0, float64(minFrameTime)/1000.0, float64(maxFrameTime)/1000.0, float64(totalFrameTime)/1000.0)

				log.Printf("🔄 Sub-operation timing: Copy avg=%.1fms, Send avg=%.1fms, Copy total=%.1fms, Send total=%.1fms",
					float64(avgCopyTime)/1000.0, float64(avgSendTime)/1000.0, float64(totalCopyTime)/1000.0, float64(totalSendTime)/1000.0)

				// Print individual frame times for detailed analysis
				if showDetailedTiming {
					frameTimingDetails := "🎬 Per-frame times (ms): "
					for i, duration := range frameDurations {
						if i > 0 {
							frameTimingDetails += ", "
						}
						frameTimingDetails += fmt.Sprintf("%.1f", float64(duration)/1000.0)
					}
					log.Printf("%s", frameTimingDetails)
				}

				// Print detailed per-frame breakdown with pre-calculated indicator
				if showDetailedTiming {
					copyTimingDetails := "📋 Copy times (ms): "
					preCalculatedCount := 0
					for i, duration := range copyTimings {
						if i > 0 {
							copyTimingDetails += ", "
						}
						if duration == 0 {
							copyTimingDetails += "0.0*" // Mark pre-calculated frames with *
							preCalculatedCount++
						} else {
							copyTimingDetails += fmt.Sprintf("%.1f", float64(duration)/1000.0)
						}
					}
					log.Printf("%s", copyTimingDetails)
					log.Printf("⚡ Pre-calculated frames used: %d/%d (%.1f%%) - Async rendering efficiency",
						preCalculatedCount, len(copyTimings),
						float64(preCalculatedCount)/float64(len(copyTimings))*100)

					sendTimingDetails := "📡 Send times (ms): "
					for i, duration := range sendTimings {
						if i > 0 {
							sendTimingDetails += ", "
						}
						sendTimingDetails += fmt.Sprintf("%.1f", float64(duration)/1000.0)
					}
					log.Printf("%s", sendTimingDetails)
				}
				//=============== end of performance printing ===============

				// Mark button press complete
				buttonPressInProgress = false
				httpChangePageTriggered = false
				changePageTriggered = false
			} else { //normal page rendering
				// Only update top bar and footer when needed (every few frames) to save CPU
				// Top bar contains mostly static information (time, battery, signal)
				// Update it less frequently to improve performance
				if middleFrames%15 == 0 { // Update top bar every 15 frames instead of every frame
					drawTopBar(display, topBarFramebuffers[topFrames%2])
				}

				// Update footer less frequently as well, except when showing SMS
				if cfg.ShowSms && isSMS {
					if middleFrames%3 == 0 { // Even SMS footer doesn't need to update every frame
						drawFooter(display, footerFramebuffers[middleFrames%2], localIdx, len(smsPagesImages), isSMS)
					}
				} else if middleFrames%10 == 0 { // Update footer every 10 frames for non-SMS pages
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

				// stable‐FPS sleep with signal-based interruption for page changes
				if delta := (time.Second/time.Duration(desiredFPS) - time.Since(start)); delta > 0 {
					sleepDuration := time.Duration(float64(delta) * 0.99)
					select {
					case <-pageChangeSignal:
						// Page change triggered, exit sleep immediately
						if showDetailedTiming {
							log.Printf("⚡ FPS sleep interrupted for immediate page change")
						}
					case <-time.After(sleepDuration):
						// Normal sleep completion
					}
				}
			}

			if middleFrames%100 == 0 {
				if autoRotatePages {
					changePageTriggered = true
					// Signal the main loop to interrupt FPS sleep
					signalPageChange()
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
	topBar *DoubleBuffer
	middle *DoubleBuffer
	footer *DoubleBuffer
	mutex  sync.RWMutex
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

// initTransitionFrameBuffers initializes pre-allocated transition frame buffers
func initTransitionFrameBuffers() {
	transitionFrames = make([]*image.RGBA, numIntermediatePages)
	for i := 0; i < numIntermediatePages; i++ {
		transitionFrames[i] = image.NewRGBA(image.Rect(0, 0, middleFrameWidth, middleFrameHeight))
	}
	log.Printf("🎬 Initialized %d pre-allocated transition frame buffers", numIntermediatePages)
}

// calculateTransitionFramesAsync calculates all transition frames in the background
func calculateTransitionFramesAsync(stitchedFrame *image.RGBA, easingValues []int) {
	// Safety check
	if stitchedFrame == nil || len(easingValues) != numIntermediatePages {
		log.Printf("❌ calculateTransitionFramesAsync: invalid parameters")
		return
	}

	transitionMutex.Lock()
	if transitionCalculating {
		transitionMutex.Unlock()
		return // Already calculating
	}
	transitionCalculating = true
	transitionFramesReady = false
	transitionMutex.Unlock()

	go func() {
		asyncStartTime := time.Now()
		log.Printf("⚡ Starting async rendering of %d transition frames", numIntermediatePages-1)

		defer func() {
			asyncEndTime := time.Now()
			asyncTotalDuration := asyncEndTime.Sub(asyncStartTime)
			log.Printf("🎭 Async rendering completed in %.1fms (%d frames, avg %.1fms per frame)",
				float64(asyncTotalDuration.Nanoseconds())/1000000.0,
				numIntermediatePages-1,
				float64(asyncTotalDuration.Nanoseconds())/float64(numIntermediatePages-1)/1000000.0)

			transitionMutex.Lock()
			transitionCalculating = false
			transitionFramesReady = true
			transitionMutex.Unlock()

			// Signal completion
			select {
			case transitionCompleteChannel <- true:
			default:
			}
		}()

		// Clear any existing channel data
		for len(transitionFrameChannel) > 0 {
			<-transitionFrameChannel
		}

		var frameTimes []float64
		// Pre-calculate transition frames starting from frame 1
		for i := 1; i < numIntermediatePages; i++ {
			frameRenderStart := time.Now()

			// Safety bounds check
			if i >= len(transitionFrames) || i >= len(easingValues) {
				log.Printf("❌ Frame index %d out of bounds", i)
				continue
			}

			xPos := easingValues[i]
			copyImageRegion(transitionFrames[i], stitchedFrame, xPos, 0, middleFrameWidth, middleFrameHeight)

			frameRenderEnd := time.Now()
			frameRenderDuration := frameRenderEnd.Sub(frameRenderStart)
			frameTimes = append(frameTimes, float64(frameRenderDuration.Nanoseconds())/1000000.0)

			// Log individual frame timing for detailed analysis (optional)
			if showDetailedTiming && i <= 5 { // Only log first 5 frames to avoid spam
				log.Printf("🖼️  Async frame %d rendered in %.1fms (x-pos: %d)",
					i, float64(frameRenderDuration.Nanoseconds())/1000000.0, xPos)
			}

			// Signal that this frame is ready (non-blocking)
			select {
			case transitionFrameChannel <- i:
			default:
				log.Printf("⚠️ Channel full, frame %d signal dropped", i)
			}
		}

		// Log timing statistics
		if len(frameTimes) > 0 {
			var minTime, maxTime, totalTime float64
			minTime = frameTimes[0]
			maxTime = frameTimes[0]
			for _, t := range frameTimes {
				totalTime += t
				if t < minTime {
					minTime = t
				}
				if t > maxTime {
					maxTime = t
				}
			}
			avgTime := totalTime / float64(len(frameTimes))
			log.Printf("📊 Async frame timing: Min=%.1fms, Max=%.1fms, Avg=%.1fms, Total=%.1fms",
				minTime, maxTime, avgTime, totalTime)
		}
	}()
}

// Helper function to convert duration to milliseconds with 1 decimal place
func durationToMs(d time.Duration) float64 {
	return float64(d.Nanoseconds()) / 1000000.0
}

// Helper function to format timing as milliseconds string
func formatTiming(d time.Duration) string {
	return fmt.Sprintf("%.1fms", durationToMs(d))
}

// Helper function to signal page change for FPS sleep interruption
func signalPageChange() {
	select {
	case pageChangeSignal <- struct{}{}:
	default: // Channel full, signal already sent
	}
}
