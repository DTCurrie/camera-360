# Model `dtcurrie:camera-360:uvc-camera`

A USB Video Class (UVC) pass-through camera. It captures whatever frame the
device is currently producing — over `v4l2` on Linux or `avfoundation` on macOS,
via ffmpeg — and exposes it as a single `raw` JPEG source with no stitching or
dewarping. This works with any UVC webcam; for a multi-mode 360 camera it
surfaces the device's currently-selected display mode.

Tested on the j5create JVCU360. Device-specific notes (its six touch-bar modes,
native formats, macOS 720p cap, and programmatic mode-switching research) are in
[`jvcu360/README.md`](jvcu360/README.md).

## Sources

A single `Images()` call returns one named-image source:

| Source | Type       | What it is                                           |
| ------ | ---------- | ---------------------------------------------------- |
| `raw`  | image/jpeg | The current frame, exactly as the device produces it |

## Configuration

All fields are optional.

```json
{
  "video_device": "/dev/video0",
  "width": 1920,
  "height": 1080,
  "frame_rate": 30
}
```

### Attributes

| Name           | Type   | Inclusion | Default                            | Description                                                                                     |
| -------------- | ------ | --------- | ---------------------------------- | ----------------------------------------------------------------------------------------------- |
| `video_device` | string | Optional  | `/dev/video0` (Linux), `0` (macOS) | V4L2 node, or avfoundation device index. Enumerate with `go run ./cmd/uvc -list`                |
| `width`        | int    | Optional  | `1920` (Linux), `1280` (macOS)     | Requested capture width. Must be non-negative                                                   |
| `height`       | int    | Optional  | `1080` (Linux), `720` (macOS)      | Requested capture height. Must be non-negative                                                  |
| `frame_rate`   | int    | Optional  | `30`                               | Requested capture frame rate. Must be non-negative                                              |
| `input_format` | string | Optional  | `mjpeg`                            | V4L2 pixel format requested from the device. Ignored on macOS (avfoundation negotiates its own) |

> [!NOTE]
> On **macOS**, `avfoundation` typically exposes only raw video, whose
> uncompressed modes above 720p many webcams cannot sustain over USB. The model
> therefore defaults to and hard-caps capture at **1280×720** on macOS, logging a
> one-time notice if it has to clamp a larger request. **Linux** pulls native
> MJPEG over V4L2 and runs at full resolution. See
> [`ISSUES.md`](ISSUES.md) and [`jvcu360/README.md`](jvcu360/README.md).

## DoCommand

Not supported in this phase (pass-through only). Runtime controls — programmatic
mode switching and a steerable virtual-pinhole view over a 360 panorama — are
planned; see the roadmap in [`jvcu360/README.md`](jvcu360/README.md).
