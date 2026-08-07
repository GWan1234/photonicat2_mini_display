package main

// drawCov_test.go — coverage for draw.go's pure-rendering helpers: scrolling
// tickers, remote/local image loading, SVG rasterizing and tinting, and the
// low-level compositing primitives. Everything renders into in-memory images;
// the display "device" is a zero gc9307.Device whose driver refuses the
// transfer before touching any hardware pin (its Size() is 0x0, so every
// FillRectangleWithImage call bails out with a bounds error).
//
// All new top-level identifiers are prefixed drawCov/DrawCov so this file can
// be merged alongside other agents' test files without collisions.

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/font/basicfont"

	"github.com/llgcode/draw2d/draw2dimg"
	"github.com/srwiley/oksvg"
)

// drawCovParseIcon parses in-memory SVG data into an oksvg icon.
func drawCovParseIcon(svgData string) (*oksvg.SvgIcon, error) {
	return oksvg.ReadIconStream(strings.NewReader(svgData))
}

// drawCovSVG is a minimal valid SVG with a 10x10 viewBox and a filled square.
const drawCovSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect x="0" y="0" width="10" height="10" fill="#FDE021"/></svg>`

// drawCovBadViewBoxSVG parses but has a zero-size viewBox.
const drawCovBadViewBoxSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 0 0"></svg>`

// drawCovSetup performs the startup pieces draw tests rely on: an allocated
// imageCache, the real font table against the repo assets, and assetsPrefix
// pointing at the repo root. assetsPrefix is restored on cleanup.
func drawCovSetup(t *testing.T) {
	t.Helper()
	ensureImageCache()
	gpsPreviewFonts()
	oldPrefix := assetsPrefix
	assetsPrefix = "."
	t.Cleanup(func() { assetsPrefix = oldPrefix })
}

// drawCovStashGlobal stores val under key in globalData for the duration of a
// test, restoring (or deleting) the previous value on cleanup.
func drawCovStashGlobal(t *testing.T, key string, val interface{}) {
	t.Helper()
	old, had := globalData.Load(key)
	t.Cleanup(func() {
		if had {
			globalData.Store(key, old)
		} else {
			globalData.Delete(key)
		}
	})
	globalData.Store(key, val)
}

// drawCovHasInk reports whether any pixel in img has non-zero alpha.
func drawCovHasInk(img *image.RGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 {
			return true
		}
	}
	return false
}

// drawCovSaveScrollState snapshots and restores the ticker globals.
func drawCovSaveScrollState(t *testing.T) {
	t.Helper()
	oldEpoch := scrollEpoch
	oldAny := anyTextScrolling
	oldScratch := scrollScratch
	t.Cleanup(func() {
		scrollEpoch = oldEpoch
		anyTextScrolling = oldAny
		scrollScratch = oldScratch
	})
}

func TestDrawCovDrawVerticalText(t *testing.T) {
	face := basicfont.Face7x13

	// Degenerate inputs are no-ops.
	drawVerticalText(nil, "CPU", 0, 0, 10, 13, face, color.White)
	frame := image.NewRGBA(image.Rect(0, 0, 40, 60))
	drawVerticalText(frame, "", 0, 0, 10, 13, face, color.White)
	if drawCovHasInk(frame) {
		t.Fatal("empty text painted pixels")
	}

	drawVerticalText(frame, "CPU", 2, 2, 12, 15, face, color.RGBA{255, 255, 255, 255})
	if !drawCovHasInk(frame) {
		t.Fatal("vertical text drew nothing")
	}
	// Letters are stacked: some ink must exist below the first line height.
	lower := frame.SubImage(image.Rect(0, 17, 40, 60)).(*image.RGBA)
	found := false
	b := lower.Bounds()
	for y := b.Min.Y; y < b.Max.Y && !found; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if lower.RGBAAt(x, y).A != 0 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("second stacked letter left no ink below the first line")
	}
}

