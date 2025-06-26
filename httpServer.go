package main

import (
	"bytes"
	"image"
	"image/png"
	"log"
	"strconv"
	"time"
	"math"
	"sync"
	"math/rand"
	"image/color"
	"os"
	"encoding/json"
	"io/ioutil"
    "net"
    "slices"
    "strings"

	"github.com/gofiber/fiber/v2"
)


var (
	drawMu sync.Mutex
	webFrame *image.RGBA
	configMutex   sync.RWMutex
	defaultConfig Config                  // loaded from default_config.json
	userOverrides map[string]interface{}  // raw overrides from user_config.json
	userJsonConfig = ""
)


func serveFrame(c *fiber.Ctx) error {
	var err error
	var buf bytes.Buffer

	if webFrame == nil {
		webFrame = image.NewRGBA(image.Rect(0, 0, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT))
		drawRect(webFrame, 0, 0, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT, PCAT_BLACK)
	}

	frameMutex.RLock()
	// Composite the frames
	err = copyImageToImageAt(webFrame, topBarFramebuffers[0], 0, 0)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Failed to copy top bar frame")
	}

	err = copyImageToImageAt(webFrame, middleFramebuffers[0], 0, PCAT2_TOP_BAR_HEIGHT)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Failed to copy middle frame")
	}

	err = copyImageToImageAt(webFrame, footerFramebuffers[frames%2], 0, PCAT2_LCD_HEIGHT-PCAT2_FOOTER_HEIGHT)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Failed to copy footer frame")
	}
	frameMutex.RUnlock()

	if webFrame == nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("No frame available")
	}

	err = png.Encode(&buf, webFrame)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Failed to encode image")
	}

	c.Set("Content-Type", "image/png")
	c.Set("Content-Length", strconv.Itoa(buf.Len()))
	return c.Send(buf.Bytes())
}

// simple index
func indexHandler(c *fiber.Ctx) error {
	return c.SendFile("assets/html/index.html")
}

// GET  /api/v1/changePage
func changePage(c *fiber.Ctx) error {
	lastActivityMu.Lock()
	httpChangePageTriggered = true
	lastActivity = time.Now()
	lastActivityMu.Unlock()
	return c.JSON(fiber.Map{"status": "page change triggered"})
}

// GET  /api/v1/data.json
func getData(c *fiber.Ctx) error {
    // 1) Build a plain map from the sync.Map
    out := make(map[string]interface{})

    globalData.Range(func(key, value interface{}) bool {
        // assume your keys are strings
        if ks, ok := key.(string); ok {
            out[ks] = value
        }
        return true // continue iteration
    })

    // 2) Return that map as JSON
    return c.JSON(out)
}


// loadUserConfig reads existing file into globalData
func loadUserConfig() string {
	if userJsonConfig != "" {
		return userJsonConfig
	}
    path := ETC_USER_CONFIG_PATH
    raw, err := ioutil.ReadFile(path)
	
    if err != nil {
        if os.IsNotExist(err) {
            log.Printf("no existing user config at %s, starting fresh", path)
        } else {
            log.Printf("error reading user config: %v", err)
        }
		userJsonConfig = "{}"
        userCfg = Config{}
        return "{}"
    }

    var m map[string]string
    if err := json.Unmarshal(raw, &m); err != nil {
        log.Printf("error parsing user config JSON: %v", err)
		userJsonConfig = "{}"
        userCfg = Config{}
		return "{}"
    }

    for k, v := range m {
        globalData.Store(k, v)
    }
    log.Printf("loaded %d entries from user config", len(m))
	userJsonConfig = string(raw)
	return userJsonConfig
}

// saveUserConfig writes the payload map back to disk
func saveUserConfigFromWeb(payload map[string]string) {
	return //not using this now.
    path := ETC_USER_CONFIG_PATH

    // marshal with nice indentation
    data, err := json.MarshalIndent(payload, "", "  ")
    if err != nil {
        log.Printf("could not marshal user config: %v", err)
        return
    }

    // write atomically
    tmp := path + ".tmp"
    if err := ioutil.WriteFile(tmp, data, 0644); err != nil {
        log.Printf("could not write temp user config: %v", err)
        return
    }
    if err := os.Rename(tmp, path); err != nil {
        log.Printf("could not rename temp config file: %v", err)
    }
}


