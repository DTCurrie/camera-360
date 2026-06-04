# Model `dtcurrie:camera-360:discovery`

A [discovery service](https://docs.viam.com/operate/reference/services/discovery/)
that detects connected USB (UVC) webcams and returns ready-to-paste configs for
this module's [`uvc-camera`](dtcurrie_camera-360_uvc-camera.md) and
[`uvc-mic`](dtcurrie_camera-360_uvc-mic.md) models — with the correct device
handles already filled in.

The main thing it resolves is picking the right `video_device`. On a Raspberry
Pi the internal ISP/codec blocks claim the low `/dev/videoN` numbers, so a USB
webcam lands at `/dev/video8` or higher; the `uvc-camera` default of
`/dev/video0` then opens a non-capture node and fails (see
[`ISSUES.md`](ISSUES.md)). Discovery reads sysfs to find the actual USB capture
node and its paired microphone, so the emitted config just works.

> [!NOTE]
> Detection is **Linux-only**. It relies on Linux sysfs (`/sys/class/...`) to
> read each device's USB Video interface class and vendor/product IDs. On
> macOS/Windows the service returns an empty list.

## How detection works

For each `/dev/videoN`, the service confirms it is a real UVC webcam by checking
its USB interface class (`bInterfaceClass == 0e`) in sysfs — which also excludes
the Pi's platform ISP/codec nodes, since those have no USB ancestry. It then:

- maps the device to its primary capture node and its USB sound card (for the mic);
- reads the USB vendor:product ID and product name;
- classifies 360/fisheye capability:
  - **`known-360`** — the VID:PID is in the module's known-360 list (authoritative;
    currently the j5create JVCU360, `0711:0360`);
  - **`name-360`** — the product name matches a hint (`360`, `fisheye`, `theta`,
    `insta360`, `kandao`, `panoram`) — best-effort;
  - **`""`** — a plain webcam.

> [!IMPORTANT]
> 360 classification is best-effort. VID:PID is the only authoritative signal; the
> name heuristic can miss a real 360 camera or flag a non-360 one. When the default
> 360-only result is empty but webcams were found, set `include_all_uvc` to `true`.

## Configuration

All fields are optional; an empty config discovers 360/fisheye webcams with mics.

```json
{
  "include_all_uvc": false,
  "include_mic": true,
  "name_prefix": ""
}
```

### Attributes

| Name              | Type   | Inclusion | Default      | Description                                                                                                |
| ----------------- | ------ | --------- | ------------ | ---------------------------------------------------------------------------------------------------------- |
| `include_all_uvc` | bool   | Optional  | `false`      | Return every confirmed UVC webcam, not just those classified as 360/fisheye. Use as a fallback if a 360 camera isn't auto-detected |
| `include_mic`     | bool   | Optional  | `true`       | Also emit a `uvc-mic` config for each device that exposes a USB microphone                                 |
| `name_prefix`     | string | Optional  | (product name) | Base name for emitted configs. When empty, the device's product name is used. Duplicates get `-1`, `-2` … suffixes |

## What it returns

One [`uvc-camera`](dtcurrie_camera-360_uvc-camera.md) config per discovered
webcam, and (when `include_mic` is set and the device has a USB mic) one
[`uvc-mic`](dtcurrie_camera-360_uvc-mic.md) config named `<camera>-mic`. Each
config sets `video_device` / `audio_device` to the resolved handle; all other
fields fall back to the model defaults. The `attributes` map also includes
informational `usb_id`, `lens_hint`, and `device_label` keys.

## DoCommand

Not supported.
