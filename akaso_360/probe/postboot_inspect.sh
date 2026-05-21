#!/usr/bin/env bash
# Join the camera AP briefly, telnet to 192.168.42.1, inspect post-boot
# state (did bootup.sh run? did usb0 come up? is the shim in place?),
# then return to HOST_SSID.
#
# Environment:
#   CAM_AP_SSID  (required) - the camera's Wi-Fi SSID (AK360_xxxx)
#   CAM_AP_PASS  (required) - the camera's Wi-Fi password
#   HOST_SSID    (required) - your normal Wi-Fi to restore at the end

set -u
: "${CAM_AP_SSID:?required: set CAM_AP_SSID=AK360_xxxx}"
: "${CAM_AP_PASS:?required: set CAM_AP_PASS=...}"
: "${HOST_SSID:?required: set HOST_SSID=YourWiFi}"

LOG=/tmp/akaso_postboot_inspect.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

AP_SSID="${CAM_AP_SSID}"
AP_PASS="${CAM_AP_PASS}"
OFFICE_SSID="${HOST_SSID}"
CAM_AP=192.168.42.1

restore_office() {
    echo
    echo "======== RESTORE ========"
    /System/Library/PrivateFrameworks/Apple80211.framework/Versions/Current/Resources/airport -z 2>/dev/null
    sleep 1
    networksetup -setairportnetwork en0 "${OFFICE_SSID}" >/dev/null 2>&1 || true
    sleep 4
    ifconfig en0 | grep 'inet ' || true
}
trap restore_office EXIT

echo "joining ${AP_SSID}..."
networksetup -setairportpower en0 on; sleep 1
networksetup -setairportnetwork en0 "${AP_SSID}" "${AP_PASS}"
for i in 1 2 3 4 5 6 7 8 9 10; do
    AP_IP=$(ifconfig en0 | awk '/inet 192.168.42/ {print $2}')
    [ -n "${AP_IP}" ] && break
    sleep 1
done
[ -z "${AP_IP:-}" ] && { echo "FAIL: AP IP not assigned"; exit 1; }
echo "on AP: ${AP_IP}"
ping -c 1 -W 1000 "${CAM_AP}" >/dev/null || { echo "FAIL: camera unreachable on AP"; exit 1; }

echo
echo "===== INSPECT POST-BOOT STATE ====="
python3 - "${CAM_AP}" <<'PY'
import socket, sys, time
cam = sys.argv[1]
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
def waitp(s,t=5):
    end=time.time()+t; buf=b""
    while time.time()<end:
        s.settimeout(max(0.2,end-time.time()))
        try: c=s.recv(8192)
        except socket.timeout: continue
        if not c: break
        buf+=strip(s,c)
        if b"# " in buf[-8:] or b"login: " in buf[-12:]: return buf.decode(errors="replace")
    return buf.decode(errors="replace")
def cmd(s,c,t=6):
    s.send((c+"\n").encode()); return waitp(s,t)

s=socket.create_connection((cam,23),timeout=5)
waitp(s,3); s.send(b"root\r\n"); waitp(s,3)

print("\n### 1. did S99bootdone exec our bootup.sh? ###")
print(cmd(s, "cat /etc/init.d/S99bootdone"))

print("\n### 2. is /tmp/SD0/bootup.sh present + executable? ###")
print(cmd(s, "ls -la /tmp/SD0/bootup.sh"))

print("\n### 3. does the log show it ran? ###")
print(cmd(s, "ls -la /tmp/SD0/bootup.log 2>/dev/null; echo ---; cat /tmp/SD0/bootup.log 2>/dev/null"))

print("\n### 4. is usb0 up? what's its state? ###")
print(cmd(s, "ifconfig usb0 2>&1 | head -5"))
print(cmd(s, "ls -la /sys/class/net/"))

print("\n### 5. is the iw shim in place? ###")
print(cmd(s, "mount | grep iw"))
print(cmd(s, "iw dev wlan0 station dump | grep Station; echo rc=$?"))

print("\n### 6. is g_ether loaded? ###")
print(cmd(s, "lsmod | grep -E 'g_ether|usb_f_|libcomposite'"))

print("\n### 7. uptime + ps ###")
print(cmd(s, "uptime; ps | grep -v grep | grep -E 'AmbaRTSPServer|S99|bootup'"))

s.send(b"exit\n"); time.sleep(0.3); s.close()
PY
