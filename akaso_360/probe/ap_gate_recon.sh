#!/usr/bin/env bash
# Investigate exactly what the camera checks to gate RTSP behind an
# associated AP station. Uses USB telnet, no Wi-Fi required.
#
# We want to find one of:
#   (a) a config file / env var that disables the check
#   (b) a /proc or /sys entry the check reads (so we can spoof it)
#   (c) a wrapper script that does the check (so we can patch it)
#   (d) the AmbaRTSPServer binary itself doing it (then strings + ltrace)

set -u
LOG=/tmp/akaso_ap_gate_recon.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

CAM=10.42.0.1

section() { echo; echo "======== $* ========"; }

section "Pre-flight"
ping -c 1 -W 1000 "$CAM" >/dev/null && echo "USB telnet path OK" || { echo "USB unreachable"; exit 1; }

python3 - "$CAM" <<'PY'
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
def cmd(s,c,t=8):
    s.send((c+"\n").encode()); return waitp(s,t)

s=socket.create_connection((cam,23),timeout=5)
waitp(s,3); s.send(b"root\r\n"); waitp(s,3)

print("\n### 1. WHERE IS AmbaRTSPServer, WHAT STARTED IT ###")
print(cmd(s, "ls -la /usr/bin/AmbaRTSPServer /usr/local/bin/AmbaRTSPServer /usr/sbin/AmbaRTSPServer 2>/dev/null"))
print(cmd(s, "file /usr/bin/AmbaRTSPServer 2>/dev/null || echo '(no file cmd)'"))
print(cmd(s, "ps | grep -i rtsp | grep -v grep"))
print(cmd(s, "cat /proc/$(pgrep AmbaRTSPServer)/cmdline 2>/dev/null | tr '\\0' ' '; echo"))
print(cmd(s, "cat /proc/$(pgrep AmbaRTSPServer)/status 2>/dev/null | head -10"))

print("\n### 2. WHO INVOKES IT (init scripts + grep) ###")
print(cmd(s, "grep -rln 'AmbaRTSPServer' /etc /usr/local/share 2>/dev/null | head -20"))
print(cmd(s, "for f in $(grep -rln 'AmbaRTSPServer' /etc /usr/local/share 2>/dev/null | head -5); do echo \"=== $f ===\"; cat $f; echo; done", t=15))

print("\n### 3. WHAT FILES IS AmbaRTSPServer HOLDING OPEN ###")
print(cmd(s, "ls -la /proc/$(pgrep AmbaRTSPServer)/fd/ 2>/dev/null | head -40"))

print("\n### 4. AMBARELLA SESSION SERVER (port 7878) - who is it ###")
print(cmd(s, "for p in $(ls /proc/ 2>/dev/null | grep -E '^[0-9]+$'); do l=$(readlink /proc/$p/fd/* 2>/dev/null | grep tcp 2>/dev/null); [ -n \"$l\" ] && echo \"$p $(cat /proc/$p/comm 2>/dev/null) $l\"; done | grep -E '7878|554' | head -10"))
# Simpler: look at netstat to identify
print(cmd(s, "netstat -lntp 2>/dev/null | grep -E ':554|:7878'"))

print("\n### 5. WHAT DOES THE GATE ACTUALLY CHECK? hostapd state surfaces ###")
print(cmd(s, "ls /var/run/hostapd* 2>/dev/null"))
print(cmd(s, "ls /tmp/hostapd* 2>/dev/null"))
print(cmd(s, "cat /tmp/hostapd*.conf 2>/dev/null | head -30"))
# When no station: what does the kernel show?
print(cmd(s, "iw dev wlan0 station dump 2>&1 | wc -l"))
print(cmd(s, "cat /proc/net/wireless 2>/dev/null"))
print(cmd(s, "cat /proc/net/arp 2>/dev/null"))
# Check for any 'station' files
print(cmd(s, "find / -name '*station*' -mtime -30 2>/dev/null | head -10"))

print("\n### 6. SEARCH BINARIES + SCRIPTS FOR GATE STRINGS ###")
for term in ["associated", "station", "hostapd", "no_client", "ap_client", "have_client", "wlan0_sta", "STA_AUTHORIZED", "wifi_client"]:
    print(cmd(s, f"echo '--- term: {term} ---'; grep -rln '{term}' /usr/local/share 2>/dev/null | head -5", t=10))

print("\n### 7. STRINGS IN AmbaRTSPServer - keywords that hint at gating ###")
print(cmd(s, "strings /usr/bin/AmbaRTSPServer 2>/dev/null | grep -iE 'wlan|hostap|station|associat|client_count|gate|ap_mode' | head -30"))
print(cmd(s, "strings /usr/bin/AmbaRTSPServer 2>/dev/null | grep -iE 'no client|no_client|client_required|preview_state' | head -10"))

print("\n### 8. AMBARELLA SESSION DAEMON BINARY - the one running 7878 ###")
# Common Ambarella names
print(cmd(s, "for b in /usr/bin/* /usr/local/bin/* /usr/sbin/*; do [ -x \"$b\" ] || continue; n=$(basename \"$b\"); case \"$n\" in *RTSP*|*ambapc*|*wifiapp*|*ambar*) echo \"$b\";; esac; done | head -10"))
print(cmd(s, "strings /usr/bin/AmbaWiFiApp 2>/dev/null | grep -iE 'hostap|station|client|associat|wlan' | head -20"))
print(cmd(s, "ls -la /usr/bin/Amba* /usr/local/bin/Amba* 2>/dev/null"))

print("\n### 9. EVERY VENDOR EXECUTABLE - profile them ###")
print(cmd(s, "ls /usr/local/bin/ /usr/local/sbin/ 2>/dev/null"))
print(cmd(s, "ps -ef 2>/dev/null | head -40; echo; ps 2>/dev/null | head -60"))

print("\n### 10. /proc/ambarella - any usb / mode / client toggles? ###")
print(cmd(s, "ls /proc/ambarella/ 2>/dev/null"))
# Read the small ones
print(cmd(s, "for f in /proc/ambarella/*; do [ -f \"$f\" ] || continue; sz=$(stat -c %s \"$f\" 2>/dev/null); [ \"${sz:-9999}\" -lt 500 ] && { echo \"--- $f (${sz}b) ---\"; cat \"$f\" 2>/dev/null; }; done", t=15))

print("\n### 11. PREF FILES that may control preview/RTSP behaviour ###")
print(cmd(s, "ls -la /tmp/pref/ /pref/ 2>/dev/null"))
print(cmd(s, "for f in /tmp/pref/* /pref/*; do [ -f \"$f\" ] || continue; echo \"--- $f ---\"; head -20 \"$f\" 2>/dev/null; done", t=15))

s.send(b"exit\n"); time.sleep(0.3); s.close()
PY

section "Done. Log at $LOG"
