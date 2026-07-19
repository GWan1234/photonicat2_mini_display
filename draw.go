package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"image/jpeg"
	"image/gif"
	"io"
	"log"
	"net/http"
	"os"
	"time"
	"bytes"
	"math"
	"math/rand"
	"strings"
	"strconv"
	"path/filepath"
	"regexp"
	gc9307 "github.com/photonicat/periph.io-gc9307"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"github.com/ajstarks/svgo"
	"github.com/llgcode/draw2d/draw2dimg"
)

var (
	cacheTopBarStr string
	cacheTopBar *image.RGBA
	cacheFooterStr string
	cacheFooter *image.RGBA
)

//---------------- Drawing Functions ----------------
func drawText(img *image.RGBA, text string, posX, posY int, face font.Face, clr color.Color, center bool) (finishX, finishY int) {
    // Check if image is nil or has invalid bounds
    if img == nil || img.Bounds().Empty() {
        return posX, posY
    }
    
    d := &font.Drawer{
        Dst:  img,
        Src:  image.NewUniform(clr),
        Face: face,
    }

    // Get font metrics once.
    metrics := face.Metrics()

    // Calculate text dimensions.
    textWidth := d.MeasureString(text).Round()
    textHeight := (metrics.Ascent + metrics.Descent).Round()
    var x, y int
    if center {
        // Center horizontally: shift x left by half the text width.
        x = posX - textWidth/2
        // Center vertically: shift y up by half the text height, then add ascent for baseline.
        y = posY - textHeight/2 - metrics.Ascent.Round() 
		//we still use the same y
		//y = posY
    } else {
        x = posX
    }
	y = posY + metrics.Ascent.Round()

    // Bounds checking - only prevent extreme out-of-bounds cases
    bounds := img.Bounds()
    // Only skip if text would be completely outside the image
    if x + textWidth < bounds.Min.X || x > bounds.Max.X {
        return posX, posY
    }
    if y < bounds.Min.Y - metrics.Descent.Round() || y - metrics.Ascent.Round() > bounds.Max.Y {
        return posX, posY
    }

    // Set drawing position and draw the text with error recovery
    d.Dot = fixed.P(x, y)
    
    // Add a recovery mechanism for font rendering panics
    defer func() {
        if r := recover(); r != nil {
            log.Printf("Font rendering panic recovered: %v at position (%d, %d) in bounds %+v", r, x, y, bounds)
        }
    }()
    
    d.DrawString(text)

    // Calculate finishing coordinates.
    finishX = x + textWidth
    finishY = y - metrics.Ascent.Round() + textHeight // Bottom of the text block.

    return
}

// drawVerticalText draws text with each rune stacked on its own line, reading
// top-to-bottom (e.g. "CPU" as C / P / U). It is used for the compact axis-style
// labels beside the CPU and memory bar meters. (x, y) is the top-left of the
// column; letters are horizontally centered within charW. lineH is the vertical
// step between letters.
func drawVerticalText(frame *image.RGBA, text string, x, y, charW, lineH int, face font.Face, clr color.Color) {
	if frame == nil || text == "" {
		return
	}
	d := &font.Drawer{Face: face}
	cy := y
	for _, r := range text {
		s := string(r)
		w := d.MeasureString(s).Round()
		// Center each glyph in the column width.
		gx := x + (charW-w)/2
		drawText(frame, s, gx, cy, face, clr, false)
		cy += lineH
	}
}

//---------------- Horizontal ticker (marquee) scrolling ----------------
//
// Long values (e.g. a Wi-Fi SSID that is wider than the 172px screen) would
// otherwise be clipped at the right edge. drawScrollingText renders such a
// value as a NASDAQ-style ticker: the text pauses briefly, then scrolls left
// at a constant pixel-per-second rate and wraps around seamlessly, so every
// character is eventually readable. Text that already fits is drawn in place,
// unchanged.
//
// Scrolling is driven off wall-clock time (not the frame counter) so the
// motion speed is identical regardless of the render FPS; the main loop only
// controls how *smooth* it looks by choosing how often to re-render.

const (
	// scrollSpeedPxPerSec is how fast the ticker slides left. ~40 px/s reads
	// like a stock ticker: fast enough to get through a long SSID quickly,
	// slow enough to actually read.
	scrollSpeedPxPerSec = 40.0
	// scrollStartPauseMs holds the text still at the start of each loop so the
	// beginning (the most important part) is readable before it moves.
	scrollStartPauseMs = 1200.0
	// scrollGapPx is the blank space between the end of the text and the start
	// of its wrapped-around copy, so the loop seam is visually obvious.
	scrollGapPx = 24
)

// scrollEpoch anchors the ticker time base. Set once on first use so all
// tickers share a phase and elapsed time never depends on process start
// details. time.Now() is fine here (this is the running app, not a workflow).
var scrollEpoch time.Time

// anyTextScrolling is set true by drawScrollingText whenever it renders a
// value that is actually overflowing (and thus animating) during the current
// render pass. The main loop reads and resets it to decide whether to bump the
// frame rate for smoothness. It is written and read from the single render
// goroutine, so no locking is required.
var anyTextScrolling bool

// drawScrollingText draws text at (x, y). availWidth is the horizontal space
// the text may occupy (from x to the clip edge). If the text fits, it is drawn
// normally and false is returned. If it overflows, it is drawn as a clipped,
// wrapping ticker and true is returned. clr is the text color; face its font.
func drawScrollingText(frame *image.RGBA, text string, x, y, availWidth int, face font.Face, clr color.Color) bool {
	if frame == nil || text == "" || availWidth <= 0 {
		drawText(frame, text, x, y, face, clr, false)
		return false
	}

	d := &font.Drawer{Face: face}
	textW := d.MeasureString(text).Round()

	// Fits comfortably: draw in place, no scrolling.
	if textW <= availWidth {
		drawText(frame, text, x, y, face, clr, false)
		return false
	}

	if scrollEpoch.IsZero() {
		scrollEpoch = time.Now()
	}

	metrics := face.Metrics()
	ascent := metrics.Ascent.Round()
	regionH := ascent + metrics.Descent.Round()
	if regionH <= 0 {
		regionH = 1
	}

	// One full loop = the text plus a trailing gap; wrapping this distance
	// brings the identical second copy exactly into the first's start.
	loopW := textW + scrollGapPx

	// Time within the current loop: a stationary start pause followed by the
	// slide. Using milliseconds keeps the math integer-friendly.
	scrollMs := (float64(loopW) / scrollSpeedPxPerSec) * 1000.0
	cycleMs := scrollStartPauseMs + scrollMs
	elapsed := math.Mod(float64(time.Since(scrollEpoch).Milliseconds()), cycleMs)

	var offset int // how many px the text is shifted left
	if elapsed > scrollStartPauseMs {
		offset = int(((elapsed - scrollStartPauseMs) / 1000.0) * scrollSpeedPxPerSec)
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= loopW {
		offset = offset % loopW
	}

	// Render into a region-sized scratch image so the text is clipped to
	// [x, x+availWidth) and never paints over neighbouring elements. Draw two
	// copies (loopW apart) so the wrap is seamless.
	region := image.NewRGBA(image.Rect(0, 0, availWidth, regionH))
	src := image.NewUniform(clr)
	rd := &font.Drawer{Dst: region, Src: src, Face: face}
	baseline := ascent
	rd.Dot = fixed.P(-offset, baseline)
	rd.DrawString(text)
	// Second copy trails the first by loopW; it scrolls in from the right as
	// the first leaves on the left.
	rd.Dot = fixed.P(-offset+loopW, baseline)
	rd.DrawString(text)

	// Blit the clipped region into the frame at the element position. Use Over
	// so anti-aliased edges blend; the region's transparent pixels leave the
	// background untouched.
	dstRect := image.Rect(x, y, x+availWidth, y+regionH)
	draw.Draw(frame, dstRect, region, image.Point{}, draw.Over)

	anyTextScrolling = true
	return true
}

// remoteImageCacheDir is where downloaded remote icons are stored on disk.
const remoteImageCacheDir = "/tmp/pcat_remote_icons"

// fetchRemoteImage downloads an http(s) image URL to a deterministic local
// file (named by the URL hash, keeping the original extension) and returns the
// local path. If the file already exists on disk it is reused without
// re-downloading, so restarts are cheap.
func fetchRemoteImage(url string) (string, error) {
	ext := strings.ToLower(filepath.Ext(url))
	if ext == "" {
		ext = ".svg" // assume SVG when the URL carries no extension
	}
	sum := sha1.Sum([]byte(url))
	dest := filepath.Join(remoteImageCacheDir, hex.EncodeToString(sum[:])+ext)

	if _, err := os.Stat(dest); err == nil {
		return dest, nil // already cached on disk
	}

	if err := os.MkdirAll(remoteImageCacheDir, 0o755); err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", getUserAgent())
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Write atomically via a temp file so a partial download is never read.
	tmp := dest + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, io.LimitReader(resp.Body, 8<<20)); err != nil { // cap 8 MiB
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	out.Close()
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return dest, nil
}

// placeholderRe matches [key] tokens in fixed_text labels.
var placeholderRe = regexp.MustCompile(`\[(\w+)\]`)

func loadImage(filePath string) (*image.RGBA, int, int, error) {
	// Check if image is in cache (keyed by the original path/URL, so a remote
	// icon is only downloaded and rendered once per process).
	imageCacheMu.RLock()
	cachedImg, inCache := imageCache[filePath]
	imageCacheMu.RUnlock()
	if inCache {
		bounds := cachedImg.Bounds()
		return cachedImg, bounds.Dx(), bounds.Dy(), nil
	}

	// Remote icon support: an http(s):// icon_path is downloaded to a local
	// cache file, then decoded by the normal path below. The firmware itself
	// has no networked file system, so the fetch happens here once.
	localPath := filePath
	if strings.HasPrefix(filePath, "http://") || strings.HasPrefix(filePath, "https://") {
		p, err := fetchRemoteImage(filePath)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("fetch remote image %q: %w", filePath, err)
		}
		localPath = p
	}

	ext := strings.ToLower(filepath.Ext(localPath))

	// Open the file.
	f, err := os.Open(localPath)
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()

	var img image.Image

	switch ext {
	case ".png":
		img, err = png.Decode(f)
		if err != nil {
			return nil, 0, 0, err
		}
	case ".jpg", ".jpeg":
		img, err = jpeg.Decode(f)
		if err != nil {
			return nil, 0, 0, err
		}
	case ".gif":
		img, err = gif.Decode(f)
		if err != nil {
			return nil, 0, 0, err
		}
	case ".svg":
		// Check if SVG is already cached as rendered image
		cacheKey := filePath + "_rendered"
		imageCacheMu.RLock()
		cachedSvg, svgCached := imageCache[cacheKey]
		imageCacheMu.RUnlock()
		if svgCached {
			bounds := cachedSvg.Bounds()
			return cachedSvg, bounds.Dx(), bounds.Dy(), nil
		}
		
		// Read the entire SVG file.
		svgData, err := io.ReadAll(f)
		if err != nil {
			return nil, 0, 0, err
		}
		// Decode the SVG using oksvg.
		icon, err := oksvg.ReadIconStream(bytes.NewReader(svgData))
		if err != nil {
			return nil, 0, 0, err
		}
		// Determine intrinsic dimensions.
		w := int(icon.ViewBox.W)
		h := int(icon.ViewBox.H)
		// Create an RGBA image to serve as the rendering canvas.
		rgba := image.NewRGBA(image.Rect(0, 0, w, h))
		// Clear the canvas with a fully transparent color.
		draw.Draw(rgba, rgba.Bounds(), image.NewUniform(color.RGBA{0, 0, 0, 0}), image.Point{}, draw.Src)
		// Set the target dimensions.
		icon.SetTarget(0, 0, float64(w), float64(h))
		// Create a scanner and dasher for rendering.
		scanner := rasterx.NewScannerGV(w, h, rgba, rgba.Bounds())
		dasher := rasterx.NewDasher(w, h, scanner)
		// Render the SVG onto the RGBA image.
		icon.Draw(dasher, 1.0)
		// Cache and return the rendered image.
		imageCacheMu.Lock()
		imageCache[cacheKey] = rgba
		imageCache[filePath] = rgba // Also cache with original path for fast lookup
		imageCacheMu.Unlock()
		return rgba, w, h, nil
	default:
		return nil, 0, 0, fmt.Errorf("unsupported image format: %s", ext)
	}

	// Convert the decoded image to RGBA if needed.
	bounds := img.Bounds()
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)
	// Cache the image.
	imageCacheMu.Lock()
	imageCache[filePath] = rgba
	imageCacheMu.Unlock()
	return rgba, bounds.Dx(), bounds.Dy(), nil
}


