# Model `dtcurrie:camera-360:akaso-360-camera`

A 360-camera driver that pulls a single RTSP H.264 stream, stitches
dual-fisheye input into an equirectangular (ERP) panorama, and exposes
a steerable virtual pinhole view derived from the ERP. The stitched ERP
source is tagged `viam:equirectangular` (a true full 360°×180° sphere).
Per-camera network setup is documented separately under each vendor
subdirectory (e.g. [`akaso_360/`](akaso_360/) for the AKASO 360).

> [!NOTE]
> Verified on the **AKASO 360**, but the control path is the generic
> **Ambarella WiFi protocol** (a JSON-over-TCP handshake on port 7878 that
> unlocks the RTSP preview), which other Ambarella action cams share — hence
> the internal protocol code is named `ambarella`/`Session`. A sibling Ambarella
> 360 camera could be supported by a thin new model reusing the same internals.

## Sources

A single `Images()` call returns five named-image sources, in this order:

| Source            | Type       | Cost   | What it is                                                                                             |
| ----------------- | ---------- | ------ | ------------------------------------------------------------------------------------------------------ |
| `raw`             | image/jpeg | free   | The full frame from the camera, exactly as decoded by ffmpeg (typically 1920×960 side-by-side fisheye) |
| `front`           | image/jpeg | cheap  | Left half of the raw frame (front fisheye hemisphere). Crop only — no projection                       |
| `back`            | image/jpeg | cheap  | Right half of the raw frame (back fisheye hemisphere). Crop only — no projection                       |
| `equirectangular` | image/jpeg | medium | Stitched ERP panorama built from `front` + `back` via the configured lens model. What `Stream()` emits |
| `pinhole`         | image/jpeg | medium | Virtual pinhole derived from the ERP using gnomonic projection. Aim via `DoCommand`                    |

`Images(ctx, filterSourceNames, ...)` honors the filter: pass
`["pinhole"]` to skip the expensive stitch when you only need the
pinhole view. `Stream()` emits the `equirectangular` source.

## Configuration

### Minimal

```json
{
  "host": "10.42.0.1"
}
```

That's enough to bring the camera online with sensible defaults for
everything else.

### Full schema

```json
{
  "host": "10.42.0.1",

  "pinhole_width": 1280,
  "pinhole_height": 720,
  "pinhole_fov_deg": 90.0,
  "initial_yaw_deg": 0.0,
  "initial_pitch_deg": 0.0,

  "erp_width": 1920,
  "erp_height": 960,
  "seam_feather_deg": 6.0,
  "back_extrinsic_yaw_deg": 0.0,
  "back_extrinsic_pitch_deg": 0.0,
  "back_extrinsic_roll_deg": 0.0,
  "lens_model": "equisolid",

  "front_lens": {
    "center_x": 480,
    "center_y": 480,
    "radius": 480,
    "fov_deg": 200.0,
    "rotation_deg": 0.0
  },
  "back_lens": {
    "center_x": 480,
    "center_y": 480,
    "radius": 480,
    "fov_deg": 200.0,
    "rotation_deg": 0.0
  }
}
```

### Attributes

| Name                       | Type   | Inclusion | Default        | Description                                                                                   |
| -------------------------- | ------ | --------- | -------------- | --------------------------------------------------------------------------------------------- |
| `host`                     | string | Required  | `192.168.42.1` | IP address or hostname of the camera. Must be reachable from the host running the module      |
| `pinhole_width`            | int    | Optional  | `1280`         | Width of the `pinhole` output in pixels                                                       |
| `pinhole_height`           | int    | Optional  | `720`          | Height of the `pinhole` output in pixels                                                      |
| `pinhole_fov_deg`          | float  | Optional  | `90.0`         | Horizontal field-of-view of the pinhole view, in degrees. Range `(0, 180)`                    |
| `initial_yaw_deg`          | float  | Optional  | `0.0`          | Starting yaw of the pinhole view, in degrees                                                  |
| `initial_pitch_deg`        | float  | Optional  | `0.0`          | Starting pitch of the pinhole view, in degrees                                                |
| `erp_width`                | int    | Optional  | `1920`         | Width of the stitched ERP output in pixels                                                    |
| `erp_height`               | int    | Optional  | `960`          | Height of the stitched ERP output in pixels                                                   |
| `seam_feather_deg`         | float  | Optional  | `6.0`          | Half-width (in degrees of longitude) of the seam-blend region where both lenses contribute    |
| `back_extrinsic_yaw_deg`   | float  | Optional  | `0.0`          | Yaw correction applied to the back lens during stitching (mechanical mis-alignment)           |
| `back_extrinsic_pitch_deg` | float  | Optional  | `0.0`          | Pitch correction applied to the back lens                                                     |
| `back_extrinsic_roll_deg`  | float  | Optional  | `0.0`          | Roll correction applied to the back lens                                                      |
| `lens_model`               | string | Optional  | `equisolid`    | Fisheye projection model. One of `equisolid` (`r = 2f·sin(θ/2)`) or `equidistant` (`r = f·θ`) |
| `front_lens`               | object | Optional  | see below      | Image-plane parameters for the front fisheye                                                  |
| `back_lens`                | object | Optional  | see below      | Image-plane parameters for the back fisheye                                                   |

### Lens object (`front_lens` / `back_lens`)

