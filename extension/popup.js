const SITES = [
  [/(^|\.)youtube\.com$|(^|\.)youtu\.be$/, "YouTube"],
  [/(^|\.)facebook\.com$|(^|\.)fb\.watch$/, "Facebook"],
  [/(^|\.)instagram\.com$/, "Instagram"],
  [/(^|\.)tiktok\.com$/, "TikTok"],
];

const $ = (id) => document.getElementById(id);
let current = null; // { url, title }

function siteOf(url) {
  try {
    const host = new URL(url).hostname;
    const hit = SITES.find(([re]) => re.test(host));
    return hit ? hit[1] : null;
  } catch { return null; }
}

function hostLabel(url) {
  try {
    const parts = new URL(url).hostname.replace(/^(www|m)\./, "").split(".");
    const n = parts.length >= 2 ? parts[parts.length - 2] : parts[0];
    return n.charAt(0).toUpperCase() + n.slice(1);
  } catch { return "Web"; }
}

function fmtDuration(s) {
  if (!s) return "";
  s = Math.round(s);
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
  return (h ? h + ":" + String(m).padStart(2, "0") : m) + ":" + String(sec).padStart(2, "0");
}

function setState(html) {
  $("state").hidden = false;
  $("state").innerHTML = html;
}

/** Ad libraries list many ads per page; their videos are read from the page itself. */
function adLibraryOf(url) {
  try {
    const u = new URL(url);
    if (/(^|\.)facebook\.com$/.test(u.hostname) && u.pathname.startsWith("/ads/library")) return "Meta Ad Library";
    if (u.hostname === "ads.tiktok.com" && u.pathname.includes("/creativecenter")) return "TikTok Creative Center";
  } catch {}
  return null;
}

/**
 * Runs inside the web page: lists <video> elements that have a real file URL
 * (not blob: streams), with a name and ad ID taken from the surrounding ad card.
 */
