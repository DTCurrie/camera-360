import type { DialConf } from '@viamrobotics/sdk';

/**
 * Connection to the companion viam-server (see ../../viam-config.json).
 *
 * This is a fully local, cloud-free dial: viam-server runs with `no_tls` and
 * acts as its own WebRTC signaler, so `host`/`serviceHost` both point at the
 * local server and `signalingAddress` is left empty. `PART_ID` is just the key
 * the svelte-sdk hooks use to look up this connection — it does not need to
 * match any cloud machine.
 */
export const PART_ID = 'camera-360-local';

export const dialConfigs: Record<string, DialConf> = {
	[PART_ID]: {
		host: 'localhost:8080',
		serviceHost: 'http://localhost:8080',
		signalingAddress: '',
		signalingInsecure: true
	}
};

/** The resources declared in viam-config.json, in render order. */
export const resources = [
	{ kind: 'camera', name: 'uvc-camera', label: 'UVC camera' },
	{ kind: 'audio_in', name: 'uvc-mic', label: 'UVC mic (audio_in)' }
] as const;
