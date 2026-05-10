# Repo Memory Ledger

This ledger stores stable, reusable decisions and evidence. Session-only progress stays in `.ai/todos.md`.

## 2026-05-10: Restored preserved local GADS iOS changes after submodule conversion

Decision: recover the intentional iOS stability work from the preserved `GADS.broken-20260507-215404` file tree into the current GADS fork worktree instead of replacing the fork with a fresh upstream checkout.

Why:

- The old `GADS.broken-20260507-215404/.git` file points to a missing `/private/tmp/GADS-src-1776533707/.git/worktrees/fix-ios-wda-session-fallback`, so the old local Git commit object is not currently recoverable from this checkout.
- The file contents were preserved and contain the actual provider/router/Broadcast Extension changes that made the iPhone XR runtime work.
- The restored source now runs from the formal GADS fork worktree and should be committed there before updating the `ios-farm` superproject submodule pointer.

Implementation memory:

- Restored provider lifecycle, Appium working-directory/logging, iOS WDA session fallback, Appium control-session fallback, Broadcast port health handling, iOS device-list debounce, and provider info fields from the preserved tree.
- Restored `resources/ios-broadcast-extension/` as the reproducible ReplayKit Broadcast host/upload/setup project.
- Restored GADS onboarding docs, project map, memory ledger, and project-local `gads-ios-remote-stability` skill.

Verification evidence:

- Static checks on the restored worktree passed: `go test -vet=off ./provider/...`, `git diff --check`, and `plutil -lint resources/ios-broadcast-extension/Sources/*/Info.plist`.
- Broadcast project build passed: `xcodebuild -project resources/ios-broadcast-extension/GADSBroadcastSigning.xcodeproj -scheme GADSBroadcastSigning -configuration Debug -sdk iphonesimulator CODE_SIGNING_ALLOWED=NO build`.
- Runtime binary `/Users/corylin/Project/ai/ios-farm/.local/gads/artifacts/GADS-restore-ios-stability` is running in `tmux:gads-provider-fastpath` from the restored worktree source.
- `GET http://127.0.0.1:12000/device/00008020-00024D8E3E88003A/info` returned `provider_state=live`, `connected=true`, `is_appium_up=true`, and `stream_type=ios_webrtc_broadcast`.
- The real device process list showed `gads-broadcast-extension` running after launching the host app and tapping `Start Broadcast`.
- `/tmp/gads-verify-13001.js` verified direct viewer first frame on `13001`: `requestVideoFrameCallback`, `readyState=4`, `videoWidth=828`, `videoHeight=1792`, first frame around `619ms`.
- `/tmp/gads-verify-hub.js` verified Hub UI on `10001`: login `/authenticate=200`, `/available-devices=200`, iPhone XR visible, control page real frame callback, non-black pixels, `waitingVisible=false`, and `/device/.../swipe=200`.

Operational note:

- If `ios_webrtc_broadcast` is selected but no frames arrive, confirm the device process list includes `gads-broadcast-extension`. The provider can be live while the Broadcast Extension itself is not running.
- The restored runtime currently uses the already installed app `com.cory2btc.h264-broadcast-extension` on the iPhone XR.

## 2026-04-22: iOS Broadcast Remote-Control Stabilization

Decision: keep `ios_webrtc_broadcast` as the preferred low-latency path for active iOS remote-control sessions, with `ios_webrtc_ffmpeg` / WDA MJPEG as fallback.

Why:

- `ios_webrtc_broadcast` avoids WDA MJPEG screenshot streaming and host-side FFmpeg re-encoding.
- The validated pipeline is ReplayKit Broadcast Upload Extension -> VideoToolbox H264 -> device TCP `8765` -> provider WebRTC H264 track.
- The practical risk is power/thermal throttling or iOS stopping a long broadcast, not screen-recording files filling disk; frames are streamed and discarded.

Implementation memory:

- `resources/ios-broadcast-extension/` contains the reproducible Broadcast host/upload extension source.
- The upload extension repeats the latest frame while a client is connected and forces SPS/PPS plus an IDR frame for new TCP clients. This prevents static-screen `track-only` WebRTC sessions from leaving the browser at `readyState=0`.
- `provider/devices/common.go` debounces iOS device-list misses before full reset.
- `provider/devices/ios.go` treats video stream port forward failures as stream health and keeps the device lifecycle live; WDA `8100` remains device-control critical.
- `provider/router/control.go` uses Appium/WDA session fallbacks for iOS tap/swipe/type/clipboard and clears stale Appium session state when fallback is needed.

