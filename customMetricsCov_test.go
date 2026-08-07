package main

// Coverage for the custom metric source lifecycles: HTTPSource (passive push),
// CommandSource (local shell command), HTTPPollSource (active URL poll) and
// JSONFileSource (file watch), plus the shared output parsers.
//
// All lifecycle tests use very long regular intervals so a started source
// performs exactly one immediate fetch/exec/read and then blocks on its timer
// until Stop() — deterministic under -race and -shuffle, no ticks in flight
// when the test ends. Every new top-level identifier here is MiscCov/miscCov
// prefixed so concurrently developed test files merge without collisions.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// miscCovWaitFor polls cond every 10ms until it is true or the deadline hits.
func miscCovWaitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s", timeout, what)
}

// miscCovCleanKeys removes the given globalData keys now and again at cleanup.
func miscCovCleanKeys(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		globalData.Delete(k)
	}
	t.Cleanup(func() {
		for _, k := range keys {
			globalData.Delete(k)
		}
	})
}

func TestMiscCovHTTPSourceLifecycle(t *testing.T) {
	miscCovCleanKeys(t, "MiscCovHKey1", "MiscCovHKey2", "MiscCovHNotAllowed")

	src, err := NewHTTPSource(SourceConfig{
		Type: "http_endpoint", Name: "misccov-http", Enabled: 1,
		Config: map[string]interface{}{
			"allowed_keys": []interface{}{"MiscCovHKey1", "MiscCovHKey2"},
		},
	}, GlobalMetricSettings{EnableLogging: 1})
	if err != nil {
		t.Fatalf("NewHTTPSource: %v", err)
	}

	if got := src.GetName(); got != "misccov-http" {
		t.Errorf("GetName() = %q, want %q", got, "misccov-http")
	}
	if got := src.GetType(); got != "http_endpoint" {
		t.Errorf("GetType() = %q, want %q", got, "http_endpoint")
	}
	if st := src.GetStatus(); st.Running {
		t.Error("new HTTPSource reports Running before Start")
	}

	if err := src.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st := src.GetStatus(); !st.Running || st.Stats.StartTime.IsZero() {
		t.Errorf("after Start: Running=%v StartTime=%v, want running with a start time", st.Running, st.Stats.StartTime)
	}

	// Allowed keys are stored, others silently skipped.
	if err := src.UpdateData(map[string]interface{}{
		"MiscCovHKey1":       "v1",
		"MiscCovHKey2":       42,
		"MiscCovHNotAllowed": "nope",
	}); err != nil {
		t.Fatalf("UpdateData: %v", err)
	}
	if v, _ := globalData.Load("MiscCovHKey1"); v != "v1" {
		t.Errorf("MiscCovHKey1 = %v, want \"v1\"", v)
	}
	if v, _ := globalData.Load("MiscCovHKey2"); v != "42" {
		t.Errorf("MiscCovHKey2 = %v, want \"42\" (fmt.Sprint of the raw value)", v)
	}
	if _, ok := globalData.Load("MiscCovHNotAllowed"); ok {
		t.Error("a key outside allowed_keys was stored")
	}
	if st := src.GetStatus(); st.Stats.SuccessCount != 1 || st.LastUpdate.IsZero() {
		t.Errorf("after UpdateData: SuccessCount=%d LastUpdate=%v", st.Stats.SuccessCount, st.LastUpdate)
	}

	if err := src.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if st := src.GetStatus(); st.Running {
		t.Error("HTTPSource still reports Running after Stop")
	}
}

func TestMiscCovHTTPSourceUnrestrictedAndBadConfig(t *testing.T) {
	miscCovCleanKeys(t, "MiscCovHFree1", "MiscCovHFree2")

	// No allowed_keys: every posted key is stored.
	src, err := NewHTTPSource(SourceConfig{
		Type: "http_endpoint", Name: "misccov-open", Enabled: 1,
		Config: map[string]interface{}{},
	}, GlobalMetricSettings{})
	if err != nil {
		t.Fatalf("NewHTTPSource: %v", err)
	}
	if err := src.UpdateData(map[string]interface{}{"MiscCovHFree1": "a", "MiscCovHFree2": 1.5}); err != nil {
		t.Fatalf("UpdateData: %v", err)
	}
	if v, _ := globalData.Load("MiscCovHFree1"); v != "a" {
		t.Errorf("MiscCovHFree1 = %v, want \"a\"", v)
	}
	if v, _ := globalData.Load("MiscCovHFree2"); v != "1.5" {
		t.Errorf("MiscCovHFree2 = %v, want \"1.5\"", v)
	}

	// A config whose fields cannot unmarshal into the typed struct is rejected.
	if _, err := NewHTTPSource(SourceConfig{
		Type: "http_endpoint", Name: "bad",
		Config: map[string]interface{}{"allowed_keys": "not-a-list"},
	}, GlobalMetricSettings{}); err == nil {
		t.Error("NewHTTPSource accepted allowed_keys of the wrong type")
	}
}

