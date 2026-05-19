# Repo Memory Ledger

This ledger stores stable, reusable decisions and evidence. Session-only progress stays in `.ai/todos.md`.

## 2026-05-19: iOS new-device onboarding runbook

### Decisions

**Added end-to-end runbook for onboarding a new iOS device to a running GADS instance.**

Lives in [`docs/provider.md`](provider.md) under `### Onboarding a New iOS Device to GADS`, covering 13 steps:

1. Discover UDID via `ios list` + hub admin GET.
2. lockdownd USB pair (`ios pair`) — device unlocked, Trust dialog + passcode.
3. CoreDevice pair (`xcrun devicectl manage pair`) — activates Developer Mode toggle on the device.
4. Enable Developer Mode in iOS UI + reboot + second confirmation.
5. Re-pair (`ios pair`) after the reboot — iOS 17/18 wipes the lockdownd record.
6. Register device in MongoDB via `POST /admin/device` to the hub.
7. Inspect current WDA IPA provisioning profile; skip to 12 if device + non-expired profile match.
8. Disable device usage in MongoDB to halt the provider retry loop.
9. **One-time Xcode GUI step** to register the device with the Apple Developer Portal (`xcodebuild -allowProvisioningUpdates` cannot do this without an App Store Connect API key).
10. CLI rebuild WDA Runner via `xcodebuild build-for-testing`.
11. Verify new profile contents + repackage IPA + back up and replace the provider's IPA.
12. Re-enable device usage in MongoDB.
13. Verify live via provider `/device/<UDID>/info` (`provider_state=live`, `is_appium_up=true`) and a real browser control-page check.

### Known pitfalls documented (8)

1. **Team ID is the certificate `OU` field**, not the dev-signing ID in `CN(...)`. Using the wrong one yields `No Account for Team "<id>"` from xcodebuild.
2. **MongoDB container is named `gads-mongodb`**, not `gads-mongo`.
3. **Developer Mode reboot wipes lockdownd pair record** on iOS 17/18 — re-pair is mandatory between Step 4 and the next provider attempt.
4. **Provider retry loop throttles iOS Trust dialogs.** Disable the device for at least 15 seconds before any manual `pair` step.
5. **`ios install` may report success while iOS silently rejects an unprovisioned IPA.** Diagnose with `ios apps --all --udid=<UDID> | grep wda` showing nothing despite a "100% installed" log.
6. **`lost connection to testmanagerd`** on first launch is usually transient; persists only if the profile is wrong/expired.
7. **iOS 17+ does not require manual cert trust** under `Settings → VPN & Device Management`. The empty entry there is expected, not a failure.
8. **`go-ios agent is not running` warning is cosmetic.** Verify the userspace tunnel via `curl http://localhost:28100/tunnels` instead.

### Profile renewal

Provisioning profiles issued by the developer portal expire after one year. The same silent-install failure mode in pitfall #5 hits on renewal day even with no new device. Recovery is Steps 9-11 only (Steps 1-6 are device-onboarding-only).

### Verification

Today's onboarding of `iPhone SE 02` (UDID `00008030-001E79543E11402E`, iOS 18.3.1) reached `provider_state=live` + `is_appium_up=true` at 22:03:46 CST. Existing devices SE 01 and XR remained live throughout. Old IPA backed up as `WebDriverAgent.ipa.bak-20260519`.

---

## 2026-05-15: iOS tap and typing latency optimization; WDA readiness hardening

### Decisions

**WDA-first tap routing + snapshotMaxDepth=0** (commit b965801)

Decision: skip WDA element-tree snapshot entirely for remote-control taps, and route taps through WDA before Appium.

Why:
- Remote-control taps do not need the XCUITest element tree. Snapshot with `snapshotMaxDepth=50` was adding ~700 ms per tap.
- Setting `snapshotMaxDepth=0` and `snapshotMaxChildren=1` reduces WDA per-tap overhead to <10 ms.
- WDA-first is faster than Appium-first; Appium route is kept as fallback for compatibility (`/grid` automation).

