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

- [`dtcurrie:camera-360:camera`](dtcurrie_camera-360_camera.md) —
  RTSP 360 camera (dual-fisheye stitch + pinhole). Configuration
  reference, source list, DoCommand schema, troubleshooting
- [`dtcurrie:camera-360:jvcu360`](jvcu360/README.md) — USB (UVC) 360
  webcam, pass-through
- [`dtcurrie:camera-360:jvcu360-mic`](jvcu360/README.md) — the JVCU360's
  built-in omnidirectional microphone (`audio_in`)

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

## Quickstart (RTSP camera)

For a USB (UVC) camera there's no setup — see
[`jvcu360/README.md`](jvcu360/README.md). For an RTSP camera, once it's
reachable (e.g. `ping 10.42.0.1` works), the component config is just:

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
[`dtcurrie_camera-360_camera.md`](dtcurrie_camera-360_camera.md).

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
├── module.go, session.go                 # RTSP 360 camera driver (Ambarella)
├── fisheye.go, pinhole.go               # stitching + projection
├── capture.go                            # ffmpeg frame capture (UVC + RTSP)
├── uvc.go, platform.go                   # USB (UVC) pass-through camera
├── mic.go, audiocapture.go               # USB (UAC) audio_in microphone
├── cmd/                                  # CLI tools (cli, offline, measure, uvc)
├── akaso_360/                            # AKASO-specific setup + reverse-engineering
├── jvcu360/                              # j5create JVCU360 docs + UVC probes
├── playground/                           # SvelteKit dev app + local viam-server config
└── dtcurrie_camera-360_camera.md         # RTSP model documentation
```
