#!/usr/bin/env bash
# Phase A: enable USB Ethernet gadget on the AKASO 360 (camera side).
#
# Runs /usr/local/share/script/usb_ether.sh on the camera via telnet to
# load the g_ether kernel module, then assigns usb0 a fixed IP. After
# this, plugging a USB-C cable into a host should cause the host to
# enumerate the camera as a USB Ethernet device.
#
# Camera-side IP: 10.42.0.1/24 on usb0
# Host-side IP:   10.42.0.2/24 (you configure this on the Mac/Pi)
#
# Strictly REVERSIBLE: all changes happen in kernel modules + tmpfs.
# Power-cycle to revert.
#
# Run while joined to the camera's hotspot (AK360_xxxx). Wi-Fi keeps
# working in parallel — usb0 is an additional interface, not a
# replacement.

set -u
LOG=/tmp/akaso_usb_ether_phaseA.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

CAM=192.168.42.1
USB_IP=10.42.0.1
USB_MASK=255.255.255.0

section() { echo; echo "======== $* ========"; }

section "Pre-flight"
ping -c 1 -W 1000 "$CAM" >/dev/null && echo "Camera reachable" || { echo "Camera unreachable, aborting"; exit 1; }

section "Enabling USB Ethernet via telnet"
python3 - "$CAM" "$USB_IP" "$USB_MASK" <<'PY'
import socket, sys, time
cam, usb_ip, usb_mask = sys.argv[1], sys.argv[2], sys.argv[3]
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

def cmd(sock, c, timeout=6.0):
    sock.send((c + "\n").encode())
    return wait_prompt(sock, timeout=timeout)

s = socket.create_connection((cam, 23), timeout=5)
wait_prompt(s, timeout=3)
s.send(b"root\r\n")
wait_prompt(s, timeout=3)

print("\n### state BEFORE (no gadget loaded yet) ###")
print(cmd(s, "ls /sys/class/udc/ 2>&1; ifconfig usb0 2>&1"))

print("\n### running /usr/local/share/script/usb_ether.sh ###")
print(cmd(s, "/usr/local/share/script/usb_ether.sh 2>&1"))

# Give the modules a beat to settle.
print("\n### waiting 2s for gadget bind ###")
time.sleep(2)

print("\n### assigning %s/%s to usb0 ###" % (usb_ip, usb_mask))
print(cmd(s, "ifconfig usb0 %s netmask %s 2>&1" % (usb_ip, usb_mask)))

print("\n### state AFTER ###")
print(cmd(s, "lsmod | grep -E 'g_ether|usb_f_|ambarella_udc|libcomposite' | head"))
print(cmd(s, "ls /sys/class/udc/ 2>&1"))
print(cmd(s, "cat /sys/class/udc/*/state 2>&1"))
print(cmd(s, "cat /sys/class/udc/*/current_speed 2>&1"))
print(cmd(s, "ifconfig usb0 2>&1"))
print(cmd(s, "ifconfig wlan0 2>&1 | head -3"))
print(cmd(s, "ip route 2>&1"))

# Persist diagnostic log to SD card, just in case.
print("\n### writing diagnostic snapshot to /tmp/SD0/usb_ether_phaseA.log ###")
print(cmd(s, "(echo '=== usb_ether phaseA at '$(date)' ==='; ifconfig usb0; lsmod | grep -E 'g_ether|usb_f_|udc|comp'; cat /sys/class/udc/*/state 2>/dev/null) > /tmp/SD0/usb_ether_phaseA.log; ls -la /tmp/SD0/usb_ether_phaseA.log"))

s.send(b"exit\n"); time.sleep(0.3); s.close()
print("\ntelnet session closed.")
PY

section "Done — camera side ready"
cat <<'EOF'

  Now do this on your Mac:

  1. Plug a USB-C cable from your Mac into the camera.

  2. macOS will enumerate the camera as a USB Ethernet device. Find
     the interface name:

     networksetup -listallhardwareports

     Look for an entry whose "Hardware Port" mentions LAN/Ethernet and
     whose MAC address starts with 02: (typical for g_ether). The
     "Device:" line tells you the interface name (e.g. en6, en7, en9).

  3. Assign the host-side IP. Replace en6 with whatever you saw above:

     sudo ifconfig en6 10.42.0.2 netmask 255.255.255.0 up

  4. Verify reachability over USB:

     ping -c 2 10.42.0.1
     nc -z -w 2 10.42.0.1 23     # telnet still works on new IP
     nc -z -w 2 10.42.0.1 554    # RTSP
     nc -z -w 2 10.42.0.1 7878   # Ambarella protocol

  5. If everything works, you now have TWO paths to the camera:
       - 192.168.42.1 over Wi-Fi (the AP)
       - 10.42.0.1   over USB Ethernet

     Either works for the camera-360 module.

  Reversal: power-cycle the camera. All gadget driver state is in
  kernel modules + tmpfs; AP-only mode returns automatically.

EOF
