package router

import "bytes"

func injectHubControlClipboardFreezeOverlay(indexBody []byte) []byte {
	if bytes.Contains(indexBody, []byte("gads-clipboard-freeze-overlay.js")) {
		return indexBody
	}
	if !bytes.Contains(indexBody, []byte("</body>")) {
		return indexBody
	}
	return bytes.Replace(indexBody, []byte("</body>"), []byte(hubControlClipboardFreezeOverlay+"</body>"), 1)
}

const hubControlClipboardFreezeOverlay = `<style id="gads-clipboard-freeze-overlay-style">
.gads-clipboard-freeze-layer {
  position: absolute;
  pointer-events: none;
  z-index: 2147483645;
  overflow: hidden;
  background: #20262d;
}
.gads-clipboard-freeze-layer img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: fill;
}
.gads-clipboard-freeze-spinner {
  position: absolute;
  right: 10px;
  top: 10px;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  border: 2px solid rgba(255,255,255,0.32);
  border-top-color: rgba(255,255,255,0.92);
  animation: gads-clipboard-freeze-spin 780ms linear infinite;
  box-shadow: 0 1px 5px rgba(0,0,0,0.25);
}
@keyframes gads-clipboard-freeze-spin {
  to { transform: rotate(360deg); }
}
</style>
<script id="gads-clipboard-freeze-overlay.js">
(function () {
  "use strict";

  if (!/^\/devices\/control\//.test(window.location.pathname)) return;

  var ATTACHED_FLAG = "__gadsClipboardFreezeAttached";
  var LAYER_CLASS = "gads-clipboard-freeze-layer";
  var SPINNER_CLASS = "gads-clipboard-freeze-spinner";
  var inFlight = 0;
  var activeLayer = null;
  var activeImage = null;
  var activeTarget = null;
  var lastSnapshot = "";
  var sampleTimer = null;

  function isClipboardURL(input) {
    var raw = "";
    if (typeof input === "string") raw = input;
    else if (input && typeof input.url === "string") raw = input.url;
    else if (input && typeof input.toString === "function") raw = input.toString();
    if (!raw) return false;
    try {
      var url = new URL(raw, window.location.href);
      return /^\/device\/[^/]+\/getClipboard\/?$/.test(url.pathname);
    } catch (_) {
      return /\/device\/[^/]+\/getClipboard(?:[?#].*)?$/.test(raw);
    }
  }

  function pickLargestStreamElement() {
    var nodes = document.querySelectorAll("canvas, video, img#image-stream, img[src*='ios-stream-mjpeg'], img[src*='mjpeg-stream'], img");
    var target = null;
    var bestArea = 0;
    var bestPriority = -1;
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      var rect = el.getBoundingClientRect();
      if (rect.width < 200 || rect.height < 200) continue;
      var area = rect.width * rect.height;
      var src = el.currentSrc || el.src || "";
      var priority = 0;
      if (el.id === "image-stream" || src.indexOf("ios-stream-mjpeg") !== -1 || src.indexOf("mjpeg-stream") !== -1) priority = 3;
      else if (el.tagName === "VIDEO") priority = 2;
      else if (el.tagName === "CANVAS") priority = 1;
      if (area > bestArea || (Math.abs(area - bestArea) < 1 && priority > bestPriority)) {
        target = el;
        bestArea = area;
        bestPriority = priority;
      }
    }
    return target;
  }

  function syncLayer(layer, target) {
    if (!layer || !target || !target.parentElement) return;
    var targetRect = target.getBoundingClientRect();
    var parentRect = target.parentElement.getBoundingClientRect();
    layer.style.left = (targetRect.left - parentRect.left) + "px";
    layer.style.top = (targetRect.top - parentRect.top) + "px";
    layer.style.width = targetRect.width + "px";
    layer.style.height = targetRect.height + "px";
  }

  function captureSnapshot(target) {
    if (!target) return "";
    var rect = target.getBoundingClientRect();
    if (rect.width < 1 || rect.height < 1) return "";
    try {
      var canvas = document.createElement("canvas");
      canvas.width = Math.max(1, Math.round(rect.width));
      canvas.height = Math.max(1, Math.round(rect.height));
      var ctx = canvas.getContext("2d");
      if (!ctx) return "";
      ctx.drawImage(target, 0, 0, canvas.width, canvas.height);
      return canvas.toDataURL("image/jpeg", 0.82);
    } catch (_) {
      return "";
    }
  }

  function refreshLastSnapshot() {
    var target = pickLargestStreamElement();
    var snapshot = captureSnapshot(target);
    if (snapshot) lastSnapshot = snapshot;
  }

  function ensureLayer(target) {
    if (!target || !target.parentElement) return null;
    var parent = target.parentElement;
    var parentStyle = window.getComputedStyle(parent);
    if (parentStyle.position === "static") parent.style.position = "relative";

    var layer = parent.querySelector(":scope > ." + LAYER_CLASS);
    if (!layer) {
      layer = document.createElement("div");
      layer.className = LAYER_CLASS;
      layer.setAttribute("aria-hidden", "true");
      var img = document.createElement("img");
      img.alt = "";
      var spinner = document.createElement("div");
      spinner.className = SPINNER_CLASS;
      layer.appendChild(img);
      layer.appendChild(spinner);
      parent.appendChild(layer);
    }
    syncLayer(layer, target);
    return layer;
  }

  function showFreeze() {
    var target = pickLargestStreamElement();
    if (!target) return;
    activeTarget = target;
    var snapshot = captureSnapshot(target) || lastSnapshot;
    var layer = ensureLayer(target);
    if (!layer) return;
    activeLayer = layer;
    activeImage = layer.querySelector("img");
    if (activeImage && snapshot) activeImage.src = snapshot;
    if (activeImage) activeImage.style.display = snapshot ? "block" : "none";
    layer.style.display = "block";
    syncLayer(layer, target);
  }

  function hideFreeze() {
    if (!activeLayer) return;
    activeLayer.style.display = "none";
    activeLayer = null;
    activeImage = null;
    activeTarget = null;
    refreshLastSnapshot();
  }

  function beginClipboardRequest() {
    inFlight += 1;
    showFreeze();
  }

  function endClipboardRequest() {
    inFlight = Math.max(0, inFlight - 1);
    if (inFlight === 0) window.setTimeout(hideFreeze, 220);
  }

  function patchFetch() {
    if (!window.fetch || window.fetch[ATTACHED_FLAG]) return;
    var originalFetch = window.fetch;
    var patchedFetch = function () {
      var shouldFreeze = isClipboardURL(arguments[0]);
      if (!shouldFreeze) return originalFetch.apply(this, arguments);
      beginClipboardRequest();
      try {
        return originalFetch.apply(this, arguments).then(function (resp) {
          endClipboardRequest();
          return resp;
        }, function (err) {
          endClipboardRequest();
          throw err;
        });
      } catch (err) {
        endClipboardRequest();
        throw err;
      }
    };
    patchedFetch[ATTACHED_FLAG] = true;
    window.fetch = patchedFetch;
  }

  function patchXHR() {
    if (!window.XMLHttpRequest || window.XMLHttpRequest.prototype[ATTACHED_FLAG]) return;
    var proto = window.XMLHttpRequest.prototype;
    var originalOpen = proto.open;
    var originalSend = proto.send;
    proto.open = function (method, url) {
      this.__gadsClipboardFreeze = isClipboardURL(url);
      return originalOpen.apply(this, arguments);
    };
    proto.send = function () {
      if (this.__gadsClipboardFreeze) {
        beginClipboardRequest();
        this.addEventListener("loadend", endClipboardRequest, { once: true });
      }
      try {
        return originalSend.apply(this, arguments);
      } catch (err) {
        if (this.__gadsClipboardFreeze) endClipboardRequest();
        throw err;
      }
    };
    proto[ATTACHED_FLAG] = true;
  }

  function startSampler() {
    if (sampleTimer) return;
    var tick = function () {
      if (inFlight === 0) refreshLastSnapshot();
      if (activeLayer && activeTarget) syncLayer(activeLayer, activeTarget);
    };
    tick();
    sampleTimer = window.setInterval(tick, 800);
    window.addEventListener("resize", tick, { passive: true });
  }

  patchFetch();
  patchXHR();
  startSampler();
})();
</script>`