// copyImageToFrameBuffer converts an image.RGBA to a 1D []color.RGBA slice.
func copyImageToFrameBuffer(img *image.RGBA, frame []color.RGBA) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := y*width + x
			frame[idx] = img.RGBAAt(x, y)
		}
	}
}

func sendTopBar(display gc9307.Device, frame *image.RGBA) {
	// Early exit for nil frames
	if frame == nil {
		return
	}
	
	// Early exit for empty frames
	if frame.Bounds().Empty() {
		return
	}
	
	if displayWrapper != nil {
		displayWrapper.FillRectangleWithImageOptimized(0, 0, topBarSendWidth, topBarSendHeight, frame)
	} else {
		display.FillRectangleWithImage(0, 0, topBarSendWidth, topBarSendHeight, frame)
	}
}

func sendFooter(display gc9307.Device, frame *image.RGBA) {
	// Early exit for nil frames
	if frame == nil {
		return
	}
	
	// Early exit for empty frames
	if frame.Bounds().Empty() {
		return
	}
	
	if displayWrapper != nil {
		displayWrapper.FillRectangleWithImageOptimized(0, footerSendY, footerSendWidth, footerSendHeight, frame)
	} else {
		display.FillRectangleWithImage(0, footerSendY, footerSendWidth, footerSendHeight, frame)
	}
}

// cropToContent scans the given frame and returns a sub-image that contains only non-background pixels.
func cropToContent(frame *image.RGBA, bgColor color.Color) *image.RGBA {
	bounds := frame.Bounds()
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y

	// Loop over all pixels in the image.
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if !isBackground(frame.At(x, y), bgColor) {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}

	// No content found? Return an empty image.
	if minX > maxX || minY > maxY {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}

	// Create the cropping rectangle.
	cropRect := image.Rect(minX, minY, maxX+1, maxY+1)
	// Use SubImage to create a new image containing only the cropped area.
	return frame.SubImage(cropRect).(*image.RGBA)
}

// isBackground compares a pixel to the given background color.
func isBackground(c color.Color, bg color.Color) bool {
	_, _, _, a1 := c.RGBA()
	//r2, g2, b2, a2 := bg.RGBA()
	return a1 == 0
}


func sendMiddlePartial(display gc9307.Device, frame *image.RGBA) {
	// Crop the frame to the region with content.
	croppedFrame := cropToContent(frame, color.Black) // assuming black is the background
	if croppedFrame.Bounds().Empty() {
		// Nothing to send.
		return
	}

	// Send the cropped frame to the display.
	// Here we use the cropped image's dimensions.
	if displayWrapper != nil {
		displayWrapper.FillRectangleWithImageOptimized(
			int16(croppedFrame.Bounds().Min.X),
			int16(croppedFrame.Bounds().Min.Y),
			int16(croppedFrame.Bounds().Dx()),
			int16(croppedFrame.Bounds().Dy()),
			croppedFrame,
		)
	} else {
		display.FillRectangleWithImage(
			int16(croppedFrame.Bounds().Min.X),
			int16(croppedFrame.Bounds().Min.Y),
			int16(croppedFrame.Bounds().Dx()),
			int16(croppedFrame.Bounds().Dy()),
			croppedFrame,
		)
	}
}

// Pre-calculated constants for send functions performance
var (
	// Middle area
	middleSendY      = int16(PCAT2_TOP_BAR_HEIGHT)
	middleSendWidth  = int16(PCAT2_LCD_WIDTH)
	middleSendHeight = int16(PCAT2_LCD_HEIGHT - PCAT2_TOP_BAR_HEIGHT - PCAT2_FOOTER_HEIGHT)
	
	// Top bar area
	topBarSendWidth  = int16(PCAT2_LCD_WIDTH)
	topBarSendHeight = int16(PCAT2_TOP_BAR_HEIGHT)
	
	// Footer area
	footerSendY      = int16(PCAT2_LCD_HEIGHT - PCAT2_FOOTER_HEIGHT)
	footerSendWidth  = int16(PCAT2_LCD_WIDTH)
	footerSendHeight = int16(PCAT2_FOOTER_HEIGHT)
	
	// Full area
	fullSendWidth    = int16(PCAT2_LCD_WIDTH)
	fullSendHeight   = int16(PCAT2_LCD_HEIGHT)
)

// sendMiddle sends the middle frame area with performance optimizations
func sendMiddle(display gc9307.Device, frame *image.RGBA) {
	// Early exit for nil frames
	if frame == nil {
		return
	}
	
	// Early exit for empty frames
	bounds := frame.Bounds()
	if bounds.Empty() {
		return
	}
	
	// Use optimized path with pre-calculated constants
	if displayWrapper != nil {
		displayWrapper.FillRectangleWithImageOptimized(0, middleSendY, middleSendWidth, middleSendHeight, frame)
	} else {
		display.FillRectangleWithImage(0, middleSendY, middleSendWidth, middleSendHeight, frame)
	}
}

// Global variable to store the last sent middle frame for comparison
var lastMiddleFrame *image.RGBA

// areFramesIdentical performs a fast comparison of two frames
func areFramesIdentical(frame1, frame2 *image.RGBA) bool {
	if frame1 == nil || frame2 == nil {
		return frame1 == frame2
	}
	
	bounds1 := frame1.Bounds()
	bounds2 := frame2.Bounds()
	
	// Quick bounds check
	if bounds1 != bounds2 {
		return false
	}
	
	// Quick pixel data length check
	if len(frame1.Pix) != len(frame2.Pix) {
		return false
	}
	
	// Fast byte-by-byte comparison of pixel data
	pix1 := frame1.Pix
	pix2 := frame2.Pix
	
	// Compare in chunks for better performance
	chunkSize := 1024 // Compare 1KB chunks at a time
	for i := 0; i < len(pix1); i += chunkSize {
		end := i + chunkSize
		if end > len(pix1) {
			end = len(pix1)
		}
		
		// Compare chunk
		for j := i; j < end; j++ {
			if pix1[j] != pix2[j] {
				return false
			}
		}
	}
	
	return true
}

// sendMiddleOptimized sends middle frame only if it has changed from the last frame
func sendMiddleOptimized(display gc9307.Device, frame *image.RGBA) {
	// Early exit for nil frames
	if frame == nil {
		return
	}
	
	// Early exit for empty frames
	bounds := frame.Bounds()
	if bounds.Empty() {
		return
	}
	
	// Skip sending if frame is identical to the last one
	if lastMiddleFrame != nil && areFramesIdentical(frame, lastMiddleFrame) {
		return // Frame unchanged, skip send
	}
	
	// Send the frame
	sendMiddle(display, frame)
	
	// Store a copy of this frame for next comparison
	// Note: This creates a memory copy - consider using a hash instead for memory efficiency
	if lastMiddleFrame == nil || !lastMiddleFrame.Bounds().Eq(bounds) {
		lastMiddleFrame = image.NewRGBA(bounds)
	}
	copy(lastMiddleFrame.Pix, frame.Pix)
}