Verification evidence:

- Strict low-latency viewer: `/tmp/gads-strict-frame-20260421-172205/summary.json` recorded `strictReceivedFrame=true`, `readyState=4`, `videoWidth=828`, `videoHeight=1792`, `firstFrameMs=489`.
- Hub UI login/control: `/tmp/gads-hub-final-20260421-172450/summary.json` recorded `/authenticate=200`, `/available-devices=200`, iPhone XR online, control page video dimensions present, and `/device/.../swipe=200`.
- Final收敛验证: after rebuilding and restarting `tmux:gads-provider-fastpath` with the local artifact `.local/gads/artifacts/GADS-ios-stability-20260422-final`, `/tmp/gads-current-13001-20260423-001231/summary.json` recorded real browser first frame callback, `readyState=4`, `videoWidth=828`, `videoHeight=1792`, first frame around `1216ms`; `/tmp/gads-current-hub-final2-20260423-001746/summary.json` recorded login, device online, real video frame callback, non-black sampled pixels, `waitingVisible=false`, `videoWidth=828`, `videoHeight=1792`, and `/device/.../swipe=200`.
- Older fallback comparison: `ios_webrtc_ffmpeg` first-frame checks were around `580ms`, `637ms`, and `774ms`, but sustained interaction felt worse than Broadcast after the static-frame/keyframe fix.

Operational note:

- If a freshly restarted provider sits in `preparing` while WDA `8100` forward logs `Failed connecting to service, error code:3`, wait for the current setup loop before declaring failure. In the 2026-04-22 run, the first two setup attempts timed out, then the third attempt brought WDA live and Appium up. Use `tmux` logs plus `/device/<udid>/info`, not the first error burst, as the source of truth.
- For Hub UI verification, do not mark the control page healthy from `readyState=4` alone. In the 2026-04-23 run, `readyState=4` and dimensions could be present before the `Waiting for video frames` overlay disappeared; the reliable gate was `requestVideoFrameCallback` or sampled non-black pixels.

## 2026-04-23: Appium startup working-directory pitfall after moving the repo under `ios-farm/GADS`

Decision: make provider-launched Appium independent of the shell working directory, and persist raw Appium output to a device-level log file.

Why:

- After moving the source repo into the current project directory, the long-running provider tmux session was started from `/Users/corylin/Project/ai/ios-farm`, which resolves `gads` / `GADS` as a real local directory on the default macOS case-insensitive filesystem.
- On Appium `2.5.1`, launching with `--use-plugin=gads` from that directory caused early CLI parsing failure before server startup: `argument --use-plugins: Could not read file 'gads': EISDIR`.
- The provider previously logged only `startAppium ... exit status 1`, which made the regression look like a WDA or device issue even though WDA was healthy.

Implementation memory:

- `provider/devices/appium.go` now starts the Appium child from `provider-folder/device_<udid>/` instead of inheriting the parent shell working directory.
- The Appium plugin activation flag is passed in a non-ambiguous form so current Appium parses it as plugin names instead of attempting to read a local path.
- Raw Appium stdout/stderr is written to `provider-folder/device_<udid>/appium-server.log` for future startup triage.

Verification evidence:

- CLI repro from the project root failed with `appium server: error: argument --use-plugins: Could not read file 'gads': EISDIR: illegal operation on a directory, read 'gads'`.
- After rebuilding and restarting `tmux:gads-provider-fastpath`, `GET /device/00008020-00024D8E3E88003A/info` returned `provider_state=live`, `is_appium_up=true`, `stream_type=ios_webrtc_broadcast`.
- `/tmp/gads-current-13001-rerun-20260423-1715/summary.json` recorded direct viewer first-frame success with `requestVideoFrameCallback`, `828x1792`, and first frame around `221ms`.
- `/tmp/gads-current-hub-rerun-20260423-1717/summary.json` recorded `/authenticate=200`, `/available-devices=200`, iPhone XR online, real frame callback, `waitingVisible=false`, five non-black sampled pixels, and `/device/.../swipe=200`.

Known follow-ups:

- Add automatic idle stop or fps/bitrate downshift when no viewer or input is active.
- Add thermal-state feedback in the Broadcast host/extension before treating Broadcast as an unattended 24/7 default.
- Add a reusable strict Playwright test in the repo if `13001`/`10001` UI verification becomes part of CI or local release gates.
- Consider hardening the `appium-gads` register/ping heartbeat path so a transient callback failure does not turn into an Appium `exit status 1` plus repeated provider resets.
