import type { DialConf } from "@viamrobotics/sdk";

/**
 * Connection used by the svelte-sdk hooks. There are two modes, selected by
 * environment (see ../../.env.example and `pnpm dev` vs `pnpm dev:live`):
 *
 *  - Local (default): a fully cloud-free dial to the companion viam-server
 *    (see ../../viam-config.json). viam-server runs with `no_tls` and acts as
 *    its own WebRTC signaler, so `host`/`serviceHost` point at the local
 *    server and `signalingAddress` is empty. `PART_ID` is just the lookup key
 *    and need not match any cloud machine.
 *
 *  - Live (when VITE_VIAM_HOST and VITE_VIAM_PART_ID are set): a cloud dial to
 *    a real machine running this module on the target device, authenticated
 *    with an API key. `PART_ID` is the machine part ID.
 */
const liveHost = import.meta.env.VITE_VIAM_HOST;
const livePartID = import.meta.env.VITE_VIAM_PART_ID;

export const PART_ID = liveHost && livePartID ? livePartID : "camera-360-local";

export const dialConfigs: Record<string, DialConf> =
  liveHost && livePartID
    ? {
        [PART_ID]: {
          host: liveHost,
          credentials: {
            type: "api-key",
            authEntity: import.meta.env.VITE_VIAM_API_KEY_ID ?? "",
            payload: import.meta.env.VITE_VIAM_API_KEY ?? "",
          },
          signalingAddress:
            import.meta.env.VITE_VIAM_SIGNALING_ADDRESS ??
            "https://app.viam.com:443",
        },
      }
    : {
        [PART_ID]: {
          host: "localhost:8080",
          serviceHost: "http://localhost:8080",
          signalingAddress: "",
          signalingInsecure: true,
        },
      };

/** The resources declared in viam-config.json, in render order. */
export const resources = [
  { kind: "camera", name: "jvcu360-camera", label: "JVCU360 camera" },
  { kind: "audio_in", name: "jvcu360-mic", label: "JVCU360 mic (audio_in)" },
] as const;
