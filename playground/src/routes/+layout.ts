// The Viam SDK connects to the robot over WebRTC/gRPC-web, which only exists in
// the browser — so this app is client-only (no SSR, no prerender).
export const ssr = false;
export const prerender = false;
