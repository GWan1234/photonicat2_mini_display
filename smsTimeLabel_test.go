package main

import (
	"strings"
	"testing"
	"time"

	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
)

func TestSmsTimeLabel(t *testing.T) {
	// Friday 2026-07-31, 10:00 local.
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.Local)

	tests := []struct {
		name  string
		msgAt time.Time
		want  string
	}{
		{"earlier today", time.Date(2026, 7, 31, 8, 5, 0, 0, time.Local), "08:05"},
		{"yesterday", time.Date(2026, 7, 30, 14, 32, 0, 0, time.Local), "1d ago 14:32"},
		{"three days ago", time.Date(2026, 7, 28, 9, 0, 0, 0, time.Local), "3d ago 09:00"},
		{"six days ago", time.Date(2026, 7, 25, 23, 15, 0, 0, time.Local), "6d ago 23:15"},
		{"a week ago drops to a date", time.Date(2026, 7, 24, 7, 45, 0, 0, time.Local), "07-24 07:45"},
		{"last year keeps the year", time.Date(2025, 12, 30, 23, 59, 0, 0, time.Local), "2025-12-30 23:59"},
		// A modem clock that is briefly ahead should not print a negative or
		// nonsense age.
		{"stamped in the future", time.Date(2026, 8, 1, 6, 0, 0, 0, time.Local), "06:00"},
	}

	for _, tt := range tests {
		if got := smsTimeLabel(tt.msgAt, now); got != tt.want {
			t.Errorf("%s: smsTimeLabel() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// Yesterday must stay "Yesterday" even when a DST change makes the two local
// midnights 23 or 25 hours apart.
func TestSmsTimeLabelAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	// 2026-03-08 is the spring-forward day there: the 7th->8th gap is 23h.
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, loc)
	msgAt := time.Date(2026, 3, 7, 20, 0, 0, 0, loc)
	if got := smsTimeLabel(msgAt, now); got != "1d ago 20:00" {
		t.Errorf("smsTimeLabel() across DST = %q, want %q", got, "1d ago 20:00")
	}
}

func TestFitSenderWidth(t *testing.T) {
	drawer := &font.Drawer{Face: basicfont.Face7x13} // 7px per character
	width := func(s string) int { return int(drawer.MeasureString(s) >> 6) }

	if got := fitSenderWidth("10086", drawer, 100); got != "10086" {
		t.Errorf("short sender was altered: %q", got)
	}

	// "+8613800138000" is 14 chars = 98px; squeeze it into 70px.
	got := fitSenderWidth("+8613800138000", drawer, 70)
	if width(got) > 70 {
		t.Errorf("fitted sender %q is %dpx, want <= 70", got, width(got))
	}
	if !strings.HasPrefix(got, "+861") || !strings.HasSuffix(got, "**00") {
		t.Errorf("fitted sender %q should keep the head and the last two digits", got)
	}

	// A CJK sender must not be cut mid-rune.
	if got := fitSenderWidth("中国移动通信客服", drawer, 30); !isValidUTF8Runes(got) {
		t.Errorf("CJK sender was cut mid-rune: %q", got)
	}

	if got := fitSenderWidth("anything", drawer, 0); got != "" {
		t.Errorf("no room should yield an empty sender, got %q", got)
	}
}

func isValidUTF8Runes(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// The title row is 172px wide at an 11px face. Check the real font's widths so
// the longest everyday label ("1d ago 14:32") still leaves a usable sender
// column - the labels are wider than the "Y-day" they replaced.
func TestSmsTitleWidthsWithRealFont(t *testing.T) {
	for _, prefix := range []string{".", "/usr/share/pcat2_mini_display", ".."} {
		assetsPrefix = prefix
		if getSmsFont() != nil {
			break
		}
	}
	fnt := getSmsFont()
	if fnt == nil {
		t.Skip("SMS font asset not available")
	}
	drawer := &font.Drawer{Face: truetype.NewFace(fnt, &truetype.Options{
		Size: 11.0, DPI: 72, Hinting: font.HintingFull,
	})}
	width := func(s string) int { return int(drawer.MeasureString(s) >> 6) }

	const rowWidth, xStart = 172, 4
	rightMargin := rowWidth - xStart - 5

	for _, label := range []string{"14:32", "1d ago 14:32", "6d ago 09:00", "07-24 07:45"} {
		room := rightMargin - width(label) - xStart - senderGap
		if room < 8*width("0") {
			t.Errorf("label %q leaves %dpx for the sender, under 8 digits", label, room)
		}
	}

	// The longest label (an old message with a year) may squeeze the sender, but
	// the fitter must still keep it inside its column rather than overlapping.
	label := "2025-12-30 23:59"
	room := rightMargin - width(label) - xStart - senderGap
	fitted := fitSenderWidth("+8613800138000", drawer, room)
	if width(fitted) > room {
		t.Errorf("sender %q is %dpx, over the %dpx left by %q", fitted, width(fitted), room, label)
	}
	t.Logf("year-label case leaves %dpx -> sender %q", room, fitted)
}
