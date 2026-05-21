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

## Models

- [`dtcurrie:camera-360:camera`](dtcurrie_camera-360_camera.md) —
  configuration reference, source list, DoCommand schema, troubleshooting

## Tested cameras

| Camera        | Notes                                                                                                                        |
| ------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| **AKASO 360** | Requires a one-time camera-side install (USB Ethernet + RTSP gate bypass). See [`akaso_360/README.md`](akaso_360/README.md). |

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

## Quickstart

Once your camera is reachable (e.g. `ping 10.42.0.1` works), the
component config is just:

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

# Smoke-test against a live camera
go run ./cmd/cli -host <camera-ip>

# Stitching-only test against a captured dual-fisheye image
go run ./cmd/offline -in path/to/captured.jpg

# Unit tests (no hardware required)
go test ./...
```

The `cmd/cli` writes one JPEG per source to `./out/` so you can visually
inspect what each stage of the pipeline produces.

## Repository layout

```
camera-360/
├── module.go, capture.go, session.go    # driver
├── fisheye.go, pinhole.go               # stitching + projection
├── cmd/                                  # CLI tools (cli, offline, measure)
├── akaso_360/                            # AKASO-specific setup + reverse-engineering
└── dtcurrie_camera-360_camera.md         # model documentation
```
