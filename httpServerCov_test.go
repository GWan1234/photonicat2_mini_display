package main

// Coverage tests for httpServer.go. Every handler runs on an isolated Fiber
// app via app.Test (no real ports except TestHTTPCovHTTPServerBindsAndServes,
// which uses an ephemeral 127.0.0.1 listener). All filesystem writes go
// through the httpCov* path seams into t.TempDir(), and every mutated global
// is restored via t.Cleanup so the suite stays order-independent under
// -shuffle and -race.
//
// All identifiers are prefixed HTTPCov/httpCov to avoid collisions with tests
// added concurrently in other worktrees.

import (
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// httpCovGet runs handler as GET /x with the given query ("" or "?k=v").
func httpCovGet(t *testing.T, handler fiber.Handler, query string) *http.Response {
	t.Helper()
	app := fiber.New()
	app.Get("/x", handler)
	resp, err := app.Test(httptest.NewRequest("GET", "/x"+query, nil), -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

// httpCovPostJSON runs handler as POST /x with a JSON body.
func httpCovPostJSON(t *testing.T, handler fiber.Handler, body string) *http.Response {
	t.Helper()
	app := fiber.New()
	app.Post("/x", handler)
	req := httptest.NewRequest("POST", "/x", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func httpCovReadBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// httpCovCloneConfig deep-copies a Config via a JSON round trip so restored
// snapshots never share slice/map backing storage with the live value.
func httpCovCloneConfig(t *testing.T, c Config) Config {
	t.Helper()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	var out Config
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return out
}

// httpCovStashConfigGlobals snapshots every config-related global the handlers
// under test mutate and restores them when the test finishes.
func httpCovStashConfigGlobals(t *testing.T) {
	t.Helper()
	configMutex.Lock()
	savedCfg := httpCovCloneConfig(t, cfg)
	savedDft := httpCovCloneConfig(t, dftCfg)
	savedUser := httpCovCloneConfig(t, userCfg)
	var savedOverrides map[string]interface{}
	if userOverrides != nil {
		savedOverrides = deepCopy(userOverrides)
	}
	configMutex.Unlock()
	savedJSON := userJsonConfig
	savedLocalExists := localConfigExists
	savedUserPath := httpCovUserConfigPath
	savedWebPath := httpCovWebUserConfigPath
	t.Cleanup(func() {
		configMutex.Lock()
		cfg = savedCfg
		dftCfg = savedDft
		userCfg = savedUser
		userOverrides = savedOverrides
		configMutex.Unlock()
		userJsonConfig = savedJSON
		localConfigExists = savedLocalExists
		httpCovUserConfigPath = savedUserPath
		httpCovWebUserConfigPath = savedWebPath
	})
}

// httpCovStashWebFrameState snapshots the web preview snapshot machinery.
func httpCovStashWebFrameState(t *testing.T) {
	t.Helper()
	webSnapMu.Lock()
	savedSnap, savedCompose := webSnapshot, webSnapCompose
	webSnapMu.Unlock()
	webFrameDemandMu.Lock()
	savedUntil := webFrameDemandUntil
	webFrameDemandMu.Unlock()
	savedTop, savedMid, savedFoot := webLastTop, webLastMiddle, webLastFooter
	t.Cleanup(func() {
		webSnapMu.Lock()
		webSnapshot, webSnapCompose = savedSnap, savedCompose
		webSnapMu.Unlock()
		webFrameDemandMu.Lock()
		webFrameDemandUntil = savedUntil
		webFrameDemandMu.Unlock()
		webLastTop, webLastMiddle, webLastFooter = savedTop, savedMid, savedFoot
	})
}

// httpCovOpaqueImage returns a w×h image filled with an opaque color, so the
// fast (bulk-copy) path in copyImageToImageAt applies.
func httpCovOpaqueImage(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// httpCovSwapMetricsMgr installs mgr as the global custom metrics manager for
// the duration of the test.
func httpCovSwapMetricsMgr(t *testing.T, mgr *CustomMetricManager) {
	t.Helper()
	saved := customMetricsMgr
	customMetricsMgr = mgr
	t.Cleanup(func() { customMetricsMgr = saved })
}

func httpCovNewManager(t *testing.T, sources ...SourceConfig) *CustomMetricManager {
	t.Helper()
	mgr, err := NewCustomMetricManager(CustomMetricsConfig{Sources: sources})
	if err != nil {
		t.Fatalf("NewCustomMetricManager: %v", err)
	}
	return mgr
}

// ---------------------------------------------------------------------------
// web frame snapshot plumbing
// ---------------------------------------------------------------------------

func TestHTTPCovWebFrameDemand(t *testing.T) {
	httpCovStashWebFrameState(t)

	webFrameDemandMu.Lock()
	webFrameDemandUntil = time.Now().Add(-time.Second)
	webFrameDemandMu.Unlock()
	if webFrameDemanded() {
		t.Error("webFrameDemanded() = true with an expired demand window")
	}

	noteWebFrameDemand()
	if !webFrameDemanded() {
		t.Error("webFrameDemanded() = false right after noteWebFrameDemand()")
	}
}

func TestHTTPCovRememberWebRegion(t *testing.T) {
	httpCovStashWebFrameState(t)

	top := image.NewRGBA(image.Rect(0, 0, 1, 1))
	mid := image.NewRGBA(image.Rect(0, 0, 1, 1))
	foot := image.NewRGBA(image.Rect(0, 0, 1, 1))

	webLastTop, webLastMiddle, webLastFooter = nil, nil, nil
	rememberWebRegion("top", nil) // nil frames must be ignored
	if webLastTop != nil {
		t.Error("rememberWebRegion stored a nil frame")
	}

	rememberWebRegion("top", top)
	rememberWebRegion("middle", mid)
	rememberWebRegion("footer", foot)
	rememberWebRegion("bogus", top) // unknown kinds are ignored
	if webLastTop != top || webLastMiddle != mid || webLastFooter != foot {
		t.Error("rememberWebRegion did not store the region pointers")
	}
}

func TestHTTPCovTryPublishWebSnapshot(t *testing.T) {
	httpCovStashWebFrameState(t)

	midHeight := PCAT2_LCD_HEIGHT - PCAT2_TOP_BAR_HEIGHT - PCAT2_FOOTER_HEIGHT
	red := color.RGBA{255, 0, 0, 255}
	green := color.RGBA{0, 255, 0, 255}
	blue := color.RGBA{0, 0, 255, 255}

	t.Run("skips_when_idle_and_seeded", func(t *testing.T) {
		existing := image.NewRGBA(image.Rect(0, 0, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT))
		webSnapMu.Lock()
		webSnapshot = existing
		webSnapMu.Unlock()
		webFrameDemandMu.Lock()
		webFrameDemandUntil = time.Now().Add(-time.Second)
		webFrameDemandMu.Unlock()
		webLastTop, webLastMiddle, webLastFooter = nil, nil, nil

		tryPublishWebSnapshot() // must be a no-op

		webSnapMu.RLock()
		same := webSnapshot == existing
		webSnapMu.RUnlock()
		if !same {
			t.Error("tryPublishWebSnapshot replaced the snapshot while idle")
		}
	})

	t.Run("waits_for_all_regions", func(t *testing.T) {
		webSnapMu.Lock()
		webSnapshot, webSnapCompose = nil, nil
		webSnapMu.Unlock()
		webLastTop = httpCovOpaqueImage(PCAT2_LCD_WIDTH, PCAT2_TOP_BAR_HEIGHT, red)
		webLastMiddle, webLastFooter = nil, nil
		noteWebFrameDemand()

		tryPublishWebSnapshot()

		webSnapMu.RLock()
		snap := webSnapshot
		webSnapMu.RUnlock()
		if snap != nil {
			t.Error("snapshot published before all three regions were drawn")
		}
	})

	t.Run("composes_and_publishes", func(t *testing.T) {
		webSnapMu.Lock()
		webSnapshot, webSnapCompose = nil, nil
		webSnapMu.Unlock()
		webLastTop = httpCovOpaqueImage(PCAT2_LCD_WIDTH, PCAT2_TOP_BAR_HEIGHT, red)
		webLastMiddle = httpCovOpaqueImage(PCAT2_LCD_WIDTH, midHeight, green)
		webLastFooter = httpCovOpaqueImage(PCAT2_LCD_WIDTH, PCAT2_FOOTER_HEIGHT, blue)
		noteWebFrameDemand()

		tryPublishWebSnapshot()

		webSnapMu.RLock()
		snap := webSnapshot
		webSnapMu.RUnlock()
		if snap == nil {
			t.Fatal("no snapshot published despite all regions being available")
		}
		if got := snap.RGBAAt(5, 5); got != red {
			t.Errorf("top region pixel = %v, want %v", got, red)
		}
		if got := snap.RGBAAt(5, PCAT2_TOP_BAR_HEIGHT+5); got != green {
			t.Errorf("middle region pixel = %v, want %v", got, green)
		}
		if got := snap.RGBAAt(5, PCAT2_LCD_HEIGHT-5); got != blue {
			t.Errorf("footer region pixel = %v, want %v", got, blue)
		}

		// Second publish reuses both the compose scratch and snapshot buffers.
		tryPublishWebSnapshot()
		webSnapMu.RLock()
		same := webSnapshot == snap
		webSnapMu.RUnlock()
		if !same {
			t.Error("republish allocated a new snapshot instead of reusing the buffer")
		}
	})
}

func TestHTTPCovServeFrame(t *testing.T) {
	httpCovStashWebFrameState(t)

	t.Run("serves_png_when_snapshot_ready", func(t *testing.T) {
		webSnapMu.Lock()
		webSnapshot = httpCovOpaqueImage(PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT, color.RGBA{9, 8, 7, 255})
		webSnapMu.Unlock()

		resp := httpCovGet(t, serveFrame, "")
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
			t.Errorf("Content-Type = %q, want image/png", ct)
		}
		img, err := png.Decode(resp.Body)
		if err != nil {
			t.Fatalf("body is not a decodable PNG: %v", err)
		}
		if img.Bounds().Dx() != PCAT2_LCD_WIDTH || img.Bounds().Dy() != PCAT2_LCD_HEIGHT {
			t.Errorf("frame size = %v, want %dx%d", img.Bounds(), PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT)
		}
	})

	t.Run("503_when_no_frame_yet", func(t *testing.T) {
		webSnapMu.Lock()
		webSnapshot = nil
		webSnapMu.Unlock()

		resp := httpCovGet(t, serveFrame, "") // waits its 500ms deadline
		if resp.StatusCode != fiber.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// simple handlers
// ---------------------------------------------------------------------------

func TestHTTPCovIndexHandler(t *testing.T) {
	resp := httpCovGet(t, indexHandler, "")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body := httpCovReadBody(t, resp); !strings.Contains(body, "<") {
		t.Errorf("index body does not look like HTML: %.60q", body)
	}
}

func TestHTTPCovChangePage(t *testing.T) {
	lastActivityMu.Lock()
	savedTriggered, savedActivity := httpChangePageTriggered, lastActivity
	lastActivityMu.Unlock()
	savedSwipping := swippingScreen
	t.Cleanup(func() {
		lastActivityMu.Lock()
		httpChangePageTriggered, lastActivity = savedTriggered, savedActivity
		lastActivityMu.Unlock()
		swippingScreen = savedSwipping
		select { // drain any page-change token this test enqueued
		case <-pageChangeSignal:
		default:
		}
	})

	lastActivityMu.Lock()
	httpChangePageTriggered = false
	lastActivityMu.Unlock()

	resp := httpCovGet(t, changePage, "")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	lastActivityMu.Lock()
	triggered := httpChangePageTriggered
	lastActivityMu.Unlock()
	if !triggered {
		t.Error("changePage did not set httpChangePageTriggered")
	}
	if !swippingScreen {
		t.Error("changePage did not set swippingScreen")
	}
}

func TestHTTPCovGetDataAndUpdateData(t *testing.T) {
	const key = "HTTPCovUD"
	t.Cleanup(func() { globalData.Delete(key) })

	t.Run("update_rejects_bad_json", func(t *testing.T) {
		resp := httpCovPostJSON(t, updateData, `{"broken":`)
		if resp.StatusCode != 400 {
			t.Errorf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("update_then_get_round_trips", func(t *testing.T) {
		resp := httpCovPostJSON(t, updateData, `{"`+key+`":"42"}`)
		if resp.StatusCode != 200 {
			t.Fatalf("update status = %d, want 200", resp.StatusCode)
		}
		if v, ok := globalData.Load(key); !ok || v != "42" {
			t.Fatalf("globalData[%s] = %v (ok=%v), want \"42\"", key, v, ok)
		}

		resp = httpCovGet(t, getData, "")
		if resp.StatusCode != 200 {
			t.Fatalf("get status = %d, want 200", resp.StatusCode)
		}
		var out map[string]interface{}
		if err := json.Unmarshal([]byte(httpCovReadBody(t, resp)), &out); err != nil {
			t.Fatalf("getData response not JSON: %v", err)
		}
		if out[key] != "42" {
			t.Errorf("getData[%s] = %v, want \"42\"", key, out[key])
		}
	})
}

func TestHTTPCovConfigGetters(t *testing.T) {
	for name, h := range map[string]fiber.Handler{
		"getDefaultConfig": getDefaultConfig,
		"getConfig":        getConfig,
		"getStatus":        getStatus,
	} {
		resp := httpCovGet(t, h, "")
		if resp.StatusCode != 200 {
			t.Errorf("%s: status = %d, want 200", name, resp.StatusCode)
			continue
		}
		var parsed interface{}
		if err := json.Unmarshal([]byte(httpCovReadBody(t, resp)), &parsed); err != nil {
			t.Errorf("%s: response not JSON: %v", name, err)
		}
	}
}

func TestHTTPCovGetUserConfig(t *testing.T) {
	httpCovStashConfigGlobals(t)
	dir := t.TempDir()

	t.Run("returns_file_contents", func(t *testing.T) {
		path := filepath.Join(dir, "user.json")
		if err := os.WriteFile(path, []byte(`{"a":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
		httpCovUserConfigPath = path

		resp := httpCovGet(t, getUserConfig, "")
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if body := httpCovReadBody(t, resp); body != `{"a":1}` {
			t.Errorf("body = %q, want the raw file contents", body)
		}
	})

	t.Run("500_when_unreadable", func(t *testing.T) {
		httpCovUserConfigPath = filepath.Join(dir, "does-not-exist.json")
		resp := httpCovGet(t, getUserConfig, "")
		if resp.StatusCode != 500 {
			t.Errorf("status = %d, want 500", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// user config load/save plumbing
// ---------------------------------------------------------------------------

func TestHTTPCovLoadUserConfig(t *testing.T) {
	httpCovStashConfigGlobals(t)
	dir := t.TempDir()
	const key = "HTTPCovLK"
	t.Cleanup(func() { globalData.Delete(key) })

	t.Run("returns_cached_value", func(t *testing.T) {
		userJsonConfig = `{"cached":"yes"}`
		if got := loadUserConfig(); got != `{"cached":"yes"}` {
			t.Errorf("loadUserConfig() = %q, want the cached string", got)
		}
	})

	t.Run("missing_file_starts_fresh", func(t *testing.T) {
		userJsonConfig = ""
		httpCovUserConfigPath = filepath.Join(dir, "missing.json")
		if got := loadUserConfig(); got != "{}" {
			t.Errorf("loadUserConfig() = %q, want \"{}\"", got)
		}
	})

	t.Run("invalid_json_starts_fresh", func(t *testing.T) {
		path := filepath.Join(dir, "bad.json")
		if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		userJsonConfig = ""
		httpCovUserConfigPath = path
		if got := loadUserConfig(); got != "{}" {
			t.Errorf("loadUserConfig() = %q, want \"{}\"", got)
		}
	})

	t.Run("valid_file_loads_into_globalData", func(t *testing.T) {
		raw := `{"` + key + `":"v1"}`
		path := filepath.Join(dir, "good.json")
		if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		userJsonConfig = ""
		httpCovUserConfigPath = path
		if got := loadUserConfig(); got != raw {
			t.Errorf("loadUserConfig() = %q, want %q", got, raw)
		}
		if v, ok := globalData.Load(key); !ok || v != "v1" {
			t.Errorf("globalData[%s] = %v (ok=%v), want \"v1\"", key, v, ok)
		}
	})
}

func TestHTTPCovSaveUserConfigToFile(t *testing.T) {
	httpCovStashConfigGlobals(t)
	dir := t.TempDir()

	t.Run("success_writes_valid_json", func(t *testing.T) {
		httpCovUserConfigPath = filepath.Join(dir, "sub", "user.json")
		if !saveUserConfigToFile() {
			t.Fatal("saveUserConfigToFile() = false on a writable temp dir")
		}
		raw, err := os.ReadFile(httpCovUserConfigPath)
		if err != nil {
			t.Fatalf("config file not written: %v", err)
		}
		var c Config
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Errorf("written config is not valid JSON: %v", err)
		}
	})

	t.Run("mkdir_failure", func(t *testing.T) {
		blocker := filepath.Join(dir, "blocker")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Parent "directory" is a regular file, so MkdirAll must fail.
		httpCovUserConfigPath = filepath.Join(blocker, "nested", "user.json")
		if saveUserConfigToFile() {
			t.Error("saveUserConfigToFile() = true with an uncreatable directory")
		}
	})

	t.Run("write_failure", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root: permissions cannot force a write failure")
		}
		ro := filepath.Join(dir, "ro")
		if err := os.Mkdir(ro, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(ro, 0o755) })
		httpCovUserConfigPath = filepath.Join(ro, "user.json")
		if saveUserConfigToFile() {
			t.Error("saveUserConfigToFile() = true writing into a read-only dir")
		}
	})

	t.Run("rename_failure", func(t *testing.T) {
		// Target path exists as a non-empty directory: the temp file writes
		// fine, but the final rename over it must fail.
		asDir := filepath.Join(dir, "asdir", "user.json")
		if err := os.MkdirAll(filepath.Join(asDir, "occupied"), 0o755); err != nil {
			t.Fatal(err)
		}
		httpCovUserConfigPath = asDir
		if saveUserConfigToFile() {
			t.Error("saveUserConfigToFile() = true when rename target is a directory")
		}
	})
}

func TestHTTPCovSaveUserConfigFromStrPaths(t *testing.T) {
	httpCovStashConfigGlobals(t)
	dir := t.TempDir()

	t.Run("mkdir_failure", func(t *testing.T) {
		blocker := filepath.Join(dir, "blocker")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		httpCovUserConfigPath = filepath.Join(blocker, "nested", "user.json")
		if saveUserConfigFromStr(`{"k":"v"}`) {
			t.Error("saveUserConfigFromStr() = true with an uncreatable directory")
		}
	})

	t.Run("write_failure", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root: permissions cannot force a write failure")
		}
		ro := filepath.Join(dir, "ro")
		if err := os.Mkdir(ro, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(ro, 0o755) })
		httpCovUserConfigPath = filepath.Join(ro, "user.json")
		if saveUserConfigFromStr(`{"k":"v"}`) {
			t.Error("saveUserConfigFromStr() = true writing into a read-only dir")
		}
	})

	t.Run("rename_failure", func(t *testing.T) {
		asDir := filepath.Join(dir, "asdir", "user.json")
		if err := os.MkdirAll(filepath.Join(asDir, "occupied"), 0o755); err != nil {
			t.Fatal(err)
		}
		httpCovUserConfigPath = asDir
		if saveUserConfigFromStr(`{"k":"v"}`) {
			t.Error("saveUserConfigFromStr() = true when rename target is a directory")
		}
	})

	t.Run("success_saves_and_remerges", func(t *testing.T) {
		httpCovUserConfigPath = filepath.Join(dir, "ok", "user.json")
		if !saveUserConfigFromStr(`{"httpcov_marker":"1"}`) {
			t.Fatal("saveUserConfigFromStr() = false on a writable temp dir")
		}
		raw, err := os.ReadFile(httpCovUserConfigPath)
		if err != nil {
			t.Fatalf("config file not written: %v", err)
		}
		if !strings.Contains(string(raw), "httpcov_marker") {
			t.Errorf("written config %q does not contain the saved key", raw)
		}
	})
}

func TestHTTPCovSaveUserConfigFromWeb(t *testing.T) {
	httpCovStashConfigGlobals(t)
	dir := t.TempDir()
	httpCovUserConfigPath = filepath.Join(dir, "user.json")

	t.Run("rejects_invalid_json", func(t *testing.T) {
		resp := httpCovPostJSON(t, saveUserConfigFromWeb, `{"broken":`)
		if resp.StatusCode != 400 {
			t.Errorf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("saves_valid_json", func(t *testing.T) {
		resp := httpCovPostJSON(t, saveUserConfigFromWeb, `{"httpcov_web":"1"}`)
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		raw, err := os.ReadFile(httpCovUserConfigPath)
		if err != nil {
			t.Fatalf("config file not written: %v", err)
		}
		if !strings.Contains(string(raw), "httpcov_web") {
			t.Errorf("written config %q does not contain the saved key", raw)
		}
	})
}

func TestHTTPCovSetUserConfig(t *testing.T) {
	httpCovStashConfigGlobals(t)
	dir := t.TempDir()

	t.Run("rejects_invalid_json", func(t *testing.T) {
		resp := httpCovPostJSON(t, setUserConfig, `{"broken":`)
		if resp.StatusCode != 400 {
			t.Errorf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("500_when_write_fails", func(t *testing.T) {
		httpCovWebUserConfigPath = filepath.Join(dir, "no-such-dir", "user.json")
		resp := httpCovPostJSON(t, setUserConfig, `{"httpcov_set":"x"}`)
		if resp.StatusCode != 500 {
			t.Errorf("status = %d, want 500", resp.StatusCode)
		}
	})

	t.Run("merges_and_persists", func(t *testing.T) {
		cfgDir := filepath.Join(dir, "config")
		if err := os.Mkdir(cfgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		httpCovWebUserConfigPath = filepath.Join(cfgDir, "user_config.json")

		resp := httpCovPostJSON(t, setUserConfig, `{"httpcov_set":"y"}`)
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		raw, err := os.ReadFile(httpCovWebUserConfigPath)
		if err != nil {
			t.Fatalf("overrides file not written: %v", err)
		}
		if !strings.Contains(string(raw), "httpcov_set") {
			t.Errorf("persisted overrides %q missing the new key", raw)
		}
		configMutex.RLock()
		v := userOverrides["httpcov_set"]
		configMutex.RUnlock()
		if v != "y" {
			t.Errorf("userOverrides[httpcov_set] = %v, want \"y\"", v)
		}
	})
}

// setConfig holds configMutex and then calls mergeConfigs(), which takes the
// same (non-reentrant) lock — its happy path self-deadlocks and the handler is
// not routed by httpServer(). Only the pre-lock validation branch is testable.
func TestHTTPCovSetConfigRejectsBadJSON(t *testing.T) {
	resp := httpCovPostJSON(t, setConfig, `{"broken":`)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHTTPCovResetConfig(t *testing.T) {
	httpCovStashConfigGlobals(t)
	dir := t.TempDir()
	httpCovUserConfigPath = filepath.Join(dir, "user.json")

	configMutex.Lock()
	userCfg.PingSite0 = "should.be.cleared"
	configMutex.Unlock()

	resp := httpCovGet(t, resetConfig, "")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	configMutex.RLock()
	cleared := userCfg.PingSite0 == ""
	configMutex.RUnlock()
	if !cleared {
		t.Error("resetConfig did not clear the user overrides")
	}
	if _, err := os.Stat(httpCovUserConfigPath); err != nil {
		t.Errorf("resetConfig did not persist the cleared config: %v", err)
	}
}

// ---------------------------------------------------------------------------
// power off
// ---------------------------------------------------------------------------

func TestHTTPCovRequestPowerOff(t *testing.T) {
	saved := httpCovPmuPowerOff
	called := make(chan struct{}, 4)
	// The stub also returns an error so the goroutine's failure log branch
	// runs. Nothing here ever shells out to poweroff.
	httpCovPmuPowerOff = func() error {
		called <- struct{}{}
		return errors.New("httpCov stub: not powering off")
	}
	t.Cleanup(func() { httpCovPmuPowerOff = saved })

	waitCalled := func(t *testing.T) {
		t.Helper()
		select {
		case <-called:
		case <-time.After(2 * time.Second):
			t.Fatal("power-off stub was never invoked")
		}
	}

	t.Run("refuses_without_confirm", func(t *testing.T) {
		resp := postForm(t, requestPowerOff, "")
		if resp.StatusCode != 400 {
			t.Errorf("status = %d, want 400", resp.StatusCode)
		}
		select {
		case <-called:
			t.Fatal("unconfirmed request still triggered power off")
		default:
		}
	})

	t.Run("confirm_via_query", func(t *testing.T) {
		app := fiber.New()
		app.Post("/x", requestPowerOff)
		resp, err := app.Test(httptest.NewRequest("POST", "/x?confirm=yes", nil), -1)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		waitCalled(t)
	})

	t.Run("confirm_via_form", func(t *testing.T) {
		resp := postForm(t, requestPowerOff, "confirm=yes")
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		waitCalled(t)
	})
}

// ---------------------------------------------------------------------------
// drawing endpoints
// ---------------------------------------------------------------------------

func TestHTTPCovHTTPDrawText(t *testing.T) {
	// httpDrawText log.Fatalf's if fonts are missing, so install the real
	// table rooted at the repo's assets dir (same trick as gpsPreviewFonts).
	savedFonts := fonts
	fonts = buildFontTable(".")
	savedRun := runMainLoop
	savedWrapper := displayWrapper
	displayWrapper = nil // zero gc9307 device rejects the blit harmlessly
	t.Cleanup(func() {
		fonts = savedFonts
		runMainLoop = savedRun
		displayWrapper = savedWrapper
	})

	t.Run("draws_provided_text", func(t *testing.T) {
		resp := httpCovGet(t, httpDrawText, "?text=hello")
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var body map[string]interface{}
		if err := json.Unmarshal([]byte(httpCovReadBody(t, resp)), &body); err != nil {
			t.Fatalf("response not JSON: %v", err)
		}
		if body["text"] != "hello" {
			t.Errorf("text = %v, want \"hello\"", body["text"])
		}
		if runMainLoop {
			t.Error("httpDrawText should pause the main loop")
		}
	})

	t.Run("draws_pattern_without_text", func(t *testing.T) {
		resp := httpCovGet(t, httpDrawText, "")
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var body map[string]interface{}
		if err := json.Unmarshal([]byte(httpCovReadBody(t, resp)), &body); err != nil {
			t.Fatalf("response not JSON: %v", err)
		}
		if body["text"] != "" {
			t.Errorf("text = %v, want \"\"", body["text"])
		}
	})
}

func TestHTTPCovMakeItRun(t *testing.T) {
	savedRun := runMainLoop
	savedWeAreRunning := weAreRunning()
	t.Cleanup(func() {
		runMainLoop = savedRun
		setWeAreRunning(savedWeAreRunning)
	})

	runMainLoop = false
	setWeAreRunning(false)

	resp := httpCovGet(t, makeItRun, "")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !runMainLoop {
		t.Error("makeItRun did not restart the main loop")
	}
	if !weAreRunning() {
		t.Error("makeItRun did not clear the shutdown flag")
	}
}

// ---------------------------------------------------------------------------
// backlight
// ---------------------------------------------------------------------------

func TestHTTPCovResetMaxBacklightAndEffectiveBrightness(t *testing.T) {
	runtimeBrightnessMu.Lock()
	savedOverride := runtimeMaxBrightness
	runtimeBrightnessMu.Unlock()
	configMutex.Lock()
	savedCfgVal := cfg.ScreenMaxBrightness
	cfg.ScreenMaxBrightness = 66
	configMutex.Unlock()
	t.Cleanup(func() {
		runtimeBrightnessMu.Lock()
		runtimeMaxBrightness = savedOverride
		runtimeBrightnessMu.Unlock()
		configMutex.Lock()
		cfg.ScreenMaxBrightness = savedCfgVal
		configMutex.Unlock()
	})

	override := 42
	runtimeBrightnessMu.Lock()
	runtimeMaxBrightness = &override
	runtimeBrightnessMu.Unlock()
	if got := getEffectiveMaxBrightness(); got != 42 {
		t.Errorf("getEffectiveMaxBrightness() = %d, want the 42 override", got)
	}

	resp, body := getJSON(t, resetMaxBacklight)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body["source"] != "config" {
		t.Errorf("source = %v, want \"config\"", body["source"])
	}
	runtimeBrightnessMu.RLock()
	overrideCleared := runtimeMaxBrightness == nil
	runtimeBrightnessMu.RUnlock()
	if !overrideCleared {
		t.Error("resetMaxBacklight did not clear the runtime override")
	}
	if got := getEffectiveMaxBrightness(); got != 66 {
		t.Errorf("getEffectiveMaxBrightness() = %d, want the config value 66", got)
	}
}

// ---------------------------------------------------------------------------
// small pure helpers
// ---------------------------------------------------------------------------

// The existing table covers the primary colors; this sweeps every sextant of
// the hue wheel so all six switch cases run.
func TestHTTPCovHsvToRgbAllSextants(t *testing.T) {
	want := map[float64][3]float64{
		1.0 / 12: {1, 0.5, 0}, // orange, case 0
		3.0 / 12: {0.5, 1, 0}, // chartreuse, case 1
		5.0 / 12: {0, 1, 0.5}, // spring green, case 2
		7.0 / 12: {0, 0.5, 1}, // azure, case 3
		9.0 / 12: {0.5, 0, 1}, // violet, case 4
		11. / 12: {1, 0, 0.5}, // rose, case 5
	}
	for h, exp := range want {
		r, g, b := hsvToRgb(h, 1, 1)
		if abs64(r-exp[0]) > 0.01 || abs64(g-exp[1]) > 0.01 || abs64(b-exp[2]) > 0.01 {
			t.Errorf("hsvToRgb(%.3f,1,1) = (%.2f,%.2f,%.2f), want (%v)", h, r, g, b, exp)
		}
	}
}

// Covers the deepMerge branch where the destination holds a primitive and the
// source replaces it with a nested map.
func TestHTTPCovDeepMergePrimitiveBecomesMap(t *testing.T) {
	dest := map[string]interface{}{"k": "primitive"}
	src := map[string]interface{}{"k": map[string]interface{}{"a": 1}}
	out := deepMerge(dest, src)
	m, ok := out["k"].(map[string]interface{})
	if !ok {
		t.Fatalf("out[k] = %v, want a nested map", out["k"])
	}
	if m["a"] != 1 {
		t.Errorf("out[k][a] = %v, want 1", m["a"])
	}
}

// ---------------------------------------------------------------------------
// custom metrics endpoints
// ---------------------------------------------------------------------------

func TestHTTPCovGetCustomMetricsStatus(t *testing.T) {
	t.Run("404_when_uninitialized", func(t *testing.T) {
		httpCovSwapMetricsMgr(t, nil)
		resp := httpCovGet(t, getCustomMetricsStatus, "")
		if resp.StatusCode != 404 {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("lists_sources", func(t *testing.T) {
		httpCovSwapMetricsMgr(t, httpCovNewManager(t, SourceConfig{
			Type: "http_endpoint", Name: "httpcov-http", Enabled: 1,
			Config: map[string]interface{}{},
		}))
		resp := httpCovGet(t, getCustomMetricsStatus, "")
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var body map[string]interface{}
		if err := json.Unmarshal([]byte(httpCovReadBody(t, resp)), &body); err != nil {
			t.Fatalf("response not JSON: %v", err)
		}
		if body["count"] != float64(1) {
			t.Errorf("count = %v, want 1", body["count"])
		}
	})
}

func TestHTTPCovUpdateCustomMetricsData(t *testing.T) {
	const httpKey = "HTTPCovCMHttp"
	const directKey = "HTTPCovCMDirect"
	t.Cleanup(func() {
		globalData.Delete(httpKey)
		globalData.Delete(directKey)
	})

	t.Run("404_when_uninitialized", func(t *testing.T) {
		httpCovSwapMetricsMgr(t, nil)
		resp := httpCovPostJSON(t, updateCustomMetricsData, `{"a":"b"}`)
		if resp.StatusCode != 404 {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("400_on_bad_json", func(t *testing.T) {
		httpCovSwapMetricsMgr(t, httpCovNewManager(t))
		resp := httpCovPostJSON(t, updateCustomMetricsData, `{"broken":`)
		if resp.StatusCode != 400 {
			t.Errorf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("routes_through_http_source", func(t *testing.T) {
		httpCovSwapMetricsMgr(t, httpCovNewManager(t, SourceConfig{
			Type: "http_endpoint", Name: "httpcov-http", Enabled: 1,
			Config: map[string]interface{}{},
		}))
		resp := httpCovPostJSON(t, updateCustomMetricsData, `{"`+httpKey+`":"7"}`)
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if v, ok := globalData.Load(httpKey); !ok || v != "7" {
			t.Errorf("globalData[%s] = %v (ok=%v), want \"7\"", httpKey, v, ok)
		}
	})

	t.Run("stores_directly_without_http_source", func(t *testing.T) {
		httpCovSwapMetricsMgr(t, httpCovNewManager(t, SourceConfig{
			Type: "env", Name: "httpcov-env", Enabled: 1,
			Config: map[string]interface{}{
				"refresh_interval": 30,
				"variables": []interface{}{
					map[string]interface{}{"env_var": "HOME", "data_key": "HTTPCovHome"},
				},
			},
		}))
		resp := httpCovPostJSON(t, updateCustomMetricsData, `{"`+directKey+`":"9"}`)
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if v, ok := globalData.Load(directKey); !ok || v != "9" {
			t.Errorf("globalData[%s] = %v (ok=%v), want \"9\"", directKey, v, ok)
		}
	})
}

func TestHTTPCovExecuteCustomMetricsSource(t *testing.T) {
	execURL := func(name string) *http.Request {
		return httptest.NewRequest("POST", "/x/"+name+"/execute", nil)
	}
	runExec := func(t *testing.T, req *http.Request) *http.Response {
		t.Helper()
		app := fiber.New()
		app.Post("/x/:source/execute", executeCustomMetricsSource)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		return resp
	}

	t.Run("404_when_uninitialized", func(t *testing.T) {
		httpCovSwapMetricsMgr(t, nil)
		if resp := runExec(t, execURL("any")); resp.StatusCode != 404 {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("400_when_name_missing", func(t *testing.T) {
		httpCovSwapMetricsMgr(t, httpCovNewManager(t))
		// A route without the :source param leaves c.Params("source") empty.
		app := fiber.New()
		app.Post("/exec", executeCustomMetricsSource)
		resp, err := app.Test(httptest.NewRequest("POST", "/exec", nil), -1)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		if resp.StatusCode != 400 {
			t.Errorf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("404_for_unknown_source", func(t *testing.T) {
		httpCovSwapMetricsMgr(t, httpCovNewManager(t))
		if resp := runExec(t, execURL("nope")); resp.StatusCode != 404 {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("400_for_unsupported_type", func(t *testing.T) {
		httpCovSwapMetricsMgr(t, httpCovNewManager(t, SourceConfig{
			Type: "env", Name: "httpcov-env", Enabled: 1,
			Config: map[string]interface{}{
				"refresh_interval": 30,
				"variables": []interface{}{
					map[string]interface{}{"env_var": "HOME", "data_key": "HTTPCovHome"},
				},
			},
		}))
		if resp := runExec(t, execURL("httpcov-env")); resp.StatusCode != 400 {
			t.Errorf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("triggers_command_source", func(t *testing.T) {
		const key = "HTTPCovExecCmd"
		t.Cleanup(func() { globalData.Delete(key) })
		httpCovSwapMetricsMgr(t, httpCovNewManager(t, SourceConfig{
			Type: "command", Name: "httpcov-cmd", Enabled: 1,
			Config: map[string]interface{}{
				"command": "echo httpcov-ran", "data_key": key,
				"interval": 60, "timeout": 5, "parser": "stdout",
			},
		}))
		resp := runExec(t, execURL("httpcov-cmd"))
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		// ExecuteNow runs async; wait for the echo to land so the goroutine
		// finishes inside this test rather than racing later ones.
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if v, ok := globalData.Load(key); ok && v == "httpcov-ran" {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		v, _ := globalData.Load(key)
		t.Fatalf("command source never stored its output (got %v)", v)
	})

	t.Run("triggers_http_poll_source", func(t *testing.T) {
		const key = "HTTPCovExecPoll"
		t.Cleanup(func() { globalData.Delete(key) })
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("31337"))
		}))
		defer srv.Close()
		httpCovSwapMetricsMgr(t, httpCovNewManager(t, SourceConfig{
			Type: "http_poll", Name: "httpcov-poll", Enabled: 1,
			Config: map[string]interface{}{
				"url": srv.URL, "data_key": key,
				"interval": 60, "timeout": 2, "parser": "stdout",
			},
		}))
		resp := runExec(t, execURL("httpcov-poll"))
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if v, ok := globalData.Load(key); ok && v == "31337" {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		v, _ := globalData.Load(key)
		t.Fatalf("http_poll source never stored the fetched value (got %v)", v)
	})
}

// ---------------------------------------------------------------------------
// httpServer bring-up
// ---------------------------------------------------------------------------

// httpServer never returns (it parks in app.Listener), so run it on an
// ephemeral loopback port in a goroutine and just verify it accepts a
// connection. The goroutine stays parked until the test process exits, which
// is harmless. The bind-retry loop is left uncovered: forcing it costs a 2s
// sleep per retry with no way to break out.
func TestHTTPCovHTTPServerBindsAndServes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	go httpServer(addr)

	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("httpServer never started accepting on %s: %v", addr, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
