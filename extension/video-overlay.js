// AnySave floating "Download" button over HTML5 <video> elements (Phase 3).
// Renders inside a closed Shadow DOM so page styles never leak in or out.
(() => {
  if (window.__anysaveVideoOverlayLoaded) return;
  window.__anysaveVideoOverlayLoaded = true;

  // Skip TikTok Creative Center and Meta Ad Library where AnySave's popup batch-picker is dedicated.
  if (location.hostname === "ads.tiktok.com") return;
  if (/facebook\.com$/.test(location.hostname) && location.pathname.startsWith("/ads/library")) return;

  let hostEl = null;
  let btnEl = null;
  let activeVideo = null;
  let hideTimer = null;
  let rafId = null;

  function ensureOverlay() {
    if (hostEl && document.documentElement.contains(hostEl)) return;
    hostEl = document.createElement("div");
    hostEl.id = "__anysave-video-overlay-host";
    hostEl.style.cssText = "position:fixed;top:0;left:0;width:0;height:0;z-index:2147483646;pointer-events:none;";
    const shadow = hostEl.attachShadow({ mode: "closed" });
    const style = document.createElement("style");
    style.textContent = `
      .as-btn {
        position: fixed;
        display: inline-flex;
        align-items: center;
        gap: 6px;
        padding: 6px 11px;
        border-radius: 99px;
        border: 1px solid rgba(255,255,255,0.22);
        background: rgba(20, 20, 25, 0.85);
        backdrop-filter: blur(8px);
        -webkit-backdrop-filter: blur(8px);
        color: #fff;
        font: 600 12px/1 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        cursor: pointer;
        pointer-events: auto;
        box-shadow: 0 4px 14px rgba(0,0,0,0.35);
        transition: opacity 0.18s ease, transform 0.18s ease, background 0.18s ease;
        opacity: 0;
        transform: translateY(-2px);
        user-select: none;
      }
      .as-btn.visible {
        opacity: 1;
        transform: translateY(0);
      }
      .as-btn:hover {
        background: #e5484d;
        border-color: #ff6b70;
      }
      .as-btn.done {
        background: #2f9e5b;
        border-color: #46b973;
      }
      .as-dot {
        width: 7px;
        height: 7px;
        border-radius: 50%;
        background: #e5484d;
      }
      .as-btn:hover .as-dot, .as-btn.done .as-dot {
        background: #fff;
      }
    `;
    btnEl = document.createElement("button");
    btnEl.type = "button";
    btnEl.className = "as-btn";
    btnEl.innerHTML = `<span class="as-dot"></span><span class="as-label">⬇ AnySave</span>`;

    btnEl.addEventListener("mouseenter", () => {
      if (hideTimer) { clearTimeout(hideTimer); hideTimer = null; }
    });
    btnEl.addEventListener("mouseleave", () => {
      scheduleHide(800);
    });
    btnEl.addEventListener("click", (e) => {
      e.preventDefault();
      e.stopPropagation();
      if (!activeVideo) return;
      const label = btnEl.querySelector(".as-label");
      const src = activeVideo.currentSrc || activeVideo.src || (activeVideo.querySelector("source") || {}).src || "";
      const isDirectHttp = /^https?:\/\//i.test(src) && !/\.(m3u8|mpd)(\?|$)/i.test(src) &&
        !/(youtube\.com|youtu\.be|vimeo\.com|tiktok\.com|instagram\.com|facebook\.com)/i.test(location.hostname);
      const title = document.title || "Video";

      btnEl.classList.add("done");
      if (label) label.textContent = "Starting…";
      try {
        const msg = isDirectHttp
          ? { type: "directDownload", src, title }
          : { type: "addLink", url: location.href, title };
        chrome.runtime.sendMessage(msg, (res) => {
          if (label) label.textContent = res?.ok ? "Downloading ✓" : "Open popup ↗";
          setTimeout(() => {
            if (!btnEl) return;
            btnEl.classList.remove("done");
            if (label) label.textContent = "⬇ AnySave";
          }, 2800);
        });
      } catch {}
    });

    shadow.append(style, btnEl);
    (document.documentElement || document.body).appendChild(hostEl);
  }

  function updatePosition() {
    if (!activeVideo || !btnEl) return;
    const rect = activeVideo.getBoundingClientRect();
    if (rect.width < 220 || rect.height < 120 || rect.bottom < 40 || rect.top > window.innerHeight - 40) {
      btnEl.classList.remove("visible");
      return;
    }
    const top = Math.max(8, rect.top + 10);
    const left = Math.max(8, Math.min(window.innerWidth - 115, rect.right - 108));
    btnEl.style.top = `${Math.round(top)}px`;
    btnEl.style.left = `${Math.round(left)}px`;
  }

  function scheduleHide(ms = 1400) {
    if (hideTimer) clearTimeout(hideTimer);
    hideTimer = setTimeout(() => {
      if (btnEl) btnEl.classList.remove("visible");
      activeVideo = null;
    }, ms);
  }

  function showForVideo(v) {
    const rect = v.getBoundingClientRect();
    if (rect.width < 220 || rect.height < 120) return;
    ensureOverlay();
    activeVideo = v;
    updatePosition();
    btnEl.classList.add("visible");
    scheduleHide(2200);
  }

  document.addEventListener("pointermove", (e) => {
    if (rafId) return;
    rafId = requestAnimationFrame(() => {
      rafId = null;
      const el = e.target;
      if (!el || !(el instanceof Element)) return;
      const v = el.tagName === "VIDEO" ? el : el.closest?.("video");
      if (v) {
        showForVideo(v);
        return;
      }
      // Some sites place a transparent controls div over the <video>; check bounding boxes if needed.
      const videos = document.getElementsByTagName("video");
      for (let i = 0; i < videos.length && i < 12; i++) {
        const vid = videos[i];
        const r = vid.getBoundingClientRect();
        if (r.width >= 220 && r.height >= 120 &&
            e.clientX >= r.left && e.clientX <= r.right &&
            e.clientY >= r.top && e.clientY <= r.bottom) {
          showForVideo(vid);
          return;
        }
      }
    });
  }, { passive: true, capture: true });

  window.addEventListener("scroll", () => {
    if (activeVideo && btnEl?.classList.contains("visible")) updatePosition();
  }, { passive: true, capture: true });
})();

