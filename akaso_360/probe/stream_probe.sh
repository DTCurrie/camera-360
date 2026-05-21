#!/usr/bin/env bash
# AKASO 360 stream probe v2: execute the Ambarella handshake on TCP 7878 to
# unlock the live RTSP stream, then capture a single frame with ffmpeg so
# we can confirm the stream's resolution / codec / projection layout.
# Run only while joined to the camera's hotspot (AK360_xxxx).

set -u
LOG=/tmp/akaso_stream_probe.log
FRAME=/tmp/akaso_frame.jpg
INFO=/tmp/akaso_frame_info.txt
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

CAM=192.168.42.1
PORT=7878
HANDSHAKE_OUT=/tmp/akaso_handshake.out
: > "$HANDSHAKE_OUT"

section() { echo; echo "======== $* ========"; }

section "Pre-flight"
echo "Gateway: $(ipconfig getoption en0 router 2>/dev/null)"
echo "Our IP : $(ipconfig getifaddr en0 2>/dev/null)"
ping -c 1 -W 1000 "$CAM" >/dev/null && echo "Camera reachable" || { echo "Camera unreachable, aborting"; exit 1; }

# We need an interactive TCP session: send start_session, parse the token from
# the (un-delimited!) response, then send subsequent commands. Pure bash + nc
# is awkward because the server's responses have no newlines. Use python3 to
# drive the socket.

section "Ambarella handshake on $CAM:$PORT"
python3 - "$CAM" "$PORT" "$HANDSHAKE_OUT" <<'PY'
import json, socket, sys, time, re

cam, port, outpath = sys.argv[1], int(sys.argv[2]), sys.argv[3]
out = open(outpath, "w")
log = lambda *a: (print(*a), out.write(" ".join(str(x) for x in a) + "\n"), out.flush())

def recv_one_json(sock, timeout=5.0):
    """Read bytes from sock until we have one balanced JSON object.
       Server emits no delimiter, so we count braces."""
    sock.settimeout(timeout)
    buf = b""
    depth = 0
    started = False
    in_str = False
    esc = False
    while True:
        chunk = sock.recv(4096)
        if not chunk:
            return buf.decode("utf-8", "replace")
        buf += chunk
        for byte in chunk:
            c = chr(byte)
            if in_str:
                if esc:
                    esc = False
                elif c == "\\":
                    esc = True
                elif c == '"':
                    in_str = False
                continue
            if c == '"':
                in_str = True
            elif c == "{":
                depth += 1
                started = True
            elif c == "}":
                depth -= 1
                if started and depth == 0:
                    return buf.decode("utf-8", "replace")

s = socket.create_connection((cam, port), timeout=5)
try:
    # 1. start_session
    msg = {"msg_id": 257, "token": 0}
    log(">> ", json.dumps(msg))
    s.sendall(json.dumps(msg).encode())
    resp = recv_one_json(s); log("<< ", resp)
    j = json.loads(resp)
    token = j.get("param")
    if not isinstance(token, int):
        log("ERROR: no token in start_session reply"); sys.exit(2)
    log("TOKEN =", token)

    # 2. set stream_out_type = rtsp
    msg = {"param": "rtsp", "msg_id": 2, "type": "stream_out_type", "token": token}
    log(">> ", json.dumps(msg))
    s.sendall(json.dumps(msg).encode())
    resp = recv_one_json(s); log("<< ", resp)

    # 2b. Also peek at full config (msg_id 3) so we learn what settings exist.
    msg = {"msg_id": 3, "token": token}
    log(">> ", json.dumps(msg))
    s.sendall(json.dumps(msg).encode())
    try:
        resp = recv_one_json(s, timeout=3.0); log("<<3", resp[:2000])
    except Exception as e:
        log("get_all failed (non-fatal):", e)

    # 3. msg_id 259 - AMBA_BOSS_RESETVF / start preview
    msg = {"msg_id": 259, "param": "none_force", "token": token}
    log(">> ", json.dumps(msg))
    s.sendall(json.dumps(msg).encode())
    try:
        resp = recv_one_json(s); log("<< ", resp)
    except Exception as e:
        log("259 reply timeout (non-fatal):", e)

    log("HANDSHAKE COMPLETE. Holding session open for 25s while ffmpeg pulls RTSP.")
    # Keep the socket alive while ffmpeg connects to RTSP.
    time.sleep(25)

    # Teardown: 260 stop preview, 258 end session
    for mid in (260, 258):
        msg = {"msg_id": mid, "token": token}
        log(">> ", json.dumps(msg))
        try:
            s.sendall(json.dumps(msg).encode())
            resp = recv_one_json(s, timeout=2.0); log("<< ", resp)
        except Exception as e:
            log(f"teardown msg_id {mid} timeout (non-fatal):", e)
