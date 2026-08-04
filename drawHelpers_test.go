package main

// Pure drawing primitives. These run on every frame, and their bounds
// handling is what stands between a malformed config element and a panic in
// the render loop.

import (
	"image"
	"image/color"
	"testing"
)

// areFramesIdentical gates whether a frame is pushed over SPI at all, so a
// false "identical" would freeze the display and a false "different" would
// waste the bus every frame.
func TestAreFramesIdentical(t *testing.T) {
	mk := func(w, h int, fill color.RGBA) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		fillRect(img, 0, 0, w, h, fill)
		return img
	}
	red := color.RGBA{255, 0, 0, 255}
	blue := color.RGBA{0, 0, 255, 255}

	t.Run("identical_content", func(t *testing.T) {
		if !areFramesIdentical(mk(20, 10, red), mk(20, 10, red)) {
			t.Error("two identically filled frames reported as different")
		}
	})

	t.Run("different_content", func(t *testing.T) {
		if areFramesIdentical(mk(20, 10, red), mk(20, 10, blue)) {
			t.Error("differently filled frames reported as identical")
		}
	})

	t.Run("different_bounds", func(t *testing.T) {
		if areFramesIdentical(mk(20, 10, red), mk(10, 20, red)) {
			t.Error("frames of different shape reported as identical")
		}
	})

	t.Run("both_nil", func(t *testing.T) {
		if !areFramesIdentical(nil, nil) {
			t.Error("nil/nil should compare equal")
		}
	})

	t.Run("one_nil", func(t *testing.T) {
		if areFramesIdentical(mk(4, 4, red), nil) {
			t.Error("frame vs nil reported as identical")
		}
		if areFramesIdentical(nil, mk(4, 4, red)) {
			t.Error("nil vs frame reported as identical")
		}
	})

	// The comparison walks 1KB chunks; a difference in the final partial
	// chunk must still be caught.
	t.Run("difference_in_last_chunk", func(t *testing.T) {
		a := mk(40, 40, red) // 6400 bytes: 6 full chunks + a remainder
		b := mk(40, 40, red)
		b.Pix[len(b.Pix)-1] ^= 0xFF
		if areFramesIdentical(a, b) {
			t.Error("a difference in the trailing partial chunk was missed")
		}
	})

	// And a difference in the very first byte must short-circuit correctly.
	t.Run("difference_in_first_byte", func(t *testing.T) {
		a := mk(40, 40, red)
		b := mk(40, 40, red)
		b.Pix[0] ^= 0xFF
		if areFramesIdentical(a, b) {
			t.Error("a difference in the first byte was missed")
		}
	})
}

// fillRect must clip to the destination rather than panic, since element
// geometry comes from a user-editable config.
func TestFillRectClipsAndIgnoresDegenerate(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}

	t.Run("nil_destination", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("fillRect panicked on a nil destination: %v", r)
			}
		}()
		fillRect(nil, 0, 0, 10, 10, white)
	})

	t.Run("degenerate_sizes", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		for _, tc := range []struct{ w, h int }{{0, 5}, {5, 0}, {-3, 5}, {5, -3}, {0, 0}} {
			fillRect(img, 2, 2, tc.w, tc.h, white)
		}
		if img.RGBAAt(2, 2) != (color.RGBA{}) {
			t.Error("a degenerate rect painted pixels")
		}
	})

	t.Run("clips_past_edges", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("fillRect panicked drawing past the edge: %v", r)
			}
		}()
		fillRect(img, 5, 5, 100, 100, white) // overruns bottom-right
		fillRect(img, -50, -50, 100, 100, white)

		// The in-bounds part must actually be painted.
		if img.RGBAAt(9, 9) != white {
			t.Error("the in-bounds portion of an overrunning rect was not drawn")
		}
	})

	t.Run("entirely_outside_is_a_noop", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		fillRect(img, 100, 100, 5, 5, white)
		for i := range img.Pix {
			if img.Pix[i] != 0 {
				t.Fatal("an entirely out-of-bounds rect painted pixels")
			}
		}
	})

	t.Run("exact_fill", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 4, 3))
		fillRect(img, 0, 0, 4, 3, white)
		for y := 0; y < 3; y++ {
			for x := 0; x < 4; x++ {
				if img.RGBAAt(x, y) != white {
					t.Fatalf("pixel (%d,%d) not filled", x, y)
				}
			}
		}
	})
}

// elementFillColor resolves a config element's colour, falling back to the
// brand yellow when the config omits or truncates the triple.
func TestElementFillColor(t *testing.T) {
	tests := []struct {
		name string
		el   DisplayElement
		want color.RGBA
	}{
		{"rgb_triple", DisplayElement{Color: []int{10, 20, 30}}, color.RGBA{10, 20, 30, 255}},
		{"extra_components_ignored", DisplayElement{Color: []int{1, 2, 3, 4}}, color.RGBA{1, 2, 3, 255}},
		{"no_color_falls_back", DisplayElement{}, PCAT_YELLOW},
		{"nil_color_falls_back", DisplayElement{Color: nil}, PCAT_YELLOW},
		{"too_few_components_falls_back", DisplayElement{Color: []int{5, 6}}, PCAT_YELLOW},
		{"empty_slice_falls_back", DisplayElement{Color: []int{}}, PCAT_YELLOW},
		{"white", DisplayElement{Color: []int{255, 255, 255}}, color.RGBA{255, 255, 255, 255}},
		{"black_is_not_a_fallback", DisplayElement{Color: []int{0, 0, 0}}, color.RGBA{0, 0, 0, 255}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := elementFillColor(tt.el); got != tt.want {
				t.Errorf("elementFillColor(%+v) = %v, want %v", tt.el, got, tt.want)
			}
		})
	}
}

