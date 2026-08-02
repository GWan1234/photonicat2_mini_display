#!/bin/sh
# SPDX-License-Identifier: GPL-2.0-or-later
#
# install.sh - install the photonicat2 low-power helpers on Debian.
# Run as root ON THE DEVICE.
#
set -e
SRC="$(cd "$(dirname "$0")" && pwd)"

if [ "$(id -u)" != "0" ]; then
    echo "run as root" >&2; exit 1
fi

install -m 0755 "$SRC/bin/pcat-mcu.py"          /usr/local/bin/pcat-mcu.py
install -m 0755 "$SRC/bin/pcat-lowpower-off.sh" /usr/local/bin/pcat-lowpower-off
install -m 0755 "$SRC/bin/pcat-measure-idle.sh" /usr/local/bin/pcat-measure-idle

# Optional: a systemd unit + a "lowpower" target you can trigger with
# `systemctl start pcat-lowpower.service` or bind to a button via
# logind / a udev rule.
cat > /etc/systemd/system/pcat-lowpower.service <<'EOF'
[Unit]
Description=photonicat2 low-power off (hand to MCU)
DefaultDependencies=no
Before=shutdown.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/pcat-lowpower-off

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload 2>/dev/null || true

echo "Installed:"
echo "  /usr/local/bin/pcat-mcu.py"
echo "  /usr/local/bin/pcat-lowpower-off   <- run this to sleep at very low current"
echo "  /usr/local/bin/pcat-measure-idle   <- run before/after to see the draw"
echo
echo "To go low-power now:   sudo pcat-lowpower-off"
echo "To measure first:      sudo pcat-measure-idle"
