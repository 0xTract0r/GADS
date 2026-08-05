package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func DeviceStreamModePage(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(streamModePageHTML))
}

const streamModePageHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>GADS iOS Stream Mode</title>
    <style>
      :root {
        color-scheme: dark;
        --bg: #101317;
        --panel: #181d23;
        --line: #303945;
        --text: #eef3f8;
        --muted: #a6b2bf;
        --accent: #7dd3a8;
        --warn: #f5c96b;
      }
      * { box-sizing: border-box; }
      body {
        margin: 0;
        min-height: 100vh;
        display: grid;
        place-items: center;
        background: var(--bg);
        color: var(--text);
        font: 14px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      }
      main {
        width: min(680px, calc(100vw - 32px));
        padding: 24px;
      }
      h1 {
        margin: 0 0 6px;
        font-size: 22px;
        letter-spacing: 0;
      }
      .muted { color: var(--muted); }
      .panel {
        margin-top: 18px;
        border: 1px solid var(--line);
        background: var(--panel);
        border-radius: 8px;
        padding: 16px;
      }
      .status {
        display: grid;
        grid-template-columns: repeat(2, minmax(0, 1fr));
        gap: 8px;
      }
      .status div {
        min-height: 42px;
        border: 1px solid var(--line);
        border-radius: 6px;
        padding: 10px;
        background: #11161c;
      }
      .actions {
        display: grid;
        grid-template-columns: repeat(2, minmax(0, 1fr));
        gap: 10px;
      }
      button {
        min-height: 44px;
        border: 0;
        border-radius: 6px;
        padding: 10px 12px;
        background: #2a3440;
        color: var(--text);
        cursor: pointer;
        font-weight: 650;
      }
      button.primary {
        background: var(--accent);
        color: #07130c;
      }
      button.warning {
        background: var(--warn);
        color: #191204;
      }
      pre {
        min-height: 92px;
        overflow: auto;
        white-space: pre-wrap;
        color: var(--muted);
        margin: 0;
      }
      @media (max-width: 620px) {
        .status,
        .actions { grid-template-columns: 1fr; }
      }
    </style>
  </head>
  <body>
    <main>
      <h1>GADS iOS Stream Mode</h1>
      <div class="muted" id="udid">Loading device...</div>

      <section class="panel status">
        <div id="state">State: -</div>
        <div id="mode">Mode: -</div>
        <div id="settings">Settings: -</div>
        <div id="appium">Appium: -</div>
      </section>

      <section class="panel actions">
        <button class="primary" data-preset="broadcast">Broadcast Fast</button>
        <button data-preset="mjpeg-fast">MJPEG Fast</button>
        <button data-preset="mjpeg-balanced">MJPEG Balanced</button>
        <button data-preset="mjpeg-full">MJPEG Full</button>
      </section>

      <section class="panel">
        <pre id="log"></pre>
      </section>
    </main>

    <script>
      const parts = window.location.pathname.split("/");
      const udid = decodeURIComponent(parts[2] || "");
      const presets = {
        broadcast: {
          stream_type: "ios_webrtc_broadcast",
          target_fps: 30,
          jpeg_quality: 50,
          scaling_factor: 70,
        },
        "mjpeg-fast": {
          stream_type: "mjpeg",
          target_fps: 24,
          jpeg_quality: 40,
          scaling_factor: 50,
        },
        "mjpeg-balanced": {
          stream_type: "mjpeg",
          target_fps: 30,
          jpeg_quality: 60,
          scaling_factor: 75,
        },
        "mjpeg-full": {
          stream_type: "mjpeg",
          target_fps: 45,
          jpeg_quality: 60,
          scaling_factor: 100,
        },
      };

      const logEl = document.getElementById("log");
      document.getElementById("udid").textContent = udid || "Missing UDID";

      function log(message) {
        const line = "[" + new Date().toLocaleTimeString() + "] " + message;
        logEl.textContent = line + "\n" + logEl.textContent;
      }

      async function readJson(path, options) {
        const response = await fetch(path, options);
        const text = await response.text();
        let body;
        try {
          body = JSON.parse(text);
        } catch {
          body = { raw: text };
        }
        if (!response.ok) {
          throw new Error(body.message || body.error || text || response.statusText);
        }
        return body;
      }

      async function refresh() {
        const body = await readJson("/device/" + encodeURIComponent(udid) + "/info");
        const d = body.result || {};
        document.getElementById("state").textContent =
          "State: " + [d.provider_state, d.connected ? "connected" : "disconnected"].filter(Boolean).join(" / ");
        document.getElementById("mode").textContent = "Mode: " + (d.stream_type || "-");
        document.getElementById("settings").textContent =
          "Settings: " + [d.stream_target_fps + " fps", "JPEG " + d.stream_jpeg_quality, "scale " + d.stream_scaling_factor + "%"].join(" / ");
        document.getElementById("appium").textContent =
          "Appium: " + (d.is_appium_up ? "up" : "down") + " / session " + (d.has_appium_session ? "yes" : "no");
      }

      async function applyPreset(name) {
        const payload = presets[name];
        log("Applying " + name + ": " + JSON.stringify(payload));
        await readJson("/device/" + encodeURIComponent(udid) + "/update-stream-settings", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload),
        });
        log("Provider accepted settings. If stream_type changed, device reprovision starts now.");
        await refresh();
      }

      document.querySelectorAll("button[data-preset]").forEach((button) => {
        button.addEventListener("click", () => {
          applyPreset(button.dataset.preset).catch((error) => log("Failed: " + error.message));
        });
      });

      refresh().then(() => log("Ready")).catch((error) => log("Failed to load device: " + error.message));
      setInterval(() => refresh().catch(() => {}), 3000);
    </script>
  </body>
</html>`