func TestDrawCovDrawScrollingText(t *testing.T) {
	drawCovSaveScrollState(t)
	face := basicfont.Face7x13
	white := color.RGBA{255, 255, 255, 255}

	t.Run("nil_frame", func(t *testing.T) {
		if drawScrollingText(nil, "abc", 0, 0, 50, face, white) {
			t.Error("nil frame should not scroll")
		}
	})

	t.Run("zero_avail_width", func(t *testing.T) {
		frame := image.NewRGBA(image.Rect(0, 0, 100, 30))
		if drawScrollingText(frame, "abc", 0, 0, 0, face, white) {
			t.Error("availWidth<=0 should draw in place, not scroll")
		}
	})

	t.Run("fits_in_place", func(t *testing.T) {
		frame := image.NewRGBA(image.Rect(0, 0, 100, 30))
		if drawScrollingText(frame, "ok", 0, 0, 90, face, white) {
			t.Error("fitting text must not scroll")
		}
		if !drawCovHasInk(frame) {
			t.Error("fitting text drew nothing")
		}
	})

	t.Run("overflow_scrolls", func(t *testing.T) {
		scrollEpoch = time.Time{} // force the set-epoch branch
		anyTextScrolling = false
		scrollScratch = nil
		frame := image.NewRGBA(image.Rect(0, 0, 100, 30))
		long := strings.Repeat("WWWW", 20)
		if !drawScrollingText(frame, long, 2, 2, 60, face, white) {
			t.Fatal("overflowing text must report scrolling")
		}
		if scrollEpoch.IsZero() {
			t.Error("scrollEpoch was not initialised")
		}
		if !anyTextScrolling {
			t.Error("anyTextScrolling was not set")
		}
		if !drawCovHasInk(frame) {
			t.Error("ticker drew nothing")
		}
	})

	t.Run("mid_scroll_reuses_scratch", func(t *testing.T) {
		// Epoch far enough back that the start pause has elapsed → offset>0.
		scrollEpoch = time.Now().Add(-3 * time.Second)
		frame := image.NewRGBA(image.Rect(0, 0, 100, 30))
		long := strings.Repeat("WWWW", 20)
		if !drawScrollingText(frame, long, 2, 2, 60, face, white) {
			t.Fatal("overflowing text must report scrolling")
		}
		scratch := scrollScratch
		// Second pass with the same geometry must reuse (and clear) the buffer.
		if !drawScrollingText(frame, long, 2, 2, 60, face, white) {
			t.Fatal("second pass must still scroll")
		}
		if scrollScratch != scratch {
			t.Error("scratch buffer was reallocated for identical geometry")
		}
		// A wider region forces a reallocation.
		if !drawScrollingText(frame, long, 0, 2, 90, face, white) {
			t.Fatal("wider pass must still scroll")
		}
		if scrollScratch == scratch {
			t.Error("scratch buffer was not grown for a wider region")
		}
	})
}

// drawCovRemoteDir points remoteImageCacheDir at a temp dir for one test.
func drawCovRemoteDir(t *testing.T) string {
	t.Helper()
	old := remoteImageCacheDir
	dir := t.TempDir()
	remoteImageCacheDir = dir
	t.Cleanup(func() { remoteImageCacheDir = old })
	return dir
}

