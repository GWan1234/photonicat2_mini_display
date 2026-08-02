#!/bin/sh
# SPDX-License-Identifier: GPL-2.0-or-later
#
# pcat-lowpower-off.sh - bring a photonicat2 (RK3576) to its lowest-current
# state on Debian (or any non-OpenWrt rootfs).
#
# Strategy (chosen for "very little current", button/DCIN wake):
#   The deepest practical low-power state on this board is NOT Linux
#   suspend-to-RAM. RK3576 s2r keeps DDR self-refresh + several S3 rails
#   (vcc_3v3_s3, vcc_1v8_s3, ...) energised => ~150-400 mW, and mainline
#   ATF/PSCI "mem" on rk3576 is unreliable. Instead we do a clean power-off
#   handed to the on-board MCU, which cuts VCC_5V0_SYS entirely. Standby
#   current is then just the MCU's (single-digit mA). The MCU re-powers the
#   SoC on a button press or DC-in event -> board cold-boots (~10-20 s).
#
# Before handing off we tear down the largest consumers so the shutdown is
# clean and nothing re-asserts power on the way down.
#
# Run as root:  pcat-lowpower-off.sh
#
set -u

log() { echo "[pcat-lowpower] $*"; }

# --- 1. Cellular modem (FM350): RF-kill, then cut its power rail ----------
# gpiochip4 line 18 = modem power enable, line 20 = modem RFKill
# (active-low) -- from pcat-manager.conf and fm350-init.
modem_down() {
    log "powering down cellular modem"
    # Graceful first: AT+CFUN=0 detaches from the network and powers the RF
    # down cleanly. This mirrors the vendor's modem-shutdown init script,
    # which sends AT+CFUN=0 to /dev/ttyUSB2 before shutdown.
    for at in /dev/ttyUSB2 /dev/ttyUSB3; do
        [ -c "$at" ] || continue
        printf 'AT+CFUN=0\r\n' > "$at" 2>/dev/null && { sleep 2; break; }
    done
    # Then ask ModemManager to disable cleanly if present
    if command -v mmcli >/dev/null 2>&1; then
        for m in $(mmcli -L 2>/dev/null | sed -n 's#.*/Modem/\([0-9]\+\).*#\1#p'); do
            mmcli -m "$m" --disable >/dev/null 2>&1
        done
    fi
    # Finally assert RFKill (active low -> 0 = killed) and drop power enable.
    # Verify the lines on your unit with `gpioinfo gpiochip4` first:
    # line 18 = modem power enable, line 20 = RFKill (from pcat-manager.conf).
    if command -v gpioset >/dev/null 2>&1; then
        gpioset gpiochip4 20=0 >/dev/null 2>&1   # RFKill on
        sleep 1
        gpioset gpiochip4 18=0 >/dev/null 2>&1   # power enable off
    fi
}

# --- 2. WiFi / Bluetooth radios -----------------------------------------
radios_down() {
    log "powering down wifi/bluetooth"
    command -v rfkill >/dev/null 2>&1 && rfkill block all 2>/dev/null
    for i in /sys/class/net/wlan* /sys/class/net/phy*; do
        [ -e "$i" ] || continue
        ifn=$(basename "$i")
        ip link set "$ifn" down 2>/dev/null
    done
    # Unload ath12k so the PCIe WLAN card is fully quiesced
    for mod in ath12k_pci ath12k; do
        lsmod 2>/dev/null | grep -q "^$mod " && rmmod "$mod" 2>/dev/null
    done
}

# --- 3. Display / backlight ----------------------------------------------
display_down() {
    log "blanking display / backlight"
    for bl in /sys/class/backlight/*/brightness; do
        [ -e "$bl" ] && echo 0 > "$bl" 2>/dev/null
    done
    for bl in /sys/class/backlight/*/bl_power; do
        [ -e "$bl" ] && echo 4 > "$bl" 2>/dev/null   # FB_BLANK_POWERDOWN
    done
}

# --- 4. USB peripherals: let them autosuspend / unbind hub ---------------
usb_down() {
    log "suspending USB"
    for f in /sys/bus/usb/devices/*/power/control; do
        [ -e "$f" ] && echo auto > "$f" 2>/dev/null
    done
}

# --- 5. Flush filesystems ------------------------------------------------
fs_sync() {
    log "syncing filesystems"
    sync
    sync
}

# --- 6. Hand off to the MCU for true power cut ----------------------------
mcu_poweroff() {
    DEV=/dev/ttyS4
    HELPER="$(dirname "$0")/pcat-mcu.py"

    # Preferred path: the kernel photonicat-pm driver registers a sys_off
    # handler that already speaks to the MCU on `poweroff`. If that driver
    # is loaded, a plain systemd poweroff is enough and is the cleanest.
    if [ -e /dev/pcat-pm-ctl ] || grep -q photonicat-pm /proc/modules 2>/dev/null; then
        log "kernel photonicat-pm present: using systemd poweroff (driver hands off to MCU)"
        fs_sync
        exec systemctl poweroff
    fi

    # Fallback: no kernel driver -> talk to the MCU ourselves, then halt.
    if [ -c "$DEV" ] && [ -x "$HELPER" ] && command -v python3 >/dev/null 2>&1; then
        log "requesting MCU shutdown over $DEV"
        if python3 "$HELPER" --dev "$DEV" poweroff; then
            log "MCU acknowledged; halting kernel (MCU will cut power)"
            fs_sync
            # halt so we stop executing; MCU removes VCC_5V0_SYS shortly after
            exec systemctl halt 2>/dev/null || halt -f
        else
            log "MCU did not ACK -- falling back to plain poweroff"
        fi
    else
        log "MCU helper/device unavailable -- falling back to plain poweroff"
    fi

    fs_sync
    exec systemctl poweroff 2>/dev/null || poweroff -f
}

if [ "$(id -u)" != "0" ]; then
    echo "must run as root" >&2
    exit 1
fi

modem_down
radios_down
display_down
usb_down
mcu_poweroff
