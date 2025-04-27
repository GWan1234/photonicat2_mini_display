#!/bin/bash

SERVICE_NAME=pcat2_mini_display
BINARY_PATH=/usr/local/bin/$SERVICE_NAME
SERVICE_FILE=/etc/systemd/system/$SERVICE_NAME.service

# Ensure running as root
if [[ $EUID -ne 0 ]]; then
   echo "Please run as root or with sudo."
   exit 1
fi

echo "Building Go binary..."
#./compile.sh

if [ $? -ne 0 ]; then
  echo "Build failed. Exiting."
  exit 1
fi

echo "Installing binary to $BINARY_PATH..."
systemctl stop $SERVICE_NAME
cp pcat2_mini_display_debian $BINARY_PATH
cp config.json /etc/pcat2_mini_display-config.json
mkdir -p /usr/local/share/pcat2_mini_display
cp -ar assets /usr/local/share/pcat2_mini_display
chmod +x $BINARY_PATH

echo "Reloading systemd daemon and starting service..."
cp service-files/$SERVICE_NAME.service $SERVICE_FILE
systemctl daemon-reload
systemctl enable --now $SERVICE_NAME

echo "Installation complete. Service status:"
systemctl status $SERVICE_NAME --no-pager
