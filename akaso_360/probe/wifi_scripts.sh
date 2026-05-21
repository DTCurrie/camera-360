#!/usr/bin/env bash
# Final read-only recon: read the actual orchestration scripts that
# implement the Wi-Fi mode switch, plus trace what invokes them at boot.
# After this we have everything needed to plan the STA-mode experiment.

set -u
LOG=/tmp/akaso_wifi_scripts.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

CAM=192.168.42.1

section() { echo; echo "======== $* ========"; }

section "Pre-flight"
ping -c 1 -W 1000 "$CAM" >/dev/null && echo "Camera reachable" || { echo "Camera unreachable"; exit 1; }

section "Telnet read-only recon"
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

def read_until_prompt(sock, timeout=4.0):
    end = time.time() + timeout; buf = b""
    while time.time() < end:
        sock.settimeout(max(0.2, end - time.time()))
        try: chunk = sock.recv(4096)
        except socket.timeout: continue
        if not chunk: break
        chunk = strip_iac(sock, chunk); buf += chunk
        if b"# " in buf[-8:] or b"$ " in buf[-8:]:
            return buf.decode("utf-8","replace")
    return buf.decode("utf-8","replace")

def cmd(sock, c, timeout=6.0):
    sock.send((c + "\n").encode())
    return read_until_prompt(sock, timeout=timeout)

s = socket.create_connection((cam, 23), timeout=5)
read_until_prompt(s, timeout=3)
s.send(b"root\r\n")
read_until_prompt(s, timeout=3)

# === the three critical orchestration scripts ===
print("\n### /usr/local/share/script/wifi_start.sh ###")
print(cmd(s, "cat /usr/local/share/script/wifi_start.sh"))

print("\n### /usr/local/share/script/sta_start.sh ###")
print(cmd(s, "cat /usr/local/share/script/sta_start.sh"))

print("\n### /usr/local/share/script/wifi_stop.sh ###")
print(cmd(s, "cat /usr/local/share/script/wifi_stop.sh"))

# === mode-specific configs ===
print("\n### /usr/local/share/script/wifi.sta.conf ###")
print(cmd(s, "cat /usr/local/share/script/wifi.sta.conf"))

print("\n### /usr/local/share/script/wifi.ap.conf ###")
print(cmd(s, "cat /usr/local/share/script/wifi.ap.conf"))

# === trace what invokes wifi_start.sh at boot ===
print("\n### Where is wifi_start.sh actually called from? ###")
print(cmd(s, "grep -rl 'wifi_start' /etc /usr/local/share/script /usr/local/bin /usr/sbin /usr/bin 2>/dev/null | head -20", timeout=10))

print("\n### Strings 'wifi_start' in binaries (likely vendor daemon) ###")
print(cmd(s, "find /usr -type f -size -2M -exec grep -l 'wifi_start' {} \\; 2>/dev/null | head -20", timeout=15))

# === the actual boot init script set ===
print("\n### /etc/init.d/rcS (boots services) ###")
print(cmd(s, "cat /etc/init.d/rcS"))

print("\n### /etc/init.d/S50service (looks suspicious — vendor svc) ###")
print(cmd(s, "cat /etc/init.d/S50service"))

print("\n### /etc/init.d/S60postservice ###")
print(cmd(s, "cat /etc/init.d/S60postservice"))

# === look at the vendor service binaries ===
print("\n### /usr/local/bin contents ###")
print(cmd(s, "ls -la /usr/local/bin/ 2>/dev/null"))

print("\n### /usr/local/sbin contents ###")
print(cmd(s, "ls -la /usr/local/sbin/ 2>/dev/null"))

# === ap_start.sh for comparison (we currently know how AP comes up) ===
print("\n### /usr/local/share/script/ap_start.sh (head 80 lines for reference) ###")
print(cmd(s, "head -80 /usr/local/share/script/ap_start.sh"))

# === wifi_status.sh: how to query current state ===
print("\n### /usr/local/share/script/wifi_status.sh ###")
print(cmd(s, "cat /usr/local/share/script/wifi_status.sh"))

# === confirm Wi-Fi chip capabilities (concurrent AP+STA?) ===
print("\n### iw list (concurrency modes) ###")
print(cmd(s, "which iw && iw list 2>&1 | head -100"))

print("\n### iw dev (current interfaces) ###")
print(cmd(s, "which iw && iw dev 2>&1"))

# === how to find the camera on a foreign network later ===
print("\n### avahi / mDNS — does the camera announce itself? ###")
print(cmd(s, "ps | grep -E 'avahi|mdns' | grep -v grep"))
print(cmd(s, "ls -la /etc/avahi 2>/dev/null"))

s.send(b"exit\n"); time.sleep(0.3); s.close()
PY

section "Done. Full log at $LOG"