// POST /api/v1/data
func updateData(c *fiber.Ctx) error {
    // 1. Parse the JSON body into a map[string]string
    var payload map[string]string
    if err := c.BodyParser(&payload); err != nil {
        return c.
            Status(fiber.StatusBadRequest).
            JSON(fiber.Map{"error": "invalid JSON"})
    }

    // 2. Store each entry into the sync.Map
    for k, v := range payload {
        globalData.Store(k, v)
    }

    // 3. Return a success response
    return c.JSON(fiber.Map{"status": "ok"})
}

func getDefaultConfig(c *fiber.Ctx) error {
	return c.JSON(cfg)
}

// GET  /api/v1/get_user_config.json
func getUserConfig(c *fiber.Ctx) error {
    configMutex.RLock()
    defer configMutex.RUnlock()

    // Return the raw overrides that the user has set
    return c.SendString(loadUserConfig())
}




// POST /api/v1/set_user_config.json
func setUserConfig(c *fiber.Ctx) error {
    // 1) Parse incoming JSON into a generic map
    var payload map[string]interface{}
    if err := c.BodyParser(&payload); err != nil {
        return c.
            Status(fiber.StatusBadRequest).
            JSON(fiber.Map{"error": "invalid JSON"})
    }

    // 2) Merge into the in-memory overrides under lock
    configMutex.Lock()
    userOverrides = deepMerge(userOverrides, payload)
    configMutex.Unlock()

    // 3) Persist back to disk
    raw, err := json.MarshalIndent(userOverrides, "", "  ")
    if err != nil {
        log.Printf("warning: could not marshal user_config.json: %v", err)
        return c.
            Status(fiber.StatusInternalServerError).
            JSON(fiber.Map{"error": "could not save config"})
    }
    if err := ioutil.WriteFile("config/user_config.json", raw, 0644); err != nil {
        log.Printf("warning: could not write user_config.json: %v", err)
        return c.
            Status(fiber.StatusInternalServerError).
            JSON(fiber.Map{"error": "could not save config"})
    }

    // 4) Rebuild merged cfg
    mergeConfigs()

    return c.JSON(fiber.Map{"status": "ok"})
}


// GET /api/v1/get_config.json
func getConfig(c *fiber.Ctx) error {
	configMutex.RLock()
	defer configMutex.RUnlock()
	return c.JSON(cfg)
}

// POST /api/v1/set_config.json
func setConfig(c *fiber.Ctx) error {
	// Parse incoming JSON as generic map
	var payload map[string]interface{}
	if err := c.BodyParser(&payload); err != nil {
		return c.Status(fiber.StatusBadRequest).
			JSON(fiber.Map{"error": "invalid JSON"})
	}

	configMutex.Lock()
	defer configMutex.Unlock()

	// Merge new values into overrides
	userOverrides = deepMerge(userOverrides, payload)

	// Persist userOverrides back to disk
	if raw, err := json.MarshalIndent(userOverrides, "", "  "); err != nil {
		log.Printf("warning: could not marshal user_config.json: %v", err)
	} else if err := ioutil.WriteFile("config/user_config.json", raw, 0644); err != nil {
		log.Printf("warning: could not write user_config.json: %v", err)
	}

	// Rebuild merged cfg
	mergeConfigs()

	return c.JSON(fiber.Map{"status": "ok"})
}

