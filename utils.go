package main

import (
	"image"
	"os"
	"time"
	"encoding/json"
	"io/ioutil"
	"log"
	"strconv"
	"fmt"
	"strings"
	"sync"
	"math"

	evdev "github.com/holoplot/go-evdev"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

var (
    fadeMu sync.Mutex
    fadeCancel chan struct{}
    swippingScreen bool
)

// loadConfig reads and unmarshals the config file.
func loadConfig(path string) (Config, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	err = json.Unmarshal(data, &cfg)
	return cfg, err
}

// getFontFace loads the font based on our mapping.
func getFontFace(fontName string) (font.Face, int, error) {
	cfg, ok := fonts[fontName]
	if !ok {
		return nil, 0, fmt.Errorf("font %s not found in mapping", fontName)
	}
	fontBytes, err := ioutil.ReadFile(cfg.FontPath)
	if err != nil {
		return nil, 0, fmt.Errorf("error reading font file: %v", err)
	}
	ttfFont, err := opentype.Parse(fontBytes)
	if err != nil {
		return nil, 0, fmt.Errorf("error parsing font: %v", err)
	}
	face, err := opentype.NewFace(ttfFont, &opentype.FaceOptions{
		Size:    cfg.FontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, 0, err
	}
	
	// Calculate font height using the ascent and descent metrics.
	metrics := face.Metrics()
	fontHeight := metrics.Ascent.Round() + metrics.Descent.Round()
	
	return face, fontHeight, nil
}

func clearFrame(frame *image.RGBA, width int, height int) {
	for i := 0; i < width * height * 4; i += 4 { //clear framebuffer
		frame.Pix[i] = 0       // R
		frame.Pix[i+1] = 0     // G
		frame.Pix[i+2] = 0     // B
		frame.Pix[i+3] = 255   // A (opaque black)
	}
}

func setBacklight(brightness int) {
    mu.Lock()
    defer mu.Unlock()

    // clamp into 0..100
    switch {
		case brightness < 0:
			brightness = 0
		case brightness > 100:
			brightness = 100
    }

    if brightness == lastLogical {
        return
    }
    lastLogical = brightness

    // cancel any pending off-timer if we're going to >0
    if brightness > 0 && offTimer != nil {
        offTimer.Stop()
        offTimer = nil
    }

    // choose what to write right now:
    phys := brightness
    if brightness == 0 {
        phys = 1
    }

    // perform the write
    if err := os.WriteFile("/sys/class/backlight/backlight/brightness", []byte(strconv.Itoa(phys)), 0644); err != nil {
        log.Printf("backlight write error: %v", err)
    } else {
        //log.Printf("→ physical backlight %d", phys)
    }

    // if we just handled a logical “0”, schedule the real off in ZERO_BACKLIGHT_DELAY s
    if brightness == 0 {
        offTimer = time.AfterFunc(ZERO_BACKLIGHT_DELAY, func() {
            mu.Lock()
            defer mu.Unlock()
            if lastLogical == 0 {
                // still supposed to be off, so write 0 now
                if err := os.WriteFile("/sys/class/backlight/backlight/brightness", []byte("1"), 0644); err != nil {
                    log.Printf("backlight final-off error: %v", err)
                } else {
                    log.Println("→ physical backlight OFF")
                }
            }
        })
    }
}

func monitorKeyboard(changePageTriggered *bool) {
    // 1) find the “rk805 pwrkey” device by name
    paths, err := evdev.ListDevicePaths()
    if err != nil {
        log.Printf("ListDevicePaths error: %v", err)
        return
    }

    var devPath string
    for _, ip := range paths {
        if ip.Name == "rk805 pwrkey" {
            devPath = ip.Path
            break
        }
    }
    if devPath == "" {
        log.Println("no EV_KEY device found")
        return
    }

    // 2) open it
    keyboard, err := evdev.Open(devPath)
    if err != nil {
        log.Printf("Open(%s) error: %v", devPath, err)
        return
    }
    defer keyboard.Ungrab()

    // 3) grab for exclusive access
    if err := keyboard.Grab(); err != nil {
        log.Printf("warning: failed to grab device: %v", err)
    }

    // 4) log what we opened
    name, _ := keyboard.Name()
    log.Printf("using input device: %s (%s)", devPath, name)

    var lastKeyPress time.Time
    for {
        ev, err := keyboard.ReadOne()
        if err != nil {
            log.Printf("read error: %v", err)
            time.Sleep(100 * time.Millisecond)
            continue
        }

        now := time.Now()
        if ev.Type == evdev.EV_KEY && ev.Code == evdev.KEY_POWER {
            switch ev.Value {
            case 1: // key press
                log.Println("POWER pressed, state =", stateName(idleState))
                if idleState == STATE_ACTIVE || idleState == STATE_FADE_IN {
                    swippingScreen = true
                    //*changePageTriggered = true
                }
                lastActivityMu.Lock()
                lastActivity = now
                lastActivityMu.Unlock()
                lastKeyPress = now

            case 0: // key release
                // only trigger if it wasn’t a quick tap (<500ms)
                if now.Sub(lastKeyPress) > KEYBOARD_DEBOUNCE_TIME {
                    log.Println("POWER released, state =", stateName(idleState))
                    if idleState == STATE_ACTIVE{
                        swippingScreen = true
                        *changePageTriggered = true
                    }
                    lastActivityMu.Lock()
                    lastActivity = now
                    lastActivityMu.Unlock()
                }
            }
        }
    }
}

func getBacklight() int {
    data, err := ioutil.ReadFile("/sys/class/backlight/backlight/brightness")
    if err != nil {
        log.Printf("getBacklight error: %v", err)
        return 0
    }
    return int(data[0])
}


func fadeBacklight(wantValue int, timePeriod time.Duration) {
	// Grab a snapshot of the “current” cancel channel under the fadeMu lock:
	fadeMu.Lock()
	cancelChan := fadeCancel
	fadeMu.Unlock()

	initValue := getBacklight()
	if timePeriod <= 0 || initValue == wantValue {
		setBacklight(wantValue)
		return
	}

	const stepDuration = 40 * time.Millisecond
	steps := int(timePeriod / stepDuration)
	if steps < 1 {
		setBacklight(wantValue)
		return
	}

	diff := wantValue - initValue
	ticker := time.NewTicker(stepDuration)
	defer ticker.Stop()

	for i := 1; i <= steps; i++ {
		select {
		case <-cancelChan:
			// Someone closed fadeCancel → abort immediately
            log.Println("fadeBacklight: cancel requested")
			return
		case <-ticker.C:
			frac := float64(i) / float64(steps)
			b := initValue + int(math.Round(frac*float64(diff)))
			setBacklight(b)
			log.Printf("fadeBacklight: step %d/%d → brightness=%d", i, steps, b)
		}
	}
	// final guarantee
	setBacklight(wantValue)
}

func idleDimmer() {
    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    prevState := STATE_UNKNOWN

    for range ticker.C {
        // 1) Movement/keypress detection
        data, err := ioutil.ReadFile("/sys/kernel/photonicat-pm/movement_trigger")
        if err == nil && strings.TrimSpace(string(data)) == "1" {
            // Reset idle timer, treat screen as already “on”
            now := time.Now()
            lastActivityMu.Lock()
            tempLastActivity := now.Add(-2 * time.Second)
            if tempLastActivity.After(lastActivity) {   
                lastActivity = tempLastActivity
            }
            lastActivityMu.Unlock()
        }

        // 2) Compute idle time
        lastActivityMu.Lock()
        idle := time.Since(lastActivity)
        lastActivityMu.Unlock()

        var newState int

        switch {
        case weAreRunning == false:
            newState = STATE_OFF
        case idle < fadeInDur:
            if swippingScreen {
                newState = STATE_ACTIVE
            } else {
                newState = STATE_FADE_IN
            }
        case idle < idleTimeout:
            newState = STATE_ACTIVE
            swippingScreen = false
        case idle < idleTimeout+fadeDuration:
            newState = STATE_FADE_OUT
        default:
            newState = STATE_IDLE
        }

        if prevState != newState {
            log.Printf("STATE CHANGED: %s -> %s", stateName(prevState), stateName(newState))
            idleState = newState
            prevState = newState

            // ── Cancel any existing fade by closing fadeCancel ──────────
			fadeMu.Lock()
            if fadeCancel != nil {
                close(fadeCancel)          // signal the currently running fadeBacklight (if any) to stop
                 // allocate a brand‑new channel
            }
            fadeCancel = make(chan struct{})
			//myCancel := fadeCancel
			fadeMu.Unlock()

            switch newState {
            case STATE_OFF:
                fadeBacklight(10, OFF_TIMEOUT)
                os.Exit(0)

            case STATE_FADE_IN:
                if !swippingScreen {
                    go fadeBacklight(maxBacklight, fadeInDur)
                }
            case STATE_FADE_OUT:
                go fadeBacklight(0, fadeDuration)

            case STATE_ACTIVE:
                setBacklight(maxBacklight)
                swippingScreen = false

            case STATE_IDLE:
                setBacklight(0)
                desiredFPS = 1
            }
        }
        
    }
}

func stateName(s int) string {
    switch s {
    case STATE_FADE_IN:
        return "FADE_IN"
    case STATE_ACTIVE:
        return "ACTIVE"
    case STATE_FADE_OUT:
        return "FADE_OUT"
    case STATE_IDLE:
        return "IDLE"
	case STATE_OFF:
		return "OFF"
    default:
        return "UNKNOWN"
    }
}