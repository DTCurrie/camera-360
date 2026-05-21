#!/usr/bin/env bash
# Finds the AKASO 360 on your home/guest network after a successful STA-
# mode switch. ARP-scans the local subnet looking for the camera's MAC.
# Computes the real subnet from the interface's netmask instead of
# assuming /24.
#
# The camera's Wi-Fi chip is a Realtek RTL8821CS — MAC OUI is 80:9d:65.
# The full address differs per unit. To find yours, ARP your local
# network with the camera on AP mode (you'd see the camera at
# 192.168.42.1 with its MAC), or set CAM_MAC env var if you know it.

set -u
LOG=/tmp/akaso_find_camera.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

CAM_MAC="${CAM_MAC:-80:9d:65}"   # OUI-only match by default

section() { echo; echo "======== $* ========"; }

section "Pre-flight"
echo "Local Wi-Fi: $(networksetup -getairportnetwork en0 2>/dev/null)"
LOCAL_IP=$(ipconfig getifaddr en0 2>/dev/null || true)
GW=$(ipconfig getoption en0 router 2>/dev/null || true)
MASK_HEX=$(ifconfig en0 | awk '/inet / {print $4; exit}')   # e.g. 0xffffff00
echo "Local IP   : ${LOCAL_IP:-<none>}"
echo "Gateway    : ${GW:-<none>}"
echo "Netmask    : ${MASK_HEX}"

if [ -z "${LOCAL_IP}" ]; then
    echo "ERROR: no IP on en0. Are you on Wi-Fi?"
    exit 1
fi
if [ "${LOCAL_IP%.*}" = "192.168.42" ]; then
    echo "WARNING: still on AK360_xxxx camera AP. Switch to your guest/home network."
    exit 1
fi

# Convert netmask hex (e.g. 0xfffffc00) into prefix length.
PREFIX=$(python3 -c "
m=int('${MASK_HEX}',16)
b=bin(m)[2:].zfill(32)
print(b.count('1'))")
echo "Prefix len : /${PREFIX}"

# Compute the network address (LOCAL_IP & MASK) using python (more
# portable than shell bitwise).
NETWORK=$(python3 -c "
import socket, struct
ip = struct.unpack('!I', socket.inet_aton('${LOCAL_IP}'))[0]
mask = int('${MASK_HEX}', 16)
net = ip & mask
print(socket.inet_ntoa(struct.pack('!I', net)))")
SUBNET="${NETWORK}/${PREFIX}"
echo "Subnet     : ${SUBNET}"

# nmap -sn handles up to ~4096 hosts (/20) comfortably in <60s. Above that
# (e.g. /16 = 65k hosts), we clamp to /20 around the local IP — the camera
# usually lands close in DHCP terms.
HOSTS=$((1 << (32 - PREFIX)))
if [ ${HOSTS} -gt 4096 ]; then
    echo "Network has ${HOSTS} hosts (large). Clamping scan to /20 around your IP."
    THIRD_OCTET=$(echo "${LOCAL_IP}" | cut -d. -f3)
    BLOCK_BASE=$(( (THIRD_OCTET / 16) * 16 ))
    NET_PREFIX=$(echo "${LOCAL_IP}" | cut -d. -f1-2)
    SUBNET="${NET_PREFIX}.${BLOCK_BASE}.0/20"
    echo "Scanning   : ${SUBNET}"
fi

section "ARP scan ${SUBNET}"
echo "running: nmap -sn -T4 ${SUBNET}"
echo "(/20 = ~4096 hosts can take 30-60s; smaller networks are faster)"
nmap -sn -T4 -n "${SUBNET}" > /tmp/_nmap_scan.out 2>&1 || true
echo "nmap done."

section "Looking for camera MAC ${CAM_MAC} in ARP table"
arp -an > /tmp/_arp_table.out
MATCH=$(grep -i "${CAM_MAC}" /tmp/_arp_table.out || true)

if [ -z "${MATCH}" ]; then
    echo "Camera MAC not found in ARP table after ${SUBNET} scan."
    echo
    echo "Quick mDNS check (sometimes the camera announces itself):"
    dns-sd -B _services._dns-sd._udp local. 2>/dev/null &
    DNSSD_PID=$!
    sleep 3
    kill ${DNSSD_PID} 2>/dev/null
    echo
    echo "Possible reasons for no result:"
    echo "  - Camera failed to associate (wrong password / WPA-Enterprise required)"
    echo "  - Guest network has client isolation (camera is there but unreachable)"
    echo "  - Camera ended up on a different VLAN / subnet"
    echo "  - Switch is still in progress (wait 30s and re-run)"
    echo
    echo "To diagnose: power-cycle camera, reconnect to AK360_xxxx, then:"
    echo "  bash $(dirname "$0")/read_switchlog.sh"
    rm -f /tmp/_nmap_scan.out /tmp/_arp_table.out
    exit 1
fi

CAM_IP=$(echo "${MATCH}" | sed -E 's/.*\(([0-9.]+)\).*/\1/')
echo "FOUND: ${MATCH}"
echo
echo "  Camera IP on this network: ${CAM_IP}"

section "Verifying camera services"
echo "--- pinging $CAM_IP ---"
ping -c 2 -W 1000 "$CAM_IP" || echo "(ping failed, but other services might still work)"

echo
echo "--- testing Ambarella protocol on port 7878 ---"
python3 - "$CAM_IP" <<'PY'
import json, socket, sys
ip = sys.argv[1]
try:
    s = socket.create_connection((ip, 7878), timeout=4)
    s.sendall(json.dumps({"msg_id":257,"token":0}).encode())
    s.settimeout(3)
    data = s.recv(512)
    s.close()
    print("  reply:", data.decode("utf-8","replace"))
    print("  -> Ambarella control protocol REACHABLE on", ip + ":7878")
except Exception as e:
    print("  FAILED:", e)
PY

echo
echo "--- testing RTSP on port 554 ---"
if nc -z -w 3 "$CAM_IP" 554; then
    echo "  -> RTSP port REACHABLE on $CAM_IP:554"
else
    echo "  -> RTSP not reachable"
fi

echo
echo "--- testing telnet on port 23 ---"
if nc -z -w 3 "$CAM_IP" 23; then
    echo "  -> telnet REACHABLE on $CAM_IP:23"
else
    echo "  -> telnet not reachable"
fi

section "Done"
echo
echo "  Camera reachable at ${CAM_IP}."
echo "  Configure the module with: \"host\": \"${CAM_IP}\""
echo "  This change reverts on next power-cycle of the camera."
rm -f /tmp/_nmap_scan.out /tmp/_arp_table.out
