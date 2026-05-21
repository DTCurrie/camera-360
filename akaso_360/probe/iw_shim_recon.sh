#!/usr/bin/env bash
# Probe: where is `iw`, is the filesystem writable enough to overlay it,
# what PATH does AmbaRTSPServer see.

set -u
LOG=/tmp/akaso_iw_shim_recon.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

CAM=10.42.0.1

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
def cmd(s,c,t=6):
    s.send((c+"\n").encode()); return waitp(s,t)

s=socket.create_connection((cam,23),timeout=5)
waitp(s,3); s.send(b"root\r\n"); waitp(s,3)

print("\n### where is iw? ###")
print(cmd(s, "for d in /sbin /usr/sbin /bin /usr/bin /usr/local/bin /usr/local/sbin; do [ -x \"$d/iw\" ] && echo \"$d/iw\"; done"))
print(cmd(s, "ls -la $(which iw 2>/dev/null) 2>&1"))

print("\n### what PATH does AmbaRTSPServer see (its process env) ###")
print(cmd(s, "PID=$(ps | awk '/AmbaRTSPServer/ && !/awk/{print $1; exit}'); echo PID=$PID; cat /proc/$PID/environ 2>/dev/null | tr '\\0' '\\n'"))

print("\n### filesystem writability — squashfs vs tmpfs ###")
print(cmd(s, "mount | head -20"))
print(cmd(s, "df -h"))

print("\n### where could we place a shim that takes precedence? ###")
# If any tmpfs dir is in PATH ahead of /usr/sbin (typical iw location)
print(cmd(s, "PID=$(ps | awk '/AmbaRTSPServer/ && !/awk/{print $1; exit}'); cat /proc/$PID/environ 2>/dev/null | tr '\\0' '\\n' | grep ^PATH"))
print(cmd(s, "echo PATH=$PATH"))

print("\n### confirm the failure mode — iw output right now (no AP client) ###")
print(cmd(s, "iw dev wlan0 station dump"))
print(cmd(s, "iw dev wlan0 station dump | grep Station; echo rc=$?"))

print("\n### confirm the binary calls iw with no path (i.e. PATH-based) ###")
# Find the exact byte sequence
print(cmd(s, "strings /usr/bin/AmbaRTSPServer | grep -E '^iw |^/.*iw |station dump'"))

print("\n### test bind-mount approach: is mount writable to do mount -o bind? ###")
print(cmd(s, "mount -o bind /tmp /tmp 2>&1; rc=$?; echo rc=$rc; mountpoint /tmp 2>&1"))
print(cmd(s, "umount /tmp 2>/dev/null"))

print("\n### preferred shim location candidates (writable dirs that exist) ###")
print(cmd(s, "for d in /tmp /var /tmp/SD0 /usr/local/bin; do touch $d/.iw_test 2>/dev/null && { echo \"WRITABLE: $d\"; rm $d/.iw_test; } || echo \"RO: $d\"; done"))

print("\n### could the /usr/local/bin dir take precedence? (squashfs?) ###")
print(cmd(s, "ls -la /usr/local/bin/ 2>&1 | head -5; mount | grep '/usr/local'"))

print("\n### can we bind-mount a file over /usr/sbin/iw (the typical iw path)? ###")
# Test capability with a harmless target
print(cmd(s, "echo 'hello' > /tmp/shim_test_src; mount --bind /tmp/shim_test_src /tmp/shim_test_dst 2>&1 || echo '(expected: dst missing)'"))
# Actually do a real attempt that we'll unmount immediately
print(cmd(s, "test -f /usr/sbin/iw && echo 'iw at /usr/sbin/iw' || echo 'not /usr/sbin/iw'"))
print(cmd(s, "test -f /sbin/iw && echo 'iw at /sbin/iw' || echo 'not /sbin/iw'"))
print(cmd(s, "test -f /usr/bin/iw && echo 'iw at /usr/bin/iw' || echo 'not /usr/bin/iw'"))

s.send(b"exit\n"); time.sleep(0.3); s.close()
PY
