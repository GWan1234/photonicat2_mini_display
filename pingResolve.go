package main

// Transparent-proxy escape for the two LCD ping rows.
//
// OpenClash & friends running in fake-ip mode answer every DNS query made by
// the router itself with an address out of 198.18.0.0/15. Those addresses only
// exist inside the proxy's TCP hairpin: nothing routes them, and the proxy's
// own ruleset rejects whatever else is aimed at the pool, so a raw ICMP
// sendto() to one fails instantly with EPERM ("operation not permitted").
// pingICMP counted that as a failed probe, so both ping rows sat on a red X
// while the user's traffic was flowing perfectly well.
//
// The escape is to resolve the row's hostname somewhere the proxy has not
// poisoned. Plain DNS is not that place - UDP and TCP :53 are both redirected
// into the proxy's own resolver - so the lookup goes over HTTPS to a resolver
// addressed by literal IP. Unlike the web app's WAN probe (wifi_wan.py), this
// socket is not bound to a netdev, so it needs no gid-65534 bypass: if the
// proxy redirects the connection it simply carries it upstream, and a genuine
// answer comes back either way.
//
// Everything here is gated on an address inside the fake-ip pool, which cannot
// happen without such a proxy. On every other device pingTarget resolves the
// host exactly once - the same lookup ping.NewPinger would have done itself -
// and hands the result straight over.

import (
	"crypto/tls"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// pingFakeIPNet is the range clash/mihomo hands out in fake-ip mode. Its
// default pool is 198.18.0.1/16; the /15 is the whole RFC 2544 benchmarking
// block it sits in, which is what the pool is configured out of.
var pingFakeIPNet = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("198.18.0.0/15")
	return n
}()

// pingDoHURLs are addressed by literal IP on purpose: a hostname here would go
// straight back through the resolver we are trying to get away from. Both
// endpoints serve the JSON lookup form and carry their own address in the
// certificate's SAN list, so TLS still verifies normally. Two of them so one
// blocked resolver cannot wedge the rows.
// (var, not const, so tests can point them at a local server.)
var pingDoHURLs = []string{
	"https://223.5.5.5/resolve",
	"https://1.1.1.1/dns-query",
}

// pingDoHClient is separate from secureHTTPClient because that one's 10s
// ceiling is longer than a probe's entire deadline (pingProbeDeadline). Two
// endpoints at this timeout still fit inside it, so a probe that has to escape
// can finish within the tick that started it.
var pingDoHClient = &http.Client{
	Timeout: 1500 * time.Millisecond,
	Transport: &uaTransport{base: &http.Transport{
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: time.Second,
	}},
}

// pingLookupIP is the system-resolver lookup. It deliberately makes the same
// call ping.NewPinger makes internally, so the address family we hand back is
// the one the pinger would have picked for itself.
// (var so tests can drive the fake-ip branch without a proxy on the machine.)
var pingLookupIP = func(host string) (string, error) {
	addr, err := net.ResolveIPAddr("ip", host)
	if err != nil {
		return "", err
	}
	return addr.String(), nil
}

// pingEscapeTTL bounds how long one DoH answer is reused. The rows probe at
// 1 Hz while their page is on screen, so without a cache a poisoned device
// would open a DoH connection every second - and a blocked one would retry
// every second. A ping row does not care that its target IP is a minute stale.
const pingEscapeTTL = 60 * time.Second

type pingEscape struct {
	addr string // "" when no real address could be found
	at   time.Time
}

var (
	pingEscapeMu    sync.Mutex
	pingEscapeCache = map[string]pingEscape{}
)

func pingEscapeCached(host string) (string, bool) {
	pingEscapeMu.Lock()
	defer pingEscapeMu.Unlock()
	e, ok := pingEscapeCache[host]
	if !ok || time.Since(e.at) > pingEscapeTTL {
		return "", false
	}
	return e.addr, true
}

func pingEscapeStore(host, addr string) {
	pingEscapeMu.Lock()
	defer pingEscapeMu.Unlock()
	pingEscapeCache[host] = pingEscape{addr: addr, at: time.Now()}
}

// pingIsFakeIP reports whether addr is one of the proxy's synthetic addresses.
func pingIsFakeIP(addr string) bool {
	ip := net.ParseIP(addr)
	return ip != nil && pingFakeIPNet.Contains(ip)
}

// pingTarget returns the address pingICMP should actually probe for host.
// Without a fake-ip proxy in the way this is just the host's own address; with
// one it is the real address behind the poisoned answer, or the hostname
// unchanged when even that cannot be found (which fails exactly as it does
// today rather than inventing a target).
func pingTarget(host string) string {
	if host == "" || net.ParseIP(host) != nil {
		// An IP literal never touches the resolver, so it cannot be poisoned -
		// which is also the standing workaround for this whole problem.
		return host
	}

	addr, err := pingLookupIP(host)
	if err != nil {
		// A resolver that is down is not a fake-ip problem, and papering over
		// it here would only hide it. Let the pinger fail as it always has.
		return host
	}
	if !pingIsFakeIP(addr) {
		return addr
	}

	if cached, ok := pingEscapeCached(host); ok {
		if cached == "" {
			return host
		}
		return cached
	}

	real := pingResolveDoH(host)
	// Stored either way: an empty answer is a result too, and re-running a
	// blocked lookup on every tick is exactly what the cache is here to stop.
	pingEscapeStore(host, real)
	if real == "" {
		log.Printf("ping: %s resolves into the fake-ip pool (%s) and no DoH resolver answered; row will show a failure", host, addr)
		return host
	}
	log.Printf("ping: %s resolved to fake-ip %s, probing %s instead", host, addr, real)
	return real
}

// pingResolveDoH asks each DoH endpoint in turn for host's A record and
// returns the first real address one of them gives up ("" if none do).
func pingResolveDoH(host string) string {
	for _, endpoint := range pingDoHURLs {
		if addr := pingQueryDoH(endpoint, host); addr != "" {
			return addr
		}
	}
	return ""
}

// pingQueryDoH runs one JSON-form DoH query. Anything unexpected - transport
// error, non-200, unparseable body, an answer that is itself a fake-ip -
// yields "" so the caller moves on to the next resolver.
func pingQueryDoH(endpoint, host string) string {
	req, err := http.NewRequest("GET", endpoint+"?type=A&name="+url.QueryEscape(host), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/dns-json")

	resp, err := pingDoHClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var body struct {
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	for _, a := range body.Answer {
		if a.Type != 1 { // 1 = A; CNAMEs in the chain are not targets
			continue
		}
		ip := net.ParseIP(a.Data)
		if ip == nil || ip.To4() == nil || pingIsFakeIP(a.Data) {
			continue
		}
		return a.Data
	}
	return ""
}
