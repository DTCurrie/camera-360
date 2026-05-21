#!/usr/bin/env bash
# Definitive test of the "AP-associated client unlocks RTSP-over-USB"
# hypothesis. macOS auto-roams off the camera AP to your usual Wi-Fi
# whenever it can, so all steps happen in one script with minimal delay.
#
# Always restores HOST_SSID at the end (success or fail).
#
# Environment:
#   CAM_AP_SSID  (required) - the camera's Wi-Fi SSID, e.g. AK360_xxxx
#                              (printed on the camera label)
#   CAM_AP_PASS  (required) - the camera's Wi-Fi password
#                              (printed under the SSID on the camera label)
#   HOST_SSID    (required) - your normal Wi-Fi (the one you want to
#                              return to after the test)

set -u
: "${CAM_AP_SSID:?required: set CAM_AP_SSID=AK360_xxxx (printed on camera label)}"
: "${CAM_AP_PASS:?required: set CAM_AP_PASS=... (password printed on camera label)}"
: "${HOST_SSID:?required: set HOST_SSID=YourWiFi (network to restore at end)}"

LOG=/tmp/akaso_ap_gate_test.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

AP_SSID="${CAM_AP_SSID}"
AP_PASS="${CAM_AP_PASS}"
OFFICE_SSID="${HOST_SSID}"
CAM_USB=10.42.0.1
CAM_AP=192.168.42.1

restore_office() {
    echo
    echo "======== RESTORE: rejoining ${OFFICE_SSID} ========"
    networksetup -setairportpower en0 off; sleep 1
    networksetup -setairportpower en0 on;  sleep 2
    networksetup -setairportnetwork en0 "${OFFICE_SSID}" >/dev/null 2>&1 || true
    sleep 4
    ifconfig en0 | grep 'inet ' || echo "(no IP)"
}
trap restore_office EXIT

section() { echo; echo "======== $* ========"; }

section "STEP 1: prerequisites"
ifconfig en6 | grep 'inet ' || { echo "FAIL: USB Ethernet en6 not up. Re-run Phase A first."; exit 1; }
ping -c 1 -W 1000 "${CAM_USB}" >/dev/null || { echo "FAIL: USB camera ${CAM_USB} unreachable. Re-run Phase A."; exit 1; }
echo "USB Ethernet OK. en6 -> ${CAM_USB} reachable."

section "STEP 2: join the camera AP fresh"
networksetup -setairportpower en0 on; sleep 1
# Force-disjoin from anything else first
networksetup -setairportnetwork en0 "${AP_SSID}" "${AP_PASS}"
echo "waiting for DHCP on AP..."
for i in 1 2 3 4 5 6 7 8 9 10; do
    AP_IP=$(ifconfig en0 | awk '/inet 192.168.42/ {print $2}')
    [ -n "${AP_IP}" ] && break
    sleep 1
done
if [ -z "${AP_IP:-}" ]; then
    echo "FAIL: did not get a 192.168.42.x IP on en0 within 10s"
    ifconfig en0 | grep 'inet '
    exit 1
fi
echo "en0 IP on AP: ${AP_IP}"

section "STEP 3: confirm camera sees us as an AP station"
python3 - <<'PY'
import socket, time
def telnet_cmd(cmds):
    IAC=0xff
    def strip(sock,buf):
        out=bytearray(); i=0
        while i<len(buf):
            b=buf[i]
            if b==IAC and i+2<len(buf):
                c=buf[i+1]
                if c in (0xfd,0xfe,0xfb,0xfc):
                    sock.send(bytes([IAC, 0xfc if c==0xfd else 0xfe, buf[i+2]]))
                    i+=3; continue
            out.append(b); i+=1
        return bytes(out)
    def waitp(s,t=3):
        end=time.time()+t; buf=b""
        while time.time()<end:
            s.settimeout(max(0.2,end-time.time()))
            try: c=s.recv(4096)
            except socket.timeout: continue
            if not c: break
            buf+=strip(s,c)
            if b"# " in buf[-8:] or b"login: " in buf[-12:]: return buf.decode(errors="replace")
        return buf.decode(errors="replace")
    s=socket.create_connection(("10.42.0.1",23),timeout=5)
    waitp(s,3); s.send(b"root\r\n"); waitp(s,3)
    for c in cmds:
        s.send((c+"\n").encode()); print(waitp(s,4))
    s.send(b"exit\n"); time.sleep(0.3); s.close()
telnet_cmd([
    "hostapd_cli -i wlan0 all_sta 2>&1 | head -3",
    "iw dev wlan0 station dump 2>&1 | grep -E 'Station|inactive' | head -5",
])
PY

section "STEP 4: AP associated -> run CLI over USB. Expect: 4 JPEGs."
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"
rm -f out/*.jpg
go run ./cmd/cli -host "${CAM_USB}" 2>&1 | tail -8
ls -la out/*.jpg 2>/dev/null || echo "no JPEGs written"
JPG_COUNT_WITH_AP=$(ls out/*.jpg 2>/dev/null | wc -l | tr -d ' ')
echo "JPEGs with AP associated: ${JPG_COUNT_WITH_AP}"

section "STEP 5: drop the AP (Wi-Fi off). USB stays."
networksetup -setairportpower en0 off; sleep 2
ifconfig en0 | grep 'inet ' || echo "(en0 has no IP, good)"
echo "verifying USB still reaches camera..."
ping -c 1 -W 1000 "${CAM_USB}" >/dev/null && echo "ping OK" || echo "ping FAILED"

section "STEP 6: AP dropped -> run CLI over USB. Expect: ?"
rm -f out/*.jpg
go run ./cmd/cli -host "${CAM_USB}" 2>&1 | tail -8
ls -la out/*.jpg 2>/dev/null || echo "no JPEGs written"
JPG_COUNT_NO_AP=$(ls out/*.jpg 2>/dev/null | wc -l | tr -d ' ')
echo "JPEGs without AP associated: ${JPG_COUNT_NO_AP}"

section "VERDICT"
echo "with AP:    ${JPG_COUNT_WITH_AP} JPEGs"
echo "without AP: ${JPG_COUNT_NO_AP} JPEGs"
if [ "${JPG_COUNT_WITH_AP}" -ge 4 ] && [ "${JPG_COUNT_NO_AP}" -lt 4 ]; then
    echo "==> CONFIRMED: AP-client association is required for RTSP-over-USB to work."
elif [ "${JPG_COUNT_WITH_AP}" -ge 4 ] && [ "${JPG_COUNT_NO_AP}" -ge 4 ]; then
    echo "==> AP association NOT needed; something else was breaking earlier tests."
else
    echo "==> Inconclusive (with-AP failed too). Look at the log."
fi
