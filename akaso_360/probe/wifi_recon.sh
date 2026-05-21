#!/usr/bin/env bash
# Focused read-only recon of the camera's Wi-Fi mode-switch logic.
# Reads init scripts and config templates so we can understand how the
# firmware chooses between AP and STA mode at boot. No modifications.
#
# Run while joined to the camera's hotspot (AK360_xxxx).

set -u
LOG=/tmp/akaso_wifi_recon.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

CAM=192.168.42.1

section() { echo; echo "======== $* ========"; }

section "Pre-flight"
echo "Gateway: $(ipconfig getoption en0 router 2>/dev/null)"
ping -c 1 -W 1000 "$CAM" >/dev/null && echo "Camera reachable" || { echo "Camera unreachable"; exit 1; }

section "Telnet read-only recon"
python3 - "$CAM" <<'PY'
import socket, sys, time, re

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
    end = time.time() + timeout
    buf = b""
    while time.time() < end:
        sock.settimeout(max(0.2, end - time.time()))
        try: chunk = sock.recv(4096)
        except socket.timeout: continue
        if not chunk: break
        chunk = strip_iac(sock, chunk)
        buf += chunk
        if b"# " in buf[-8:] or b"$ " in buf[-8:]:
            return buf.decode("utf-8","replace")
    return buf.decode("utf-8","replace")

def cmd(sock, c, timeout=5.0):
    sock.send((c + "\n").encode())
    return read_until_prompt(sock, timeout=timeout)

s = socket.create_connection((cam, 23), timeout=5)
# login: root / no password
read_until_prompt(s, timeout=3)
s.send(b"root\r\n")
read_until_prompt(s, timeout=3)

# === core mode-switch logic ===
print("\n### /etc/init.d/S91wifi (Wi-Fi startup decision) ###")
print(cmd(s, "cat /etc/init.d/S91wifi"))

print("\n### /etc/init.d/S40network ###")
print(cmd(s, "cat /etc/init.d/S40network"))

print("\n### /etc/network/interfaces ###")
print(cmd(s, "cat /etc/network/interfaces"))

print("\n### /etc/network/if-pre-up.d/ ###")
print(cmd(s, "ls -la /etc/network/if-pre-up.d/ /etc/network/if-post-up.d/"))
print(cmd(s, "for f in /etc/network/if-pre-up.d/* /etc/network/if-post-up.d/*; do echo \"--- $f ---\"; cat $f; done"))

# === current runtime state ===
print("\n### /tmp/pref/wifi.conf (runtime preference) ###")
print(cmd(s, "cat /tmp/pref/wifi.conf"))

print("\n### /tmp/hostapd.conf (currently-active AP config) ###")
print(cmd(s, "cat /tmp/hostapd.conf"))

print("\n### all files in /tmp/pref/ ###")
print(cmd(s, "ls -la /tmp/pref/ && echo '---' && for f in /tmp/pref/*; do echo \"=== $f ===\"; cat $f 2>/dev/null; done"))

# === STA-mode templates ===
print("\n### /usr/local/share/script/wpa_supplicant.conf (STA template) ###")
print(cmd(s, "cat /usr/local/share/script/wpa_supplicant.conf"))

print("\n### /usr/local/share/script/wpa_supplicant.ap.conf (AP template) ###")
print(cmd(s, "cat /usr/local/share/script/wpa_supplicant.ap.conf"))

print("\n### /usr/local/share/script/hostapd.conf (AP template) ###")
print(cmd(s, "cat /usr/local/share/script/hostapd.conf"))

# === enumerate all helper scripts (this is where the mode-switch logic likely lives) ###
print("\n### /usr/local/share/script/ directory listing ###")
print(cmd(s, "ls -la /usr/local/share/script/"))

print("\n### identify shell scripts in /usr/local/share/script/ ###")
print(cmd(s, "for f in /usr/local/share/script/*; do file $f 2>/dev/null | grep -E 'script|text'; done"))

# === look for any 'sta'/'station'/'client'-mode helpers ###
print("\n### grep for STA-mode helpers ###")
print(cmd(s, "grep -lr -i 'station\\|sta_mode\\|client_mode' /usr/local/share/script/ /etc/init.d/ 2>/dev/null", timeout=8))

# === how is the current mode actually invoked? ===
print("\n### running processes related to wifi ###")
print(cmd(s, "ps | grep -E 'hostapd|wpa_supp|dnsmasq|wlan' | grep -v grep"))

# === dnsmasq config (it's running the DHCP for AP clients) ===
print("\n### dnsmasq config ###")
print(cmd(s, "cat /etc/dnsmasq.conf 2>/dev/null; cat /tmp/dnsmasq.conf 2>/dev/null"))

# === see if there's any "switch wifi mode" CLI utility on the camera ===
print("\n### binaries in /usr/local/bin and /usr/local/share/script/ ###")
print(cmd(s, "ls /usr/local/bin/ /usr/local/share/script/ 2>/dev/null | head -60"))

# === full cherokee config (in case there's a hidden CGI endpoint) ===
print("\n### cherokee.conf (relevant sections only) ###")
print(cmd(s, "grep -E 'vserver|rule|handler|directory|extensions|target' /etc/cherokee/cherokee.conf 2>/dev/null | head -40"))

# === final logout ===
s.send(b"exit\n")
time.sleep(0.3)
s.close()
PY

section "Done. Full log at $LOG"
