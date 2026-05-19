/*
 * This file is part of GADS.
 *
 * Copyright (c) 2022-2025 Nikola Shabanov
 *
 * This source code is licensed under the GNU Affero General Public License v3.0.
 * You may obtain a copy of the license at https://www.gnu.org/licenses/agpl-3.0.html
 */

package router

import "bytes"

// injectHubControlTapFeedbackOverlay adds an optimistic, client-side tap ripple
// effect on top of the control-page stream element (canvas / video / img — the
// hub UI varies by stream type). The ripple is rendered immediately
// on mousedown/touchstart so that the perceived latency of an iOS XR tap drops
// from ~410ms (the iOS 18 XCTest IPC floor) to ~0ms. The injected script is a
// purely visual overlay — it does NOT intercept or modify the existing React
// onMouseDown / onMouseUp handlers, so the original `/tap` HTTP request keeps
// firing exactly as before.
func injectHubControlTapFeedbackOverlay(indexBody []byte) []byte {
	if bytes.Contains(indexBody, []byte("gads-tap-feedback-overlay.js")) {
		return indexBody
	}
	if !bytes.Contains(indexBody, []byte("</body>")) {
		return indexBody
	}
	return bytes.Replace(indexBody, []byte("</body>"), []byte(hubControlTapFeedbackOverlay+"</body>"), 1)
}

