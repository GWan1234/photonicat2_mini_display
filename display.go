package main

import (
	st7789 "photonicat2_display/periph.io-st7789"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"fmt"
	"time"
	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/conn/v3/physic"
	"periph.io/x/conn/v3/spi"
	"periph.io/x/conn/v3/spi/spireg"
	"periph.io/x/host/v3"
)

const (
	RST_PIN = "GPIO122"
	DC_PIN = "GPIO121"
	CS_PIN = "GPIO13"
	BL_PIN = "GPIO117"
)


func main() {
	// setup board
	if _, err := host.Init(); err != nil {
		log.Fatal(err)
	}

	// setup connection to display
	spiPort, err := spireg.Open("SPI1.0")

	if err != nil {
		log.Fatal(err)
	}

	defer spiPort.Close()

	conn, err := spiPort.Connect(80000*physic.KiloHertz, spi.Mode0, 8)

	if err != nil {
		log.Fatal(err)
	}
	//gpio enable pins


	//bus spi.Conn, resetPin, dcPin, csPin, blPin gpio.PinOut
	display := st7789.New(conn,
		gpioreg.ByName(RST_PIN),
		gpioreg.ByName(DC_PIN),
		gpioreg.ByName("GPIO0"),
		gpioreg.ByName(BL_PIN))

	display.Configure(st7789.Config{
		Width:        172,
		Height:       320,
		Rotation:     st7789.ROTATION_180,
		RowOffset:    0,
		ColumnOffset: 34,
		FrameRate:    st7789.FRAMERATE_60,
		VSyncLines:   st7789.MAX_VSYNC_SCANLINES,
	})

	// test display
	display.EnableBacklight(false)
	fps := 0
	frames := 0
	startTime := time.Now()

	for {
		
		displayClock(display, 0, 0)
		displayPNG(display, 0, 0, "example.png")
		frames++
		//calc fps
		elapsedTime := time.Since(startTime)
		fps = int(float64(frames) / elapsedTime.Seconds())
		fmt.Println("FPS:", fps)
	}
}

func displayClock(display st7789.Device, x int16, y int16) {
	display.FillRectangle(x, y, 172, 320, color.RGBA{R: 132, G: 22, B: 0, A: 255})
}

func displayPNG(display st7789.Device, x int, y int, filePath string) {
	// read and parse image file
	image.RegisterFormat("png", "png", png.Decode, png.DecodeConfig)
	imgFile, err := os.Open(filePath)
	if err != nil {
		log.Fatal(err)
	}
	defer imgFile.Close()

	img, _, err := image.Decode(imgFile)
	if err != nil {
		log.Fatal(err)
	}

	// convert image to slice of colors
	bounds := img.Bounds()
	width, height := bounds.Max.X, bounds.Max.Y
	buffer := make([]color.RGBA, height*width)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// get pixel color and convert channels from int32 to int8
			r, g, b, a := img.At(x, y).RGBA()
			buffer[y*width+x] = color.RGBA{R: uint8(r / 0x100), G: uint8(g / 0x100), B: uint8(b / 0x100), A: uint8(a / 0x100)}
		}
	}

	// send image buffer to display
	err = display.FillRectangleWithBuffer(int16(x), int16(y), int16(width), int16(height), buffer)
	if err != nil {
		fmt.Println(err)
	}

}
