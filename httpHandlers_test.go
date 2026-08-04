package main

// The HTTP API is what pcat-manager-web drives. Every handler here takes
// untrusted form input, so the tests focus on rejection: a bad value must come
// back as a 4xx with the config untouched, never be coerced into something
// that blanks the panel.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// postForm runs one handler on an isolated Fiber app and returns the response.
func postForm(t *testing.T, handler fiber.Handler, body string) *http.Response {
	t.Helper()
	app := fiber.New()
	app.Post("/x", handler)

	req := httptest.NewRequest("POST", "/x", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func getJSON(t *testing.T, handler fiber.Handler) (*http.Response, map[string]interface{}) {
	t.Helper()
	app := fiber.New()
	app.Get("/x", handler)

	resp, err := app.Test(httptest.NewRequest("GET", "/x", nil), -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)

	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("response was not JSON (%q): %v", string(body), err)
	}
	return resp, parsed
}

// setMaxBacklight guards the 0..100 range. Anything outside it, or
// non-numeric, must be refused — the value drives the backlight PWM directly.
func TestSetMaxBacklightRejectsBadInput(t *testing.T) {
	saved := runtimeMaxBrightness
	t.Cleanup(func() {
		runtimeBrightnessMu.Lock()
		runtimeMaxBrightness = saved
		runtimeBrightnessMu.Unlock()
	})

	tests := []struct {
		name string
		body string
	}{
		{"missing", ""},
		{"empty", "max_brightness="},
		{"not_a_number", "max_brightness=bright"},
		{"float", "max_brightness=50.5"},
		{"negative", "max_brightness=-1"},
		{"over_100", "max_brightness=101"},
		{"way_over", "max_brightness=99999"},
		{"injection_attempt", "max_brightness=50;rm+-rf+/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeBrightnessMu.Lock()
			runtimeMaxBrightness = nil
			runtimeBrightnessMu.Unlock()

			resp := postForm(t, setMaxBacklight, tt.body)
			if resp.StatusCode != 400 {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}

			runtimeBrightnessMu.RLock()
			got := runtimeMaxBrightness
			runtimeBrightnessMu.RUnlock()
			if got != nil {
				t.Errorf("a rejected request still set the override to %d", *got)
			}
		})
	}
}

func TestSetMaxBacklightAcceptsValidRange(t *testing.T) {
	saved := runtimeMaxBrightness
	t.Cleanup(func() {
		runtimeBrightnessMu.Lock()
		runtimeMaxBrightness = saved
		runtimeBrightnessMu.Unlock()
	})

	for _, want := range []int{0, 1, 50, 99, 100} {
		runtimeBrightnessMu.Lock()
		runtimeMaxBrightness = nil
		runtimeBrightnessMu.Unlock()

		resp := postForm(t, setMaxBacklight, "max_brightness="+itoa(want))
		if resp.StatusCode != 200 {
			t.Errorf("max_brightness=%d: status = %d, want 200", want, resp.StatusCode)
			continue
		}

		runtimeBrightnessMu.RLock()
		got := runtimeMaxBrightness
		runtimeBrightnessMu.RUnlock()
		if got == nil {
			t.Errorf("max_brightness=%d: override was not set", want)
		} else if *got != want {
			t.Errorf("override = %d, want %d", *got, want)
		}
	}
}

