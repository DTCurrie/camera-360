#!/usr/bin/env bash
# Read-only investigation of USB gadget Ethernet support on the AKASO 360.
# Looks for: existing USB ether scripts, loaded kernel modules, available
# gadget functions, current USB controller state, and how the firmware
# selects between USB modes (mass storage / ethernet / console).
#
# Run while joined to the camera's hotspot (AK360_xxxx).
# Strictly read-only — no USB-mode switches attempted.

set -u
LOG=/tmp/akaso_usb_recon.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

CAM=192.168.42.1

section() { echo; echo "======== $* ========"; }

section "Pre-flight"
ping -c 1 -W 1000 "$CAM" >/dev/null && echo "Camera reachable" || { echo "Camera unreachable, aborting"; exit 1; }

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

def cmd(sock, c, timeout=5.0):
    sock.send((c + "\n").encode())
    return wait_prompt(sock, timeout=timeout)

s = socket.create_connection((cam, 23), timeout=5)
wait_prompt(s, timeout=3)
s.send(b"root\r\n")
wait_prompt(s, timeout=3)

# === the key scripts ===
print("\n### usb_ether.sh ###")
print(cmd(s, "cat /usr/local/share/script/usb_ether.sh"))

print("\n### usb3_ether.sh ###")
print(cmd(s, "cat /usr/local/share/script/usb3_ether.sh"))

print("\n### insert_usb_modules.sh ###")
print(cmd(s, "cat /usr/local/share/script/insert_usb_modules.sh"))

print("\n### remove_usb_modules.sh ###")
print(cmd(s, "cat /usr/local/share/script/remove_usb_modules.sh"))

print("\n### usb_host.sh ###")
print(cmd(s, "cat /usr/local/share/script/usb_host.sh"))

print("\n### usb_mass_storage.sh (for comparison — what's currently used) ###")
print(cmd(s, "cat /usr/local/share/script/usb_mass_storage.sh"))

print("\n### usb_msg_dev.sh ###")
print(cmd(s, "cat /usr/local/share/script/usb_msg_dev.sh"))

print("\n### usb3_msg_dev.sh ###")
print(cmd(s, "cat /usr/local/share/script/usb3_msg_dev.sh"))

print("\n### usb_console.sh ###")
print(cmd(s, "cat /usr/local/share/script/usb_console.sh"))

print("\n### load.sh + unload.sh (driver lifecycle helpers) ###")
print(cmd(s, "cat /usr/local/share/script/load.sh"))
print(cmd(s, "cat /usr/local/share/script/unload.sh"))

# === current state of the USB controller ===
print("\n### USB device controllers (UDC) ###")
print(cmd(s, "ls -la /sys/class/udc/ 2>&1"))
print(cmd(s, "for f in /sys/class/udc/*/state /sys/class/udc/*/function /sys/class/udc/*/current_speed; do echo \"--- $f ---\"; cat $f 2>/dev/null; done"))

# === Ambarella-specific USB interface ===
print("\n### /proc/ambarella/ entries (look for USB selectors) ###")
print(cmd(s, "ls /proc/ambarella/ 2>&1"))
print(cmd(s, "for f in /proc/ambarella/usb* /proc/ambarella/*usb* /proc/ambarella/role* ; do echo \"--- $f ---\"; cat $f 2>/dev/null; done"))

# === currently loaded modules related to USB gadget ===
print("\n### loaded modules (USB + gadget related) ###")
print(cmd(s, "lsmod | grep -iE 'usb|gadget|composite|ether|rndis|udc|cdc|g_'"))

# === available gadget function modules on disk ===
print("\n### gadget kernel modules on disk ###")
print(cmd(s, "find /lib/modules -name 'g_*.ko' -o -name 'usb_f_*.ko' -o -name 'libcomposite.ko' -o -name 'configfs.ko' 2>/dev/null | head -30"))

# === configfs (modern USB gadget framework) ===
print("\n### USB gadget configfs ###")
print(cmd(s, "ls -la /sys/kernel/config/usb_gadget/ 2>&1"))
print(cmd(s, "mount | grep -E 'configfs|usb'"))

# === current network interfaces (look for any usb0 etc) ===
print("\n### network interfaces (looking for existing usb0/usbX) ###")
print(cmd(s, "ifconfig -a 2>&1 | grep -E '^[a-z]' "))
print(cmd(s, "ls /sys/class/net/ 2>&1"))

# === dmesg snippets ===
print("\n### dmesg: USB controller + gadget startup messages ###")
print(cmd(s, "dmesg 2>/dev/null | grep -iE 'usb|gadget|cdc|rndis|ether|g_ether|composite' | head -40"))

# === look for vendor-specific USB mode selection ===
print("\n### grep for 'usb_ether' invocation in init scripts and binaries ###")
print(cmd(s, "grep -rl 'usb_ether\\|usb_mass_storage\\|usb_msg_dev' /etc/init.d /usr/local/share/script /usr/local/bin /usr/local/sbin 2>/dev/null | head -20", timeout=8))

# === any pref file controlling USB mode? ===
print("\n### /pref and /tmp/pref USB-related files ###")
print(cmd(s, "ls -la /pref/ 2>/dev/null; ls -la /tmp/pref/ 2>/dev/null"))
print(cmd(s, "grep -l -i 'usb' /pref/* /tmp/pref/* 2>/dev/null"))

# === any boot-time config or selector ===
print("\n### /etc/init.d/rcS (master init) ###")
print(cmd(s, "cat /etc/init.d/rcS"))

print("\n### vendor service init scripts that might select USB mode ###")
print(cmd(s, "for f in /etc/init.d/S50service /etc/init.d/S60postservice /etc/init.d/S99bootdone; do echo \"--- $f ---\"; cat $f 2>/dev/null; done"))

# === binaries that might invoke USB mode switching ===
print("\n### vendor /usr/local/bin contents ###")
print(cmd(s, "ls -la /usr/local/bin/ /usr/local/sbin/ 2>&1"))

# === check what USB device the camera presents WHEN plugged in (gadget mode) ===
print("\n### USB gadget configfs detail (if it exists) ###")
print(cmd(s, "find /sys/kernel/config/usb_gadget -type f 2>/dev/null | head -20"))
print(cmd(s, "for f in $(find /sys/kernel/config/usb_gadget -type f 2>/dev/null | head -10); do echo \"--- $f ---\"; cat $f 2>/dev/null | head -3; done"))

s.send(b"exit\n")
time.sleep(0.3); s.close()
PY

section "Done. Full log at $LOG"
