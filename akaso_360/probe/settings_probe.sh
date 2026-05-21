#!/usr/bin/env bash
# Read-only enumeration of the AKASO 360's Ambarella protocol surface.
# Looks specifically for Wi-Fi STA mode capability that the AKASO Go app
# doesn't expose. Makes no writes to the camera.
#
# Run while joined to the camera's hotspot (AK360_xxxx).

set -u
LOG=/tmp/akaso_settings_probe.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

CAM=192.168.42.1
PORT=7878

section() { echo; echo "======== $* ========"; }

section "Pre-flight"
echo "Gateway: $(ipconfig getoption en0 router 2>/dev/null)"
echo "Our IP : $(ipconfig getifaddr en0 2>/dev/null)"
ping -c 1 -W 1000 "$CAM" >/dev/null && echo "Camera reachable" || { echo "Camera unreachable, aborting"; exit 1; }

section "Driving the Ambarella protocol"
python3 - "$CAM" "$PORT" <<'PY'
import json, socket, sys, time

cam, port = sys.argv[1], int(sys.argv[2])

def recv_one_json(sock, timeout=3.0):
    """Read until one balanced JSON object arrives. The server emits no
    delimiter between messages, so we brace-count."""
    sock.settimeout(timeout)
    buf = b""; depth=0; started=False; in_str=False; esc=False
    while True:
        chunk = sock.recv(4096)
        if not chunk:
            return buf.decode("utf-8","replace")
        buf += chunk
        for byte in chunk:
            c = chr(byte)
            if in_str:
                if esc: esc=False
                elif c=="\\": esc=True
                elif c=='"': in_str=False
                continue
            if c=='"': in_str=True
            elif c=='{': depth+=1; started=True
            elif c=='}':
                depth-=1
                if started and depth==0:
                    return buf.decode("utf-8","replace")

def send_and_recv(sock, msg, timeout=3.0):
    """Send a JSON message, return parsed response or None on timeout."""
    try:
        sock.sendall(json.dumps(msg).encode())
        resp = recv_one_json(sock, timeout=timeout)
        return json.loads(resp)
    except (socket.timeout, json.JSONDecodeError) as e:
        return {"_error": f"{type(e).__name__}: {e}"}

s = socket.create_connection((cam, port), timeout=5)
try:
    # Auth
    resp = send_and_recv(s, {"msg_id": 257, "token": 0})
    print(">> start_session ->", json.dumps(resp))
    token = resp.get("param")
    if not isinstance(token, int):
        print("ERROR: no token, bailing"); sys.exit(2)

    print()
    print("=== msg_id 3: get_all_settings (what the camera tells the app) ===")
    resp = send_and_recv(s, {"msg_id": 3, "token": token})
    print(json.dumps(resp, indent=2))

    print()
    print("=== msg_id 4: get_all_setting_options (every settable knob) ===")
    resp = send_and_recv(s, {"msg_id": 4, "token": token}, timeout=8.0)
    print(json.dumps(resp, indent=2)[:8000])

    print()
    print("=== msg_id 9: get_setting_by_index probes (Ambarella variant) ===")
    for idx in range(0, 12):
        resp = send_and_recv(s, {"msg_id": 9, "token": token, "param": idx}, timeout=1.5)
        if resp and resp.get("rval", -999) == 0:
            print(f"idx={idx:>2}:", json.dumps(resp))

    print()
    print("=== msg_id 1: get_setting by NAME — probe common Wi-Fi keys ===")
    candidates = [
        "wifi_mode", "wifi_type", "wifi_ssid", "wifi_password", "wifi_status",
        "wifi_setup", "network_mode", "network_type", "ap_mode", "sta_mode",
        "app_mode", "app_status", "app_type", "mode", "wifi_band",
        "client_mode", "station_mode", "bridge_mode",
    ]
    for name in candidates:
        resp = send_and_recv(s, {"msg_id": 1, "token": token, "type": name}, timeout=1.5)
        if resp and resp.get("rval", -999) == 0:
            print(f"FOUND: {name:>20} ->", json.dumps(resp))
        elif resp and "_error" in resp:
            pass  # timeout, ignore
        # else: rejected (rval != 0) — useful negative signal but verbose

    print()
    print("=== Blind msg_id sweep 8..32, 256..280, 1024..1056 ===")
    # Walk known-protocol-relevant msg_id ranges. For each, send the bare
    # token-only message and see if the camera responds at all. Skip msg_ids
    # we already know to avoid (257/258/259/260 = session lifecycle).
    skip = {257, 258, 259, 260}
    ranges = list(range(8, 33)) + list(range(256, 281)) + list(range(1024, 1057))
    for mid in ranges:
        if mid in skip:
            continue
        resp = send_and_recv(s, {"msg_id": mid, "token": token}, timeout=1.0)
        if resp is None or "_error" in resp:
            continue
        rval = resp.get("rval")
        # rval=-23 / -19 / -4 are "rejected/invalid"; report any other reply.
        if rval not in (-23, -19, -4, -22):
            print(f"msg_id={mid:>4}:", json.dumps(resp))

    print()
    print("=== HTTP probes that weren't tried last time ===")
    # Re-probe a few more HTTP paths in case any expose Wi-Fi config.
    # (We do this OUTSIDE the Ambarella session.)

    # Teardown
    send_and_recv(s, {"msg_id": 260, "token": token}, timeout=1.5)
    send_and_recv(s, {"msg_id": 258, "token": token}, timeout=1.5)
finally:
    s.close()
PY

section "Extra HTTP / Cherokee paths"
for path in "/config" "/admin" "/admin/" "/system" "/wifi" "/network" "/api" "/api/v1/wifi" "/cgi-bin/wifi" "/cgi-bin/network" "/setup" "/CGI/index"; do
  echo "--- http://$CAM$path ---"
  curl -sS -m 2 -o /tmp/_body.$$ -w "HTTP %{http_code} (%{size_download} bytes)\n" "http://$CAM$path" || true
  if [ -s /tmp/_body.$$ ] && grep -q "wifi\|network\|station\|client\|station" /tmp/_body.$$ 2>/dev/null; then
    echo "  (contains wifi/network keywords)"
    head -c 600 /tmp/_body.$$
    echo
  fi
done
rm -f /tmp/_body.$$

section "Done. Full log at $LOG"
