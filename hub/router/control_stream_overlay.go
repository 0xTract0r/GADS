package router

import "bytes"

func injectHubControlStreamModeOverlay(indexBody []byte) []byte {
	if bytes.Contains(indexBody, []byte("gads-stream-mode-overlay.js")) {
		return indexBody
	}
	if !bytes.Contains(indexBody, []byte("</body>")) {
		return indexBody
	}
	return bytes.Replace(indexBody, []byte("</body>"), []byte(hubControlStreamModeOverlay+"</body>"), 1)
}

// hubControlStreamModeOverlay 注入到 SPA index.html 的 </body> 之前。
// 它在 /devices/control/:udid 页面下，把一个 inline 面板挂到 .back-button-bar 之后，
// 显示当前 stream 模式、轮询 /info、提供切换按钮并在 reprovision 期间给出实时反馈。
const hubControlStreamModeOverlay = `<script id="gads-stream-mode-overlay.js">
(function () {
  "use strict";

  var PANEL_ID = "gads-stream-mode-panel";
  var POLL_INFO_MS = 3000;
  var SWITCH_POLL_MS = 2000;
  var SWITCH_TIMEOUT_MS = 90000;
  var TOKEN_DEADLINE_MS = 30000;
  var startedAt = Date.now();
  var MJPEG_IMG_SELECTOR = "img#image-stream, img[src*='ios-stream-mjpeg'], img[src*='mjpeg-stream']";

  var BROADCAST_CANDIDATES = [
    "com.cory2btc.h264-broadcast-extension",
    "com.codeyee.gads.broadcast",
    "com.codeyee.gads.broadcast.host"
  ];

  var MODES = [
    {
      key: "broadcast-fast",
      label: "Broadcast Fast",
      payload: { stream_type: "ios_webrtc_broadcast", target_fps: 30, jpeg_quality: 50, scaling_factor: 70 },
      summary: "Broadcast (30 fps, q50, scale 70)"
    },
    {
      key: "mjpeg-fast",
      label: "MJPEG Fast",
      payload: { stream_type: "mjpeg", target_fps: 24, jpeg_quality: 40, scaling_factor: 50 },
      summary: "MJPEG (24 fps, q40, scale 50)"
    },
    {
      key: "mjpeg-full",
      label: "MJPEG Full",
      payload: { stream_type: "mjpeg", target_fps: 45, jpeg_quality: 60, scaling_factor: 100 },
      summary: "MJPEG (45 fps, q60, scale 100)"
    }
  ];

  function currentUdid() {
    var m = window.location.pathname.match(/^\/devices\/control\/([^/?#]+)/);
    return m ? decodeURIComponent(m[1]) : "";
  }

  function getAccessToken() {
    var explicit = ["accessToken", "token", "gadsAccessToken", "gads:accessToken"];
    for (var i = 0; i < explicit.length; i++) {
      var v = window.localStorage.getItem(explicit[i]);
      if (v) return v;
    }
    for (var idx = 0; idx < window.localStorage.length; idx++) {
      var key = window.localStorage.key(idx);
      if (!key || key.toLowerCase().indexOf("token") === -1) continue;
      var val = window.localStorage.getItem(key);
      if (val && val.split(".").length === 3) return val;
    }
    return "";
  }

  function css(node, styles) {
    Object.keys(styles).forEach(function (k) { node.style[k] = styles[k]; });
  }

  function hub(path, options) {
    var headers = { "Content-Type": "application/json" };
    var t = getAccessToken();
    if (t) headers.Authorization = "Bearer " + t;
    return fetch(path, Object.assign({ headers: headers }, options || {})).then(function (resp) {
      return resp.text().then(function (text) {
        var body = {};
        try { body = text ? JSON.parse(text) : {}; } catch (_) { body = { raw: text }; }
        if (!resp.ok) {
          var msg = body.message || body.error || body.raw || resp.statusText || ("HTTP " + resp.status);
          var err = new Error(msg);
          err.status = resp.status;
          err.body = body;
          throw err;
        }
        return body;
      });
    });
  }

  // Hub 的 device proxy 要求 device.Available=true，
  // 该字段只在 /available-devices SSE 的 tick 中刷新。
  // 所以 panel 走两条信息源：
  // 1) /admin/devices: 一次性拿 workspace_id 与初始 stream_type
  // 2) /available-devices?workspaceId=...&token=...: EventSource 长连接，
  //    既保活 Available=true，又能源源不断推送 stream_type/fps/quality/scaling/provider_state
  function listAdminDevices() {
    return hub("/admin/devices", { method: "GET" });
  }

  function extractFromAdminEntry(entry) {
    if (!entry) return {};
    return {
      workspace_id: entry.workspace_id || "",
      stream_type: entry.stream_type || "",
      target_fps: null,
      jpeg_quality: null,
      scaling_factor: null,
      provider_state: ""
    };
  }

  // 把 SSE 推过来的一条 LocalHubDevice JSON 归一化
  function extractFromSseEntry(entry) {
    if (!entry) return null;
    var info = entry.info || {};
    return {
      udid: info.udid || "",
      workspace_id: info.workspace_id || "",
      stream_type: info.stream_type || "",
      target_fps: info.stream_target_fps != null ? info.stream_target_fps : null,
      jpeg_quality: info.stream_jpeg_quality != null ? info.stream_jpeg_quality : null,
      scaling_factor: info.stream_scaling_factor != null ? info.stream_scaling_factor : null,
      provider_state: entry.provider_state || "",
      connected: !!entry.connected,
      available: !!entry.available
    };
  }

  function matchMode(info) {
    if (!info || !info.stream_type) return null;
    for (var i = 0; i < MODES.length; i++) {
      var m = MODES[i];
      var p = m.payload;
      if (info.stream_type !== p.stream_type) continue;
      if (Number(info.target_fps) === p.target_fps &&
          Number(info.jpeg_quality) === p.jpeg_quality &&
          Number(info.scaling_factor) === p.scaling_factor) {
        return m;
      }
    }
    // 仅 stream_type 相同的近似匹配
    for (var j = 0; j < MODES.length; j++) {
      if (MODES[j].payload.stream_type === info.stream_type) return MODES[j];
    }
    return null;
  }

  function describeInfo(info) {
    if (!info || !info.stream_type) return "Unknown";
    var parts = [];
    parts.push(info.stream_type);
    if (info.target_fps != null) parts.push(info.target_fps + " fps");
    if (info.jpeg_quality != null) parts.push("q" + info.jpeg_quality);
    if (info.scaling_factor != null) parts.push("scale " + info.scaling_factor);
    return parts.join(", ");
  }

  function findHostContainer() {
    // 控制页特征：URL 已经过 currentUdid 检查，这里只找稳定挂载点。
    // 优先 .back-button-bar 的父容器；其次 #stream-div 的祖先；再次 #root 的子 MuiBox-root。
    var backBar = document.querySelector(".back-button-bar");
    if (backBar && backBar.parentElement) return backBar.parentElement;
    var streamDiv = document.getElementById("stream-div");
    if (streamDiv) {
      var p = streamDiv;
      while (p && p !== document.body) {
        if (p.classList && p.classList.contains("MuiBox-root") && p.parentElement && p.parentElement.id === "root") return p;
        p = p.parentElement;
      }
      return streamDiv.parentElement;
    }
    return null;
  }

  function refreshMjpegImg() {
    var imgs = document.querySelectorAll(MJPEG_IMG_SELECTOR);
    for (var i = 0; i < imgs.length; i++) {
      var img = imgs[i];
      if (!img.src) continue;
      var base = img.src.replace(/[?&]_cb=\d+/, "");
      var sep = base.indexOf("?") === -1 ? "?" : "&";
      img.src = base + sep + "_cb=" + Date.now();
    }
  }

  function ControlPanel(udid) {
    var self = this;
    self.udid = udid;
    self.busy = false;
    self.lastInfo = null;
    self.buttons = {};

    var root = document.createElement("div");
    root.id = PANEL_ID;
    root.setAttribute("data-udid", udid);
    css(root, {
      margin: "12px 0 0 0",
      padding: "10px 12px",
      border: "1px solid rgba(0,0,0,0.12)",
      borderRadius: "8px",
      background: "#11161d",
      color: "#eef3f8",
      fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif",
      fontSize: "13px",
      boxShadow: "0 4px 14px rgba(0,0,0,0.18)"
    });

    var header = document.createElement("div");
    css(header, { display: "flex", alignItems: "center", justifyContent: "space-between", gap: "8px", marginBottom: "8px" });
    var title = document.createElement("div");
    title.textContent = "Stream Mode";
    css(title, { fontWeight: "700", fontSize: "14px" });
    var current = document.createElement("div");
    current.id = PANEL_ID + "-current";
    current.textContent = "Current: loading…";
    css(current, { color: "#a6b2bf", fontSize: "12px" });
    header.appendChild(title);
    header.appendChild(current);
    root.appendChild(header);

    var grid = document.createElement("div");
    css(grid, { display: "grid", gridTemplateColumns: "repeat(2, minmax(0, 1fr))", gap: "6px" });
    root.appendChild(grid);

    function makeBtn(label, key, onClick, isPrimary) {
      var b = document.createElement("button");
      b.type = "button";
      b.textContent = label;
      b.dataset.modeKey = key || "";
      css(b, {
        border: "1px solid rgba(255,255,255,0.12)",
        borderRadius: "6px",
        padding: "7px 8px",
        background: isPrimary ? "#22384b" : "#1c2530",
        color: "#eef3f8",
        fontWeight: "600",
        fontSize: "12px",
        cursor: "pointer",
        textAlign: "center"
      });
      b.addEventListener("click", onClick);
      grid.appendChild(b);
      return b;
    }

    MODES.forEach(function (mode) {
      self.buttons[mode.key] = makeBtn(mode.label, mode.key, function () { self.switchTo(mode); });
    });
    self.buttons["__broadcast_app"] = makeBtn("Open Broadcast App", "__broadcast_app", function () { self.openBroadcastApp(); });

    var status = document.createElement("div");
    status.id = PANEL_ID + "-status";
    css(status, { marginTop: "8px", fontSize: "12px", color: "#a6b2bf", minHeight: "16px", lineHeight: "1.35" });
    status.textContent = "";
    root.appendChild(status);

    var hint = document.createElement("div");
    css(hint, { marginTop: "6px", fontSize: "11px", color: "#6b7480", lineHeight: "1.3" });
    hint.textContent = "Broadcast requires iOS Screen Recording → Start Broadcast on the device.";
    root.appendChild(hint);

    self.el = root;
    self.statusEl = status;
    self.currentEl = current;
  }

  ControlPanel.prototype.setStatus = function (text) {
    if (this.statusEl) this.statusEl.textContent = text || "";
  };

  ControlPanel.prototype.setBusy = function (busy, reason) {
    this.busy = busy;
    Object.keys(this.buttons).forEach(function (k) {
      var b = this.buttons[k];
      if (b) b.disabled = !!busy;
    }.bind(this));
    if (busy) {
      this.setStatus(reason || "Working…");
    }
  };

  ControlPanel.prototype.renderCurrent = function (info) {
    this.lastInfo = info;
    if (!info || !info.stream_type) {
      this.currentEl.textContent = "Current: unknown";
      return;
    }
    var matched = matchMode(info);
    var preset = matched ? matched.label : "custom";
    this.currentEl.textContent = "Current: " + preset + " — " + describeInfo(info);
    // 高亮当前模式按钮
    var self = this;
    MODES.forEach(function (m) {
      var b = self.buttons[m.key];
      if (!b) return;
      var active = matched && matched.key === m.key;
      b.style.background = active ? "#0f5132" : "#1c2530";
      b.style.borderColor = active ? "#1f9d55" : "rgba(255,255,255,0.12)";
      b.style.fontWeight = active ? "800" : "600";
    });
  };

  // SSE 推的 entry 用于让 hub.AvailableDevicesSSE tick 把 device.Available=true，
  // 但 entry 本身的 stream_type 在 hub 内是 mongo 持久化字段，落后于 provider 运行时设置；
  // 同时 SSE 不带 fps/quality/scaling。所以这里只兜底首次显示（panel 尚未拿到 /info 时）。
  ControlPanel.prototype.handleSseEntry = function (entry) {
    if (!entry || entry.udid !== this.udid) return;
    if (!this.lastInfo || !this.lastInfo.stream_type) {
      this.renderCurrent(entry);
    }
  };

  ControlPanel.prototype.startStream = function (workspaceId) {
    var self = this;
    self.workspaceId = workspaceId || self.workspaceId;
    if (!self.workspaceId) return;
    self.stopStream();
    var token = getAccessToken();
    if (!token) return;
    var url = "/available-devices?workspaceId=" + encodeURIComponent(self.workspaceId) + "&token=" + encodeURIComponent(token);
    var es;
    try {
      es = new EventSource(url);
    } catch (e) {
      self.currentEl.textContent = "Current: (SSE error: " + e.message + ")";
      return;
    }
    self.es = es;
    es.onmessage = function (ev) {
      try {
        var list = JSON.parse(ev.data);
        if (!Array.isArray(list)) return;
        for (var i = 0; i < list.length; i++) {
          var entry = extractFromSseEntry(list[i]);
          if (entry && entry.udid === self.udid) {
            self.handleSseEntry(entry);
            return;
          }
        }
      } catch (_) { /* ignore */ }
    };
    es.onerror = function () {
      // EventSource 自带重连；只是更新 status
      if (!self.busy) self.setStatus("Live feed reconnecting…");
    };
  };

  ControlPanel.prototype.stopStream = function () {
    if (this.es) {
      try { this.es.close(); } catch (_) {}
      this.es = null;
    }
    this.stopInfoPolling();
  };

  ControlPanel.prototype.fetchInfo = function () {
    var self = this;
    if (self.busy) return Promise.resolve(null);
    return hub("/device/" + encodeURIComponent(self.udid) + "/info", { method: "GET" }).then(function (resp) {
      var src = resp;
      if (resp && resp.result) src = resp.result;
      if (resp && resp.device) src = resp.device;
      var entry = {
        udid: self.udid,
        stream_type: src.stream_type || src.streamType || "",
        target_fps: src.target_fps != null ? src.target_fps : (src.stream_target_fps != null ? src.stream_target_fps : null),
        jpeg_quality: src.jpeg_quality != null ? src.jpeg_quality : (src.stream_jpeg_quality != null ? src.stream_jpeg_quality : null),
        scaling_factor: src.scaling_factor != null ? src.scaling_factor : (src.stream_scaling_factor != null ? src.stream_scaling_factor : null),
        provider_state: src.provider_state || src.providerState || ""
      };
      if (entry.stream_type) self.renderCurrent(entry);
      return entry;
    }).catch(function (err) {
      if (!self.lastInfo || !self.lastInfo.stream_type) {
        self.currentEl.textContent = "Current: (info error: " + (err.message || err) + ")";
      }
      return null;
    });
  };

  ControlPanel.prototype.startInfoPolling = function () {
    var self = this;
    if (self.infoTimer) clearInterval(self.infoTimer);
    self.fetchInfo();
    self.infoTimer = setInterval(function () { self.fetchInfo(); }, POLL_INFO_MS);
  };

  ControlPanel.prototype.stopInfoPolling = function () {
    if (this.infoTimer) {
      clearInterval(this.infoTimer);
      this.infoTimer = null;
    }
  };

  ControlPanel.prototype.bootstrap = function () {
    var self = this;
    return listAdminDevices().then(function (resp) {
      var devices = (resp && resp.result && resp.result.devices) || [];
      var match = null;
      for (var i = 0; i < devices.length; i++) {
        if (devices[i].udid === self.udid) { match = devices[i]; break; }
      }
      if (match) {
        var initial = extractFromAdminEntry(match);
        self.workspaceId = initial.workspace_id;
        // 仅给一个粗略初始显示，正式信息源是 SSE 让 Available=true 后由 fetchInfo 拿
        self.renderCurrent({ stream_type: initial.stream_type });
      }
      // SSE 长连接保活 Available；再起 /info 短轮询拿权威字段
      self.startStream(self.workspaceId);
      // 给 SSE 一点点时间把 Available=true，然后再开始拉 /info
      setTimeout(function () { self.startInfoPolling(); }, 1200);
    }).catch(function (err) {
      self.currentEl.textContent = "Current: (admin/devices error: " + (err.message || err) + ")";
    });
  };

  // 切换期间 SSE 推送的字段不一定立刻反映新模式（hub 缓存），
  // 因此 waitForLive 直接短轮询 hub proxy /info（device 已经 Available=true，由 SSE 长连接保活）。
  ControlPanel.prototype.waitForLive = function (targetStreamType, deadline) {
    var self = this;
    var startedAt = Date.now();
    function step() {
      if (Date.now() > deadline) {
        return Promise.reject(new Error("timeout waiting for provider live"));
      }
      return hub("/device/" + encodeURIComponent(self.udid) + "/info", { method: "GET" }).then(function (resp) {
        var src = resp;
        if (resp && resp.result) src = resp.result;
        if (resp && resp.device) src = resp.device;
        var streamType = src.stream_type || src.streamType || "";
        var providerState = src.provider_state || src.providerState || src.connected_provider || "";
        var fps = src.target_fps || src.stream_target_fps || null;
        var quality = src.jpeg_quality || src.stream_jpeg_quality || null;
        var scaling = src.scaling_factor || src.stream_scaling_factor || null;
        var entry = {
          udid: self.udid,
          stream_type: streamType,
          target_fps: fps,
          jpeg_quality: quality,
          scaling_factor: scaling,
          provider_state: providerState
        };
        var providerLive = providerState === "live" || providerState === "Live" || providerState === "LIVE";
        var match = !targetStreamType || streamType === targetStreamType;
        if (providerLive && match) {
          self.renderCurrent(entry);
          return entry;
        }
        var waited = Math.round((Date.now() - startedAt) / 1000);
        var label = providerLive ? "Live but stream still " + (streamType || "?") + " (" + waited + "s)" : "Reprovisioning… (" + waited + "s)";
        self.setStatus(label);
        return new Promise(function (r) { setTimeout(r, SWITCH_POLL_MS); }).then(step);
      }).catch(function (err) {
        var waited = Math.round((Date.now() - startedAt) / 1000);
        self.setStatus("Reprovisioning… (" + waited + "s, " + (err.message || err) + ")");
        return new Promise(function (r) { setTimeout(r, SWITCH_POLL_MS); }).then(step);
      });
    }
    return step();
  };

  ControlPanel.prototype.switchTo = function (mode) {
    if (this.busy) return;
    var self = this;
    var udid = self.udid;
    var current = self.lastInfo || {};
    // 同 stream_type 切换不会触发 provider reset，只是 fps/quality/scale 热更新；
    // 但仍等待至少一次 SSE confirm
    var sameType = current.stream_type === mode.payload.stream_type;
    self.setBusy(true, "Switching to " + mode.label + "…");
    hub("/device/" + encodeURIComponent(udid) + "/update-stream-settings", {
      method: "POST",
      body: JSON.stringify(mode.payload)
    }).then(function () {
      var deadline = Date.now() + SWITCH_TIMEOUT_MS;
      // 同 stream_type 一般是热更新（只调 fps/quality/scale），不会触发 provider reset；
      // 但仍轮询一次 /info 确认实际值并刷新流。
      return self.waitForLive(mode.payload.stream_type, deadline).then(function (info) {
        self.setStatus("Live ✓ (" + describeInfo(info) + ")");
        // 强制刷新 MJPEG img；WebRTC 由 SPA 自身重连
        if (mode.payload.stream_type === "mjpeg") {
          setTimeout(refreshMjpegImg, 200);
          setTimeout(refreshMjpegImg, 1500);
        }
      });
    }).catch(function (err) {
      var msg = err && err.message ? err.message : String(err);
      if (msg.indexOf("timeout") !== -1) {
        self.setStatus("Switch timed out after 90s — try again or reload the page.");
      } else {
        self.setStatus("Switch failed: " + msg);
      }
    }).then(function () {
      self.setBusy(false);
      // 立刻刷新一次权威信息
      self.fetchInfo();
      void sameType;
    });
  };

  ControlPanel.prototype.openBroadcastApp = function () {
    if (this.busy) return;
    var self = this;
    self.setBusy(true, "Trying to open Broadcast host app…");
    var chain = Promise.reject(new Error("no candidate tried"));
    BROADCAST_CANDIDATES.forEach(function (bundleId) {
      chain = chain.catch(function () {
        return hub("/device/" + encodeURIComponent(self.udid) + "/launchApp", {
          method: "POST",
          body: JSON.stringify({ app: bundleId })
        }).then(function () {
          self.setStatus("Opened " + bundleId + ". On the iPhone confirm Start Broadcast.");
        });
      });
    });
    chain.catch(function () {
      self.setStatus("Could not auto-open Broadcast host app. Use iOS Screen Recording picker manually.");
    }).then(function () { self.setBusy(false); });
  };

  // ---------- mount / observer ----------

  var activePanel = null;
  var lastUdid = "";

  function ensureMounted() {
    var udid = currentUdid();
    if (!udid) {
      // 不在控制页：拆除
      if (activePanel) {
        activePanel.stopStream();
        if (activePanel.el && activePanel.el.parentNode) activePanel.el.parentNode.removeChild(activePanel.el);
        activePanel = null;
        lastUdid = "";
      }
      return;
    }
    // 设备变了：重建
    if (activePanel && activePanel.udid !== udid) {
      activePanel.stopStream();
      if (activePanel.el && activePanel.el.parentNode) activePanel.el.parentNode.removeChild(activePanel.el);
      activePanel = null;
    }

    // 等待 SPA 渲染好控制页和登录 token
    if (!getAccessToken()) {
      if (Date.now() - startedAt < TOKEN_DEADLINE_MS) return;
      // 超过 deadline 还没 token 就放弃这一轮
      return;
    }
    var host = findHostContainer();
    if (!host) return;

    var existing = document.getElementById(PANEL_ID);
    if (existing && existing.getAttribute("data-udid") === udid && activePanel && activePanel.el === existing) {
      // 还在 DOM 里：什么都不做
      return;
    }
    if (existing && existing.parentNode) existing.parentNode.removeChild(existing);

    var panel = new ControlPanel(udid);

    // 挂在 .back-button-bar 之后；没有时挂到 host 顶端
    var backBar = host.querySelector(".back-button-bar");
    if (backBar && backBar.parentNode === host) {
      backBar.insertAdjacentElement("afterend", panel.el);
    } else {
      host.insertBefore(panel.el, host.firstChild);
    }
    activePanel = panel;
    lastUdid = udid;
    panel.bootstrap();
  }

  // 监听 DOM 变化和 history navigation
  var mo = new MutationObserver(function () { ensureMounted(); });
  function startObserver() {
    if (document.body) {
      mo.observe(document.body, { childList: true, subtree: true });
    } else {
      setTimeout(startObserver, 200);
    }
  }
  startObserver();

  // history.pushState / popstate 都触发一次
  var origPush = history.pushState;
  history.pushState = function () { var r = origPush.apply(this, arguments); setTimeout(ensureMounted, 50); return r; };
  var origReplace = history.replaceState;
  history.replaceState = function () { var r = origReplace.apply(this, arguments); setTimeout(ensureMounted, 50); return r; };
  window.addEventListener("popstate", function () { setTimeout(ensureMounted, 50); });

  // 兜底：每 2 秒检查一次（防止 SPA 用非 history API 切路由）
  setInterval(ensureMounted, 2000);
  setTimeout(ensureMounted, 800);
})();
</script>`
