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
	"encoding/json"
	"io/ioutil"

	"github.com/gofiber/fiber/v2"
)


var (
	drawMu sync.Mutex
	webFrame *image.RGBA
	configMutex   sync.RWMutex
	defaultConfig Config                  // loaded from default_config.json
	userOverrides map[string]interface{}  // raw overrides from user_config.json
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

// mergeConfig rebuilds `cfg` by overlaying userOverrides on defaultConfig.
func mergeConfig() {
	// Convert defaultConfig struct to a map
	defMap := make(map[string]interface{})
	b, _ := json.Marshal(defaultConfig)
	json.Unmarshal(b, &defMap)

	// Deep-merge overrides into that map
	mergedMap := deepMerge(defMap, userOverrides)

	// Marshal back into Config struct
	b2, err := json.Marshal(mergedMap)
	if err != nil {
		log.Printf("warning: could not marshal merged config: %v", err)
		return
	}
	if err := json.Unmarshal(b2, &cfg); err != nil {
		log.Printf("warning: could not unmarshal merged config into struct: %v", err)
	}
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
	mergeConfig()

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


func httpServer(port string) {
	app := fiber.New()

	// Routes
	app.Get("/", indexHandler)
	app.Get("/api/v1/frame.png", serveFrame)
	app.Get("/api/v1/data.json", getData) //TODO: add content
	app.Post("/api/v1/data.json", updateData) //TODO: add content
	app.Get("/api/v1/changePage", changePage)
	//new
	app.Get("/api/v1/get_default_config.json", getDefaultConfig)
	app.Get("/api/v1/get_config.json", getConfig)
	app.Post("/api/v1/set_config.json", setConfig)
	app.Get("/api/v1/get_status.json", getStatus)
	
	app.Get("/api/v1/display_text.json", httpDrawText)
	app.Get("/api/v1/make_it_run", makeItRun)

	app.Get("/api/v1/reset_config", resetConfig)


	// Start server
	log.Println("Starting Fiber server on", port)
	log.Fatal(app.Listen(port))
}
