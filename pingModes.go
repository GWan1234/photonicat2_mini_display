package main

// TCP and HTTP probe modes for the two LCD ping rows.
//
// ICMP answers whether a host is reachable, but plenty of networks (and
// transparent proxies) drop or fake ICMP while real traffic flows fine. The
// two extra modes measure what the user actually cares about:
//
//   - tcp:  one TCP handshake to host[:port] (port 443 when omitted). Success
//     means the SYN/SYN-ACK round trip completed, so the path carries real
//     connections, not just echo replies.
//   - http: one HTTP GET to a URL (scheme defaults to http://). Any HTTP
//     response counts as success — a captive-portal-style 204 from
//     generate_204, a 200, even a 404 all prove the request went out and an
//     answer came back. This is the Android/Google "gen_204" style check.
//
// Neither mode goes through pingTarget's fake-ip escape: a fake-ip proxy
// hairpins TCP, so dialing the poisoned address still exercises (and times)
// the path the user's traffic actually takes — which is the point.

import (
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	pingTypeICMP  = "icmp"
	pingTypeTCP   = "tcp"
	pingTypeHTTP  = "http"
	pingTypeHTTPS = "https"
)

// normalizePingType maps any config value onto one of the three modes.
// Unknown or empty values fall back to ICMP so an old user_config, a typo, or
// a newer web UI never turns a row off.
func normalizePingType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case pingTypeTCP:
		return pingTypeTCP
	case pingTypeHTTP:
		return pingTypeHTTP
	case pingTypeHTTPS:
		return pingTypeHTTPS
	default:
		return pingTypeICMP
	}
}

// pingSiteLabel is what the LCD prints for a site. The scheme is dropped
// because the row is only ~170px wide and "http://" says nothing there.
func pingSiteLabel(site string) string {
	site = strings.TrimPrefix(site, "https://")
	site = strings.TrimPrefix(site, "http://")
	return site
}

// pingSiteDisplay is the ping page's site text: the scheme-stripped site,
// prefixed with the probe mode when it is not the ICMP default — "TCP
// google.com" / "HTTP google.com" — so the row says what kind of "up" it is
// measuring.
func pingSiteDisplay(site, pingType string) string {
	label := pingSiteLabel(site)
	switch normalizePingType(pingType) {
	case pingTypeTCP:
		return "TCP " + label
	case pingTypeHTTP:
		return "HTTP " + label
	case pingTypeHTTPS:
		return "HTTPS " + label
	default:
		return label
	}
}

// pingTCPDefaultPort is used when the site has no explicit port. 443 rather
// than 80 because HTTPS is the one port effectively every site answers on.
const pingTCPDefaultPort = "443"

// pingTCPAddr turns a configured site into the host:port DialTimeout wants.
// Accepts "host", "host:port", and tolerates a pasted URL by stripping the
// scheme and path first.
func pingTCPAddr(site string) string {
	site = pingSiteLabel(site)
	if i := strings.IndexByte(site, '/'); i >= 0 {
		site = site[:i]
	}
	if host, port, err := net.SplitHostPort(site); err == nil && port != "" {
		return net.JoinHostPort(host, port)
	}
	// No (parseable) port — bare hostname, IPv4, or bracketless IPv6.
	return net.JoinHostPort(strings.Trim(site, "[]"), pingTCPDefaultPort)
}

// pingClampMs converts an elapsed probe time to the row's value: at least 1
// (publish treats <=0 as failure) and a timeout marker past pingTimeout's
// 3000ms, matching pingICMP's classification.
func pingClampMs(elapsed time.Duration) int64 {
	ms := elapsed.Milliseconds()
	if ms > 3000 {
		return -2
	}
	if ms < 1 {
		return 1
	}
	return ms
}

// pingErrIsTimeout mirrors pingICMP's timeout detection for dial/HTTP errors.
func pingErrIsTimeout(err error) bool {
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	return strings.Contains(err.Error(), "timeout") ||
		strings.Contains(err.Error(), "deadline exceeded")
}

// pingTCP times one TCP handshake to the site. Same return convention as
// pingICMP: round trip in ms, -2 for a timeout, -1 for any other failure.
func pingTCP(site string) (int64, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", pingTCPAddr(site), pingTimeout)
	if err != nil {
		if pingErrIsTimeout(err) {
			return -2, nil
		}
		return -1, err
	}
	conn.Close()
	return pingClampMs(time.Since(start)), nil
}

// pingHTTPPingClient is dedicated to the http rows: keep-alives are off so
// every probe measures a full fresh request (otherwise tick 2 onward would
// time a reused connection and read absurdly low), and redirects are not
// followed — the first response already proves the path works and is the only
// one whose timing means anything.
var pingHTTPPingClient = &http.Client{
	Timeout: pingTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Transport: &uaTransport{base: &http.Transport{
		DisableKeepAlives:   true,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: pingTimeout,
	}},
}

// pingHTTPURL fills in the mode's scheme when the site is a bare host; a full
// URL passes through untouched, whatever its scheme.
func pingHTTPURL(site, scheme string) string {
	if strings.Contains(site, "://") {
		return site
	}
	return scheme + "://" + site
}

// pingHTTP times one HTTP GET to the site (gen_204 style). Any HTTP response,
// whatever its status code, is a success: connectivity is what is being
// measured, not the page. Same return convention as pingICMP.
func pingHTTP(site string) (int64, error) {
	return pingHTTPProbe(pingHTTPURL(site, "http"))
}

// pingHTTPS is pingHTTP with https:// as the default scheme, so the probe
// times the TLS handshake as well as the request.
func pingHTTPS(site string) (int64, error) {
	return pingHTTPProbe(pingHTTPURL(site, "https"))
}

func pingHTTPProbe(url string) (int64, error) {
	start := time.Now()
	resp, err := pingHTTPPingClient.Get(url)
	if err != nil {
		if pingErrIsTimeout(err) {
			return -2, nil
		}
		return -1, err
	}
	resp.Body.Close()
	return pingClampMs(time.Since(start)), nil
}
