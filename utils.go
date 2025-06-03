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

	evdev "github.com/holoplot/go-evdev"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
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
        log.Printf("→ physical backlight %d", phys)
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
                log.Println("POWER pressed")
                if idleState == STATE_ACTIVE {
                    *changePageTriggered = true
                }
                lastActivityMu.Lock()
                lastActivity = now
                lastActivityMu.Unlock()
                lastKeyPress = now

            case 0: // key release
                // only trigger if it wasn’t a quick tap (<500ms)
                if now.Sub(lastKeyPress) > KEYBOARD_DEBOUNCE_TIME {
                    log.Println("POWER released")
                    if idleState == STATE_ACTIVE {
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

func idleDimmer() {
    ticker := time.NewTicker(25 * time.Millisecond)
    defer ticker.Stop()

	prevState := STATE_UNKNOWN
	var brightness int
	var newState int
	lastStateScreenOn := false
	
    for range ticker.C {
        data, err := ioutil.ReadFile("/sys/kernel/photonicat-pm/movement_trigger")
        if err == nil && strings.TrimSpace(string(data)) == "1" {
            now := time.Now()
            lastActivityMu.Lock()
            if time.Since(lastActivity) > 5 * time.Second {
                lastActivity = now
            }
            lastActivityMu.Unlock()
            lastStateScreenOn = false
            
        }

        lastActivityMu.Lock()
        idle := time.Since(lastActivity)
        lastActivityMu.Unlock()
        
        switch {
        case weAreRunning == false:
            p := float64(time.Since(offTime)) / float64(OFF_TIMEOUT) 
            brightness = int((1 - p) * float64(maxBacklight))
            if brightness < 10 {
				brightness = 10
			}
			newState = STATE_OFF
        case idle < fadeInDur && lastStateScreenOn == false:
            // 1) Fade in from 0→maxBacklight over fadeInDur
			desiredFPS = DEFAULT_FPS
			p := float64(idle) / float64(fadeInDur)      // goes 0→1
			if lastStateScreenOn {
				brightness = 100 
			}else{
				brightness = int(p * float64(maxBacklight))
				newState = STATE_FADE_IN
			}
        case idle < idleTimeout:
            // 2) Fully on during the “active” window
            brightness = maxBacklight
			newState  = STATE_ACTIVE
			lastStateScreenOn = true
        case idle < idleTimeout+fadeDuration:
            // 3) Fade out from maxBacklight→0 over fadeDuration
            p := float64(idle-idleTimeout) / float64(fadeDuration) 
            brightness = int((1 - p) * float64(maxBacklight)) // 1→0
			newState  = STATE_FADE_OUT
			lastStateScreenOn = true
        default:
            brightness = 0
			newState  = STATE_IDLE
			lastStateScreenOn = false
			desiredFPS = 1
        }
        setBacklight(brightness)
		// If the state changed, log it
        if newState != prevState {
            log.Printf("idleDimmer: state changed from %s to %s", stateName(prevState), stateName(newState))
            prevState = newState
        }
        idleState = newState 
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