func TestMiscCovCommandSourceLifecycle(t *testing.T) {
	const key = "MiscCovCmdLine"
	miscCovCleanKeys(t, key)

	src, err := NewCommandSource(SourceConfig{
		Type: "command", Name: "misccov-cmd", Enabled: 1,
		Config: map[string]interface{}{
			"command":  `printf 'aa\nbb\ncc'`,
			"interval": 3600, // one immediate exec, then blocked until Stop
			"timeout":  5,
			"parser":   "line:1",
			"data_key": key,
		},
	}, GlobalMetricSettings{EnableLogging: 1})
	if err != nil {
		t.Fatalf("NewCommandSource: %v", err)
	}
	if got := src.GetType(); got != "command" {
		t.Errorf("GetType() = %q, want \"command\"", got)
	}

	if err := src.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	miscCovWaitFor(t, 5*time.Second, "command result", func() bool {
		v, ok := globalData.Load(key)
		return ok && v == "bb"
	})
	st := src.GetStatus()
	if st.Stats.SuccessCount < 1 || st.LastError != "" {
		t.Errorf("after exec: SuccessCount=%d LastError=%q", st.Stats.SuccessCount, st.LastError)
	}

	if err := src.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Second Stop is a no-op on an already-stopped source.
	if err := src.Stop(); err != nil {
		t.Errorf("second Stop returned %v, want nil", err)
	}
}

func TestMiscCovCommandSourceExecuteNow(t *testing.T) {
	const key = "MiscCovCmdNow"
	miscCovCleanKeys(t, key)

	src, err := NewCommandSource(SourceConfig{
		Type: "command", Name: "misccov-now", Enabled: 1,
		Config: map[string]interface{}{
			"command":  "echo 7",
			"data_key": key,
		},
	}, GlobalMetricSettings{})
	if err != nil {
		t.Fatalf("NewCommandSource: %v", err)
	}

	// ExecuteNow runs a single exec in its own goroutine; the source's run loop
	// is never started, so once the value lands nothing is left running.
	src.ExecuteNow()
	miscCovWaitFor(t, 5*time.Second, "ExecuteNow result", func() bool {
		v, ok := globalData.Load(key)
		return ok && v == "7"
	})
}

func TestMiscCovCommandSourceFailures(t *testing.T) {
	const failKey = "MiscCovCmdFail"
	const parseKey = "MiscCovCmdParse"
	miscCovCleanKeys(t, failKey, parseKey)

	// Non-zero exit stores the ERROR sentinel and bumps the error count.
	src, err := NewCommandSource(SourceConfig{
		Type: "command", Name: "misccov-fail", Enabled: 1,
		Config: map[string]interface{}{
			"command":  "exit 3",
			"data_key": failKey,
		},
	}, GlobalMetricSettings{EnableLogging: 1})
	if err != nil {
		t.Fatalf("NewCommandSource: %v", err)
	}
	if src.executeCommand() {
		t.Error("executeCommand reported success for a failing command")
	}
	if v, _ := globalData.Load(failKey); v != "ERROR" {
		t.Errorf("%s = %v, want ERROR sentinel", failKey, v)
	}
	if st := src.GetStatus(); st.Stats.ErrorCount != 1 || st.LastError == "" {
		t.Errorf("after failure: ErrorCount=%d LastError=%q", st.Stats.ErrorCount, st.LastError)
	}

	// Unparseable output stores the PARSE_ERROR sentinel.
	src2, err := NewCommandSource(SourceConfig{
		Type: "command", Name: "misccov-parse", Enabled: 1,
		Config: map[string]interface{}{
			"command":  "echo notjson",
			"parser":   "json:a.b",
			"data_key": parseKey,
		},
	}, GlobalMetricSettings{})
	if err != nil {
		t.Fatalf("NewCommandSource: %v", err)
	}
	if src2.executeCommand() {
		t.Error("executeCommand reported success for unparseable output")
	}
	if v, _ := globalData.Load(parseKey); v != "PARSE_ERROR" {
		t.Errorf("%s = %v, want PARSE_ERROR sentinel", parseKey, v)
	}

	// A config whose typed fields don't unmarshal is rejected up front.
	if _, err := NewCommandSource(SourceConfig{
		Type: "command", Name: "bad",
		Config: map[string]interface{}{"command": 5, "data_key": "K"},
	}, GlobalMetricSettings{}); err == nil {
		t.Error("NewCommandSource accepted a command of the wrong type")
	}
}

