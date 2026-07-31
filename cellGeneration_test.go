package main

import "testing"

// The top bar used to read dashboard.json's "carrier" alone. On an RM500Q-GL
// that field reads "Other" — the modem answers AT+QNWINFO with a bogus
// "CDMA","46001","CDMA BC0" record while actually camped on 5G, so the web
// side's cell_tech (and the carrier built from it) degrade to "Other" even
// though the dashboard page shows 5G from modem_mode. The result was a top bar
// with no signal icon at all on 500Q boxes.
func TestCellGeneration(t *testing.T) {
	tests := []struct {
		name      string
		modemMode string
		carrier   string
		want      string
	}{
		{"rm500q reports 5G via modem_mode while carrier is Other", "5G", "Other", "5"},
		{"rm500q on LTE with poisoned carrier", "4G", "Other", "4"},
		{"honest modem agrees on both fields", "5G", "5G", "5"},
		{"carrier carries it when modem_mode is empty", "", "4G", "4"},
		{"3G aliases", "WCDMA", "", "3"},
		{"lte alias is normalized", "LTE", "", "4"},
		{"nr alias is normalized", "nr5g", "", "5"},
		{"whitespace and case are tolerated", " 5g ", "", "5"},
		{"both unusable yields no generation", "Other", "Other", ""},
		{"both empty yields no generation", "", "", ""},
		{"2G is not a drawable generation", "2G", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cellGeneration(tc.modemMode, tc.carrier); got != tc.want {
				t.Errorf("cellGeneration(%q, %q) = %q, want %q",
					tc.modemMode, tc.carrier, got, tc.want)
			}
		})
	}
}

// A mobile egress must draw signal bars whether or not the generation is known,
// so "c" (cellular, generation unknown) counts as cellular alongside 5/4/3.
func TestIsCellular(t *testing.T) {
	for _, s := range []string{"5", "4", "3", "c"} {
		if !isCellular(s) {
			t.Errorf("isCellular(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "w", "i", "u", "2"} {
		if isCellular(s) {
			t.Errorf("isCellular(%q) = true, want false", s)
		}
	}
}
