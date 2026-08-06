package main

// GPS page: an extra cfg page that exists in the rotation only while
// pcat-manager-web reports the GPS feature usable AND enabled (the web UI's
// "GPS Page" toggle on a GNSS-capable modem). The page template ships as the
// LAST pageN in the embedded config; at boot it is hidden, and the collector
// below adds/removes it by adjusting cfgNumPages/totalNumPages — the same
// runtime page-count mutation the SMS pages already do.
//
// Data comes from GET /api/v1/gps.json on localhost (auth-exempt there). All
// values land in globalData preformatted; "--" renders literally, "-" renders
// as an empty slot (draw.go convention).

import (
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	gpsPageConfigured = false // template's last cfg page carries Gps* keys
	gpsBaseCfgPages   = 0     // page count including the GPS page
	gpsPageShown      = false
	gpsReschedule     = make(chan struct{}, 1)
)

type gpsWebResponse struct {
	Capable bool `json:"capable"`
	Enabled bool `json:"enabled"`
	Powered bool `json:"powered"`
	Fix     struct {
		HasFix    bool     `json:"has_fix"`
		FixType   string   `json:"fix_type"`
		Lat       *float64 `json:"lat"`
		Lon       *float64 `json:"lon"`
		AltM      *float64 `json:"alt_m"`
		AccuracyM *float64 `json:"accuracy_m"`
		SpeedKmh  *float64 `json:"speed_kmh"`
		CourseDeg *float64 `json:"course_deg"`
		SatsUsed  int      `json:"sats_used"`
	} `json:"fix"`
	Satellites struct {
		InView int `json:"in_view"`
	} `json:"satellites"`
}

// detectGpsPage runs inside validateConfig, right after cfgNumPages is set:
// if the highest-numbered page binds any Gps* data key it is the GPS page,
// and it starts hidden until collectGpsData learns the feature is on.
func detectGpsPage() {
	gpsBaseCfgPages = cfgNumPages
	gpsPageConfigured = false
	last, ok := cfg.DisplayTemplate.Elements["page"+strconv.Itoa(gpsBaseCfgPages-1)]
	if ok {
		for _, el := range last {
			if strings.HasPrefix(el.DataKey, "Gps") {
				gpsPageConfigured = true
				break
			}
		}
	}
	if gpsPageConfigured && !gpsPageShown {
		cfgNumPages = gpsBaseCfgPages - 1
	}
}

// updateGpsPagePresence adds/removes the GPS page from the rotation. The
// render loop re-derives currPageIdx % totalNumPages every frame, so a
// shrink while the GPS page is showing just wraps to page 0.
func updateGpsPagePresence(want bool) {
	if !gpsPageConfigured || want == gpsPageShown {
		return
	}
	gpsPageShown = want
	if want {
		cfgNumPages = gpsBaseCfgPages
	} else {
		cfgNumPages = gpsBaseCfgPages - 1
	}
	if cfg.ShowSms {
		totalNumPages = cfgNumPages + lenSmsPagesImages
	} else {
		totalNumPages = cfgNumPages
	}
	// If the display is parked on (or past) the page we just removed, nothing
	// re-renders on its own — kick the normal page-change path so the stale
	// page animates away instead of lingering until a button press.
	if !want && currPageIdx >= cfgNumPages {
		httpChangePageTriggered = true
	}
	log.Println("gps page shown:", want, "cfgNumPages:", cfgNumPages,
		"totalNumPages:", totalNumPages)
}

// currentGpsInterval: 1 s while awake on the GPS page (live speed/heading),
// otherwise 30 s — enough to track the web toggle and fix state cheaply.
func currentGpsInterval() time.Duration {
	if idleState != STATE_IDLE && gpsPageShown && currPageIdx == cfgNumPages-1 {
		return 1 * time.Second
	}
	return 30 * time.Second
}

func signalGpsReschedule() {
	select {
	case gpsReschedule <- struct{}{}:
	default:
	}
}

