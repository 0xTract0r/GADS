---
name: gads-ios-remote-stability
description: Use when debugging or stabilizing GADS iOS remote control, ReplayKit Broadcast streaming, WDA/Appium sessions, clipboard/text input, swipe latency, Offline flapping, or `Waiting for video frames`.
---

# GADS iOS Remote Stability

## Scope

Use this skill for GADS iOS provider work involving:

- `ios_webrtc_broadcast`, `ios_webrtc_ffmpeg`, WDA MJPEG, or ReplayKit Broadcast Extension.
- iOS control actions: tap, swipe, text input, clipboard, Home, app activation.
- Device flapping between `live` and `Offline`.
- Browser UI stuck at `Waiting for video frames`.

## First Checks

1. Read `AGENTS.md`, `docs/repo-knowledge-map.md`, and `docs/repo-memory-ledger.md`.
2. Reuse the active writer worktree if the task is a continuation; do not split the same iOS stabilization intent into a second dirty worktree.
3. Check provider runtime:

```bash
curl -sS http://127.0.0.1:12000/device/<UDID>/info | jq '.result | {provider_state,connected,is_appium_up,has_appium_session,stream_type,screen_width,screen_height,stream_target_fps,stream_scaling_factor}'
tmux ls
```

4. For the real device, check the expected processes:

```bash
xcrun devicectl device info processes --device <UDID> | rg 'SpringBoard|WebDriverAgentRunner|h264-broadcast-extension|gads-broadcast-extension'
```

## Diagnosis Rules

- Do not mark video healthy because WebRTC received a track. A valid browser video check needs `video.readyState >= 2`, `videoWidth > 0`, and `videoHeight > 0`.
- Prefer `requestVideoFrameCallback` or sampled non-black pixels for final browser checks. On the Hub control page, `readyState=4` and dimensions can arrive before the waiting overlay is hidden.
- Treat Broadcast `8765` and WDA MJPEG `9100` as stream health. Do not reset the whole iOS device if WDA `8100`, Appium, and USB discovery are still healthy.
- Debounce iOS device-list misses. `ios.ListDevices()` can return a transient empty list or error.
- If Appium reports invalid/no such session, clear provider-side session state and fallback to a control session or WDA session endpoint.
- For static-screen Broadcast failures, inspect whether the extension sends SPS/PPS plus IDR on new TCP clients and repeats the latest frame while a client is connected.
- If provider is `preparing` and WDA `8100` logs `Failed connecting to service, error code:3`, do not stop at the first burst. Check whether the setup loop recovers WDA/Appium in a later attempt, and only intervene after the current setup timeout/retry cycle is clear.

## Stabilization Pattern

1. Keep long-running provider processes in local `tmux`, not one-off background tooling.
2. Fix lifecycle before tuning latency:
   - WDA `8100` setup and status.
   - Appium register/ping/session self-healing.
   - iOS list miss debounce.
   - Broadcast/WDA stream health separated from device health.
3. Fix interaction paths:
   - Prefer healthy Appium for tap/swipe/touch.
   - Fallback to WDA session endpoints when no-session WDA endpoints are unsupported.
   - Use session `/wda/keys` for iOS text input.
   - Activate WDA before reading iOS pasteboard.
4. Tune video last:
   - Active sessions: `ios_webrtc_broadcast`.
   - If hot or choppy: lower fps first, then bitrate.
   - Idle sessions: stop Broadcast or add idle timeout.

## Verification Gate

Minimum static checks:

```bash
gofmt -w provider/devices/*.go provider/router/*.go
go test -vet=off ./provider/...
git diff --check
```

Minimum live checks for iOS stream/control changes:

- Rebuild provider from the current worktree and restart the local `tmux` provider session with that binary.
- `12000`: provider `/device/<UDID>/info` shows `provider_state=live`, Appium health, stream type, and expected screen/settings.
- `13001`: strict browser video check sees `readyState >= 2` and non-zero dimensions.
- `10001`: log in through the hub UI, device appears online/available, control page receives a real video frame, `Waiting for video frames` is not visible, and at least one real `Swipe` returns HTTP `200` with no `Swipe failed`.
- If the Hub UI shows an old in-use lock, release the device first and then re-enter the control page before judging `Waiting for video frames`.

## Documentation Gate

If behavior, commands, troubleshooting, or stable decisions change, update the smallest relevant docs:

- `docs/provider.md` for setup/operation.
- `docs/faq.md` for repeated troubleshooting.
- `docs/repo-knowledge-map.md` for onboarding structure.
- `docs/repo-memory-ledger.md` for dated decisions and evidence.
- `AGENTS.md` for AI handoff rules.
