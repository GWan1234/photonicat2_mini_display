package main

// The GPS page is added to and removed from the page rotation at runtime by
// mutating cfgNumPages/totalNumPages. Getting that arithmetic wrong either
// strands the user on a blank page or hides a page that should be visible, so
// the presence logic is covered here alongside the pure formatting helpers.

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// withGpsState saves and restores every global the GPS page mutates, so these
// tests can run in any order without leaking state into the rest of the suite.
func withGpsState(t *testing.T) {
	t.Helper()
	sConfigured, sBase, sShown := gpsPageConfigured, gpsBaseCfgPages, gpsPageShown
	sCfgNum, sTotal, sCurr := cfgNumPages, totalNumPages, currPageIdx
	sSms, sLenSms, sTrig := cfg.ShowSms, lenSmsPagesImages, httpChangePageTriggered
	sIdle := idleState
	// The trip meter is a session accumulator, so a test that drives
	// collectGpsData would otherwise leave kilometres behind for the next one.
	sTrip := gpsTrip
	t.Cleanup(func() {
		gpsTrip = sTrip
		gpsPageConfigured, gpsBaseCfgPages, gpsPageShown = sConfigured, sBase, sShown
		cfgNumPages, totalNumPages, currPageIdx = sCfgNum, sTotal, sCurr
		cfg.ShowSms, lenSmsPagesImages, httpChangePageTriggered = sSms, sLenSms, sTrig
		idleState = sIdle
	})
}

func TestGpsCardinal(t *testing.T) {
	tests := []struct {
		deg  float64
		want string
	}{
		{0, "N"}, {22, "N"}, {23, "NE"},
		{45, "NE"}, {67, "NE"}, {68, "E"},
		{90, "E"}, {135, "SE"}, {180, "S"},
		{225, "SW"}, {270, "W"}, {315, "NW"},
		{337, "NW"}, {338, "N"}, {359.9, "N"},
		// Wrapping: 360 and beyond must fold back, not run off the slice.
		{360, "N"}, {405, "NE"}, {720, "N"}, {1080.5, "N"},
	}
	for _, tt := range tests {
		if got := gpsCardinal(tt.deg); got != tt.want {
			t.Errorf("gpsCardinal(%g) = %q, want %q", tt.deg, got, tt.want)
		}
	}
}

// Regression: gpsCardinal used to compute int((deg+22.5)/45) % 8 directly.
// Go's % keeps the dividend's sign, so any course below -22.5 produced a
// negative index and panicked — and because collectGpsData runs on its own
// goroutine, that panic took down the entire display daemon.
func TestGpsCardinalNegativeHeadingDoesNotPanic(t *testing.T) {
	tests := []struct {
		deg  float64
		want string
	}{
		{-1, "N"},
		{-10, "N"},
		{-70, "W"},  // equivalent to 290°
		{-90, "W"},  // equivalent to 270°
		{-180, "S"}, // equivalent to 180°
		{-359, "N"},
		{-400, "NW"}, // folds twice: -400 → -40 → 320°
	}
	for _, tt := range tests {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("gpsCardinal(%g) panicked: %v", tt.deg, r)
				}
			}()
			if got := gpsCardinal(tt.deg); got != tt.want {
				t.Errorf("gpsCardinal(%g) = %q, want %q", tt.deg, got, tt.want)
			}
		}()
	}
}

// Non-finite values reach here if the modem reports garbage; they must degrade
// to a default rather than panic on an out-of-range index.
func TestGpsCardinalNonFinite(t *testing.T) {
	for _, deg := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("gpsCardinal(%v) panicked: %v", deg, r)
				}
			}()
			if got := gpsCardinal(deg); got == "" {
				t.Errorf("gpsCardinal(%v) returned empty string", deg)
			}
		}()
	}
}

// detectGpsPage identifies the GPS page as the last cfg page bound to a Gps*
// data key, and hides it at boot until the feature is known to be on.
func TestDetectGpsPageHidesUntilEnabled(t *testing.T) {
	withGpsState(t)
	savedElements := cfg.DisplayTemplate.Elements
	t.Cleanup(func() { cfg.DisplayTemplate.Elements = savedElements })

	cfg.DisplayTemplate.Elements = map[string][]DisplayElement{
		"page0": {{DataKey: "BatterySoc"}},
		"page1": {{DataKey: "CpuUsage"}},
		"page2": {{DataKey: "GpsSpeed"}, {DataKey: "GpsFix"}},
	}
	cfgNumPages = 3
	gpsPageShown = false

	detectGpsPage()

	if !gpsPageConfigured {
		t.Fatal("detectGpsPage did not recognise the Gps* page")
	}
	if gpsBaseCfgPages != 3 {
		t.Errorf("gpsBaseCfgPages = %d, want 3", gpsBaseCfgPages)
	}
	if cfgNumPages != 2 {
		t.Errorf("cfgNumPages = %d, want 2 — the GPS page starts hidden", cfgNumPages)
	}
}