function collectPageVideos() {
  const isMeta = /facebook\.com$/.test(location.hostname);
  const skip = /^(Active|Inactive|Library ID|Started running|Platforms|See ad details|See summary details|This ad has|Sponsored|See analytics|Likes|CTR|Budget|Reach|Conversions?|Traffic|App Installs?|Video Views|Lead Generation|Product Sales|Community Interaction|Engagement|Catalog Sales|\d)/i;
  const out = [];
  const seen = new Set();
  for (const v of document.querySelectorAll("video")) {
    const src = v.currentSrc || v.src || (v.querySelector("source") || {}).src || "";
    if (!/^https?:/.test(src) || seen.has(src)) continue;
    // TikTok Creative Center: ad videos come from ".../video/tos/..."; skip TikTok's own promo clips.
    if (location.hostname === "ads.tiktok.com" && !/\/video\/tos\//.test(src)) continue;
    seen.add(src);
    // Walk up to the ad's card. On Meta, the card is the block that shows "Library ID".
    let el = v, text = "", first = "";
    for (let i = 0; i < 16 && el.parentElement; i++) {
      el = el.parentElement;
      const t = el.innerText || "";
      if (!first && t.trim().length > 15) first = t;
      if (isMeta ? /Library ID/.test(t) : t.length > 40) { text = t; break; }
    }
    text = text || first;
    const link = el.querySelector && el.querySelector('a[href*="/topads/"]');
    const id = (text.match(/Library ID:?\s*(\d{6,})/) || [])[1]
      || (link && (link.href.match(/\/topads\/(\d+)/) || [])[1]) || null;
    const lines = text.split("\n").map((s) => s.trim()).filter(Boolean);
    const sp = lines.findIndex((l) => l === "Sponsored");
    const name = (sp > 0 && lines[sp - 1]) || lines.find((l) => l.length > 2 && l.length < 70 && !skip.test(l)) || null;
    out.push({
      src, id, name,
      poster: v.poster || null,
      duration: isFinite(v.duration) ? Math.round(v.duration) : null,
    });
  }
  // TikTok Creative Center: add the ads capture.js recorded while they played on hover.
  if (window.__vgSeen) {
    const recorded = [...window.__vgSeen.values()].sort((a, b) => a.at - b.at);
    for (const r of recorded) {
      const i = out.findIndex((o) => o.src === r.src);
      if (i >= 0) out[i] = { ...r, ...out[i], name: out[i].name || r.name, id: out[i].id || r.id, poster: out[i].poster || r.poster };
      else out.push({ src: r.src, id: r.id, name: r.name, poster: r.poster, duration: r.duration });
    }
  }
  return out;
}

async function scanPage(tab) {
  try {
    // Start the TikTok recorder on pages that were open before AnySave was installed/updated.
    if (adLibraryOf(tab.url) === "TikTok Creative Center") {
      await chrome.scripting.executeScript({ target: { tabId: tab.id }, files: ["capture.js"] });
    }
    const [{ result }] = await chrome.scripting.executeScript({ target: { tabId: tab.id }, func: collectPageVideos });
    return result || [];
  } catch (e) {
    return [];
  }
}

function safeName(s) {
  return String(s || "video").replace(/[\\/:*?"<>|\n\r\t]+/g, " ").replace(/\s+/g, " ").trim().slice(0, 80) || "video";
}

/** Download one page video: Meta ads go through yt-dlp by Library ID (HD); others are saved directly. */
function downloadPageVideo(v, library) {
  const title = safeName(
    library === "Meta Ad Library"
      ? `${v.name || "Meta ad"}${v.id ? " - ad " + v.id : ""}`
      : `${v.name || "TikTok ad"}${v.duration ? " - " + v.duration + "s" : ""}`
  );
  if (library === "Meta Ad Library" && v.id) {
    return chrome.runtime.sendMessage({
      type: "download", url: `https://www.facebook.com/ads/library/?id=${v.id}`,
      title, format: "hd/b/bv*+ba", useCookies: $("cookies").checked,
    });
  }
  return chrome.runtime.sendMessage({ type: "directDownload", src: v.src, title });
}

// Quality for a whole batch (a playlist's videos can each have different formats).
const BATCH_QUALITIES = [
  { label: "Best quality (MP4)", format: "bv*+ba/b", sort: "res,fps,vcodec:h264,acodec:m4a" },
  ...[1080, 720, 480].map((h) => ({ label: `${h}p MP4`, format: `bv*[height<=${h}]+ba/b[height<=${h}]`, sort: "res,fps,vcodec:h264,acodec:m4a" })),
  { label: "Audio only (MP3)", format: "ba[ext=m4a]/ba/b", audioOnly: true },
];

// YouTube playlist / channel pages → the link that lists their videos.
// watch: the page is one video that is part of a playlist.
function youtubeList(pageUrl) {
  try {
    const u = new URL(pageUrl);
    if (!/(^|\.)youtube\.com$/.test(u.hostname)) return null;
    const list = u.searchParams.get("list");
    if (list) return { url: `https://www.youtube.com/playlist?list=${encodeURIComponent(list)}`, watch: u.pathname === "/watch", heading: "Videos in this playlist" };
    const m = u.pathname.match(/^\/(@[^/]+|channel\/[^/]+|c\/[^/]+|user\/[^/]+)(?:\/(featured|videos|shorts|streams))?\/?$/);
    if (m) {
      const tab = !m[2] || m[2] === "featured" ? "videos" : m[2];
      return { url: `https://www.youtube.com/${m[1]}/${tab}`, watch: false, heading: tab === "shorts" ? "Shorts on this channel" : tab === "streams" ? "Live videos on this channel" : "Videos on this channel" };
    }
  } catch {}
  return null;
}

// Lists a playlist / channel and shows it as a pick-and-download list.
async function showPlaylist(url, heading, below) {
  const pl = await chrome.runtime.sendMessage({ type: "playlist", url, useCookies: $("cookies").checked });
  if (!pl?.ok || !pl.entries?.length) return false;
  const videos = pl.entries.map((e) => ({ src: e.url, name: e.title, duration: e.duration, poster: e.thumbnail }));
  // "Blender Studio - Shorts" already names the channel; don't repeat it.
  const note = pl.uploader && !String(pl.title || "").includes(pl.uploader) ? [pl.title, pl.uploader].filter(Boolean).join(" · ") : (pl.title || "");
  renderPageVideos(videos, null, note, {
    heading, quality: true, below,
    jobUrl: (v) => v.src,
    download: (v) => {
      const q = JSON.parse($("pvQuality").value);
      return chrome.runtime.sendMessage({ type: "download", url: v.src, title: v.name, format: q.format, sort: q.sort,
        audioOnly: !!q.audioOnly, useCookies: $("cookies").checked });
    },
  });
  return true;
}

// The download a page ad belongs to: Meta ads by Library ID, others by their video file.
function adJobUrl(v, library) {
  return library === "Meta Ad Library" && v.id ? `https://www.facebook.com/ads/library/?id=${v.id}` : v.src;
}
let adProgress = null; // while an ad list is shown: called with the latest jobs

// `opts` (playlists): { heading, download(v), jobUrl(v), quality: true, below: true }
function renderPageVideos(videos, library, note, opts = {}) {
  const box = $("pageVideos");
  box.hidden = false;
  box.classList.toggle("below", !!opts.below); // under a single video's card
  const noun = library ? "ad" : "video";
  const plural = (n) => `${n} ${noun}${n > 1 ? "s" : ""}`;
  $("pvTitle").textContent = `${opts.heading || (library ? "Ads on this page" : "Videos on this page")} (${videos.length})`;
  $("pvNote").textContent = note || "";
  $("pvQualityBox").hidden = !opts.quality;
  if (opts.quality && !$("pvQuality").options.length) {
    for (const q of BATCH_QUALITIES) $("pvQuality").append(new Option(q.label, JSON.stringify(q)));
  }
  const download = opts.download || ((v) => downloadPageVideo(v, library));
  const jobUrl = opts.jobUrl || ((v) => adJobUrl(v, library));
  // Pick any number with the checkboxes (or tap a row), then "Download selected".
  // Each ad then shows its own live progress, also after the popup is closed and opened again.
  const selected = new Set();
  const jobOf = new Map(); // ad -> job id started from this list
  const pending = new Set(); // asked to start, job not created yet
  const rows = new Map();
  let jobs = {};
  const jobFor = (v) => {
    const own = jobs[jobOf.get(v)];
    if (own) return own;
    const url = jobUrl(v);
    let best = null;
    for (const j of Object.values(jobs)) {
      if (j.url === url && j.status !== "cancelled" && (!best || (j.createdAt || 0) > (best.createdAt || 0))) best = j;
    }
    return best;
  };
  const busy = (v) => { const j = jobFor(v); return pending.has(v) || (!!j && j.status !== "error"); }; // failed ones can be picked again

  const start = async (v) => {
    pending.add(v); selected.delete(v); refresh(); // shows "Starting…" at once
    const r = await download(v);
    if (r?.ok && r.id) jobOf.set(v, r.id);
    pending.delete(v);
    refresh();
  };

  // The right-hand side of a row: Download → live progress → Saved ✓ (opens the file), or Retry.
  const showAction = (v, li, j) => {
    const slot = li.querySelector(".pv-act");
    if (!j && pending.has(v)) j = { status: "starting", percent: 0 };
    const st = j?.status;
    const kind = !j ? "new" : st === "error" ? "error" : st === "done" ? "done" : "run";
    if (slot.dataset.kind !== kind) {
      slot.dataset.kind = kind;
      if (kind === "new" || kind === "error") {
        const b = Object.assign(document.createElement("button"), { className: "mini", textContent: kind === "new" ? "Download" : "Retry" });
        if (kind === "error") b.title = j.error || "Download failed";
        b.onclick = (e) => {
          e.stopPropagation(); b.disabled = true;
          const cur = jobFor(v);
          if (cur?.status === "error") chrome.runtime.sendMessage({ type: "retry", id: cur.id }); else start(v);
        };
        slot.replaceChildren(b);
      } else if (kind === "run") {
        slot.innerHTML = `<div class="prog-pill"><div class="fill"></div><span></span></div>`;
      } else {
        const b = Object.assign(document.createElement("button"), { className: "prog-pill ok", textContent: "Saved ✓", title: "Open the file" });
        b.onclick = (e) => { e.stopPropagation(); openDownload(jobFor(v)); };
        slot.replaceChildren(b);
      }
    }
    if (kind === "run") {
      const pill = slot.firstElementChild;
      const pct = Math.max(0, Math.min(100, Math.floor(j.percent || 0)));
      const waiting = st === "starting" || pct === 0;
      const queued = st === "queued";
      pill.classList.toggle("wait", waiting && st !== "paused" && !queued);
      pill.classList.toggle("paused", st === "paused" || queued);
      pill.querySelector(".fill").style.width = waiting && !queued ? "" : (queued ? "0%" : pct + "%");
      pill.querySelector("span").textContent = queued ? "Queued" : st === "paused" ? `Paused ${pct}%` : waiting ? "Starting…" : `${pct}%`;
    }
  };

  const refresh = () => {
    let running = 0, saved = 0, sum = 0;
    for (const [v, li] of rows) {
      const j = jobFor(v) || (pending.has(v) ? { status: "starting", percent: 0 } : null);
      li.classList.toggle("selected", selected.has(v));
      li.classList.toggle("started", busy(v));
      const cb = li.querySelector("input");
      cb.checked = selected.has(v); cb.disabled = busy(v);
      showAction(v, li, j);
      if (j?.status === "done") { saved++; sum += 100; }
      else if (j && j.status !== "error") { running++; sum += j.percent || 0; }
    }
    const open = videos.filter((v) => !busy(v));
    $("pvAllBox").hidden = open.length < 2;
    $("pvAll").checked = open.length > 0 && open.every((v) => selected.has(v));
    $("pvAll").indeterminate = selected.size > 0 && !$("pvAll").checked;
    const btn = $("pvDownload");
    const n = selected.size;
    btn.classList.toggle("prog", !n && running > 0);
    btn.classList.toggle("saved", !n && !running && saved > 0);
    if (n) {
      btn.disabled = false; btn.textContent = `Download ${n} selected ${noun}${n > 1 ? "s" : ""}`;
    } else if (running) {
      const pct = Math.floor(sum / (running + saved));
      btn.disabled = true; btn.style.setProperty("--p", pct + "%");
      btn.textContent = `Downloading ${plural(running)} · ${pct}%`;
    } else if (saved) {
      btn.disabled = true; btn.textContent = `✓ ${plural(saved)} saved · see Downloads below`;
    } else {
      btn.disabled = true; btn.textContent = `Select ${noun}s to download`;
    }
  };

  $("pvAll").onchange = () => {
    const open = videos.filter((v) => !busy(v));
    if ($("pvAll").checked) open.forEach((v) => selected.add(v)); else selected.clear();
    refresh();
  };
  $("pvDownload").onclick = async () => {
    const list = videos.filter((v) => selected.has(v));
    $("pvDownload").disabled = true;
    await Promise.all(list.map(start)); // all at once, each shows its own progress
  };
  const ul = $("pvList");
  ul.innerHTML = "";
  for (const v of videos) {
    const li = document.createElement("li");
    li.className = "pv";
    li.innerHTML = `
      <input type="checkbox" aria-label="Select this ${noun}" />
      ${v.poster ? `<img src="${escapeHtml(v.poster)}" alt="" loading="lazy" />` : `<div class="pv-ph">▶</div>`}
      <div class="pv-info">
        <div class="name">${escapeHtml(v.name || (library ? "Ad" : "Video"))}</div>
        <div class="small muted meta">${[fmtDuration(v.duration), v.id ? "ID " + v.id : ""].filter(Boolean).map(escapeHtml).join(" · ")}</div>
      </div>
      <div class="pv-act"></div>`;
    // Tapping anywhere on the row (or its checkbox) selects it.
    li.onclick = (e) => {
      if (busy(v) || e.target.closest("button")) return;
      if (e.target.matches("input")) { e.target.checked ? selected.add(v) : selected.delete(v); }
      else { selected.has(v) ? selected.delete(v) : selected.add(v); }
      refresh();
    };
    rows.set(v, li);
    ul.appendChild(li);
  }
  adProgress = (j) => { jobs = j || {}; refresh(); };
  refresh();
  chrome.storage.local.get("jobs").then((r) => adProgress(r.jobs));
}

async function loadInfo() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  const site = siteOf(tab?.url || "");
  const library = adLibraryOf(tab?.url || "");
  $("site").textContent = library || site || "";
  $("media").hidden = true;
  $("pageVideos").hidden = true;

  if (library) {
    setState("Finding ad videos on this page…");
    const videos = await scanPage(tab);
    const tiktokHint = "Move your mouse over the ads you want so each one starts playing. AnySave remembers them, even after they stop. Then click AnySave again.";
    if (!videos.length) {
      setState(library === "TikTok Creative Center" ? tiktokHint
        : "No ad videos found yet. Scroll so the ads load (filter Media type: Videos), then open AnySave again.");
      return;
    }
    $("state").hidden = true;
    renderPageVideos(videos, library,
      library === "TikTok Creative Center" ? "Ads you've hovered over on this page. Hover over more to add them." : "Downloaded in HD where available.");
    return;
  }

  if (!site) {
    if (!/^https?:/.test(tab?.url || "")) {
      setState("Open a page with a video, then click AnySave. Or open the download manager to add any link.");
      return;
    }
    // Any other site: yt-dlp knows 1,000+ video sites; fall back to video files the page plays.
    $("site").textContent = hostLabel(tab.url);
  }
  // A YouTube playlist or channel page: list its videos to pick from.
  const yl = youtubeList(tab.url);
  if (yl && !yl.watch) {
    setState("Listing the videos…");
    if (await showPlaylist(yl.url, yl.heading, false)) { $("state").hidden = true; return; }
  }
  setState("Looking up video…");
  const res = await chrome.runtime.sendMessage({ type: "info", url: tab.url, useCookies: $("cookies").checked });
  // Other sites: a link yt-dlp reads as a list of videos (no single video in it).
  if (!res?.hostMissing && (!res?.ok || !res.options?.length) && !yl) {
    setState("Looking for a list of videos…");
    if (await showPlaylist(tab.url, "Videos on this page", false)) { $("state").hidden = true; return; }
  }
  if (!res?.ok && !res?.hostMissing) {
    // yt-dlp couldn't read the page: fall back to video files the page itself is playing.
    const videos = await scanPage(tab);
    if (videos.length) {
      setState('<span class="small muted">Couldn\'t read this page directly, but found its video files:</span>');
      renderPageVideos(videos, null, "");
      return;
    }
  }
  if (!res?.ok) {
    if (res?.hostMissing) {
      setState('Helper app not found. Run the AnySave installer again, then restart Chrome.<br><span class="small">' + escapeHtml(res.error) + "</span>");
    } else {
      if (!site) {
        setState("No video found on this page. Play the video and open AnySave again, or right-click a link → Download link with AnySave.");
        return;
      }
      setState('<span class="err">' + escapeHtml(res?.error || "Unknown error") + "</span>" +
        (site === "Instagram" || site === "Facebook"
          ? '<br><span class="small">' + ($("cookies").checked
              ? "Make sure you're logged in to " + site + " in this Chrome window, then reopen AnySave."
              : 'Tip: tick "Use my Chrome login" below and try again.') + "</span>"
          : ""));
    }
    return;
  }
  current = { url: res.url || tab.url, title: res.title };
  $("state").hidden = true;
  $("media").hidden = false;
  $("thumb").src = res.thumbnail || "";
  $("title").textContent = res.title || "Untitled";
  $("meta").textContent = [res.uploader, fmtDuration(res.duration)].filter(Boolean).join(" · ");
  const sel = $("format");
  sel.innerHTML = "";
  for (const opt of res.options || []) {
    const o = document.createElement("option");
    o.value = JSON.stringify(opt);
    o.textContent = opt.label;
    sel.appendChild(o);
  }
  $("dl").disabled = !sel.options.length;
  dlJobId = null; dlFresh = false;
  // A video that's part of a playlist: offer the rest of the playlist underneath.
  if (yl?.watch) showPlaylist(yl.url, yl.heading, true);
  lastJobs = (await chrome.storage.local.get("jobs")).jobs || {};
  showDlProgress();
  if (res.needsFfmpeg) {
    $("meta").textContent += " · video needs ffmpeg (audio only for now)";
  } else if (!res.hasFfmpeg) {
    $("meta").textContent += " · install ffmpeg for HD";
  }
}

function escapeHtml(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

// The Download button turns into the download's live progress: Starting… → percent, speed
// and time left → "Saved ✓ · Open file". Picking another quality makes it a Download button
// again (to save a second version). It also picks up a download of this video started before.
let dlJobId = null;      // download started from this button
let dlFresh = false;     // quality changed: ignore earlier downloads of this video
let lastJobs = {};
function dlJob() {
  if (dlJobId && lastJobs[dlJobId]) return lastJobs[dlJobId];
  if (!current || dlFresh) return null;
  let best = null;
  for (const j of Object.values(lastJobs)) {
    if (j.kind === "video" && j.url === current.url && j.status !== "cancelled" && (!best || (j.createdAt || 0) > (best.createdAt || 0))) best = j;
  }
  return best;
}
function showDlProgress() {
  const btn = $("dl");
  if (!current || $("media").hidden) return;
  const j = dlJob();
  const st = j?.status;
  const pct = Math.max(0, Math.min(100, Math.floor(j?.percent || 0)));
  btn.classList.toggle("prog", !!j && !["done", "error"].includes(st));
  btn.classList.toggle("wait", !!j && (st === "starting" || st === "queued" || (st === "downloading" && pct === 0)));
  btn.classList.toggle("saved", st === "done");
  btn.style.setProperty("--p", pct + "%");
  if (!j || st === "cancelled") { btn.dataset.mode = "download"; btn.disabled = !$("format").options.length; btn.textContent = "Download"; }
  else if (st === "done") { btn.dataset.mode = "open"; btn.disabled = false; btn.textContent = "Saved ✓ · Open file"; }
  else if (st === "error") { btn.dataset.mode = "download"; btn.disabled = false; btn.textContent = "Failed · Try again"; btn.title = j.error || ""; }
  else if (st === "paused") { btn.dataset.mode = "busy"; btn.disabled = true; btn.textContent = `Paused · ${pct}%`; }
  else if (st === "queued") { btn.dataset.mode = "busy"; btn.disabled = true; btn.textContent = "Queued · starts after the current downloads"; }
  else if (btn.classList.contains("wait")) { btn.dataset.mode = "busy"; btn.disabled = true; btn.textContent = "Starting…"; }
  else { btn.dataset.mode = "busy"; btn.disabled = true; btn.textContent = [`Downloading · ${pct}%`, j.speed, j.eta && j.eta !== "?" && `${j.eta} left`].filter(Boolean).join(" · "); }
}
$("format").addEventListener("change", () => { dlJobId = null; dlFresh = true; showDlProgress(); });

$("dl").addEventListener("click", async () => {
  if (!current || $("dl").disabled) return;
  if ($("dl").dataset.mode === "open") { const j = dlJob(); if (j) openDownload(j); return; }
  if (!$("format").value) return;
  const opt = JSON.parse($("format").value);
  $("dl").disabled = true; // one click, one download
  $("dl").classList.add("prog", "wait"); $("dl").textContent = "Starting…";
  const res = await chrome.runtime.sendMessage({
    type: "download",
    url: current.url,
    title: current.title,
    format: opt.format,
    sort: opt.sort,
    audioOnly: !!opt.audioOnly,
    useCookies: $("cookies").checked,
  });
  if (res?.ok) { dlJobId = res.id; dlFresh = false; }
  else setState('<span class="err">' + escapeHtml(res?.error || "Couldn't start the download") + "</span>");
  lastJobs = (await chrome.storage.local.get("jobs")).jobs || {};
  showDlProgress();
});

$("cookies").addEventListener("change", async () => {
  await chrome.storage.local.set({ useCookies: $("cookies").checked });
  loadInfo();
});

$("clear").addEventListener("click", () => chrome.runtime.sendMessage({ type: "clear" }));
$("openManager").addEventListener("click", () => { chrome.runtime.sendMessage({ type: "openManager" }); window.close(); });
$("openConvert").addEventListener("click", () => { chrome.runtime.sendMessage({ type: "openManager", tab: "convert" }); window.close(); });

// ---------- open / drag a finished download ----------
// Files saved through Chrome's downloader (any file, website downloads) use Chrome's own
// open and drag, like its Downloads page. Files the helper saved (videos, conversions) are
// opened by the helper, and dragged as a file link where the browser allows it.
async function openDownload(j) {
  if (j.downloadId) {
    try { await chrome.downloads.open(j.downloadId); return { ok: true }; } catch {}
  }
  return chrome.runtime.sendMessage({ type: "open", id: j.id });
}
function fileUrl(path) {
  const p = String(path).replace(/\\/g, "/");
  return "file://" + (p.startsWith("/") ? "" : "/") + encodeURI(p).replace(/#/g, "%23").replace(/\?/g, "%3F");
}
// The list is redrawn on every progress update; not while a row is being dragged.
let dragging = false, redrawAfterDrag = null;
document.addEventListener("dragend", () => { dragging = false; const f = redrawAfterDrag; redrawAfterDrag = null; f && f(); });
function startDrag(e, j) {
  // Chrome's own drag takes over at once (no dragend comes back), so no pause needed.
  if (j.downloadId) { e.preventDefault(); chrome.downloads.drag(j.downloadId); return; }
  dragging = true;
  const name = String(j.file).split(/[\\/]/).pop();
  e.dataTransfer.effectAllowed = "copy";
  e.dataTransfer.setData("DownloadURL", `application/octet-stream:${name}:${fileUrl(j.file)}`);
  e.dataTransfer.setData("text/uri-list", fileUrl(j.file));
}

function renderJobs(jobs) {
  if (dragging) { redrawAfterDrag = () => renderJobs(jobs); return; }
  const list = Object.values(jobs || {}).sort((a, b) => (b.createdAt || b.id) - (a.createdAt || a.id)).slice(0, 5);
  const ul = $("jobs");
  ul.innerHTML = list.length ? "" : '<li class="muted small">Nothing yet.</li>';
  for (const j of list) {
    const li = document.createElement("li");
    const pct = Math.max(0, Math.min(100, j.percent || 0));
    let line = "";
    if (j.status === "downloading") line = `${pct.toFixed(0)}% · ${j.speed || ""} · ETA ${j.eta || "?"}`;
    else if (j.status === "done") line = '<span class="ok">✓ Saved</span>';
    else if (j.status === "error") line = `<span class="err">${escapeHtml(j.error || "Failed")}</span>`;
    else line = escapeHtml({ queued: "Queued", starting: "Starting…", paused: `Paused · ${pct.toFixed(0)}%`, cancelled: "Cancelled" }[j.status] || j.status);
    li.innerHTML = `<div class="name">${escapeHtml(j.title || j.url)}</div>
      ${["downloading", "starting", "merging", "converting", "queued", "paused"].includes(j.status) ? `<div class="bar"><div style="width:${pct}%"></div></div>` : ""}
      <div class="row small muted"><span>${line}</span><span class="act"></span></div>`;
    const act = li.querySelector(".act");
    if (j.status === "done" && (j.file || j.downloadId)) {
      const open = Object.assign(document.createElement("button"), { className: "link small", textContent: "Open" });
      open.onclick = () => openDownload(j).then((r) => { if (r && !r.ok) alert(r.error || "Couldn't open the file"); });
      const show = Object.assign(document.createElement("button"), { className: "link small", textContent: "Show in folder" });
      show.onclick = () => chrome.runtime.sendMessage({ type: "reveal", id: j.id });
      act.append(open, show);
      // Drag the row out (to the desktop, a folder, a chat…), or double-click to open.
      li.draggable = true;
      li.classList.add("drag");
      li.title = "Drag the file anywhere, or double-click to open";
      li.ondragstart = (e) => startDrag(e, j);
      li.ondblclick = (e) => { if (!e.target.closest("button")) open.click(); };
    } else if (!["done", "error", "cancelled"].includes(j.status)) {
      const b = Object.assign(document.createElement("button"), { className: "link small", textContent: "Cancel" });
      b.onclick = () => chrome.runtime.sendMessage({ type: "cancel", id: j.id });
      act.appendChild(b);
    }
    ul.appendChild(li);
  }
}

chrome.storage.onChanged.addListener((changes, area) => {
  if (area === "local" && changes.jobs) {
    renderJobs(changes.jobs.newValue);
    if (adProgress) adProgress(changes.jobs.newValue);
    lastJobs = changes.jobs.newValue || {};
    showDlProgress();
    const finished = Object.values(changes.jobs.newValue || {}).some((j) => j.status === "done" && changes.jobs.oldValue?.[j.id]?.status !== "done");
    if (finished) chrome.runtime.sendMessage({ type: "seen" }); // finished while the popup is open
  }
});

const TERMS_VERSION = 2;

function openLegal(e) {
  e.preventDefault();
  chrome.tabs.create({ url: chrome.runtime.getURL("legal.html") });
}
$("legal").addEventListener("click", openLegal);
$("legal2").addEventListener("click", openLegal);

/** First use: show the agreement; nothing is looked up or downloaded until it's accepted. */
async function ensureAgreed() {
  const { acceptedTermsVersion = 0 } = await chrome.storage.local.get("acceptedTermsVersion");
  if (acceptedTermsVersion >= TERMS_VERSION) return;
  $("terms").hidden = false;
  $("agree").addEventListener("change", () => { $("accept").disabled = !$("agree").checked; });
  await new Promise((resolve) => {
    $("accept").addEventListener("click", async () => {
      await chrome.storage.local.set({ acceptedTermsVersion: TERMS_VERSION });
      $("terms").hidden = true;
      resolve();
    });
  });
}

(async () => {
  await ensureAgreed();
  $("cookies").checked = !!(await chrome.storage.local.get("useCookies")).useCookies;
  renderJobs((await chrome.storage.local.get("jobs")).jobs);
  chrome.runtime.sendMessage({ type: "seen" }); // clears the ✓ on the toolbar icon
  loadInfo();
})();
