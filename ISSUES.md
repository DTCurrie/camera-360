# Known issues & open work

The items below are what we hit bringing the JVCU360 up on macOS dev. Each has
what's **confirmed**, current **hypotheses**, and **next steps** so others can
pick them up.

---

## 1. Camera image never updates (live view is a frozen frame)

**Status: open / blocking the live view.** In the playground the `CameraWidget`
shows a single frame and never advances, in both "live" and timed-refresh modes.
The browser console shows, repeatedly:

```
GET blob:http://localhost:5175/<uuid> net::ERR_FILE_NOT_FOUND
  at live-or-polling-video.svelte:242
```

There are **two independent problems stacked here.** Fix the server one first —
it's ours.

### 1a. Server-side: the camera serves the _same_ frame every time

**Confirmed.** Eight consecutive grabs via the CLI (same camera code path as the
module, no browser/SDK involved) are **byte-identical**:

```bash
go run ./cmd/uvc -capture -frames 8 -video-device 0 -out /tmp/capframes
md5 -q /tmp/capframes/*.jpg | sort | uniq -c
#   8 3e72b3fbd3733928004bd4604e8e817d      <- all identical, all 196817 bytes
```

Frames were written ~155 ms apart over ~1.3 s. A live sensor's JPEG bytes differ
frame-to-frame from noise alone, so identical output means **only one frame is
ever decoded** and `Capture.Latest()` keeps returning it. Because this
reproduces through `cmd/uvc`, it is **not** a browser/widget bug.

**Hypotheses (not yet disambiguated):**

1. **ffmpeg/avfoundation emits a single frame then stalls.** The module's ffmpeg
   invocation (from the server log) is:
   ```
   -f avfoundation -framerate 30 -video_size 1920x1080 -i 0 \
   -f image2pipe -vcodec mjpeg -q:v 4 pipe:1
   ```
   On startup ffmpeg warns `Stream #0: not enough frames to estimate rate;
consider increasing probesize` and `Selected pixel format (yuv420p) is not
supported ... Overriding ... to uyvy422`. avfoundation rate-estimation stalls
   and pixel-format mismatches are a known class of "captures once then hangs."
2. **The Go MJPEG decode loop stops after frame 1.** [capture.go](capture.go)
   `decodeStream` (lines 149–179) holds one `bufio.Reader` and loops
   `scanToSOI` → `jpeg.Decode` → `latest.Store`. If `jpeg.Decode` errors on
   frame 2 it logs `jpeg decode failed` and returns, and `runLoop` would log
   `ffmpeg session ended; will retry`. We have **not** confirmed whether either
   line appears during a sustained run.

**Next steps (the disambiguating test):**

- Run the exact ffmpeg command above to a file for ~3 s and count `FF D8`
  markers. **Many frames** → the Go decode loop is at fault (look at
  `decodeStream` / `scanToSOI`). **One frame** → it's the ffmpeg/avfoundation
  input args.
- Grep a sustained server run for `jpeg decode failed` and `ffmpeg session
ended; will retry`.
- If it's the input: on **Linux** the device offers MJPEG natively, so
  `-f v4l2 -input_format mjpeg` ([platform.go](platform.go)) likely sidesteps the
  whole avfoundation raw/rate mess — worth validating there, since Linux is the
  deploy target anyway. On macOS, try dropping `-framerate`, adding `-pix_fmt`,
  or `-probesize`/`-analyzeduration` tweaks.
- Relevant files: [capture.go](capture.go), [platform.go](platform.go),
  [uvc.go](uvc.go).

### 1b. Client-side: blob-URL revoke race in the test-widget

