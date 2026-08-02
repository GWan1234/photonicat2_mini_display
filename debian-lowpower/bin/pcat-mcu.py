#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-or-later
#
# pcat-mcu.py - userspace control of the photonicat2 power-management MCU
#
# Speaks the exact serial frame protocol implemented by the kernel
# photonicat-pm driver (drivers/staging/photonicat-pm/photonicat-pm.c).
# Use this on Debian / any rootfs when you do NOT have the kernel driver
# loaded, so you can still hand the board off to the MCU for a true
# low-current power-off (MCU cuts VCC_5V0_SYS; wakes on button / DCIN).
#
# Frame layout (little-endian), matching pcat_pm_uart_write_data():
#   A5 <src=01> <dst=81> <frame_lo> <frame_hi>
#   <len_lo> <len_hi>            # len = payload(=extra)+3
#   <cmd_lo> <cmd_hi> [extra...] # command id + optional extra bytes
#   <need_ack>                   # 1 or 0
#   <crc_lo> <crc_hi>            # CRC16/MODBUS over bytes [1 .. len+6]
#   5A
#
# CRC: CRC-16/MODBUS (init 0xFFFF, poly 0xA001, reflected) over the frame
# starting at offset 1 (the src byte) through the need_ack byte inclusive
# == (3 header + 2 frame + 2 len + len) bytes == "dp_size + 6".
#
import sys, os, time, struct, argparse, termios, fcntl

# --- command ids (mirror PCatPMCommandType in the kernel driver) ----------
CMD_HEARTBEAT                = 0x01
CMD_SCHEDULE_STARTUP_TIME_SET= 0x0B
CMD_HOST_REQUEST_SHUTDOWN    = 0x0F
CMD_HOST_REQUEST_SHUTDOWN_ACK= 0x10
CMD_WATCHDOG_TIMEOUT_SET     = 0x13

SRC = 0x01      # host
DST = 0x81      # PMU (matches driver's outgoing dst byte)

def crc16_modbus(data: bytes) -> int:
    crc = 0xFFFF
    for b in data:
        crc ^= b
        for _ in range(8):
            if crc & 1:
                crc = (crc >> 1) ^ 0xA001
            else:
                crc >>= 1
    return crc & 0xFFFF

_framenum = 0
def build_frame(cmd: int, extra: bytes = b"", need_ack: bool = False) -> bytes:
    global _framenum
    body = bytearray()
    body += bytes([0xA5, SRC, DST])
    body += struct.pack("<H", _framenum & 0xFFFF)
    _framenum = (_framenum + 1) & 0xFFFF
    dp_size = len(extra) + 3
    body += struct.pack("<H", dp_size)
    body += struct.pack("<H", cmd)
    body += extra
    body += bytes([1 if need_ack else 0])
    # CRC over body[1 : 1 + (dp_size + 6)] == src .. need_ack inclusive
    crc = crc16_modbus(bytes(body[1:1 + dp_size + 6]))
    body += struct.pack("<H", crc)
    body += bytes([0x5A])
    return bytes(body)

def open_serial(dev: str, baud: int):
    fd = os.open(dev, os.O_RDWR | os.O_NOCTTY | os.O_NONBLOCK)
    attrs = termios.tcgetattr(fd)
    iflag, oflag, cflag, lflag, ispeed, ospeed, cc = attrs
    speed = getattr(termios, "B%d" % baud, termios.B115200)
    # raw 8N1, no flow control
    iflag = 0
    oflag = 0
    cflag = (cflag & ~termios.CSIZE) | termios.CS8
    cflag |= (termios.CLOCAL | termios.CREAD)
    cflag &= ~(termios.PARENB | termios.PARODD | termios.CSTOPB | termios.CRTSCTS)
    lflag = 0
    cc[termios.VMIN] = 0
    cc[termios.VTIME] = 1
    termios.tcsetattr(fd, termios.TCSANOW, [iflag, oflag, cflag, lflag, speed, speed, cc])
    # blocking reads from here on
    fl = fcntl.fcntl(fd, fcntl.F_GETFL)
    fcntl.fcntl(fd, fcntl.F_SETFL, fl & ~os.O_NONBLOCK)
    return fd

def find_shutdown_ack(buf: bytearray) -> bool:
    # minimal scan for a frame whose command == HOST_REQUEST_SHUTDOWN_ACK
    i = 0
    while i + 13 <= len(buf):
        if buf[i] != 0xA5:
            i += 1
            continue
        expect_len = buf[i+5] | (buf[i+6] << 8)
        if expect_len < 3 or expect_len > 515 or i + 10 + expect_len > len(buf):
            i += 1
            continue
        if buf[i + 9 + expect_len] != 0x5A:
            i += 1
            continue
        cmd = buf[i+7] | (buf[i+8] << 8)
        if cmd == CMD_HOST_REQUEST_SHUTDOWN_ACK:
            return True
        i += 10 + expect_len
    return False

def request_shutdown(fd, timeout=5.0) -> bool:
    frame = build_frame(CMD_HOST_REQUEST_SHUTDOWN, need_ack=True)
    os.write(fd, frame)
    deadline = time.time() + timeout
    buf = bytearray()
    while time.time() < deadline:
        try:
            chunk = os.read(fd, 256)
        except BlockingIOError:
            chunk = b""
        if chunk:
            buf += chunk
            if find_shutdown_ack(buf):
                return True
        else:
            time.sleep(0.05)
        # resend periodically like the driver retries
    return False

def set_startup_time(fd, seconds_from_now: int):
    # Schedule an RTC wake N seconds from now. Payload format is the
    # MCU's date-time struct; only use this if your MCU firmware supports
    # CMD_SCHEDULE_STARTUP_TIME_SET. Disabled by default.
    raise NotImplementedError(
        "RTC scheduled wake payload is firmware-specific; "
        "use button/DCIN wake (the default) instead.")

def main():
    ap = argparse.ArgumentParser(description="photonicat2 MCU control")
    ap.add_argument("--dev", default="/dev/ttyS4")
    ap.add_argument("--baud", type=int, default=115200)
    ap.add_argument("action", choices=["poweroff", "heartbeat"])
    ap.add_argument("--timeout", type=float, default=5.0)
    args = ap.parse_args()

    fd = open_serial(args.dev, args.baud)
    try:
        if args.action == "heartbeat":
            os.write(fd, build_frame(CMD_HEARTBEAT))
            print("heartbeat sent")
        elif args.action == "poweroff":
            ok = request_shutdown(fd, args.timeout)
            if ok:
                print("MCU acknowledged shutdown request")
                sys.exit(0)
            else:
                print("WARNING: no shutdown ACK from MCU within %.1fs" % args.timeout,
                      file=sys.stderr)
                sys.exit(1)
    finally:
        os.close(fd)

if __name__ == "__main__":
    main()
