#!/usr/bin/env bash
# Reversible STA-mode switch experiment for AKASO 360.
# v2: logs to the SD card (persists across power cycles) so we can
# diagnose failures even after a recovery reboot.
#
# Run while joined to the camera's hotspot (AK360_xxxx).
# Recovery from a hang: power-cycle the camera. AP mode returns.

set -u
LOG=/tmp/akaso_sta_switch.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

CAM=192.168.42.1

section() { echo; echo "======== $* ========"; }

section "Pre-flight"
ping -c 1 -W 1000 "$CAM" >/dev/null && echo "Camera reachable" || { echo "Camera unreachable, aborting"; exit 1; }

echo
echo "Enter the credentials of a WPA2-PSK Wi-Fi to test on:"
echo "(Avoid corporate WPA2-Enterprise; phone hotspot or guest network works well.)"
read -p "  SSID: " HOME_SSID
read -sp "  Password: " HOME_PASS
echo; echo
if [ -z "${HOME_SSID}" ] || [ -z "${HOME_PASS}" ]; then
    echo "ERROR: SSID and password are both required."
    exit 1
fi

echo "About to:"
echo "  1. Telnet to ${CAM} as root"
echo "  2. Write /tmp/switch_to_sta.sh on the camera"
echo "  3. Launch it detached"
echo "  4. The script logs to BOTH /tmp/switch_log AND /tmp/SD0/akaso_sta_switch.log"
echo "     (the SD card path persists across reboots — readable after recovery)"
echo
read -p "Proceed? [y/N] " yn
case "$yn" in [Yy]*) ;; *) echo "aborted."; exit 0 ;; esac

section "Connecting via telnet and launching switch"
HOME_SSID="$HOME_SSID" HOME_PASS="$HOME_PASS" python3 - "$CAM" <<'PY'
import os, socket, sys, time

cam = sys.argv[1]
ssid = os.environ["HOME_SSID"]
password = os.environ["HOME_PASS"]

IAC, DONT, WONT, DO, WILL = 0xff, 0xfe, 0xfc, 0xfd, 0xfb

def strip_iac(sock, buf):
    out = bytearray(); i = 0
    while i < len(buf):
        b = buf[i]
        if b == IAC and i+1 < len(buf):
            cmd = buf[i+1]
            if cmd in (DO, DONT, WILL, WONT) and i+2 < len(buf):
                opt = buf[i+2]
                if cmd == DO: sock.send(bytes([IAC, WONT, opt]))
                elif cmd == WILL: sock.send(bytes([IAC, DONT, opt]))
                i += 3; continue
            elif cmd == IAC:
                out.append(IAC); i += 2; continue
            else:
                i += 2; continue
        out.append(b); i += 1
    return bytes(out)

def wait_prompt(sock, timeout=4.0):
    end = time.time() + timeout; buf = b""
    while time.time() < end:
        sock.settimeout(max(0.2, end - time.time()))
        try: chunk = sock.recv(4096)
        except socket.timeout: continue
        if not chunk: break
        chunk = strip_iac(sock, chunk); buf += chunk
        if b"# " in buf[-8:] or b"$ " in buf[-8:] or b"login: " in buf[-12:]:
            return buf.decode("utf-8","replace")
    return buf.decode("utf-8","replace")

def shell_escape_dq(s):
    return s.replace("\\", "\\\\").replace('"', '\\"').replace("$", "\\$").replace("`", "\\`")

