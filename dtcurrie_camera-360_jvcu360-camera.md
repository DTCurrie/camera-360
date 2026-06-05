# Model `dtcurrie:camera-360:jvcu360-camera`

The camera model for the **j5create JVCU360** USB 360 webcam. It captures the
device's already-dewarped UVC output — over `v4l2` on Linux or `avfoundation` on
macOS, via ffmpeg — and exposes it as a single `raw` JPEG source, tagging every
frame with **GPano** (Google Photo Sphere) cropped-area XMP that describes the
JVCU360's partial equatorial band so a capable 360 viewer maps it at its true
latitudes (empty poles) instead of stretching it pole-to-pole.

Device background (the six touch-bar modes, native formats, the macOS 720p cap,
and the mode-switching research) is in [`jvcu360/README.md`](jvcu360/README.md).

> [!IMPORTANT]
> **Set the device to its "360 All Around" mode on the touch bar before use.**
> The JVCU360 can't report or be reliably switched between its display modes over
> USB (see the deep-probe notes in [`jvcu360/`](jvcu360/)), so this model *assumes*
> the 360 All-Around panorama and always emits GPano metadata. In any other mode
> the frame isn't an equirectangular band and the 360 viewer will misframe it.

## Sources

A single `Images()` call returns one named-image source:

| Source | Type       | What it is                                           |
| ------ | ---------- | ---------------------------------------------------- |
| `raw`  | image/jpeg | The current frame, exactly as the device produces it |

Each frame carries a `GPano:ProjectionType=equirectangular` XMP packet with
cropped-area fields computed from the frame's own dimensions and the configured
`h_fov_deg`/`v_fov_deg`. Viam's camera test-widget reads it from the polled
`GetImage`/`GetImages` response (so the 360 viewer appears when the camera card
is on a polling refresh interval, not the live WebRTC stream). Injection is
lossless (the packet is spliced into the device's own JPEG bytes) and idempotent
(a frame that already carries XMP is left untouched).

## Configuration

All fields are optional. For the JVCU360 360 All-Around band, pair a crop (to
trim the black letterbox bars) with `v_fov_deg` set to the content band's true
vertical FOV — use the `detect_bars` DoCommand to find the crop.

```json
{
  "video_device": "/dev/video0",
  "width": 1920,
  "height": 1080,
  "frame_rate": 30,
  "crop_top": 265,
  "crop_bottom": 265,
  "h_fov_deg": 360,
  "v_fov_deg": 53
}
```

### Attributes

| Name           | Type   | Inclusion | Default                            | Description                                                                                                     |
| -------------- | ------ | --------- | ---------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `video_device` | string | Optional  | `/dev/video0` (Linux), `0` (macOS) | V4L2 node, or avfoundation device index. Enumerate with `go run ./cmd/uvc -list`                                |
| `width`        | int    | Optional  | `1920` (Linux), `1280` (macOS)     | Requested capture width. Must be non-negative                                                                   |
| `height`       | int    | Optional  | `1080` (Linux), `720` (macOS)      | Requested capture height. Must be non-negative                                                                  |
| `frame_rate`   | int    | Optional  | `30`                               | Requested capture frame rate. Must be non-negative                                                              |
| `input_format` | string | Optional  | `mjpeg`                            | V4L2 pixel format requested from the device. Ignored on macOS (avfoundation negotiates its own)                 |
| `crop_top`     | int    | Optional  | `0`                                | Pixels trimmed off the top of every frame (the panorama's black pole bar). Run `detect_bars` to find the value  |
| `crop_bottom`  | int    | Optional  | `0`                                | Pixels trimmed off the bottom. Left/right aren't exposed — trimming them would break the 360° longitude wrap    |
| `h_fov_deg`    | float  | Optional  | `360`                              | Horizontal coverage of the (post-crop) frame, for the GPano cropped-area math. Must be in (0, 360]              |
| `v_fov_deg`    | float  | Optional  | `53`                               | Vertical coverage of the (post-crop) frame. The JVCU360 360 All-Around band is ~53°. Must be in (0, 180]        |

> [!NOTE]
> On **macOS**, `avfoundation` typically exposes only raw video, whose
> uncompressed modes above 720p many webcams cannot sustain over USB. The model
> therefore defaults to and hard-caps capture at **1280×720** on macOS, logging a
> one-time notice if it has to clamp a larger request. **Linux** pulls native
> MJPEG over V4L2 and runs at full resolution. See
> [`ISSUES.md`](ISSUES.md) and [`jvcu360/README.md`](jvcu360/README.md).

## DoCommand

| Command       | Arguments                          | Returns                                                                                          |
| ------------- | ---------------------------------- | ------------------------------------------------------------------------------------------------ |
| `detect_bars` | `luma_threshold` (int 0–255, opt.) | Suggested `crop_top`/`crop_bottom` plus the resulting content band's size and aspect ratio       |

```json
{ "command": "detect_bars" }
```

`detect_bars` scans the latest frame in-process for black letterbox rows and
returns the crop to configure. Run it with **no** crop set to find the bars; the
content aspect it reports helps confirm you've isolated the equatorial band. A
steerable virtual-pinhole view over the panorama is planned — see the roadmap in
[`jvcu360/README.md`](jvcu360/README.md).
