# Repo Knowledge Map

Last updated: 2026-05-10

## Fast Onboarding

- `README.md`: product overview, feature list, basic run targets.
- `AGENTS.md`: AI handoff rules, verification expectations, iOS remote-control pitfalls.
- `docs/provider.md`: provider setup, iOS WDA/Appium/Broadcast Extension operation, stream tradeoffs.
- `docs/hub.md`: hub setup and operating notes.
- `docs/faq.md`: high-signal troubleshooting entries.
- `docs/repo-memory-ledger.md`: dated decisions, measured baselines, known gaps.

## Core Runtime Map

- `main.go`: process entrypoint.
- `hub/`: central hub API, auth, device state, provider update ingestion, UI-facing routes.
- `provider/`: local provider runtime for attached devices and TV platforms.
- `provider/devices/`: device lifecycle, discovery, setup, reset, stream settings, Appium/WDA startup.
- `provider/router/`: provider HTTP routes, Appium/WDA control proxy, WebRTC stream signaling.
- `common/models/`: shared provider/hub/device models and stream type definitions.
- `resources/`: device-side binary assets and reproducible helper app sources.

## iOS Remote-Control Map

- Device setup and WDA/Appium lifecycle: `provider/devices/ios.go`, `provider/devices/appium.go`, `provider/devices/common.go`.
- Runtime state and provider info fields: `provider/devices/runtime.go`, `provider/devices/platform.go`, `provider/router/routes.go`.
- Tap/swipe/type/clipboard routing: `provider/router/control.go`, `provider/router/appium.go`, `provider/router/device_routes.go`.
- WDA MJPEG to WebRTC fallback: `provider/router/ios_stream_webrtc.go`.
- ReplayKit Broadcast H264 to WebRTC path: `provider/router/ios_stream_webrtc_broadcast.go`.
- Broadcast host/upload extension source: `resources/ios-broadcast-extension/`.

## Current iOS Streaming Decision

- Preferred active-session path: `ios_webrtc_broadcast`, implemented as ReplayKit Broadcast Upload Extension -> device-side VideoToolbox H264 -> TCP port `8765` -> provider WebRTC H264 track.
- Stable fallback path: `ios_webrtc_ffmpeg`, implemented as WDA MJPEG -> host-side FFmpeg H264 -> WebRTC.
- Debug fallback: plain WDA MJPEG/screenshot behavior.
- Stream health is not device health. `8765` Broadcast and `9100` WDA MJPEG failures should not automatically mark the iOS device offline while WDA `8100`, Appium, and device discovery remain healthy.

## Verification Baselines

- Provider package tests: `go test -vet=off ./provider/...`.
- Strict iOS first-frame check must assert browser `<video>` readiness and dimensions, not just WebRTC track arrival.
- Latest restored strict Broadcast viewer evidence: `/tmp/gads-verify-13001.js` run on 2026-05-10 against `http://127.0.0.1:13001`, with `requestVideoFrameCallback` first frame, `readyState=4`, `828x1792`, and first frame around `619ms`.
- Latest restored hub UI evidence: `/tmp/gads-verify-hub.js` run on 2026-05-10 against `http://127.0.0.1:10001`, with `/authenticate=200`, `/available-devices=200`, iPhone XR visible, real video frame callback, non-black sampled pixels, `waitingVisible=false`, `828x1792` video dimensions, and `/device/.../swipe=200`.

## Handoff Rules

- Start iOS debugging by checking `/device/<udid>/info` on provider `12000`, process list on the real device, and real browser state on `13001` or `10001`.
- Do not trust a Playwright smoke that only receives a WebRTC track; inspect `requestVideoFrameCallback` when available, `video.readyState`, `videoWidth`, and `videoHeight`.
- On the Hub control page, `readyState=4` can appear before the waiting overlay disappears. Wait for a real video frame callback or non-black sampled pixels before judging `Waiting for video frames`.
- Keep long-running provider processes in local `tmux`; record the session name, binary path, and log path in `.ai/todos.md`.
- Before committing, ensure any Apple signing team IDs, provisioning profiles, DerivedData, or personal bundle IDs are not hardcoded unless deliberately documented as local examples.