// getMaxBacklight must report which source the value came from, so the web UI
// can show whether a runtime override is in effect.
func TestGetMaxBacklightReportsSource(t *testing.T) {
	saved := runtimeMaxBrightness
	t.Cleanup(func() {
		runtimeBrightnessMu.Lock()
		runtimeMaxBrightness = saved
		runtimeBrightnessMu.Unlock()
	})

	t.Run("from_config", func(t *testing.T) {
		runtimeBrightnessMu.Lock()
		runtimeMaxBrightness = nil
		runtimeBrightnessMu.Unlock()

		configMutex.Lock()
		savedCfg := cfg.ScreenMaxBrightness
		cfg.ScreenMaxBrightness = 77
		configMutex.Unlock()
		defer func() {
			configMutex.Lock()
			cfg.ScreenMaxBrightness = savedCfg
			configMutex.Unlock()
		}()

		resp, body := getJSON(t, getMaxBacklight)
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if body["source"] != "config" {
			t.Errorf("source = %v, want \"config\"", body["source"])
		}
		if body["max_brightness"] != float64(77) {
			t.Errorf("max_brightness = %v, want 77", body["max_brightness"])
		}
	})

	t.Run("from_runtime_override", func(t *testing.T) {
		override := 33
		runtimeBrightnessMu.Lock()
		runtimeMaxBrightness = &override
		runtimeBrightnessMu.Unlock()

		_, body := getJSON(t, getMaxBacklight)
		if body["source"] != "runtime_override" {
			t.Errorf("source = %v, want \"runtime_override\"", body["source"])
		}
		if body["max_brightness"] != float64(33) {
			t.Errorf("max_brightness = %v, want 33", body["max_brightness"])
		}
	})
}

// setShowSMS only accepts the literal strings "true"/"false"; anything else
// (including 1/0 and "TRUE") is a client bug and must be surfaced as one.
func TestSetShowSMSValidation(t *testing.T) {
	configMutex.Lock()
	saved := userCfg.ShowSms
	configMutex.Unlock()
	t.Cleanup(func() {
		configMutex.Lock()
		userCfg.ShowSms = saved
		configMutex.Unlock()
	})

	t.Run("rejects_non_boolean", func(t *testing.T) {
		for _, body := range []string{
			"", "showSMS=", "showSMS=1", "showSMS=0", "showSMS=yes",
			"showSMS=TRUE", "showSMS=True", "showSMS=maybe",
		} {
			resp := postForm(t, setShowSMS, body)
			if resp.StatusCode != 400 {
				t.Errorf("%q: status = %d, want 400", body, resp.StatusCode)
			}
		}
	})

	t.Run("accepts_true", func(t *testing.T) {
		configMutex.Lock()
		userCfg.ShowSms = false
		configMutex.Unlock()

		resp := postForm(t, setShowSMS, "showSMS=true")
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		configMutex.RLock()
		got := userCfg.ShowSms
		configMutex.RUnlock()
		if !got {
			t.Error("showSMS=true did not enable SMS pages")
		}
	})

	t.Run("accepts_false", func(t *testing.T) {
		configMutex.Lock()
		userCfg.ShowSms = true
		configMutex.Unlock()

		resp := postForm(t, setShowSMS, "showSMS=false")
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		configMutex.RLock()
		got := userCfg.ShowSms
		configMutex.RUnlock()
		if got {
			t.Error("showSMS=false did not disable SMS pages")
		}
	})
}

// A non-numeric dimmer time must be rejected rather than silently becoming 0,
// which would dim the screen immediately.
func TestSetScreenDimmerTimeValidation(t *testing.T) {
	configMutex.Lock()
	savedBat := userCfg.ScreenDimmerTimeOnBatterySeconds
	savedDC := userCfg.ScreenDimmerTimeOnDCSeconds
	configMutex.Unlock()
	t.Cleanup(func() {
		configMutex.Lock()
		userCfg.ScreenDimmerTimeOnBatterySeconds = savedBat
		userCfg.ScreenDimmerTimeOnDCSeconds = savedDC
		configMutex.Unlock()
	})

	t.Run("rejects_bad_values", func(t *testing.T) {
		for _, body := range []string{
			"",
			"screen_dimmer_time_on_battery_seconds=abc&screen_dimmer_time_on_dc_seconds=300",
			"screen_dimmer_time_on_battery_seconds=30&screen_dimmer_time_on_dc_seconds=abc",
			"screen_dimmer_time_on_battery_seconds=30", // missing DC field
			"screen_dimmer_time_on_battery_seconds=1.5&screen_dimmer_time_on_dc_seconds=300",
		} {
			resp := postForm(t, setScreenDimmerTime, body)
			if resp.StatusCode != 400 {
				t.Errorf("%q: status = %d, want 400", body, resp.StatusCode)
			}
		}
	})

	t.Run("accepts_valid_values", func(t *testing.T) {
		resp := postForm(t, setScreenDimmerTime,
			"screen_dimmer_time_on_battery_seconds=45&screen_dimmer_time_on_dc_seconds=600")
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		configMutex.RLock()
		bat, dc := userCfg.ScreenDimmerTimeOnBatterySeconds, userCfg.ScreenDimmerTimeOnDCSeconds
		configMutex.RUnlock()
		if bat != 45 || dc != 600 {
			t.Errorf("stored (%d, %d), want (45, 600)", bat, dc)
		}
	})
}