func TestMiscCovNewHTTPPollSourceValidationAndDefaults(t *testing.T) {
	settings := GlobalMetricSettings{}

	if _, err := NewHTTPPollSource(SourceConfig{
		Type: "http_poll", Name: "no-url",
		Config: map[string]interface{}{"data_key": "K"},
	}, settings); err == nil {
		t.Error("NewHTTPPollSource accepted a config without url")
	}
	if _, err := NewHTTPPollSource(SourceConfig{
		Type: "http_poll", Name: "no-key",
		Config: map[string]interface{}{"url": "http://127.0.0.1/x"},
	}, settings); err == nil {
		t.Error("NewHTTPPollSource accepted a config without data_key")
	}
	if _, err := NewHTTPPollSource(SourceConfig{
		Type: "http_poll", Name: "bad-type",
		Config: map[string]interface{}{"url": 12, "data_key": "K"},
	}, settings); err == nil {
		t.Error("NewHTTPPollSource accepted a url of the wrong type")
	}

	src, err := NewHTTPPollSource(SourceConfig{
		Type: "http_poll", Name: "defaults", Enabled: 1,
		Config: map[string]interface{}{
			"url":      "http://127.0.0.1/x",
			"data_key": "MiscCovPollDefaults",
			"method":   "post",
		},
	}, settings)
	if err != nil {
		t.Fatalf("NewHTTPPollSource: %v", err)
	}
	if src.interval != 60 {
		t.Errorf("interval = %d, want the 60s default", src.interval)
	}
	if src.timeout != 10 {
		t.Errorf("timeout = %d, want the 10s default", src.timeout)
	}
	if src.parser != "stdout" {
		t.Errorf("parser = %q, want \"stdout\"", src.parser)
	}
	if src.method != "POST" {
		t.Errorf("method = %q, want upper-cased \"POST\"", src.method)
	}
}

func TestMiscCovHTTPPollSourceLifecycle(t *testing.T) {
	const key = "MiscCovPollValue"
	miscCovCleanKeys(t, key)

	var sawHeader atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Misc-Cov") == "yes" {
			sawHeader.Store(true)
		}
		w.Write([]byte("  42.5\n"))
	}))
	defer server.Close()

	src, err := NewHTTPPollSource(SourceConfig{
		Type: "http_poll", Name: "misccov-poll", Enabled: 1,
		Config: map[string]interface{}{
			"url":      server.URL,
			"interval": 3600, // one immediate fetch, then blocked until Stop
			"timeout":  5,
			"data_key": key,
			"headers":  map[string]interface{}{"X-Misc-Cov": "yes"},
		},
	}, GlobalMetricSettings{EnableLogging: 1})
	if err != nil {
		t.Fatalf("NewHTTPPollSource: %v", err)
	}
	if got := src.GetName(); got != "misccov-poll" {
		t.Errorf("GetName() = %q, want %q", got, "misccov-poll")
	}
	if got := src.GetType(); got != "http_poll" {
		t.Errorf("GetType() = %q, want \"http_poll\"", got)
	}

	if err := src.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	miscCovWaitFor(t, 5*time.Second, "poll result", func() bool {
		v, ok := globalData.Load(key)
		return ok && v == "42.5"
	})
	if !sawHeader.Load() {
		t.Error("configured request header was not sent")
	}
	if st := src.GetStatus(); st.Stats.SuccessCount < 1 || st.LastError != "" {
		t.Errorf("after fetch: SuccessCount=%d LastError=%q", st.Stats.SuccessCount, st.LastError)
	}

	if err := src.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := src.Stop(); err != nil {
		t.Errorf("second Stop returned %v, want nil", err)
	}
}

