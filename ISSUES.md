# Known issues & open work

The items below are what we hit bringing the JVCU360 up on macOS dev. Each has
what's **confirmed**, current **hypotheses**, and **next steps** so others can
pick them up.

## Display modes (the 6 touch-bar modes) — no programmatic control

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

## GPano tagging assumes the device is in 360 All-Around

**Status: accepted / depends on mode detection.** The
[jvcu360-camera](jvcu360/jvcu360.go) tags every frame with **GPano** cropped-area
XMP (a partial equatorial band, computed from the frame dimensions and the
configured `h_fov_deg`/`v_fov_deg`), so the test-widget's 3D viewer frames the
~360°×53° band correctly instead of stretching it pole-to-pole. This is right for
the JVCU360's **360 All-Around** mode, which the model assumes — but the device
also has flat/dewarped modes (Host, Single View, Wide Angle — see
[jvcu360/README.md](jvcu360/README.md)) whose frames aren't an equirectangular
band; in those, the GPano framing is wrong.

The device can't report its mode over USB (the mode XU is a write-mostly command
pipe — see the deep-probe notes), so the model can't auto-detect this; the user
must set the touch bar to 360 All-Around. A future fix is to make the tag track
the active mode once a `jvcu360-mode` switch can drive/publish it — deferred until
that control exists (it needs the Windows companion app's `SET_CUR` traffic
sniffed; brute-forcing the 64-byte command space on Linux isn't tractable).
