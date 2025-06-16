package main

import (
	"bytes"
	"image"
	"image/png"
	"log"
	"strconv"
	"time"
	"github.com/gofiber/fiber/v2"
)

var webFrame *image.RGBA

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

func updateData(c *fiber.Ctx) error {
	if c.Method() != fiber.MethodPost {
		return c.Status(fiber.StatusMethodNotAllowed).SendString("Method not allowed")
	}

	var data map[string]string
	err := c.BodyParser(&data)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid JSON")
	}

	dataMutex.Lock()
	for k, v := range data {
		dynamicData[k] = v
	}
	dataMutex.Unlock()

	return c.SendString("Data updated")
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
	dataMutex.RLock()
	defer dataMutex.RUnlock()
	return c.JSON(globalData)
}


func getDefaultConfig(c *fiber.Ctx) error {
	return c.JSON(cfg)
}

func getConfig(c *fiber.Ctx) error {
	return c.JSON(cfg)
}

func setConfig(c *fiber.Ctx) error {
	var payload map[string]string
	if err := c.BodyParser(&payload); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid JSON")
	}
	//cfg = payload
	return c.JSON(fiber.Map{"status": "ok"})
}

func getStatus(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

func resetConfig(c *fiber.Ctx) error {
	//TODO: cfg = defaultConfig
	return c.JSON(fiber.Map{"status": "ok"})
}




func httpServer(port string) {
	app := fiber.New()

	// Routes
	app.Get("/", indexHandler)
	app.Get("/api/v1/frame.png", serveFrame)
	app.Get("/api/v1/data.json", getData)
	app.Post("/api/v1/data.json", updateData)
	app.Get("/api/v1/changePage", changePage)
	//new
	app.Get("/api/v1/get_default_config.json", getDefaultConfig)
	app.Get("/api/v1/get_config.json", getConfig)
	app.Post("/api/v1/set_config.json", setConfig)
	app.Get("/api/v1/get_status.json", getStatus)

	app.Get("/api/v1/reset_config", resetConfig)


	// Start server
	log.Println("Starting Fiber server on", port)
	log.Fatal(app.Listen(port))
}