| Name           | Type  | Default | Description                                                                          |
| -------------- | ----- | ------- | ------------------------------------------------------------------------------------ |
| `center_x`     | int   | `480`   | X coordinate (in the lens's half-frame) of the fisheye circle's optical center       |
| `center_y`     | int   | `480`   | Y coordinate of the fisheye circle's optical center                                  |
| `radius`       | int   | `480`   | Radius in pixels of the usable image circle                                          |
| `fov_deg`      | float | `200.0` | Diagonal field of view of the lens, in degrees. Typical 360 cameras: 180–220         |
| `rotation_deg` | float | `0.0`   | Rotation applied to the lens's coordinate frame (compensates for sensor orientation) |

Defaults assume a 1920×960 dual-fisheye source frame split into two
960×960 halves. For other source resolutions, divide each value
proportionally.

## DoCommand

Runtime configuration of pinhole aim and per-lens calibration. All
commands are JSON objects with one or more top-level keys.

### `set_pinhole`

Re-aim the pinhole view without redeploying. All sub-fields are
optional; only provided fields change.

```json
{
  "set_pinhole": {
    "yaw_deg": 90,
    "pitch_deg": -10,
    "fov_deg": 100
  }
}
```

Returns `{"set_pinhole": "ok"}` on success.

### `get_pinhole`

```json
{ "get_pinhole": {} }
```

Returns the currently-active pinhole parameters:

```json
{
  "get_pinhole": {
    "yaw_deg": 90,
    "pitch_deg": -10,
    "fov_deg": 100,
    "width": 1280,
    "height": 720
  }
}
```

### `set_stitch`

Per-lens calibration at runtime. Useful for tuning seam alignment
without redeploying. Any subset of fields is accepted; unspecified
fields are left at their current values.

```json
{
  "set_stitch": {
    "seam_feather_deg": 8.0,
    "back_extrinsic_yaw_deg": 1.5,
    "lens_model": "equisolid",
    "front_lens": {
      "center_x": 482,
      "center_y": 478,
      "radius": 478,
      "fov_deg": 198.0
    },
    "back_lens": {
      "radius": 482
    }
  }
}
```

Changes apply on the next `Images()` / `Stream()` call. The stitching
LUT is rebuilt under a lock — overhead is one frame's worth of CPU
work the first time after a parameter change.

### `get_stitch`

```json
{ "get_stitch": {} }
```

Returns the current stitch parameters in the same shape as `set_stitch`.

## Example configurations

### AKASO 360 over USB Ethernet (after running the camera-side install)

```json
{
  "host": "10.42.0.1"
}
```

### AKASO 360 over Wi-Fi AP

```json
{
  "host": "192.168.42.1"
}
```

### Tuning seam alignment for a unit with mechanical mis-alignment

After confirming the raw `front` and `back` images look correct, but
the seam in `equirectangular` ghosts:

```json
{
  "host": "10.42.0.1",
  "seam_feather_deg": 10.0,
  "back_extrinsic_yaw_deg": 1.2,
  "back_extrinsic_pitch_deg": -0.3,
  "back_lens": {
    "radius": 478
  }
}
```

Then iterate with `set_stitch` DoCommands until the seam is invisible.

## Troubleshooting

**`dial tcp <host>:7878: i/o timeout`** — the host running the module
can't reach the camera. Check `ping <host>` from that machine. If
ping fails, your network setup is wrong (the IP isn't on the right
interface, or the cable isn't plugged in). Vendor-specific setup
docs (e.g. [`akaso_360/README.md`](akaso_360/README.md)) cover the
expected interface configuration.

**`ffmpeg session ended; will retry` repeatedly, then context
deadline exceeded** — TCP to the camera works, but the RTSP server
isn't serving the stream. For the AKASO 360 specifically this usually
means the AP-client gate bypass isn't active; see
[`akaso_360/README.md`](akaso_360/README.md). For other cameras,
check that the RTSP URL `rtsp://<host>:554/live` is what your camera
serves (some use different paths).

**`ffmpeg: executable file not found in $PATH`** — install ffmpeg on
the host. On Debian / Ubuntu / Raspberry Pi OS: `sudo apt install
ffmpeg`.

**Blank or garbled frames** — pull `raw.jpg` from `go run ./cmd/cli`
to see what the camera is actually sending. If `raw.jpg` is correct
but `equirectangular.jpg` is broken, it's a stitching calibration
problem — adjust `front_lens` / `back_lens` / `back_extrinsic_*` via
`set_stitch`. If `raw.jpg` itself is wrong, the issue is upstream of
this module (RTSP / camera state).

**Pinhole view rotates wrong direction or starts from wrong angle** —
adjust `initial_yaw_deg` / `initial_pitch_deg` in config, or
`set_pinhole` at runtime. Yaw is positive-CCW looking down from
above; pitch is positive-up.

**Need to inspect the wire protocol** — see
[`akaso_360/README.md`](akaso_360/README.md) for the Ambarella JSON
control protocol (which other AKASO / Ambarella-based cameras may
share) and the `akaso_360/probe/` scripts for runnable probes.

**Repeated `traces export: ... connect: network is unreachable` in
the module's stderr** — the OpenTelemetry SDK (pulled in transitively
by viam-rdk) is trying to ship spans to a Viam telemetry endpoint and
your host has no route to it (typical on a Pi without IPv6 / offline
deployments). This is log noise rather than a functional failure, but
silences cleanly by disabling the SDK in the module's environment.
Add to the component's module config in the robot's machine config:

```json
"env": {
  "OTEL_SDK_DISABLED": "true"
}
```
