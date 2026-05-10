# AGENTS.md

## 项目入口

- 先读 `docs/repo-knowledge-map.md`，再按任务进入 `docs/provider.md`、`docs/hub.md` 或相关代码目录。
- 长任务必须维护 `.ai/todos.md`；只有实际验证过的步骤才能标记完成。
- 默认使用简体中文汇报；代码标识符、协议字段和命令保持英文。

## iOS 远控稳定性规则

- `ios_webrtc_broadcast` 是当前低延迟交互首选；`ios_webrtc_ffmpeg` / WDA MJPEG 是稳定兜底。
- 不要把 ReplayKit Broadcast Extension、设备端 `8765` 或 WDA MJPEG `9100` 的视频流问题等同于整机离线；先判断 WDA `8100`、Appium、设备枚举是否仍健康。
- `Waiting for video frames` 不能只看 WebRTC track；真实通过优先看 `requestVideoFrameCallback` 或非黑屏像素，最低也要检查浏览器 `<video>` 达到 `readyState >= 2` 且 `videoWidth/videoHeight > 0`。
- iOS 控制链优先复用健康 Appium session；发现 stale session 后要清理本地状态，再 fallback 到 control session 或 WDA session endpoint。
- iOS 设备枚举有抖动，不能因为一次 `ios.ListDevices()` miss 立刻 reset 整机。

## 本地运行与验证

- 长期 provider 进程用本机 `tmux` 托管，不使用一次性 background tool 伪装后台运行。
- 修改 provider 后最小验证是 `go test -vet=off ./provider/...`，涉及 iOS 实机链路时还要重编译 provider 并重启 tmux 进程。
- UI 验证必须覆盖 `12000` provider info、`13001` 低延迟 viewer 首帧、`10001` hub 登录/设备在线/控制页动作。
- 提交前运行 `git status --short`、`git diff --check`，确认没有提交个人签名证书、临时 DerivedData、账号信息或不可复现本地路径。

## 可复用 Skill

- 本仓库版本化了 `.codex/skills/gads-ios-remote-stability/SKILL.md`，用于下次快速恢复 iOS Broadcast/WDA/Appium 稳定化流程。
- 如果当前 Codex 运行时没有自动加载项目内 skill，可把该目录复制或同步到 `$CODEX_HOME/skills/gads-ios-remote-stability` 后开启新会话。
