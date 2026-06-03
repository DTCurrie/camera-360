# j5create JVCU360 (USB 360 webcam)

The [JVCU360](https://en.j5create.com/products/jvcu360) is a standard
**UVC + UAC** USB 360 conference webcam (silicon: Magic Control Technology,
USB ID `0711:0360`). Unlike RTSP/Wi-Fi 360 cameras, it is plug-and-play — no Wi-Fi, no
RTSP, no vendor handshake — and it **dewarps the fisheye on-board**, so the
module simply captures its already-finished output over UVC via ffmpeg.

This module ships two models for it:

| Model | API | What it does |
| ----- | --- | ------------ |
| `dtcurrie:camera-360:jvcu360` | `rdk:component:camera` | UVC pass-through of the current frame as a single `raw` JPEG source |
| `dtcurrie:camera-360:jvcu360-mic` | `rdk:component:audio_in` | The built-in omnidirectional mic (PCM s16, 48 kHz mono) |

## Prerequisites

- `ffmpeg` on `PATH` (used for both video and audio capture).
- **Linux** (deploy): the camera is a V4L2 node (`/dev/videoN`) and an ALSA
  capture device. **macOS** (dev): both are reached via `avfoundation`.

## Configuration

**Camera** (`dtcurrie:camera-360:jvcu360`) — all fields optional:

| Field | Default | Notes |
| ----- | ------- | ----- |
| `video_device` | `/dev/video0` (Linux), `0` (macOS) | V4L2 node, or avfoundation device index (see `-list`) |
| `width` / `height` | `1920` / `1080` | Native sizes: 1920×1080, 1280×720, 640×480, 640×360 |
| `frame_rate` | `30` | 15 or 30 |
| `input_format` | `mjpeg` | V4L2 pixel format; ignored on macOS |

```json
{ "video_device": "/dev/video0", "width": 1920, "height": 1080 }
```

**Microphone** (`dtcurrie:camera-360:jvcu360-mic`) — all fields optional:

| Field | Default | Notes |
| ----- | ------- | ----- |
| `audio_device` | `default` (Linux), `:0` (macOS) | ALSA device (e.g. `plughw:1,0`) or avfoundation audio index (e.g. `:2`). On Linux set this explicitly to the webcam's card. |
| `sample_rate_hz` | `48000` | |
| `num_channels` | `1` | The mic is mono |

`GetAudio` streams `pcm16` in 100 ms chunks; `codec` must be `pcm16`.

## Discovery CLI (`cmd/uvc`)

The pass-through model surfaces whatever frame the device is producing, so
the CLI is the way to see what each mode looks like before configuring:

```bash
go run ./cmd/uvc -list                                    # enumerate devices
go run ./cmd/uvc -capture -frames 30 -video-device 1 -out out
go run ./cmd/uvc -audio -seconds 3 -audio-device ":2" -out out
```

Frames land in `./out/` (`frame_NNN.jpg`); audio as `out/mic.wav`.

## What the device outputs

The JVCU360 has a single upward fisheye and a **capacitive touch bar** that
selects one of six firmware-rendered display modes (there is no servo/motor):

| Mode | Frame | Content |
| ---- | ----- | ------- |
| 360° All Around | 1920×720 | Pure equirectangular panorama (cleanest 360 source) |
| Full Screen | 1920×540 | |
| Host | 1920×1080 | 360 panorama strip (top 270) + dewarped host view (bottom 810) |
| Dual Host | 1920×1080 | Panorama strip + two host views |
| Single View | 1920×1080 | One dewarped rectilinear view |
| Wide Angle | — | ~120° rectilinear |

Key point: **the camera dewarps internally** — it never exposes a raw
circular fisheye over UVC — so there is no dual-fisheye stitching to do here
(contrast the RTSP dual-fisheye path in [`../fisheye.go`](../fisheye.go)). Native UVC
formats are **MJPEG, H.264 (frame-based), and YUY2** at the sizes above.

## Mode control (deferred — needs Linux)

Switching the six modes programmatically (instead of via the touch bar) goes
through **two vendor UVC Extension Units** on the VideoControl interface,
discovered by dumping the device descriptors:

- `unitID 2` — GUID `{8da31e37-c7c1-4af2-b2a5-e4aab18675f0}`, control selectors 1–10
- `unitID 3` — GUID `{8da31e37-c7c1-4af2-b2a5-e4aab18674f0}`, control selectors 9–12

The Windows "Webcam Companion App" drives modes via `SET_CUR` on one of these
controls. **macOS blocks Extension-Unit access** (the system camera driver owns
the control interface — libusb returns `Access denied`), so this work must be
done on **Linux** (detach `uvcvideo` / use `v4l2`/`uvcdynctrl`). The remaining
unknown is the control-selector→mode mapping, best resolved by sniffing the
companion app's `SET_CUR` traffic or brute-forcing on Linux.

The probe scripts used to find the above are kept here (pyusb + libusb):

```bash
python3 uvc_descriptors.py      # full config-descriptor dump (formats + XUs)
python3 uvc_extension_probe.py  # characterize XU controls (GET_INFO/LEN/CUR)
```

## Status / roadmap

- **Shipped:** UVC pass-through camera + mic (Phase 1).
- **Deferred:** programmatic mode switching via the Extension Units (Linux);
  a steerable virtual-pinhole view over the 360 panorama (reuse
  [`../pinhole.go`](../pinhole.go)); a `switch`/`servo` control surface on top.