finally:
    s.close()
PY
HANDSHAKE_RC=$?
echo "Handshake script exit code: $HANDSHAKE_RC"

if [ $HANDSHAKE_RC -ne 0 ]; then
    section "Handshake failed; skipping RTSP capture"
    exit 1
fi

# The handshake script holds the session open for 25s while we run ffmpeg
# in parallel against the RTSP URL. But the handshake ran to completion
# above (synchronously), so the session is already torn down by now.
# Re-run the handshake in the background while we ffprobe.

section "Re-running handshake in background; capturing RTSP frame in parallel"
( python3 - "$CAM" "$PORT" <<'PY' &
import json, socket, sys, time
cam, port = sys.argv[1], int(sys.argv[2])
def recv_one_json(sock, timeout=5.0):
    sock.settimeout(timeout); buf=b""; depth=0; started=False; in_str=False; esc=False
    while True:
        chunk = sock.recv(4096)
        if not chunk: return buf.decode("utf-8","replace")
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
                if started and depth==0: return buf.decode("utf-8","replace")
s = socket.create_connection((cam,port), timeout=5)
s.sendall(json.dumps({"msg_id":257,"token":0}).encode())
j = json.loads(recv_one_json(s)); tok = j["param"]
s.sendall(json.dumps({"param":"rtsp","msg_id":2,"type":"stream_out_type","token":tok}).encode())
recv_one_json(s)
s.sendall(json.dumps({"msg_id":259,"param":"none_force","token":tok}).encode())
try: recv_one_json(s, timeout=3)
except Exception: pass
sys.stdout.write(f"BG_TOKEN={tok}\n"); sys.stdout.flush()
time.sleep(30)
for mid in (260,258):
    try:
        s.sendall(json.dumps({"msg_id":mid,"token":tok}).encode())
        recv_one_json(s, timeout=2)
    except Exception: pass
s.close()
PY
) &
BG_PID=$!
sleep 2  # let the BG handshake reach the 30s sleep

section "ffprobe rtsp://$CAM:554/live"
ffprobe -hide_banner -loglevel info -rw_timeout 5000000 -i rtsp://$CAM:554/live 2>&1 | tee "$INFO" | head -60 || true

section "ffmpeg snapshot from rtsp://$CAM:554/live (single frame JPEG)"
ffmpeg -y -hide_banner -loglevel warning -rw_timeout 5000000 \
    -i rtsp://$CAM:554/live -frames:v 1 -q:v 2 "$FRAME" 2>&1 | head -30 || true

if [ -s "$FRAME" ]; then
    section "Frame saved to $FRAME"
    file "$FRAME"
    sips -g pixelWidth -g pixelHeight "$FRAME" 2>/dev/null || true
fi

section "Fallback: try rtsp://$CAM/H264"
ffprobe -hide_banner -loglevel info -rw_timeout 3000000 -i rtsp://$CAM/H264 2>&1 | head -20 || true

wait $BG_PID 2>/dev/null
section "Done. Full log at $LOG, frame at $FRAME, ffprobe details at $INFO"
