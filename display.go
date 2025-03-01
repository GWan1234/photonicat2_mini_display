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

type ImageBuffer struct {
    buffer []color.RGBA
    width  int
    height int
    loaded bool
}

var cachedImage ImageBuffer
const (
	PCAT2_LCD_WIDTH = 172
	PCAT2_LCD_HEIGHT = 320
	PCAT2_X_OFFSET = 34
	PCAT2_L_MARGIN = 10
	PCAT2_R_MARGIN = 10
	PCAT2_T_MARGIN = 10
	PCAT2_B_MARGIN = 10
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

	conn, err := spiPort.Connect(100000*physic.KiloHertz, spi.Mode0, 8)

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
		Width:        PCAT2_LCD_WIDTH,
		Height:       PCAT2_LCD_HEIGHT,
		Rotation:     st7789.ROTATION_180,
		RowOffset:    0,
		ColumnOffset: PCAT2_X_OFFSET,
		FrameRate:    st7789.FRAMERATE_60,
		VSyncLines:   st7789.MAX_VSYNC_SCANLINES,
		UseCS:        false,
	})

	// test display
	display.EnableBacklight(false)
	fps := 0
	frames := 0
	startTime := time.Now()

	//make 2 framebuffers
	framebuffers := make([][]color.RGBA, 2)
	framebuffers[0] = make([]color.RGBA, (PCAT2_LCD_WIDTH-PCAT2_L_MARGIN-PCAT2_R_MARGIN)*(PCAT2_LCD_HEIGHT-PCAT2_T_MARGIN-PCAT2_B_MARGIN))
	framebuffers[1] = make([]color.RGBA, (PCAT2_LCD_WIDTH-PCAT2_L_MARGIN-PCAT2_R_MARGIN)*(PCAT2_LCD_HEIGHT-PCAT2_T_MARGIN-PCAT2_B_MARGIN))

	for {
		//displayClock(display, 0, 0)
		//displayPNG(display, 0, 0, "example.png")
		display.FillRectangleWithBuffer(PCAT2_L_MARGIN, PCAT2_T_MARGIN, PCAT2_LCD_WIDTH-PCAT2_L_MARGIN-PCAT2_R_MARGIN, PCAT2_LCD_HEIGHT-PCAT2_T_MARGIN-PCAT2_B_MARGIN, framebuffers[0])
		frames++
		//calc fps
		if frames % 10 == 0 {
			elapsedTime := time.Since(startTime)
			fps = int(float64(frames) / elapsedTime.Seconds())
			fmt.Printf("FPS: %d, FRAMES: %d\n", fps, frames)
		}
	}
}




func displayClock(display st7789.Device, x int16, y int16) {
	display.FillRectangle(x, y, 172, 320, color.RGBA{R: 132, G: 22, B: 0, A: 255})
}

func displayPNG(display st7789.Device, x int, y int, filePath string) {
	// read and parse image file
	if !cachedImage.loaded {
        // Register format once (could be moved to init())
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

        bounds := img.Bounds()
        cachedImage.width, cachedImage.height = bounds.Max.X, bounds.Max.Y
        cachedImage.buffer = make([]color.RGBA, cachedImage.height*cachedImage.width)

        for y := 0; y < cachedImage.height; y++ {
            for x := 0; x < cachedImage.width; x++ {
                r, g, b, a := img.At(x, y).RGBA()
                cachedImage.buffer[y*cachedImage.width+x] = color.RGBA{
                    R: uint8(r / 0x100), G: uint8(g / 0x100), B: uint8(b / 0x100), A: uint8(a / 0x100),
                }
            }
        }
        cachedImage.loaded = true
    }

	// send image buffer to display
	err := display.FillRectangleWithBuffer(int16(x), int16(y), int16(cachedImage.width), int16(cachedImage.height), cachedImage.buffer)
	if err != nil {
		fmt.Println(err)
	}

}
