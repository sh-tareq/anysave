// AnySave button over videos on web pages: the AnySave icon (its arrow gently moving) in the
// video's corner. Click it to pick a quality. Works on any site:
//  - YouTube, Facebook, Instagram, TikTok and other video sites: the helper (yt-dlp) lists the
//    qualities of the video's own page, found from the post or thumbnail the video sits in.
//  - Meta Ad Library, TikTok Creative Center and pages playing a plain video file: that file.
// Everything lives in a closed Shadow DOM so the page's styles never leak in or out.
(() => {
  if (window.__anysaveVideoOverlayLoaded) return;
  window.__anysaveVideoOverlayLoaded = true;

  const MIN_W = 200, MIN_H = 110;
  const host = location.hostname.replace(/^www\.|^m\./, "");
  const onSite = (d) => host === d || host.endsWith("." + d);
  const isAdLibrary = (onSite("facebook.com") && location.pathname.startsWith("/ads/library")) ||
    host === "ads.tiktok.com";

  let hostEl, root, btnEl, menuEl;
  let activeVideo = null, menuVideo = null;
  let hideTimer = null, rafId = null;
  const infoCache = new Map(); // page url -> Promise<info result>

  const ICON = `
    <svg viewBox="0 0 32 32" width="30" height="30" aria-hidden="true">
      <defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stop-color="#ff6a70"/><stop offset="1" stop-color="#d92332"/></linearGradient></defs>
      <rect x="1" y="1" width="30" height="30" rx="8" fill="url(#g)"/>
      <g class="arrow" fill="none" stroke="#fff" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
        <path d="M16 7v11"/><path d="M11 13.5l5 5 5-5"/></g>
      <path d="M10 24h12" stroke="#fff" stroke-width="3" stroke-linecap="round"/>
      <path class="tick" d="M10 16.5l4 4 8-9" fill="none" stroke="#fff" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round"/>
    </svg>`;

  const CSS = `
    :host { all: initial; }
    .btn {
      position: fixed; width: 38px; height: 38px; padding: 4px; border: 0; border-radius: 12px;
      background: rgba(18,18,22,.55); backdrop-filter: blur(6px); -webkit-backdrop-filter: blur(6px);
      box-shadow: 0 4px 14px rgba(0,0,0,.35); cursor: pointer; pointer-events: auto;
      opacity: 0; transform: translateY(-3px) scale(.92); transition: opacity .18s, transform .18s, background .18s;
    }
    .btn.visible { opacity: 1; transform: none; }
    .btn:hover, .btn.open { background: rgba(18,18,22,.8); transform: scale(1.06); }
    .btn svg { display: block; }
    .arrow { animation: drop 1.6s ease-in-out infinite; }
    .btn:hover .arrow, .btn.busy .arrow { animation-duration: .7s; }
    @keyframes drop { 0%,100% { transform: translateY(-1.5px); } 50% { transform: translateY(2px); } }
    .tick { display: none; }
    .btn.done .arrow { display: none; }
    .btn.done .tick { display: inline; stroke-dasharray: 22; stroke-dashoffset: 22; animation: tick .35s ease-out forwards; }
    @keyframes tick { to { stroke-dashoffset: 0; } }
    @media (prefers-reduced-motion: reduce) { .arrow { animation: none; } }

    .menu {
      position: fixed; width: 248px; max-height: 340px; overflow: auto; pointer-events: auto;
      background: #1c1c21; color: #f2f2f4; border: 1px solid rgba(255,255,255,.12); border-radius: 12px;
      box-shadow: 0 10px 30px rgba(0,0,0,.45); padding: 6px;
      font: 13px/1.3 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    .menu[hidden] { display: none; }
    .head { padding: 6px 8px 8px; font-weight: 600; font-size: 12px; color: #a9a9b3;
      white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
    .item { display: flex; align-items: center; gap: 8px; width: 100%; box-sizing: border-box; text-align: left;
      padding: 8px 10px; border: 0; border-radius: 8px; background: none; color: inherit; font: inherit; cursor: pointer; }
    .item:hover, .item:focus-visible { background: #e5484d; color: #fff; outline: none; }
    .item .sub { margin-left: auto; font-size: 11px; opacity: .65; }
    .note { padding: 8px 10px; font-size: 12px; color: #a9a9b3; display: flex; align-items: center; gap: 8px; }
    .note.err { color: #ff8a8e; }
    .spin { width: 12px; height: 12px; border: 2px solid #555; border-top-color: #e5484d; border-radius: 50%;
      animation: spin .8s linear infinite; flex: none; }
    @keyframes spin { to { transform: rotate(360deg); } }
    hr { border: 0; border-top: 1px solid rgba(255,255,255,.1); margin: 4px 2px; }
  `;

  function ensureUI() {
    if (hostEl && document.documentElement.contains(hostEl)) return;
    hostEl = document.createElement("div");
    hostEl.id = "__anysave-video-overlay-host";
    hostEl.style.cssText = "position:fixed;top:0;left:0;width:0;height:0;z-index:2147483646;pointer-events:none;";
    root = hostEl.attachShadow({ mode: "closed" });
    const style = document.createElement("style");
    style.textContent = CSS;
    btnEl = document.createElement("button");
    btnEl.type = "button";
    btnEl.className = "btn";
    btnEl.title = "Download with AnySave";
    btnEl.setAttribute("aria-label", "Download with AnySave");
    btnEl.innerHTML = ICON;
    menuEl = document.createElement("div");
    menuEl.className = "menu";
    menuEl.hidden = true;
    menuEl.setAttribute("role", "menu");

    btnEl.addEventListener("mouseenter", () => clearTimeout(hideTimer));
    btnEl.addEventListener("mouseleave", () => { if (menuEl.hidden) scheduleHide(800); });
    btnEl.addEventListener("click", (e) => {
      e.preventDefault(); e.stopPropagation();
      if (!menuEl.hidden) closeMenu(); else if (activeVideo) openMenu(activeVideo);
    });
    for (const ev of ["pointerdown", "mousedown", "mouseup", "click", "dblclick"]) {
      menuEl.addEventListener(ev, (e) => e.stopPropagation()); // keep the page's player from reacting
    }
    root.append(style, btnEl, menuEl);
    (document.documentElement || document.body).appendChild(hostEl);
  }

  // ---------- which video is this? ----------

  function directSrc(v) {
    const src = v.currentSrc || v.src || v.querySelector("source")?.src || "";
    return /^https?:\/\//i.test(src) && !/\.(m3u8|mpd)(\?|$)/i.test(src) ? src : "";
  }

  // The nearest link (in the video's post, card or thumbnail) that matches `re`.
  function nearbyLink(v, re) {
    let el = v;
    for (let i = 0; i < 14 && el; i++, el = el.parentElement) {
      if (el.tagName === "A" && re.test(el.href)) return el.href;
      const links = el.querySelectorAll?.("a[href]") || [];
      for (const a of links) if (re.test(a.href)) return a.href;
      if (el.tagName === "ARTICLE" || el.getAttribute?.("role") === "article") break; // one post only
    }
    return "";
  }

  // A readable name for a video without a page of its own (ad libraries): the first short line
  // of text in its card, e.g. the advertiser.
  function nearbyName(v) {
    // Meta: the advertiser's name is the line just above "Sponsored".
    let el = v;
    for (let i = 0; i < 12 && el.parentElement; i++) {
      el = el.parentElement;
      const all = (el.innerText || "").split("\n").map((s) => s.trim()).filter(Boolean);
      const k = all.findIndex((s) => /^sponsored$/i.test(s));
      if (k > 0 && all[k - 1].length < 70) return all[k - 1];
    }
    el = v;
    for (let i = 0; i < 10 && el.parentElement; i++) {
      el = el.parentElement;
      const lines = (el.innerText || "").split("\n").map((s) => s.trim())
        .filter((s) => s.length > 2 && s.length < 70 && !/^(sponsored|active|library id|see ad details|\d)/i.test(s));
      if (lines.length) return lines[0];
    }
    return "";
  }

  // What to download: { page } for the helper to read, and/or { file } for a plain video file.
  function targetOf(v) {
    const file = directSrc(v);
    const path = location.pathname;
    if (isAdLibrary) {
      const name = nearbyName(v);
      return { file, title: name ? `${name} ad` : (onSite("tiktok.com") ? "TikTok ad" : "Meta ad") };
    }
    let page = "";
    if (onSite("youtube.com")) {
      const main = v.closest("#movie_player, ytd-reel-video-renderer, #shorts-player");
      page = main && /^\/(watch|shorts\/)/.test(path) ? location.href : nearbyLink(v, /\/(watch\?v=|shorts\/)/);
    } else if (onSite("facebook.com")) {
      const re = /\/(videos|reel|reels|watch)\b|\/watch\/?\?v=|\/share\/(v|r)\//;
      page = re.test(path + location.search) ? location.href : nearbyLink(v, re);
    } else if (onSite("instagram.com")) {
      const re = /\/(p|reel|reels|tv)\/[\w-]+/;
      page = re.test(path) ? location.href : nearbyLink(v, re);
    } else if (onSite("tiktok.com")) {
      const re = /\/video\/\d+/;
      page = re.test(path) ? location.href : nearbyLink(v, re);
    } else if (onSite("x.com") || onSite("twitter.com")) {
      page = nearbyLink(v, /\/status\/\d+/);
    }
    return { page: page || location.href, file, title: document.title };
  }

  // ---------- the quality menu ----------

  function send(msg) {
    return new Promise((resolve) => {
      try {
        chrome.runtime.sendMessage(msg, (res) => resolve(chrome.runtime.lastError ? { ok: false, error: "AnySave was updated. Reload this page." } : res));
      } catch { resolve({ ok: false, error: "AnySave was updated. Reload this page." }); }
    });
  }

  async function useCookies() {
    try { return !!(await chrome.storage.local.get("useCookies")).useCookies; } catch { return false; }
  }

  function getInfo(url, cookies) {
    const key = url + (cookies ? " +login" : "");
    if (!infoCache.has(key)) {
      const p = send({ type: "info", url, useCookies: cookies });
      p.then((r) => { if (!r?.ok) setTimeout(() => infoCache.delete(key), 2000); }); // let a failure be retried
      infoCache.set(key, p);
    }
    return infoCache.get(key);
  }

  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function item(label, sub, onPick) {
    const b = el("button", "item");
    b.type = "button";
    b.setAttribute("role", "menuitem");
    b.append(el("span", "", label));
    if (sub) b.append(el("span", "sub", sub));
    b.addEventListener("click", (e) => { e.preventDefault(); onPick(); });
    return b;
  }

  async function openMenu(v) {
    menuVideo = v;
    clearTimeout(hideTimer);
    btnEl.classList.add("open");
    const t = targetOf(v);
    menuEl.replaceChildren(el("div", "head", "Download with AnySave"));
    menuEl.hidden = false;
    placeMenu();

    const start = async (msg) => {
      closeMenu();
      btnEl.classList.add("busy");
      const res = await send(msg);
      btnEl.classList.remove("busy");
      if (res?.ok) flashDone();
      else showProblem(v, res?.error || "Couldn't start the download.");
    };
    const fileItem = () => item("Video file", "MP4", () =>
      start({ type: "directDownload", src: t.file, title: t.title }));

    if (!t.page) { // ad libraries: the video is a plain file
      if (t.file) menuEl.append(fileItem());
      else menuEl.append(el("div", "note err", "This video can't be saved from here. Play it, then try again."));
      return;
    }

    const loading = el("div", "note");
    loading.append(el("span", "spin"), el("span", "", "Finding qualities…"));
    menuEl.append(loading);
    const cookies = await useCookies();
    const res = await getInfo(t.page, cookies);
    if (menuEl.hidden || menuVideo !== v) return; // closed meanwhile
    loading.remove();

    if (res?.ok && res.options?.length) {
      if (res.title) menuEl.firstChild.textContent = res.title;
      for (const opt of res.options) {
        menuEl.append(item(opt.label.replace(/ \((MP4|MP3|M4A)\)$/, ""), opt.audioOnly ? "audio" : "", () => start({
          type: "download", url: res.url || t.page, title: res.title || t.title,
          format: opt.format, sort: opt.sort, audioOnly: !!opt.audioOnly, useCookies: cookies,
        })));
      }
      if (t.file) { menuEl.append(el("hr")); menuEl.append(fileItem()); }
    } else if (t.file) { // the helper couldn't read the page, but the video is a plain file
      menuEl.append(fileItem());
    } else {
      const msg = res?.hostMissing ? "The AnySave helper isn't installed. Run the AnySave installer."
        : !cookies && (onSite("facebook.com") || onSite("instagram.com"))
          ? "Couldn't read this video. Open the AnySave popup and tick \"Use my Chrome login\"."
          : (res?.error || "Couldn't read this video.");
      menuEl.append(el("div", "note err", msg));
    }
    placeMenu();
  }

  function showProblem(v, text) {
    menuEl.replaceChildren(el("div", "head", "AnySave"), el("div", "note err", text));
    menuEl.hidden = false;
    menuVideo = v;
    btnEl.classList.add("open");
    placeMenu();
  }

  function closeMenu() {
    if (!menuEl || menuEl.hidden) return;
    menuEl.hidden = true;
    menuVideo = null;
    btnEl.classList.remove("open");
    scheduleHide(1500);
  }

  function flashDone() {
    btnEl.classList.add("done");
    btnEl.title = "Downloading: see the AnySave popup";
    setTimeout(() => {
      btnEl?.classList.remove("done");
      if (btnEl) btnEl.title = "Download with AnySave";
    }, 2600);
  }

  function placeMenu() {
    if (menuEl.hidden) return;
    const b = btnEl.getBoundingClientRect();
    const w = 248, h = menuEl.offsetHeight || 120;
    const left = Math.max(8, Math.min(window.innerWidth - w - 8, b.right - w));
    const below = b.bottom + 6;
    const top = below + h > window.innerHeight - 8 ? Math.max(8, b.top - 6 - h) : below;
    menuEl.style.left = `${Math.round(left)}px`;
    menuEl.style.top = `${Math.round(top)}px`;
  }

  // ---------- showing the button ----------

  function bigEnough(r) {
    return r.width >= MIN_W && r.height >= MIN_H && r.bottom > 40 && r.top < window.innerHeight - 40;
  }

  function updatePosition() {
    if (!activeVideo || !btnEl) return;
    const r = activeVideo.getBoundingClientRect();
    if (!bigEnough(r)) { btnEl.classList.remove("visible"); closeMenu(); return; }
    btnEl.style.top = `${Math.round(Math.max(8, r.top + 10))}px`;
    btnEl.style.left = `${Math.round(Math.max(8, Math.min(window.innerWidth - 46, r.right - 48)))}px`;
    placeMenu();
  }

  function scheduleHide(ms = 1400) {
    clearTimeout(hideTimer);
    hideTimer = setTimeout(() => {
      if (menuEl && !menuEl.hidden) return; // stays while its menu is open
      btnEl?.classList.remove("visible");
      activeVideo = null;
    }, ms);
  }

  function showForVideo(v) {
    if (!bigEnough(v.getBoundingClientRect())) return;
    ensureUI();
    if (menuEl && !menuEl.hidden && menuVideo && menuVideo !== v) return; // keep the open menu's video
    activeVideo = v;
    updatePosition();
    btnEl.classList.add("visible");
    scheduleHide(2200);
  }

  document.addEventListener("pointermove", (e) => {
    if (rafId) return;
    const x = e.clientX, y = e.clientY, target = e.target;
    rafId = requestAnimationFrame(() => {
      rafId = null;
      if (!(target instanceof Element) || target === hostEl) return;
      const v = target.tagName === "VIDEO" ? target : target.closest?.("video");
      if (v) { showForVideo(v); return; }
      // Many sites lay a see-through layer (controls, links) over the video: go by position.
      const videos = document.getElementsByTagName("video");
      for (let i = 0; i < videos.length && i < 40; i++) {
        const r = videos[i].getBoundingClientRect();
        if (x >= r.left && x <= r.right && y >= r.top && y <= r.bottom && bigEnough(r)) { showForVideo(videos[i]); return; }
      }
    });
  }, { passive: true, capture: true });

  window.addEventListener("scroll", () => {
    if (activeVideo && btnEl?.classList.contains("visible")) updatePosition();
  }, { passive: true, capture: true });
  window.addEventListener("resize", () => updatePosition(), { passive: true });

  // Close the menu by clicking elsewhere or pressing Escape.
  document.addEventListener("pointerdown", (e) => {
    if (menuEl && !menuEl.hidden && e.composedPath()[0] && !e.composedPath().includes(hostEl)) closeMenu();
  }, true);
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") closeMenu(); }, true);
})();