func TestDrawCovFetchRemoteImage(t *testing.T) {
	drawCovRemoteDir(t)

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/missing.png"):
			w.WriteHeader(http.StatusNotFound)
		default:
			hits++
			w.Write([]byte(drawCovSVG))
		}
	}))
	defer srv.Close()

	t.Run("downloads_and_caches", func(t *testing.T) {
		url := srv.URL + "/icon.svg"
		p1, err := fetchRemoteImage(url)
		if err != nil {
			t.Fatalf("fetchRemoteImage: %v", err)
		}
		if _, err := os.Stat(p1); err != nil {
			t.Fatalf("downloaded file missing: %v", err)
		}
		before := hits
		p2, err := fetchRemoteImage(url)
		if err != nil {
			t.Fatalf("cached fetch: %v", err)
		}
		if p2 != p1 {
			t.Errorf("cache returned a different path: %q vs %q", p2, p1)
		}
		if hits != before {
			t.Error("second fetch re-downloaded a cached file")
		}
	})

	t.Run("extensionless_url_assumes_svg", func(t *testing.T) {
		p, err := fetchRemoteImage(srv.URL + "/noext")
		if err != nil {
			t.Fatalf("fetchRemoteImage: %v", err)
		}
		if filepath.Ext(p) != ".svg" {
			t.Errorf("extensionless URL cached as %q, want .svg", filepath.Ext(p))
		}
	})

	t.Run("http_error_status", func(t *testing.T) {
		if _, err := fetchRemoteImage(srv.URL + "/missing.png"); err == nil {
			t.Error("HTTP 404 should be an error")
		}
	})

	t.Run("bad_url", func(t *testing.T) {
		if _, err := fetchRemoteImage("http://bad url with spaces/x.png"); err == nil {
			t.Error("malformed URL should be an error")
		}
	})

	t.Run("connection_refused", func(t *testing.T) {
		dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		deadURL := dead.URL
		dead.Close()
		if _, err := fetchRemoteImage(deadURL + "/x.png"); err == nil {
			t.Error("connection to a closed server should be an error")
		}
	})

	t.Run("mkdir_failure", func(t *testing.T) {
		old := remoteImageCacheDir
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		remoteImageCacheDir = filepath.Join(blocker, "sub") // parent is a file
		t.Cleanup(func() { remoteImageCacheDir = old })
		if _, err := fetchRemoteImage(srv.URL + "/other.svg"); err == nil {
			t.Error("MkdirAll under a file should be an error")
		}
	})
}