// deepMerge merges src into dest (in-place) for nested maps
// deepMerge merges src into dest (in-place) for nested maps, initializing dest if nil
func deepMerge(dest, src map[string]interface{}) map[string]interface{} {
	if dest == nil {
		dest = make(map[string]interface{}, len(src))
	}
	for k, v := range src {
		if vMap, ok := v.(map[string]interface{}); ok {
			// merge nested map
			nested, found := dest[k].(map[string]interface{})
			if !found || nested == nil {
				nested = make(map[string]interface{}, len(vMap))
			}
			dest[k] = deepMerge(nested, vMap)
		} else {
			// override primitive or slice
			dest[k] = v
		}
	}
	return dest
}

// deepCopy returns a deep copy of a map[string]interface{}
func deepCopy(src map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(src))
	for k, v := range src {
		if m, ok := v.(map[string]interface{}); ok {
			copy[k] = deepCopy(m)
		} else {
			copy[k] = v
		}
	}
	return copy
}



func getStatus(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

func resetConfig(c *fiber.Ctx) error {
	//TODO: cfg = defaultConfig
	return c.JSON(fiber.Map{"status": "ok"})
}

// hsvToRgb converts h∈[0,1], s∈[0,1], v∈[0,1] to r,g,b∈[0,1].
func hsvToRgb(h, s, v float64) (r, g, b float64) {
    i := int(h * 6)
    f := h*6 - float64(i)
    p := v * (1 - s)
    q := v * (1 - f*s)
    t := v * (1 - (1-f)*s)
    switch i % 6 {
    case 0:
        r, g, b = v, t, p
    case 1:
        r, g, b = q, v, p
    case 2:
        r, g, b = p, v, t
    case 3:
        r, g, b = p, q, v
    case 4:
        r, g, b = t, p, v
    case 5:
        r, g, b = v, p, q
    }
    return
}

func httpDrawText(c *fiber.Ctx) error {
    // Acquire lock (blocks if another request is drawing)
	now := time.Now().Format("2025-01-01 15:04:05")
    drawMu.Lock()
    defer drawMu.Unlock()

    // 1) Grab the "text" query (empty if missing)
    text := c.Query("text", "")

    // 2) Load your tiny font
    faceTiny, _, err := getFontFace("tiny")
    if err != nil {
        log.Fatalf("Failed to load font: %v", err)
    }

	faceHuge, _, err := getFontFace("huge")
    if err != nil {
        log.Fatalf("Failed to load font: %v", err)
    }
	
	
    // 3) Pause your main‐loop so it won’t overwrite
    runMainLoop = false

    // 4) Prepare a blank frame
    width, height := 172, 320
    frame := image.NewRGBA(image.Rect(0, 0, width, height))

    if text != "" {
        // Draw the provided text centered
        drawText(frame, text, width/2, height/2, faceHuge, PCAT_WHITE, true)
    } else {
        // Seed randomness for a fresh pattern
        rand.Seed(time.Now().UnixNano())
        hueOffset := rand.Float64()
        phase     := rand.Float64() * 2 * math.Pi
        freq      := 0.01 + rand.Float64()*0.09

        cx, cy := float64(width)/2, float64(height)/2
        for x := 0; x < width; x++ {
            for y := 0; y < height; y++ {
                dx, dy := float64(x)-cx, float64(y)-cy
                angle   := math.Atan2(dy, dx)
                hue     := math.Mod((angle+math.Pi)/(2*math.Pi)+hueOffset, 1.0)
                dist    := math.Hypot(dx, dy)
                val     := 0.5 + 0.5*math.Sin(dist*freq+phase)

                rF, gF, bF := hsvToRgb(hue, 1.0, val)
                frame.Set(x, y, color.RGBA{
                    R: uint8(rF * 255),
                    G: uint8(gF * 255),
                    B: uint8(bF * 255),
                    A: 255,
                })
            }
        }

        // Overlay current time
        
		drawText(frame, "no text provided", width/2, height/2-20, faceTiny, PCAT_WHITE, true)
        drawText(frame, now, width/2, height/2, faceTiny, PCAT_WHITE, true)
    }

    // 5) Push to display
	time.Sleep(50 * time.Millisecond) //wait other goroutine to finish, TODO use mutex
    sendFull(display, frame)

    // 6) JSON response
    return c.JSON(fiber.Map{
        "status": "ok",
        "text":   text,
		"time":   now,
    })
}

func makeItRun(c *fiber.Ctx) error {
	weAreRunning = true
	runMainLoop = true
	return c.JSON(fiber.Map{"status": "ok"})
}


func setPingSites(c *fiber.Ctx) error {
    // update in-memory
    configMutex.Lock()
    userCfg.PingSite0 = c.FormValue("ping_site0")
    userCfg.PingSite1 = c.FormValue("ping_site1")
    saveUserConfigToFile()     // persist
    configMutex.Unlock()

    return c.JSON(fiber.Map{"status": "ok"})
}

func setScreenDimmerTime(c *fiber.Ctx) error {
    onBatterySeconds := c.FormValue("screen_dimmer_time_on_battery_seconds")
    onDCSeconds := c.FormValue("screen_dimmer_time_on_dc_seconds")

    onBatterySecondsInt, err := strconv.Atoi(onBatterySeconds)
    if err != nil {
        return c.Status(400).JSON(fiber.Map{"status": "error", "message": "Invalid screen_dimmer_time_on_battery_seconds"})
    }

    onDCSecondsInt, err := strconv.Atoi(onDCSeconds)
    if err != nil {
        return c.Status(400).JSON(fiber.Map{"status": "error", "message": "Invalid screen_dimmer_time_on_dc_seconds"})
    }

    configMutex.Lock()
    userCfg.ScreenDimmerTimeOnBatterySeconds = onBatterySecondsInt
    userCfg.ScreenDimmerTimeOnDCSeconds = onDCSecondsInt
    saveUserConfigToFile()
    configMutex.Unlock()

    return c.JSON(fiber.Map{"status": "ok"})
}

func setShowSMS(c *fiber.Ctx) error {
    raw := c.FormValue("showSMS")
    
    valid_values := []string{"true", "false"}

    if !slices.Contains(valid_values, raw) {
        return c.Status(400).JSON(fiber.Map{
            "status":  "error",
            "message": "showSMS must be boolean",
        })
    }

    configMutex.Lock()
    userCfg.ShowSms = (strings.ToLower(raw) == "true")
    saveUserConfigToFile()
    configMutex.Unlock()

    return c.JSON(fiber.Map{"status": "ok", "showSMS": raw})
}

func httpServer(port string) {
	app := fiber.New()

	// Routes
	app.Get("/", indexHandler)
	app.Get("/api/v1/go_frame.png", serveFrame)
	app.Get("/api/v1/go_data.json", getData) //TODO: add content
	app.Post("/api/v1/go_data.json", updateData) //TODO: add content
	app.Get("/api/v1/go_changePage", changePage)
    app.Get("/api/v1/go_display_text.json", httpDrawText)
	app.Get("/api/v1/go_make_it_run", makeItRun)
	//get/set configs (json)
	app.Get("/api/v1/go_get_default_config.json", getDefaultConfig)
	app.Get("/api/v1/go_get_config.json", getConfig)
	app.Get("/api/v1/go_get_user_config.json", getUserConfig)
	app.Post("/api/v1/go_set_user_config.json", setUserConfig)
	app.Get("/api/v1/go_get_status.json", getStatus)
    app.Get("/api/v1/go_reset_config", resetConfig)
	
    //get/set individual configs
    app.Post("/api/v1/go_set_ping_sites", setPingSites)
    app.Post("/api/v1/go_set_screen_dimmer_time", setScreenDimmerTime)
    app.Post("/api/v1/go_set_show_sms", setShowSMS)

	// Start server, retry if failed
	var ln net.Listener
    var err error
    for {
        ln, err = net.Listen("tcp", port)
        if err != nil {
            log.Printf("cannot bind to %s: %v — retrying in 2s…", port, err)
            time.Sleep(2 * time.Second)
            continue
        }
        break
    }

    log.Println("Successfully bound to", port)
    log.Fatal(app.Listener(ln))
}
