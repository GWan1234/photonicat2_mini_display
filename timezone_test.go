package main

// The top-bar clock resolves its own timezone rather than trusting time.Local
// (which can latch to UTC if the daemon starts before OpenWrt writes
// /tmp/localtime). parsePosixTZ is the OpenWrt half of that; a wrong sign here
// puts the clock hours off in the wrong direction, which is the exact failure
// the hand-rolled resolver exists to prevent.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParsePosixTZ(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantOffset int // seconds east of UTC
		wantName   string
	}{
		// POSIX counts hours WEST of UTC, so the sign inverts.
		{"china", "CST-8", 8 * 3600, "CST"},
		{"us_pacific_std_only", "PST8", -8 * 3600, "PST"},
		{"us_pacific_with_dst_rules", "PST8PDT,M3.2.0,M11.1.0", -8 * 3600, "PST"},
		{"utc_bare", "UTC", 0, "UTC"},
		{"gmt_bare", "GMT", 0, "UTC"},
		{"utc_with_zero", "UTC0", 0, "UTC"},
		{"half_hour_east", "<+0630>-6:30", 6*3600 + 30*60, "+0630"},
		{"half_hour_west", "<-0330>3:30", -(3*3600 + 30*60), "-0330"},
		{"india", "IST-5:30", 5*3600 + 30*60, "IST"},
		{"explicit_plus_is_west", "XXX+5", -5 * 3600, "XXX"},
		{"newfoundland", "NST3:30NDT", -(3*3600 + 30*60), "NST"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc := parsePosixTZ(tt.in)
			if loc == nil {
				t.Fatalf("parsePosixTZ(%q) = nil, want a location", tt.in)
			}
			// Use a fixed instant; these are all fixed zones so DST never applies.
			ref := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
			name, offset := ref.In(loc).Zone()
			if offset != tt.wantOffset {
				t.Errorf("parsePosixTZ(%q) offset = %d s (UTC%+g), want %d s (UTC%+g)",
					tt.in, offset, float64(offset)/3600, tt.wantOffset, float64(tt.wantOffset)/3600)
			}
			if name != tt.wantName {
				t.Errorf("parsePosixTZ(%q) zone name = %q, want %q", tt.in, name, tt.wantName)
			}
		})
	}
}

// Anything unparseable must return nil so the caller falls through to the next
// source — and ultimately draws "--:--" rather than a confidently wrong hour.
func TestParsePosixTZRejectsGarbage(t *testing.T) {
	bad := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"unterminated_angle", "<+0630"},
		{"empty_angle_name", "<>-5"},
		{"leading_digits_no_name", "8"},
		{"bare_unknown_zone", "CST"}, // no offset digits and not UTC/GMT
		{"hours_out_of_range", "XX-25"},
		{"minutes_out_of_range", "XX-5:99"},
		{"punctuation_only", ",,,"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			if loc := parsePosixTZ(tt.in); loc != nil {
				ref := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
				name, off := ref.In(loc).Zone()
				t.Errorf("parsePosixTZ(%q) = %q (offset %d), want nil", tt.in, name, off)
			}
		})
	}
}

// A bare zone name only resolves when it is unambiguous. "CST" alone is
// ambiguous (China vs US Central), so it must NOT be guessed at.
func TestParsePosixTZBareNameOnlyUTCAndGMT(t *testing.T) {
	for _, ok := range []string{"UTC", "GMT"} {
		if parsePosixTZ(ok) == nil {
			t.Errorf("parsePosixTZ(%q) = nil, want time.UTC", ok)
		}
	}
	for _, bad := range []string{"CST", "EST", "JST", "BST"} {
		if loc := parsePosixTZ(bad); loc != nil {
			t.Errorf("parsePosixTZ(%q) = %v, want nil — ambiguous bare names must not be guessed", bad, loc)
		}
	}
}

// displayNow reports ok=false until a timezone resolves; the clock renders
// "--:--" in that window rather than a wrong time.
func TestDisplayNowUnresolvedReportsNotOK(t *testing.T) {
	saved := displayLoc.Load()
	defer displayLoc.Store(saved)

	displayLoc.Store(nil)
	if _, ok := displayNow(); ok {
		t.Error("displayNow reported ok with no resolved timezone")
	}

	tokyo := time.FixedZone("JST", 9*3600)
	displayLoc.Store(tokyo)
	got, ok := displayNow()
	if !ok {
		t.Fatal("displayNow reported not-ok after a timezone was published")
	}
	if _, off := got.Zone(); off != 9*3600 {
		t.Errorf("displayNow returned offset %d, want %d", off, 9*3600)
	}
}

// resolveTimezone prefers the TZif file, then /etc/TZ, then $TZ. Exercise the
// $TZ tail of that chain, which is the only one addressable without root.
func TestResolveTimezoneFallsBackToTZEnv(t *testing.T) {
	if _, err := os.Stat("/etc/localtime"); err == nil {
		t.Skip("/etc/localtime exists on this host; the TZif path wins and shadows $TZ")
	}
	if _, err := os.Stat("/etc/TZ"); err == nil {
		t.Skip("/etc/TZ exists on this host and takes precedence over $TZ")
	}
	t.Setenv("TZ", "CST-8")

	loc := resolveTimezone()
	if loc == nil {
		t.Fatal("resolveTimezone returned nil with $TZ set")
	}
	ref := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	if _, off := ref.In(loc).Zone(); off != 8*3600 {
		t.Errorf("resolveTimezone offset = %d, want %d", off, 8*3600)
	}
}

// The TZif branch must accept real zoneinfo bytes — this is the path used on
// Debian and on OpenWrt images that ship zoneinfo, and the only one that gets
// DST transitions right.
func TestResolveTimezoneParsesTZifData(t *testing.T) {
	// Find a zoneinfo file on the host to use as realistic TZif input.
	var data []byte
	for _, p := range []string{
		"/usr/share/zoneinfo/Asia/Shanghai",
		"/var/db/timezone/zoneinfo/Asia/Shanghai",
	} {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			data = b
			break
		}
	}
	if data == nil {
		t.Skip("no zoneinfo database on this host")
	}

	loc, err := time.LoadLocationFromTZData("local", data)
	if err != nil {
		t.Fatalf("LoadLocationFromTZData rejected real zoneinfo: %v", err)
	}
	ref := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	if _, off := ref.In(loc).Zone(); off != 8*3600 {
		t.Errorf("Asia/Shanghai offset = %d, want %d", off, 8*3600)
	}

	// And confirm a truncated/garbage TZif is rejected rather than yielding a
	// bogus zone — resolveTimezone relies on that error to fall through.
	tmp := filepath.Join(t.TempDir(), "bad")
	if err := os.WriteFile(tmp, []byte("not a tzfile"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(tmp)
	if _, err := time.LoadLocationFromTZData("local", b); err == nil {
		t.Error("LoadLocationFromTZData accepted garbage as TZif data")
	}
}
