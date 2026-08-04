//go:build !linux

package main

// Portable stub for the PMU direct-UART path. The target is always Linux; this
// file exists purely so the package compiles on a development host (macOS),
// which is what allows `go test ./...` to run there. Nothing calls
// pmuOpenSerial off-device: pmuUartManager is only reached after the device
// tree scan finds a PMU UART node, which cannot happen on a non-Linux host.

import (
	"errors"
	"os"
	"runtime"
)

// pmuBaudConstants is empty off-Linux — the Bxxx termios constants it maps to
// are Linux-only. Kept declared so the rest of the package type-checks.
var pmuBaudConstants = map[uint32]uint32{}

func pmuOpenSerial(dev string, baud uint32) (*os.File, error) {
	return nil, errors.New("PMU direct UART mode is only supported on Linux, not " + runtime.GOOS)
}