**Confirmed by reading the widget source.** Even with a correctly streaming
server, [test-widgets' live-or-polling-video.svelte](playground/node_modules/@viamrobotics/test-widgets/dist/components/widgets/camera/live-or-polling-video.svelte)
(the polling render `$effect`, ~lines 217–249) does:

```js
objectUrl = URL.createObjectURL(blob);
img.src = objectUrl;
// cleanup on re-run / destroy:
return () => {
  cancelled = true;
  if (objectUrl) URL.revokeObjectURL(objectUrl);
};
```

When the effect re-runs on the next poll it revokes the _previous_ object URL. If
that revoke beats the `<img>` finishing its load/decode — very plausible for a
1920×1080 JPEG at a short refetch interval — the browser fetches a
already-revoked blob and throws `ERR_FILE_NOT_FOUND`, the `load`→`drawImage`
handler never fires, and the canvas (hence the video element it `captureStream`s
from) stays on the last good frame. This is **upstream code in `node_modules`**,
so we can't patch it durably.

**Next steps:** confirm whether it still reproduces once 1a is fixed and/or at a
lower resolution (smaller JPEGs load before the revoke); if it persists, report
upstream to viamrobotics/test-widgets, or render the live image ourselves with a
thin component (svelte-sdk's `<CameraImage>` or a hand-rolled poller that doesn't
revoke mid-load) instead of the test widget.

### 1c. WebRTC "live" path is unverified locally

The widget's "live" mode uses `StreamClient` (WebRTC media track), a different
path from 1b. We have **not** confirmed WebRTC video works against a local,
no-cloud `viam-server`. If it doesn't, the widget falls back to the polling path
(1b). Verifying this is its own task.

---

## 2. Display modes (the 6 touch-bar modes) — no programmatic control

**Status: open / design decision needed.** The JVCU360 has one physical fisheye
and renders **six firmware modes** (360° All-Around 1920×720, Full Screen, Host,
Dual Host, Single View, Wide Angle — see
[jvcu360/README.md](jvcu360/README.md)). Today the only selector is the
**capacitive touch bar** on the device; the module just passes through whatever
mode is currently active. The active mode also dictates the frame size, so our
fixed `width`/`height` config can mismatch the live mode.

### What it takes to switch modes programmatically

Mode selection goes through **two vendor UVC Extension Units** on the
VideoControl interface (unitID 2 and 3, GUIDs and selector ranges documented in
[jvcu360/README.md](jvcu360/README.md)). Blockers:

- **macOS blocks Extension-Unit access** — the system camera driver owns the
  control interface (`libusb` returns `Access denied`). **This work must be done
  on Linux** (detach `uvcvideo` / use `v4l2`/`uvcdynctrl`).
- The **control-selector → mode mapping is unknown.** Best resolved by sniffing
  the Windows companion app's `SET_CUR` traffic, or brute-forcing selectors on
  Linux. Probe scripts are in
  [jvcu360/uvc_extension_probe.py](jvcu360/uvc_extension_probe.py) and
  [jvcu360/uvc_descriptors.py](jvcu360/uvc_descriptors.py).

### Two ways to surface modes once switching works

- **Option A — a `switch` model** (`toggleswitch.API`, dir `components/switch`):
  positions map to the six modes; `SetPosition` issues the XU `SET_CUR`. Clean
  Viam-native control surface, composes with the camera as a dependency. This is
  the most idiomatic option.
- **Option B — each mode as an independent `getImages` source.** Tempting, but
  **the device only outputs one mode at a time over a single UVC stream** — it
  can't emit all six simultaneously. So a true multi-source `Images()` would have
  to _switch the device between modes and re-capture per request_, which is slow,
  stateful, and fights any other consumer. Only viable as switch-on-demand, not
  concurrent sources.

### Better alternative for "multiple views": do it in software

The **360° All-Around** mode is the cleanest panoramic source. Rather than
hardware modes, capture that one and derive views ourselves with the existing
projector ([pinhole.go](pinhole.go)) — exposing e.g. `equirectangular` plus
several steerable `pinhole` sources via `Images()`, with no device switching.
This needs the projector made coverage/FOV-aware for a partial-height panorama
(the 360 mode is ~360°×53°, not a full sphere) — see the lens-architecture notes.
This is likely the highest-value direction and is independent of the Linux XU
work.