// drawCovWriteImageFile encodes a tiny 3x2 image in the given format under dir.
func drawCovWriteImageFile(t *testing.T, dir, name string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	var buf bytes.Buffer
	var err error
	switch filepath.Ext(name) {
	case ".png":
		err = png.Encode(&buf, img)
	case ".jpg", ".jpeg":
		err = jpeg.Encode(&buf, img, nil)
	case ".gif":
		err = gif.Encode(&buf, img, nil)
	default:
		t.Fatalf("unsupported test format %s", name)
	}
	if err != nil {
		t.Fatalf("encode %s: %v", name, err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDrawCovLoadImage(t *testing.T) {
	ensureImageCache()
	dir := t.TempDir()

	t.Run("raster_formats", func(t *testing.T) {
		for _, name := range []string{"a.png", "b.jpg", "c.jpeg", "d.gif"} {
			p := drawCovWriteImageFile(t, dir, name)
			img, w, h, err := loadImage(p)
			if err != nil {
				t.Fatalf("loadImage(%s): %v", name, err)
			}
			if img == nil || w != 3 || h != 2 {
				t.Errorf("loadImage(%s) = %dx%d, want 3x2", name, w, h)
			}
			// Second call must come from the cache (same pointer).
			img2, _, _, err := loadImage(p)
			if err != nil {
				t.Fatalf("cached loadImage(%s): %v", name, err)
			}
			if img2 != img {
				t.Errorf("loadImage(%s) second call did not hit the cache", name)
			}
		}
	})

	t.Run("corrupt_files_error", func(t *testing.T) {
		for _, name := range []string{"x.png", "x.jpg", "x.gif", "x.svg"} {
			p := filepath.Join(dir, name)
			if err := os.WriteFile(p, []byte("not an image"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := loadImage(p); err == nil {
				t.Errorf("corrupt %s should be an error", name)
			}
		}
	})

	t.Run("svg_render_and_cache", func(t *testing.T) {
		p := filepath.Join(dir, "ok.svg")
		if err := os.WriteFile(p, []byte(drawCovSVG), 0o644); err != nil {
			t.Fatal(err)
		}
		img, w, h, err := loadImage(p)
		if err != nil {
			t.Fatalf("loadImage svg: %v", err)
		}
		if w != 10 || h != 10 {
			t.Errorf("svg size = %dx%d, want 10x10", w, h)
		}
		if !drawCovHasInk(img) {
			t.Error("rendered svg has no ink")
		}
		// Drop only the path key so the second call reaches the svg-specific
		// rendered-AA cache branch.
		imageCacheMu.Lock()
		delete(imageCache, p)
		imageCacheMu.Unlock()
		img2, _, _, err := loadImage(p)
		if err != nil {
			t.Fatalf("svg cache branch: %v", err)
		}
		if img2 != img {
			t.Error("svg rendered-AA cache was not used")
		}
	})

	t.Run("svg_invalid_viewbox", func(t *testing.T) {
		p := filepath.Join(dir, "zero.svg")
		if err := os.WriteFile(p, []byte(drawCovBadViewBoxSVG), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := loadImage(p); err == nil {
			t.Error("zero viewBox should be an error")
		}
	})

	t.Run("remote_url", func(t *testing.T) {
		drawCovRemoteDir(t)
		var pngBytes bytes.Buffer
		src := image.NewRGBA(image.Rect(0, 0, 4, 4))
		if err := png.Encode(&pngBytes, src); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(pngBytes.Bytes())
		}))
		defer srv.Close()

		_, w, h, err := loadImage(srv.URL + "/remote.png")
		if err != nil {
			t.Fatalf("remote loadImage: %v", err)
		}
		if w != 4 || h != 4 {
			t.Errorf("remote image = %dx%d, want 4x4", w, h)
		}

		// A dead server → fetch error path.
		dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		deadURL := dead.URL
		dead.Close()
		if _, _, _, err := loadImage(deadURL + "/gone.png"); err == nil {
			t.Error("fetch failure should surface as a loadImage error")
		}
	})
}

func TestDrawCovCopyImageToFrameBuffer(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	want := color.RGBA{10, 20, 30, 255}
	img.SetRGBA(2, 1, want)
	frame := make([]color.RGBA, 6)
	copyImageToFrameBuffer(img, frame)
	if frame[1*3+2] != want {
		t.Errorf("frame[5] = %v, want %v", frame[5], want)
	}
	if frame[0] != (color.RGBA{}) {
		t.Errorf("frame[0] = %v, want zero", frame[0])
	}
}

func TestDrawCovCopyImageToImageAtBlending(t *testing.T) {
	dst := image.NewRGBA(image.Rect(0, 0, 20, 20))
	fillRect(dst, 0, 0, 20, 20, color.RGBA{100, 100, 100, 255})

	// A source with mixed alpha exercises the blend fallback: skipped (0),
	// direct copy (255), and mixed (128) pixels.
	src := image.NewRGBA(image.Rect(0, 0, 3, 1))
	src.SetRGBA(0, 0, color.RGBA{0, 0, 0, 0})
	src.SetRGBA(1, 0, color.RGBA{255, 0, 0, 255})
	src.SetRGBA(2, 0, color.RGBA{200, 0, 0, 128})

	if err := copyImageToImageAt(dst, src, 5, 5); err != nil {
		t.Fatalf("copyImageToImageAt: %v", err)
	}
	if got := dst.RGBAAt(5, 5); got != (color.RGBA{100, 100, 100, 255}) {
		t.Errorf("transparent pixel overwrote dst: %v", got)
	}
	if got := dst.RGBAAt(6, 5); got != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("opaque pixel not copied: %v", got)
	}
	blended := dst.RGBAAt(7, 5)
	if blended.R <= 100 || blended.R >= 200 {
		t.Errorf("semi-transparent pixel not blended: %v", blended)
	}

	// Fast (row-wise) path: fully opaque source at a non-zero x offset.
	opaque := image.NewRGBA(image.Rect(0, 0, 4, 4))
	fillRect(opaque, 0, 0, 4, 4, color.RGBA{0, 255, 0, 255})
	if err := copyImageToImageAt(dst, opaque, 3, 10); err != nil {
		t.Fatalf("opaque copy: %v", err)
	}
	if dst.RGBAAt(3, 10) != (color.RGBA{0, 255, 0, 255}) {
		t.Error("fast path did not paint")
	}

	// Ultra-fast path: opaque source spanning the full destination width at x0=0.
	full := image.NewRGBA(image.Rect(0, 0, 20, 2))
	fillRect(full, 0, 0, 20, 2, color.RGBA{0, 0, 255, 255})
	if err := copyImageToImageAt(dst, full, 0, 15); err != nil {
		t.Fatalf("full-width copy: %v", err)
	}
	if dst.RGBAAt(19, 16) != (color.RGBA{0, 0, 255, 255}) {
		t.Error("ultra-fast path did not paint")
	}
}

func TestDrawCovStitchFramesOptimized(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	blue := color.RGBA{0, 0, 255, 255}

	mk := func(w, h int, c color.RGBA) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		fillRect(img, 0, 0, w, h, c)
		return img
	}

	t.Run("nil_inputs", func(t *testing.T) {
		dst := mk(8, 4, color.RGBA{})
		if err := stitchFramesOptimized(nil, mk(4, 4, red), mk(4, 4, blue)); err == nil {
			t.Error("nil dst must error")
		}
		if err := stitchFramesOptimized(dst, nil, mk(4, 4, blue)); err == nil {
			t.Error("nil left must error")
		}
		if err := stitchFramesOptimized(dst, mk(4, 4, red), nil); err == nil {
			t.Error("nil right must error")
		}
	})

	t.Run("height_mismatch", func(t *testing.T) {
		if err := stitchFramesOptimized(mk(8, 4, color.RGBA{}), mk(4, 4, red), mk(4, 2, blue)); err == nil {
			t.Error("mismatched heights must error")
		}
	})

	t.Run("exceeds_destination", func(t *testing.T) {
		if err := stitchFramesOptimized(mk(6, 4, color.RGBA{}), mk(4, 4, red), mk(4, 4, blue)); err == nil {
			t.Error("frames wider than dst must error")
		}
	})

	t.Run("fast_path", func(t *testing.T) {
		dst := mk(8, 4, color.RGBA{})
		if err := stitchFramesOptimized(dst, mk(4, 4, red), mk(4, 4, blue)); err != nil {
			t.Fatalf("stitch: %v", err)
		}
		if dst.RGBAAt(1, 1) != red || dst.RGBAAt(6, 1) != blue {
			t.Error("fast path stitched pixels wrong")
		}
	})

	t.Run("stride_fallback_path", func(t *testing.T) {
		// SubImages carry non-zero Min and a parent stride, forcing the
		// standard path.
		parent := mk(10, 10, red)
		left := parent.SubImage(image.Rect(1, 1, 5, 5)).(*image.RGBA)
		parentB := mk(10, 10, blue)
		right := parentB.SubImage(image.Rect(2, 2, 6, 6)).(*image.RGBA)
		dst := mk(8, 4, color.RGBA{})
		if err := stitchFramesOptimized(dst, left, right); err != nil {
			t.Fatalf("stitch subimages: %v", err)
		}
		if dst.RGBAAt(0, 0) != red || dst.RGBAAt(7, 3) != blue {
			t.Error("fallback path stitched pixels wrong")
		}
	})
}

func TestDrawCovDrawRoundedRect(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 50, 40))
	gc := draw2dimg.NewGraphicContext(img)
	gc.SetFillColor(color.RGBA{255, 0, 0, 255})
	drawRoundedRect(gc, 5, 5, 40, 30, 6)
	gc.Fill()
	if img.RGBAAt(25, 20).R == 0 {
		t.Error("rounded rect interior not filled")
	}
	if img.RGBAAt(5, 5).A != 0 {
		t.Error("rounded corner should stay outside the path")
	}
}

