#!/usr/bin/env bash
# Deep probe (prong 1: exhaustive enumeration) for the j5create JVCU360, hunting
# for a RAW fisheye channel the firmware may expose but not present as one of the
# six touch-bar "modes". Run on the LINUX Pi (the test rig) — macOS blocks XU
# access and synthesizes phantom sizes over avfoundation, so its output lies.
#
#   bash jvcu360/probe_raw.sh 2>&1 | tee /tmp/jvcu360_probe.log
#
# Then paste /tmp/jvcu360_probe.log back. We are looking for, in priority order:
#   1. a vendor-specific INTERFACE (bInterfaceClass ff) or an alt setting with an
#      endpoint that isn't accounted for by the UVC VideoStreaming interface
#      — the ONLY finding that would justify a non-UVC (libusb) driver;
#   2. a STILL image frame descriptor or an undocumented square/portrait/circular
#      frame size (≈1:1) — a likely raw-sensor render;
#   3. the mode control's GET_MAX > the 6th mode's value — hidden XU states to
#      walk in prong 2.
#
# Set the camera to "360 All-Around" on the touch bar before running, so the
# baseline format/geometry is known.
set -uo pipefail

VID=0711 PID=0360
say() { printf '\n========== %s ==========\n' "$1"; }

say "PROBE ENV"
date
uname -a
command -v lsusb v4l2-ctl ffprobe go 2>/dev/null || true
# v4l2-ctl ships in v4l-utils: sudo apt install -y v4l-utils usbutils

say "USB: locate device ${VID}:${PID}"
lsusb -d ${VID}:${PID} || { echo "device ${VID}:${PID} not found on USB — is it plugged into THIS host?"; }
busdev=$(lsusb -d ${VID}:${PID} | sed -E 's/Bus ([0-9]+) Device ([0-9]+).*/\1 \2/' | head -1)
echo "bus/dev: ${busdev:-<none>}"

say "USB: FULL verbose descriptor (interfaces, alt settings, endpoints, XUs)"
# The key read: scan bInterfaceClass for 'ff' (vendor-specific) and any alternate
# setting whose endpoints aren't the standard VideoStreaming bulk/iso EP.
sudo lsusb -v -d ${VID}:${PID} 2>/dev/null || lsusb -v -d ${VID}:${PID}

say "USB: interface-class / alt-setting / endpoint summary (quick scan)"
sudo lsusb -v -d ${VID}:${PID} 2>/dev/null | grep -E \
  'bInterfaceNumber|bAlternateSetting|bInterfaceClass|bInterfaceSubClass|bEndpointAddress|Transfer Type|wMaxPacketSize|bNumEndpoints' \
  || echo "(need sudo for full verbose dump; re-run with sudo)"

say "V4L2: device node map for this camera"
v4l2-ctl --list-devices 2>/dev/null || echo "v4l2-ctl missing (apt install v4l-utils)"

# Resolve every /dev/video* node and probe each — a raw stream can hide on a
# secondary node of the same physical device.
nodes=$(for n in /dev/video*; do [ -e "$n" ] && echo "$n"; done)
for dev in $nodes; do
  say "V4L2: $dev --info"
  v4l2-ctl -d "$dev" --info 2>/dev/null
  say "V4L2: $dev --list-formats-ext   (EVERY format/size/fps, not just the 6 modes)"
  v4l2-ctl -d "$dev" --list-formats-ext 2>/dev/null
  say "V4L2: $dev --all   (current fmt, controls, XU-derived ctrls if mapped)"
  v4l2-ctl -d "$dev" --all 2>/dev/null
done

here=$(cd "$(dirname "$0")" && pwd)

say "XU: full Extension-Unit dump (INFO/LEN/CUR/MIN/MAX/DEF for every candidate)"
echo "# MAX on the control that selects the mode is the tell: if MAX > the 6th"
echo "# mode's CUR value, there are hidden states beyond the touch-bar modes."
if python3 -c 'import usb.core' 2>/dev/null && [ -f "$here/uvc_extension_probe.py" ]; then
  sudo python3 "$here/uvc_extension_probe.py" 2>&1 || python3 "$here/uvc_extension_probe.py" 2>&1 || true
else
  echo "skipped: needs pyusb (sudo pip3 install pyusb + apt install libusb-1.0-0)"
  echo "and uvc_extension_probe.py next to this script."
fi

say "OPTIONAL: parsed descriptor view (needs pyusb+libusb; skip if not present)"
if python3 -c 'import usb.core' 2>/dev/null && [ -f "$here/uvc_descriptors.py" ]; then
  sudo python3 "$here/uvc_descriptors.py" 2>&1 || python3 "$here/uvc_descriptors.py" 2>&1 || true
else
  echo "skipped — lsusb -v above already has the same data"
fi

say "DONE — paste /tmp/jvcu360_probe.log back"
