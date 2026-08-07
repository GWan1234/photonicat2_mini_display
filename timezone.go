package main

// timezone.go - the top-bar clock's timezone, resolved by hand.
//
// Go caches time.Local on first use, and this daemon can start before the
// OpenWrt boot has written /tmp/localtime (or /tmp/TZ): with unlucky boot
// order time.Local locks to UTC for the whole process lifetime and the clock
// shows the wrong hour. The clock therefore never trusts time.Local: a keeper
// goroutine polls the system timezone sources and publishes a *time.Location;
// until one resolves the clock draws "--:--" rather than a wrong time, and a
// timezone change from the web UI is picked up within half a minute.

import (
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var displayLoc atomic.Pointer[time.Location]

// Testability seams: the real system timezone sources, reassigned only by
// tests (to t.TempDir() fixtures) so every fallback branch can be exercised.
var (
	utilCovLocaltimePath = "/etc/localtime"
	utilCovPosixTZPaths  = []string{"/etc/TZ", "/tmp/TZ"}
)

// displayNow returns the current time in the resolved display timezone and
// whether the timezone is known yet.
func displayNow() (time.Time, bool) {
	loc := displayLoc.Load()
	if loc == nil {
		return time.Now(), false
	}
	return time.Now().In(loc), true
}

// startTimezoneKeeper polls quickly (2s) until the timezone resolves, then
// re-checks every 30s to follow configuration changes without a restart.
func startTimezoneKeeper() {
	go func() {
		for {
			time.Sleep(utilCovTimezoneKeeperStep())
		}
	}()
}

// utilCovTimezoneKeeperStep runs one resolve/publish pass and returns how long
// the keeper should sleep before the next one. Extracted from the endless
// keeper goroutine so the resolve/publish logic is testable; behaviour is
// unchanged.
func utilCovTimezoneKeeperStep() time.Duration {
	if loc := resolveTimezone(); loc != nil {
		displayLoc.Store(loc)
		return 30 * time.Second
	}
	return 2 * time.Second
}

func resolveTimezone() *time.Location {
	// TZif database file (Debian always; OpenWrt with zoneinfo installed:
	// /etc/localtime -> /tmp/localtime -> /usr/share/zoneinfo/...). Handles
	// DST rules properly, so it is preferred.
	if b, err := os.ReadFile(utilCovLocaltimePath); err == nil && len(b) > 0 {
		if loc, err := time.LoadLocationFromTZData("local", b); err == nil {
			return loc
		}
	}
	// POSIX TZ string (OpenWrt: /etc/TZ -> /tmp/TZ, e.g. "CST-8").
	for _, p := range utilCovPosixTZPaths {
		if b, err := os.ReadFile(p); err == nil {
			if loc := parsePosixTZ(strings.TrimSpace(string(b))); loc != nil {
				return loc
			}
		}
	}
	if tz := os.Getenv("TZ"); tz != "" {
		if loc := parsePosixTZ(tz); loc != nil {
			return loc
		}
	}
	return nil
}

// parsePosixTZ handles the shapes OpenWrt writes: "CST-8", "UTC",
// "<+0630>-6:30", "PST8PDT,M3.2.0,M11.1.0". Only the standard offset is
// used - DST rule evaluation is left to the TZif path above.
func parsePosixTZ(s string) *time.Location {
	if s == "" {
		return nil
	}
	var name string
	i := 0
	if s[0] == '<' {
		j := strings.IndexByte(s, '>')
		if j < 0 {
			return nil
		}
		name, i = s[1:j], j+1
	} else {
		for i < len(s) && ((s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z')) {
			i++
		}
		name = s[:i]
	}
	if name == "" {
		return nil
	}
	sign := 1
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		if s[i] == '-' {
			sign = -1
		}
		i++
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == i {
		// No offset digits: a bare zone name. Only UTC/GMT are unambiguous.
		if name == "UTC" || name == "GMT" {
			return time.UTC
		}
		return nil
	}
	h, _ := strconv.Atoi(s[i:j])
	m := 0
	if j < len(s) && s[j] == ':' {
		k := j + 1
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		m, _ = strconv.Atoi(s[j+1 : k])
	}
	if h > 24 || m > 59 {
		return nil
	}
	// POSIX offsets count hours west of UTC: "CST-8" means UTC+8.
	return time.FixedZone(name, -sign*(h*3600+m*60))
}
