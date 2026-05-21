#!/usr/bin/env bash
# Install a tmpfs shim over /sbin/iw so that AmbaRTSPServer's gate check
# (`iw dev wlan0 station dump | grep Station`) sees a fake station even
# when no real AP client is associated. Then test that RTSP-over-USB
# works without joining the camera AP.
#
# Strictly REVERSIBLE: everything lives in tmpfs + bind mounts.
# Power-cycle the camera to undo.
#
# Stay on office Wi-Fi the whole time. Talks to camera via USB telnet.

set -u
LOG=/tmp/akaso_iw_shim_install.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

CAM_USB=10.42.0.1

section() { echo; echo "======== $* ========"; }

section "Pre-flight"
ifconfig en0 | grep 'inet ' || true
ifconfig en6 | grep 'inet ' || { echo "FAIL: en6 not up"; exit 1; }
ping -c 1 -W 1000 "${CAM_USB}" >/dev/null || { echo "FAIL: USB unreachable"; exit 1; }
echo "Office Wi-Fi (en0): assumed up. USB (en6) reachable."

section "Install shim on camera over USB telnet"
python3 - "${CAM_USB}" <<'PY'
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
def waitp(s,t=4):
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

# Cleanup from prior runs.
cmd(s, "umount /sbin/iw 2>/dev/null; umount /usr/sbin/iw 2>/dev/null; true")

# 1. Stash a copy of real iw in tmpfs.
print("--- backing up real iw to /tmp/iw.real ---")
print(cmd(s, "cp /sbin/iw /tmp/iw.real && chmod +x /tmp/iw.real && ls -la /tmp/iw.real"))

# 2. Write the shim.
print("--- writing /tmp/iw shim ---")
shim = r'''cat > /tmp/iw <<'SHIM'
#!/bin/sh
# Intercept "dev <iface> station dump": return one fake station so that
# AmbaRTSPServer's gate ("iw dev wlan0 station dump | grep Station") is
# satisfied without a real AP client.
if [ "$1" = "dev" ] && [ "$3" = "station" ] && [ "$4" = "dump" ]; then
    cat <<EOF
Station 02:00:00:00:00:01 (on $2)
	inactive time:	0 ms
	rx bytes:	0
	rx packets:	0
	tx bytes:	0
	tx packets:	0
	signal:  	-50 dBm
	authorized:	yes
	authenticated:	yes
	associated:	yes
EOF
    exit 0
fi
exec /tmp/iw.real "$@"
SHIM
chmod +x /tmp/iw
'''
print(cmd(s, shim, t=4))
print(cmd(s, "ls -la /tmp/iw /tmp/iw.real"))
print(cmd(s, "cat /tmp/iw | head -8"))

# 3. Bind-mount over /sbin/iw (and /usr/sbin/iw for safety).
print("--- bind-mounting shim over /sbin/iw and /usr/sbin/iw ---")
print(cmd(s, "mount --bind /tmp/iw /sbin/iw && echo OK_sbin || echo FAIL_sbin"))
print(cmd(s, "mount --bind /tmp/iw /usr/sbin/iw && echo OK_usr_sbin || echo FAIL_usr_sbin"))
print(cmd(s, "mount | grep iw"))

# 4. Confirm the gate is now flipped from the camera's POV.
print("--- verify the gate is flipped ---")
print(cmd(s, "iw dev wlan0 station dump"))
print(cmd(s, "iw dev wlan0 station dump | grep Station; echo rc=$?"))

# 5. Restart AmbaRTSPServer in case it caches anything (cheap, ~0.5s).
print("--- restart AmbaRTSPServer (defensive — may not be required) ---")
print(cmd(s, "killall -9 AmbaRTSPServer 2>/dev/null; sleep 1; /usr/bin/AmbaRTSPServer >/dev/null 2>&1 &"))
print(cmd(s, "sleep 2; ps | grep -v grep | grep AmbaRTSPServer"))

s.send(b"exit\n"); time.sleep(0.3); s.close()
PY

section "Now try the CLI over USB *without* joining the camera AP"
# This script lives at akaso_360/probe/, so the repo root is two levels up.
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"
rm -f out/*.jpg
go run ./cmd/cli -host "${CAM_USB}" 2>&1 | tail -10
ls -la out/*.jpg 2>/dev/null || echo "(no JPEGs)"
JPGS=$(ls out/*.jpg 2>/dev/null | wc -l | tr -d ' ')

section "VERDICT"
echo "JPEGs written without AP client: ${JPGS}"
if [ "${JPGS}" -ge 4 ]; then
    echo "==> SHIM WORKS. RTSP-over-USB now functions without an AP client."
    echo "    Host solution: USB Ethernet + this shim, no Wi-Fi dongle needed."
    echo "    To make persistent: install akaso_360/camera-bootup.sh on the"
    echo "    camera SD card (see akaso_360/install_camera_bootup.sh)."
else
    echo "==> SHIM DID NOT FIX IT. Either the gate is elsewhere or the binary"
    echo "    caches the check. Inspect ${LOG} for details."
fi
