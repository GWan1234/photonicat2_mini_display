package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// ipServer returns an httptest server that always responds with the given
// status code and body, and records the User-Agent of the last request.
func ipServer(t *testing.T, status int, body string, lastUA *atomic.Value) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if lastUA != nil {
			lastUA.Store(r.Header.Get("User-Agent"))
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

func TestFetchOnePublicIP_Parsers(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		parser  string
		wantV6  bool
		want    string
		wantErr bool
	}{
		{"plain ipv4 stdout", "203.0.113.7\n", "stdout", false, "203.0.113.7", false},
		{"empty parser defaults to stdout", "203.0.113.7", "", false, "203.0.113.7", false},
		{"json field (ip-api style)", `{"query":"203.0.113.8","status":"success"}`, "json:query", false, "203.0.113.8", false},
		{"json nested path", `{"data":{"ip":"203.0.113.9"}}`, "json:data.ip", false, "203.0.113.9", false},
		{"regex capture", "Your IP is 203.0.113.10 right now", `regex:(\d+\.\d+\.\d+\.\d+)`, false, "203.0.113.10", false},
		{"ipv6 stdout", "2001:db8::1", "stdout", true, "2001:db8::1", false},
		{"ipv4 rejected when ipv6 wanted", "203.0.113.7", "stdout", true, "", true},
		{"ipv6 rejected when ipv4 wanted", "2001:db8::1", "stdout", false, "", true},
		{"garbage is not an ip", "not-an-ip", "stdout", false, "", true},
		{"bad json path", `{"query":"203.0.113.8"}`, "json:missing", false, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := ipServer(t, http.StatusOK, tt.body, nil)
			defer srv.Close()

			got, err := fetchOnePublicIP(PublicIPSource{URL: srv.URL, Parser: tt.parser}, "", tt.wantV6)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// A non-2xx response must be treated as a failure (not parsed as a body).
func TestFetchOnePublicIP_HTTPError(t *testing.T) {
	srv := ipServer(t, http.StatusServiceUnavailable, "203.0.113.7", nil)
	defer srv.Close()

	if _, err := fetchOnePublicIP(PublicIPSource{URL: srv.URL, Parser: "stdout"}, "", false); err == nil {
		t.Fatal("expected error on HTTP 503, got nil")
	}
}

// fetchPublicIP must skip failing/invalid sources and return the first one that
// yields a valid IP — this is the forum scenario (photonicat.com blocked, a
// later mirror still answers).
func TestFetchPublicIP_FallsBackToNextSource(t *testing.T) {
	blocked := ipServer(t, http.StatusOK, "blocked-page-html", nil) // returns junk
	defer blocked.Close()
	down := ipServer(t, http.StatusBadGateway, "", nil) // 502
	defer down.Close()
	good := ipServer(t, http.StatusOK, "198.51.100.42", nil)
	defer good.Close()

	sources := []PublicIPSource{
		{URL: blocked.URL, Parser: "stdout"},
		{URL: down.URL, Parser: "stdout"},
		{URL: good.URL, Parser: "stdout"},
	}
	got, err := fetchPublicIP(sources, "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "198.51.100.42" {
		t.Errorf("got %q, want 198.51.100.42", got)
	}
}

func TestFetchPublicIP_AllSourcesFail(t *testing.T) {
	down := ipServer(t, http.StatusInternalServerError, "", nil)
	defer down.Close()

	if _, err := fetchPublicIP([]PublicIPSource{{URL: down.URL}}, "", false); err == nil {
		t.Fatal("expected error when every source fails, got nil")
	}
	if _, err := fetchPublicIP(nil, "", false); err == nil {
		t.Fatal("expected error for empty source list, got nil")
	}
}

// A configured custom User-Agent must be sent; an empty one falls back to the
// default getUserAgent().
func TestFetchOnePublicIP_UserAgent(t *testing.T) {
	var lastUA atomic.Value
	srv := ipServer(t, http.StatusOK, "203.0.113.7", &lastUA)
	defer srv.Close()

	if _, err := fetchOnePublicIP(PublicIPSource{URL: srv.URL}, "MyCustomAgent/1.0", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ua, _ := lastUA.Load().(string); ua != "MyCustomAgent/1.0" {
		t.Errorf("custom UA not sent: got %q", ua)
	}

	if _, err := fetchOnePublicIP(PublicIPSource{URL: srv.URL}, "", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ua, _ := lastUA.Load().(string); ua != getUserAgent() {
		t.Errorf("default UA not used: got %q, want %q", ua, getUserAgent())
	}
}

// getPublicIPv4 / getIPv6Public read the global cfg; verify the config wiring
// and the IPv4/IPv6 family routing end-to-end.
func TestGetPublicIP_UsesConfig(t *testing.T) {
	v4 := ipServer(t, http.StatusOK, "192.0.2.55", nil)
	defer v4.Close()
	v6 := ipServer(t, http.StatusOK, "2001:db8::abcd", nil)
	defer v6.Close()

	saved := cfg.PublicIPLookup
	defer func() { cfg.PublicIPLookup = saved }()

	cfg.PublicIPLookup = PublicIPLookup{
		IPv4: []PublicIPSource{{URL: v4.URL, Parser: "stdout"}},
		IPv6: []PublicIPSource{{URL: v6.URL, Parser: "stdout"}},
	}

	if got, err := getPublicIPv4(); err != nil || got != "192.0.2.55" {
		t.Errorf("getPublicIPv4() = %q, %v; want 192.0.2.55, nil", got, err)
	}
	if got, err := getIPv6Public(); err != nil || got != "2001:db8::abcd" {
		t.Errorf("getIPv6Public() = %q, %v; want 2001:db8::abcd, nil", got, err)
	}
}
