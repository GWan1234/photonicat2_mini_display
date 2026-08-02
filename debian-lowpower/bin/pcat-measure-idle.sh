#!/bin/sh
# SPDX-License-Identifier: GPL-2.0-or-later
#
# pcat-measure-idle.sh - report what is drawing power on a photonicat2
# right now, using the MCU's own battery/charger telemetry plus the usual
# Linux PM introspection. Run this BEFORE and AFTER changes to see the
# effect, and to decide whether you even need deeper measures.
#
# The MCU reports battery voltage/current and charger voltage; on battery,
# current_now (uA) * voltage_now (uV) gives instantaneous draw of the
# whole board after the 5V buck -- the single most useful number here.
#
set -u

hr() { echo "------------------------------------------------------------"; }

echo "photonicat2 idle/power snapshot"
hr

# --- MCU battery telemetry (via kernel power_supply, if driver loaded) ----
PS=/sys/class/power_supply
if [ -d "$PS/battery" ]; then
    echo "[battery / board draw, from MCU]"
    v=$(cat "$PS/battery/voltage_now" 2>/dev/null)   # uV
    i=$(cat "$PS/battery/current_now" 2>/dev/null)   # uA (neg = discharging)
    st=$(cat "$PS/battery/status" 2>/dev/null)
    cap=$(cat "$PS/battery/capacity" 2>/dev/null)
    echo "  status=$st capacity=${cap}%"
    [ -n "${v:-}" ] && echo "  voltage_now=$((v/1000)) mV"
    if [ -n "${i:-}" ] && [ -n "${v:-}" ]; then
        # power = |i| * v ; uA*uV = pW -> /1e9 -> mW
        ai=${i#-}
        mw=$(( (ai/1000) * (v/1000) / 1000 ))
        echo "  current_now=$((i/1000)) mA  => approx board power ${mw} mW"
    fi
    [ -d "$PS/charger" ] && echo "  charger online=$(cat $PS/charger/online 2>/dev/null)"
    hr
else
    echo "[no /sys/class/power_supply/battery -- photonicat-pm driver not loaded]"
    echo "  (use ./pcat-mcu.py heartbeat to confirm the MCU link on /dev/ttyS4)"
    hr
fi

# --- CPU idle / frequency ------------------------------------------------
echo "[cpu]"
for p in /sys/devices/system/cpu/cpufreq/policy*; do
    [ -d "$p" ] || continue
    g=$(cat "$p/scaling_governor" 2>/dev/null)
    f=$(cat "$p/scaling_cur_freq" 2>/dev/null)
    echo "  $(basename $p): governor=$g cur=$((${f:-0}/1000)) MHz"
done
echo "  load:$(cut -d' ' -f1-3 /proc/loadavg)"
hr

# --- Radios --------------------------------------------------------------
echo "[radios]"
command -v rfkill >/dev/null 2>&1 && rfkill list 2>/dev/null | sed 's/^/  /'
if command -v mmcli >/dev/null 2>&1; then
    echo "  modems: $(mmcli -L 2>/dev/null | grep -c Modem)"
fi
hr

# --- Devices NOT runtime-suspended (these keep rails up) -----------------
echo "[devices still active (runtime_status=active)]"
n=0
for s in /sys/devices/platform/*/power/runtime_status \
         /sys/bus/*/devices/*/power/runtime_status; do
    [ -e "$s" ] || continue
    if [ "$(cat "$s" 2>/dev/null)" = "active" ]; then
        d=$(dirname "$(dirname "$s")")
        echo "  $(basename "$d")"
        n=$((n+1))
    fi
done
echo "  ($n active devices)"
hr

# --- Backlight -----------------------------------------------------------
echo "[backlight]"
for b in /sys/class/backlight/*; do
    [ -d "$b" ] || continue
    echo "  $(basename "$b"): brightness=$(cat $b/brightness 2>/dev/null) bl_power=$(cat $b/bl_power 2>/dev/null)"
done
hr
echo "Tip: lowest current = ./pcat-lowpower-off.sh (MCU cuts 5V; wake on button/DCIN)."