const hubControlTapFeedbackOverlay = `<style id="gads-tap-feedback-overlay-style">
@keyframes gads-tap-ripple-anim {
  0%   { transform: translate(-50%, -50%) scale(0.4); opacity: 0.85; }
  60%  { opacity: 0.55; }
  100% { transform: translate(-50%, -50%) scale(1.8); opacity: 0; }
}
.gads-tap-ripple {
  position: absolute;
  width: 28px;
  height: 28px;
  margin: 0;
  border-radius: 50%;
  background: rgba(120, 200, 255, 0.65);
  box-shadow: 0 0 12px rgba(120, 200, 255, 0.55);
  pointer-events: none;
  will-change: transform, opacity;
  animation: gads-tap-ripple-anim 240ms ease-out forwards;
  transform: translate(-50%, -50%) scale(0.4);
}
.gads-tap-ripple-layer {
  position: absolute;
  inset: 0;
  pointer-events: none;
  z-index: 2147483646;
  overflow: hidden;
}
</style>
<script id="gads-tap-feedback-overlay.js">
(function () {
  "use strict";

  // Only run on the device control page.
  if (!/^\/devices\/control\//.test(window.location.pathname)) return;

  var ATTACHED_FLAG = "__gadsTapFeedbackAttached";
  var LAYER_CLASS = "gads-tap-ripple-layer";
  var RIPPLE_CLASS = "gads-tap-ripple";
  var startedAt = Date.now();
  var deadline = startedAt + 60000; // give SPA up to 60s to render canvas
  var pollTimer = null;

  function ensureLayer(canvas) {
    var parent = canvas.parentElement;
    if (!parent) return null;
    // The control-page canvas is absolutely positioned over the video <img>;
    // make sure the parent establishes a positioning context so our overlay
    // matches the canvas box exactly.
    var parentStyle = window.getComputedStyle(parent);
    if (parentStyle.position === "static") {
      parent.style.position = "relative";
    }
    var existing = parent.querySelector(":scope > ." + LAYER_CLASS);
    if (existing) return existing;
    var layer = document.createElement("div");
    layer.className = LAYER_CLASS;
    // Size to canvas, not parent (canvas may be inset/letterboxed).
    syncLayerToCanvas(layer, canvas);
    parent.appendChild(layer);
    return layer;
  }

  function syncLayerToCanvas(layer, canvas) {
    var parent = canvas.parentElement;
    if (!parent) return;
    var canvasRect = canvas.getBoundingClientRect();
    var parentRect = parent.getBoundingClientRect();
    layer.style.left = (canvasRect.left - parentRect.left) + "px";
    layer.style.top = (canvasRect.top - parentRect.top) + "px";
    layer.style.width = canvasRect.width + "px";
    layer.style.height = canvasRect.height + "px";
  }

  function spawnRipple(layer, x, y) {
    var ripple = document.createElement("div");
    ripple.className = RIPPLE_CLASS;
    ripple.style.left = x + "px";
    ripple.style.top = y + "px";
    layer.appendChild(ripple);
    // Force a frame so the initial transform is committed before the
    // animation runs, even if React schedules a heavy reflow this tick.
    if (typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(function () { /* no-op, just commit */ });
    }
    var cleanup = function () {
      if (ripple.parentNode) ripple.parentNode.removeChild(ripple);
    };
    ripple.addEventListener("animationend", cleanup, { once: true });
    // Hard fallback in case animationend never fires (e.g. tab hidden).
    setTimeout(cleanup, 600);
  }

  function rippleFromEvent(layer, canvas, clientX, clientY) {
    var rect = canvas.getBoundingClientRect();
    var x = clientX - rect.left;
    var y = clientY - rect.top;
    if (x < 0 || y < 0 || x > rect.width || y > rect.height) return;
    spawnRipple(layer, x, y);
  }

  function attach(canvas) {
    if (canvas[ATTACHED_FLAG]) return;
    var layer = ensureLayer(canvas);
    if (!layer) return;
    canvas[ATTACHED_FLAG] = true;

    var onMouseDown = function (event) {
      try {
        syncLayerToCanvas(layer, canvas);
        rippleFromEvent(layer, canvas, event.clientX, event.clientY);
      } catch (_) { /* never break React's listener */ }
    };
    var onTouchStart = function (event) {
      try {
        syncLayerToCanvas(layer, canvas);
        var touches = event.changedTouches || event.touches;
        if (!touches) return;
        for (var i = 0; i < touches.length; i++) {
          rippleFromEvent(layer, canvas, touches[i].clientX, touches[i].clientY);
        }
      } catch (_) { /* never break React's listener */ }
    };

    // capture=true so we run BEFORE React's synthetic-event handlers.
    // passive=true so we never block scrolling/touch behaviour.
    canvas.addEventListener("mousedown", onMouseDown, { capture: true, passive: true });
    canvas.addEventListener("touchstart", onTouchStart, { capture: true, passive: true });

    // Keep the layer geometry in sync if the canvas resizes/relocates
    // (e.g. window resize, orientation change, video stream resize).
    if (typeof window.ResizeObserver === "function") {
      try {
        var ro = new window.ResizeObserver(function () { syncLayerToCanvas(layer, canvas); });
        ro.observe(canvas);
      } catch (_) { /* ignore */ }
    }
    window.addEventListener("resize", function () { syncLayerToCanvas(layer, canvas); }, { passive: true });
  }

  function pickLargestStreamElement() {
    // Hub control page may render the stream as <canvas> (WebRTC), <video>
    // (broadcast), or <img> (MJPEG). Pick the largest one above a size floor.
    var nodes = document.querySelectorAll("canvas, video, img");
    var target = null;
    var bestArea = 0;
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      var rect = el.getBoundingClientRect();
      if (rect.width < 200 || rect.height < 200) continue;
      var area = rect.width * rect.height;
      if (area > bestArea) {
        target = el;
        bestArea = area;
      }
    }
    return target;
  }

  function tryAttach() {
    var target = pickLargestStreamElement();
    if (target) {
      attach(target);
      return true;
    }
    return false;
  }

  function startPolling() {
    if (tryAttach()) return;
    pollTimer = window.setInterval(function () {
      if (tryAttach() || Date.now() > deadline) {
        if (pollTimer) {
          window.clearInterval(pollTimer);
          pollTimer = null;
        }
      }
    }, 200);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", startPolling, { once: true });
  } else {
    startPolling();
  }

  // Also watch for SPA route changes (re-mounting the canvas) without
  // needing to hook into React internals.
  if (typeof window.MutationObserver === "function") {
    try {
      var mo = new window.MutationObserver(function () {
        var target = pickLargestStreamElement();
        if (target && !target[ATTACHED_FLAG]) attach(target);
      });
      mo.observe(document.body, { childList: true, subtree: true });
    } catch (_) { /* ignore */ }
  }
})();
</script>`