// setPingSites has no validation of its own; mergeConfigs' length check is the
// safety net. Verify the values do land where the merge can see them.
func TestSetPingSitesStoresValues(t *testing.T) {
	configMutex.Lock()
	saved0, saved1 := userCfg.PingSite0, userCfg.PingSite1
	configMutex.Unlock()
	t.Cleanup(func() {
		configMutex.Lock()
		userCfg.PingSite0, userCfg.PingSite1 = saved0, saved1
		configMutex.Unlock()
	})

	resp := postForm(t, setPingSites, "ping_site0=9.9.9.9&ping_site1=example.org")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	configMutex.RLock()
	got0, got1 := userCfg.PingSite0, userCfg.PingSite1
	configMutex.RUnlock()
	if got0 != "9.9.9.9" {
		t.Errorf("PingSite0 = %q, want \"9.9.9.9\"", got0)
	}
	if got1 != "example.org" {
		t.Errorf("PingSite1 = %q, want \"example.org\"", got1)
	}
}

// Extra validateJSON cases beyond the table in httpServer_test.go: the
// remaining suspicious-content patterns and case-insensitivity, since the
// config body is echoed back into a browser.
func TestValidateJSONSuspiciousPatternCoverage(t *testing.T) {
	for _, s := range []string{
		`{"a":"document.cookie"}`,
		`{"a":"window.location"}`,
		`{"a":"<SCRIPT>"}`,  // uppercase must still trip the check
		`{"a":"</ScRiPt>"}`, // mixed case closing tag
	} {
		if err := validateJSON([]byte(s)); err == nil {
			t.Errorf("validateJSON(%s) accepted suspicious content", s)
		}
	}

	// Legitimate config values that merely look structural must pass.
	for _, s := range []string{`[]`, `[1,2,3]`, `{"nested":{"k":"v"}}`, `{"url":"https://example.org/ip"}`} {
		if err := validateJSON([]byte(s)); err != nil {
			t.Errorf("validateJSON(%s) = %v, want nil", s, err)
		}
	}
}

// secureUnmarshal must refuse the same inputs validateJSON does, and must not
// populate the target when it refuses.
func TestSecureUnmarshalRejectsAndLeavesTargetAlone(t *testing.T) {
	type payload struct {
		A string `json:"a"`
	}

	var got payload
	if err := secureUnmarshal([]byte(`{"a":"<script>x</script>"}`), &got); err == nil {
		t.Error("secureUnmarshal accepted suspicious content")
	}
	if got.A != "" {
		t.Errorf("target was populated despite rejection: %+v", got)
	}

	if err := secureUnmarshal([]byte(`{"a":"ok"}`), &got); err != nil {
		t.Fatalf("secureUnmarshal rejected valid input: %v", err)
	}
	if got.A != "ok" {
		t.Errorf("A = %q, want \"ok\"", got.A)
	}
}

// itoa avoids pulling strconv into the test file for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
