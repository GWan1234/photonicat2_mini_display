package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizePingType(t *testing.T) {
	cases := map[string]string{
		"":      pingTypeICMP,
		"icmp":  pingTypeICMP,
		"ICMP":  pingTypeICMP,
		"tcp":   pingTypeTCP,
		" TCP ": pingTypeTCP,
		"http":  pingTypeHTTP,
		"Http":  pingTypeHTTP,
		"bogus": pingTypeICMP,
	}
	for in, want := range cases {
		if got := normalizePingType(in); got != want {
			t.Errorf("normalizePingType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProbeForPingType(t *testing.T) {
	origICMP, origTCP, origHTTP := pingProbe, pingProbeTCP, pingProbeHTTP
	defer func() { pingProbe, pingProbeTCP, pingProbeHTTP = origICMP, origTCP, origHTTP }()

	pingProbe = func(string) (int64, error) { return 1, nil }
	pingProbeTCP = func(string) (int64, error) { return 2, nil }
	pingProbeHTTP = func(string) (int64, error) { return 3, nil }

	for in, want := range map[string]int64{
		"icmp": 1, "": 1, "junk": 1,
		"tcp":  2,
		"http": 3,
	} {
		got, _ := probeForPingType(in)("x")
		if got != want {
			t.Errorf("probeForPingType(%q) picked probe %d, want %d", in, got, want)
		}
	}
}

func TestPingTCPAddr(t *testing.T) {
	cases := map[string]string{
		"photonicat.com":              "photonicat.com:443",
		"photonicat.com:8080":         "photonicat.com:8080",
		"1.1.1.1":                     "1.1.1.1:443",
		"1.1.1.1:53":                  "1.1.1.1:53",
		"https://photonicat.com/path": "photonicat.com:443",
		"http://example.com:81/x":     "example.com:81",
		"[2001:db8::1]:22":            "[2001:db8::1]:22",
		"2001:db8::1":                 "[2001:db8::1]:443",
	}
	for in, want := range cases {
		if got := pingTCPAddr(in); got != want {
			t.Errorf("pingTCPAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPingSiteDisplay(t *testing.T) {
	cases := []struct{ site, ptype, want string }{
		{"google.com", "icmp", "google.com"},
		{"google.com", "", "google.com"},
		{"google.com", "tcp", "TCP google.com"},
		{"google.com", "http", "HTTP google.com"},
		{"http://www.google.com/generate_204", "http", "HTTP www.google.com/generate_204"},
		{"photonicat.com:443", "tcp", "TCP photonicat.com:443"},
	}
	for _, c := range cases {
		if got := pingSiteDisplay(c.site, c.ptype); got != c.want {
			t.Errorf("pingSiteDisplay(%q, %q) = %q, want %q", c.site, c.ptype, got, c.want)
		}
	}
}

func TestPingSiteLabel(t *testing.T) {
	if got := pingSiteLabel("https://www.google.com/generate_204"); got != "www.google.com/generate_204" {
		t.Errorf("pingSiteLabel stripped to %q", got)
	}
	if got := pingSiteLabel("1.1.1.1"); got != "1.1.1.1" {
		t.Errorf("pingSiteLabel(%q) = %q, want unchanged", "1.1.1.1", got)
	}
}

func TestPingClampMs(t *testing.T) {
	if got := pingClampMs(0); got != 1 {
		t.Errorf("sub-ms probe = %d, want clamp to 1", got)
	}
	if got := pingClampMs(25 * time.Millisecond); got != 25 {
		t.Errorf("25ms probe = %d, want 25", got)
	}
	if got := pingClampMs(3100 * time.Millisecond); got != -2 {
		t.Errorf("3.1s probe = %d, want -2 (timeout marker)", got)
	}
}

// TestPingTCPHandshake drives pingTCP against a real local listener: a
// completed handshake is a success, a closed port is a failure (not a
// timeout).
func TestPingTCPHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	ms, err := pingTCP(ln.Addr().String())
	if err != nil || ms <= 0 {
		t.Errorf("pingTCP(open port) = %d, %v; want >0, nil", ms, err)
	}

	// Grab a port that is then closed again so nothing listens on it.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadAddr := dead.Addr().String()
	dead.Close()

	ms, err = pingTCP(deadAddr)
	if ms != -1 || err == nil {
		t.Errorf("pingTCP(closed port) = %d, %v; want -1 and an error", ms, err)
	}
}

// TestPingHTTPGen204 drives pingHTTP against a local server: a 204 (gen_204
// style) and even a 404 both count as connectivity, and a bare host:port gets
// the http:// scheme filled in.
func TestPingHTTPGen204(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/generate_204" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	ms, err := pingHTTP(srv.URL + "/generate_204")
	if err != nil || ms <= 0 {
		t.Errorf("pingHTTP(204) = %d, %v; want >0, nil", ms, err)
	}

	ms, err = pingHTTP(srv.URL + "/missing")
	if err != nil || ms <= 0 {
		t.Errorf("pingHTTP(404) = %d, %v; want >0, nil (any response is connectivity)", ms, err)
	}

	bare := strings.TrimPrefix(srv.URL, "http://")
	ms, err = pingHTTP(bare)
	if err != nil || ms <= 0 {
		t.Errorf("pingHTTP(bare host) = %d, %v; want >0, nil", ms, err)
	}
}
