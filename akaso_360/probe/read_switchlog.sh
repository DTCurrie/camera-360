#!/usr/bin/env bash
# After a failed STA-mode switch + power-cycle recovery, run this while
# joined to the camera's AP again to read the persistent SD-card log
# and see exactly what happened.

set -u
CAM=192.168.42.1

ping -c 1 -W 1000 "$CAM" >/dev/null || { echo "Camera unreachable. Are you on AK360_xxxx?"; exit 1; }

echo "=== reading /tmp/SD0/akaso_sta_switch.log from camera ==="
echo

python3 - "$CAM" <<'PY'
import socket, sys, time
cam = sys.argv[1]
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

def cmd(sock, c, timeout=4.0):
    sock.send((c + "\n").encode())
    return wait_prompt(sock, timeout=timeout)

s = socket.create_connection((cam, 23), timeout=5)
wait_prompt(s, timeout=3)
s.send(b"root\r\n")
wait_prompt(s, timeout=3)

print(cmd(s, "ls -la /tmp/SD0/akaso_sta_switch.log 2>&1"))
print(cmd(s, "cat /tmp/SD0/akaso_sta_switch.log 2>&1", timeout=8))

s.send(b"exit\n")
time.sleep(0.3); s.close()
PY
