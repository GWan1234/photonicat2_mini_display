package main

// Shared helpers for tests that need to intercept the daemon's calls to
// pcat-manager-web. Those call sites hardcode http://localhost:80/... and go
// through localHTTPClient, so the cleanest seam is to swap that client's
// transport for one that rewrites the destination to an httptest server.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// rewriteTransport sends every request to target, preserving the original
// path and query so handlers can still route on them.
type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.target.Scheme
	clone.URL.Host = rt.target.Host
	clone.Host = rt.target.Host
	base := rt.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

// redirectLocalHTTPURL points localHTTPClient at rawURL for the duration of a
// test. The returned func restores the original transport; it is also
// registered with t.Cleanup so a test that forgets to call it still unwinds.
func redirectLocalHTTPURL(t *testing.T, rawURL string) func() {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("bad redirect URL %q: %v", rawURL, err)
	}

	saved := localHTTPClient.Transport
	localHTTPClient.Transport = &rewriteTransport{target: u}

	var restored bool
	restore := func() {
		if !restored {
			localHTTPClient.Transport = saved
			restored = true
		}
	}
	t.Cleanup(restore)
	return restore
}

// redirectLocalHTTP is the httptest.Server form of redirectLocalHTTPURL.
func redirectLocalHTTP(t *testing.T, srv *httptest.Server) func() {
	t.Helper()
	return redirectLocalHTTPURL(t, srv.URL)
}
