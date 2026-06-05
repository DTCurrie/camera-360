# camera-360 playground

A local, cloud-free dev loop for the camera-360 module's models. A SvelteKit app
renders Viam's [test-widgets](https://github.com/viamrobotics/test-widgets)
(camera + audio_in) and talks to a companion `viam-server` running the module
locally — no Viam cloud account required.

By default it wires up a **USB webcam** via the generic `jvcu360-camera` camera +
`jvcu360-mic` audio_in models. The reference device is the j5create JVCU360, which
works out of the box on a dev machine. See [`viam-config.json`](viam-config.json).

## Prerequisites

- `viam-server` and `ffmpeg` on `PATH`.
- Node + pnpm (this repo was built with node 24 / pnpm 10).
- A UVC webcam plugged in. **Set the device indices** in
  [`viam-config.json`](viam-config.json) to match your machine — they are not
  fixed across hosts. On macOS, find them with:

  ```bash
  go run ./cmd/uvc -list     # from the repo root
  ```

  Use the avfoundation **video** index for `video_device` (e.g. `"0"`) and the
  **audio** index for `audio_device` (e.g. `":2"` — note the leading colon).

## Run it

From the repo root, one command builds the module binary, starts the local
`viam-server`, and runs the app (installing deps on first run):

```bash
make playground                 # then open the printed http://localhost:5173
```

`Ctrl-C` stops the dev server and tears down the `viam-server` with it. To run
the two halves separately instead:

```bash
viam-server -config playground/viam-config.json    # terminal 1
cd playground && pnpm install && pnpm dev           # terminal 2
```

## Run it against a live machine

To point the same widgets at a real Viam machine running this module on the
target device (e.g. the Linux box with the camera attached) instead of a local
`viam-server`:

```bash
cp playground/.env.example playground/.env.live.local   # then fill it in
make playground-live
```

`.env.live.local` is gitignored. Fill it from the machine's **CONNECT** tab in
the Viam app:

- `VITE_VIAM_HOST` — the machine main part address (`….viam.cloud`)
- `VITE_VIAM_PART_ID` — the machine part ID
- `VITE_VIAM_API_KEY_ID` / `VITE_VIAM_API_KEY` — an API key for the machine
- `VITE_VIAM_SIGNALING_ADDRESS` — optional; defaults to `https://app.viam.com:443`

`make playground-live` runs only the SvelteKit app (`pnpm dev:live`, i.e.
`vite dev --mode live`) — it does **not** build the module or start a local
server, since the module runs on the remote device. When `VITE_VIAM_HOST` and
`VITE_VIAM_PART_ID` are set the app builds a cloud `DialConf` authenticated with
the API key; otherwise it falls back to the local dial (see
[`src/lib/viam.ts`](src/lib/viam.ts)).

The page lists each configured resource with its test widget:

- **`jvcu360-camera` (camera)** — `CameraWidget`: live/polling video, source select,
  360° view, screenshot. The interactive 3D/360 viewer reads the `viam:equirectangular` XMP
  tag the module always adds; it shows up when the card is set to a **polling**
  refresh interval (the tag rides on `GetImage`, not the live WebRTC stream).
- **`jvcu360-mic` (audio_in)** — `AudioInputWidget`: codec select (`pcm16`),
  record, download.

## How the connection works

`viam-server` runs with `no_tls` and acts as its own local WebRTC signaler. The
app dials it directly (see [`src/lib/viam.ts`](src/lib/viam.ts)):

```ts
{ host: 'localhost:8080', serviceHost: 'http://localhost:8080',
  signalingAddress: '', signalingInsecure: true }
```

No auth is configured (the server config has no `auth` block), which is fine for
a local-only dev server.

## Notes / gotchas

- **macOS permissions:** the OS prompts for camera/microphone access the first
  time the module opens the device (the request comes from the process running
  `viam-server`). Grant both. To do it up front / troubleshoot a denial, run
  `bash jvcu360/macos-permissions.sh` from the same terminal — it provokes the
  prompts, opens the Privacy panes, and verifies access.
- **Single consumer:** only one process can hold the camera/mic at a time. Close
  Photo Booth / `cmd/uvc` / other apps using it before running the server.
- **CORS fallback:** if the browser can't reach `localhost:8080` from the Vite
  dev origin, add a `server.proxy` entry in `vite.config.ts` for the gRPC-web
  paths and point `host`/`serviceHost` at the proxy.
- This is a **dev tool** — it runs via `pnpm dev` and is not meant to be built or
  deployed.