func TestMiscCovHTTPPollSourceExecuteNow(t *testing.T) {
	const key = "MiscCovPollNow"
	miscCovCleanKeys(t, key)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("now-value"))
	}))
	defer server.Close()

	src, err := NewHTTPPollSource(SourceConfig{
		Type: "http_poll", Name: "misccov-poll-now", Enabled: 1,
		Config: map[string]interface{}{
			"url":      server.URL,
			"data_key": key,
		},
	}, GlobalMetricSettings{})
	if err != nil {
		t.Fatalf("NewHTTPPollSource: %v", err)
	}

	// ExecuteNow fetches once in its own goroutine without starting the loop.
	src.ExecuteNow()
	miscCovWaitFor(t, 5*time.Second, "ExecuteNow fetch", func() bool {
		v, ok := globalData.Load(key)
		return ok && v == "now-value"
	})
}

func TestMiscCovHTTPPollSourceFetchErrors(t *testing.T) {
	newSrc := func(t *testing.T, url, method, parser, key string) *HTTPPollSource {
		t.Helper()
		src, err := NewHTTPPollSource(SourceConfig{
			Type: "http_poll", Name: "misccov-err", Enabled: 1,
			Config: map[string]interface{}{
				"url":      url,
				"method":   method,
				"parser":   parser,
				"timeout":  5,
				"data_key": key,
			},
		}, GlobalMetricSettings{EnableLogging: 1})
		if err != nil {
			t.Fatalf("NewHTTPPollSource: %v", err)
		}
		return src
	}

	t.Run("http_status_error", func(t *testing.T) {
		const key = "MiscCovPollErr500"
		miscCovCleanKeys(t, key)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer server.Close()

		src := newSrc(t, server.URL, "GET", "stdout", key)
		if src.fetch() {
			t.Error("fetch reported success on HTTP 500")
		}
		if v, _ := globalData.Load(key); v != "ERROR" {
			t.Errorf("%s = %v, want ERROR sentinel", key, v)
		}
		if st := src.GetStatus(); st.Stats.ErrorCount != 1 || !strings.Contains(st.LastError, "HTTP 500") {
			t.Errorf("status after 500: ErrorCount=%d LastError=%q", st.Stats.ErrorCount, st.LastError)
		}
	})

	t.Run("parse_error", func(t *testing.T) {
		const key = "MiscCovPollErrParse"
		miscCovCleanKeys(t, key)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("not json"))
		}))
		defer server.Close()

		src := newSrc(t, server.URL, "GET", "json:a.b", key)
		if src.fetch() {
			t.Error("fetch reported success on unparseable body")
		}
		if v, _ := globalData.Load(key); v != "PARSE_ERROR" {
			t.Errorf("%s = %v, want PARSE_ERROR sentinel", key, v)
		}
	})

	t.Run("connection_refused", func(t *testing.T) {
		const key = "MiscCovPollErrConn"
		miscCovCleanKeys(t, key)
		// Grab a URL that is guaranteed closed by shutting the server first.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := server.URL
		server.Close()

		src := newSrc(t, url, "GET", "stdout", key)
		if src.fetch() {
			t.Error("fetch reported success against a closed server")
		}
		if v, _ := globalData.Load(key); v != "ERROR" {
			t.Errorf("%s = %v, want ERROR sentinel", key, v)
		}
	})

	t.Run("bad_request", func(t *testing.T) {
		const key = "MiscCovPollErrReq"
		miscCovCleanKeys(t, key)
		// A method with a space fails http.NewRequestWithContext validation.
		src := newSrc(t, "http://127.0.0.1/x", "bad method", "stdout", key)
		if src.fetch() {
			t.Error("fetch reported success with an invalid method")
		}
		if st := src.GetStatus(); !strings.Contains(st.LastError, "bad request") {
			t.Errorf("LastError = %q, want a bad request error", st.LastError)
		}
	})
}

