# camera-360

A Viam camera component for 360-degree cameras reachable via RTSP. Pulls
the live H.264 stream, runs configurable dual-fisheye stitching to
produce an equirectangular panorama, and exposes a steerable virtual
pinhole view on top of the ERP.

The driver is camera-agnostic by design: anything that serves a single
RTSP stream containing a 360 view (raw dual-fisheye side-by-side, or a
pre-stitched equirectangular feed) can in principle be configured against
it. The per-camera quirks live in vendor-specific subdirectories under
this repo.

A second class of camera is also supported: standard **USB (UVC) 360
webcams** such as the j5create JVCU360. These plug-and-play devices
dewarp on-board, so the module captures their output directly over UVC
(via ffmpeg — `v4l2` on Linux, `avfoundation` on macOS) with no RTSP,
stitching, or vendor handshake. They also expose a microphone as a
standard `audio_in`. See [`jvcu360/README.md`](jvcu360/README.md).

## Models

- [`dtcurrie:camera-360:akaso-360-camera`](dtcurrie_camera-360_akaso-360-camera.md) —
  Ambarella RTSP 360 camera (dual-fisheye stitch + pinhole). Configuration
  reference, source list, DoCommand schema, troubleshooting
- [`dtcurrie:camera-360:jvcu360-camera`](dtcurrie_camera-360_jvcu360-camera.md) —
  j5create JVCU360 USB 360 webcam; emits its 360 All-Around band tagged with GPano
  cropped-area metadata