func sendFull(display gc9307.Device, frame *image.RGBA) {
	// Early exit for nil frames
	if frame == nil {
		return
	}
	
	// Early exit for empty frames
	if frame.Bounds().Empty() {
		return
	}
	
	if displayWrapper != nil {
		displayWrapper.FillRectangleWithImageOptimized(0, 0, fullSendWidth, fullSendHeight, frame)
	} else {
		display.FillRectangleWithImage(0, 0, fullSendWidth, fullSendHeight, frame)
	}
}

// Function to display time on frame buffer
func testClock(frame *image.RGBA) {
    
    // Get current time and format it
    currDateTime := time.Now()
    currHour := currDateTime.Hour()
    currMinute := currDateTime.Minute()
    currSecond := currDateTime.Second()
    currMilli := currDateTime.Nanosecond() / 1000000 // Convert nanoseconds to milliseconds
    currDay := currDateTime.Day()
    currMonth := currDateTime.Month()
    currYear := currDateTime.Year()

    // Format the time as hh:mm:ss:SSS
    timeStr := fmt.Sprintf("%02d:%02d:%02d:%03d", currHour, currMinute, currSecond, currMilli)
    dateStr := fmt.Sprintf("%04d-%02d-%02d", currYear, currMonth, currDay)
    
    // Get font face for big time display
    face, _, err := getFontFace("big")
    if err != nil {
        fmt.Println("Error loading font:", err)
        return
    }

    // Clear the frame to black (optional, or use a background color)
    draw.Draw(frame, frame.Bounds(), &image.Uniform{color.Black}, image.Point{}, draw.Src)

    // Set the text color to white
    textColor := color.RGBA{255, 229, 0, 255}
    randomColor := color.RGBA{
        R: uint8(rand.Intn(256)),
        G: uint8(rand.Intn(256)),
        B: uint8(rand.Intn(256)),
        A: uint8(rand.Intn(256)),
    }

    // Draw the formatted time string onto the image
    drawText(frame, dateStr, 0, 0, face, textColor, false)
    drawText(frame, timeStr, 0, 30, face, randomColor, false)
}

func drawSVG(frame *image.RGBA, svgPath string, x0, y0, targetWidth, targetHeight int) error {
	// If target dimensions are zero, we need to load the SVG to obtain its intrinsic size.
	if targetWidth == 0 || targetHeight == 0 {
		svgFile, err := os.Open(svgPath)
		if err != nil {
			return err
		}
		defer svgFile.Close()
		
		svgData, err := io.ReadAll(svgFile)
		if err != nil {
			return err
		}
		
		icon, err := oksvg.ReadIconStream(bytes.NewReader(svgData))
		if err != nil {
			return err
		}
		if targetWidth == 0 {
			targetWidth = int(icon.ViewBox.W)
		}
		if targetHeight == 0 {
			targetHeight = int(icon.ViewBox.H)
		}
	}
	
	// Build a cache key: svgPath + "_" + targetWidth + "_" + targetHeight.
	cacheKey := fmt.Sprintf("%s_%d_%d", svgPath, targetWidth, targetHeight)
	
	// Check if we already have a cached rendered image.
	if cachedImg, ok := svgCache[cacheKey]; ok {
		copyImageToImageAt(frame, cachedImg, x0, y0)
		return nil
	}

	// Not in cache, so load and render the SVG.
	svgFile, err := os.Open(svgPath)
	if err != nil {
		return err
	}
	defer svgFile.Close()

	svgData, err := io.ReadAll(svgFile)
	if err != nil {
		return err
	}

	icon, err := oksvg.ReadIconStream(bytes.NewReader(svgData))
	if err != nil {
		return err
	}

	// Set the target dimensions for the SVG rendering.
	icon.SetTarget(0, 0, float64(targetWidth), float64(targetHeight))

	// Create an RGBA image to serve as the rendering canvas.
	img := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))

	// Set up the rasterizer context.
	scanner := rasterx.NewScannerGV(targetWidth, targetHeight, img, img.Bounds())
	dasher := rasterx.NewDasher(targetWidth, targetHeight, scanner)

	// Render the SVG onto the image.
	icon.Draw(dasher, 1.0)

	// Cache the rendered image.
	svgCache[cacheKey] = img

	// Copy the rendered image into the frame buffer at the specified offset.
	copyImageToImageAt(frame, img, x0, y0)

	return nil
}

// renderSvgBytes rasterizes in-memory SVG data at its intrinsic size, so
// generated graphics (signal bars, boot progress bar) never touch the
// filesystem. If cacheKey is non-empty the rendered image is stored in
// imageCache under that key for reuse.
func renderSvgBytes(svgData []byte, cacheKey string) (*image.RGBA, error) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(svgData))
	if err != nil {
		return nil, err
	}
	w := int(icon.ViewBox.W)
	h := int(icon.ViewBox.H)
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	icon.SetTarget(0, 0, float64(w), float64(h))
	scanner := rasterx.NewScannerGV(w, h, rgba, rgba.Bounds())
	dasher := rasterx.NewDasher(w, h, scanner)
	icon.Draw(dasher, 1.0)
	if cacheKey != "" {
		imageCacheMu.Lock()
		imageCache[cacheKey] = rgba
		imageCacheMu.Unlock()
	}
	return rgba, nil
}

// cropImageAt crops the given src image starting at (x0, y0) with the specified width and height.
// It returns a new *image.RGBA whose bounds begin at (0,0).
func cropImageAt(src *image.RGBA, x0, y0, width, height int) *image.RGBA {
	// Get source image bounds.
	srcBounds := src.Bounds()
	// Optionally, clamp the cropping rectangle if it exceeds src bounds.
	if x0 < srcBounds.Min.X {
		x0 = srcBounds.Min.X
	}
	if y0 < srcBounds.Min.Y {
		y0 = srcBounds.Min.Y
	}
	if x0+width > srcBounds.Max.X {
		width = srcBounds.Max.X - x0
	}
	if y0+height > srcBounds.Max.Y {
		height = srcBounds.Max.Y - y0
	}
	// Define the source rectangle to crop.
	srcRect := image.Rect(x0, y0, x0+width, y0+height)
	// Create a new RGBA image with bounds starting at (0,0).
	cropped := image.NewRGBA(image.Rect(0, 0, width, height))
	// Copy the source rectangle into the new image.
	draw.Draw(cropped, cropped.Bounds(), src, srcRect.Min, draw.Src)
	return cropped
}

// copyImageToImageAt copies an image to an image at a specified offset. frame is the destination image, img is the source image. x0, y0 is the offset.
func copyImageToImageAt(frame *image.RGBA, img *image.RGBA, x0, y0 int) error {
	// Validate input parameters first.
	if frame == nil || img == nil {
		return fmt.Errorf("nil image provided")
	}

	targetWidth := img.Bounds().Dx()
	targetHeight := img.Bounds().Dy()

	// Check bounds.
	if x0 < 0 || y0 < 0 {
		return fmt.Errorf("x, y is negative: %d,%d", x0, y0)
	}

	imgBounds := img.Bounds()
	frameBounds := frame.Bounds()
	frameStride := frame.Stride
	imgStride := img.Stride
	
	// Ultra-fast path: bulk memory copy for opaque images with optimal alignment
	if isFullyOpaque(img) && x0 == 0 && targetWidth == frameBounds.Dx() && targetWidth == imgBounds.Dx() {
		// Perfect alignment - copy entire rows at once
		srcStart := (imgBounds.Min.Y * imgStride) + (imgBounds.Min.X * 4)
		dstStart := (y0 * frameStride)
		rowSize := targetWidth * 4 // 4 bytes per RGBA pixel
		
		for y := 0; y < targetHeight; y++ {
			if y0+y >= frameBounds.Min.Y && y0+y < frameBounds.Max.Y {
				srcOffset := srcStart + (y * imgStride)
				dstOffset := dstStart + (y * frameStride)
				
				// Add bounds checking to prevent panic
				if srcOffset >= 0 && srcOffset+rowSize <= len(img.Pix) &&
				   dstOffset >= 0 && dstOffset+rowSize <= len(frame.Pix) {
					copy(frame.Pix[dstOffset:dstOffset+rowSize], img.Pix[srcOffset:srcOffset+rowSize])
				} else {
					// Log error and use safe fallback
					log.Printf("⚠️ Ultra-fast path bounds error: y=%d, src[%d:%d] (cap:%d), dst[%d:%d] (cap:%d)",
						y, srcOffset, srcOffset+rowSize, len(img.Pix),
						dstOffset, dstOffset+rowSize, len(frame.Pix))
					
					// Safe pixel-by-pixel fallback for this row
					for x := 0; x < targetWidth; x++ {
						srcX := imgBounds.Min.X + x
						dstX := x0 + x
						if srcX >= imgBounds.Min.X && srcX < imgBounds.Max.X &&
						   dstX >= frameBounds.Min.X && dstX < frameBounds.Max.X {
							frame.SetRGBA(dstX, y0+y, img.RGBAAt(srcX, imgBounds.Min.Y+y))
						}
					}
				}
			}
		}
		return nil
	}
	
	// Fast path for fully opaque images - row-wise copy when possible
	if isFullyOpaque(img) {
		for y := 0; y < targetHeight; y++ {
			srcY := imgBounds.Min.Y + y
			dstY := y0 + y
			
			if dstY >= frameBounds.Min.Y && dstY < frameBounds.Max.Y {
				// Calculate byte offsets for this row
				srcRowStart := (srcY * imgStride) + (imgBounds.Min.X * 4)
				dstRowStart := (dstY * frameStride) + (x0 * 4)
				rowByteSize := targetWidth * 4
				
				// Bounds check for destination and buffer capacity
				if x0 >= frameBounds.Min.X && x0+targetWidth <= frameBounds.Max.X &&
				   srcRowStart >= 0 && srcRowStart+rowByteSize <= len(img.Pix) &&
				   dstRowStart >= 0 && dstRowStart+rowByteSize <= len(frame.Pix) {
					// Safe to copy entire row at once
					copy(frame.Pix[dstRowStart:dstRowStart+rowByteSize], 
						 img.Pix[srcRowStart:srcRowStart+rowByteSize])
				} else {
					// Log bounds error if it's a buffer capacity issue
					if srcRowStart+rowByteSize > len(img.Pix) || dstRowStart+rowByteSize > len(frame.Pix) {
						log.Printf("⚠️ Fast path bounds error: y=%d, src[%d:%d] (cap:%d), dst[%d:%d] (cap:%d)",
							y, srcRowStart, srcRowStart+rowByteSize, len(img.Pix),
							dstRowStart, dstRowStart+rowByteSize, len(frame.Pix))
					}
					// Pixel-by-pixel fallback for edge cases
					for x := 0; x < targetWidth; x++ {
						srcX := imgBounds.Min.X + x
						dstX := x0 + x
						
						if srcX >= imgBounds.Min.X && srcX < imgBounds.Max.X &&
						   dstX >= frameBounds.Min.X && dstX < frameBounds.Max.X {
							frame.SetRGBA(dstX, dstY, img.RGBAAt(srcX, srcY))
						}
					}
				}
			}
		}
		return nil
	}

	// Fallback for images with transparency - use alpha blending
	for y := 0; y < targetHeight; y++ {
		for x := 0; x < targetWidth; x++ {
			sample := img.RGBAAt(x, y)
			// Skip fully transparent pixels.
			if sample.A == 0 {
				continue
			}

			// Get the destination pixel.
			dst := frame.RGBAAt(x0+x, y0+y)
			if sample.A == 255 {
				// Fully opaque: copy sample pixel directly.
				frame.SetRGBA(x0+x, y0+y, sample)
			} else {
				// Mix sample and destination pixels.
				a := uint16(sample.A)
				invA := uint16(255 - sample.A)
				outR := (uint16(sample.R)*a + uint16(dst.R)*invA) / 255
				outG := (uint16(sample.G)*a + uint16(dst.G)*invA) / 255
				outB := (uint16(sample.B)*a + uint16(dst.B)*invA) / 255
				// For the alpha channel, use the over operator:
				// outA = sample.A + dst.A*(255-sample.A)/255
				outA := uint8(uint16(sample.A) + (uint16(dst.A)*invA)/255)
				frame.SetRGBA(x0+x, y0+y, color.RGBA{
					R: uint8(outR),
					G: uint8(outG),
					B: uint8(outB),
					A: outA,
				})
			}
		}
	}

	return nil
}

