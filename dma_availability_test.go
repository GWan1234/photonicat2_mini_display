package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCheckDMAAvailability verifies that DMA is considered available whenever
// the TX channel is present, regardless of RX. This is a display (write-only)
// driver, so a TX-only DMA setup — as exposed by some device trees — must not
// disable DMA. See checkDMAAvailability.
func TestCheckDMAAvailability(t *testing.T) {
	// touch creates an empty file to stand in for a sysfs dma channel symlink.
	touch := func(t *testing.T, path string) {
		t.Helper()
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("failed to create %s: %v", path, err)
		}
	}

	tests := []struct {
		name    string
		rx      bool
		tx      bool
		wantErr bool
	}{
		{name: "both channels present", rx: true, tx: true, wantErr: false},
		{name: "tx only (RX-missing case from the field log)", rx: false, tx: true, wantErr: false},
		{name: "rx only (no tx: display cannot use DMA)", rx: true, tx: false, wantErr: true},
		{name: "neither channel present", rx: false, tx: false, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.rx {
				touch(t, filepath.Join(dir, "dma:rx"))
			}
			if tt.tx {
				touch(t, filepath.Join(dir, "dma:tx"))
			}

			err := checkDMAAvailabilityAt(dir)
			if tt.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
		})
	}
}
