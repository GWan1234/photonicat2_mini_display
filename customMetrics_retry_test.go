package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryDelay(t *testing.T) {
	interval := 600 * time.Second

	tests := []struct {
		name           string
		consecFailures int
		everSucceeded  bool
		gs             GlobalMetricSettings
		expected       time.Duration
	}{
		{"no failures uses interval", 0, true, GlobalMetricSettings{}, interval},
		{"first failure uses default retry", 1, true, GlobalMetricSettings{}, defaultErrorRetryInterval},
		{"failures within default max retries", 5, true, GlobalMetricSettings{}, defaultErrorRetryInterval},
		{"failures beyond default max retries fall back", 6, true, GlobalMetricSettings{}, interval},
		{"configured retry interval", 1, true, GlobalMetricSettings{ErrorRetryInterval: 30}, 30 * time.Second},
		{"configured max retries respected", 3, true, GlobalMetricSettings{MaxRetries: 2}, interval},
		{"configured max retries within bound", 2, true, GlobalMetricSettings{MaxRetries: 2}, defaultErrorRetryInterval},
		{"retry never slower than interval", 1, true, GlobalMetricSettings{ErrorRetryInterval: 9999}, interval},
		// Never-succeeded sources back off exponentially instead of giving up:
		// 15s x5, then 30s, 60s, 120s ... capped at interval.
		{"never succeeded: first backoff step", 6, false, GlobalMetricSettings{}, 30 * time.Second},
		{"never succeeded: second backoff step", 7, false, GlobalMetricSettings{}, 60 * time.Second},
		{"never succeeded: backoff capped at interval", 20, false, GlobalMetricSettings{}, interval},
		{"never succeeded: within max retries still fast", 3, false, GlobalMetricSettings{}, defaultErrorRetryInterval},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := retryDelay(interval, tt.consecFailures, tt.everSucceeded, tt.gs)
			if got != tt.expected {
				t.Errorf("retryDelay(%v, %d, %v, %+v) = %v, want %v",
					interval, tt.consecFailures, tt.everSucceeded, tt.gs, got, tt.expected)
			}
		})
	}

	// A short regular interval beats the retry interval.
	short := 5 * time.Second
	if got := retryDelay(short, 1, true, GlobalMetricSettings{}); got != short {
		t.Errorf("retryDelay with short interval = %v, want %v", got, short)
	}
}

func TestStoreSentinelKeepsGoodValue(t *testing.T) {
	const key = "test_sentinel_key"
	globalData.Delete(key)
	defer globalData.Delete(key)

	// No prior value: sentinel is stored so the screen shows the error state.
	storeSentinel(key, "ERROR")
	if v, _ := globalData.Load(key); v != "ERROR" {
		t.Errorf("expected sentinel ERROR for empty key, got %v", v)
	}

	// Prior value (even a sentinel) is never overwritten by a new sentinel.
	globalData.Store(key, "42")
	storeSentinel(key, "TIMEOUT")
	if v, _ := globalData.Load(key); v != "42" {
		t.Errorf("expected good value 42 to survive sentinel, got %v", v)
	}
}

func TestIsErrorSentinel(t *testing.T) {
	for _, sentinel := range []string{"ERROR", "TIMEOUT", "PARSE_ERROR", "FILE_ERROR", "EXTRACT_ERROR"} {
		if !isErrorSentinel(sentinel) {
			t.Errorf("expected %q to be classified as an error sentinel", sentinel)
		}
	}
	for _, value := range []string{"", "-", "42", "21.5", "OK", "error", "Error"} {
		if isErrorSentinel(value) {
			t.Errorf("expected %q NOT to be classified as an error sentinel", value)
		}
	}
}

// Simulates the boot scenario: the endpoint fails for the first two requests
// (daemon up before network/DNS) and then recovers. With a 60s interval the
// old code would not retry for a minute; the retry logic must recover within
// a few error_retry_interval ticks instead.
func TestHTTPPollSourceRetriesAfterFailure(t *testing.T) {
	const key = "test_retry_key"
	globalData.Delete(key)
	defer globalData.Delete(key)

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("99.9"))
	}))
	defer server.Close()

	source, err := NewHTTPPollSource(SourceConfig{
		Type:    "http_poll",
		Name:    "retry-test",
		Enabled: 1,
		Config: map[string]interface{}{
			"url":      server.URL,
			"interval": 60, // old behavior: stuck with ERROR for 60s
			"timeout":  2,
			"data_key": key,
		},
	}, GlobalMetricSettings{ErrorRetryInterval: 1, MaxRetries: 5})
	if err != nil {
		t.Fatalf("NewHTTPPollSource: %v", err)
	}

	if err := source.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer source.Stop()

	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			v, _ := globalData.Load(key)
			t.Fatalf("source did not recover within 10s (calls=%d, value=%v)", calls.Load(), v)
		case <-tick.C:
			if v, ok := globalData.Load(key); ok && v == "99.9" {
				if c := calls.Load(); c < 3 {
					t.Fatalf("expected at least 3 attempts, got %d", c)
				}
				return // recovered via fast retry
			}
		}
	}
}
