// AnySave: runs on TikTok Creative Center pages.
// An ad's video only exists while the mouse is over the ad, and it's gone by the time the
// AnySave popup opens. So this script records each ad video the moment it starts playing,
// and the popup lists everything recorded on this page.
(() => {
  if (window.__vgSeen) return;
  const seen = (window.__vgSeen = new Map()); // src -> { src, id, name, poster, duration, at }

  // Metric labels and campaign objectives; the industry name (e.g. "Pet Information") is a better title.
  const skip = /^(?:(?:See analytics|Likes|CTR|Budget|Low|Medium|High|Top|Reach|Conversions?|Traffic|App Installs?|Video Views|Lead Generation|Product Sales|Community Interaction|Engagement|Catalog Sales)$|\d)/i;

  function describe(v, src) {
    // Walk up to the ad card: the first ancestor with some text, and its link to the ad page.
    let el = v, text = "";
    for (let i = 0; i < 12 && el.parentElement; i++) {
      el = el.parentElement;
      const t = el.innerText || "";
      if (t.trim().length > 15) { text = t; break; }
    }
    let card = el;
    for (let i = 0; i < 6 && card && !card.querySelector('a[href*="/topads/"]'); i++) card = card.parentElement;
    const link = card && card.querySelector('a[href*="/topads/"]');
    const id = (link && (link.href.match(/\/topads\/(\d+)/) || [])[1]) || null;
    const lines = text.split("\n").map((s) => s.trim()).filter(Boolean);
    const name = lines.find((l) => l.length > 2 && l.length < 70 && !skip.test(l)) || null;
    const img = card && card.querySelector("img");
    return {
      src, id, name,
      poster: v.poster || (img && img.src) || null,
      duration: isFinite(v.duration) ? Math.round(v.duration) : null,
      at: Date.now(),
    };
  }

  function scan() {
    for (const v of document.querySelectorAll("video")) {
      const src = v.currentSrc || v.src || "";
      // Ad videos come from ".../video/tos/..."; TikTok's own promo clips don't.
      if (!/^https?:/.test(src) || !/\/video\/tos\//.test(src)) continue;
      const known = seen.get(src);
      if (!known) seen.set(src, describe(v, src));
      else if (!known.duration && isFinite(v.duration)) known.duration = Math.round(v.duration);
    }
  }

  // Catch videos as soon as they appear or start playing (media events don't bubble, so listen in capture).
  new MutationObserver(scan).observe(document.documentElement, {
    subtree: true, childList: true, attributes: true, attributeFilter: ["src"],
  });
  for (const ev of ["play", "playing", "loadedmetadata"]) document.addEventListener(ev, scan, true);
  setInterval(scan, 1500);
  scan();
})();