// stitchFramesOptimized combines two frames horizontally in a single operation for maximum performance
func stitchFramesOptimized(dst *image.RGBA, leftFrame, rightFrame *image.RGBA) error {
	// Validate inputs
	if dst == nil || leftFrame == nil || rightFrame == nil {
		return fmt.Errorf("nil image provided to stitchFramesOptimized")
	}
	
	leftBounds := leftFrame.Bounds()
	rightBounds := rightFrame.Bounds()
	dstBounds := dst.Bounds()
	
	leftWidth := leftBounds.Dx()
	leftHeight := leftBounds.Dy()
	rightWidth := rightBounds.Dx()
	rightHeight := rightBounds.Dy()
	
	// Ensure frames are the same height and fit in destination
	if leftHeight != rightHeight {
		return fmt.Errorf("frames must have same height: left=%d, right=%d", leftHeight, rightHeight)
	}
	if leftWidth + rightWidth > dstBounds.Dx() || leftHeight > dstBounds.Dy() {
		return fmt.Errorf("combined frames exceed destination bounds")
	}
	
	// Ultra-fast path: if both frames have simple bounds starting at (0,0), use bulk copy
	if leftBounds.Min.X == 0 && leftBounds.Min.Y == 0 && rightBounds.Min.X == 0 && rightBounds.Min.Y == 0 &&
	   leftFrame.Stride == leftWidth*4 && rightFrame.Stride == rightWidth*4 && dst.Stride == (leftWidth+rightWidth)*4 {
		
		// Interleave copy: copy rows from both frames simultaneously
		for y := 0; y < leftHeight; y++ {
			leftRowStart := y * leftWidth * 4
			rightRowStart := y * rightWidth * 4
			dstRowStart := y * (leftWidth + rightWidth) * 4
			
			// Copy left frame row
			copy(dst.Pix[dstRowStart:dstRowStart+leftWidth*4], 
				 leftFrame.Pix[leftRowStart:leftRowStart+leftWidth*4])
			
			// Copy right frame row immediately after
			copy(dst.Pix[dstRowStart+leftWidth*4:dstRowStart+leftWidth*4+rightWidth*4], 
				 rightFrame.Pix[rightRowStart:rightRowStart+rightWidth*4])
		}
		return nil
	}
	
	// Standard path with stride calculations (fallback for complex bounds)
	leftStride := leftFrame.Stride
	rightStride := rightFrame.Stride
	dstStride := dst.Stride
	
	// Copy both frames with proper stride handling
	for y := 0; y < leftHeight; y++ {
		// Source row offsets
		leftSrcRow := (leftBounds.Min.Y + y) * leftStride + leftBounds.Min.X * 4
		rightSrcRow := (rightBounds.Min.Y + y) * rightStride + rightBounds.Min.X * 4
		
		// Destination row offsets
		dstLeftRow := y * dstStride
		dstRightRow := y * dstStride + leftWidth * 4
		
		// Bounds checking
		if leftSrcRow + leftWidth*4 <= len(leftFrame.Pix) &&
		   rightSrcRow + rightWidth*4 <= len(rightFrame.Pix) &&
		   dstLeftRow + leftWidth*4 <= len(dst.Pix) &&
		   dstRightRow + rightWidth*4 <= len(dst.Pix) {
			
			// Copy left frame row
			copy(dst.Pix[dstLeftRow:dstLeftRow+leftWidth*4], 
				 leftFrame.Pix[leftSrcRow:leftSrcRow+leftWidth*4])
			
			// Copy right frame row immediately after
			copy(dst.Pix[dstRightRow:dstRightRow+rightWidth*4], 
				 rightFrame.Pix[rightSrcRow:rightSrcRow+rightWidth*4])
		}
	}
	
	return nil
}

// isFullyOpaque checks if an image is fully opaque (no transparency)
func isFullyOpaque(img *image.RGBA) bool {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if img.RGBAAt(x, y).A < 255 {
				return false
			}
		}
	}
	return true
}

// copyImageRegion efficiently copies a region from src to dst using bulk memory operations
func copyImageRegion(dst *image.RGBA, src *image.RGBA, x0, y0, width, height int) {
	srcBounds := src.Bounds()
	dstBounds := dst.Bounds()
	
	// Clamp to valid bounds
	if x0 < srcBounds.Min.X {
		width -= srcBounds.Min.X - x0
		x0 = srcBounds.Min.X
	}
	if y0 < srcBounds.Min.Y {
		height -= srcBounds.Min.Y - y0
		y0 = srcBounds.Min.Y
	}
	
	if x0+width > srcBounds.Max.X {
		width = srcBounds.Max.X - x0
	}
	if y0+height > srcBounds.Max.Y {
		height = srcBounds.Max.Y - y0
	}
	
	// Early return if invalid region
	if width <= 0 || height <= 0 {
		return
	}
	
	// Ultra-fast bulk memory copy using direct Pix access
	srcStride := src.Stride
	dstStride := dst.Stride
	rowByteSize := width * 4 // 4 bytes per RGBA pixel
	
	for y := 0; y < height; y++ {
		srcY := y0 + y
		dstY := y
		
		if srcY >= srcBounds.Min.Y && srcY < srcBounds.Max.Y && 
		   dstY >= dstBounds.Min.Y && dstY < dstBounds.Max.Y {
			
			// Calculate byte offsets for this row
			srcOffset := (srcY * srcStride) + (x0 * 4)
			dstOffset := (dstY * dstStride)
			
			// Bounds check to ensure we don't go out of bounds
			if srcOffset >= 0 && srcOffset+rowByteSize <= len(src.Pix) &&
			   dstOffset >= 0 && dstOffset+rowByteSize <= len(dst.Pix) {
				// Ultra-fast row copy using built-in copy function
				copy(dst.Pix[dstOffset:dstOffset+rowByteSize], 
					 src.Pix[srcOffset:srcOffset+rowByteSize])
			} else {
				// Fallback to pixel-by-pixel for edge cases (shouldn't happen in normal use)
				for x := 0; x < width; x++ {
					srcX := x0 + x
					dstX := x
					
					if srcX >= srcBounds.Min.X && srcX < srcBounds.Max.X &&
					   dstX >= dstBounds.Min.X && dstX < dstBounds.Max.X {
						dst.SetRGBA(dstX, dstY, src.RGBAAt(srcX, srcY))
					}
				}
			}
		}
	}
}

func drawRoundedRect(gc *draw2dimg.GraphicContext, x, y, w, h, r float64) {
	// Start at the top-left corner, offset by the radius.
	gc.MoveTo(x+r, y)
	// Draw top edge.
	gc.LineTo(x+w-r, y)
	// Top-right arc.
	gc.ArcTo(x+w-r, y+r, r, r, -90, 90)
	// Right edge.
	gc.LineTo(x+w, y+h-r)
	// Bottom-right arc.
	gc.ArcTo(x+w-r, y+h-r, r, r, 0, 90)
	// Bottom edge.
	gc.LineTo(x+r, y+h)
	// Bottom-left arc.
	gc.ArcTo(x+r, y+h-r, r, r, 90, 90)
	// Left edge.
	gc.LineTo(x, y+r)
	// Top-left arc.
	gc.ArcTo(x+r, y+r, r, r, 180, 90)
	gc.Close()
}

