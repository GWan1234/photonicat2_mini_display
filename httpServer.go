package main

import (
	"bytes"
	"image"
	"image/png"
	"log"
	"strconv"

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

func indexHandler(c *fiber.Ctx) error {
	return c.SendFile("assets/html/index.html")
}

func httpServer() {
	app := fiber.New()

	// Routes
	app.Get("/", indexHandler)
	app.Get("/frame", serveFrame)
	app.Post("/data", updateData)

	// Start server
	port := ":8081"
	log.Println("Starting Fiber server on", port)
	log.Fatal(app.Listen(port))
}
