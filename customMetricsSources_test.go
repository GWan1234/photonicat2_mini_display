package main

// Custom metric sources are user-configured, so every constructor is a place
// where a malformed config from the web UI reaches the daemon. They must
// return errors rather than build a half-initialised source that panics or
// spins on a nil field later.

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateSourceKnownTypes(t *testing.T) {
	settings := GlobalMetricSettings{}

	tests := []struct {
		name   string
		config SourceConfig
	}{
		{
			name: "command",
			config: SourceConfig{
				Type: "command", Name: "cmd", Enabled: 1,
				Config: map[string]interface{}{
					"command":  "echo hi",
					"interval": 10,
					"timeout":  5,
					"parser":   "stdout",
					"data_key": "Cmd",
				},
			},
		},
		{
			name: "env",
			config: SourceConfig{
				Type: "env", Name: "envsrc", Enabled: 1,
				Config: map[string]interface{}{
					"refresh_interval": 30,
					"variables": []interface{}{
						map[string]interface{}{"env_var": "HOME", "data_key": "Home"},
					},
				},
			},
		},
		{
			name: "json_file",
			config: SourceConfig{
				Type: "json_file", Name: "jf", Enabled: 1,
				Config: map[string]interface{}{
					"path":     "/tmp/does-not-need-to-exist.json",
					"interval": 5,
					"mappings": []interface{}{
						map[string]interface{}{"data_key": "K", "json_path": "a.b"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, err := createSource(tt.config, settings)
			if err != nil {
				t.Fatalf("createSource(%s) returned error: %v", tt.name, err)
			}
			if src == nil {
				t.Fatal("createSource returned a nil source with no error")
			}
			if got := src.GetName(); got != tt.config.Name {
				t.Errorf("GetName() = %q, want %q", got, tt.config.Name)
			}
			if got := src.GetType(); got != tt.config.Type {
				t.Errorf("GetType() = %q, want %q", got, tt.config.Type)
			}
			// A freshly created source must report itself as not running.
			if st := src.GetStatus(); st.Running {
				t.Error("a newly created source reports Running=true")
			}
		})
	}
}

// An unrecognised type must be rejected, not silently ignored — otherwise a
// typo in the web UI produces a source that never reports anything and gives
// no clue why.
func TestCreateSourceUnknownTypeErrors(t *testing.T) {
	_, err := createSource(SourceConfig{Type: "does_not_exist", Name: "x"}, GlobalMetricSettings{})
	if err == nil {
		t.Fatal("createSource accepted an unknown source type")
	}
}

// Each constructor's required-field validation. A source missing these would
// otherwise start a goroutine that can never produce a value.
func TestSourceConstructorsRejectIncompleteConfig(t *testing.T) {
	settings := GlobalMetricSettings{}

	tests := []struct {
		name   string
		config SourceConfig
	}{
		{"env_no_variables", SourceConfig{
			Type: "env", Name: "e",
			Config: map[string]interface{}{"refresh_interval": 10},
		}},
		{"json_file_no_path", SourceConfig{
			Type: "json_file", Name: "j",
			Config: map[string]interface{}{
				"mappings": []interface{}{map[string]interface{}{"data_key": "K", "json_path": "a"}},
			},
		}},
		{"json_file_no_mappings", SourceConfig{
			Type: "json_file", Name: "j",
			Config: map[string]interface{}{"path": "/tmp/x.json"},
		}},
		{"command_no_command", SourceConfig{
			Type: "command", Name: "c",
			Config: map[string]interface{}{"data_key": "K"},
		}},
		{"command_no_data_key", SourceConfig{
			Type: "command", Name: "c",
			Config: map[string]interface{}{"command": "echo hi"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := createSource(tt.config, settings); err == nil {
				t.Error("constructor accepted an incomplete config")
			}
		})
	}
}

// A sub-1s interval is clamped to a sane default rather than spinning the CPU.
func TestNewJSONFileSourceClampsInterval(t *testing.T) {
	src, err := NewJSONFileSource(SourceConfig{
		Type: "json_file", Name: "jf", Enabled: 1,
		Config: map[string]interface{}{
			"path":     "/tmp/x.json",
			"interval": 0,
			"mappings": []interface{}{
				map[string]interface{}{"data_key": "K", "json_path": "a"},
			},
		},
	}, GlobalMetricSettings{})
	if err != nil {
		t.Fatalf("NewJSONFileSource: %v", err)
	}
	if src.interval != 5 {
		t.Errorf("interval = %d, want the 5s clamp for a zero/negative config", src.interval)
	}
}

// Unspecified interval/timeout/parser fall back to safe defaults instead of
// zero — a zero interval would spin the CPU and a zero timeout would kill
// every command before it produced output.
func TestNewCommandSourceAppliesDefaults(t *testing.T) {
	src, err := NewCommandSource(SourceConfig{
		Type: "command", Name: "cmd", Enabled: 1,
		Config: map[string]interface{}{
			"command":  "echo hi",
			"data_key": "CmdDefaults",
		},
	}, GlobalMetricSettings{})
	if err != nil {
		t.Fatalf("NewCommandSource: %v", err)
	}
	if src.interval != 5 {
		t.Errorf("interval = %d, want the 5s default", src.interval)
	}
	if src.timeout != 5 {
		t.Errorf("timeout = %d, want the 5s default", src.timeout)
	}
	if src.parser != "stdout" {
		t.Errorf("parser = %q, want \"stdout\"", src.parser)
	}
}

func TestJSONFileSourceReadsValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.json")
	if err := os.WriteFile(path, []byte(`{"outer":{"inner":"42"},"top":"hello"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	src, err := NewJSONFileSource(SourceConfig{
		Type: "json_file", Name: "jf", Enabled: 1,
		Config: map[string]interface{}{
			"path":     path,
			"interval": 5,
			"mappings": []interface{}{
				map[string]interface{}{"data_key": "TestInner", "json_path": "outer.inner"},
				map[string]interface{}{"data_key": "TestTop", "json_path": "top"},
			},
		},
	}, GlobalMetricSettings{})
	if err != nil {
		t.Fatalf("NewJSONFileSource: %v", err)
	}

	if ok := src.readJSONFile(); !ok {
		t.Errorf("readJSONFile reported failure on a valid file: %+v", src.GetStatus().LastError)
	}
	if got, _ := globalData.Load("TestInner"); got != "42" {
		t.Errorf("TestInner = %v, want \"42\"", got)
	}
	if got, _ := globalData.Load("TestTop"); got != "hello" {
		t.Errorf("TestTop = %v, want \"hello\"", got)
	}
	if st := src.GetStatus(); st.Stats.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", st.Stats.SuccessCount)
	}
}

// A missing file, malformed JSON, or a path that isn't there must publish an
// error sentinel so the display shows a marker instead of a stale value.
func TestJSONFileSourceErrorSentinels(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		write   bool
		content string
		dataKey string
	}{
		{"missing_file", false, "", "SentinelMissing"},
		{"bad_json", true, `{not valid`, "SentinelBadJSON"},
		{"path_absent_in_json", true, `{"other":"x"}`, "SentinelNoPath"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name+".json")
			if tt.write {
				if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			src, err := NewJSONFileSource(SourceConfig{
				Type: "json_file", Name: tt.name, Enabled: 1,
				Config: map[string]interface{}{
					"path":     path,
					"interval": 5,
					"mappings": []interface{}{
						map[string]interface{}{"data_key": tt.dataKey, "json_path": "outer.inner"},
					},
				},
			}, GlobalMetricSettings{})
			if err != nil {
				t.Fatalf("NewJSONFileSource: %v", err)
			}

			if ok := src.readJSONFile(); ok {
				t.Error("readJSONFile reported success on a broken input")
			}
			raw, ok := globalData.Load(tt.dataKey)
			if !ok {
				t.Fatalf("%s was never stored — the slot keeps a stale value", tt.dataKey)
			}
			got, isStr := raw.(string)
			if !isStr {
				t.Fatalf("%s = %#v, want a string sentinel", tt.dataKey, raw)
			}
			if !isErrorSentinel(got) {
				t.Errorf("%s = %q, want an error sentinel", tt.dataKey, got)
			}
			if st := src.GetStatus(); st.Stats.ErrorCount == 0 {
				t.Error("ErrorCount was not incremented on failure")
			}
		})
	}
}

func TestEnvVarSourceReadsAndDefaults(t *testing.T) {
	t.Setenv("PCAT_TEST_METRIC", "present-value")

	src, err := NewEnvVarSource(SourceConfig{
		Type: "env", Name: "envsrc", Enabled: 1,
		Config: map[string]interface{}{
			"refresh_interval": 30,
			"variables": []interface{}{
				map[string]interface{}{"env_var": "PCAT_TEST_METRIC", "data_key": "EnvPresent"},
				map[string]interface{}{"env_var": "PCAT_TEST_UNSET", "data_key": "EnvFallback", "default": "fallback-value"},
				map[string]interface{}{"env_var": "PCAT_TEST_UNSET2", "data_key": "EnvEmpty"},
			},
		},
	}, GlobalMetricSettings{})
	if err != nil {
		t.Fatalf("NewEnvVarSource: %v", err)
	}

	src.readEnvironmentVariables()

	if got, _ := globalData.Load("EnvPresent"); got != "present-value" {
		t.Errorf("EnvPresent = %v, want \"present-value\"", got)
	}
	// An unset variable with a configured default takes the default.
	if got, _ := globalData.Load("EnvFallback"); got != "fallback-value" {
		t.Errorf("EnvFallback = %v, want \"fallback-value\"", got)
	}
	// An unset variable with no default stores empty rather than being skipped.
	if got, ok := globalData.Load("EnvEmpty"); !ok || got != "" {
		t.Errorf("EnvEmpty = %v (stored=%v), want \"\"", got, ok)
	}

	if st := src.GetStatus(); st.Stats.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", st.Stats.SuccessCount)
	}
}

// isTimeoutErr decides whether a failure is retried on the short error
// interval or treated as a hard failure, so it must classify both the
// context deadline and net.Error timeouts.
func TestIsTimeoutErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context_deadline", context.DeadlineExceeded, true},
		{"wrapped_context_deadline", errWrap(context.DeadlineExceeded), true},
		{"net_timeout", &net.DNSError{IsTimeout: true}, true},
		{"net_non_timeout", &net.DNSError{IsTimeout: false}, false},
		{"context_canceled", context.Canceled, false},
		{"plain_error", errors.New("boom"), false},
		{"os_not_exist", os.ErrNotExist, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTimeoutErr(tt.err); got != tt.want {
				t.Errorf("isTimeoutErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func errWrap(err error) error { return wrappedErr{err} }

type wrappedErr struct{ err error }

func (w wrappedErr) Error() string { return "wrapped: " + w.err.Error() }
func (w wrappedErr) Unwrap() error { return w.err }

// The manager skips disabled sources entirely rather than creating them and
// leaving them idle.
func TestNewCustomMetricManagerSkipsDisabledSources(t *testing.T) {
	mgr, err := NewCustomMetricManager(CustomMetricsConfig{
		Sources: []SourceConfig{
			{
				Type: "env", Name: "enabled-one", Enabled: 1,
				Config: map[string]interface{}{
					"refresh_interval": 30,
					"variables": []interface{}{
						map[string]interface{}{"env_var": "HOME", "data_key": "MgrHome"},
					},
				},
			},
			{
				Type: "env", Name: "disabled-one", Enabled: 0,
				Config: map[string]interface{}{
					"refresh_interval": 30,
					"variables": []interface{}{
						map[string]interface{}{"env_var": "PATH", "data_key": "MgrPath"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewCustomMetricManager: %v", err)
	}

	statuses := mgr.GetAllStatus()
	if len(statuses) != 1 {
		t.Fatalf("manager holds %d sources, want 1 (the disabled one must be skipped)", len(statuses))
	}
	if statuses[0].Name != "enabled-one" {
		t.Errorf("kept source %q, want \"enabled-one\"", statuses[0].Name)
	}

	if src := mgr.GetSourceByName("enabled-one"); src == nil {
		t.Error("GetSourceByName could not find the enabled source")
	}
	if src := mgr.GetSourceByName("disabled-one"); src != nil {
		t.Error("GetSourceByName returned a source that should have been skipped")
	}
	if src := mgr.GetSourceByName("never-configured"); src != nil {
		t.Error("GetSourceByName invented a source that was never configured")
	}
}

// Start/Stop must be safe to call and must leave the manager stopped, with no
// goroutines still running when the test ends.
func TestCustomMetricManagerStartStop(t *testing.T) {
	mgr, err := NewCustomMetricManager(CustomMetricsConfig{
		Sources: []SourceConfig{
			{
				Type: "env", Name: "cycle", Enabled: 1,
				Config: map[string]interface{}{
					"refresh_interval": 3600, // long, so it ticks once at most
					"variables": []interface{}{
						map[string]interface{}{"env_var": "HOME", "data_key": "CycleHome"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewCustomMetricManager: %v", err)
	}

	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st := mgr.GetAllStatus(); len(st) != 1 || !st[0].Running {
		t.Error("source does not report Running after Start")
	}

	done := make(chan struct{})
	go func() { defer close(done); mgr.Stop() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop() hung")
	}

	if st := mgr.GetAllStatus(); len(st) == 1 && st[0].Running {
		t.Error("source still reports Running after Stop")
	}
}