// The returned colour is always fully opaque — a translucent fill would let
// the previous frame bleed through on a panel with no alpha compositing.
func TestElementFillColorAlwaysOpaque(t *testing.T) {
	for _, el := range []DisplayElement{
		{Color: []int{1, 2, 3}},
		{Color: []int{0, 0, 0}},
		{},
	} {
		if got := elementFillColor(el); got.A != 255 {
			t.Errorf("elementFillColor(%+v).A = %d, want 255", el, got.A)
		}
	}
}

// pageHasOverflowText decides whether a page needs the scrolling ticker. A nil
// config or an unknown page index must answer false rather than panic.
func TestPageHasOverflowTextDegenerateInputs(t *testing.T) {
	if pageHasOverflowText(nil, 0) {
		t.Error("a nil config reported overflow")
	}

	empty := &Config{DisplayTemplate: DisplayTemplate{Elements: map[string][]DisplayElement{}}}
	if pageHasOverflowText(empty, 0) {
		t.Error("an empty template reported overflow")
	}
	if pageHasOverflowText(empty, 99) {
		t.Error("an out-of-range page index reported overflow")
	}
	if pageHasOverflowText(empty, -1) {
		t.Error("a negative page index reported overflow")
	}
}

// Elements that cannot produce visible text must never trigger the ticker:
// disabled ones, non-text ones, and ones whose data key holds a placeholder
// or an error sentinel.
func TestPageHasOverflowTextSkipsNonRenderingElements(t *testing.T) {
	globalData.Store("OverflowTestLong", "a very long string that would certainly not fit on a 172 pixel wide panel")
	globalData.Store("OverflowTestDash", "-")
	globalData.Store("OverflowTestEmpty", "")

	tests := []struct {
		name string
		el   DisplayElement
	}{
		{"disabled", DisplayElement{Enable: 0, Type: "text", DataKey: "OverflowTestLong"}},
		{"not_text", DisplayElement{Enable: 1, Type: "icon", DataKey: "OverflowTestLong"}},
		{"no_data_key", DisplayElement{Enable: 1, Type: "text", DataKey: ""}},
		{"missing_data_key", DisplayElement{Enable: 1, Type: "text", DataKey: "OverflowTestNeverStored"}},
		{"dash_placeholder", DisplayElement{Enable: 1, Type: "text", DataKey: "OverflowTestDash"}},
		{"empty_value", DisplayElement{Enable: 1, Type: "text", DataKey: "OverflowTestEmpty"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{DisplayTemplate: DisplayTemplate{
				Elements: map[string][]DisplayElement{"page0": {tt.el}},
			}}
			if pageHasOverflowText(c, 0) {
				t.Errorf("%s element triggered the scrolling ticker", tt.name)
			}
		})
	}
}

// A negative ping value is the "timed out" marker drawn as an X; it must not
// be treated as overflowing text.
func TestPageHasOverflowTextIgnoresPingTimeouts(t *testing.T) {
	for _, key := range []string{"Ping0", "Ping1"} {
		t.Run(key, func(t *testing.T) {
			saved, had := globalData.Load(key)
			defer func() {
				if had {
					globalData.Store(key, saved)
				}
			}()

			globalData.Store(key, -1)
			c := &Config{DisplayTemplate: DisplayTemplate{
				Elements: map[string][]DisplayElement{
					"page0": {{Enable: 1, Type: "text", DataKey: key, Font: "reg"}},
				},
			}}
			if pageHasOverflowText(c, 0) {
				t.Errorf("%s timeout marker was treated as overflowing text", key)
			}

			globalData.Store(key, int64(-1))
			if pageHasOverflowText(c, 0) {
				t.Errorf("%s int64 timeout marker was treated as overflowing text", key)
			}
		})
	}
}

// copyImageToImageAt is used for every composite; it must clip rather than
// panic when the source does not fit at the requested offset.
func TestCopyImageToImageAtClips(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}

	dst := image.NewRGBA(image.Rect(0, 0, 20, 20))
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	fillRect(src, 0, 0, 10, 10, white)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("copyImageToImageAt panicked: %v", r)
		}
	}()

	copyImageToImageAt(dst, src, 15, 15) // overruns the bottom-right
	copyImageToImageAt(dst, src, -5, -5) // overruns the top-left
	copyImageToImageAt(dst, src, 100, 100)

	// A fully in-bounds copy must land where asked.
	copyImageToImageAt(dst, src, 5, 5)
	if dst.RGBAAt(5, 5) != white {
		t.Error("in-bounds copy did not paint the destination")
	}
}
