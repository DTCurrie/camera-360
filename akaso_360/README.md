# AKASO 360

Setup and reverse-engineering notes for the [AKASO 360](https://www.akasotech.com/akaso-360-action-camera)
action camera as a source for the [camera-360](../README.md) Viam
module.

## Why this camera needs special setup

Out of the box, the AKASO 360 is not reachable from the
[`dtcurrie:camera-360:ambarella-camera`](../dtcurrie_camera-360_ambarella-camera.md) module
without a one-time camera-side modification. Two firmware behaviors
cause this:

1. **No UVC webcam mode, no HDMI output.** The only ingress channel is
   the camera's Wi-Fi hotspot (`AK360_xxxx`). A host machine that joins
   that hotspot can't simultaneously be on another Wi-Fi for internet.
2. **The camera's RTSP server refuses to serve clients unless a Wi-Fi
   station is associated to its hostapd.** Verified empirically: TCP
   port 554 accepts the handshake, then the server sends RST as soon as
   a real RTSP request comes in.

The workaround installed by this directory's scripts: enable the
camera's USB Ethernet gadget driver so the camera presents itself as
`10.42.0.1` over USB-C, and bind-mount a shim over `/sbin/iw` so the
RTSP server's "is a station associated?" check always returns yes.

Net effect: a host (e.g. Raspberry Pi) with normal Wi-Fi for internet
plus a USB-C cable to the camera can pull RTSP from it directly, with
no Wi-Fi association in play.

```
+---------------------+              +-----------------------+
|  host (Pi / Mac)    |              |  AKASO 360            |
|  wlan0  internet ==>|              |  hostapd (still on,   |
|  usb0   10.42.0.2   |==============|        ignored)       |
|                     |  USB-C cable |  usb0    10.42.0.1    |
|                     |              |  AmbaRTSPServer:554   |
+---------------------+              +-----------------------+
```

Everything modified on the camera lives in tmpfs + bind mounts. A
power-cycle reverts to factory behavior. The persistent install just
puts a single executable script (`bootup.sh`) onto the camera's SD
card; the camera's stock init runs it at every boot via
`/etc/init.d/S99bootdone`.

## Setup steps

### 1. Install `bootup.sh` on the camera (one-time per physical camera)

This pushes `camera-bootup.sh` from this directory to the camera's SD
card, so the camera auto-configures USB Ethernet and the iw shim on
every boot.

```bash
# 1. Power on the camera.
# 2. Join the camera's Wi-Fi AP from your laptop or workstation. The
#    SSID is printed on the camera (format AK360_xxxx); the password
#    is printed under the SSID.
# 3. Run the installer from the repo root.
./akaso_360/install_camera_bootup.sh
```

To dry-run without uploading:

```bash
./akaso_360/install_camera_bootup.sh --dry-run
```

After install: power-cycle the camera. From then on, plugging the
USB-C cable into a host gives a working network path to `10.42.0.1`
with the RTSP gate open.

To revert: telnet into the camera, `rm /tmp/SD0/bootup.sh`,
power-cycle. The firmware reverts to factory defaults — no permanent
state was modified.

### 2. Configure the host's USB Ethernet interface

When you plug the USB-C cable into the host, the OS sees a new
network interface (typically `usb0` on Linux, an
`RNDIS/Ethernet Gadget` entry on macOS). It needs a static
`10.42.0.2/24` to talk to the camera.

**Linux with systemd-networkd:**

```bash
sudo cp akaso_360/10-akaso-camera.network /etc/systemd/network/
sudo systemctl restart systemd-networkd
```

**Linux with NetworkManager:**

```bash
sudo nmcli connection add type ethernet ifname usb0 con-name akaso-camera \
    ipv4.method manual ipv4.addresses 10.42.0.2/24 \
    ipv6.method ignore
sudo nmcli connection up akaso-camera
```

**macOS (manual; useful for dev):**

```bash
IFACE=$(networksetup -listallhardwareports | awk '/RNDIS/{p=1; next} p && /Device:/{print $2; exit}')
sudo ifconfig "$IFACE" 10.42.0.2 netmask 255.255.255.0 up
```

### 3. Verify

```bash
ping -c 2 10.42.0.1
go run ./cmd/cli -host 10.42.0.1
```

`./out/` should contain `raw.jpg`, `front.jpg`, `back.jpg`,
`equirectangular.jpg`, `pinhole.jpg`. If any step fails, see
[Troubleshooting](#troubleshooting) below.

### 4. Configure the Viam component

In your Viam machine config, add a `dtcurrie:camera-360:ambarella-camera`
component with:

```json
{ "host": "10.42.0.1" }
```

See [`../dtcurrie_camera-360_ambarella-camera.md`](../dtcurrie_camera-360_ambarella-camera.md)
for the full config schema and DoCommand semantics.

## Files in this directory

| File                          | Purpose                                                                  |
| ----------------------------- | ------------------------------------------------------------------------ |
| `camera-bootup.sh`            | Runs on the camera at every boot. Brings up USB Ethernet + iw shim       |
| `install_camera_bootup.sh`    | Pushes `camera-bootup.sh` to the camera's SD card via telnet over the AP |
| `10-akaso-camera.network`     | systemd-networkd unit for the host side; assigns `10.42.0.2/24` to `usb*` |
| `probe/`                      | Read-only probe scripts used to reverse-engineer the firmware            |

## Troubleshooting

**`install_camera_bootup.sh` reports `FAIL: 192.168.42.1 unreachable`** —
the host isn't joined to the camera's Wi-Fi AP. Join it and re-run.

**Camera's SD card not present at `/tmp/SD0`** — insert an SD card and
retry. The persistence hook reads from there.

**`ping 10.42.0.1` fails after power-cycle** — `bootup.sh` may not have
run (`/etc/init.d/S99bootdone` is missing on some firmware variants) or
USB Ethernet didn't come up. Briefly rejoin the camera's Wi-Fi AP,
telnet in (`telnet 192.168.42.1`, user `root`, no password), and check
`/tmp/SD0/bootup.log`. The script logs each step there.

**Ping works but RTSP returns `unexpected EOF`** — the iw shim didn't
take effect. Telnet in and verify with `mount | grep iw`; you should
see two bind-mount lines. If empty, your `bootup.sh` didn't run on this
boot. Power-cycle and retry; if it still fails, check
`/tmp/SD0/bootup.log` for the failing step.

**Different host interface name on Linux** — the `10-akaso-camera.network`
unit matches `usb*`. If your distro renames USB Ethernet interfaces
(e.g. `enxAABBCCDDEEFF` with systemd's predictable names), edit the
`[Match]` block to use `Driver=cdc_ether rndis_host` instead.

---

# Reverse-engineering notes

The remainder of this document captures what we learned while figuring
out how to talk to this camera, in case you're trying to port the
playbook to a sibling Ambarella-based device, or you want to understand
why the setup steps above are necessary.

All probe scripts in `probe/` are read-only against a live camera
unless explicitly noted. Logs go to `/tmp/akaso_*.log`. Scripts that
need credentials read them from env vars (`CAM_AP_SSID`,
`CAM_AP_PASS`, `HOST_SSID`) — there are no hardcoded values.

## Camera hardware / firmware identity

- **SoC:** Ambarella S5L (also marketed as Ambarella H22)
- **Wi-Fi chip:** Realtek RTL8821CS (dual-band, full Linux support, OUI
  `80:9d:65`)
- **OS:** Buildroot 2016.08.1 — Linux 4.9.76 ARMv7 SMP
- **Hostname:** `AmbaLink`
- **Firmware label:** `WATERM_V1.0.5.2` (returned by `msg_id 11` over
  the Ambarella protocol). May differ on your unit.
- **Lens system:** dual ~200° fisheye, opposed; live preview is raw
  dual-fisheye side-by-side at 1920×960. The camera does NOT stitch in
  real time — that happens in the AKASO Go app, the desktop AKASO 360
  Studio, or in this module.
- **SD card mount:** `/tmp/SD0` (typical retail card sizes), also
  bind-mounted at `/var/www` for the on-board Cherokee HTTP server.
- **Internal codename clue:** `/tmp/pref/bt.conf` advertises the device
  as `QooCam` (Kandao QooCam family) — AKASO appears to white-label this
  firmware.

## Network surface (Wi-Fi AP mode, factory default)

When the camera is in default AP mode (`AK360_xxxx`, password printed on
the camera label):

| Port  | Proto | Service                    | Notes                                              |
| ----- | ----- | -------------------------- | -------------------------------------------------- |
| 23    | TCP   | telnet                     | **root login, no password** — full r/w access      |
| 53    | TCP   | dnsmasq                    | AP DHCP+DNS                                        |
| 80    | TCP   | Cherokee HTTP              | Serves `/tmp/SD0` (SD card index); no API          |
| 554   | TCP   | RTSP (AmbaRTSPServer)      | `/live` only valid while preview is on             |
| 7878  | TCP   | Ambarella JSON control     | The real control plane                             |

Camera IP on AP: `192.168.42.1`. DHCP range: `192.168.42.2`–`192.168.42.6`
(5 client slots — small but enough).

The same ports are open on `usb0` (10.42.0.1) once the USB gadget is
loaded.

## Ambarella JSON protocol (TCP 7878)

JSON messages over TCP with **no delimiter between messages** — you
must brace-count to find object boundaries. Only one client connection
allowed at a time. Session-based with a token.

### Minimum-viable handshake for live preview

```
1. send {"msg_id":257,"token":0}                       # start_session
   recv {"rval":0,"msg_id":257,"param":<TOKEN>}        # save TOKEN

2. send {"msg_id":259,"param":"none_force","token":T}  # start_preview
   recv {"rval":0,"msg_id":259}

3. open rtsp://<camera_ip>:554/live
   stream is H.264 Main, 1920x960, 29.97 fps, yuvj420p, BT.709,
   DAR 2:1, title="Ambarella streaming"
   IMPORTANT: this is raw dual fisheye side-by-side, NOT pre-stitched ERP

4. teardown: msg_id 260 (stop_preview), then msg_id 258 (end_session)
```

Notes:

- Setting `stream_out_type=rtsp` (msg_id 2) is NOT needed and returns
  `rval:-1` on this firmware — RTSP is the default
- The fallback URL `rtsp://<camera_ip>/H264` referenced by some AKASO
  Go app decompilations returns 404 on this firmware; only `/live` works
- `rval = -4` means session token invalid; re-handshake
- `rval = -7` means msg_id unsupported on this firmware
- Camera clock skew is real (firmware boots thinking it's 2014-01-01
  until the app syncs it); don't trust the camera's clock for timestamps

### Useful read-only commands

- `msg_id: 3` — `get_all_settings`. On this firmware returns only:
  `video_quality`, `video_resolution` (always `"unknown"` for live),
  `default_setting`, `camera_clock`, `video_coding_format` (`"H.264"`)
- `msg_id: 11` — firmware info. Returns `fw_ver`, `sn`, `is_activated`,
  `media_folder` (SD card recording path)
- `msg_id: 1` with `type:"app_status"` — returns `"vf"` (viewfinder)
  when preview is running

### What's NOT exposed

- `msg_id: 4` (`get_all_setting_options`) returns `rval:-7` — firmware
  refuses to enumerate settings
- No Wi-Fi config msg_ids work (1024–1056 all return `-7`)
- 18 candidate Wi-Fi setting names probed via msg_id 1 — none match.
  Wi-Fi mode is NOT exposed through this protocol.

## Telnet shell access (TCP 23)

**Credentials: `root` / (empty password).** No authentication required
past the username. The shell is a normal busybox-ish Linux environment
with full root privileges.

Default login banner: `AmbaLink login: `.

### Filesystem layout

```
/dev/root       squashfs (read-only)   — system files, scripts, binaries
/dev            devtmpfs               — devices
/tmp            tmpfs   (writable, ~26 MB) — runtime configs, logs
/run            tmpfs                  — pid files
/pref           tmpfs                  — user preferences (lost on reboot)
/tmp/FL0        ambafs                 — internal flash partition
/tmp/SD0        ambafs                 — microSD card mount (persistent)
/var/www        c: (same as /tmp/SD0)  — Cherokee document root
```

**Any modification to system files is impossible (squashfs is RO), but
anything in `/tmp`, `/pref`, or the SD card is writable.** Power-cycling
the camera resets `/tmp` and `/pref`; only the SD card survives.

### Useful binaries available

`busybox` provides most of the toolbox. Notable extras / quirks:

- `iw`, `iwconfig`, `wpa_supplicant`, `wpa_cli`, `hostapd`, `hostapd_cli`
- `udhcpc` (DHCP client), `dnsmasq`
- `mount --bind` works (used by the USB-Ethernet bypass)
- `md5sum`, `tr`, `printf`, `od` are present
- `base64`, `nc`, `pgrep`, `wget` (with limits) are **missing** — design
  any installer around what's actually here

## The AP-client gate (and how to bypass it)

This is the most surprising finding. AmbaRTSPServer's behavior:

- TCP port 554 is `LISTEN` on `0.0.0.0:554` — appears reachable on any
  interface, including USB
- A client can complete the TCP three-way handshake
- The moment the client sends a real RTSP request (DESCRIBE), the
  server sends RST and closes the connection
- This happens unless at least one Wi-Fi station is associated to
  `hostapd` on `wlan0`

`strings /usr/bin/AmbaRTSPServer` reveals the literal command embedded
in the binary:

```
iw dev wlan0 station dump |grep Station
```

So AmbaRTSPServer (per-request, via `popen` or `system`) runs `iw dev
wlan0 station dump` and greps for the string "Station". If grep matches
nothing, the server refuses to serve.

### Bypass: shim `iw`

`iw` lives at `/sbin/iw`. AmbaRTSPServer's `$PATH` is
`/sbin:/usr/sbin:/bin:/usr/bin`, so `/sbin/iw` is found first.

The root filesystem is squashfs (read-only), so we can't modify
`/sbin/iw` in place. But Linux `mount --bind` lets us overlay a
writable replacement onto the read-only path:

```sh
# 1. Save the real iw to tmpfs.
cp /sbin/iw /tmp/iw.real

# 2. Write a shim that fakes the gate check.
cat > /tmp/iw <<'SHIM'
#!/bin/sh
if [ "$1" = "dev" ] && [ "$3" = "station" ] && [ "$4" = "dump" ]; then
    echo "Station 02:00:00:00:00:01 (on $2)"
    exit 0
fi
exec /tmp/iw.real "$@"
SHIM
chmod +x /tmp/iw

# 3. Bind-mount over both paths AmbaRTSPServer might use.
mount --bind /tmp/iw /sbin/iw
mount --bind /tmp/iw /usr/sbin/iw

# 4. Restart AmbaRTSPServer so any cached state is cleared.
killall -9 AmbaRTSPServer
/usr/bin/AmbaRTSPServer &
```

After this, `iw dev wlan0 station dump | grep Station` returns rc=0
regardless of real Wi-Fi state, and AmbaRTSPServer serves clients over
any interface — including USB Ethernet. Power-cycle reverts everything.

The shim has to use spaces (not tabs) when emitting the fake station
block; file transport over telnet eats tab characters.

## Persistence: `/etc/init.d/S99bootdone`

The squashfs root means we can't add init scripts, but the camera's
init has one customization hook:

```
$ cat /etc/init.d/S99bootdone
#!/bin/sh
case "$1" in
    start)
        if [ -x /tmp/SD0/bootup.sh ]; then
            /tmp/SD0/bootup.sh
        fi
        ;;
    *) ;;
esac
```

So the entire camera-side install is: put an executable
`bootup.sh` on the SD card at `/tmp/SD0/bootup.sh`. It runs last in init
on every boot. This directory ships `camera-bootup.sh` for this purpose,
which does USB Ethernet + iw shim + AmbaRTSPServer restart, logging
each step to `/tmp/SD0/bootup.log`.

To revert: delete `/tmp/SD0/bootup.sh` and power-cycle. No firmware
mutation, no permanent state.

## Wi-Fi STA mode (an alternative path we explored)

The firmware can be coerced into Wi-Fi station mode (join an existing
AP rather than running its own). We confirmed this works but ultimately
preferred USB Ethernet because (a) corporate / public Wi-Fi networks
often enforce **AP client isolation** that prevents peer reachability,
and (b) USB is cable-bound and immune to RF flakiness.

The notes below preserve what was learned, in case STA mode is the
right answer for your environment.

### The decision file

`/tmp/pref/wifi.conf` — runtime preferences file. Lives in tmpfs.
Created from a template at `/usr/local/share/script/wifi.conf`.

The critical line:

```
WIFI_MODE=ap     # ap | sta | p2p
```

The STA section in the file is already populated with placeholder creds
(`ESSID=CMW-AP`, `PASSWORD=12345678`), waiting for real values.

### Existing scripts (`/usr/local/share/script/`)

```
wifi_start.sh        — main orchestrator (reads wifi.conf, dispatches)
wifi_stop.sh         — clean shutdown of current mode
wifi_status.sh       — query current state
ap.sh / ap_start.sh  — AP-mode startup
sta.sh / sta_start.sh — STA-mode startup
p2p.sh / p2p_start.sh — Wi-Fi Direct
wpa_event.sh         — wpa_supplicant event handler
*.conf templates     — mode-specific configs
```

`sta_start.sh` has one important gotcha: it hardcodes a static IP from
the factory test lab:

```sh
ifconfig wlan0 192.168.48.130 netmask 255.255.255.0
route add default gw 192.168.48.1
#udhcpc -i wlan0 -A 1 -b    ← commented out
```

For STA mode on a real network you must either skip `sta_start.sh` and
replicate its logic with `udhcpc`, or run `udhcpc` immediately after.

`sta_start.sh` auto-detects security type (WPA / WPA2 / WEP / open) from
`wpa_cli scan_r` output and writes an appropriate `wpa_supplicant.conf`
with the right `key_mgmt` / `proto` / `pairwise` — you only need to
supply SSID + passphrase, not the security type.

### Factory artifact: `/usr/local/share/script/wifi.sta.conf`

```
WIFI_MODE=sta
ESSID=HYT-WiFi
PASSWORD=Hong#Yuan*Tai
```

The AKASO/Ambarella factory QA network's credentials, baked into the
firmware image. Confirms STA mode was used during manufacturing — the
firmware just ships configured for AP mode by default. No security
implication; the credentials would only work on a network at the
factory.

### How to flip the camera to STA mode at runtime

This is what [`probe/sta_switch.sh`](probe/sta_switch.sh) does:

1. `wifi_stop.sh` to kill `hostapd` / `dnsmasq`
2. Clean stale `/var/run/wpa_supplicant` and `/var/run/hostapd` sockets
3. Write a `WPA-PSK` `/tmp/wpa_supplicant.conf` with target SSID and PSK
4. Bounce `wlan0` and start `wpa_supplicant -D nl80211 -B`
5. `udhcpc -i wlan0 -A 5 -b -t 8` for the DHCP lease
6. Persist a diagnostic log to `/tmp/SD0/akaso_sta_switch.log` so you
   can read it after a recovery power-cycle if anything goes wrong

The change is **ephemeral** (everything in tmpfs / `/var/run/`) — a
power-cycle restores AP mode. To make persistent, add `wifi.conf` and
the wpa_supplicant.conf manipulation to `camera-bootup.sh`.

### AP client isolation gotcha

Many corporate / public Wi-Fi networks enforce client isolation:
stations can reach the gateway but not each other. The camera will
associate and get DHCP successfully, but a peer (your laptop / Pi) on
the same network won't be able to reach it. Verified empirically against
a corporate Wi-Fi — full L4 reachability to the gateway, zero ARP
response from the camera's peer-side address.

If you're hitting this, the workarounds are:

- A home / dev Wi-Fi without isolation
- A phone hotspot (typically no isolation)
- A small dedicated AP that both camera and host join
- USB Ethernet instead (what this repo settled on)

## Probe scripts

Read these in order if you want to retrace the discovery path:

| Script                          | What it does                                            |
| ------------------------------- | ------------------------------------------------------- |
| `probe/probe.sh`                | First-contact port scan + Ambarella `msg_id:11` probe   |
| `probe/telnet_probe.sh`         | Confirms root telnet and dumps the firmware bill of materials |
| `probe/settings_probe.sh`       | Enumerates which Ambarella settings work (most don't)   |
| `probe/stream_probe.sh`         | Full handshake + 5 second frame capture                 |
| `probe/wifi_recon.sh`           | Locates `/tmp/pref/wifi.conf` and the Wi-Fi scripts     |
| `probe/wifi_scripts.sh`         | Dumps every script in `/usr/local/share/script/`        |
| `probe/sta_switch.sh`           | Flips the camera into STA mode (joins your Wi-Fi)       |
| `probe/find_camera.sh`          | After STA switch, ARP-scans the local subnet to find it |
| `probe/read_switchlog.sh`       | Recovery: reads `/tmp/SD0/akaso_sta_switch.log` after power-cycle |
| `probe/usb_recon.sh`             | Reads the camera's USB gadget scripts (`usb_ether.sh`)  |
| `probe/usb_ether_phaseA.sh`     | One-shot: brings up USB Ethernet via telnet (no persist)|
| `probe/ap_gate_recon.sh`        | Investigates what gates RTSP behind an AP client        |
| `probe/ap_gate_test.sh`         | Definitive test: AP-on -> 4 JPEGs, AP-off -> 0          |
| `probe/iw_shim_recon.sh`        | Finds `iw`, confirms `mount --bind` works               |
| `probe/iw_shim_install.sh`      | Installs the `iw` shim manually (no persist)            |
| `probe/postboot_inspect.sh`     | Joins AP, telnets in, verifies `bootup.sh` ran on boot  |

All scripts:

- log to `/tmp/akaso_<name>.log` so you can grab a transcript later
- expect to be run from the repo root (`bash akaso_360/probe/<name>.sh`)
- read credentials from env vars where required:

  ```sh
  export CAM_AP_SSID="AK360_xxxx"
  export CAM_AP_PASS="..."          # printed on camera label
  export HOST_SSID="YourWiFi"       # to return to after the script
  ```

## Open questions

1. **What invokes `wifi_start.sh` at boot?** `/etc/init.d/S91wifi` has
   its case-statement commented out — it's a no-op. `/etc/init.d/rcS`,
   `bt_start.sh`, and `wifi_mdev_action.sh` reference `wifi_start` —
   `rcS` is probably the master init. Worth tracing if you want STA
   mode to persist by editing config rather than scripts.
2. **Does the Wi-Fi chip support concurrent AP+STA mode?** Some RTL8821
   variants can. `iw list` partial output suggests yes but we didn't
   verify the data plane works. If yes, you could keep the camera AP up
   for fallback access while also joining your Wi-Fi.
3. **Is the random gadget MAC a problem?** `g_ether` regenerates the
   gadget's MAC on every boot. This means matching the USB Ethernet
   interface by MAC OUI on the host side is fragile; better to match by
   driver (`cdc_ether`, `rndis_host`) or by interface-name pattern
   (`usb*`) as the systemd-networkd unit in this directory does.