func TestMiscCovJSONFileSourceLifecycle(t *testing.T) {
	const key = "MiscCovJSONLive"
	miscCovCleanKeys(t, key)

	path := filepath.Join(t.TempDir(), "m.json")
	if err := os.WriteFile(path, []byte(`{"a":{"b":"5"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	src, err := NewJSONFileSource(SourceConfig{
		Type: "json_file", Name: "misccov-json", Enabled: 1,
		Config: map[string]interface{}{
			"path":     path,
			"interval": 3600, // one immediate read, then blocked until Stop
			"mappings": []interface{}{
				map[string]interface{}{"data_key": key, "json_path": "a.b"},
			},
		},
	}, GlobalMetricSettings{EnableLogging: 1})
	if err != nil {
		t.Fatalf("NewJSONFileSource: %v", err)
	}
	if got := src.GetType(); got != "json_file" {
		t.Errorf("GetType() = %q, want \"json_file\"", got)
	}

	if err := src.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	miscCovWaitFor(t, 5*time.Second, "json file value", func() bool {
		v, ok := globalData.Load(key)
		return ok && v == "5"
	})

	if err := src.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := src.Stop(); err != nil {
		t.Errorf("second Stop returned %v, want nil", err)
	}
	if st := src.GetStatus(); st.Running {
		t.Error("JSONFileSource still reports Running after Stop")
	}
}

func TestMiscCovCreateSourceHTTPTypes(t *testing.T) {
	settings := GlobalMetricSettings{}

	src, err := createSource(SourceConfig{
		Type: "http_endpoint", Name: "cs-endpoint", Enabled: 1,
		Config: map[string]interface{}{"allowed_keys": []interface{}{"K"}},
	}, settings)
	if err != nil || src == nil {
		t.Fatalf("createSource(http_endpoint) = %v, %v", src, err)
	}
	if src.GetType() != "http_endpoint" {
		t.Errorf("GetType() = %q, want \"http_endpoint\"", src.GetType())
	}

	src, err = createSource(SourceConfig{
		Type: "http_poll", Name: "cs-poll", Enabled: 1,
		Config: map[string]interface{}{
			"url":      "http://127.0.0.1/x",
			"data_key": "MiscCovCSPoll",
		},
	}, settings)
	if err != nil || src == nil {
		t.Fatalf("createSource(http_poll) = %v, %v", src, err)
	}
	if src.GetType() != "http_poll" {
		t.Errorf("GetType() = %q, want \"http_poll\"", src.GetType())
	}
}

func TestMiscCovParseCommandOutput(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		parser  string
		want    string
		wantErr bool
	}{
		{"stdout_trims", "  hello \n", "stdout", "hello", false},
		{"json_path", `{"a":{"b":"v"}}`, "json:a.b", "v", false},
		{"line_valid", "l0\nl1\nl2", "line:1", "l1", false},
		{"line_trims", "l0\n  l1  \n", "line:1", "l1", false},
		{"line_out_of_range", "only", "line:9", "", true},
		{"line_bad_format", "x", "line:notanumber", "", true},
		{"regex_capture", "temp=42C", `regex:temp=(\d+)`, "42", false},
		{"regex_no_match", "cold", `regex:temp=(\d+)`, "", true},
		{"regex_no_group", "temp=42", `regex:temp=\d+`, "", true},
		{"regex_invalid", "x", "regex:(unclosed", "", true},
		{"unknown_parser", "x", "csv:0", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCommandOutput(tt.output, tt.parser)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseCommandOutput(%q, %q) error = %v, wantErr %v", tt.output, tt.parser, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("parseCommandOutput(%q, %q) = %q, want %q", tt.output, tt.parser, got, tt.want)
			}
		})
	}
}

func TestMiscCovExtractJSONPath(t *testing.T) {
	doc := `{"a":{"b":"v","n":7},"arr":[{"x":"first"},{"x":"second"}],"nil":null,"f":1.5}`

	tests := []struct {
		name    string
		json    string
		path    string
		want    string
		wantErr bool
	}{
		{"nested_map", doc, "a.b", "v", false},
		{"number_value", doc, "a.n", "7", false},
		{"float_value", doc, "f", "1.5", false},
		{"array_index", doc, "arr.1.x", "second", false},
		{"null_is_empty", doc, "nil", "", false},
		{"missing_key", doc, "a.zzz", "", true},
		{"array_non_numeric_index", doc, "arr.x", "", true},
		{"array_index_out_of_range", doc, "arr.9.x", "", true},
		{"traverse_into_scalar", doc, "a.b.deeper", "", true},
		{"invalid_json", "{nope", "a", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractJSONPath(tt.json, tt.path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("extractJSONPath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("extractJSONPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
