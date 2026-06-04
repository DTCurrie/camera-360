#!/usr/bin/env bash
# macOS camera + microphone permission helper for the JVCU360 (and any UVC/UAC
# device this module captures via ffmpeg/avfoundation).
#
# WHAT THIS CAN AND CANNOT DO
# macOS gates camera/mic access behind TCC (Privacy & Security). The grant
# belongs to the app that *runs the capture* — for this module that's whatever
# launches `viam-server` (your terminal app during dev, or a launchd service in
# production). A script CANNOT set a TCC grant: the database is SIP-protected and
# Apple requires explicit user consent. So this helper instead:
#   1. provokes the macOS permission prompt by doing a 1-frame / 1-second
#      capture, so you can click "Allow" while you're here;
#   2. opens the Camera / Microphone Privacy panes if a capture fails (e.g. you
#      previously denied access and the prompt no longer reappears);
#   3. verifies it can actually read a frame and a moment of audio.
#
# IMPORTANT: run this from the SAME terminal you'll use to start the module
# (`make playground` / `viam-server`) so the grant lands on the right app. A
# headless/launchd viam-server is a separate case — it generally needs the
# permission pre-granted to the responsible binary; see the module README.
set -uo pipefail

if [[ "$(uname)" != "Darwin" ]]; then
  echo "macOS-only helper. On Linux, camera/mic access is via device permissions"
  echo "(e.g. add your user to the 'video'/'audio' groups, or udev rules) — not TCC."
  exit 0
fi
if ! command -v ffmpeg >/dev/null 2>&1; then
  echo "error: ffmpeg not found on PATH (brew install ffmpeg)"; exit 1
fi

open_pane() { open "x-apple.systempreferences:com.apple.preference.security?Privacy_${1}" 2>/dev/null || true; }

# Enumerate avfoundation devices and pick the JVCU360's video + audio indices.
devs="$(ffmpeg -hide_banner -f avfoundation -list_devices true -i "" 2>&1 || true)"
vid="$(printf '%s\n' "$devs" | sed -n '/video devices:/,/audio devices:/p' | grep -i 'j5create' | grep -oE '\[[0-9]+\]' | head -1 | tr -d '[]')"
aud="$(printf '%s\n' "$devs" | sed -n '/audio devices:/,$p'                | grep -i 'j5create' | grep -oE '\[[0-9]+\]' | head -1 | tr -d '[]')"

if [[ -z "${vid}" ]]; then
  echo "warning: no 'j5create' video device found — is the camera plugged in?"
  echo "Devices avfoundation sees:"; printf '%s\n' "$devs" | grep -E 'video devices:|audio devices:|\[[0-9]+\]'
  vid=0
fi
[[ -z "${aud}" ]] && aud=0

echo "Detected JVCU360 → video index: ${vid}, audio index: :${aud}"
echo

cam_ok=1; mic_ok=1

echo "1/2 Camera — requesting one frame (approve the macOS prompt if it appears)…"
if ffmpeg -hide_banner -loglevel error -f avfoundation -framerate 30 -video_size 1280x720 \
     -i "${vid}" -frames:v 1 -y /tmp/jvcu360-permcheck.jpg </dev/null 2>/tmp/jvcu360-cam.err \
   && [[ -s /tmp/jvcu360-permcheck.jpg ]]; then
  echo "   ✓ camera OK (wrote /tmp/jvcu360-permcheck.jpg)"
else
  cam_ok=0
  echo "   ✗ could not read from the camera. Opening Camera privacy settings —"
  echo "     enable access for your terminal app, then re-run this script."
  sed 's/^/       ffmpeg: /' /tmp/jvcu360-cam.err 2>/dev/null | head -4
  open_pane Camera
fi
echo

echo "2/2 Microphone — capturing one second (approve the macOS prompt if it appears)…"
if ffmpeg -hide_banner -loglevel error -f avfoundation -i ":${aud}" \
     -t 1 -y /tmp/jvcu360-permcheck.wav </dev/null 2>/tmp/jvcu360-mic.err \
   && [[ -s /tmp/jvcu360-permcheck.wav ]]; then
  echo "   ✓ microphone OK (wrote /tmp/jvcu360-permcheck.wav)"
else
  mic_ok=0
  echo "   ✗ could not read from the microphone. Opening Microphone privacy settings —"
  echo "     enable access for your terminal app, then re-run this script."
  sed 's/^/       ffmpeg: /' /tmp/jvcu360-mic.err 2>/dev/null | head -4
  open_pane Microphone
fi
echo

if [[ "$cam_ok" == 1 && "$mic_ok" == 1 ]]; then
  echo "All set — camera and microphone are accessible from this terminal."
  echo "Start the module from this same terminal so the grant applies."
  exit 0
fi
echo "One or more devices were not accessible. After enabling access in the"
echo "Privacy panes that just opened, re-run: bash jvcu360/macos-permissions.sh"
exit 1