- [`dtcurrie:camera-360:jvcu360-mic`](dtcurrie_camera-360_jvcu360-mic.md) — USB (UAC)
  microphone exposed as `audio_in` (tested on the JVCU360's built-in mic)
- [`dtcurrie:camera-360:discovery`](dtcurrie_camera-360_discovery.md) — discovery
  service that finds connected UVC webcams (Linux) and emits ready-to-paste
  `jvcu360-camera` / `jvcu360-mic` configs with the correct device handles

## Tested cameras

| Camera               | Notes                                                                                                                                    |
| -------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| **AKASO 360**        | RTSP/Wi-Fi. Requires a one-time camera-side install (USB Ethernet + RTSP gate bypass). See [`akaso_360/README.md`](akaso_360/README.md). |
| **j5create JVCU360** | USB (UVC) plug-and-play, no setup. Pass-through camera + mic. See [`jvcu360/README.md`](jvcu360/README.md).                              |

If you bring up a new camera with this module, a PR adding a section
here and a vendor subdirectory with setup notes / scripts is welcome.

## General prerequisites

Regardless of camera:

- The host running the module needs `ffmpeg` on `PATH`. The driver
  shells out to it for H.264 RTSP decoding.
- The host needs IP reachability to the camera. Whatever address you
  configure as `host` must be pingable from the machine running the
  Viam server.

Vendor-specific subdirectories under this repo describe the network /
firmware setup steps for each tested camera.

## USB (UVC) cameras

USB 360 webcams (e.g. the JVCU360) need no network setup, but there are
two host-OS specifics worth knowing up front. Full per-model
configuration is in [`jvcu360/README.md`](jvcu360/README.md).

**macOS camera & microphone permissions.** Access is gated by macOS
Privacy (TCC) and granted to the app that _runs the capture_ — your
terminal during dev, or the `viam-server` service in production. The
first capture triggers a system prompt; if it's missed or was previously
denied, capture silently fails. To set it up or debug it, run the helper
from the same terminal you'll launch the module from:

```bash
bash jvcu360/macos-permissions.sh
```

It auto-detects the camera/mic, provokes the prompts, opens the Privacy
panes, and verifies it can read a frame and audio. A script cannot
_grant_ TCC access (Apple requires explicit user consent) — it surfaces
the prompt for you to approve and points you at the right settings.

> [!IMPORTANT]
> **macOS caps capture at 720p.** On macOS, `avfoundation` only exposes
> _raw_ video for these devices — it cannot request the camera's native
> MJPEG. The JVCU360's uncompressed 1080p mode advertises but does not
> actually stream (it emits a single frozen frame), so on macOS the
> module defaults to and hard-caps capture at **1280×720**, logging a
> one-time notice if it has to cap a larger request. **Linux** pulls the
> native MJPEG over V4L2 and runs at full **1080p**, so deploy targets
> are unaffected.

## Discovering USB cameras

Rather than hand-pick a `video_device`, add the
[`dtcurrie:camera-360:discovery`](dtcurrie_camera-360_discovery.md) service and
let it find connected UVC webcams for you. It returns ready-to-paste
`jvcu360-camera` (and `jvcu360-mic`) configs with the correct `/dev/videoN` and ALSA
handles already filled in — which is the easiest fix for the **Raspberry Pi**
gotcha where the camera is at `/dev/video8+` (the Pi's internal ISP/codec blocks
take the low numbers) and the `jvcu360-camera` default of `/dev/video0` fails.

An empty config discovers 360/fisheye webcams with their mics:

```json
{}
```

Set `include_all_uvc` to `true` to return every UVC webcam (not just ones
detected as 360/fisheye). Detection is **Linux-only**. Full reference:
[`dtcurrie_camera-360_discovery.md`](dtcurrie_camera-360_discovery.md).

You can preview what it will find from the CLI:

```bash
go run ./cmd/uvc -list
```

## Quickstart (RTSP camera)

For a USB (UVC) camera there's no setup — see
[`jvcu360/README.md`](jvcu360/README.md). For an RTSP camera, once it's
reachable, an **empty config works** — `host` defaults to `192.168.42.1`
(the AKASO Wi-Fi hotspot address):

```json
{}
```

`host` is the only commonly-set field, and only when your camera is at a
different address (it's specific to the RTSP model — the USB models use
`video_device` / `audio_device` instead):

```json
{
  "host": "10.42.0.1"
}
```

Add the component to your Viam machine config and the module will:

1. Open the Ambarella JSON control channel on TCP 7878 to unlock the
   live preview (no-op if your camera doesn't need it — see model docs)
2. Pull the RTSP stream at `rtsp://<host>:554/live`
3. Decode frames via ffmpeg, stitch dual fisheye → ERP, project ERP →
   pinhole
4. Emit five named-image sources (`raw`, `front`, `back`,
   `equirectangular`, `pinhole`)

Full configuration schema and DoCommand semantics are in
[`dtcurrie_camera-360_akaso-360-camera.md`](dtcurrie_camera-360_akaso-360-camera.md).

## Development

```bash
# Build the module bundle
make module

# Smoke-test against a live RTSP camera
go run ./cmd/cli -host <camera-ip>

# Stitching-only test against a captured dual-fisheye image
go run ./cmd/offline -in path/to/captured.jpg

# USB (UVC) cameras: enumerate devices, capture frames / audio
go run ./cmd/uvc -list
go run ./cmd/uvc -capture -frames 30 -video-device <idx-or-/dev/videoN> -out out
go run ./cmd/uvc -audio -seconds 3 -audio-device <dev> -out out

# Unit tests (no hardware required)
go test ./...
```

The `cmd/cli` and `cmd/uvc` tools write JPEG frames (and `mic.wav`) to
`./out/` so you can visually inspect what each source produces.

### Playground (local UI)

For an interactive, cloud-free dev loop, [`playground/`](playground/README.md)
is a SvelteKit app that renders Viam's
[test-widgets](https://github.com/viamrobotics/test-widgets) against a companion
local `viam-server`:

```bash
make playground                        # builds the module, runs viam-server + the app together
```

## Repository layout

```
camera-360/
├── capture.go, audiocapture.go          # ffmpeg frame/PCM capture (UVC + RTSP)
├── session.go                           # Ambarella JSON-over-TCP control handshake
├── fisheye.go, pinhole.go               # dual-fisheye stitching + pinhole projection
├── platform.go                          # per-OS ffmpeg input args, device defaults
├── xmp.go                               # equirectangular + GPano XMP tagging
├── models.go, sources.go                # model identifiers + shared source constants
├── discovery.go, enumerate.go           # UVC webcam discovery service (Linux sysfs)
├── akaso_360/akaso.go                   # akaso-360-camera model (+ AKASO setup scripts)
├── jvcu360/jvcu360.go, jvcu360/mic.go   # jvcu360-camera + jvcu360-mic models
├── jvcu360/xu/                          # JVCU360 UVC Extension-Unit probe (mode control)
├── cmd/                                  # CLI tools (module, cli, uvc, jvcu360, …)
├── playground/                          # SvelteKit dev app + local viam-server config
└── dtcurrie_camera-360_*.md             # per-model documentation
```