func TestDrawCovRasterizeIconAAAndTint(t *testing.T) {
	icon, err := drawCovParseIcon(drawCovSVG)
	if err != nil {
		t.Fatalf("parse icon: %v", err)
	}

	// Clamped degenerate inputs.
	img := rasterizeIconAA(icon, 0, 0, 0)
	if img.Bounds().Dx() != 1 || img.Bounds().Dy() != 1 {
		t.Errorf("degenerate rasterize = %v, want 1x1", img.Bounds())
	}

	plain := rasterizeIconAA(icon, 10, 10, 1) // aaScale==1: no downscale
	if plain.Bounds().Dx() != 10 || !drawCovHasInk(plain) {
		t.Error("1:1 rasterize produced no ink")
	}
	aa := rasterizeIconAA(icon, 10, 10, 2) // supersampled
	if aa.Bounds().Dx() != 10 || !drawCovHasInk(aa) {
		t.Error("AA rasterize produced no ink")
	}

	// Tint: transparent stays transparent, bright yellow becomes ~the tint,
	// over-bright pixels clamp at f=1.
	src := image.NewRGBA(image.Rect(0, 0, 3, 1))
	src.SetRGBA(0, 0, color.RGBA{0, 0, 0, 0})
	src.SetRGBA(1, 0, color.RGBA{253, 224, 33, 255})  // asset yellow, luma≈211
	src.SetRGBA(2, 0, color.RGBA{255, 255, 255, 255}) // luma 255 > ref → clamp
	tint := color.RGBA{10, 200, 30, 255}
	out := tintIconImage(src, tint)
	if out.RGBAAt(0, 0).A != 0 {
		t.Error("transparent pixel gained alpha")
	}
	got := out.RGBAAt(1, 0)
	if got.A != 255 || got.G < 190 || got.G > 210 {
		t.Errorf("yellow pixel tinted to %v, want ≈%v", got, tint)
	}
	if out.RGBAAt(2, 0) != (color.RGBA{10, 200, 30, 255}) {
		t.Errorf("over-bright pixel = %v, want exactly the tint", out.RGBAAt(2, 0))
	}
}

