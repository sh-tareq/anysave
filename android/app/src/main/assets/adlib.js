// VidGrab: injected into Meta Ad Library and TikTok Creative Center pages in the in-app browser.
// Records every ad video as it appears or plays (TikTok removes a video when it stops playing),
// and window.__vgList() returns them all as JSON for the app's "Ads found" list.
(() => {
  if (window.__vgList) return;
  const isMeta = /facebook\.com$/.test(location.hostname) && location.pathname.startsWith("/ads/library");
  const isTikTokCC = location.hostname === "ads.tiktok.com";
  const isWeb = !isMeta && !isTikTokCC; // any other website
  // Keep a longer list of loaded resources so streamed videos (.m3u8/.mpd) can be found.
  try { performance.setResourceTimingBufferSize(3000); } catch (e) {}
  const seen = new Map(); // src -> item

  const skip = /^(?:(?:Active|Inactive|Sponsored|See ad details|See summary details|See analytics|Likes|CTR|Budget|Low|Medium|High|Top|Reach|Conversions?|Traffic|App Installs?|Video Views|Lead Generation|Product Sales|Community Interaction|Engagement|Catalog Sales)$|Library ID|Started running|Platforms|This ad has|\d)/i;

  function describe(v, src) {
    // Walk up to the ad card. On Meta it's the block that shows "Library ID".
    let el = v, text = "", first = "";
    for (let i = 0; i < 16 && el.parentElement; i++) {
      el = el.parentElement;
      const t = el.innerText || "";
      if (!first && t.trim().length > 15) first = t;
      if (isMeta ? /Library ID/.test(t) : t.trim().length > 15) { text = t; break; }
    }
    text = text || first;
    let card = el;
    for (let i = 0; i < 6 && card && !isMeta && !card.querySelector('a[href*="/topads/"]'); i++) card = card.parentElement;
    const link = !isMeta && card && card.querySelector('a[href*="/topads/"]');
    const id = (text.match(/Library ID:?\s*(\d{6,})/) || [])[1]
      || (link && (link.href.match(/\/topads\/(\d+)/) || [])[1]) || null;
    const lines = text.split("\n").map((s) => s.trim()).filter(Boolean);
    const sp = lines.indexOf("Sponsored");
    const name = (sp > 0 && lines[sp - 1]) || lines.find((l) => l.length > 2 && l.length < 70 && !skip.test(l)) || null;
    const img = card && card.querySelector && card.querySelector("img");
    return {
      src, id, name: isWeb ? (name && name.length < 70 ? name : null) || document.title || null : name,
      site: isMeta ? "meta" : isTikTokCC ? "tiktok" : "web",
      poster: v.poster || (img && img.src) || null,
      duration: isFinite(v.duration) ? Math.round(v.duration) : null,
      at: Date.now(),
    };
  }

  function scan() {
    for (const v of document.querySelectorAll("video")) {
      const src = v.currentSrc || v.src || (v.querySelector("source") || {}).src || "";
      if (!/^https?:/.test(src)) continue;
      // TikTok: ad videos come from ".../video/tos/..."; skip TikTok's own promo clips.
      if (isTikTokCC && !/\/video\/tos\//.test(src)) continue;
      const known = seen.get(src);
      if (!known) seen.set(src, describe(v, src));
      else {
        if (!known.duration && isFinite(v.duration)) known.duration = Math.round(v.duration);
        if (!known.id || !known.name) Object.assign(known, describe(v, src), { at: known.at });
      }
    }
  }

  new MutationObserver(scan).observe(document.documentElement, {
    subtree: true, childList: true, attributes: true, attributeFilter: ["src"],
  });
  for (const ev of ["play", "playing", "loadedmetadata"]) document.addEventListener(ev, scan, true);
  setInterval(scan, 1500);
  scan();

  // Videos streamed through MediaSource show up as blob: in <video>; the real playlist or
  // file URL is still visible in the list of resources the page loaded.
  const mediaRe = /\.(m3u8|mpd|mp4|webm|m4v|mov)(\?|#|$)/i;
  function scanResources() {
    if (!isWeb) return;
    let entries = [];
    try { entries = performance.getEntriesByType("resource"); } catch (e) {}
    for (const e of entries) {
      const u = e.name;
      if (!/^https?:/.test(u) || seen.has(u) || !mediaRe.test(u)) continue;
      // Skip byte-range segments of a larger file and tiny files (ads, previews).
      if (/[?&](range|bytestart|byterange)=/i.test(u)) continue;
      if (e.transferSize && e.transferSize < 30000 && !/\.(m3u8|mpd)/i.test(u)) continue;
      const stream = /\.(m3u8|mpd)/i.test(u);
      seen.set(u, { src: u, id: null, name: document.title || null, site: "web",
        poster: null, duration: null, stream, at: Date.now() });
    }
  }
  setInterval(scanResources, 2000);

  window.__vgList = () => {
    scan();
    scanResources();
    // Meta can show the same ad twice (multiple versions): keep one per Library ID.
    const out = [], ids = new Set();
    for (const it of [...seen.values()].sort((a, b) => a.at - b.at)) {
      if (it.id && ids.has(it.id)) continue;
      if (it.id) ids.add(it.id);
      out.push(it);
    }
    return JSON.stringify(out);
  };
})();
