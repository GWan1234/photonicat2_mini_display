package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// withPingLookup swaps the system-resolver seam for the duration of a test.
func withPingLookup(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	orig := pingLookupIP
	pingLookupIP = fn
	t.Cleanup(func() { pingLookupIP = orig })
}

// withDoHServer points the escape at a local endpoint and reports how many
// queries reached it, so the caching claims can be checked rather than assumed.
func withDoHServer(t *testing.T, handler http.HandlerFunc) *int32 {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	origURLs, origClient := pingDoHURLs, pingDoHClient
	pingDoHURLs = []string{srv.URL + "/resolve"}
	pingDoHClient = srv.Client()
	t.Cleanup(func() { pingDoHURLs, pingDoHClient = origURLs, origClient })

	resetPingEscapeCache(t)
	return &hits
}

func resetPingEscapeCache(t *testing.T) {
	t.Helper()
	pingEscapeMu.Lock()
	pingEscapeCache = map[string]pingEscape{}
	pingEscapeMu.Unlock()
}

func answerJSON(ip string) string {
	return fmt.Sprintf(`{"Status":0,"Answer":[{"name":"x.","type":1,"TTL":60,"data":%q}]}`, ip)
}

func TestPingTargetIPLiteralSkipsTheResolver(t *testing.T) {
	// The standing workaround for the whole fake-ip problem is an IP literal;
	// it must not acquire a DNS lookup it never had.
	called := false
	withPingLookup(t, func(string) (string, error) {
		called = true
		return "", nil
	})

	for _, host := range []string{"1.1.1.1", "223.5.5.5", ""} {
		if got := pingTarget(host); got != host {
			t.Errorf("pingTarget(%q) = %q, want it unchanged", host, got)
		}
	}
	if called {
		t.Error("resolver was consulted for an IP literal")
	}
}

func TestPingTargetHandsBackTheResolvedAddress(t *testing.T) {
	withPingLookup(t, func(string) (string, error) { return "93.184.216.34", nil })

	if got := pingTarget("example.com"); got != "93.184.216.34" {
		t.Errorf("pingTarget = %q, want the resolved address", got)
	}
}

func TestPingTargetKeepsTheHostnameWhenTheResolverFails(t *testing.T) {
	// A broken resolver is not a fake-ip problem: the pinger must be left to
	// fail exactly as it did before this escape existed.
	withPingLookup(t, func(string) (string, error) {
		return "", fmt.Errorf("no such host")
	})

	if got := pingTarget("example.com"); got != "example.com" {
		t.Errorf("pingTarget = %q, want the hostname unchanged", got)
	}
}

func TestPingTargetEscapesAFakeIPAnswer(t *testing.T) {
	withPingLookup(t, func(string) (string, error) { return "198.18.0.7", nil })
	hits := withDoHServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("name"); got != "photonicat.com" {
			t.Errorf("DoH query name = %q, want the row's hostname", got)
		}
		fmt.Fprint(w, answerJSON("104.21.5.9"))
	})

	if got := pingTarget("photonicat.com"); got != "104.21.5.9" {
		t.Errorf("pingTarget = %q, want the real address behind the fake-ip", got)
	}
	if *hits != 1 {
		t.Errorf("DoH queries = %d, want 1", *hits)
	}
}

func TestPingTargetReusesOneDoHAnswerAcrossProbes(t *testing.T) {
	// The rows probe at 1 Hz while their page is on screen; a DoH connection
	// per tick would be its own bug.
	withPingLookup(t, func(string) (string, error) { return "198.18.0.7", nil })
	hits := withDoHServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, answerJSON("104.21.5.9"))
	})

	for i := 0; i < 5; i++ {
		if got := pingTarget("photonicat.com"); got != "104.21.5.9" {
			t.Fatalf("probe %d: pingTarget = %q", i, got)
		}
	}
	if *hits != 1 {
		t.Errorf("DoH queries = %d over 5 probes, want 1", *hits)
	}
}

func TestPingTargetDoesNotRetryABlockedResolverEveryTick(t *testing.T) {
	withPingLookup(t, func(string) (string, error) { return "198.18.0.7", nil })
	hits := withDoHServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusForbidden)
	})

	for i := 0; i < 5; i++ {
		// Nothing better is available, so the row goes back to failing the way
		// it does today - but quietly, not by hammering a dead endpoint.
		if got := pingTarget("photonicat.com"); got != "photonicat.com" {
			t.Fatalf("probe %d: pingTarget = %q, want the hostname unchanged", i, got)
		}
	}
	if *hits != 1 {
		t.Errorf("DoH queries = %d over 5 probes, want 1", *hits)
	}
}

func TestPingTargetIsSafeAcrossConcurrentRows(t *testing.T) {
	// Ping0 and Ping1 are collected by separate goroutines, so they reach the
	// escape cache concurrently. Run under -race.
	withPingLookup(t, func(string) (string, error) { return "198.18.0.7", nil })
	hits := withDoHServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, answerJSON("104.21.5.9"))
	})

	const rows, probes = 8, 20
	var wg sync.WaitGroup
	for i := 0; i < rows; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < probes; j++ {
				if got := pingTarget("photonicat.com"); got != "104.21.5.9" {
					t.Errorf("pingTarget = %q", got)
					return
				}
			}
		}()
	}
	wg.Wait()

	// The lock is not held across the lookup - that would serialise both rows
	// behind one 1.5s network call - so a burst that all misses the cache at
	// once can duplicate the query. What must not happen is a query per probe.
	if *hits > rows {
		t.Errorf("DoH queries = %d over %d probes, want at most %d", *hits, rows*probes, rows)
	}
}

func TestPingQueryDoHRejectsUnusableAnswers(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"a CNAME rather than an address", `{"Answer":[{"type":5,"data":"cdn.example.net."}]}`},
		{"an answer that is itself a fake-ip", `{"Answer":[{"type":1,"data":"198.18.0.9"}]}`},
		{"an AAAA smuggled in as type 1", `{"Answer":[{"type":1,"data":"2606:4700::1111"}]}`},
		{"no answer section at all", `{"Status":3}`},
		{"not JSON", `<html>captive portal</html>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withPingLookup(t, func(string) (string, error) { return "198.18.0.7", nil })
			withDoHServer(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			})

			if got := pingTarget("photonicat.com"); got != "photonicat.com" {
				t.Errorf("pingTarget = %q, want the hostname unchanged", got)
			}
		})
	}
}

func TestPingIsFakeIP(t *testing.T) {
	for _, addr := range []string{"198.18.0.1", "198.18.255.255", "198.19.0.1"} {
		if !pingIsFakeIP(addr) {
			t.Errorf("pingIsFakeIP(%q) = false, want true", addr)
		}
	}
	for _, addr := range []string{"198.17.255.255", "198.20.0.1", "1.1.1.1", "", "nonsense"} {
		if pingIsFakeIP(addr) {
			t.Errorf("pingIsFakeIP(%q) = true, want false", addr)
		}
	}
}