func drawRect(img *image.RGBA, x0, y0, width, height int, c color.Color) {
    // Convert the color.Color to a color.RGBA.
    r, g, b, a := c.RGBA()
    // The RGBA() method returns values in the range [0, 65535],
    // so we need to shift them to [0, 255].
    col := color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}

    for x := x0; x < x0+width; x++ {
        for y := y0; y < y0+height; y++ {
            img.SetRGBA(x, y, col)
        }
    }
}

func drawSignalStrength(frame *image.RGBA, x0, y0 int, strength float64) {
	xBarSize := 5
	yBarSize := 15
	barSpace := 1
	numBars := 4
	yMinHeight := 6
	strengthInt := int(math.Ceil(strength * 4))
	cacheKey := "gen:strength-" + strconv.Itoa(strengthInt)

	imageCacheMu.RLock()
	img, cached := imageCache[cacheKey]
	imageCacheMu.RUnlock()
	if !cached {
		var buf bytes.Buffer
		canvas := svg.New(&buf)
		canvas.Start(xBarSize*numBars+barSpace*(numBars-1), yBarSize+yMinHeight)

		for i := 0; i < numBars; i++ {
			if i < strengthInt {
				canvas.Roundrect(i*xBarSize+i*barSpace, yBarSize/4*(4-i), xBarSize, yBarSize/4*i+yMinHeight, 2, 2, "fill:white")
			}else{
				fillColorHex := fmt.Sprintf("#%02X%02X%02X", PCAT_GREY.R, PCAT_GREY.G, PCAT_GREY.B)
				canvas.Roundrect(i*xBarSize+i*barSpace, yBarSize/4*(4-i), xBarSize, yBarSize/4*i+yMinHeight, 2, 2, "fill:" + fillColorHex)
			}
		}
		canvas.End()

		var err error
		img, err = renderSvgBytes(buf.Bytes(), cacheKey)
		if err != nil {
			panic(err)
		}
	}
	copyImageToImageAt(frame, img, x0, y0)
}

// Bar-chart styling shared by the CPU and memory meters. Colors reuse the boot
// progress bar's palette (yellow fill on a grey track); the corner radii echo
// its rounded look, scaled down for the small in-page bars.
var (
	barFillHex  = fmt.Sprintf("#%02X%02X%02X", PCAT_YELLOW.R, PCAT_YELLOW.G, PCAT_YELLOW.B)
	barTrackHex = fmt.Sprintf("#%02X%02X%02X", PCAT_GREY.R, PCAT_GREY.G, PCAT_GREY.B)
	// iStat-menu look: track sits on our black background with only a thin
	// grey frame drawn to show the edge of each bar.
	barBgHex = fmt.Sprintf("#%02X%02X%02X", PCAT_BLACK.R, PCAT_BLACK.G, PCAT_BLACK.B)
)

const (
	barFrameRadius = 3 // outer frame corner radius for the CPU box
	barBarRadius   = 2 // per-core vertical bar corner radius
	hbarRadius     = 5 // horizontal bar corner radius (matches boot progress bar)
)

// drawCpuBars renders a framed box of vertical bars — one per CPU core — at
// (x0, y0) with total size w×h. Each bar is a full-height grey track with a
// yellow fill rising from the bottom in proportion to that core's usage
// (0-100). The whole thing is cached keyed on the quantized usages so it only
// re-rasterizes when the reading actually changes.
func drawCpuBars(frame *image.RGBA, x0, y0, w, h int, usages []float64) {
	numCores := len(usages)
	if numCores == 0 || w <= 0 || h <= 0 {
		return
	}

	// Build a cache key from usage buckets (5% granularity) so steady load
	// reuses the rendered image instead of rasterizing every frame.
	var keyBuf strings.Builder
	keyBuf.WriteString("gen:cpubars:")
	keyBuf.WriteString(strconv.Itoa(w))
	keyBuf.WriteByte('x')
	keyBuf.WriteString(strconv.Itoa(h))
	for _, u := range usages {
		bucket := int(u) / 5
		keyBuf.WriteByte(':')
		keyBuf.WriteString(strconv.Itoa(bucket))
	}
	cacheKey := keyBuf.String()

	imageCacheMu.RLock()
	img, cached := imageCache[cacheKey]
	imageCacheMu.RUnlock()
	if !cached {
		var buf bytes.Buffer
		canvas := svg.New(&buf)
		canvas.Start(w, h)

		// Background fill + outer frame: our black bg with a thin grey outline
		// (iStat-menu style) so the box edge reads against the page.
		canvas.Roundrect(0, 0, w-1, h-1, barFrameRadius, barFrameRadius,
			"fill:"+barBgHex+";stroke:"+barTrackHex+";stroke-width:1")

		// Inner drawing area, inset from the frame so bars don't touch it.
		padX := 3
		padY := 3
		innerX := padX
		innerY := padY
		innerW := w - 2*padX
		innerH := h - 2*padY
		if innerW <= 0 || innerH <= 0 {
			canvas.End()
			renderAndCache(&buf, cacheKey, frame, x0, y0)
			return
		}

		// Evenly divide the inner width into numCores columns with a 1px gap
		// between bars.
		gap := 1
		barW := (innerW - gap*(numCores-1)) / numCores
		if barW < 1 {
			barW = 1
		}

		for i := 0; i < numCores; i++ {
			bx := innerX + i*(barW+gap)
			// Track (full height) behind every bar: black background with a thin
			// grey frame so the empty part of the bar still shows its edge.
			canvas.Roundrect(bx, innerY, barW, innerH, barBarRadius, barBarRadius,
				"fill:"+barBgHex+";stroke:"+barTrackHex+";stroke-width:0.5")
			// Yellow fill rising from the bottom, inset 1px inside the bar's
			// frame so the grey edge stays visible around it (iStat-menu look).
			u := usages[i]
			if u < 0 {
				u = 0
			} else if u > 100 {
				u = 100
			}
			fillX := bx + 1
			fillW := barW - 2
			if fillW < 1 {
				fillX = bx
				fillW = barW
			}
			fillTrackH := innerH - 2
			if fillTrackH < 1 {
				fillTrackH = innerH
			}
			fillH := int(math.Round(float64(fillTrackH) * u / 100.0))
			if fillH > 0 {
				fr := barBarRadius - 1
				if fr < 0 {
					fr = 0
				}
				fy := innerY + 1 + (fillTrackH - fillH)
				canvas.Roundrect(fillX, fy, fillW, fillH, fr, fr,
					"fill:"+barFillHex)
			}
		}
		canvas.End()

		var err error
		img, err = renderSvgBytes(buf.Bytes(), cacheKey)
		if err != nil {
			log.Printf("drawCpuBars: render error: %v", err)
			return
		}
	}
	copyImageToImageAt(frame, img, x0, y0)
}

// drawHBar renders a framed horizontal progress bar at (x0, y0) of size w×h:
// a grey rounded track with a yellow rounded fill whose width is proportional
// to pct (0-100). Corner radius matches the boot progress bar. Cached on the
// quantized percentage.
func drawHBar(frame *image.RGBA, x0, y0, w, h int, pct float64) {
	if w <= 0 || h <= 0 {
		return
	}
	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}

	bucket := int(pct) // 1% granularity is plenty and still caches well
	cacheKey := "gen:hbar:" + strconv.Itoa(w) + "x" + strconv.Itoa(h) + ":" + strconv.Itoa(bucket)

	imageCacheMu.RLock()
	img, cached := imageCache[cacheKey]
	imageCacheMu.RUnlock()
	if !cached {
		var buf bytes.Buffer
		canvas := svg.New(&buf)
		canvas.Start(w, h)

		r := hbarRadius
		if r > h/2 {
			r = h / 2 // keep corners sane for short bars
		}
		// Track spans the full width: black bg with a thin grey frame outline
		// (iStat-menu style) so the bar edge reads against the page. Inset by
		// half a pixel so the 1px stroke isn't clipped by the canvas edge.
		canvas.Roundrect(0, 0, w-1, h-1, r, r,
			"fill:"+barBgHex+";stroke:"+barTrackHex+";stroke-width:1")
		// Fill from the left, inset by 1px so the grey frame stays visible
		// around the yellow (iStat-menu style).
		inset := 1
		innerW := w - 2*inset
		innerH := h - 2*inset
		fillW := int(math.Round(float64(innerW) * pct / 100.0))
		if fillW > 0 && innerH > 0 {
			fr := r - inset
			if fr < 0 {
				fr = 0
			}
			if fr > fillW/2 {
				fr = fillW / 2
			}
			canvas.Roundrect(inset, inset, fillW, innerH, fr, fr, "fill:"+barFillHex)
		}
		canvas.End()

		var err error
		img, err = renderSvgBytes(buf.Bytes(), cacheKey)
		if err != nil {
			log.Printf("drawHBar: render error: %v", err)
			return
		}
	}
	copyImageToImageAt(frame, img, x0, y0)
}

// renderAndCache rasterizes the SVG buffer under cacheKey and blits it. Small
// helper used by the early-return path in drawCpuBars.
func renderAndCache(buf *bytes.Buffer, cacheKey string, frame *image.RGBA, x0, y0 int) {
	img, err := renderSvgBytes(buf.Bytes(), cacheKey)
	if err != nil {
		log.Printf("renderAndCache: render error: %v", err)
		return
	}
	copyImageToImageAt(frame, img, x0, y0)
}