func TestDrawCovDrawSVGTinted(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sq.svg")
	if err := os.WriteFile(p, []byte(drawCovSVG), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("plain_and_cache", func(t *testing.T) {
		frame := image.NewRGBA(image.Rect(0, 0, 30, 30))
		if err := drawSVG(frame, p, 2, 2, 12, 12); err != nil {
			t.Fatalf("drawSVG: %v", err)
		}
		if !drawCovHasInk(frame) {
			t.Error("drawSVG drew nothing")
		}
		// Second call with the same key hits svgCache.
		frame2 := image.NewRGBA(image.Rect(0, 0, 30, 30))
		if err := drawSVG(frame2, p, 2, 2, 12, 12); err != nil {
			t.Fatalf("cached drawSVG: %v", err)
		}
		if !drawCovHasInk(frame2) {
			t.Error("cached drawSVG drew nothing")
		}
	})

	t.Run("intrinsic_size", func(t *testing.T) {
		frame := image.NewRGBA(image.Rect(0, 0, 30, 30))
		if err := drawSVG(frame, p, 0, 0, 0, 0); err != nil { // 0 → viewBox 10x10
			t.Fatalf("intrinsic drawSVG: %v", err)
		}
		if !drawCovHasInk(frame) {
			t.Error("intrinsic-size drawSVG drew nothing")
		}
	})

	t.Run("tinted", func(t *testing.T) {
		frame := image.NewRGBA(image.Rect(0, 0, 30, 30))
		tint := color.RGBA{0, 128, 255, 255}
		if err := drawSVGTinted(frame, p, 0, 0, 10, 10, &tint); err != nil {
			t.Fatalf("drawSVGTinted: %v", err)
		}
		if frame.RGBAAt(5, 5).B < 200 {
			t.Errorf("tinted pixel = %v, want strongly blue", frame.RGBAAt(5, 5))
		}
	})

	t.Run("errors", func(t *testing.T) {
		frame := image.NewRGBA(image.Rect(0, 0, 30, 30))
		if err := drawSVG(frame, filepath.Join(dir, "nope.svg"), 0, 0, 10, 10); err == nil {
			t.Error("missing file should error")
		}
		bad := filepath.Join(dir, "bad.svg")
		if err := os.WriteFile(bad, []byte("<svg"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := drawSVG(frame, bad, 0, 0, 10, 10); err == nil {
			t.Error("corrupt svg should error")
		}
		zero := filepath.Join(dir, "zerovb.svg")
		if err := os.WriteFile(zero, []byte(drawCovBadViewBoxSVG), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := drawSVG(frame, zero, 0, 0, 0, 0); err == nil {
			t.Error("zero-size target should error")
		}
	})
}

func TestDrawCovRenderAndCache(t *testing.T) {
	ensureImageCache()
	frame := image.NewRGBA(image.Rect(0, 0, 20, 20))
	renderAndCache(bytes.NewBufferString(drawCovSVG), "drawcov:renderandcache", frame, 0, 0)
	if !drawCovHasInk(frame) {
		t.Error("renderAndCache drew nothing")
	}
	// Broken SVG: error path logs and leaves the frame untouched.
	frame2 := image.NewRGBA(image.Rect(0, 0, 20, 20))
	renderAndCache(bytes.NewBufferString("<svg"), "drawcov:renderandcache:bad", frame2, 0, 0)
	if drawCovHasInk(frame2) {
		t.Error("failed render painted pixels")
	}
}

func TestDrawCovClampBarRadiusAndDiskHelpers(t *testing.T) {
	if got := clampBarRadius(-3, 10); got != 0 {
		t.Errorf("negative radius clamped to %d, want 0", got)
	}
	if got := clampBarRadius(9, 10); got != 5 {
		t.Errorf("radius > h/2 clamped to %d, want 5", got)
	}
	if got := clampBarRadius(4, 0); got != 4 {
		t.Errorf("h<=0 should leave radius alone, got %d", got)
	}

	drawCovStashGlobal(t, "DrawCovPct", 42)
	if got := diskBarPercent("DrawCovPct"); got != 42 {
		t.Errorf("int percent = %v, want 42", got)
	}
	globalData.Store("DrawCovPct", 13.5)
	if got := diskBarPercent("DrawCovPct"); got != 13.5 {
		t.Errorf("float percent = %v, want 13.5", got)
	}
	globalData.Store("DrawCovPct", "not a number")
	if got := diskBarPercent("DrawCovPct"); got != 0 {
		t.Errorf("string percent = %v, want 0", got)
	}
	if got := diskBarPercent("DrawCovPctMissing"); got != 0 {
		t.Errorf("missing percent = %v, want 0", got)
	}

	drawCovStashGlobal(t, "DrawCovPresent", "yes") // wrong type
	if diskBarPresent("DrawCovPresent") {
		t.Error("non-bool presence flag treated as true")
	}
	globalData.Store("DrawCovPresent", true)
	if !diskBarPresent("DrawCovPresent") {
		t.Error("true presence flag not seen")
	}
}

func TestDrawCovGetBarInnerMaskDegenerate(t *testing.T) {
	ensureImageCache()
	// Inset consumes the whole box → the cached empty-mask branch.
	empty := getBarInnerMask(4, 4, 2, 3)
	if drawCovHasInk(empty) {
		t.Error("degenerate mask should be fully transparent")
	}
	// And again from the cache.
	empty2 := getBarInnerMask(4, 4, 2, 3)
	if empty2 != empty {
		t.Error("degenerate mask was not cached")
	}
	// Negative inset is clamped to 0.
	m := getBarInnerMask(12, 8, 3, -2)
	if !drawCovHasInk(m) {
		t.Error("mask with clamped inset should have an opaque interior")
	}
}

func TestDrawCovHbarFillSVG(t *testing.T) {
	// Empty fill → just an empty canvas.
	b := hbarFillSVG(20, 10, 1, 1, 0, 8, 3, false, "#FFAA00")
	if _, err := drawCovParseIcon(string(b)); err != nil {
		t.Errorf("empty fill svg does not parse: %v", err)
	}
	// Full → Roundrect branch.
	b = hbarFillSVG(20, 10, 1, 1, 18, 8, 3, true, "#FFAA00")
	if _, err := drawCovParseIcon(string(b)); err != nil {
		t.Errorf("full fill svg does not parse: %v", err)
	}
	// fr <= 0 → Roundrect branch too.
	b = hbarFillSVG(20, 10, 1, 1, 10, 8, 0, false, "#FFAA00")
	if _, err := drawCovParseIcon(string(b)); err != nil {
		t.Errorf("square fill svg does not parse: %v", err)
	}
	// Partial → left-rounded path branch.
	b = hbarFillSVG(20, 10, 1, 1, 10, 8, 3, false, "#FFAA00")
	if !bytes.Contains(b, []byte("<path")) {
		t.Error("partial fill should use the path form")
	}
}