**Typing coalescer** (commit ae93c5e)

Decision: batch consecutive `type_text` actions into a single WDA `/wda/keys` POST on the provider side, fire-and-forget with 90 ms idle window, 350 ms hard deadline, and 64-char max per batch.

Why:
- Hub-UI SPA was sending one HTTP POST per keystroke; 10-char input produced 10 serial Mach IPC round-trips (~3162 ms).
- Provider-side coalescing merges bursts into 1-2 WDA calls regardless of client-side behavior.
- Fire-and-forget: hub does not wait for WDA ACK per keystroke; error surfacing is best-effort.

**HTTP /status probe for WDA readiness** (commit c3e591f)

Decision: replace TCP dial probe with `GET http://127.0.0.1:<wda-port>/status` for WDA startup detection.

Why: TCP dial succeeds as soon as the port opens but before WDA is ready to serve HTTP; this caused premature "WDA ready" signals and sporadic first-action failures.

**Stale WDA port heal** (commit 04c1f60)

Decision: detect and close stale port-forward handles when stream mode switches, then re-establish before the new mode starts WDA.

Why: rapid mode switching left orphaned port-forward handles that caused the new WDA session to fail to bind.

### iOS tap latency physical breakdown (non-jailbreak hard limit ~400 ms)

Measured on iPhone XR `00008020-00024D8E3E88003A`, iOS 18.7.7:

| Layer | Latency |
|-------|---------|
| `tapDuration=0.05` gesture window hardcoded in `XCUIDevice+Gads.m:96` | 50 ms |
| WDA → testmanagerd via `XCTRunnerDaemonSession` DTX over Mach IPC | 80-150 ms |
| testmanagerd → BackBoardd Mach IPC + HID event queue dispatch | 30-80 ms |
| BackBoardd → foreground app | 20-60 ms |
| Callback → WDA → provider → hub → browser | 50-80 ms |
| Video encode + transport | 50-100 ms |
| HTTP localhost 3-hop (optimized) | 10-25 ms |

Three root causes for the floor:
1. iOS sandbox prevents WDA from sending IOHIDEvent directly (requires `com.apple.private.hid.client.event-dispatch` entitlement; not available with a standard developer certificate without jailbreak).
2. WDA → testmanagerd → BackBoardd → App is three Mach IPC hops by design; this is iOS test infrastructure overhead, not a single slow component.
3. iOS 18 security mitigations (PAC/BTI + daemon QoS changes) add latency per hop.

Conclusion: ~400 ms is the ceiling for non-jailbreak devices with a standard developer certificate. HTTP/video path improvements can save ~40-80 ms more, but the gain is imperceptible. The highest-leverage remaining improvement is browser optimistic UI (immediate ripple feedback) to make perceived latency ≈ 0 — but the current ripple overlay is not mounting on the control page (see B001 below).

### Performance baselines

| Metric | Before | After | Evidence |
|--------|--------|-------|---------|
| XR tap latency | ~1200 ms | ~410-500 ms | commit b965801; manual timing on XR iOS 18.7.7 |
| 10-char typing | 3162 ms | 567 ms (-82%) | commit ae93c5e; 2 coalesced WDA calls vs 10 serial |
| Mode switch stability | flaky (stale port, TCP-dial false-ready) | stable | commits 04c1f60, c3e591f; 5 consecutive Playwright mode-switch PASS |

### iOS 17+ mandatory tunnel (runbook gap now closed)

**`gads-ios-tunnel` tmux session is required before starting the provider for iOS 17+ devices.**

Without it, new or cold devices loop: `WDA install → 60 s timeout → reset`.

Start command (no root needed):
```
ios tunnel start --userspace --tunnel-info-port=28100
```

This was previously undocumented in the runbook. Add this to any fresh-machine setup checklist.

