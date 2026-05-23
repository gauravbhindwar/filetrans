#!/usr/bin/env bash
# Sets up USB gadget mode (g_ether) on Linux using configfs.
# Run as root. Tested on Ubuntu 22.04+, Fedora 38+.
set -euo pipefail

GADGET_DIR="/sys/kernel/config/usb_gadget/filetrans"
VENDOR_ID="0x1d6b"   # Linux Foundation
PRODUCT_ID="0x0104"  # Multifunction Composite Gadget

check_udc() {
    UDC=$(ls /sys/class/udc/ 2>/dev/null | head -1)
    if [ -z "$UDC" ]; then
        echo "ERROR: No USB Device Controller found in /sys/class/udc/"
        echo "       Your USB-C port may be host-only. Fallback to Ethernet/Wi-Fi required."
        exit 1
    fi
    echo "UDC found: $UDC"
}

teardown_gadget() {
    if [ -d "$GADGET_DIR" ]; then
        echo "" > "$GADGET_DIR/UDC" 2>/dev/null || true
        rm -rf "$GADGET_DIR"
        echo "Removed existing gadget config."
    fi
}

setup_gadget() {
    modprobe libcomposite

    mkdir -p "$GADGET_DIR"
    echo "$VENDOR_ID" > "$GADGET_DIR/idVendor"
    echo "$PRODUCT_ID" > "$GADGET_DIR/idProduct"
    echo "0x0200"     > "$GADGET_DIR/bcdUSB"

    mkdir -p "$GADGET_DIR/strings/0x409"
    echo "filetrans"  > "$GADGET_DIR/strings/0x409/manufacturer"
    echo "FileTransUSB" > "$GADGET_DIR/strings/0x409/product"
    echo "FT000001"   > "$GADGET_DIR/strings/0x409/serialnumber"

    mkdir -p "$GADGET_DIR/functions/ecm.usb0"
    # Fixed MACs so Windows caches the driver binding
    echo "12:34:56:78:9a:bc" > "$GADGET_DIR/functions/ecm.usb0/host_addr"
    echo "12:34:56:78:9a:bd" > "$GADGET_DIR/functions/ecm.usb0/dev_addr"

    mkdir -p "$GADGET_DIR/configs/c.1/strings/0x409"
    echo "FileTransConfig" > "$GADGET_DIR/configs/c.1/strings/0x409/configuration"
    echo 250 > "$GADGET_DIR/configs/c.1/MaxPower"

    ln -sf "$GADGET_DIR/functions/ecm.usb0" "$GADGET_DIR/configs/c.1/"

    echo "$UDC" > "$GADGET_DIR/UDC"
    echo "Gadget bound to UDC: $UDC"
}

configure_ip() {
    # Wait for usb0 to appear after gadget bind
    for i in $(seq 1 10); do
        if ip link show usb0 &>/dev/null; then break; fi
        sleep 0.5
    done

    ip addr flush dev usb0 2>/dev/null || true
    ip addr add 192.168.7.1/24 dev usb0
    ip link set usb0 up
    echo "Configured 192.168.7.1/24 on usb0"
}

echo "=== filetrans USB Gadget Setup ==="
[ "$EUID" -ne 0 ] && { echo "Run as root."; exit 1; }

check_udc
teardown_gadget
setup_gadget
configure_ip

echo ""
echo "Done. usb0 is UP at 192.168.7.1/24"
echo "Connect Windows host — it will get 192.168.7.2 via RNDIS."