// gpsCardinal maps a course in degrees to one of the 8 compass points.
//
// The heading is normalised into [0,360) first. Go's % keeps the sign of the
// dividend, so a negative course (some modems report one, and a NaN/garbage
// value from the web API deserialises to 0 but a signed one does not) would
// otherwise index the slice negatively and panic — taking the whole daemon
// down with it, since collectGpsData runs on its own goroutine.
func gpsCardinal(deg float64) string {
	dirs := []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
	if math.IsNaN(deg) || math.IsInf(deg, 0) {
		return dirs[0]
	}
	deg = math.Mod(deg, 360)
	if deg < 0 {
		deg += 360
	}
	idx := int((deg+22.5)/45.0) % 8
	return dirs[idx]
}

func collectGpsData() {
	resp, err := localHTTPClient.Get("http://localhost:80/api/v1/gps.json")
	if err != nil {
		updateGpsPagePresence(false)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		updateGpsPagePresence(false)
		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		updateGpsPagePresence(false)
		return
	}
	var g gpsWebResponse
	if err := secureUnmarshal(body, &g); err != nil {
		updateGpsPagePresence(false)
		return
	}

	updateGpsPagePresence(g.Capable && g.Enabled)

	const dash = "--"
	fix := g.Fix
	switch {
	case !g.Powered:
		globalData.Store("GpsFix", "Off")
	case !fix.HasFix:
		globalData.Store("GpsFix", "No Fix")
	default:
		globalData.Store("GpsFix", fix.FixType)
	}

	// Sats read as "7/12" beside a satellite icon — the slash already says
	// "used of in view", so the spaces (and the label) are gone.
	if g.Powered {
		globalData.Store("GpsSats", fmt.Sprintf("%d/%d", fix.SatsUsed, g.Satellites.InView))
	} else {
		globalData.Store("GpsSats", dash)
	}

	// Speed carries the km/h unit, so it needs no label. Always whole numbers:
	// the tenth is below GNSS doppler noise anyway, and dropping it lets the
	// readout render at the page's largest font without a decimal point
	// eating a glyph slot on a 172px panel.
	if fix.HasFix && fix.SpeedKmh != nil {
		globalData.Store("GpsSpeed", fmt.Sprintf("%.0f", *fix.SpeedKmh))
	} else {
		globalData.Store("GpsSpeed", dash)
	}
	// GpsCourse stays as text for any layout that still binds it; GpsCourseDeg
	// is the raw float the compass tape rotates with, and its presence doubles
	// as the "we have a heading" flag (absent → tape greys out).
	if fix.HasFix && fix.CourseDeg != nil {
		globalData.Store("GpsCourse", fmt.Sprintf("%.0f° %s", *fix.CourseDeg, gpsCardinal(*fix.CourseDeg)))
		globalData.Store("GpsCourseDeg", normalizeDeg(*fix.CourseDeg))
	} else {
		globalData.Store("GpsCourse", dash)
		globalData.Delete("GpsCourseDeg")
	}
	if fix.HasFix && fix.AltM != nil {
		globalData.Store("GpsAlt", fmt.Sprintf("%.0f", *fix.AltM))
	} else {
		globalData.Store("GpsAlt", dash)
	}
	// Accuracy renders as "+-4m": the sign is the label. It is spelled "+-"
	// and not "±" because Orbitron has no U+00B1 glyph — that renders as a
	// tofu box on the panel.
	if fix.HasFix && fix.AccuracyM != nil {
		globalData.Store("GpsAccuracy", fmt.Sprintf("+-%.0f", *fix.AccuracyM))
	} else {
		globalData.Store("GpsAccuracy", dash)
	}
	if fix.HasFix && fix.Lat != nil && fix.Lon != nil {
		latH, lonH := "N", "E"
		lat, lon := *fix.Lat, *fix.Lon
		if lat < 0 {
			latH, lat = "S", -lat
		}
		if lon < 0 {
			lonH, lon = "W", -lon
		}
		// Position lines are "31.2304° N" / "121.4737° E" — the hemisphere
		// letter is the only label a coordinate needs. Four decimals is ~11m,
		// finer than the fix itself and two glyphs narrower than six, which is
		// what lets the line render at a readable size on a 172px panel.
		globalData.Store("GpsLat", fmt.Sprintf("%.4f° %s", lat, latH))
		globalData.Store("GpsLon", fmt.Sprintf("%.4f° %s", lon, lonH))
	} else {
		// "-" renders as an intentionally empty slot
		globalData.Store("GpsLat", "-")
		globalData.Store("GpsLon", "-")
	}
}
