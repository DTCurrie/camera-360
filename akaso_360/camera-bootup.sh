#!/bin/sh
# bootup.sh — runs on AKASO 360 at every boot via /etc/init.d/S99bootdone.
# Brings up USB Ethernet (10.42.0.1/24 on usb0) and installs an iw shim so
# AmbaRTSPServer's "AP client associated?" gate sees a fake station.
#
# Effect: after this runs, a host on 10.42.0.2/24 can pull RTSP from the
# camera at rtsp://10.42.0.1:554/live with no Wi-Fi association needed.
#
# Everything is bind-mounts + tmpfs files. To revert to factory behavior:
# remove /tmp/SD0/bootup.sh and power-cycle.

LOG=/tmp/SD0/bootup.log
echo "" >> "$LOG"
echo "===== bootup.sh @ $(date) =====" >> "$LOG"
exec >> "$LOG" 2>&1

set +e   # never let an error here brick the boot

# Wait until the firmware's own init has settled — AmbaRTSPServer/hostapd
# need to exist before we mess with them. /etc/init.d/S99bootdone is
# last in the init order, so this is mostly belt-and-suspenders.
sleep 3

#---------------------------------------------------------------
# 1. USB Ethernet gadget
#---------------------------------------------------------------
echo "--- enabling USB Ethernet gadget ---"
/usr/local/share/script/usb_ether.sh
# usb_ether.sh loads g_ether but does not assign an IP. Do that here.
sleep 1
ifconfig usb0 10.42.0.1 netmask 255.255.255.0 up
ifconfig usb0 | head -3

#---------------------------------------------------------------
# 2. iw shim — fake an associated AP station so AmbaRTSPServer's
#    "iw dev wlan0 station dump | grep Station" check passes when no
#    real Wi-Fi client is connected.
#---------------------------------------------------------------
echo "--- installing iw shim ---"
# Back up real iw if not already done.
if [ ! -f /tmp/iw.real ]; then
    cp /sbin/iw /tmp/iw.real
    chmod +x /tmp/iw.real
fi

# Write the shim. (Uses spaces, not tabs — TTY transports may eat tabs.)
cat > /tmp/iw <<'SHIM'
#!/bin/sh
# Intercept "dev <iface> station dump": return one fake station so
# AmbaRTSPServer's gate is satisfied without a real AP client.
if [ "$1" = "dev" ] && [ "$3" = "station" ] && [ "$4" = "dump" ]; then
    cat <<EOF
Station 02:00:00:00:00:01 (on $2)
    inactive time:  0 ms
    rx bytes:       0
    rx packets:     0
    tx bytes:       0
    tx packets:     0
    signal:         -50 dBm
    authorized:     yes
    authenticated:  yes
    associated:     yes
EOF
    exit 0
fi
exec /tmp/iw.real "$@"
SHIM
chmod +x /tmp/iw

# Bind-mount the shim over the real iw at both PATH locations.
# Idempotent: clear prior mounts first in case bootup.sh re-runs.
umount /sbin/iw 2>/dev/null
umount /usr/sbin/iw 2>/dev/null
mount --bind /tmp/iw /sbin/iw
mount --bind /tmp/iw /usr/sbin/iw
echo "iw shim bind mounts:"
mount | grep iw

# Sanity-check the shim from the camera side.
echo "shim output:"
iw dev wlan0 station dump
echo "grep result rc:"
iw dev wlan0 station dump | grep Station; echo "  rc=$?"

#---------------------------------------------------------------
# 3. Restart AmbaRTSPServer so it sees the shimmed iw on first
#    client request. (The check is run per-request, but restarting
#    is cheap and avoids any cached state.)
#---------------------------------------------------------------
echo "--- restarting AmbaRTSPServer ---"
killall -9 AmbaRTSPServer 2>/dev/null
sleep 1
/usr/bin/AmbaRTSPServer >/dev/null 2>&1 &
sleep 2
ps | grep -v grep | grep AmbaRTSPServer

echo "--- bootup.sh done @ $(date) ---"
