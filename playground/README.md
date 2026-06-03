# camera-360 playground

A local, cloud-free dev loop for the camera-360 module's models. A SvelteKit app
renders Viam's [test-widgets](https://github.com/viamrobotics/test-widgets)
(camera + audio_in) and talks to a companion `viam-server` running the module
locally — no Viam cloud account required.

By default it wires up the **JVCU360 USB webcam** (`jvcu360` camera +
`jvcu360-mic` audio_in), which is the device that works out of the box on a dev
machine. See [`viam-config.json`](viam-config.json).

## Prerequisites

- `viam-server` and `ffmpeg` on `PATH`.
- Node + pnpm (this repo was built with node 24 / pnpm 10).
- The JVCU360 plugged in. **Set the device indices** in
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

The page lists each configured resource with its test widget:

- **`jvcu360` (camera)** — `CameraWidget`: live/polling video, source select,
  360° view, screenshot.
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
  `viam-server`). Grant both.
- **Single consumer:** only one process can hold the camera/mic at a time. Close
  Photo Booth / `cmd/uvc` / other apps using it before running the server.
- **CORS fallback:** if the browser can't reach `localhost:8080` from the Vite
  dev origin, add a `server.proxy` entry in `vite.config.ts` for the gRPC-web
  paths and point `host`/`serviceHost` at the proxy.
- This is a **dev tool** — it runs via `pnpm dev` and is not meant to be built or
  deployed.
