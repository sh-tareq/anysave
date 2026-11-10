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

function renderPageVideos(videos, library, note) {
  const box = $("pageVideos");
  box.hidden = false;
  $("pvTitle").textContent = `${library ? "Ads" : "Videos"} on this page (${videos.length})`;
  $("pvNote").textContent = note || "";
  $("pvAll").hidden = videos.length < 2;
  $("pvAll").onclick = async () => {
    $("pvAll").disabled = true;
    for (const v of videos) await downloadPageVideo(v, library);
    $("pvAll").textContent = "Started ✓";
  };
  const ul = $("pvList");
  ul.innerHTML = "";
  for (const v of videos) {
    const li = document.createElement("li");
    li.className = "pv";
    li.innerHTML = `
      ${v.poster ? `<img src="${escapeHtml(v.poster)}" alt="" />` : `<div class="pv-ph">▶</div>`}
      <div class="pv-info">
        <div class="name">${escapeHtml(v.name || (library ? "Ad" : "Video"))}</div>
        <div class="small muted">${[v.id ? "ID " + v.id : "", fmtDuration(v.duration)].filter(Boolean).map(escapeHtml).join(" · ")}</div>
      </div>
      <button class="mini">Download</button>`;
    li.querySelector("button").onclick = async (e) => {
      e.target.disabled = true;
      await downloadPageVideo(v, library);
      e.target.textContent = "Started";
    };
    ul.appendChild(li);
  }
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
  setState("Looking up video…");
  const res = await chrome.runtime.sendMessage({ type: "info", url: tab.url, useCookies: $("cookies").checked });
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
  if (res.needsFfmpeg) {
    $("meta").textContent += " · video needs ffmpeg (audio only for now)";
  } else if (!res.hasFfmpeg) {
    $("meta").textContent += " · install ffmpeg for HD";
  }
}

function escapeHtml(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

$("dl").addEventListener("click", async () => {
  if (!current || !$("format").value || $("dl").disabled) return;
  const opt = JSON.parse($("format").value);
  const label = $("dl").textContent;
  $("dl").disabled = true; // one click, one download
  const res = await chrome.runtime.sendMessage({
    type: "download",
    url: current.url,
    title: current.title,
    format: opt.format,
    sort: opt.sort,
    audioOnly: !!opt.audioOnly,
    useCookies: $("cookies").checked,
  });
  if (res?.ok) {
    $("dl").textContent = "Started ✓";
    setTimeout(() => { $("dl").textContent = label; $("dl").disabled = false; }, 2000);
  } else {
    $("dl").textContent = label; $("dl").disabled = false;
    setState('<span class="err">' + escapeHtml(res?.error || "Couldn't start the download") + "</span>");
  }
});

$("cookies").addEventListener("change", async () => {
  await chrome.storage.local.set({ useCookies: $("cookies").checked });
  loadInfo();
});

$("clear").addEventListener("click", () => chrome.runtime.sendMessage({ type: "clear" }));
$("openManager").addEventListener("click", () => { chrome.runtime.sendMessage({ type: "openManager" }); window.close(); });
$("openConvert").addEventListener("click", () => { chrome.runtime.sendMessage({ type: "openManager", tab: "convert" }); window.close(); });

function renderJobs(jobs) {
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
    else line = escapeHtml(j.status);
    li.innerHTML = `<div class="name">${escapeHtml(j.title || j.url)}</div>
      ${["downloading", "starting", "merging", "converting"].includes(j.status) ? `<div class="bar"><div style="width:${pct}%"></div></div>` : ""}
      <div class="row small muted"><span>${line}</span><span class="act"></span></div>`;
    const act = li.querySelector(".act");
    if (j.status === "done" && (j.file || j.downloadId)) {
      const b = Object.assign(document.createElement("button"), { className: "link small", textContent: "Show file" });
      b.onclick = () => chrome.runtime.sendMessage({ type: "reveal", id: j.id });
      act.appendChild(b);
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