### Architectural side-bugs (stable findings, not one-off todos)

These are architecture- or operations-level issues unlikely to self-resolve; they affect any contributor who touches these paths:

**SSE stream_type staleness (B003)**: Hub `/available-devices` SSE emits `stream_type` from Mongo persistent field, not provider runtime. The workaround (overlay polls `/info`) is fragile; the real fix is pushing runtime state into the SSE heartbeat.

**device.Available SSE coupling (B005)**: `device.Available` is tied to SSE subscription lifetime on the control page. When the control page doesn't subscribe to SSE, `Available` may be false and hub proxy rejects forwarding. This is a latent incorrect-rejection risk for any future control-page refactor.

**developer image mount race (B002)**: Rapid mode switches can trigger re-provisioning while a developer image is already mounted; provider exits with `there is already a developer image mounted, reboot the device`. The guard should be idempotent: skip mount if already present.

## 2026-05-11: Local iOS stream mode switch

Decision: expose a provider-local operator page and extend `POST /device/<UDID>/update-stream-settings` so iOS stream mode can be switched without depending on unavailable Hub UI source.

Why:

- `hub-ui` is an uninitialized nested submodule pointing at `git@github.com:shamanec/GADS-hub-ui.git`; the current account receives `Repository not found`, so the Hub control page source cannot be edited in this checkout.
- Operators still need a web surface to switch between low-latency Broadcast and stable MJPEG fallback per device.
- Changing `stream_type` affects provider setup, so the provider must persist the device config and reprovision that device after a mode switch.

Implementation memory:

- `UpdateStreamSettings` accepts optional `stream_type`.
- `POST /device/<UDID>/update-stream-settings` validates the requested stream type against supported OS defaults or runtime-supported types, updates the device DB record, persists MJPEG settings, and resets the device when `stream_type` changes.
- Provider route `GET /device/<UDID>/stream-mode` serves a minimal local page with Broadcast and MJPEG preset buttons.
- Because `hub-ui` source cannot be fetched, the Hub Go server injects a small overlay into the embedded React fallback page for `/devices/control/<UDID>`. The overlay appears in the normal Hub control page and calls the local provider directly on `127.0.0.1:12000`.

Operational boundary:

- Broadcast cannot be silently self-started. ReplayKit still requires user/system Broadcast picker interaction; automation can only assist and then verify `gads-broadcast-extension` plus real browser frames.
- For speed-focused MJPEG, reduce `scaling_factor` and `jpeg_quality` before raising fps. The local fast preset is `24 fps / JPEG 40 / scale 50`.
- The Hub overlay's `Start Recording` button only attempts to open known Broadcast host app bundle IDs. It does not prove recording started; the success gate remains extension process plus real browser frame.

Verification evidence:

- Static route checks passed: `go test -vet=off ./provider/router ./provider/...`.
- Build passed: `go build -o /Users/corylin/Project/ai/ios-farm/.local/gads/artifacts/GADS-stream-mode-switch ./`.
- Runtime page route was served by the rebuilt provider: `GET /device/00008020-00024D8E3E88003A/stream-mode` returned HTML containing `GADS iOS Stream Mode`, `Broadcast Fast`, and `MJPEG Fast`.
- Rebuilt Hub with embedded UI assets and overlay: `go build -tags ui -o /Users/corylin/Project/ai/ios-farm/.local/gads/artifacts/GADS-hub-stream-mode-ui ./`.
- Playwright verified `http://127.0.0.1:10001/devices/control/00008020-00024D8E3E88003A` exposes overlay buttons `Broadcast Fast`, `MJPEG Fast`, `MJPEG Full`, and `Start Recording`; screenshot saved at `/tmp/gads-hub-control-stream-overlay.png`.
- Playwright clicked `Broadcast Fast`; the browser received HTTP 200 from `http://127.0.0.1:12000/device/00008020-00024D8E3E88003A/update-stream-settings`. Provider detail then reported `provider_state=live`, `stream_type=ios_webrtc_broadcast`, `stream_target_fps=30`, `stream_jpeg_quality=50`, and `stream_scaling_factor=70`.

