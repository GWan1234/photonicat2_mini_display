# photonicat2 (RK3576) low-power sleep for Debian

Goal: put the photonicat2 to "sleep" drawing **very little current**, waking on
**button / DC-in**.

## The key finding

On this board the lowest-current state is **not Linux suspend-to-RAM**. It is a
clean power-off handed to the on-board **power-management MCU** (the
`photonicat-pm` device on `/dev/ttyS4`).

| Mode | What stays powered | Rough draw | Wake |
|------|--------------------|-----------|------|
| Linux suspend-to-RAM (`mem`) | DDR self-refresh + S3 rails (`vcc_3v3_s3`, `vcc_1v8_s3`, …) | ~150–400 mW | instant, but RK3576 mainline `mem` is flaky |
| **MCU power-off (this tool)** | only the MCU | **single-digit mA** | button / DC-in, cold-boot ~10–20 s |

The MCU cuts `VCC_5V0_SYS` entirely. The DTS confirms several rails are kept on
in suspend (`regulator-on-in-suspend`), which is why s2r can't get truly low —
the MCU path bypasses all of that.

## How it works

`pcat-lowpower-off` tears down the big consumers, then hands off:

1. **Modem (FM350)** – `mmcli --disable`, then RFKill (`gpiochip4 20=0`) and
   power-enable off (`gpiochip4 18=0`). (Lines come from `pcat-manager.conf` /
   `fm350-init` in the OpenWrt tree.)
2. **WiFi/BT** – `rfkill block all`, ifdown, unload `ath12k_pci`/`ath12k`.
3. **Display** – backlight brightness 0 + `bl_power` powerdown.
4. **USB** – set all devices to runtime `auto`.
5. **sync**, then **hand off to MCU**:
   - If the kernel `photonicat-pm` driver is loaded, a plain `systemctl poweroff`
     already triggers its `sys_off` handler, which sends
     `HOST_REQUEST_SHUTDOWN` (0x0F) to the MCU and drops the power GPIO. We use
     that path.
   - On a stock Debian kernel **without** that driver, `pcat-mcu.py` speaks the
     exact same serial frame protocol to `/dev/ttyS4` itself, waits for the ACK
     (0x10), then halts so the MCU can cut power.

`pcat-mcu.py` reimplements the driver's framing precisely (CRC-16/MODBUS, the
`A5 … 5A` envelope). Verified against the kernel `pcat_pm_compute_crc16` and the
`123456789 -> 0x4B37` reference vector.

## Install (on the device)

```sh
sudo ./install.sh
```

Installs:
- `/usr/local/bin/pcat-lowpower-off` — go low-power now
- `/usr/local/bin/pcat-measure-idle` — see what's drawing power (run before/after)
- `/usr/local/bin/pcat-mcu.py` — raw MCU control (`poweroff`, `heartbeat`)
- `pcat-lowpower.service` — optional systemd unit to bind to a button

## Use

```sh
sudo pcat-measure-idle      # baseline
sudo pcat-lowpower-off      # board powers down to MCU standby
# press the power button (or apply DC-in) to wake -> cold boot
```

## Notes / next steps

- **Confirm the MCU link first**: `sudo pcat-mcu.py heartbeat` (no error = good).
- **Verify the modem GPIO lines** on your unit with `gpioinfo gpiochip4`
  (line 18 = modem power, line 20 = RFKill) before trusting the teardown.
- **RTC scheduled wake** (`CMD_SCHEDULE_STARTUP_TIME_SET`, 0x0B) exists in the
  protocol but its payload is firmware-specific; left unimplemented because you
  chose button/DC-in wake. Ask if you want it added.
- This does **not** change any hardware settings — it only sequences existing
  power rails down and uses the vendor MCU's own shutdown command.
