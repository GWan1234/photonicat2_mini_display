//go:build linux

package main

// Linux-only half of the PMU direct-UART path. The termios ioctls and the
// Bxxx baud constants below only exist on Linux, so keeping them here lets the
// rest of the package — and therefore the whole test suite — build and run on
// a macOS development host.

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

var pmuBaudConstants = map[uint32]uint32{
	9600:    unix.B9600,
	19200:   unix.B19200,
	38400:   unix.B38400,
	57600:   unix.B57600,
	115200:  unix.B115200,
	230400:  unix.B230400,
	460800:  unix.B460800,
	921600:  unix.B921600,
	1500000: unix.B1500000,
}

// pmuOpenSerial opens the PMU UART raw at 8N1 and claims it exclusively
// (flock + TIOCEXCL) so nothing else can grab it while we own it.
func pmuOpenSerial(dev string, baud uint32) (*os.File, error) {
	baudConst, ok := pmuBaudConstants[baud]
	if !ok {
		baudConst = unix.B115200
	}

	f, err := os.OpenFile(dev, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd())

	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("port is locked by another process: %w", err)
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCEXCL, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("TIOCEXCL: %w", err)
	}

	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("TCGETS: %w", err)
	}
	// raw 8N1, no flow control (cfmakeraw equivalent)
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON | unix.IXOFF
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB | unix.CSTOPB | unix.CRTSCTS | unix.CBAUD
	t.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL | baudConst
	t.Ispeed = baudConst
	t.Ospeed = baudConst
	// blocking reads with a 200 ms timeout so the reader loop can notice closes
	t.Cc[unix.VMIN] = 0
	t.Cc[unix.VTIME] = 2
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, t); err != nil {
		f.Close()
		return nil, fmt.Errorf("TCSETS: %w", err)
	}
	unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIOFLUSH)

	return f, nil
}
