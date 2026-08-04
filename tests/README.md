# Testing the Photonicat2 mini display

All tests live in the **root package** (`package main`, `*_test.go` next to the
code they cover). They used to sit in this directory as `package main` files in
a subdirectory, which meant they referenced root-package symbols they could not
see and so never compiled — `go test ./...` failed for the whole module. They
have been moved into the root package and fixed; this directory now holds only
the on-device runner and this guide.

## Running on a development host

The package builds and tests natively on macOS and Linux. The Linux-only PMU
UART code (termios ioctls, the `unix.Bxxx` baud constants) is isolated behind
build tags in `pcatPmuSerial_linux.go` / `pcatPmuSerial_other.go`, so it no
longer blocks the host build.

```bash
go test ./...                 # whole suite
go test -race ./...           # run before pushing; the suite is race-clean
go test -run TestParsePosixTZ -v .
make test                     # go test ./...
make test-race                # go test -race ./...
```

Coverage:

```bash
go test -coverprofile=coverage.out .
go tool cover -func=coverage.out | tail -1
go tool cover -html=coverage.out -o coverage.html
```

## Running on the device

Some behaviour only exists on real hardware: sysfs battery nodes, DMA channels,
the PMU UART, the actual disk layout. Cross-compile the test binary and run it
on the target:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c -o /tmp/pcat-tests .
scp /tmp/pcat-tests root@<device>:/tmp/
ssh root@<device> '/tmp/pcat-tests -test.v -test.run TestCollectDiskUsage'
```

`run_test_arm_bin.sh` wraps that last step for a device with no Go toolchain:
copy it alongside a binary named `test_runner_openwrt`, `test_runner_debian`
or `test_runner` and run it.

## Conventions

- Tests that mutate package globals (`cfg`, `cfgNumPages`, `idleState`,
  `weAreRunning`, …) must save and restore them via `t.Cleanup`. The suite runs
  in a single process, so a leaked mutation surfaces as an unrelated failure in
  some later test.
- Do not leave production goroutines running past a test. Several loop until
  the shutdown flag clears and poll shared state every second; leaked, they
  race every later test. `TestInitPowerDataRecording` shows the pattern.
- Tests needing pcat-manager-web should point `localHTTPClient` at an
  `httptest.Server` via `redirectLocalHTTP` (`testhelpers_test.go`) instead of
  depending on a live service.
- Tests must not require root, and must skip rather than fail when a host
  facility is missing — see the zoneinfo and `/etc/localtime` skips in
  `timezone_test.go`.
