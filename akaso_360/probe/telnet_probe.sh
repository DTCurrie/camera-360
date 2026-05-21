#!/usr/bin/env bash
# Read-only telnet recon of the AKASO 360.
# Tries common default credentials in order; on first successful login,
# runs a battery of READ-ONLY commands to characterize the firmware and
# network configuration. NO writes, NO modifications. Then disconnects.
#
# Run while joined to the camera's hotspot (AK360_xxxx).

set -u
LOG=/tmp/akaso_telnet_probe.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

CAM=192.168.42.1
PORT=23

section() { echo; echo "======== $* ========"; }

section "Pre-flight"
echo "Gateway: $(ipconfig getoption en0 router 2>/dev/null)"
echo "Our IP : $(ipconfig getifaddr en0 2>/dev/null)"
ping -c 1 -W 1000 "$CAM" >/dev/null && echo "Camera reachable" || { echo "Camera unreachable, aborting"; exit 1; }

section "Telnet probe via python"
python3 - "$CAM" "$PORT" <<'PY'
import socket, sys, time, re

cam, port = sys.argv[1], int(sys.argv[2])

# Telnet IAC byte and the three negotiation commands we need to handle.
IAC, DONT, WONT, DO, WILL = 0xff, 0xfe, 0xfc, 0xfd, 0xfb

def strip_iac(sock, buf):
    """Strip telnet IAC sequences from buf, automatically responding
    WONT/DONT to all options (we don't want negotiated terminal features).
    Returns the cleaned bytes."""
    out = bytearray()
    i = 0
    while i < len(buf):
        b = buf[i]
        if b == IAC and i+1 < len(buf):
            cmd = buf[i+1]
            if cmd in (DO, DONT, WILL, WONT) and i+2 < len(buf):
                opt = buf[i+2]
                # Refuse every option negotiation cleanly.
                if cmd == DO:
                    sock.send(bytes([IAC, WONT, opt]))
                elif cmd == WILL:
                    sock.send(bytes([IAC, DONT, opt]))
                i += 3
                continue
            elif cmd == IAC:
                # Escaped 0xff -> literal
                out.append(IAC); i += 2; continue
            else:
                i += 2; continue
        out.append(b); i += 1
    return bytes(out)

def read_until(sock, patterns, timeout=4.0):
    """Read until any pattern (regex) appears, or timeout. Returns
    (matched_pattern_or_None, accumulated_text)."""
    end = time.time() + timeout
    buf = b""
    while time.time() < end:
        sock.settimeout(max(0.2, end - time.time()))
        try:
            chunk = sock.recv(4096)
        except socket.timeout:
            continue
        if not chunk:
            break
        chunk = strip_iac(sock, chunk)
        buf += chunk
        text = buf.decode("utf-8", "replace")
        for pat in patterns:
            m = re.search(pat, text)
            if m:
                return pat, text
    return None, buf.decode("utf-8", "replace")

def banner_then_login(sock, user, password, timeout=5.0):
    """Try (user, password). Returns (succeeded, transcript)."""
    pat, text = read_until(sock,
        [r"login:\s*$", r"login:\s*\Z", r"[Ll]ogin:", r"[Uu]sername:",
         r"# $", r"\$ $", r">\s*$"],
        timeout=timeout)
    transcript = text
    if pat is None:
        return False, transcript + "\n[no login prompt seen]"
    # If we got dropped straight to a shell, win.
    if "# " in text or "$ " in text:
        return True, transcript + "\n[appears auto-shell, no login required]"
    sock.send(user.encode() + b"\r\n")
    pat2, text2 = read_until(sock,
        [r"[Pp]assword:", r"# $", r"\$ $", r"incorrect", r"[Ll]ogin:"],
        timeout=timeout)
    transcript += text2
    if pat2 is None:
        return False, transcript + "\n[no password prompt or shell]"
    if "# " in text2 or "$ " in text2:
        return True, transcript + "\n[shell after username, no password required]"
    if "incorrect" in text2.lower() or "Login:" in text2:
        return False, transcript + "\n[rejected before password]"
    sock.send(password.encode() + b"\r\n")
    pat3, text3 = read_until(sock,
        [r"# $", r"\$ $", r">\s*$", r"incorrect", r"[Ll]ogin:", r"denied", r"Bad password"],
        timeout=timeout)
    transcript += text3
    if pat3 in (r"# $", r"\$ $", r">\s*$"):
        return True, transcript
    return False, transcript

def shell_cmd(sock, cmd, timeout=4.0):
    sock.send((cmd + "\n").encode())
    pat, text = read_until(sock, [r"# $", r"\$ $", r">\s*$"], timeout=timeout)
    return text

candidates = [
    ("",       ""),
    ("root",   ""),
    ("root",   "root"),
    ("root",   "admin"),
    ("root",   "12345"),
    ("root",   "123456"),
    ("root",   "password"),
    ("root",   "ambarella"),
    ("root",   "akaso"),
    ("admin",  ""),
    ("admin",  "admin"),
    ("admin",  "12345"),
]

for (u, p) in candidates:
    print(f"\n--- attempting user={u!r} pass={p!r} ---")
    try:
        s = socket.create_connection((cam, port), timeout=4)
    except OSError as e:
        print(f"connect failed: {e}"); break
    try:
        ok, transcript = banner_then_login(s, u, p, timeout=4.0)
        # Print short transcript head only
        head = transcript[-1200:] if len(transcript) > 1200 else transcript
        print(head)
        if ok:
            print(f"\n*** LOGIN SUCCEEDED: user={u!r} pass={p!r} ***")
            print("\n=== gathering read-only system info ===")
            cmds = [
                "id",
                "uname -a",
                "cat /etc/issue 2>/dev/null",
                "cat /etc/version 2>/dev/null",
                "cat /proc/version | head -1",
                "cat /etc/os-release 2>/dev/null",
                "echo '---network---'",
                "ifconfig 2>&1 | head -40",
                "iwconfig 2>&1 | head -20",
                "cat /proc/net/wireless 2>&1",
                "cat /proc/net/route 2>&1",
                "echo '---routing---'",
                "route -n 2>&1 | head -10",
                "echo '---wifi config files---'",
                "ls -la /etc/wpa_supplicant* /etc/hostapd* /etc/wifi* /etc/network 2>&1",
                "cat /etc/wpa_supplicant.conf 2>/dev/null | head -30",
                "cat /etc/hostapd.conf 2>/dev/null | head -30",
                "find /etc /opt /var /tmp -name '*.conf' -size -10k 2>/dev/null | head -40",
                "find / -name 'wpa_supplicant*' 2>/dev/null | head -10",
                "find / -name 'hostapd*' 2>/dev/null | head -10",
                "echo '---mounts---'",
                "mount 2>&1 | head -20",
                "df -h 2>&1 | head -10",
                "echo '---processes---'",
                "ps 2>&1 | head -40",
                "echo '---startup scripts---'",
                "ls -la /etc/init.d/ 2>/dev/null",
                "ls -la /etc/rc* 2>/dev/null",
                "cat /etc/passwd 2>/dev/null",
                "cat /proc/cpuinfo 2>&1 | head -15",
            ]
            for c in cmds:
                print(f"\n$ {c}")
                print(shell_cmd(s, c, timeout=4.0))
            s.send(b"exit\n")
            time.sleep(0.3)
            s.close()
            sys.exit(0)
    finally:
        try: s.close()
        except: pass
    time.sleep(0.5)

print("\nAll credential attempts rejected.")
PY

section "Done. Full log at $LOG"
