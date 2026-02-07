#!/bin/bash

# SRE Collector Bare-Metal Uninstaller

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

echo -e "${RED}🗑️ Uninstalling SRE Collector...${NC}"

# 1. Stop and disable service
echo -e "⏹️ Stopping and disabling service..."
sudo systemctl stop sre-collector || true
sudo systemctl disable sre-collector || true

# 2. Remove systemd service file
echo -e "🧹 Removing systemd service file..."
sudo rm -f /etc/systemd/system/sre-collector.service
sudo systemctl daemon-reload

# 3. Remove binary
echo -e "🧹 Removing binary..."
sudo rm -f /usr/local/bin/sre-collector

# 4. Remove config (optional, ask user?)
echo -e "⚠️  The configuration in /etc/sre-collector has been kept."
echo -e "To remove it, run: ${RED}sudo rm -rf /etc/sre-collector${NC}"

echo -e "\n${GREEN}✅ SRE Collector uninstalled successfully.${NC}"