# Remote script: dual-log (tmp + SD), aggressive shutdown of AP daemons,
# then wpa_supplicant + udhcpc. If association fails, the log on the SD
# card tells us why (visible after a power-cycle reboot).
remote_script = """#!/bin/sh
SD_LOG=/tmp/SD0/akaso_sta_switch.log
TMP_LOG=/tmp/switch_log
# Ensure log files are fresh.
mkdir -p /tmp/SD0
: > "$SD_LOG" 2>/dev/null
: > "$TMP_LOG"

LOG () {
    local msg="$*"
    echo "$msg" >> "$TMP_LOG"
    echo "$msg" >> "$SD_LOG" 2>/dev/null
}

LOG "=== switch_to_sta.sh starting at $(date) ==="
LOG "target SSID: __SSID__"

LOG "killing AP-mode daemons..."
killall hostapd 2>/dev/null
killall dnsmasq 2>/dev/null
killall hostapd_cli 2>/dev/null
killall udhcpc 2>/dev/null
killall wpa_supplicant 2>/dev/null
sleep 2
# Clean up stale ctrl_interface socket so wpa_supplicant won't refuse to start
rm -rf /var/run/wpa_supplicant 2>/dev/null
rm -rf /var/run/hostapd 2>/dev/null

LOG "writing /tmp/wpa_supplicant.conf..."
cat > /tmp/wpa_supplicant.conf << 'WPAEOF'
ctrl_interface=/var/run/wpa_supplicant
network={
ssid="__SSID__"
scan_ssid=1
key_mgmt=WPA-PSK
psk="__PASSWORD__"
}
WPAEOF

LOG "wpa_supplicant.conf written. interface state:"
ifconfig wlan0 >> "$TMP_LOG" 2>&1
ifconfig wlan0 >> "$SD_LOG" 2>&1
ifconfig wlan0 down
sleep 1
ifconfig wlan0 up
sleep 1

LOG "starting wpa_supplicant..."
wpa_supplicant -D nl80211 -iwlan0 -c /tmp/wpa_supplicant.conf -B >> "$TMP_LOG" 2>&1
wpa_supplicant -D nl80211 -iwlan0 -c /tmp/wpa_supplicant.conf -B >> "$SD_LOG" 2>&1 || true

LOG "waiting 8s for association..."
sleep 8

LOG "wpa_cli status after association attempt:"
wpa_cli -i wlan0 status >> "$TMP_LOG" 2>&1
wpa_cli -i wlan0 status >> "$SD_LOG" 2>&1

LOG "running DHCP client..."
udhcpc -i wlan0 -A 5 -b -t 8 -T 2 >> "$TMP_LOG" 2>&1 &
sleep 6

LOG "=== final wlan0 state ==="
ifconfig wlan0 >> "$TMP_LOG" 2>&1
ifconfig wlan0 >> "$SD_LOG" 2>&1
LOG "=== final routing ==="
route -n >> "$TMP_LOG" 2>&1
route -n >> "$SD_LOG" 2>&1
LOG "=== wpa_cli scan_results ==="
wpa_cli -i wlan0 scan_results >> "$TMP_LOG" 2>&1
wpa_cli -i wlan0 scan_results >> "$SD_LOG" 2>&1
LOG "=== switch_to_sta.sh done at $(date) ==="
sync
""".replace("__SSID__", shell_escape_dq(ssid)).replace("__PASSWORD__", shell_escape_dq(password))

s = socket.create_connection((cam, 23), timeout=5)
banner = wait_prompt(s, timeout=3)
print(banner.strip()[-200:])
s.send(b"root\r\n")
print(wait_prompt(s, timeout=3).strip()[-200:])

SENTINEL = "AKASOEND_X9Z"
print("\nwriting /tmp/switch_to_sta.sh on the camera...")
s.send(("cat > /tmp/switch_to_sta.sh << '%s'\n" % SENTINEL).encode())
s.send(remote_script.encode())
s.send(("\n%s\n" % SENTINEL).encode())
print(wait_prompt(s, timeout=4).strip()[-200:])

s.send(b"chmod +x /tmp/switch_to_sta.sh\n")
print(wait_prompt(s, timeout=2).strip()[-100:])

s.send(b"ls -l /tmp/switch_to_sta.sh\n")
print(wait_prompt(s, timeout=2).strip()[-200:])

# Make sure the SD card mount point is good before we lose the AP.
s.send(b"ls -la /tmp/SD0/ | head -5\n")
print(wait_prompt(s, timeout=2).strip()[-300:])

print("\nlaunching switch script detached...")
s.send(b"nohup setsid sh /tmp/switch_to_sta.sh > /dev/null 2>&1 < /dev/null &\n")
time.sleep(1.5)
# Don't bother waiting for output; the AP is likely going down NOW.

s.send(b"exit\n")
time.sleep(0.5)
s.close()
print("\ntelnet disconnected. switch script continues running on the camera.")
PY

section "Done"
echo
echo "  Next steps:"
echo "  1. The camera should disappear from your Wi-Fi list within ~20 seconds"
echo "     (AK360_xxxx network goes away while wpa_supplicant runs)."
echo "  2. Switch your Mac's Wi-Fi to the SAME guest/test network you specified above."
echo "  3. Run: bash /tmp/akaso_find_camera.sh"
echo
echo "  If find_camera fails:"
echo "    - power-cycle the camera (AP mode returns)"
echo "    - re-join AK360_xxxx"
echo "    - bash /tmp/akaso_read_switchlog.sh"
echo "      ^^ reads the SD-card log to see exactly why the switch failed."