// A template with no Gps* keys must leave the page count untouched.
func TestDetectGpsPageNoGpsPageLeavesCountAlone(t *testing.T) {
	withGpsState(t)
	savedElements := cfg.DisplayTemplate.Elements
	t.Cleanup(func() { cfg.DisplayTemplate.Elements = savedElements })

	cfg.DisplayTemplate.Elements = map[string][]DisplayElement{
		"page0": {{DataKey: "BatterySoc"}},
		"page1": {{DataKey: "CpuUsage"}},
	}
	cfgNumPages = 2
	gpsPageShown = false

	detectGpsPage()

	if gpsPageConfigured {
		t.Error("detectGpsPage flagged a non-GPS template as GPS-configured")
	}
	if cfgNumPages != 2 {
		t.Errorf("cfgNumPages = %d, want 2 — no GPS page means no adjustment", cfgNumPages)
	}
}

func TestUpdateGpsPagePresence(t *testing.T) {
	withGpsState(t)

	gpsPageConfigured = true
	gpsBaseCfgPages = 4
	gpsPageShown = false
	cfgNumPages = 3
	cfg.ShowSms = false
	currPageIdx = 0

	// Show it.
	updateGpsPagePresence(true)
	if cfgNumPages != 4 {
		t.Errorf("after show: cfgNumPages = %d, want 4", cfgNumPages)
	}
	if totalNumPages != 4 {
		t.Errorf("after show: totalNumPages = %d, want 4", totalNumPages)
	}

	// Hide it again.
	updateGpsPagePresence(false)
	if cfgNumPages != 3 {
		t.Errorf("after hide: cfgNumPages = %d, want 3", cfgNumPages)
	}
	if totalNumPages != 3 {
		t.Errorf("after hide: totalNumPages = %d, want 3", totalNumPages)
	}
}

// With SMS on, totalNumPages must account for the SMS pages too — otherwise
// the rotation either skips SMS or wraps onto pages that do not exist.
func TestUpdateGpsPagePresenceIncludesSmsPages(t *testing.T) {
	withGpsState(t)

	gpsPageConfigured = true
	gpsBaseCfgPages = 4
	gpsPageShown = false
	cfgNumPages = 3
	cfg.ShowSms = true
	lenSmsPagesImages = 2
	currPageIdx = 0

	updateGpsPagePresence(true)
	if totalNumPages != 6 { // 4 cfg + 2 sms
		t.Errorf("totalNumPages = %d, want 6 (4 cfg + 2 sms)", totalNumPages)
	}

	updateGpsPagePresence(false)
	if totalNumPages != 5 { // 3 cfg + 2 sms
		t.Errorf("totalNumPages = %d, want 5 (3 cfg + 2 sms)", totalNumPages)
	}
}

// Removing the page the user is parked on must kick the page-change path, or
// the stale GPS page lingers on screen until someone presses a button.
func TestUpdateGpsPagePresenceHideFromGpsPageTriggersChange(t *testing.T) {
	withGpsState(t)

	gpsPageConfigured = true
	gpsBaseCfgPages = 4
	gpsPageShown = true
	cfgNumPages = 4
	cfg.ShowSms = false
	currPageIdx = 3 // sitting on the GPS page
	httpChangePageTriggered = false

	updateGpsPagePresence(false)

	if !httpChangePageTriggered {
		t.Error("hiding the visible GPS page did not trigger a page change")
	}
}

// Hiding while parked on an earlier page must NOT force a page change.
func TestUpdateGpsPagePresenceHideFromOtherPageIsQuiet(t *testing.T) {
	withGpsState(t)

	gpsPageConfigured = true
	gpsBaseCfgPages = 4
	gpsPageShown = true
	cfgNumPages = 4
	cfg.ShowSms = false
	currPageIdx = 0
	httpChangePageTriggered = false

	updateGpsPagePresence(false)

	if httpChangePageTriggered {
		t.Error("hiding an off-screen GPS page needlessly triggered a page change")
	}
}

