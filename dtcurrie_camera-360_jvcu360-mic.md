# Model `dtcurrie:camera-360:jvcu360-mic`

A USB Audio Class (UAC) microphone exposed as an `audio_in` component, such as a
webcam's built-in mic. It captures PCM by shelling out to ffmpeg — ALSA on
Linux, `avfoundation` on macOS — and streams `pcm16` in 100 ms chunks. This works
with any UAC capture device.

Tested on the j5create JVCU360's built-in omnidirectional mic; see
[`jvcu360/README.md`](jvcu360/README.md).

## Audio

`GetAudio` streams signed 16-bit little-endian PCM (`pcm16`) in 100 ms chunks.
The `codec` argument must be `pcm16`.

## Configuration

All fields are optional.

```json
{ "audio_device": "plughw:1,0", "sample_rate_hz": 48000, "num_channels": 1 }
```

### Attributes

| Name             | Type   | Inclusion | Default                         | Description                                                                                                                                                           |
| ---------------- | ------ | --------- | ------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `audio_device`   | string | Optional  | `default` (Linux), `:0` (macOS) | ALSA device (e.g. `plughw:1,0`) or avfoundation audio index (e.g. `:2`). On Linux, set this explicitly to the device's capture card — the default is the system input |
| `sample_rate_hz` | int    | Optional  | `48000`                         | Requested capture sample rate. Must be non-negative                                                                                                                   |
| `num_channels`   | int    | Optional  | `1`                             | Requested channel count. Must be non-negative. Many webcam mics are mono                                                                                              |

Enumerate audio devices with `go run ./cmd/uvc -list`.