func drawBattery(w, h int, soc float64, isCharging bool, x0, y0 int) *image.RGBA {
	terminalWidth := 3
	face, _, err := getFontFace("clock")
	if err != nil {
		fmt.Println("Error loading font:", err)
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var colorMain, colorShaded color.RGBA
	if soc < 20 {
		colorMain = PCAT_RED
	}else{
		if isCharging {
			colorMain = PCAT_GREEN
		}else{
			colorMain = PCAT_WHITE
		}
	}
	colorShaded = PCAT_GREY
	
	drawRect(img, 0, 0, w-terminalWidth, h, colorMain) //main battery part
	drawRect(img, w-terminalWidth, h/2-3, terminalWidth, 6, colorMain) //terminal part
	
	//soc shade
	startShadeX := int(math.Round((soc / 100.0) * float64(w)))
	if startShadeX < w {
		for x := startShadeX; x < w-3; x++ { 
			for y := 0; y < h; y++ { 
				img.SetRGBA(x, y, colorShaded)
			}
		}
		var terminalX int
		if startShadeX > w-3{
			terminalX = startShadeX
		}else{
			terminalX = w-3
		}
		for x := terminalX; x < w; x++ { 
			for y := h/2-3; y < h/2+3; y++ { 
				img.SetRGBA(x, y, colorShaded)
			}
		}
	}

	//draw corners
	cornerCroods := []struct {X, Y int}{
		{0, 0},
		{w-terminalWidth-1, 0},
		{0, h-1},
		{w-terminalWidth-1, h-1},
		{w-1, h/2-3},
		{w-1, h/2+3-1},
	}
	
	for _, coord := range cornerCroods {
		origColor := img.RGBAAt(coord.X, coord.Y)
		newColor := color.RGBA{uint8(float64(origColor.R) *0.6), uint8(float64(origColor.G) * 0.6), uint8(float64(origColor.B) *0.6), 255}
		img.SetRGBA(coord.X, coord.Y, newColor)
	}
	
	textColor := PCAT_BLACK
	chargingBlotWidth := 10
	//draw text
	if soc < 20 {
		textColor = PCAT_WHITE
	}else{
		textColor = PCAT_BLACK
	}
	batteryText := strconv.Itoa(int(soc))
	drawChargingBlot := true
	if !isCharging || soc == 100 {
		chargingBlotWidth = 0
		drawChargingBlot = false
	}
	//drawText(img, batteryText, (w-terminalWidth)/2, -3, face, textColor, true)
	x, _ := drawText(img, batteryText, (w-terminalWidth-chargingBlotWidth)/2+1, -4, face, textColor, true)
	if drawChargingBlot {
		var chargingBolt *image.RGBA
		var err error
		if soc < 20 {
			chargingBolt, _, _, err = loadImage(assetsPrefix+"/assets/svg/blotWhite.svg")
		}else{
			chargingBolt, _, _, err = loadImage(assetsPrefix+"/assets/svg/blotBlack.svg")
		}
		if err != nil {
			fmt.Println("Error loading charging bolt:", err)
			return nil
		}
		copyImageToImageAt(img, chargingBolt, x, 1)
	}
	return img
}


func drawTopBar(display gc9307.Device, frame *image.RGBA) {
	var timeStr string
	var networkStr string
	currDateTime := time.Now()

	if currDateTime.Year() < 2025 {
		timeStr = "--:--"
	} else {
		timeStr = fmt.Sprintf("%02d:%02d", currDateTime.Hour(), currDateTime.Minute())
	}

	gatewayDevice, _ := globalData.Load("GatewayDevice")
	carrier, _ := globalData.Load("Carrier")
	activeEgress, _ := globalData.Load("ActiveEgress")

	// Prefer the precise active egress (which can also be "wifi" in Smart WAN
	// mode) over the coarse wired/mobile gateway hint.
	// networkStr: "5"/"4"/"3" cellular, "w" ethernet, "i" WiFi (Smart WAN),
	// "" (empty) when no WAN is secured yet.
	if activeEgress == "wifi" {
		networkStr = "i"
	} else if activeEgress == "wan" || activeEgress == "lan" {
		networkStr = "w"
	} else if activeEgress == "mobile" || gatewayDevice == "mobile" {
		if carrier == "5G"{
			networkStr = "5"
		}else if carrier == "4G"{
			networkStr = "4"
		}
	}else if gatewayDevice == "wired"{
		networkStr = "w"
	}else{
		// No WAN secured yet (e.g. at startup before pcat-manager-web reports
		// a real egress, or when it reports none). Show no network icon rather
		// than falsely claiming ethernet.
		networkStr = ""
	}
	signalStrength := 0.43
	// Resolve the cellular signal level for the cache key so the bar refreshes
	// when it changes. (WiFi-as-WAN draws a static icon, so it needs no level.)
	if networkStr == "4" || networkStr == "5" || networkStr == "3" {
		if v, ok := globalData.Load("ModemSignalStrength"); ok {
			if vi, ok := v.(int); ok {
				signalStrength = float64(vi) / 100.0
			}
		}
	}
	magicStr := timeStr + " " + strconv.Itoa(int(signalStrength*100)) + " " + networkStr + " " + strconv.Itoa(int(battSOC)) + " " + strconv.FormatBool(battChargingStatus)

	if cacheTopBarStr == magicStr {
		return //no need to refresh
	}

	topBarFrameWidth := PCAT2_LCD_WIDTH
	topBarFrameHeight := PCAT2_TOP_BAR_HEIGHT

	clearFrame(frame, topBarFrameWidth, topBarFrameHeight)
	
	faceClock, _, err := getFontFace("clock")
	faceTiny, _, err := getFontFace("tiny")
	if err != nil {
		fmt.Println("Error loading font:", err)
		return
	}
	fiveGonTop :=true


	x0 := PCAT2_L_MARGIN
	y0 := PCAT2_T_MARGIN

	//draw time
	drawText(frame, timeStr, x0+2, y0-3, faceClock, PCAT_WHITE, false)	

	if networkStr == "w"{
		//draw wired
		eth, _, _, err := loadImage(assetsPrefix+"/assets/svg/eth.svg")
		if err != nil {
			fmt.Println("Error loading eth:", err)
			return
		}
		copyImageToImageAt(frame, eth, x0+80, y0+2)

	}else if networkStr == "i"{
		// WiFi as WAN (Smart WAN): draw the WiFi icon, same as wired draws eth.svg.
		wifi, _, _, err := loadImage(assetsPrefix+"/assets/svg/wifi.svg")
		if err != nil {
			fmt.Println("Error loading wifi:", err)
			return
		}
		copyImageToImageAt(frame, wifi, x0+80, y0+2)
	}else if networkStr == "4" || networkStr == "5" || networkStr == "3" {
		// signalStrength was already resolved above from ModemSignalStrength.
		//draw signal strength
		if fiveGonTop {
			drawSignalStrength(frame, x0+80, y0, signalStrength)
			drawText(frame, networkStr, x0+78, y0-6, faceTiny, PCAT_WHITE, false)
		}else{
			drawSignalStrength(frame, x0+70, y0, signalStrength)
			drawText(frame, networkStr, x0+94, y0-3, faceTiny, PCAT_WHITE, false)
		}
	}else if networkStr == "u"{
		nolink, _, _, err := loadImage(assetsPrefix+"/assets/svg/nolink.svg")
		if err != nil {
			fmt.Println("Error loading nolink:", err)
			return
		}
		copyImageToImageAt(frame, nolink, x0+80, y0+2)
	}

	//draw Battery
	socValue, _ := globalData.Load("BatterySoc") //it is int
	socInt, _ := socValue.(int)
	socFloat := float64(socInt) // now convert int to float64
	charging, _ := globalData.Load("BatteryCharging")
	chargingBool, ok := charging.(bool) // Type assertion to bool
	if !ok {
		chargingBool = false // Default value if assertion fails
	}
	if fiveGonTop {
		img := drawBattery(50, 19, socFloat, chargingBool, x0, y0)
		copyImageToImageAt(frame, img, x0+108, y0)
	}else{
		img := drawBattery(45, 18, socFloat, chargingBool, x0, y0)
		copyImageToImageAt(frame, img, x0+113, y0)
	}
	cacheTopBar = frame
	cacheTopBarStr = magicStr
	sendTopBar(display, frame)
}

func saveFrameToPng(frame *image.RGBA, filename string) {
	outFile, err := os.Create(filename)
	if err != nil {
		panic(err)
	}
	defer outFile.Close()
	png.Encode(outFile, frame)
	fmt.Println("Frame saved to", filename)
}

func renderMiddle(frame *image.RGBA, cfg *Config, isSMS bool, pageIdx int) {
	// Safety check for frame validity
	if frame == nil || frame.Bounds().Empty() {
		log.Printf("renderMiddle: invalid frame bounds %+v", frame)
		return
	}

	if isSMS {
		// Bounds check for SMS pages
		if pageIdx >= 0 && pageIdx < len(smsPagesImages) && smsPagesImages[pageIdx] != nil {
			copyImageToImageAt(frame, smsPagesImages[pageIdx], 0, 0)
		} else {
			log.Printf("renderMiddle: invalid SMS page index %d", pageIdx)
		}
		return
	}

	page := cfg.DisplayTemplate.Elements["page"+strconv.Itoa(pageIdx)]

	// Process each element.
	for _, element := range page {
		// Check if the element is enabled.
		if element.Enable == 0 {
			continue
		}

		switch element.Type {
		case "text":
			// Determine the text to display first (moved up to use for font selection)
			textValue, exists := globalData.Load(element.DataKey)
			var textToDisplay string
			var isPingTimeout bool = false
			
			if exists {
				if textValue == nil {
					textToDisplay = "-"
				} else {
					// Special handling for ping timeout (-2) and errors
					if (element.DataKey == "Ping0" || element.DataKey == "Ping1") {
						// Handle both int64 and int types for ping values
						var pingVal int64
						var validPingValue bool
						
						if val, ok := textValue.(int64); ok {
							pingVal = val
							validPingValue = true
						} else if val, ok := textValue.(int); ok {
							pingVal = int64(val)
							validPingValue = true
						}
						
						if validPingValue {
							if pingVal == -2 || pingVal == -1 {
								// Both timeout (-2) and other failures (-1) show red X
								textToDisplay = "X"
								isPingTimeout = true
							} else if pingVal >= 0 {
								textToDisplay = fmt.Sprintf("%d", pingVal)
							} else {
								// Any other negative value should not happen, but show as red X
								textToDisplay = "X"
								isPingTimeout = true
							}
						} else {
							// If not a numeric type, show as error
							textToDisplay = "-"
						}
					} else {
						textToDisplay = fmt.Sprintf("%v", textValue)
						// Failed custom-metric fetches store sentinels like
						// "ERROR"/"TIMEOUT"; show the regular "-" placeholder
						// instead of the raw sentinel (plus units).
						if isErrorSentinel(textToDisplay) {
							textToDisplay = "-"
						}
					}
				}
			} else {
				textToDisplay = "-" // or any default value you prefer
			}
			
			// Get the font face for the main text (choose Chinese font if needed)
			face, _, err := getFontFaceForText(element.Font, textToDisplay)
			if err != nil {
				log.Printf("Error getting font face for %s: %v", element.Font, err)
				continue
			}
			// Get the font face for the units.
			unitFace, _, err := getFontFace(element.UnitsFont)
			if err != nil {
				log.Printf("Error getting font face for %s: %v", element.UnitsFont, err)
				continue
			}

			// Convert the color array (assumed to be [R,G,B]) to a color.RGBA.
			var clr color.RGBA
			if len(element.Color) >= 3 {
				clr = color.RGBA{
					R: uint8(element.Color[0]),
					G: uint8(element.Color[1]),
					B: uint8(element.Color[2]),
					A: 255,
				}
			} else {
				// Default color: white.
				clr = color.RGBA{255, 255, 255, 255}
			}

			// Use red color for ping timeouts
			if isPingTimeout {
				clr = PCAT_RED
			}

			// Draw the main text.
			// The drawText function uses the provided y plus the font ascent as the baseline.
			mainAscent := face.Metrics().Ascent.Round()
			// element.Position.Y acts as the top of the text area.
			mainBaseline := element.Position.Y + mainAscent

			// Available horizontal space for this value: from its start x to the
			// right screen edge (minus the standard right margin), unless the
			// element pins an explicit width via "size".width. Values wider than
			// this (long SSIDs, IPv6 addresses, ISP names, …) scroll as a ticker
			// instead of being clipped; values that fit are drawn in place.
			availWidth := PCAT2_LCD_WIDTH - element.Position.X - PCAT2_R_MARGIN
			if element.Size != nil && element.Size.Width > 0 {
				availWidth = element.Size.Width
			} else if element.Size2 != nil && element.Size2.Width > 0 {
				availWidth = element.Size2.Width
			}

			mainW := font.MeasureString(face, textToDisplay).Round()
			// A ping timeout is a single "X"; never scroll it. Only overflowing
			// values scroll, and a scrolling value carries no trailing units.
			if !isPingTimeout && mainW > availWidth {
				drawScrollingText(frame, textToDisplay, element.Position.X, element.Position.Y, availWidth, face, clr)
				break
			}

			xMain, _ := drawText(frame, textToDisplay, element.Position.X, element.Position.Y, face, clr, false)

			// Calculate the y position for the units text so that its baseline aligns with the main text.
			unitAscent := unitFace.Metrics().Ascent.Round()
			unitY := mainBaseline - unitAscent

			// Draw the units text slightly to the right of the main text (skip units for timeout)
			if !isPingTimeout {
				unitText := element.Units
				//check if there is a override unit
				theKey := element.DataKey + "_Unit"
				if _, ok := globalData.Load(theKey); ok {
					unitTextVal, _ := globalData.Load(theKey)
					unitText = unitTextVal.(string)
				}
				drawText(frame, unitText, xMain+1, unitY, unitFace, clr, false)
			}
		
		case "icon":
			var iconImg *image.RGBA
			var err error
			iconImg, _, _, err = loadImage(assetsPrefix + "/" + element.IconPath)
			if err != nil {
				log.Printf("Error loading icon from %s: %v", element.IconPath, err)
				continue
			}

			// Determine the size for the icon.
			var sz Size
			if element.Size != nil {
				sz = *element.Size
			} else if element.Size2 != nil {
				sz = *element.Size2
			} else {
				sz = Size{Width: iconImg.Bounds().Dx(), Height: iconImg.Bounds().Dy()}
			}

			// Define the destination rectangle for the icon.
			iconX := element.Position.X
			// When anchored to a text data_key, stick the icon to the right of
			// that value's rendered width so it tracks variable-width text
			// (e.g. "0:10" vs "12:30") instead of sitting at a fixed x.
			if element.AnchorAfterDataKey != "" {
				anchorText := ""
				if v, ok := globalData.Load(element.AnchorAfterDataKey); ok && v != nil {
					anchorText = fmt.Sprintf("%v", v)
				}
				// The icon labels the anchored value; when there is no value
				// yet (startup) or nothing to show ("" / "-"), draw neither.
				if anchorText == "" || anchorText == "-" {
					continue
				}
				anchorFontName := element.AnchorFont
				if anchorFontName == "" {
					anchorFontName = "reg"
				}
				if anchorFace, _, ferr := getFontFaceForText(anchorFontName, anchorText); ferr == nil {
					textW := font.MeasureString(anchorFace, anchorText).Round()
					iconX = element.Position.X + textW + element.AnchorGap
				}
			}
			pt := image.Pt(iconX, element.Position.Y)
			rect := image.Rect(pt.X, pt.Y, pt.X+sz.Width, pt.Y+sz.Height)
			draw.Draw(frame, rect, iconImg, image.Point{}, draw.Over)
		case "fixed_text":
			face, _, err := getFontFace(element.Font)
			if err != nil {
				log.Printf("Error getting font face for %s: %v", element.Font, err)
				continue
			}
			// pick a default white if no color
			var clr color.RGBA
			if len(element.Color) >= 3 {
				clr = color.RGBA{uint8(element.Color[0]), uint8(element.Color[1]), uint8(element.Color[2]), 255}
			} else {
				clr = color.RGBA{255, 255, 255, 255}
			}
		
			// replace [key] → cfg.Field
			label := placeholderRe.ReplaceAllStringFunc(element.Label, func(tok string) string {
				key := tok[1 : len(tok)-1] // strip brackets
				switch key {
				case "ping_site0":
					return cfg.PingSite0
				case "ping_site1":
					return cfg.PingSite1
				/*case "screen_dimmer_time_on_battery_seconds":
					return strconv.Itoa(cfg.ScreenDimmerTimeOnBatterySeconds)
				case "screen_dimmer_time_on_dc_seconds":
					return strconv.Itoa(cfg.ScreenDimmerTimeOnDCSeconds)*/
				// add more fields here if you ever parameterize them in fixed_text
				default:
					return tok
				}
			})
		

			drawText(frame, label, element.Position.X, element.Position.Y, face, clr, false)

		case "vtext":
			// Vertical (stacked-letter) fixed label, e.g. "CPU"/"MEM" beside a
			// bar meter. Font, color and position come from the element; the
			// per-letter line height defaults to the font height but can be
			// tuned via size.height, and the column width via size.width.
			face, fh, err := getFontFace(element.Font)
			if err != nil {
				log.Printf("Error getting font face for %s: %v", element.Font, err)
				continue
			}
			var clr color.RGBA
			if len(element.Color) >= 3 {
				clr = color.RGBA{uint8(element.Color[0]), uint8(element.Color[1]), uint8(element.Color[2]), 255}
			} else {
				clr = color.RGBA{255, 255, 255, 255}
			}
			charW := 10
			lineH := fh
			if element.Size != nil {
				if element.Size.Width > 0 {
					charW = element.Size.Width
				}
				if element.Size.Height > 0 {
					lineH = element.Size.Height
				}
			}
			drawVerticalText(frame, element.Label, element.Position.X, element.Position.Y, charW, lineH, face, clr)

		case "graph":
			// Handle graph elements
			if element.GraphConfig == nil {
				log.Printf("Graph element missing graph_config")
				continue
			}
			
			// Determine the size for the graph
			var sz Size
			if element.Size != nil {
				sz = *element.Size
			} else if element.Size2 != nil {
				sz = *element.Size2
			} else {
				// Default graph size
				sz = Size{Width: 60, Height: 25}
			}
			
			// Set time frame if specified
			if element.GraphConfig.TimeFrameMins > 0 {
				setPowerGraphTimeFrame(element.GraphConfig.TimeFrameMins)
			}
			
			// Draw the graph based on type
			switch element.GraphConfig.GraphType {
			case "power":
				drawPowerGraph(frame, element.Position.X, element.Position.Y, sz.Width, sz.Height)
			default:
				log.Printf("Unknown graph type: %s", element.GraphConfig.GraphType)
			}

		case "cpu_bars":
			// Framed 8-bar (per-core) CPU meter. Size comes from the element;
			// the per-core usages come from the CpuUsages data key.
			sz := Size{Width: 80, Height: 40}
			if element.Size != nil {
				sz = *element.Size
			} else if element.Size2 != nil {
				sz = *element.Size2
			}
			var usages []float64
			if v, ok := globalData.Load(element.DataKey); ok && v != nil {
				if u, ok := v.([]float64); ok {
					usages = u
				}
			}
			// Nothing sampled yet (startup): draw an empty framed box so the
			// slot isn't blank, using a default 8 cores at 0%.
			if len(usages) == 0 {
				usages = make([]float64, 8)
			}
			drawCpuBars(frame, element.Position.X, element.Position.Y, sz.Width, sz.Height, usages)

		case "hbar":
			// Framed horizontal progress bar driven by a 0-100 data key
			// (e.g. MemUsagePercent).
			sz := Size{Width: 100, Height: 12}
			if element.Size != nil {
				sz = *element.Size
			} else if element.Size2 != nil {
				sz = *element.Size2
			}
			pct := 0.0
			if v, ok := globalData.Load(element.DataKey); ok && v != nil {
				switch n := v.(type) {
				case float64:
					pct = n
				case int:
					pct = float64(n)
				}
			}
			drawHBar(frame, element.Position.X, element.Position.Y, sz.Width, sz.Height, pct)

		default:
			log.Printf("Unknown element type: %s", element.Type)
		}
	}
}

func drawFooter(display gc9307.Device, frame *image.RGBA, currPage int, numOfPages int, isSMS bool) {
	magicStr:= strconv.Itoa(currPage) + " " + strconv.Itoa(numOfPages) + " " + strconv.FormatBool(isSMS)
	if cacheFooterStr == magicStr {
		return //no need to refresh
	}
	faceMicro, _, err := getFontFace("micro")
	if err != nil {
		log.Printf("Error getting font face for %s: %v", "tiny", err)
		return
	}

	footerFrameWidth := PCAT2_LCD_WIDTH
	footerFrameHeight := PCAT2_FOOTER_HEIGHT
	clearFrame(frame, footerFrameWidth, footerFrameHeight)

	if isSMS {
		footerText := "SMS: " + strconv.Itoa(currPage+1) + "/" + strconv.Itoa(numOfPages)
		drawText(frame, footerText, 172/2, 2, faceMicro, PCAT_WHITE, true)

	}else{
		cir, _, _, err := loadImage(assetsPrefix+"/assets/svg/dotCircle.svg")
		if err != nil {
			log.Printf("Error loading circle_dot from %s: %v", "assets/svg/dotCircle.svg", err)
			return
		}
		dot, _, _, err := loadImage(assetsPrefix+"/assets/svg/dotSolid.svg")
		if err != nil {
			log.Printf("Error loading dot from %s: %v", "assets/svg/dotSolid.svg", err)
			return
		}

		whiteDotRadius := 8
		greyDotRadius := 4
		xPart := 10 + whiteDotRadius * 2
		yOffset := 2
		x0 := (PCAT2_LCD_WIDTH - (numOfPages-1)*xPart) / 2  - whiteDotRadius

		for i := 0; i < numOfPages; i++ {
			if i == currPage {
				copyImageToImageAt(frame, cir, x0+i*xPart, yOffset)
			}else{
				copyImageToImageAt(frame, dot, x0+i*xPart + greyDotRadius, yOffset + greyDotRadius)
			}
		}
	}
	//make a frame cache
	cacheFooter = frame
	cacheFooterStr = magicStr
	sendFooter(display, frame)
}

// showWelcome plays the "cat waking up" boot animation (welcomeAnim.go) for
// the given duration. Rendering is pipelined: a goroutine rasterizes the next
// frame into one of two buffers while the SPI push of the previous frame is
// still in flight, so the effective rate is max(render, push) instead of
// their sum. Everything is derived from wall-clock time, so a slow frame
// never stretches the animation past its duration.
func showWelcome(display gc9307.Device, width, height int, duration time.Duration) {
	dur := duration.Seconds()
	if dur <= 0 {
		dur = welcomeAnimDur
	}
	timeScale := welcomeAnimDur / dur

	// Prove the animation pipeline works before committing the boot screen
	// to it; fall back to the static welcome otherwise.
	first, err := welcomeAnimFrameInto(nil, 0, width, height)
	if err != nil {
		log.Printf("Welcome animation unavailable (%v); showing static welcome", err)
		showWelcomeStatic(display, width, height)
		return
	}
	sendFull(display, first)

	const frameInterval = time.Second / 60 // pace renders; LCD tops out around 60 Hz anyway

	frames := make(chan *image.RGBA) // unbuffered: hand-off keeps the two buffers race-free
	go func() {
		defer close(frames)
		var bufs [2]*image.RGBA
		start := time.Now()
		for i := 0; ; i++ {
			frameStart := time.Now()
			t := frameStart.Sub(start).Seconds() * timeScale
			if t >= welcomeAnimDur {
				return
			}
			f, err := welcomeAnimFrameInto(bufs[i%2], t, width, height)
			if err != nil {
				log.Printf("Welcome animation frame failed: %v", err)
				return
			}
			bufs[i%2] = f
			frames <- f
			if d := frameInterval - time.Since(frameStart); d > 0 {
				time.Sleep(d)
			}
		}
	}()

	sent := 0
	animStart := time.Now()
	for f := range frames {
		sendFull(display, f)
		sent++
	}
	if el := time.Since(animStart); sent > 0 && el > 0 {
		log.Printf("Welcome animation: %d frames in %.2fs (%.1f fps)", sent, el.Seconds(), float64(sent)/el.Seconds())
	}

	// Land exactly on the resting pose: the static logo plus the full bar.
	if f, err := welcomeAnimFrameInto(nil, welcomeAnimDur, width, height); err == nil {
		sendFull(display, f)
	}
}

// showWelcomeStatic is the pre-animation welcome screen: static logo plus a
// progress bar swept as fast as the SPI link allows. Kept as the fallback
// path should the animation ever fail to render.
func showWelcomeStatic(display gc9307.Device, width, height int) {
	radiusBarCorner := 5
	spaceBetweenLogoAndBar := 28
	barWidth := 82
    barX := width/2 - barWidth/2
	barHeight := 8

	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	clearFrame(frame, width, height)
	welcomeLogo, w, h, err := loadImage(assetsPrefix+"/assets/svg/welcome.svg")
	if err != nil {
		log.Printf("Error loading welcome logo from %s: %v", "assets/svg/welcome.svg", err)
		return
	}
	logoY := height/2 - (h+spaceBetweenLogoAndBar+barHeight)/2
	x0 := width/2 - w/2
	y0 := logoY
	log.Printf("Welcome logo at: x0: %d, y0: %d, w: %d, h: %d", x0, y0, w, h)
	copyImageToImageAt(frame, welcomeLogo, x0, y0 )

	var bufBack bytes.Buffer
	canvas := svg.New(&bufBack)
	canvas.Start(barWidth, barHeight)
	canvas.Roundrect(0, 0, barWidth, barHeight, radiusBarCorner, radiusBarCorner, "fill:#627482")
	canvas.End()
	barBackground, err := renderSvgBytes(bufBack.Bytes(), "")
	if err != nil {
		log.Printf("Error rendering bar background: %v", err)
		return
	}
	barY := logoY + spaceBetweenLogoAndBar + h
	copyImageToImageAt(frame, barBackground, barX, barY)
	sendFull(display, frame)

	var bufProgress bytes.Buffer

    for i := 1; i <= barWidth; i++ {
		bufProgress.Reset()
		canvasProgress := svg.New(&bufProgress)
		canvasProgress.Start(barWidth, barHeight)
		canvasProgress.Roundrect(0, 0, i, barHeight, radiusBarCorner, radiusBarCorner, "fill:#FDE021")
		canvasProgress.End()
		progressBar, err := renderSvgBytes(bufProgress.Bytes(), "")
		if err != nil {
			log.Printf("Error rendering progress bar: %v", err)
			return
		}
		copyImageToImageAt(frame, progressBar, barX, barY)
		sendFull(display, frame)

		//time.Sleep(sleepPerPixel)
    }
}

func showCiao(display gc9307.Device, width, height int, duration time.Duration) {
	spaceBetweenLogoAndText := 28
	textHeight := 12
	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	clearFrame(frame, width, height)
	//clear display
	sendFull(display, frame)
	ciaoLogo, w, h, err := loadImage(assetsPrefix+"/assets/svg/ciao.svg")
	if err != nil {
		log.Printf("Error loading ciao logo from %s: %v", "assets/svg/ciao.svg", err)
		return
	}
	logoY := height/2 - (h+spaceBetweenLogoAndText+textHeight)/2
	copyImageToImageAt(frame, ciaoLogo, width/2 - w/2, logoY ) 
	text := "Powering Off..."
	faceUnit, _, err := getFontFace("unit")
	if err != nil {
		log.Printf("Error getting font face for %s: %v", "unit", err)
		return
	}
	drawText(frame, text, width/2, logoY + h + spaceBetweenLogoAndText, faceUnit, PCAT_WHITE, true)
	sendFull(display, frame)

}

func showCiaoInstant(display gc9307.Device, width, height int) {
	spaceBetweenLogoAndText := 28
	textHeight := 12
	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	clearFrame(frame, width, height)
	
	// Load and display shutdown screen
	ciaoLogo, w, h, err := loadImage(assetsPrefix+"/assets/svg/ciao.svg")
	if err != nil {
		log.Printf("Error loading ciao logo from %s: %v", "assets/svg/ciao.svg", err)
		return
	}
	logoY := height/2 - (h+spaceBetweenLogoAndText+textHeight)/2
	copyImageToImageAt(frame, ciaoLogo, width/2 - w/2, logoY)
	text := "Powering Off..."
	faceUnit, _, err := getFontFace("unit")
	if err != nil {
		log.Printf("Error getting font face for %s: %v", "unit", err)
		return
	}
	drawText(frame, text, width/2, logoY + h + spaceBetweenLogoAndText, faceUnit, PCAT_WHITE, true)
	sendFull(display, frame)
	
	// Instantly dim to minimum brightness
	setBacklight(0)
}