// No-ops must stay no-ops: an unconfigured page, or a redundant call, must not
// move the page counters.
func TestUpdateGpsPagePresenceNoOps(t *testing.T) {
	t.Run("not_configured", func(t *testing.T) {
		withGpsState(t)
		gpsPageConfigured = false
		cfgNumPages, totalNumPages = 3, 3

		updateGpsPagePresence(true)

		if cfgNumPages != 3 || totalNumPages != 3 {
			t.Errorf("unconfigured GPS page changed counts: cfg=%d total=%d",
				cfgNumPages, totalNumPages)
		}
	})

	t.Run("already_in_wanted_state", func(t *testing.T) {
		withGpsState(t)
		gpsPageConfigured = true
		gpsPageShown = true
		gpsBaseCfgPages = 4
		cfgNumPages, totalNumPages = 4, 4

		updateGpsPagePresence(true) // already shown

		if cfgNumPages != 4 || totalNumPages != 4 {
			t.Errorf("redundant show changed counts: cfg=%d total=%d",
				cfgNumPages, totalNumPages)
		}
	})
}

// 1 s only while awake AND on the GPS page; 30 s otherwise. Polling the modem
// every second in the background would cost power for nothing.
func TestCurrentGpsInterval(t *testing.T) {
	withGpsState(t)
	gpsBaseCfgPages = 4
	cfgNumPages = 4

	tests := []struct {
		name    string
		idle    int
		shown   bool
		pageIdx int
		want    time.Duration
	}{
		{"awake_on_gps_page", STATE_ACTIVE, true, 3, 1 * time.Second},
		{"idle_on_gps_page", STATE_IDLE, true, 3, 30 * time.Second},
		{"awake_off_gps_page", STATE_ACTIVE, true, 1, 30 * time.Second},
		{"page_hidden", STATE_ACTIVE, false, 3, 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idleState, gpsPageShown, currPageIdx = tt.idle, tt.shown, tt.pageIdx
			if got := currentGpsInterval(); got != tt.want {
				t.Errorf("currentGpsInterval() = %v, want %v", got, tt.want)
			}
		})
	}
}

// signalGpsReschedule must never block, even when nobody is draining the
// channel — it is called from the render path.
func TestSignalGpsRescheduleDoesNotBlock(t *testing.T) {
	// Drain anything left over from other tests.
	for len(gpsReschedule) > 0 {
		<-gpsReschedule
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			signalGpsReschedule() // buffered size 1; extras must be dropped
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("signalGpsReschedule blocked")
	}

	if len(gpsReschedule) != 1 {
		t.Errorf("gpsReschedule holds %d items, want 1 (coalesced)", len(gpsReschedule))
	}
	<-gpsReschedule // leave it drained
}

// collectGpsData formats every value for direct rendering. "--" is the literal
// placeholder and "-" means "draw nothing", so the distinction matters.
func TestCollectGpsDataFormatsFix(t *testing.T) {
	withGpsState(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"capable": true, "enabled": true, "powered": true,
			"fix": {
				"has_fix": true, "fix_type": "3D",
				"lat": 31.230416, "lon": 121.473701,
				"alt_m": 12.7, "accuracy_m": 4.25,
				"speed_kmh": 63.44, "course_deg": 91.6,
				"sats_used": 9
			},
			"satellites": {"in_view": 14}
		}`))
	}))
	defer srv.Close()

	restore := redirectLocalHTTP(t, srv)
	defer restore()

	gpsPageConfigured = true
	gpsBaseCfgPages = 4
	gpsPageShown = false
	cfgNumPages = 3
	cfg.ShowSms = false

	collectGpsData()

	// The page renders label-free: the values carry their own meaning, so the
	// formats are the compact ones the icons sit beside.
	want := map[string]string{
		"GpsFix": "3D",
		// "used/in view" — the slash is the label.
		"GpsSats": "9/14",
		// At/above 10 km/h the tenth is dropped: it is noise at this speed and
		// costs a glyph the gigantic font needs.
		"GpsSpeed":  "63",
		"GpsCourse": "92° E",
		"GpsAlt":    "13",
		// Drawn with a composed ±: Orbitron carries no U+00B1 glyph.
		"GpsAccuracy": "±4",
		// 4 decimals ≈ 11 m, finer than the fix itself and narrow enough to
		// render large.
		"GpsLat": "31.2304° N",
		"GpsLon": "121.4737° E",
		// First fix of the session: it anchors the trip meter, and anchoring
		// is not travelling.
		"GpsTrip": "0.0",
	}
	for key, exp := range want {
		got, ok := globalData.Load(key)
		if !ok {
			t.Errorf("%s was not stored", key)
			continue
		}
		if got != exp {
			t.Errorf("%s = %v, want %v", key, got, exp)
		}
	}

	if !gpsPageShown {
		t.Error("a capable+enabled GPS response should show the page")
	}
}

// Southern/western coordinates must flip the hemisphere letter and drop the
// sign, not render as a negative number.
func TestCollectGpsDataSouthWestHemispheres(t *testing.T) {
	withGpsState(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"capable": true, "enabled": true, "powered": true,
			"fix": {"has_fix": true, "fix_type": "3D", "lat": -33.865143, "lon": -70.6693, "sats_used": 5},
			"satellites": {"in_view": 8}
		}`))
	}))
	defer srv.Close()

	restore := redirectLocalHTTP(t, srv)
	defer restore()

	gpsPageConfigured = true
	gpsBaseCfgPages = 4
	cfgNumPages = 3

	collectGpsData()

	if got, _ := globalData.Load("GpsLat"); got != "33.8651° S" {
		t.Errorf("GpsLat = %v, want \"33.8651° S\"", got)
	}
	if got, _ := globalData.Load("GpsLon"); got != "70.6693° W" {
		t.Errorf("GpsLon = %v, want \"70.6693° W\"", got)
	}
}

