# Setup Guide for Provider Component

The provider component is responsible for setting up the Appium servers and managing dependencies for connected devices. It exposes these devices for testing or remote control.

## Table of Contents

- [Provider Configuration](#provider-configuration)
- [Provider Data Folder](#provider-data-folder---optional)
- [Provider Setup](#provider-setup)
  - [macOS](#macos)
  - [Linux](#linux)
  - [Windows](#windows)
- [Dependencies Notes](#dependencies-notes)
- [Device Notes](#device-notes)
  - [iOS Phones](#ios-phones)
  - [Android](#android-phone)
  - [Tizen TV](#tizen-tv)
  - [WebOS TV](#webos-tv)
- [Starting Provider Instance](#starting-a-provider-instance)
- [Logging](#logging)

## Provider Configuration

**Provider configuration is added through the GADS UI:**

1. Log in to the hub UI with an admin user.
2. Navigate to the `Admin` section.
3. Open `Providers`.
4. On the `New provider` tab, fill in all necessary data and save.
5. You should see a new provider component with the configuration you provided. You can now start up a provider instance using this configuration.

## Provider Data Folder - Optional

The provider requires a persistent folder to store logs, apps, and other files.

You can skip this step, and the provider will create a folder named after the provider instance nickname relative to where the provider binary is located. For example, if you run the provider in `/Users/shamanec/GADS` with the nickname `Provider1`, it will create `/Users/shamanec/GADS/Provider1` to store all related data.

To specify a folder, create it on your machine and provide it at startup using the `--provider-folder` flag.

## Provider Setup

### macOS

#### Common

- **Install** [Appium](#appium)

#### Android

- **Install** [ADB (Android Debug Bridge)](#adb---android-debug-bridge) if providing Android devices.
- **Enable** [USB Debugging](#usb-debugging) on each Android device.

#### iOS

- **Prepare** [WebDriverAgent](#build-webdriveragent-ipa-file-manually-using-xcode).
- (Optional) **Supervise** [your iOS devices](#supervise-devices).

#### Tizen

- **Install** [SDB (Smart Development Bridge)](#sdb---tizen-only)
- **Enable** [Developer Mode](#developer-mode-tizen) on each Tizen TV

#### WebOS

- **Install** [WebOS CLI](#webos-cli---webos-only)
- **Enable** [Developer Mode](#developer-mode---webos) on each WebOS TV

<br>

---

### Linux

#### Common

- **Install** [Appium](#appium)

#### Android

- **Install** [ADB (Android Debug Bridge)](#adb---android-debug-bridge) if providing Android devices.
- **Enable** [USB Debugging](#usb-debugging) on each Android device.

#### iOS

- **Install** [usbmuxd](#usbmuxd) if providing iOS devices.
- **Prepare** [WebDriverAgent](#prebuilt-custom-webdriveragent).
- (Optional) **Supervise** [your iOS devices](#supervise-devices).

#### Tizen

- **Install** [SDB (Smart Development Bridge)](#sdb---tizen-only)
- **Enable** [Developer Mode](#developer-mode-tizen) on each Tizen TV

#### WebOS

- **Install** [WebOS CLI](#webos-cli---webos-only)
- **Enable** [Developer Mode](#developer-mode---webos) on each WebOS TV

#### ⚠️ Known Limitations - Linux, iOS

1. The command **driver.executeScript("mobile: startPerfRecord")** cannot be executed due to the unavailability of Xcode tools.
2. Any functionality requiring Instruments or other Xcode/macOS-exclusive tools is limited.

<br>

---

### Windows

#### Common

- **Install** [Appium](#appium)

#### Android

- **Install** [ADB (Android Debug Bridge)](#adb---android-debug-bridge) if providing Android devices.
- **Enable** [USB Debugging](#usb-debugging) on each Android device.

#### iOS

- **Install** [iTunes](#itunes) if providing iOS devices.
- **Prepare** [WebDriverAgent](#prebuilt-custom-webdriveragent).
- (Optional) **Supervise** [your iOS devices](#supervise-devices).

#### Tizen

- **Install** [SDB (Smart Development Bridge)](#sdb---tizen-only)
- **Enable** [Developer Mode](#developer-mode-tizen) on each Tizen TV

#### WebOS

- **Install** [WebOS CLI](#webos-cli---webos-only)
- **Enable** [Developer Mode](#developer-mode---webos) on each WebOS TV

#### ⚠️ Known Limitations - Windows, iOS

1. The command **driver.executeScript("mobile: startPerfRecord")** cannot be executed due to the unavailability of Xcode tools.
2. Any functionality requiring Instruments or other Xcode/macOS-exclusive tools is limited.

## Dependencies notes

### Appium - optional

If you want the configured devices to each have a respective Appium server set up registered in Selenium Grid or the GADS Appium grid for test execution you need to enable this in the provider configuration in the Admin UI!!!  
**NOTE** Appium has to be installed and set up on the provider host machine if you want to take advantage of this.  
Installation is pretty similar for all operating systems, you just have to find the proper steps for your setup.

- Install Node > 16
- Install Appium with `npm install -g appium`
- Install Appium drivers
  - iOS - `appium driver install xcuitest`
  - Android - `appium driver install uiautomator2`
  - Tizen TV - `appium driver install --source=npm appium-tizen-tv-driver`
  - WebOS TV - `appium driver install --source=npm appium-lg-webos-driver`
- Add any additional Appium dependencies like `ANDROID_HOME`(Android SDK) environment variable, Java, etc.
- Test with `appium driver doctor uiautomator2` and `appium driver doctor xcuitest` to check for errors with the setup.

<br>

---

### adb - Android Only

`adb` (Android Debug Bridge) is mandatory when providing Android devices. You can skip installing it if no Android devices will be provided.

- Install `adb` in a valid way for the provider OS. It should be available in PATH so it can be directly accessed via terminal. <br>
  Example installation on macOS - `brew install adb`

<br>

---

### usbmuxd - Linux -> iOS

`usbmuxd` is used only on **Linux** and only when providing **iOS devices**.  
Example installation command for Ubuntu - `sudo apt install usbmuxd`.

---

### iTunes - Windows -> iOS

`iTunes` is needed only on **Windows** and mandatory when providing **iOS devices**. Install it through an installation package or Microsoft Store, shouldn't really matter

### WebDriverAgent -> iOS

#### WebDriverAgent ipa

You need to prepare and upload a signed `WebDriverAgent` ipa file from the hub UI in `Admin > Files`  
GADS supports only WebDriverAgent from my [fork](https://github.com/shamanec/WebDriverAgent).  
The fork has optimizations for the mjpeg video stream and additional endpoints for faster tap/swipe interactions that are not available in the mainstream repo.  
Additionally those endpoints require different coordinates for interaction from mainstream WDA which forces separate handling for the remote control which is too much work.  
Fork is kept up to date with latest mainstream.

#### Prebuilt custom WebDriverAgent

- Download the prebuilt `WebDriverAgent.ipa` from my fork of [WebDriverAgent](https://github.com/shamanec/WebDriverAgent)
- Use any tool to re-sign it with your developer account (or provisioning profile + certificate)
  - [zsign](https://github.com/zhlynn/zsign)
  - [fastlane-sigh](https://docs.fastlane.tools/actions/sigh/)
  - [codesign](https://developer.apple.com/library/archive/documentation/Security/Conceptual/CodeSigningGuide/Procedures/Procedures.html)
  - Re-sign from hub UI - TODO

#### Build WebDriverAgent IPA file manually using Xcode

- Download the code from the `main` branch of my fork of [WebDriverAgent](https://github.com/shamanec/WebDriverAgent).
- **macOS only**: ensure `xcode-select` points at the full Xcode app, not just Command Line Tools:
  ```bash
  sudo xcode-select -s /Applications/Xcode.app/Contents/Developer
  ```
- Open `WebDriverAgent.xcodeproj` in Xcode.
- Select the `WebDriverAgentRunner` target → `Signing & Capabilities` tab.
  - Enable **Automatically manage signing** and set your Team.
  - Set the Bundle Identifier to something unique, e.g. `com.yourname.WebDriverAgentRunner`. Note this value — you will need to enter it as the **WDA bundle ID** in the provider configuration.
- Plug in your iOS device and select it as the build destination in the Xcode toolbar (required for free Apple Developer accounts so the device UDID is included in the provisioning profile).
- Select `Build > Clean build folder` (just in case)
- Select `Product > Build For Testing`. This will create a `Products/Debug-iphoneos` folder under the DerivedData directory.  
   `Example`: **/Users/<username>/Library/Developer/Xcode/DerivedData/WebDriverAgent-<hash>/Build/Products/Debug-iphoneos**
- Navigate to that folder and package the IPA using `ditto` (macOS only). Using `cp`/`zip` will break the code signature:
  ```bash
  cd /Users/<username>/Library/Developer/Xcode/DerivedData/WebDriverAgent-<hash>/Build/Products/Debug-iphoneos
  mkdir Payload
  ditto WebDriverAgentRunner-Runner.app Payload/WebDriverAgentRunner-Runner.app
  ditto -c -k --sequesterRsrc --keepParent Payload ~/Downloads/WebDriverAgent-signed.ipa
  ```
- **NB** iOS 17-17.3 Windows/Linux WebDriverAgent additional step
  - Open the `.app` bundle, navigate to `Frameworks` and delete the `XC*.framework` folders before moving it to `Payload`
  - IPA has to be re-signed after that once again using any applicable tool

## Device Notes

### iOS Phones

#### Enable Developer mode - iOS 16+ only

Developer mode needs to be enabled on iOS 16+ devices to allow `go-ios` usage against the device

- Open `Settings > Privacy & Security > Developer Mode`
- Enable the toggle

#### Disable Auto-Lock

> **Required.** When the device screen locks, iOS drops the tunnel connection used by GADS, causing WebDriverAgent to crash and the provider to enter a setup loop.

- Open `Settings > Display & Brightness > Auto-Lock`
- Set to **Never**

#### Allow WebDriverAgent paste access - iOS 16+ only

> **Required for reliable clipboard reads.** On iOS 16 and later, the system asks before one app reads pasteboard content copied from another app. GADS reads the iOS clipboard through `WebDriverAgentRunner-Runner`, so new real iOS devices should allow WDA paste access once during onboarding.

- Install and launch WebDriverAgent on the device, or trigger one GADS clipboard read so iOS creates the app privacy entry.
- Open `Settings > Apps > WebDriverAgentRunner-Runner > Paste from Other Apps`.
- Set it to **Allow**.

This setting is normally kept when the same device reconnects or when the same WDA app is updated in place with the same bundle identifier. Recheck it after uninstalling/reinstalling WDA, changing the WDA bundle identifier, erasing/resetting the device, or if `Failed to get device clipboard!` appears with an `Allow Paste` prompt. If the `Paste from Other Apps` menu is missing, launch WDA or trigger one clipboard read first so iOS creates the per-app setting.

#### Supervise devices

This is an optional but a preferable step - it can make devices setup more autonomous - it can allow trusted pairing with devices without interacting with Trust popup  
**NB** You need a Mac machine to do this!

- Install Apple Configurator 2 on your Mac.
- Attach your first device.
- Set it up for supervision using a new (or existing) supervision identity. You can do that for free without having a paid MDM account.
- Connect each consecutive device and supervise it using the same supervision identity.
- Export your supervision identity file and choose a password.
- Save your new supervision identity as `*.p12` file.
- Log in to the hub with admin and upload the `*.p12` file via `Admin > Files`.

**NB** Make sure to remember the supervision password, you need to set it up in the provider config for each provider that will use a supervision profile.  
**NB** Provider will fall back to manual pairing if something is wrong with the supervision profile, provided password or supervised pairing.  
**NB** You can skip supervising the devices and you should trust manually on first pair attempt by the provider but if devices are supervised in advance setup can be more autonomous.

#### Prepare Broadcast Extension for WebRTC video - optional

GADS supports iOS devices WebRTC video stream using a Broadcast Extension to generate H264 frames from the device screen. This is currently not automatically done by GADS. The legacy `resources/gads-broadcast.ipa` file can be useful as a reference artifact, but iOS provisioning profiles expire, so the source project under `resources/ios-broadcast-extension` is the reproducible path.

- Install [XcodeGen](https://github.com/yonaskolb/XcodeGen) if the host does not already have it.
- Generate and build the Broadcast host app. Replace the team and bundle prefix with values that match your Apple signing account:

```bash
cd resources/ios-broadcast-extension
xcodegen generate
xcodebuild \
  -scheme GADSBroadcastSigning \
  -configuration Release \
  -destination 'generic/platform=iOS' \
  DEVELOPMENT_TEAM=<APPLE_TEAM_ID> \
  GADS_BROADCAST_BUNDLE_PREFIX=com.example.gads.broadcast \
  CODE_SIGN_STYLE=Automatic \
  build
```

- Install the built `h264-broadcast-extension.app` from Xcode's `Release-iphoneos` products folder on each iOS device, for example with Xcode Devices and Simulators or `xcrun devicectl device install app`.
- On each device edit the Control Center and add the `Screen Recording` option
- Tap & hold the `Screen Recording` button until the context menu with selection appears
- Select `gads-broadcast-extension` from the menu and tap `Start Broadcast`
- The broadcast should start in a few seconds
- You can now start the provider instance

#### iOS Broadcast Extension operating guidance

The Broadcast Extension path is the current preferred low-latency iOS video path when the goal is interactive remote control. The device-side pipeline is:

1. ReplayKit captures screen `CMSampleBuffer` objects in a Broadcast Upload Extension.
2. The extension encodes frames to H264, preferably through VideoToolbox hardware encoding.
3. The extension serves length-prefixed Annex-B H264 frames on device port `8765`.
4. The provider forwards device port `8765` to the host, reads the H264 packets, and writes them to the iOS WebRTC Broadcast track.

The checked-in extension keeps the latest ReplayKit frame in memory, forces SPS/PPS plus an IDR frame for a new TCP client, and repeats the latest frame while a client is connected. This avoids the `track-only` failure mode where a static screen creates a WebRTC track but no decodable browser video frame.

This path avoids the WDA MJPEG screenshot stream and host-side FFmpeg re-encoding used by the fallback iOS WebRTC path, so it usually has much lower first-frame and interaction latency. On the iPhone XR validation device, the local baseline on 2026-04-21 was `readyState=4`, `828x1792`, and first frame around `489 ms` in the direct low-latency viewer.

It is not a good unattended 24/7 default. A system-wide ReplayKit broadcast keeps the device screen active, keeps the Broadcast Upload Extension alive, and continuously captures, encodes, and sends video. This can increase battery drain and device temperature, and sustained heat can later show up as lower frame rate, higher latency, system dialogs, or the broadcast being stopped by iOS.

Use this mode with the following policy:

| Scenario | Recommended mode |
| --- | --- |
| Interactive remote control, live debugging, short operator sessions | Use `ios_webrtc_broadcast`. Keep the broadcast running while an operator is connected. |
| Long idle periods where nobody is watching the screen | Stop the broadcast, or add an idle timer that stops it after a few minutes without an active viewer or input. |
| Long sessions that must stay visible | Lower `stream_target_fps` first, then lower bitrate in the Broadcast Extension. Start with 15-18 fps before reducing resolution. |
| Device gets hot, latency rises, or the device throttles | Stop the broadcast, let the phone cool, then restart at lower fps/bitrate. |
| Highest stability over lowest latency | Use the WDA/MJPEG based iOS path instead of the Broadcast Extension path. |

Recommended production guardrails:

- Keep the device physically cool: remove the case, keep it ventilated, and avoid stacking phones.
- Do not rely on a running Broadcast Extension as the only device health signal. Treat `8765` or broadcast process failure as stream health, not full device offline.
- Add automatic idle handling before using this in a long-running device farm: start the broadcast when a viewer opens the control page, and stop or reduce fps after an idle timeout.
- Add a thermal policy in the host app or extension when possible: on elevated thermal state, lower fps/bitrate; on critical thermal state, stop the broadcast.
- Keep a first-frame verification that checks the real `<video>` state, not just WebRTC track arrival. Prefer `requestVideoFrameCallback` or sampled non-black pixels; at minimum require `video.readyState >= 2`, `videoWidth > 0`, and `videoHeight > 0`.

If the control page shows `Waiting for video frames`, distinguish these cases before resetting the whole device:

- No `gads-broadcast-extension` process: the broadcast is not running. Start it from the Screen Recording picker.
- Provider cannot connect to forwarded port `8765`: the stream is down, but WDA/Appium may still be healthy.
- WebRTC reaches `track-only` but `<video>` has `readyState=0`: the browser received a track but no decodable H264 keyframe. The extension must emit SPS/PPS and an IDR frame for new connections, and should repeat the latest frame when the screen is static.
- iOS shows `Attempted to start an invalid broadcast session`: dismiss the system dialog and start a fresh broadcast session.

### iOS Stream Mode Switching

When the Hub UI source is unavailable, the provider exposes a small local operator page for switching the active iOS stream mode:

```text
http://127.0.0.1:12000/device/<UDID>/stream-mode
```

The local Hub can also show these controls directly on the normal control page:

```text
http://127.0.0.1:10001/devices/control/<UDID>
```

Because the `hub-ui` source submodule is not accessible in this checkout, the Hub binary injects a small local overlay into the embedded React fallback page. The overlay calls the provider API on `127.0.0.1:12000` and offers `Broadcast Fast`, `MJPEG Fast`, `MJPEG Full`, and `Start Recording`.

The page calls:

```http
POST /device/<UDID>/update-stream-settings
Content-Type: application/json

{
  "stream_type": "mjpeg",
  "target_fps": 24,
  "jpeg_quality": 40,
  "scaling_factor": 50
}
```

Supported iOS `stream_type` values are:

| Mode | Use when |
| --- | --- |
| `ios_webrtc_broadcast` | Lowest latency active control, if the ReplayKit Broadcast Extension is installed and already running. |
| `mjpeg` | Stable JPG/MJPEG fallback, useful when Broadcast is not running or is blank. |
| `ios_webrtc_ffmpeg` | WebRTC fallback built from WDA MJPEG plus host-side FFmpeg. |

Changing `stream_type` persists the device config and triggers provider reprovisioning for that device. It does not silently start ReplayKit Broadcast on iOS. Apple requires a user/system Broadcast picker flow, so automation can only assist by launching the host app, guiding/tapping the picker when possible, then verifying the real `gads-broadcast-extension` process and browser first frame.

`Start Recording` is an assist button, not a success signal. It attempts to open known Broadcast host app bundle IDs. The operator still has to complete the iOS Broadcast picker / Start Broadcast flow if iOS shows it, and the final check must confirm both the extension process and a real browser frame.

For speed over image quality in MJPEG mode, lower `scaling_factor` and `jpeg_quality` first. A practical fast preset is `24 fps / JPEG 40 / scale 50`. Raising fps while keeping `scale 100` can increase CPU, USB, and browser decode load without improving control latency.

Current alternatives and tradeoffs:

| Option | Pros | Cons | Fit |
| --- | --- | --- | --- |
| `ios_webrtc_broadcast` with ReplayKit + H264 + WebRTC | Best current latency; browser receives H264 directly; good remote-control UX | Requires a signed app/extension and a user-started broadcast; continuous screen capture can heat the phone; not ideal as a 24/7 idle daemon | Preferred for active sessions |
| `ios_webrtc_ffmpeg` using WDA MJPEG + host FFmpeg | No ReplayKit broadcast on the phone; easier to recover when the broadcast extension is not installed | Higher latency; WDA MJPEG and host transcoding add overhead; quality depends on WDA stream settings | Stable fallback |
| Plain WDA MJPEG/screenshot polling | Simple and compatible with standard WDA paths | Slow and bandwidth-heavy; usually unsuitable for smooth remote control | Debug fallback only |
| Native AirPlay/QuickTime mirroring | Can offload more work to system components | Not integrated with GADS control/session routing; harder to automate and multiplex in a device farm | Research option, not current baseline |

References for the operating assumptions:

- Apple ReplayKit `RPBroadcastSampleHandler` handles screen `CMSampleBuffer` objects in `RPBroadcastProcessModeSampleBuffer`: https://developer.apple.com/documentation/replaykit/rpbroadcastsamplehandler
- Apple `ProcessInfo` thermal guidance says apps should reduce system resource usage at higher thermal states: https://developer.apple.com/documentation/foundation/processinfo
- Twilio's ReplayKit screen-share guide describes the same Broadcast Extension model and warns that ReplayKit Broadcast Extensions have limited memory: https://www.twilio.com/docs/video/ios-v5-screen-share
- Twilio's ReplayKit sample notes the Broadcast Extension memory limit and uses H264/format requests to reduce memory usage: https://github.com/twilio/video-quickstart-ios/blob/master/ReplayKitExample/README.md

### Onboarding a New iOS Device to GADS

End-to-end runbook for adding a fresh iOS device (iOS 17/18) to a running GADS instance. Each step has a verification command. If a step fails, do not skip — read the troubleshooting section at the bottom.

#### Prerequisites

- Four `tmux` sessions running on the host: `gads-hub`, `gads-provider-fastpath`, `gads-low-latency-viewer`, `gads-ios-tunnel`.
- The tunnel session must run `ios tunnel start --userspace --tunnel-info-port=28100` (verify with `curl -s http://localhost:28100/tunnels`).
- MongoDB container running. **The container name is `gads-mongodb`** (not `gads-mongo`). Verify with `docker ps --format '{{.Names}}' | grep mongo`.
- Xcode is installed, opened at least once, with an Apple Developer account added in `Xcode → Settings → Accounts` (paid Individual/Organization team, not Free Provisioning Team).
- The signing certificate is in the macOS keychain. Confirm with `security find-identity -v -p codesigning`. The certificate subject looks like `/UID=.../CN=Apple Development: <name> (<DEV_ID>)/OU=<TEAM_ID>/O=...`. **The `OU` field is the Team ID** to use later; the `(<DEV_ID>)` inside CN is not the team ID.
- WDA source tree available at `.local/gads/recovered-wda/shamanec-WebDriverAgent-*/` (used by Step 9-11 to rebuild the IPA).
- Provider's active WDA IPA at `.local/gads/provider-instance/WebDriverAgent.ipa`.
- The device must be **unlocked and screen-on** for every `pair` step. Cable connected directly, not through a hub when possible.

#### Step-by-step

Replace `<UDID>` with the new device UDID. Replace `<TEAM_ID>` with the Team ID from the certificate `OU` field.

**1. Discover UDID and confirm it is new**

```bash
ios list
TOKEN=$(curl -s -X POST http://localhost:10001/authenticate \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"password"}' \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["result"]["access_token"])')
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:10001/admin/devices \
  | python3 -m json.tool | grep -E '"udid"|"name"'
```

Compare `ios list` UDIDs against the hub's known devices to identify the new one.

**2. lockdownd pair over USB**

```bash
ios pair --udid=<UDID>
```

On the device screen, accept the **Trust This Computer** dialog and enter the passcode.

Success line: `{"level":"info","msg":"Successfully paired <UDID>"}`.
If you see `Lockdown error: PasswordProtected`, the device screen is locked — unlock it and rerun.

**3. CoreDevice pair (activates the Developer Mode toggle on the device)**

```bash
xcrun devicectl list devices                                   # confirm new device is "available" (unpaired)
xcrun devicectl manage pair --device <UDID> --timeout 300
```

The device screen will show a second pairing prompt. Accept and enter the passcode again.

Verify:

```bash
xcrun devicectl list devices | grep <UDID-prefix>              # expect "available (paired)"
```

Without this CoreDevice pairing, the `Developer Mode` toggle will not appear on the device.

**4. Enable Developer Mode on the device**

Physical interaction on the device:

1. `Settings → Privacy & Security` → scroll to the bottom → tap `Developer Mode`.
2. Toggle on. Device asks to restart. Restart.
3. After reboot, unlock the device. iOS prompts "Turn on Developer Mode?". Tap **Turn On** and enter the passcode.

Verify from the host:

```bash
ios devmode get --udid=<UDID>                                  # "Developer mode enabled: true"
xcrun devicectl device info details --device <UDID> \
  --json-output /tmp/dm.json --quiet \
  && python3 -c 'import json;print(json.load(open("/tmp/dm.json"))["result"]["deviceProperties"]["developerModeStatus"])'
# expect: enabled
```

**5. Re-pair after the reboot**

iOS 17/18 wipes the lockdownd USB pair record after the Developer-Mode-induced reboot.

```bash
ios pair --udid=<UDID>
```

A new Trust dialog appears on the device. Accept + passcode again. Without this step, the provider will loop on `Please accept the PairingDialog`.

**6. Register the device in the hub**

Use an existing same-model device record as the template (run the `admin/devices` GET from Step 1 again to fetch one). For an iPhone SE 2nd/3rd gen the screen is `375 × 667`. Copy `workspace_id` from an existing device — do not hard-code.

```bash
curl -s -X POST http://localhost:10001/admin/device \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "udid": "<UDID>",
    "name": "<Display Name>",
    "os": "ios",
    "os_version": "<X.Y.Z>",
    "ip_address": "",
    "provider": "<your-provider-nickname>",
    "screen_width": "<W>",
    "screen_height": "<H>",
    "device_type": "real",
    "stream_type": "mjpeg",
    "usage": "enabled",
    "use_webrtc_video": false,
    "workspace_id": "<copy-from-existing-device>"
  }'

# Verify
docker exec gads-mongodb mongosh --quiet gads \
  --eval "db.new_devices.findOne({udid:'<UDID>'})"
```

**7. Check whether the active WDA IPA already provisions this device**

```bash
mkdir -p /tmp/wda-check && cd /tmp/wda-check
unzip -qo /Users/corylin/Project/ai/ios-farm/.local/gads/provider-instance/WebDriverAgent.ipa
python3 - <<'PYINNER'
import plistlib, subprocess
data = subprocess.check_output(["security","cms","-D","-i",
  "./Payload/WebDriverAgentRunner-Runner.app/embedded.mobileprovision"])
p = plistlib.loads(data)
print("Profile :", p.get("Name"))
print("Expires :", p.get("ExpirationDate"))
print("Devices :", p.get("ProvisionedDevices"))
PYINNER
```

- If the new UDID is already in `ProvisionedDevices` **and** the profile has not expired → skip to Step 12.
- Otherwise continue with Steps 8-11 to rebuild and replace the IPA.

**8. Pause the provider's retry loop for this device**

The provider re-runs setup roughly once per second on failure. Each attempt sends a fresh pairing/install request, which iOS throttles after a few rapid requests, suppressing Trust dialogs and adding minutes of false negatives. Disable the device before manual work:

```bash
docker exec gads-mongodb mongosh --quiet gads \
  --eval "db.new_devices.updateOne({udid:'<UDID>'},{\$set:{usage:'disabled'}})"
sleep 15
grep <UDID> /Users/corylin/Project/ai/ios-farm/.local/gads/provider-instance/provider.log | tail -3
# expect: no new lines after the disable timestamp
```

**9. Register the device on the Apple Developer Portal (one-time Xcode GUI step)**

`xcodebuild -allowProvisioningUpdates` cannot register a new device from the CLI unless an App Store Connect API key is configured. The simplest path is the Xcode GUI, one minute total:

1. Open the WDA project:
   ```bash
   WDA=$(ls -d /Users/corylin/Project/ai/ios-farm/.local/gads/recovered-wda/shamanec-WebDriverAgent-*/)
   open -a Xcode "$WDA/WebDriverAgent.xcodeproj"
   ```
2. `Shift+Cmd+2` → Devices and Simulators → confirm the new device appears (briefly `Connecting`, then ready).
3. Close that window. In the project window, change the active scheme (toolbar, next to destination) from the default to **`WebDriverAgentRunner`**.
4. Change destination to the new device.
5. `Cmd+B`. The first build registers the device on the developer portal and refreshes the wildcard provisioning profile.
6. Wait for `Build Succeeded`, then quit Xcode.

**10. Rebuild WDA Runner from CLI with the refreshed profile**

```bash
WDA=$(ls -d /Users/corylin/Project/ai/ios-farm/.local/gads/recovered-wda/shamanec-WebDriverAgent-*/)
mkdir -p /tmp/wda-build
cd "$WDA"
xcodebuild build-for-testing \
  -project WebDriverAgent.xcodeproj \
  -scheme WebDriverAgentRunner \
  -destination "id=<UDID>" \
  -derivedDataPath /tmp/wda-build \
  -allowProvisioningUpdates \
  DEVELOPMENT_TEAM=<TEAM_ID> \
  CODE_SIGN_STYLE=Automatic
```

Success line at the very end: `** TEST BUILD SUCCEEDED **`.

If you see `No Account for Team "<id>"`, the team ID is wrong — re-read the OU field of the certificate, not the parenthesised dev ID.

**11. Verify the rebuilt profile, package the IPA, replace and back up**

```bash
APP=/tmp/wda-build/Build/Products/Debug-iphoneos/WebDriverAgentRunner-Runner.app

# Verify all expected devices are present and the profile is fresh
python3 - <<'PYINNER'
import plistlib, subprocess
data = subprocess.check_output(["security","cms","-D","-i",
  "/tmp/wda-build/Build/Products/Debug-iphoneos/WebDriverAgentRunner-Runner.app/embedded.mobileprovision"])
p = plistlib.loads(data)
print("Profile:", p.get("Name"))
print("Expires:", p.get("ExpirationDate"))
for d in p.get("ProvisionedDevices", []): print("  -", d)
PYINNER

# Repackage as IPA
mkdir -p /tmp/wda-build/ipa-stage/Payload
cp -R "$APP" /tmp/wda-build/ipa-stage/Payload/
cd /tmp/wda-build/ipa-stage
zip -qr /tmp/wda-build/WebDriverAgent.ipa Payload

# Back up the current provider IPA and replace
TS=$(date +%Y%m%d)
IPA=/Users/corylin/Project/ai/ios-farm/.local/gads/provider-instance/WebDriverAgent.ipa
cp "$IPA" "$IPA.bak-$TS"
cp /tmp/wda-build/WebDriverAgent.ipa "$IPA"
```

**12. Re-enable the device**

```bash
docker exec gads-mongodb mongosh --quiet gads \
  --eval "db.new_devices.updateOne({udid:'<UDID>'},{\$set:{usage:'enabled'}})"
```

The provider's next retry (within ~60 s) picks up the new IPA, installs it, mounts the Developer Disk Image, launches WDA over `testmanagerd`, then starts Appium.

**13. Verify live**

```bash
# Wait up to 2 minutes, then:
curl -s http://localhost:12000/device/<UDID>/info | python3 -m json.tool
# expect: provider_state=live, connected=true, is_appium_up=true

# In the browser: http://localhost:10001 → log in admin/password → device list shows new device →
# open control page → confirm stream renders and a real tap responds.
```

Done.

#### Troubleshooting

1. **`No Account for Team "<id>"` on xcodebuild.** Wrong Team ID. Take the certificate `OU` field, not the parenthesised dev signing ID inside CN.
2. **MongoDB commands fail with "No such container".** The container name is `gads-mongodb`, not `gads-mongo`.
3. **`Please accept the PairingDialog on the device` looping after reboot.** Step 5 was skipped. Enabling Developer Mode triggers a reboot that wipes the lockdownd USB pair record. Run `ios pair --udid=<UDID>` again with the device unlocked.
4. **Trust dialog never appears even though the device is unlocked.** The provider's retry loop is spamming pair requests and iOS has throttled the prompt. Disable the device (Step 8) for at least 15 seconds, then try `ios pair` manually.
5. **`Did not find test app for 'com.codeyee.tempwda.xctrunner' on device. Is it installed?`.** The install step looked successful in logs but the IPA's provisioning profile does not include this device, so iOS silently refused the install. Diagnose with `ios apps --all --udid=<UDID> | grep -iE 'codeyee|wda|xctrunner'` — empty output confirms the silent failure. Fix by Steps 9-11.
6. **`lost connection to testmanagerd. the test-runner may have been killed`.** Usually a transient first-launch validation by iOS. The next provider retry typically succeeds. If it persists for 3+ retries, re-check the profile expiration and provisioned devices.
7. **No "Developer App" entry under `Settings → General → VPN & Device Management`.** Expected. On iOS 17+, Xcode automatic-signing certificates on a paired device are implicitly trusted; the manual cert-trust step required on iOS 16 and earlier no longer applies. If the entry is missing it is not a problem.
8. **`go-ios agent is not running` warning on `ios` commands.** Cosmetic. As long as the userspace tunnel session is running and `curl http://localhost:28100/tunnels` returns the device, ignore the warning.

#### WDA profile renewal

Provisioning profiles issued by Apple Developer Portal expire after one year. When the profile is close to expiry or already expired, the same silent-install failure mode in Troubleshooting #5 will occur, even though no new device was added. Recovery is just Steps 9-11 — Steps 1-6 do not need to be repeated.

To check the current profile's expiry without onboarding, run the verification block in Step 7.

### Android Phones

#### USB Debugging

- On each device activate `Developer options`, open them and enable `Enable USB debugging`
- Connect each device to the host - a popup will appear on the device to pair - allow it.

#### Android WebRTC video - EXPERIMENTAL

GADS has experimental WebRTC video streaming for Android that can be used instead of MJPEG. The quality can be lower because it is controlled by WebRTC itself but it can potentially work better on external networks with lower bandwidth consumption. There are two different WebRTC options - GetStream and GADS H264. If you have issues with one of them you can try using the other.

##### WebRTC device setup

- Go to `Admin > Devices` in the hub UI
- Set `Use WebRTC video?` to `true` for the target device
- Select a preferred video [codec](#webrtc-video-codecs)

##### WebRTC video codecs

Many Android phones support hardware encoding for H264/VP8.  
Some devices like Huawei do not - for them software encoding is enforced.  
You can test the performance and select H264, VP8 or VP9 per device to achieve the best quality and performance of the video stream.  
Note that it is possible that on some devices it might not work at all, in this case you should disable WebRTC and use the MJPEG stream instead.

**NB** It is complex to handle both device encoder and browser decoder limitations, I would suggest using Chrome/Safari, but I assume that most of the time also Firefox should manage.  
**NB** WebRTC video has some initial delay/latency while calculating the bitrate and connection capabilities when you access the device control.

## Starting a provider instance

- Execute `./GADS provider` providing the following flags:
  - `--nickname=` - mandatory, this is used to get the correct provider configuration from MongoDB
  - `--mongo-db=` - optional, IP address and port of the MongoDB instance (default is `localhost:27017`)
  - `--provider-folder=` - optional, folder where provider should store logs and apps and other needed files. Can be relative path to the folder where provider binary is located or full path on the host - `./test`, `.`, `./test/test1`, `/Users/shamanec/Desktop/test` are all valid. Default is the folder where the binary is currently located - `.`
  - `--log-level=` - optional, how verbose should the provider logs be (default is `info`, use `debug` for more log output)
  - `--hub=` - mandatory, the address of the hub instance so the provider can push data to it automatically, e.g `http://192.168.68.109:10000`
  - `--use-ios-pair-cache` - optional, cache iOS pair records on disk to skip the Trust dialog on reconnect for unsupervised devices (default is `false`)

## Logging

Provider logs both to local files and to MongoDB.
Provider logs can be found in the `provider.log` file in the used provider folder - default or provided by the `--provider-folder` flag.  
They will also be stored in MongoDB in DB `logs` and collection corresponding to the provider nickname.

## Device logs

On start a log folder and file is created for each device relative to the used provider folder - default or provided by the `--provider-folder` flag.  
They will also be stored in MongoDB in DB `logs` and collection corresponding to the device UDID.

For devices that start a local Appium server, the provider also writes the raw Appium startup/runtime output to `device_<udid>/appium-server.log` inside the provider folder. This is the first place to check when the device stays in `preparing` but provider logs only show `startAppium ... exit status 1`.

If Appium fails with `argument --use-plugins: Could not read file 'gads': EISDIR`, the child process is being started from a directory where `gads` resolves to a local folder on a case-insensitive filesystem. Start Appium from a neutral provider/device folder, not from the repository root that contains `GADS/`.

### SDB - Tizen Only

`sdb` (Smart Development Bridge) is mandatory when providing Tizen TV devices. You can skip installing it if no Tizen devices will be provided.

- Download and install [Tizen Studio CLI](https://developer.tizen.org/development/tizen-studio/download)
- Set up environment variables:
  ```bash
  # Add to your ~/.bashrc or equivalent
  export TIZEN_HOME=/path/to/tizen-studio
  export PATH=${PATH}:${TIZEN_HOME}/tools:${TIZEN_HOME}/tools/ide/bin
  ```
- Ensure `sdb` is available in PATH by running `sdb version` in terminal
- Restart your terminal or run `source ~/.bashrc` to apply changes

**Note**: Replace `/path/to/tizen-studio` with your actual Tizen Studio installation path. Common locations are:

- macOS: `/Users/<username>/tizen-studio`
- Linux: `/home/<username>/tizen-studio`
- Windows: `C:\tizen-studio`

## Tizen TV

### Developer Mode

- On each TV, navigate to Settings and enter the Apps menu
- Select the "Developer mode" option
- Enable Developer mode and enter the IP address of your development machine
- Accept any security prompts that appear
- The TV will restart to apply the changes

### Device Connection

- Ensure the TV and the Appium host machine are on the same local network
- After enabling developer mode, connect to the TV using SDB:
  ```bash
  sdb connect <tv-ip-address>
  ```
- Verify the connection by running:

  ```bash
  sdb devices
  ```

- The TV should appear in the list of connected devices with status "device"
- First connection will require accepting a pairing request on the TV
- For app testing:
  - Only correctly-signed debug versions of apps can be tested
  - Apps must be built with the appropriate Tizen TV SDK certificates

### Known Limitations

- Video streaming is not available for Tizen TV devices
- Some remote control features may be limited due to TV-specific interactions
- Screen dimensions are fixed based on TV resolution

### WebOS CLI - WebOS Only

`WebOS CLI` is mandatory when providing WebOS TV devices. You can skip installing it if no WebOS devices will be provided.

- Download the [WebOS TV CLI](https://webostv.developer.lge.com/develop/tools/webos-tv-cli-installation) (v1.12.4 recommended)
- Extract the downloaded CLI archive and place the extracted contents in `${LG_WEBOS_TV_SDK_HOME}/CLI`
- Set up environment variables:
  ```bash
  # Add to your ~/.bashrc or equivalent
  export LG_WEBOS_TV_SDK_HOME=/path/to/webOS_TV_SDK
  export WEBOS_CLI_TV=${LG_WEBOS_TV_SDK_HOME}/CLI
  export PATH=${PATH}:${WEBOS_CLI_TV}/bin
  ```
- Ensure `ares` commands are available in PATH by running `ares -V` in terminal
- Restart your terminal or run `source ~/.bashrc` to apply changes

**Note**: Replace `/path/to/webOS_TV_SDK` with your actual WebOS TV SDK installation path. Common locations are:

- macOS: `/Users/<username>/webOS_TV_SDK`
- Linux: `/home/<username>/webOS_TV_SDK`
- Windows: `C:\webOS_TV_SDK`

## WebOS TV

### Developer Mode - WebOS

- Install the Developer Mode app from LG Content Store
- Sign in with your LG Developer account (create one at https://webostv.developer.lge.com if needed)
- Enable Developer Mode by clicking the Dev Mode Status button
- The TV will reboot automatically

### Device Connection

- Ensure the TV and the provider host machine are on the same network
- Add the TV as a device using the WebOS CLI:

  ```bash
  ares-setup-device --add target -i "host=10.123.45.67" -i "port=9922" -i "username=prisoner" -i "default=true"
  ```

  > **⚠️ IMPORTANT**: The device name (e.g., `target` in the example above) must be:
  >
  > - Descriptive and meaningful for your setup
  > - **EXACTLY the same** as the device name registered in GADS
  > - If the names don't match, there will be configuration issues with the provisioned Appium server for the TV
  - Default port is 9922
  - Default username is "prisoner"
  - Leave password empty

- For first-time connections, you'll need to accept the pairing request on the TV
- Verify the connection by running:
  ```bash
  ares-setup-device --list
  ```
- The TV should appear in the list with its IP:PORT identifier

### Chromedriver Requirements

- WebOS TVs require Chromedriver 2.36 for compatibility
- GADS will manage the Chromedriver installation automatically
- The driver path will be configured in Appium capabilities

### Device UDID Format

- WebOS devices use the format `IP:PORT` as their UDID (e.g., `192.168.1.100:9922`)
- This UDID must be registered in the GADS database before the device can be used

### Known Limitations

- Video streaming is not available for WebOS TV devices
- Remote control features are limited compared to mobile devices
- Only web-based TV apps can be automated (native apps have limited support)
- Developer Mode has a 1000-hour time limit and needs periodic renewal
