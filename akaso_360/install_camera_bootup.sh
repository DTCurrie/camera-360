#!/usr/bin/env bash
# One-shot installer: pushes akaso_360/camera-bootup.sh to the AKASO
# 360's SD card via telnet, so it runs automatically at every boot.
#
# Usage:
#   1. Power on the camera, join its Wi-Fi AP from this machine. The
#      AP SSID is printed on the camera (format AK360_xxxx) and the
#      password is under the SSID label.
#   2. Run this script. It uses the camera's AP IP (192.168.42.1) by
#      default; override with --host if you've put the camera on a
#      different network.
#
#   ./akaso_360/install_camera_bootup.sh
#   ./akaso_360/install_camera_bootup.sh --host 192.168.42.1
#   ./akaso_360/install_camera_bootup.sh --dry-run
#
# Effect after a power-cycle: camera presents itself as a USB Ethernet
# gadget at 10.42.0.1, and its RTSP server (AmbaRTSPServer) serves
# streams over that link without requiring an associated Wi-Fi client.
#
# Reversible: delete /tmp/SD0/bootup.sh on the camera and power-cycle
# to restore factory behavior.
#
# Requires: bash, python3 (for the telnet loop), the bootup.sh artifact
# alongside this script (akaso_360/camera-bootup.sh).

set -u

HOST="192.168.42.1"
DRY_RUN=0

usage() {
    sed -n '2,/^set -u/p' "$0" | sed 's/^# \{0,1\}//' | head -n 30 | sed -n '2,$p'
    exit 1
}

while [ $# -gt 0 ]; do
    case "$1" in
        --host) HOST="$2"; shift 2;;
        --dry-run) DRY_RUN=1; shift;;
        -h|--help) usage;;
        *) echo "unknown arg: $1"; usage;;
    esac
done

# Resolve script directory so it works no matter the user's cwd.
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BOOTUP="$SCRIPT_DIR/camera-bootup.sh"

[ -f "$BOOTUP" ] || { echo "FAIL: $BOOTUP not found"; exit 1; }

section() { echo; echo "==== $* ===="; }

section "Pre-flight"
ping -c 1 -W 2 "$HOST" >/dev/null 2>&1 || {
    echo "FAIL: $HOST unreachable. Are you joined to the camera's Wi-Fi AP?"
    exit 1
}
echo "Camera reachable at $HOST"

if [ "$DRY_RUN" = "1" ]; then
    echo
    echo "DRY RUN: would upload $BOOTUP ($(wc -c < "$BOOTUP") bytes) to $HOST:/tmp/SD0/bootup.sh"
    exit 0
fi

section "Uploading $BOOTUP to camera at $HOST:/tmp/SD0/bootup.sh"

# Marker unlikely to appear in the file content. We assert this below.
MARKER="EOF_BOOTUP_$(date +%s)_$$"
if grep -qF "$MARKER" "$BOOTUP"; then
    echo "FAIL: marker collision (extremely unlikely)"; exit 1
fi

python3 - "$HOST" "$BOOTUP" "$MARKER" <<'PY'
import socket, sys, time, hashlib
host, path, marker = sys.argv[1], sys.argv[2], sys.argv[3]
content = open(path, "rb").read()

IAC = 0xff
def strip(sock, buf):
    out = bytearray(); i = 0
    while i < len(buf):
        b = buf[i]
        if b == IAC and i+2 < len(buf):
            c = buf[i+1]
            if c in (0xfd, 0xfe, 0xfb, 0xfc):
                sock.send(bytes([IAC, 0xfc if c == 0xfd else 0xfe, buf[i+2]]))
                i += 3; continue
        out.append(b); i += 1
    return bytes(out)

def waitp(s, t=5):
    end = time.time() + t
    buf = b""
    while time.time() < end:
        s.settimeout(max(0.2, end - time.time()))
        try:
            c = s.recv(8192)
        except socket.timeout:
            continue
        if not c: break
        buf += strip(s, c)
        if b"# " in buf[-8:] or b"login: " in buf[-12:]:
            return buf.decode(errors="replace")
    return buf.decode(errors="replace")

def cmd(s, c, t=5):
    s.send((c + "\n").encode())
    return waitp(s, t)

s = socket.create_connection((host, 23), timeout=5)
waitp(s, 3)
s.send(b"root\r\n")
waitp(s, 3)

# Quickly verify /tmp/SD0 is present (SD card mounted).
out = cmd(s, "test -d /tmp/SD0 && echo OK || echo NO_SD")
if "NO_SD" in out:
    print("FAIL: SD card not present at /tmp/SD0. Insert an SD card and retry.")
    sys.exit(2)

# Verify the S99bootdone hook exists.
out = cmd(s, "test -f /etc/init.d/S99bootdone && echo HOOK_OK || echo HOOK_MISSING")
if "HOOK_MISSING" in out:
    print("WARN: /etc/init.d/S99bootdone is missing on this firmware.")
    print("      bootup.sh will be uploaded but won't auto-run at boot.")

# Clear any prior copy and write fresh.
cmd(s, "rm -f /tmp/SD0/bootup.sh /tmp/SD0/bootup.log")

s.send(f"cat > /tmp/SD0/bootup.sh <<'{marker}'\n".encode())
chunk = 1024
for i in range(0, len(content), chunk):
    s.send(content[i:i+chunk])
    time.sleep(0.02)
if not content.endswith(b"\n"):
    s.send(b"\n")
s.send(f"{marker}\n".encode())
waitp(s, 6)

cmd(s, "chmod +x /tmp/SD0/bootup.sh")
out = cmd(s, "sh -n /tmp/SD0/bootup.sh && echo SYNTAX_OK || echo SYNTAX_BAD")
if "SYNTAX_BAD" in out:
    print("FAIL: bootup.sh has a syntax error on the camera.")
    print(out)
    sys.exit(3)

# Size sanity-check.
out = cmd(s, "wc -c /tmp/SD0/bootup.sh")
print(out.strip())
print(f"local size:  {len(content)} bytes")

# Optional integrity check via md5sum.
out = cmd(s, "md5sum /tmp/SD0/bootup.sh")
print(out.strip())
print(f"local md5:   {hashlib.md5(content).hexdigest()}")

s.send(b"exit\n")
time.sleep(0.3)
s.close()
PY

section "Done"
cat <<EOF

Camera-side install complete. To activate:

  1. Power-cycle the camera (hold power button until off, then back on).
  2. Wait ~20 seconds for the camera's boot + bootup.sh to finish.
  3. Plug a USB-C cable from your host (Pi, Mac, etc.) to the camera.

To verify on the host side once the camera is plugged in, see:
  README.md > Host-side setup
EOF