// No fix / powered off must produce placeholders rather than stale or zeroed
// readings — a "0.0 km/h" when there is no fix would be a lie.
func TestCollectGpsDataPlaceholders(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantFix  string
		wantSats string
	}{
		{
			name:     "powered_off",
			body:     `{"capable":true,"enabled":true,"powered":false,"fix":{"has_fix":false},"satellites":{"in_view":0}}`,
			wantFix:  "Off",
			wantSats: "--",
		},
		{
			name:     "no_fix",
			body:     `{"capable":true,"enabled":true,"powered":true,"fix":{"has_fix":false,"sats_used":0},"satellites":{"in_view":3}}`,
			wantFix:  "No Fix",
			wantSats: "0/3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withGpsState(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			restore := redirectLocalHTTP(t, srv)
			defer restore()

			gpsPageConfigured = true
			gpsBaseCfgPages = 4
			cfgNumPages = 3

			collectGpsData()

			if got, _ := globalData.Load("GpsFix"); got != tt.wantFix {
				t.Errorf("GpsFix = %v, want %v", got, tt.wantFix)
			}
			if got, _ := globalData.Load("GpsSats"); got != tt.wantSats {
				t.Errorf("GpsSats = %v, want %v", got, tt.wantSats)
			}
			for _, key := range []string{"GpsSpeed", "GpsCourse", "GpsAlt", "GpsAccuracy"} {
				if got, _ := globalData.Load(key); got != "--" {
					t.Errorf("%s = %v, want \"--\" with no fix", key, got)
				}
			}
			// "-" is the draw-nothing marker, distinct from the "--" literal.
			for _, key := range []string{"GpsLat", "GpsLon"} {
				if got, _ := globalData.Load(key); got != "-" {
					t.Errorf("%s = %v, want \"-\" with no fix", key, got)
				}
			}
		})
	}
}