Runtime blocker:

- Do not claim Broadcast recording start unless `gads-broadcast-extension` is visible on the device and a browser receives a real frame. The stream switch is UI/API verified; recording start remains an assisted human/system flow.

## 2026-05-10: iPhone SE Broadcast setup and black-screen verification

Decision: use `ios_webrtc_broadcast` for the iPhone SE (`00008030-000265002ED1402E`) active remote-control path, and judge health only from real browser video evidence plus a control action.

Why:

- The SE was initially configured around MJPEG and did not have a usable ReplayKit Broadcast app/extension installed.
- Existing Broadcast IPAs were not valid for this SE: one profile was expired or provisioned only for the XR, and the repo artifact profile did not include the SE UDID.
- Provider `live`, WDA/Appium health, and WebRTC track arrival are not sufficient to rule out a black screen. The gate is a real `<video>` frame with non-black pixels in `13001` or `10001`.

Implementation memory:

- Rebuilt the Broadcast app from `resources/ios-broadcast-extension/` with local Xcode automatic signing for the SE and installed bundle `com.codeyee.gads.broadcast`.
- Added missing extension metadata to both Broadcast extension plists so iOS accepts the archive: non-empty `CFBundleDisplayName`, SetupUI `NSExtension` for `com.apple.broadcast-services-setupui`, and Upload `NSExtension` for `com.apple.broadcast-services-upload` with `RPBroadcastProcessModeSampleBuffer`.
- Hub device config for the SE uses `stream_type=ios_webrtc_broadcast`. The older `use_webrtc_video` field is deprecated and may remain false in stored records; current stream selection is `stream_type`.

Verification evidence:

- Provider `GET /device/00008030-000265002ED1402E/info` returned `provider_state=live`, `connected=true`, `is_appium_up=true`, `has_appium_session=true`, and `stream_type=ios_webrtc_broadcast`.
- Real device process list showed `h264-broadcast-extension`, `gads-broadcast-extension`, `WebDriverAgentRunner-Runner`, and `SpringBoard`.
- Direct viewer `http://127.0.0.1:13001` targeted at the SE wrote `/tmp/gads-se-broadcast-targeted-smoke/summary.json`: `requestVideoFrameCallback` first frame around `514ms`, `readyState=4`, `videoWidth=750`, `videoHeight=1334`, and `nonBlackRatio=0.975`.
- Hub UI `http://127.0.0.1:10001/devices/control/00008030-000265002ED1402E` wrote `/tmp/gads-se-hub-smoke/summary.json`: `readyState=4`, `750x1334`, `waitingVisible=false`, `nonBlackRatio=0.975`, and WebRTC logs ended with `Video is playing, hiding loading overlay`.
- Hub proxy control check `POST /device/00008030-000265002ED1402E/swipe` returned success with `{"value":null}`.

Operational note:

- If the human still sees a black screen after this verified state, first refresh or re-enter the Hub control page for the SE and make sure the selected UDID is `00008030-000265002ED1402E`. The current runtime has verified SE Broadcast frames; do not switch back to MJPEG as a primary fix.

## 2026-05-10: iOS clipboard fast path

Decision: iOS clipboard reads should try `wda/getPasteboard` directly with a short timeout before any WDA app activation.

Why:

- The old path always activated WebDriverAgent before reading the pasteboard and then navigated Home. On the XR this visibly brought a black WDA-like app to the foreground and could stall for 15-50 seconds.
- Direct `wda/getPasteboard` worked without foreground activation on the current WDA runtime and returned in about 0.1 seconds during manual testing.
- A clipboard read should not disrupt the human's current foreground app unless the fast path fails and the fallback is explicitly needed.

Implementation memory:

- `provider/router/control.go` now supports a short-timeout WDA HTTP client for selected calls.
- `deviceGetClipboard` first calls `wda/getPasteboard` directly through the normal no-session/session fallback chain.
- Only if the fast path fails does it activate WDA with a 5 second timeout; in that fallback it tries to restore the previous foreground app instead of forcing SpringBoard/Home.

Verification evidence:

- Static checks passed: `go test -vet=off ./provider/...` and `git diff --check`.
- Rebuilt runtime binary `.local/gads/artifacts/GADS-clipboard-fastpath` and restarted `tmux:gads-provider-fastpath`.
- SE provider-level verification passed three consecutive `GET /device/00008030-000265002ED1402E/getClipboard` calls in `0.04-0.10s`; the device log showed only `wda/getPasteboard` calls and no `wda/apps/activate` or `wda/homescreen`.

Known runtime blocker:

- XR provider-level verification could not complete in this run because after provider restart, `ios list --details` from go-ios only returned the SE even though `system_profiler` and `xcrun devicectl` showed the XR physically connected, paired, and CoreDevice tunnel-connected. Provider therefore kept XR at `provider_state=init`, `connected=false`.

Correction on 2026-05-10:

- The earlier SE verification was incomplete. It proved latency and that the route returned HTTP 200, but it did not prove clipboard content was usable.
- `wda/getPasteboard` can return HTTP 200 with `{"value":""}` when WDA is not the foreground app, even if a later foreground WDA read can return real pasteboard data.
- `DeviceGetClipboard` now decodes the WDA base64 payload and returns the text in both `message` and `result`.
- The iOS fast path now treats WDA 200 plus empty clipboard value as a fast-path miss, then activates WDA and rereads before returning.
- Verification must include provider content and browser content: `GET /device/<udid>/getClipboard` must return the expected non-empty text in `result`, and Hub `Get clipboard` must make `navigator.clipboard.readText()` return the same text. The toast `Device clipboard copied!` is not a success gate.

Verification evidence for the correction:

- Static checks passed: `go test -vet=off ./provider/router ./provider/...`.
- Rebuilt `.local/gads/artifacts/GADS-clipboard-fastpath` and restarted `tmux:gads-provider-fastpath`.
- On XR, with Gmail foregrounded and a known WDA-set sentinel clipboard value, provider `GET /device/00008020-00024D8E3E88003A/getClipboard` returned `result="gads-clipboard-smoke-foreground-222244"`.
- Hub `http://127.0.0.1:10001/devices/control/00008020-00024D8E3E88003A` `Get clipboard` returned the same provider `result`, and browser `navigator.clipboard.readText()` also returned `gads-clipboard-smoke-foreground-222244`.

Follow-up latency correction:

- The first correction still used WDA HTTP `/wda/apps/activate` as the primary activation path. Under live XR use, that route timed out after 5 seconds, then the immediate pasteboard reread timed out after another 5 seconds; Hub showed `Failed to get device clipboard!` after about `10.2s`.
- The replacement path checks the foreground app first. If WDA is not foreground, it skips the direct pasteboard read that can hang, uses iOS process-control to bring WDA foreground without killing the WDA process, waits only until WDA is confirmed foreground, then reads pasteboard once with enough timeout for iOS pasteboard alert handling. The previous foreground app is restored asynchronously so the HTTP request does not wait on the restore step.
- Current provider check after rebuilding and restarting `.local/gads/artifacts/GADS-clipboard-fastpath`: `GET /device/00008020-00024D8E3E88003A/getClipboard` returned sentinel `gads-clipboard-live-224509` in `result` with HTTP 200 in about `2.8s`.
- Current Hub check: `http://127.0.0.1:10001/devices/control/00008020-00024D8E3E88003A` `Get clipboard` returned HTTP 200 in about `2.5s`; browser `navigator.clipboard.readText()` returned the same sentinel `gads-clipboard-live-224509`.

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
