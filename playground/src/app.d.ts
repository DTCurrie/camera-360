// See https://svelte.dev/docs/kit/types#app.d.ts
// for information about these interfaces
declare global {
	namespace App {
		// interface Error {}
		// interface Locals {}
		// interface PageData {}
		// interface PageState {}
		// interface Platform {}
	}
}

// Connection env for the `live` mode (see src/lib/viam.ts and .env.example).
// All optional: when VITE_VIAM_HOST / VITE_VIAM_PART_ID are unset the app dials
// the local viam-server instead.
interface ImportMetaEnv {
	readonly VITE_VIAM_HOST?: string;
	readonly VITE_VIAM_PART_ID?: string;
	readonly VITE_VIAM_API_KEY_ID?: string;
	readonly VITE_VIAM_API_KEY?: string;
	readonly VITE_VIAM_SIGNALING_ADDRESS?: string;
}

interface ImportMeta {
	readonly env: ImportMetaEnv;
}

export {};
