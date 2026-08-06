# Repo Knowledge Map

Last updated: 2026-08-06

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
- Plain WDA MJPEG HTTP and WebSocket proxies: `provider/router/stream.go`. Both must preserve the fixed-buffer, downstream-cancellable, frame-size-bounded invariant documented in `docs/repo-memory-ledger.md` under the 2026-08-06 entry.
- ReplayKit Broadcast H264 to WebRTC path: `provider/router/ios_stream_webrtc_broadcast.go`.
- Broadcast host/upload extension source: `resources/ios-broadcast-extension/`.

## Current iOS Streaming Decision

- Preferred active-session path: `ios_webrtc_broadcast`, implemented as ReplayKit Broadcast Upload Extension -> device-side VideoToolbox H264 -> TCP port `8765` -> provider WebRTC H264 track.
- Stable fallback path: `ios_webrtc_ffmpeg`, implemented as WDA MJPEG -> host-side FFmpeg H264 -> WebRTC.
- Debug fallback: plain WDA MJPEG/screenshot behavior.
- Local operator mode switch: the Hub control page `http://127.0.0.1:10001/devices/control/<UDID>` has an injected `iOS Stream Mode` overlay; provider fallback is `http://127.0.0.1:12000/device/<UDID>/stream-mode`. Both call provider `POST /device/<UDID>/update-stream-settings` with `stream_type` and MJPEG preset fields.
- Stream health is not device health. `8765` Broadcast and `9100` WDA MJPEG failures should not automatically mark the iOS device offline while WDA `8100`, Appium, and device discovery remain healthy.

## iOS Tap Latency and Typing Performance (2026-05-15)

- Measured baselines, 400 ms physical floor breakdown, WDA-first decision, and typing coalescer rationale: `docs/repo-memory-ledger.md` § "2026-05-15: iOS tap and typing latency optimization".
- iOS 17+ tunnel requirement (`gads-ios-tunnel`) is documented in the same ledger entry.

## Verification Baselines

- Provider package tests: `go test -vet=off ./provider/...`.
- WDA MJPEG changes also require focused normal/oversize/stall/cancel/WebSocket tests, a static check that no `io.ReadAll(part)` remains in `provider/router/stream.go`, and a reconnect-heavy RSS check.
- Strict iOS first-frame check must assert browser `<video>` readiness and dimensions, not just WebRTC track arrival.
- Latest restored strict Broadcast viewer evidence: `/tmp/gads-verify-13001.js` run on 2026-05-10 against `http://127.0.0.1:13001`, with `requestVideoFrameCallback` first frame, `readyState=4`, `828x1792`, and first frame around `619ms`.
- Latest restored hub UI evidence: `/tmp/gads-verify-hub.js` run on 2026-05-10 against `http://127.0.0.1:10001`, with `/authenticate=200`, `/available-devices=200`, iPhone XR visible, real video frame callback, non-black sampled pixels, `waitingVisible=false`, `828x1792` video dimensions, and `/device/.../swipe=200`.
- Latest iPhone SE Broadcast evidence: `/tmp/gads-se-broadcast-targeted-smoke/summary.json` verified direct viewer `13001` with `requestVideoFrameCallback`, `readyState=4`, `750x1334`, first frame around `514ms`, and `nonBlackRatio=0.975`; `/tmp/gads-se-hub-smoke/summary.json` verified Hub control `10001` with `waitingVisible=false`, `750x1334`, `nonBlackRatio=0.975`, and Hub proxy `/device/.../swipe=200`.
- Latest clipboard evidence: XR previously returned sentinel `gads-clipboard-live-224509` through both provider and Hub. The current fallback first tries a short direct WDA pasteboard read, temporarily foregrounds WDA only when required, watches for explicit paste-permission alerts without blind coordinate taps, and restores the previous app asynchronously. SE02 verified provider/Hub content plus a non-black control-page stream after this final no-blind-tap behavior; XR still needs the same final runtime regression check.

## Handoff Rules

- Start iOS debugging by checking `/device/<udid>/info` on provider `12000`, process list on the real device, and real browser state on `13001` or `10001`.
- Do not trust a Playwright smoke that only receives a WebRTC track; inspect `requestVideoFrameCallback` when available, `video.readyState`, `videoWidth`, and `videoHeight`.
- On the Hub control page, `readyState=4` can appear before the waiting overlay disappears. Wait for a real video frame callback or non-black sampled pixels before judging `Waiting for video frames`.
- For newly added iPhones, confirm the ReplayKit Broadcast app is installed and provisioned for that exact UDID before expecting `ios_webrtc_broadcast` to work.
- Do not claim Broadcast self-start is solved. The Hub overlay `Start Recording` button only tries to open known Broadcast host apps. ReplayKit still requires user/system picker interaction; automation may assist and then must verify the extension process plus real browser frame.
- For iOS clipboard reads, prefer direct `wda/getPasteboard` with a short timeout only when it returns non-empty text. If it returns WDA 200 with an empty value, treat that as a fast-path miss, temporarily foreground WDA, handle only positively identified paste alerts, reread, and restore the previous foreground app asynchronously. Never issue a blind coordinate tap for an alert WDA cannot identify.
- Clipboard verification must check actual content: provider `result` must match expected non-empty text and Hub browser clipboard must read the same value. Toast `Device clipboard copied!` alone is not evidence.
- Keep long-running provider processes in local `tmux`; record the session name, binary path, and log path in `.ai/todos.md`.
- On iOS 17+, start and verify `tmux:gads-ios-tunnel` before restarting Provider. A provider that repeatedly times out starting WDA after a cold restart is not valid MJPEG performance evidence.
- Before committing, ensure any Apple signing team IDs, provisioning profiles, DerivedData, or personal bundle IDs are not hardcoded unless deliberately documented as local examples.
