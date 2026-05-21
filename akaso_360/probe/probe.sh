#!/usr/bin/env bash
# AKASO 360 Wi-Fi hotspot discovery probe.
# Run while joined to the camera's hotspot (AK360_xxxx). Output is written
# both to stdout and /tmp/akaso_probe.log so it can be analyzed after
# switching back to a normal Wi-Fi network.

set -u
LOG=/tmp/akaso_probe.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

section() { echo; echo "======== $* ========"; }

section "Local network state"
echo "Wi-Fi SSID:"
networksetup -getairportnetwork en0 2>/dev/null || true
echo
echo "en0 ifconfig:"
ifconfig en0 | grep -E "inet |status|ether" || true
echo
LOCAL_IP=$(ipconfig getifaddr en0 2>/dev/null || true)
GW=$(ipconfig getoption en0 router 2>/dev/null || true)
echo "Local IP: $LOCAL_IP"
echo "Gateway (camera?): $GW"
echo
echo "ARP table (en0):"
arp -an | grep -v "incomplete" | head -20

if [ -z "${GW:-}" ]; then
  section "No gateway found — aborting active probes"
  echo "Hint: confirm you're joined to AK360_* and DHCP completed (ipconfig getpacket en0)."
  exit 1
fi

CAM="$GW"
section "Ping sanity check ($CAM)"
ping -c 2 -W 1000 "$CAM" || true

section "Fast TCP scan: candidate ports"
nmap -sT -Pn -n --open --max-retries 1 --host-timeout 30s \
  -p 21,22,23,53,80,443,554,1935,5353,6789,7777,7878,8000,8080,8081,8192,8554,8888,9000,9100,49152,49153,49154,49155 \
  "$CAM" || true

section "Full TCP scan (slower, backstop)"
nmap -sT -Pn -n --open --max-retries 1 --host-timeout 180s -p- "$CAM" || true

section "Common UDP ports (very limited — UDP scans are unreliable)"
nmap -sU -Pn -n --open --max-retries 1 --host-timeout 30s \
  -p 53,67,123,554,5353,8554 "$CAM" || true

section "HTTP root probes"
for port in 80 8080 8000 8081 8888; do
  echo "--- http://$CAM:$port/ ---"
  curl -sS -m 3 -D - -o /dev/null "http://$CAM:$port/" || true
done

section "Common HTTP endpoints on port 80"
for path in "/" "/index.html" "/cgi-bin/" "/cgi-bin/Config.cgi" "/live" "/livestream" "/snapshot.jpg" "/snap.jpg" "/videostream.cgi" "/?action=stream" "/?action=snapshot" "/?custom=1&cmd=3001" "/?cmd=3001" "/MISC/getversion" "/sd/" "/state" "/v1/info" "/v1/status"; do
  echo "--- http://$CAM$path ---"
  curl -sS -m 3 -D - "http://$CAM$path" -o /tmp/akaso_body.$$ || true
  echo "BODY (first 400 chars):"
  head -c 400 /tmp/akaso_body.$$ 2>/dev/null; echo
done
rm -f /tmp/akaso_body.$$

section "Ambarella-style JSON probe on TCP 7878"
# Many Ambarella-based action cams answer {"token":0,"msg_id":257} (start session).
(printf '{"token":0,"msg_id":257}\n'; sleep 1) | nc -w 2 "$CAM" 7878 | head -c 600 || true
echo

section "RTSP probes"
for path in "" "live" "live/stream" "live.sdp" "stream" "stream1" "media/stream1" "cam" "preview" "h264" "0" "1"; do
  url="rtsp://$CAM:554/$path"
  echo "--- $url ---"
  ffprobe -hide_banner -loglevel error -rw_timeout 3000000 -i "$url" 2>&1 | head -20 || true
done

section "mDNS / Bonjour services (3s scan)"
timeout 4 dns-sd -B _services._dns-sd._udp local. 2>/dev/null | head -20 || true
echo "--- HTTP service browse ---"
timeout 4 dns-sd -B _http._tcp local. 2>/dev/null | head -20 || true

section "Done. Full log at $LOG"