// Every failure mode of the web call must hide the page rather than leave a
// stale one in the rotation.
func TestCollectGpsDataFailuresHidePage(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"http_500", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }},
		{"bad_json", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{not json`)) }},
		{"gps_disabled", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"capable":true,"enabled":false,"powered":false,"fix":{},"satellites":{}}`))
		}},
		{"not_capable", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"capable":false,"enabled":true,"powered":false,"fix":{},"satellites":{}}`))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withGpsState(t)
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			restore := redirectLocalHTTP(t, srv)
			defer restore()

			gpsPageConfigured = true
			gpsBaseCfgPages = 4
			gpsPageShown = true
			cfgNumPages = 4
			cfg.ShowSms = false
			currPageIdx = 0

			collectGpsData()

			if gpsPageShown {
				t.Error("GPS page still shown after a failed/disabled response")
			}
			if cfgNumPages != 3 {
				t.Errorf("cfgNumPages = %d, want 3 after hiding", cfgNumPages)
			}
		})
	}
}

// An unreachable server must not panic or hang the collector.
func TestCollectGpsDataUnreachableServer(t *testing.T) {
	withGpsState(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close() // nothing is listening now

	restore := redirectLocalHTTPURL(t, addr)
	defer restore()

	gpsPageConfigured = true
	gpsBaseCfgPages = 4
	gpsPageShown = true
	cfgNumPages = 4
	cfg.ShowSms = false

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("collectGpsData panicked on a dead server: %v", r)
			}
		}()
		collectGpsData()
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("collectGpsData hung against a dead server")
	}

	if gpsPageShown {
		t.Error("GPS page still shown after an unreachable server")
	}
}

// Speed is always whole. Walking pace used to keep a decimal ("8.4"), which
// cost a glyph slot in the page's largest font; the tenth is below GNSS
// doppler noise anyway, so every speed now rounds.
func TestCollectGpsDataSpeedHasNoDecimals(t *testing.T) {
	cases := []struct {
		kmh  float64
		want string
	}{
		{0, "0"},
		{0.4, "0"},
		{0.6, "1"},
		{8.44, "8"},
		{9.99, "10"},
		{63.44, "63"},
		{188.7, "189"},
	}

	for _, c := range cases {
		withGpsState(t)

		body := fmt.Sprintf(`{
			"capable": true, "enabled": true, "powered": true,
			"fix": {"has_fix": true, "fix_type": "3D", "speed_kmh": %v, "sats_used": 6},
			"satellites": {"in_view": 9}
		}`, c.kmh)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		}))
		restore := redirectLocalHTTP(t, srv)

		gpsPageConfigured = true
		gpsBaseCfgPages = 4
		cfgNumPages = 3

		collectGpsData()

		got, _ := globalData.Load("GpsSpeed")
		if got != c.want {
			t.Errorf("%v km/h -> GpsSpeed = %v, want %v", c.kmh, got, c.want)
		}
		if s, ok := got.(string); ok && strings.Contains(s, ".") {
			t.Errorf("%v km/h -> GpsSpeed = %q still has a decimal point", c.kmh, s)
		}

		restore()
		srv.Close()
	}
}

// A receiver standing still still moves: its reported position wanders inside
// the error circle, and at one sample a second an unfiltered accumulator would
// invent kilometres a day out of that wander. Nothing here is real motion, so
// the trip total must not move at all.
func TestGpsTripIgnoresReceiverJitter(t *testing.T) {
	var trip gpsTripMeter
	base := time.Unix(1700000000, 0)
	// ~4 m of scatter around one point, which is what a ±4 m fix looks like.
	const degPer4m = 4.0 / 111320.0
	offsets := []float64{0, 1, -1, 0.7, -0.9, 0.4, -0.6, 1, -1, 0.3}

	for i, o := range offsets {
		trip.add(31.2304+o*degPer4m, 121.4737-o*degPer4m, 4, base.Add(time.Duration(i)*time.Second))
	}
	if trip.meters != 0 {
		t.Errorf("stationary receiver logged %.1f m", trip.meters)
	}
}

// The flip side: every step of a walk is smaller than the noise floor, so a
// filter that re-anchored on each rejected step would throw the whole walk
// away one metre at a time. The anchor has to survive rejection.
func TestGpsTripAccumulatesStepsBelowTheNoiseFloor(t *testing.T) {
	var trip gpsTripMeter
	base := time.Unix(1700000000, 0)
	const degPerM = 1.0 / 111320.0

	// 200 s of walking due north at 1.4 m/s: 280 m, in steps of 1.4 m.
	for i := 0; i <= 200; i++ {
		trip.add(31.2304+float64(i)*1.4*degPerM, 121.4737, 4, base.Add(time.Duration(i)*time.Second))
	}
	// The tail of the walk sits unclaimed in the current anchor, so the total
	// trails the truth by less than one noise floor.
	if trip.meters < 280-gpsTripMinStepM || trip.meters > 280 {
		t.Errorf("280 m walk logged as %.1f m, want within %.0f m below 280",
			trip.meters, gpsTripMinStepM)
	}
}

// Two ways to get a hop that is not a journey: the daemon stops sampling (fix
// lost, modem off, device asleep) and picks up somewhere else, or the receiver
// glitches and reports a position on the far side of town. Neither is distance
// travelled, but both leave the meter tracking from the new position.
func TestGpsTripSkipsGapsAndGlitches(t *testing.T) {
	base := time.Unix(1700000000, 0)
	const degPerM = 1.0 / 111320.0

	t.Run("gap", func(t *testing.T) {
		var trip gpsTripMeter
		trip.add(31.2304, 121.4737, 4, base)
		trip.add(31.2304+5000*degPerM, 121.4737, 4, base.Add(gpsTripMaxGap+time.Second))
		if trip.meters != 0 {
			t.Errorf("5 km across a %v gap logged %.1f m", gpsTripMaxGap, trip.meters)
		}
		// ...but the new position is the anchor now, so real motion after the
		// gap counts from there.
		trip.add(31.2304+5100*degPerM, 121.4737, 4, base.Add(gpsTripMaxGap+11*time.Second))
		if trip.meters < 95 || trip.meters > 105 {
			t.Errorf("100 m after the gap logged %.1f m", trip.meters)
		}
	})

	t.Run("glitch", func(t *testing.T) {
		var trip gpsTripMeter
		trip.add(31.2304, 121.4737, 4, base)
		// 2 km in one second is 7200 km/h.
		trip.add(31.2304+2000*degPerM, 121.4737, 4, base.Add(time.Second))
		if trip.meters != 0 {
			t.Errorf("teleport logged %.1f m", trip.meters)
		}
	})

	t.Run("clock stepping backwards", func(t *testing.T) {
		var trip gpsTripMeter
		trip.add(31.2304, 121.4737, 4, base)
		trip.add(31.2304+100*degPerM, 121.4737, 4, base.Add(-time.Hour))
		if trip.meters != 0 {
			t.Errorf("hop across a backwards clock step logged %.1f m", trip.meters)
		}
	})
}

// The readout is 172px wide and shares its row with an icon and a unit, so the
// decimal is dropped once the number is long enough not to need it.
func TestGpsTripDisplay(t *testing.T) {
	tests := []struct {
		meters float64
		want   string
	}{
		// Before any fix, and before moving: the honest answer is zero, not a
		// placeholder — the number is a session counter, not a reading.
		{0, "0.0"},
		{42, "0.0"},
		{940, "0.9"},
		{42_730, "42.7"},
		{99_949, "99.9"},
		{128_400, "128"},
		{1_284_000, "1284"},
	}
	for _, tt := range tests {
		trip := gpsTripMeter{meters: tt.meters}
		if got := trip.display(); got != tt.want {
			t.Errorf("%.0f m -> %q, want %q", tt.meters, got, tt.want)
		}
	}
}

func TestHaversineMeters(t *testing.T) {
	tests := []struct {
		name                   string
		lat1, lon1, lat2, lon2 float64
		want, tol              float64
	}{
		{"same point", 31.2304, 121.4737, 31.2304, 121.4737, 0, 0.001},
		{"one degree of latitude", 0, 0, 1, 0, 111195, 200},
		{"one degree of longitude at the equator", 0, 0, 0, 1, 111195, 200},
		// Longitude lines converge: at 60°N a degree is half as wide.
		{"one degree of longitude at 60N", 60, 0, 60, 1, 55597, 200},
		{"across the antimeridian", 0, 179.99, 0, -179.99, 2224, 20},
		{"Shanghai to Beijing", 31.2304, 121.4737, 39.9042, 116.4074, 1067000, 5000},
	}
	for _, tt := range tests {
		got := haversineMeters(tt.lat1, tt.lon1, tt.lat2, tt.lon2)
		if math.Abs(got-tt.want) > tt.tol {
			t.Errorf("%s: %.0f m, want %.0f ±%.0f", tt.name, got, tt.want, tt.tol)
		}
	}
}

// Garbage coordinates (a modem reporting 0/0 as NaN, or a parse producing an
// out-of-range value) must not become distance, and must not poison the anchor
// for the fixes that follow.
func TestGpsTripRejectsImpossibleCoordinates(t *testing.T) {
	var trip gpsTripMeter
	base := time.Unix(1700000000, 0)
	const degPerM = 1.0 / 111320.0

	trip.add(31.2304, 121.4737, 4, base)
	trip.add(math.NaN(), 121.4737, 4, base.Add(time.Second))
	trip.add(91.5, 121.4737, 4, base.Add(2*time.Second))
	trip.add(31.2304, 999, 4, base.Add(3*time.Second))
	if trip.meters != 0 {
		t.Errorf("impossible coordinates logged %.1f m", trip.meters)
	}
	trip.add(31.2304+100*degPerM, 121.4737, 4, base.Add(10*time.Second))
	if trip.meters < 95 || trip.meters > 105 {
		t.Errorf("100 m after the garbage logged %.1f m, want the anchor intact", trip.meters)
	}
}
