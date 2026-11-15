// AnySave download manager: downloads list (sidebar, toolbar, table, details, status bar),
// converter, transcripts, site grabber and settings. Also runs inside the desktop app
// (desktop-bridge.js stands in for the chrome.* APIs there).
const $ = (id) => document.getElementById(id);
const send = (msg) => chrome.runtime.sendMessage(msg);
const IN_EXTENSION = !!(window.chrome && chrome.runtime && chrome.runtime.id);
let jobs = {};
let filter = "all";
let view = "downloads";
let currentSettings = {}; // the latest settings (queues, schedule)
let selected = new Set();
let anchorId = null; // where a Shift-click selection starts
let query = "";
let sortKey = ""; // "" = running first, then newest
let sortAsc = false;
let shownIds = [];

// ---------- helpers ----------
function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}
function extOf(n) { const m = String(n || "").split(/[?#]/)[0].match(/\.([a-z0-9]{1,8})$/i); return m ? m[1].toLowerCase() : ""; }
function category(j) {
  if (j.kind === "convert") return "Converted";
  if (j.kind === "transcribe") return "Documents";
  if (j.kind === "subs") return "Videos";
  const e = extOf(j.fileName || j.file || j.title);
  if (j.audioOnly || /^(mp3|m4a|aac|wav|flac|ogg|opus|wma)$/.test(e)) return "Music";
  if (j.kind === "video" || /^(mp4|mkv|webm|mov|avi|m4v|flv|wmv|ts|mpe?g|3gp)$/.test(e)) return "Videos";
  if (/^(jpe?g|png|gif|webp|heic|bmp|svg|tiff?)$/.test(e)) return "Pictures";
  if (/^(pdf|docx?|xlsx?|pptx?|txt|csv|rtf|odt|ods|epub|srt)$/.test(e)) return "Documents";
  if (/^(zip|rar|7z|tar|gz|tgz|bz2|xz|iso|dmg)$/.test(e)) return "Archives";
  if (/^(apk|exe|msi|pkg|deb|rpm|appimage)$/.test(e)) return "Apps";
  return "Other";
}
// Material-style icons (24×24), drawn with currentColor so they follow the theme.
const P = {
  Videos: "M17 10.5V7a1 1 0 0 0-1-1H4a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-3.5l4 4v-11z",
  Music: "M12 3v10.55A4 4 0 1 0 14 17V7h4V3h-6z",
  Pictures: "M21 19V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2zM8.5 13.5l2.5 3 3.5-4.5 4.5 6H5l3.5-4.5z",
  Documents: "M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6zm2 16H8v-2h8v2zm0-4H8v-2h8v2zm-3-5V3.5L18.5 9H13z",
  Archives: "M20 6h-8l-2-2H4a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2zm-2 6h-2v2h2v2h-2v2h-2v-2h2v-2h-2v-2h2v-2h-2V8h2v2h2v2z",
  Apps: "M17.6 9.48l1.84-3.18a.4.4 0 0 0-.69-.4l-1.86 3.22a11.4 11.4 0 0 0-9.78 0L5.25 5.9a.4.4 0 0 0-.69.4L6.4 9.48A10.8 10.8 0 0 0 1 18h22a10.8 10.8 0 0 0-5.4-8.52zM7 15.25a1.25 1.25 0 1 1 0-2.5 1.25 1.25 0 0 1 0 2.5zm10 0a1.25 1.25 0 1 1 0-2.5 1.25 1.25 0 0 1 0 2.5z",
  Converted: "M12 6V3L8 7l4 4V8a4 4 0 0 1 4 4 3.9 3.9 0 0 1-.47 1.88l1.46 1.46A6 6 0 0 0 12 6zm0 10a4 4 0 0 1-4-4c0-.67.17-1.32.47-1.88L7.01 8.66A6 6 0 0 0 12 18v3l4-4-4-4v3z",
  Other: "M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6zm-1 7V3.5L18.5 9H13z",
  pause: "M6 19h4V5H6v14zm8-14v14h4V5h-4z",
  play: "M8 5v14l11-7z",
  close: "M19 6.41 17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z",
  open: "M19 19H5V5h7V3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7h-2v7zM14 3v2h3.59l-9.83 9.83 1.41 1.41L19 6.41V10h2V3h-7z",
  folder: "M10 4H4a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-8l-2-2z",
  convert: "M6.99 11 3 15l3.99 4v-3H14v-2H6.99v-3zM21 9l-3.99-4v3H10v2h7.01v3L21 9z",
  voice: "M12 14a3 3 0 0 0 3-3V5a3 3 0 0 0-6 0v6a3 3 0 0 0 3 3zm5.3-3a5.3 5.3 0 0 1-10.6 0H5a7 7 0 0 0 6 6.92V21h2v-3.08A7 7 0 0 0 19 11h-1.7z",
  captions: "M19 4H5a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V6a2 2 0 0 0-2-2zm0 14H5V6h14v12zM7 15h4v-2H7v2zm6 0h4v-2h-4v2zm-6-4h10V9H7v2z",
  text: "M14 17H4v2h10v-2zm6-8H4v2h16V9zM4 15h16v-2H4v2zM4 5v2h16V5H4z",
  cc: "M19 4H5c-1.11 0-2 .9-2 2v12c0 1.1.89 2 2 2h14c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zm-8 7H9.5v-.5h-2v3h2V13H11v1c0 .55-.45 1-1 1H7c-.55 0-1-.45-1-1v-4c0-.55.45-1 1-1h3c.55 0 1 .45 1 1v1zm7 0h-1.5v-.5h-2v3h2V13H18v1c0 .55-.45 1-1 1h-3c-.55 0-1-.45-1-1v-4c0-.55.45-1 1-1h3c.55 0 1 .45 1 1v1z",
  burn: "M21 3H3c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h5v2h8v-2h5c1.1 0 1.99-.9 1.99-2L23 5c0-1.1-.9-2-2-2zm0 14H3V5h18v12zM5 13h9v2H5zm0-3h14v2H5z",
  retry: "M17.65 6.35A7.96 7.96 0 0 0 12 4a8 8 0 1 0 7.73 10h-2.08A6 6 0 1 1 12 6c1.66 0 3.14.69 4.22 1.78L13 11h7V4l-2.35 2.35z",
  link: "M3.9 12c0-1.71 1.39-3.1 3.1-3.1h4V7H7c-2.76 0-5 2.24-5 5s2.24 5 5 5h4v-1.9H7c-1.71 0-3.1-1.39-3.1-3.1zM8 13h8v-2H8v2zm9-6h-4v1.9h4c1.71 0 3.1 1.39 3.1 3.1s-1.39 3.1-3.1 3.1h-4V17h4c2.76 0 5-2.24 5-5s-2.24-5-5-5z",
  check: "M9 16.17 4.83 12l-1.42 1.41L9 19 21 7l-1.41-1.41z",
  remove: "M6 19a2 2 0 0 0 2 2h8a2 2 0 0 0 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z",
  all: "M3 13h2v-2H3v2zm0 4h2v-2H3v2zm0-8h2V7H3v2zm4 4h14v-2H7v2zm0 4h14v-2H7v2zM7 7v2h14V7H7z",
  active: "M5 20h14v-2H5v2zM19 9h-4V3H9v6H5l7 7 7-7z",
  done: "M9 16.17 4.83 12l-1.42 1.41L9 19 21 7l-1.41-1.41z",
  waiting: "M11.99 2C6.47 2 2 6.48 2 12s4.47 10 9.99 10C17.52 22 22 17.52 22 12S17.52 2 11.99 2zM12 20c-4.42 0-8-3.58-8-8s3.58-8 8-8 8 3.58 8 8-3.58 8-8 8zm.5-13H11v6l5.25 3.15.75-1.23-4.5-2.67z",
  failed: "M11 15h2v2h-2zm0-8h2v6h-2zm.99-5C6.47 2 2 6.48 2 12s4.47 10 9.99 10C17.52 22 22 17.52 22 12S17.52 2 11.99 2zM12 20c-4.42 0-8-3.58-8-8s3.58-8 8-8 8 3.58 8 8-3.58 8-8 8z",
  transcripts: "M14 17H4v2h10v-2zm6-8H4v2h16V9zM4 15h16v-2H4v2zM4 5v2h16V5H4z",
  grab: "M15.5 14h-.79l-.28-.27A6.47 6.47 0 0 0 16 9.5 6.5 6.5 0 1 0 9.5 16c1.61 0 3.09-.59 4.23-1.57l.27.28v.79l5 4.99L20.49 19l-4.99-5zm-6 0C7.01 14 5 11.99 5 9.5S7.01 5 9.5 5 14 7.01 14 9.5 11.99 14 9.5 14z",
  settings: "M19.14 12.94a7.5 7.5 0 0 0 0-1.88l2.03-1.58a.5.5 0 0 0 .12-.64l-1.92-3.32a.5.5 0 0 0-.61-.22l-2.39.96a7 7 0 0 0-1.62-.94l-.36-2.54a.5.5 0 0 0-.5-.42h-3.84a.5.5 0 0 0-.5.42l-.36 2.54c-.59.24-1.13.56-1.62.94l-2.39-.96a.5.5 0 0 0-.61.22L2.66 8.84a.5.5 0 0 0 .12.64l2.03 1.58a7.5 7.5 0 0 0 0 1.88l-2.03 1.58a.5.5 0 0 0-.12.64l1.92 3.32c.13.22.39.3.61.22l2.39-.96c.49.38 1.03.7 1.62.94l.36 2.54c.05.24.26.42.5.42h3.84c.25 0 .46-.18.5-.42l.36-2.54c.59-.24 1.13-.56 1.62-.94l2.39.96c.22.08.48 0 .61-.22l1.92-3.32a.5.5 0 0 0-.12-.64l-2.03-1.58zM12 15.6a3.6 3.6 0 1 1 0-7.2 3.6 3.6 0 0 1 0 7.2z",
  resumeAll: "M4 5h3v14H4zM9 5v14l11-7z",
  pauseAll: "M4 5h3v14H4zM10 5h3v14h-3zM16 5h3v14h-3z",
  schedule: "M11.99 2C6.47 2 2 6.48 2 12s4.47 10 9.99 10C17.52 22 22 17.52 22 12S17.52 2 11.99 2zM12 20c-4.42 0-8-3.58-8-8s3.58-8 8-8 8 3.58 8 8-3.58 8-8 8zm.5-13H11v6l5.25 3.15.75-1.23-4.5-2.67z",
  more: "M6 10a2 2 0 1 0 0 4 2 2 0 0 0 0-4zm12 0a2 2 0 1 0 0 4 2 2 0 0 0 0-4zm-6 0a2 2 0 1 0 0 4 2 2 0 0 0 0-4z",
  copy: "M16 1H4a2 2 0 0 0-2 2v14h2V3h12V1zm3 4H8a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2zm0 16H8V7h11v14z",
  verify: "M23 12l-2.44-2.79.34-3.69-3.61-.82-1.89-3.2L12 2.96 8.6 1.5 6.71 4.69 3.1 5.5l.34 3.7L1 12l2.44 2.79-.34 3.7 3.61.82L8.6 22.5l3.4-1.47 3.4 1.46 1.89-3.19 3.61-.82-.34-3.69L23 12zm-12.91 4.72l-3.8-3.81 1.48-1.48 2.32 2.33 5.85-5.87 1.48 1.48-7.33 7.35z",
  login: "M11 7 9.6 8.4l2.6 2.6H2v2h10.2l-2.6 2.6L11 17l5-5-5-5zm9 12h-8v2h8a2 2 0 0 0 2-2V5a2 2 0 0 0-2-2h-8v2h8v14z",
};
const svg = (name, size = 20) =>
  `<svg viewBox="0 0 24 24" width="${size}" height="${size}" aria-hidden="true"><path fill="currentColor" d="${P[name] || P.Other}"/></svg>`;
document.querySelectorAll(".tool[data-i]").forEach((b) => b.insertAdjacentHTML("afterbegin", svg(b.dataset.i, 19)));
document.querySelectorAll(".ni[data-i]").forEach((s) => { s.innerHTML = svg(s.dataset.i, 16); });

/** "Me at the zoo [jNQXAC9IVRw] 240p.mp4" -> "Me at the zoo 240p.mp4" (site IDs hidden). */
function displayName(j) {
  const raw = String(j.fileName || (j.file ? String(j.file).split(/[\\/]/).pop() : "") || j.title || j.url || "");
  return raw.replace(/\s*\[[^\]]{4,}\]/g, "").replace(/\s+/g, " ").replace(/\s+(\.[a-z0-9]{1,5})$/i, "$1").trim() || raw;
}
function size(b) {
  if (!b || b <= 0) return "";
  const mb = b / 1048576;
  return mb >= 1024 ? (mb / 1024).toFixed(1) + " GB" : mb >= 10 ? mb.toFixed(0) + " MB" : mb >= 1 ? mb.toFixed(1) + " MB" : Math.max(1, Math.round(b / 1024)) + " KB";
}
function ago(t) {
  if (!t) return "";
  const s = (Date.now() - t) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return Math.floor(s / 60) + " min ago";
  if (s < 86400) return Math.floor(s / 3600) + " h ago";
  if (s < 172800) return "yesterday";
  return new Date(t).toLocaleDateString(undefined, { day: "numeric", month: "short" });
}
// Speeds come as "4.2MiB/s" (videos) or "12.8 MB/s" (files): one way of writing them.
function bytesPerSec(s) {
  const m = String(s || "").match(/([\d.]+)\s*([KMGT]?)i?B\/s/i);
  if (!m) return 0;
  return (parseFloat(m[1]) || 0) * ({ "": 1, K: 1024, M: 1048576, G: 1073741824, T: 1099511627776 }[m[2].toUpperCase()] || 1);
}
function fmtSpeed(bps) {
  if (!bps) return "";
  return bps >= 1048576 ? (bps / 1048576).toFixed(bps >= 104857600 ? 0 : 1) + " MB/s" : Math.max(1, Math.round(bps / 1024)) + " KB/s";
}
// Time left comes as "01:12", "0:21" or "1m 12s": shown as "1 min 12 s".
function etaSeconds(s) {
  s = String(s || "").trim();
  if (!s || /unknown|^-/i.test(s)) return null;
  if (/^\d+(:\d+){1,2}$/.test(s)) return s.split(":").map(Number).reduce((a, v) => a * 60 + v, 0);
  let t = 0, m, found = false;
  const re = /(\d+)\s*(h|m|s)/gi;
  while ((m = re.exec(s))) { found = true; t += Number(m[1]) * { h: 3600, m: 60, s: 1 }[m[2].toLowerCase()]; }
  return found ? t : null;
}
function fmtDuration(sec) {
  if (sec == null || !isFinite(sec)) return "";
  sec = Math.max(0, Math.round(sec));
  if (sec < 60) return `${sec} s`;
  if (sec < 3600) return `${Math.floor(sec / 60)} min${sec % 60 ? ` ${sec % 60} s` : ""}`;
  const h = Math.floor(sec / 3600), mn = Math.floor((sec % 3600) / 60);
  return `${h} h${mn ? ` ${mn} min` : ""}`;
}
const ACTIVE = ["starting", "downloading"];
const WAITING = ["queued", "scheduled", "paused", "ask"];
const FINISHED = ["done", "error", "cancelled"];
const jobSize = (j) => j.totalBytes || j.size || 0;
const created = (j) => j.createdAt || j.queuedAt || 0;
const qualityNote = (q) => (q && !/^(Fast engine|Scheduled|Choose downloader|From website)$/i.test(q) ? q : "");
const needsLogin = (j) => j.status === "error" && j.kind === "video" && IN_EXTENSION && !j.useCookies &&
  /log ?in|sign ?in|cookies|private|members|age|confirm your|not available|unavailable|registered users/i.test(j.error || "");

// ---------- views ----------
const VIEWS = ["downloads", "convert", "captions", "voice", "transcripts", "grab", "settings"];
function showTab(name) {
  if (!VIEWS.includes(name)) name = "downloads";
  view = name;
  for (const v of VIEWS) $(v).hidden = v !== name;
  history.replaceState(null, "", "#" + name);
  if (name === "convert") window.fillDownloadsPicker?.(); // convert.js may still be loading
  if (name === "captions") { window.fillCaptionsPicker?.(); window.AnySaveCv?.session().catch(() => {}); }
  if (name === "voice") window.showVoiceLab?.();
  if (name === "transcripts") searchTranscripts($("txQuery").value);
  highlightNav();
}
function highlightNav() {
  document.querySelectorAll(".nav").forEach((n) => n.classList.toggle("on",
    view === "downloads" ? n.dataset.f === filter : n.dataset.view === view));
  $("openSettings").classList.toggle("on", view === "settings");
}
document.addEventListener("click", (e) => {
  const v = e.target.closest("[data-view]");
  if (v) { e.preventDefault(); showTab(v.dataset.view); return; }
  const f = e.target.closest(".nav[data-f]");
  if (f) { filter = f.dataset.f; selected.clear(); showTab("downloads"); render(); }
});

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

// ---------- messages ----------
let toastTimer = null;
function toast(text, isErr, ms = 5000) {
  const m = $("addMsg");
  m.hidden = false; m.className = "toast" + (isErr ? " err" : ""); m.textContent = text;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { m.hidden = true; }, isErr ? Math.max(ms, 7000) : ms);
}
const listMsg = toast;

// ---------- the downloads list ----------
let openRefreshJobId = null;
let openTranscribeJobId = null;
// Spoken languages offered for "Transcribe" (Bangla needs the language picked: auto-detect fails on it).
const TX_LANGS = [["en", "English"], ["bn", "বাংলা (Bangla)"], ["hi", "हिन्दी (Hindi)"], ["ur", "اردو (Urdu)"], ["ar", "العربية (Arabic)"],
  ["es", "Español"], ["fr", "Français"], ["de", "Deutsch"], ["pt", "Português"], ["ru", "Русский"], ["tr", "Türkçe"],
  ["id", "Indonesia"], ["zh", "中文 (Chinese)"], ["ja", "日本語 (Japanese)"], ["ko", "한국어 (Korean)"], ["auto", "Auto-detect"]];

function inFilter(j, f) {
  const c = category(j);
  switch (f) {
    case "all": return j.status !== "cancelled";
    case "active": return ACTIVE.includes(j.status);
    case "done": return j.status === "done";
    case "waiting": return WAITING.includes(j.status);
    case "failed": return j.status === "error" || j.status === "cancelled";
    case "Archives": return j.status === "done" && (c === "Archives" || c === "Apps");
    case "Other": return j.status === "done" && (c === "Other" || c === "Pictures");
    default:
      if (f.startsWith("q:")) return (j.queue || "") === f.slice(2) && j.status !== "cancelled";
      return j.status === "done" && c === f;
  }
}
const COUNTED = ["all", "active", "done", "Videos", "Music", "Documents", "Archives", "Converted", "Other", "waiting", "failed"];
const STATUS_ORDER = { downloading: 0, starting: 0, ask: 1, paused: 2, queued: 3, scheduled: 4, error: 5, cancelled: 6, done: 7 };

function sortJobs(list) {
  const dir = sortAsc ? 1 : -1;
  const by = {
    name: (a, b) => displayName(a).localeCompare(displayName(b), undefined, { numeric: true }) * dir,
    size: (a, b) => (jobSize(a) - jobSize(b)) * dir,
    status: (a, b) => (STATUS_ORDER[a.status] ?? 9) - (STATUS_ORDER[b.status] ?? 9) || created(b) - created(a),
    added: (a, b) => (created(a) - created(b)) * dir,
  }[sortKey];
  if (by) return list.sort(by);
  // Default: running and waiting ones on top, then newest first.
  return list.sort((a, b) => FINISHED.includes(a.status) - FINISHED.includes(b.status) || created(b) - created(a));
}

function render() {
  if (dragging) { redrawAfterDrag = render; return; }
  const all = Object.values(jobs);
  // Counts in the sidebar
  for (const k of COUNTED) {
    const el = document.querySelector(`[data-count="${k}"]`);
    if (el) { const n = all.filter((j) => inFilter(j, k)).length; el.textContent = n || ""; }
  }
  document.querySelectorAll("#queueChips [data-count]").forEach((el) => {
    const n = all.filter((j) => inFilter(j, el.dataset.count)).length; el.textContent = n || "";
  });
  updateSpeedStats(all);
  const q = query.toLowerCase();
  const shown = sortJobs(all.filter((j) => inFilter(j, filter) &&
    (!q || [displayName(j), j.title, j.site, j.url, j.queue].some((s) => String(s || "").toLowerCase().includes(q)))));
  shownIds = shown.map((j) => j.id);
  for (const id of [...selected]) if (!jobs[id]) selected.delete(id);

  const ul = $("list");
  const old = new Map([...ul.children].map((li) => [li.dataset.id, li]));
  let prev = null;
  for (const j of shown) {
    let li = old.get(j.id);
    old.delete(j.id);
    if (!li) { li = document.createElement("li"); li.className = "item"; li.dataset.id = j.id; li.setAttribute("role", "option"); }
    const html = rowHtml(j);
    if (li.dataset.h !== html) { li.innerHTML = html; li.dataset.h = html; }
    const sel = selected.has(j.id);
    li.classList.toggle("sel", sel);
    li.setAttribute("aria-selected", String(sel));
    const draggable = j.status === "done" && !!(j.file || j.downloadId);
    li.draggable = draggable;
    li.classList.toggle("drag", draggable);
    li.title = draggable ? "Double-click to open, or drag the file anywhere" : "";
    if ((prev ? prev.nextSibling : ul.firstChild) !== li) ul.insertBefore(li, prev ? prev.nextSibling : ul.firstChild);
    prev = li;
  }
  for (const li of old.values()) li.remove();

  const empty = shown.length === 0;
  $("empty").hidden = !empty;
  if (empty) {
    const anyJobs = all.length > 0;
    $("emptyTitle").textContent = q ? `Nothing matches "${query}"` : anyJobs ? "Nothing in this list" : "Nothing here yet";
    $("emptyText").innerHTML = q || anyJobs ? "Try another list on the left, or clear the search."
      : "Paste a link above, drop one onto this window, or right-click a link or video on any page and choose <b>Download with AnySave</b>.";
  }
  document.querySelectorAll(".th[data-sort]").forEach((th) => {
    th.classList.toggle("sorted", th.dataset.sort === sortKey);
    th.classList.toggle("asc", th.dataset.sort === sortKey && sortAsc);
  });
  updateToolbar();
  renderDetail();
}

const qa = (act, icon, title) => `<button class="ib" data-act="${act}" title="${esc(title)}" aria-label="${esc(title)}">${svg(icon, 17)}</button>`;

function progressBar(j, cls) {
  const pct = Math.max(0, Math.min(100, j.percent || 0));
  if (Array.isArray(j.segments) && j.segments.length > 1 && j.totalBytes > 0) {
    const slices = j.segments.map((s) => {
      const left = Math.max(0, Math.min(100, (s.start / j.totalBytes) * 100));
      const w = Math.max(0, Math.min(100 - left, ((s.current - s.start) / j.totalBytes) * 100));
      return `<i style="left:${left.toFixed(2)}%;width:${w.toFixed(2)}%"></i>`;
    }).join("");
    return `<div class="pbar seg ${cls}" title="${j.segments.length} connections">${slices}</div>`;
  }
  return `<div class="pbar ${cls}"><i style="width:${pct}%"></i></div>`;
}

function rowHtml(j) {
  const cat = category(j);
  const pct = Math.max(0, Math.min(100, j.percent || 0)).toFixed(0);
  const running = ACTIVE.includes(j.status);
  const ext = extOf(j.fileName || j.file || (j.kind === "file" ? j.title : "")) || (j.audioOnly ? "mp3" : j.kind === "video" ? "mp4" : "");
  const badge = ext ? esc(ext.toUpperCase().slice(0, 4)) : svg(cat, 16);
  const name = displayName(j);
  const sub = [j.site, qualityNote(j.qualityLabel), j.queue && `${j.queue} queue`,
    running && Array.isArray(j.segments) && j.segments.length > 1 && `${j.segments.length} connections`].filter(Boolean).join(" · ");
  let status = "", speed = "", eta = "", acts = "";
  switch (j.status) {
    case "starting":
      status = `<div class="pbar indet"><i></i></div><div class="stx">Starting…</div>`;
      acts = (j.kind !== "convert" ? qa("pause", "pause", "Pause") : "") + qa("cancel", "close", "Cancel");
      break;
    case "downloading": {
      const prog = j.totalBytes > 0 && j.doneBytes ? ` · ${size(j.doneBytes)} of ${size(j.totalBytes)}` : "";
      status = `${progressBar(j, "")}<div class="stx">${pct}%${prog}</div>`;
      speed = fmtSpeed(bytesPerSec(j.speed));
      eta = fmtDuration(etaSeconds(j.eta));
      acts = (j.kind !== "convert" ? qa("pause", "pause", "Pause") : "") + qa("cancel", "close", "Cancel");
      break;
    }
    case "paused":
      status = `${progressBar(j, "paused")}<div class="stx"><span class="pill warn">Paused</span> · ${pct}%</div>`;
      acts = qa("resume", "play", "Resume") + qa("cancel", "close", "Cancel");
      break;
    case "scheduled":
      status = `<span class="pill wait">Scheduled · starts at ${esc(currentSettings.scheduleStart || "the set time")}</span>`;
      acts = qa("startNow", "play", "Start now") + qa("cancel", "close", "Cancel");
      break;
    case "queued":
      status = `<span class="pill wait">Queued · starts when a download finishes</span>`;
      acts = qa("pause", "pause", "Pause") + qa("cancel", "close", "Cancel");
      break;
    case "ask":
      status = `<div class="choose"><span class="lbl">Download with:</span><button class="mini p" data-act="useAnysave">AnySave (fast)</button><button class="mini" data-act="useChrome">Chrome</button></div>`;
      acts = qa("cancel", "close", "Cancel");
      break;
    case "done":
      status = `<span class="pill ok">Done</span>`;
      acts = (j.file || j.downloadId ? qa("open", "open", "Open") + qa("reveal", "folder", "Show in folder") : "") + qa("remove", "remove", "Remove from list");
      break;
    case "error":
      status = `<span class="pill err" title="${esc(j.error || "Failed")}">${esc(j.error || "Failed")}</span>` +
        (needsLogin(j) ? `<div><button class="mini fix" data-act="loginRetry">Use my Chrome login &amp; retry</button></div>` : "");
      acts = qa("retry", "retry", "Retry") + qa("remove", "remove", "Remove from list");
      break;
    default:
      status = `<span class="pill wait">Cancelled</span>`;
      acts = qa("retry", "retry", "Retry") + qa("remove", "remove", "Remove from list");
  }
  const sz = size(jobSize(j));
  return `<div class="c-name"><span class="ficon t-${cat}">${badge}${j.status === "done" ? `<span class="tick">${svg("check", 11)}</span>` : ""}</span>
      <div class="nm"><div class="name" title="${esc(j.fileName || j.title || j.url)}">${esc(name)}</div>${sub ? `<div class="sub">${esc(sub)}</div>` : ""}</div></div>
    <div class="c-size${sz ? "" : " dim"}">${sz || "—"}</div>
    <div class="c-status">${status}</div>
    <div class="c-speed${speed ? "" : " dim"}">${speed || (running ? "…" : "")}</div>
    <div class="c-eta${eta ? "" : " dim"}">${eta}</div>
    <div class="c-act"><span class="when">${esc(ago(j.status === "done" ? j.finishedAt || created(j) : created(j)))}</span><span class="qa">${acts}</span></div>`;
}

// One action on one download (row buttons, details buttons, toolbar).
async function act(j, a) {
  switch (a) {
    case "pause": return send({ type: "pause", id: j.id });
    case "resume": return send({ type: "resume", id: j.id });
    case "startNow": return send({ type: "startNow", id: j.id });
    case "cancel": return send({ type: "cancel", id: j.id });
    case "retry": return send({ type: "retry", id: j.id });
    case "loginRetry": return send({ type: "retry", id: j.id, useCookies: true });
    case "remove": selected.delete(j.id); return send({ type: "remove", id: j.id });
    case "useAnysave": return send({ type: "chooseHandler", id: j.id, handler: "anysave" });
    case "useChrome": return send({ type: "chooseHandler", id: j.id, handler: "chrome" });
    case "open": return openDownload(j).then((r) => { if (r && !r.ok) toast(r.error || "Couldn't open the file", true); });
    case "reveal": return send({ type: "reveal", id: j.id });
    case "copyLink":
      try { await navigator.clipboard.writeText(j.url); toast("Link copied."); } catch { prompt("The link:", j.url); }
      return;
    case "verify": return verifyChecksum(j);
    case "transcribe": openTranscribeJobId = openTranscribeJobId === j.id ? null : j.id; return renderDetail(true);
    case "refresh": openRefreshJobId = openRefreshJobId === j.id ? null : j.id; return renderDetail(true);
    case "convert": showTab("convert"); return window.choose?.({ path: j.file, name: j.fileName || j.title });
    case "captions": return window.openCaptions?.(j.kind === "transcribe" && j.url ? j.url : j.file);
    case "voice": return window.openVoiceLab?.(j.file);
    case "embedSubs": case "burnSubs": {
      const name = String(j.url).split(/[\\/]/).pop();
      const r = await send({ type: "subs", video: j.url, srt: j.file, lang: j.lang, mode: a === "embedSubs" ? "embed" : "burn", name });
      if (!r?.ok) toast(r?.error || "Couldn't start.", true);
      return;
    }
  }
}

// Row clicks: select (Ctrl/⌘ adds, Shift picks a range), buttons act, double-click opens.
$("list").addEventListener("click", (e) => {
  const li = e.target.closest(".item");
  if (!li) return;
  const j = jobs[li.dataset.id];
  if (!j) return;
  const b = e.target.closest("[data-act]");
  if (b) { e.stopPropagation(); act(j, b.dataset.act); return; }
  if (e.shiftKey && anchorId && shownIds.includes(anchorId)) {
    const [a, z] = [shownIds.indexOf(anchorId), shownIds.indexOf(j.id)].sort((x, y) => x - y);
    if (!(e.metaKey || e.ctrlKey)) selected.clear();
    shownIds.slice(a, z + 1).forEach((id) => selected.add(id));
  } else if (e.metaKey || e.ctrlKey) {
    selected.has(j.id) ? selected.delete(j.id) : selected.add(j.id);
    anchorId = j.id;
  } else {
    selected = new Set([j.id]);
    anchorId = j.id;
  }
  render();
});
$("list").addEventListener("dblclick", (e) => {
  const li = e.target.closest(".item");
  if (!li || e.target.closest("button")) return;
  const j = jobs[li.dataset.id];
  if (!j) return;
  if (j.status === "done") act(j, "open");
  else if (j.status === "paused") act(j, "resume");
  else if (j.status === "scheduled") act(j, "startNow");
});
$("list").addEventListener("dragstart", (e) => {
  const li = e.target.closest(".item");
  const j = li && jobs[li.dataset.id];
  if (j && j.status === "done") startDrag(e, j);
});
// Clicking the empty part of the list clears the selection.
$("table").addEventListener("click", (e) => {
  if (!e.target.closest(".item") && !e.target.closest(".thead")) { selected.clear(); render(); }
});
document.querySelectorAll(".th[data-sort]").forEach((th) => th.addEventListener("click", () => {
  if (sortKey !== th.dataset.sort) { sortKey = th.dataset.sort; sortAsc = th.dataset.sort === "name"; }
  else if (sortAsc === (th.dataset.sort === "name")) sortAsc = !sortAsc;
  else { sortKey = ""; sortAsc = false; } // third click: back to the normal order
  render();
}));
$("search").addEventListener("input", (e) => { query = e.target.value.trim(); render(); });

// ---------- details of the selected download ----------
function renderDetail(force) {
  const box = $("detail");
  const j = selected.size === 1 ? jobs[[...selected][0]] : null;
  if (!j || !shownIds.includes(j.id)) { box.hidden = true; box.innerHTML = ""; box.dataset.h = ""; return; }
  // Don't redraw under someone typing in it.
  if (!force && document.activeElement && box.contains(document.activeElement) && document.activeElement.matches("input, select")) return;
  const running = ACTIVE.includes(j.status);
  const paused = j.status === "paused";
  const waiting = WAITING.includes(j.status);
  const cat = category(j);
  const fileName = j.file ? String(j.file) : "";
  const rows = [
    ["Status", { starting: "Starting…", downloading: `Downloading · ${Math.round(j.percent || 0)}%`, paused: `Paused · ${Math.round(j.percent || 0)}%`,
      queued: "Queued", scheduled: `Scheduled · starts at ${currentSettings.scheduleStart || "the set time"}`, ask: "Waiting for you to choose who downloads it",
      done: "Done", error: "Failed", cancelled: "Cancelled" }[j.status] || j.status],
    j.status === "error" && ["Problem", `<span class="err">${esc(j.error || "Unknown error")}</span>`, true],
    j.url && /^https?:/i.test(j.url) && ["Address", j.url],
    j.kind !== "file" && j.url && !/^https?:/i.test(j.url) && ["From", j.url],
    fileName && ["Saved as", fileName],
    !fileName && ["Saving to", j.kind === "convert" ? "Downloads/AnySave/Converted" : "Downloads/AnySave"],
    jobSize(j) && ["Size", size(jobSize(j))],
    qualityNote(j.qualityLabel) && ["Quality", j.qualityLabel],
    j.site && ["Site", j.site],
    ["Queue", j.queue || "Main queue"],
    created(j) && ["Added", new Date(created(j)).toLocaleString()],
    j.finishedAt && j.status === "done" && ["Finished", new Date(j.finishedAt).toLocaleString()],
    (j.expectedSha256 || j.sha256) && ["SHA-256", j.sha256 || j.expectedSha256],
  ].filter(Boolean);
  const kv = rows.map(([k, v, html]) => `<span>${esc(k)}</span><span title="${html ? "" : esc(v)}">${html ? v : esc(v)}</span>`).join("");

  let side = "";
  if (Array.isArray(j.segments) && j.segments.length > 1 && j.totalBytes > 0 && (running || paused)) {
    const segs = j.segments.map((s) => {
      const len = Math.max(1, s.end - s.start);
      return `<div title="${size(s.current - s.start)} of ${size(len)}"><i style="width:${Math.min(100, ((s.current - s.start) / len) * 100).toFixed(1)}%"></i></div>`;
    }).join("");
    side += `<h4>${j.segments.length} connections</h4><div class="segs">${segs}</div>`;
  }
  if (running) {
    const sp = fmtSpeed(bytesPerSec(j.speed));
    side += `<div class="kv">${sp ? `<span>Speed</span><span>${sp}</span>` : ""}${j.totalBytes ? `<span>Downloaded</span><span>${size(j.doneBytes || 0)} of ${size(j.totalBytes)}</span>` : ""}${j.eta ? `<span>Time left</span><span>${esc(fmtDuration(etaSeconds(j.eta)) || j.eta)}</span>` : ""}</div>`;
  }

  const B = (a, icon, label, title) => `<button class="btn" data-act="${a}" title="${esc(title || label)}">${svg(icon, 15)}${esc(label)}</button>`;
  let buttons = "";
  if (j.status === "done" && (j.file || j.downloadId)) buttons += B("open", "open", "Open") + B("reveal", "folder", "Show in folder");
  if (running && j.kind !== "convert") buttons += B("pause", "pause", "Pause");
  if (paused) buttons += B("resume", "play", "Resume");
  if (j.status === "scheduled") buttons += B("startNow", "play", "Start now");
  if (needsLogin(j)) buttons += B("loginRetry", "login", "Use my Chrome login & retry", "Try again using your Chrome login for this site");
  if (j.status === "error" || j.status === "cancelled") buttons += B("retry", "retry", "Retry");
  if (j.url && /^https?:/i.test(j.url)) buttons += B("copyLink", "copy", "Copy link");
  if (paused || j.status === "error") buttons += B("refresh", "link", "Refresh expired link", "Paste a fresh link and continue from the parts already saved");
  if (j.status === "done" && j.file) {
    if (j.kind === "transcribe" && /\.srt$/i.test(j.file) && j.url) {
      buttons += B("embedSubs", "cc", "Put subtitles in the video", "Put the subtitles inside the video (viewers can switch them on or off; takes seconds)");
      buttons += B("burnSubs", "burn", "Draw captions on the video", "Draw the captions onto the video (always visible, like social media videos)");
      buttons += B("captions", "captions", "Edit captions", "Fix and style these captions with a live preview");
    }
    if (["Videos", "Music"].includes(cat)) {
      buttons += B("transcribe", "text", "Transcribe", "Transcribe: turn the speech into text and subtitles");
      buttons += B("convert", "convert", "Convert");
      if (cat === "Videos") buttons += B("captions", "captions", "Edit captions", "Make, fix and style captions, then save them or draw them on the video");
      buttons += B("voice", "voice", "Clean up voice", "Take out background noise and even out the loudness (Voice Lab)");
    }
    if (j.kind !== "transcribe") buttons += B("verify", "verify", "Check SHA-256", "Check against a SHA-256 checksum");
  }
  if (FINISHED.includes(j.status)) buttons += B("remove", "remove", "Remove from list");
  else buttons += B("cancel", "close", "Cancel download");
  let queueSel = "";
  if ((waiting || paused) && j.status !== "ask" && queueNames().length) {
    queueSel = `<label class="btn" title="Move to another queue">Queue <select class="row-queue">${["", ...queueNames()].map((q) =>
      `<option value="${esc(q)}"${(j.queue || "") === q ? " selected" : ""}>${esc(q || "Main queue")}</option>`).join("")}</select></label>`;
  }

  let inline = "";
  if (openRefreshJobId === j.id && (paused || j.status === "error")) {
    inline = `<div class="inline-box refresh-box">
      <input type="url" class="refresh-input" placeholder="Paste a fresh link to continue from the saved parts…" value="${esc(j.url || "")}" />
      <button type="button" class="btn primary refresh-go">Continue</button>
      <div class="note">Links from some sites stop working after a while. Get a new one from the same page and paste it here: nothing already downloaded is lost.</div></div>`;
  } else if (openTranscribeJobId === j.id && j.status === "done") {
    const last = currentSettings.transcribeLang || "en";
    inline = `<div class="inline-box transcribe-box">
      <label class="small">Spoken language
        <select class="tx-lang">${TX_LANGS.map(([c, n]) => `<option value="${c}"${c === last ? " selected" : ""}>${esc(n)}</option>`).join("")}</select></label>
      <button type="button" class="btn primary tx-go">Transcribe</button>
      <div class="note tx-note"></div></div>`;
  }

  const html = `<div><h4><span class="ficon t-${cat}">${esc((extOf(j.fileName || j.file) || "").toUpperCase().slice(0, 4)) || svg(cat, 14)}</span><span>${esc(displayName(j))}</span></h4>
      <div class="kv">${kv}</div></div>
    <div>${side}</div>
    <div class="dact">${buttons}${queueSel}</div>${inline}`;
  if (!force && box.dataset.h === html) return;
  box.innerHTML = html;
  box.dataset.h = html;
  box.hidden = false;

  box.querySelectorAll("[data-act]").forEach((b) => { b.onclick = () => act(j, b.dataset.act); });
  const qs = box.querySelector(".row-queue");
  if (qs) qs.onchange = () => send({ type: "moveQueue", id: j.id, queue: qs.value });
  const inp = box.querySelector(".refresh-input");
  if (inp) {
    const go = async () => {
      const u = inp.value.trim();
      if (!u) return;
      openRefreshJobId = null;
      const r = await send({ type: "refreshUrl", id: j.id, url: u });
      if (r && !r.ok) toast(r.error || "Couldn't use that link.", true);
      renderDetail(true);
    };
    box.querySelector(".refresh-go").onclick = go;
    inp.onkeydown = (ev) => { if (ev.key === "Enter") { ev.preventDefault(); go(); } };
    inp.focus(); inp.select();
  }
  const sel = box.querySelector(".tx-lang");
  if (sel) {
    const note = () => {
      box.querySelector(".tx-note").textContent = sel.value === "en"
        ? "Fast. The first time, a 252 MB speech model is downloaded once."
        : sel.value === "auto"
          ? "Auto-detect often mistakes Bangla for another language: pick the language if you know it. The first time, a 547 MB speech model is downloaded once."
          : "The first time, a 547 MB speech model is downloaded once. Everything runs on this computer; nothing is uploaded.";
    };
    note();
    sel.onchange = note;
    box.querySelector(".tx-go").onclick = async () => {
      openTranscribeJobId = null;
      const res = await send({ type: "transcribe", path: j.file, name: j.fileName || j.title, lang: sel.value });
      if (!res?.ok) toast(res?.error || "Couldn't start the transcription.", true);
      else toast("Transcribing… it shows in the list.");
      renderDetail(true);
    };
  }
}

// ---------- toolbar ----------
const selJobs = () => [...selected].map((id) => jobs[id]).filter(Boolean);
const canResume = (j) => ["paused", "scheduled"].includes(j.status);
const canPause = (j) => [...ACTIVE, "queued", "scheduled"].includes(j.status) && j.kind !== "convert";
const canRetry = (j) => ["error", "cancelled"].includes(j.status);
function updateToolbar() {
  const s = selJobs();
  $("tbResume").disabled = !s.some((j) => canResume(j) || canRetry(j));
  $("tbPause").disabled = !s.some(canPause);
  $("tbRemove").disabled = !s.length;
  $("tbRetry").disabled = !s.some(canRetry);
  $("tbShow").disabled = !(s.length === 1 && s[0].status === "done" && (s[0].file || s[0].downloadId));
  const all = Object.values(jobs);
  $("resumeAll").disabled = !all.some((j) => inShownQueue(j) && j.status === "paused");
  $("pauseAll").disabled = !all.some((j) => inShownQueue(j) && canPause(j));
  $("clearDone").disabled = !all.some((j) => FINISHED.includes(j.status));
}
$("tbResume").onclick = () => selJobs().forEach((j) => {
  if (j.status === "paused") act(j, "resume");
  else if (j.status === "scheduled") act(j, "startNow");
  else if (canRetry(j)) act(j, "retry");
});
$("tbPause").onclick = () => selJobs().filter(canPause).forEach((j) => act(j, "pause"));
$("tbRetry").onclick = () => selJobs().filter(canRetry).forEach((j) => act(j, "retry"));
$("tbShow").onclick = () => { const [j] = selJobs(); if (j) act(j, "reveal"); };
$("tbRemove").onclick = () => removeSelected();
async function removeSelected() {
  const s = selJobs();
  if (!s.length) return;
  const unfinished = s.filter((j) => !FINISHED.includes(j.status));
  if (unfinished.length && !confirm(unfinished.length === s.length && s.length === 1
    ? "Stop this download and remove it from the list?"
    : `Remove ${s.length} downloads from the list? ${unfinished.length} of them ${unfinished.length === 1 ? "is" : "are"} not finished and will stop.\n\nFiles already saved stay on your computer.`)) return;
  for (const j of unfinished) await send({ type: "cancel", id: j.id });
  for (const j of s) await send({ type: "remove", id: j.id });
  selected.clear();
  render();
}
// With a queue selected on the left, Pause all / Resume all act on that queue only.
const inShownQueue = (j) => !filter.startsWith("q:") || (j.queue || "") === filter.slice(2);
$("pauseAll").onclick = () => Object.values(jobs).filter((j) => inShownQueue(j) && canPause(j)).forEach((j) => send({ type: "pause", id: j.id }));
$("resumeAll").onclick = () => Object.values(jobs).filter((j) => inShownQueue(j) && j.status === "paused").forEach((j) => send({ type: "resume", id: j.id }));
$("clearDone").onclick = () => send({ type: "clear" });
$("tbScheduler").onclick = () => { showTab("settings"); $("scheduleCard").scrollIntoView({ behavior: "smooth", block: "start" }); };
// "More" menu
function closeMenu() { $("moreMenu").hidden = true; $("tbMore").setAttribute("aria-expanded", "false"); }
$("tbMore").onclick = (e) => {
  e.stopPropagation();
  const open = $("moreMenu").hidden;
  $("moreMenu").hidden = !open;
  $("tbMore").setAttribute("aria-expanded", String(open));
};
document.addEventListener("click", (e) => { if (!e.target.closest(".menu-wrap")) closeMenu(); });
$("moreMenu").addEventListener("click", () => setTimeout(closeMenu, 0));
$("selectAll").onclick = () => { selected = new Set(shownIds); render(); };

// ---------- keyboard ----------
document.addEventListener("keydown", (e) => {
  if (e.target.closest("input, textarea, select, dialog") || view !== "downloads") {
    if (e.key === "Escape" && e.target.id === "search") { e.target.value = ""; query = ""; render(); e.target.blur(); }
    return;
  }
  const mod = e.metaKey || e.ctrlKey;
  const s = selJobs();
  if (mod && e.key.toLowerCase() === "a") { e.preventDefault(); selected = new Set(shownIds); render(); }
  else if (e.key === "Escape") { selected.clear(); closeMenu(); render(); }
  else if (e.key === "/") { e.preventDefault(); $("search").focus(); }
  else if ((e.key === "Delete" || e.key === "Backspace") && s.length) { e.preventDefault(); removeSelected(); }
  else if (e.key === "Enter" && s.length === 1 && s[0].status === "done") act(s[0], "open");
  else if (e.key === " " && s.length) {
    e.preventDefault();
    if (s.some(canPause)) s.filter(canPause).forEach((j) => act(j, "pause"));
    else s.forEach((j) => { if (canResume(j)) act(j, j.status === "scheduled" ? "startNow" : "resume"); });
  } else if ((e.key === "ArrowDown" || e.key === "ArrowUp") && shownIds.length) {
    e.preventDefault();
    const cur = anchorId && shownIds.includes(anchorId) ? shownIds.indexOf(anchorId) : -1;
    const next = Math.max(0, Math.min(shownIds.length - 1, cur + (e.key === "ArrowDown" ? 1 : -1)));
    anchorId = shownIds[next];
    if (e.shiftKey) selected.add(anchorId); else selected = new Set([anchorId]);
    render();
    document.querySelector(`.item[data-id="${CSS.escape(anchorId)}"]`)?.scrollIntoView({ block: "nearest" });
  }
});
// Paste anywhere on the page (not in a text box): download the copied link.
document.addEventListener("paste", (e) => {
  if (e.target.closest("input, textarea, select, dialog")) return;
  const text = e.clipboardData?.getData("text") || "";
  const found = linksIn(text);
  if (!found.length) return;
  e.preventDefault();
  $("addUrl").value = found.join(" ");
  $("addUrl").dispatchEvent(new Event("input"));
  $("addForm").requestSubmit();
});

// ---------- status bar ----------
const speedSamples = [];
function updateSpeedStats(all) {
  const active = all.filter((j) => j.status === "downloading");
  const waiting = all.filter((j) => WAITING.includes(j.status)).length;
  const dayAgo = Date.now() - 86400000;
  const today = all.filter((j) => j.status === "done" && (j.finishedAt || 0) > dayAgo).length;
  const bps = active.reduce((acc, j) => acc + bytesPerSec(j.speed), 0);
  speedSamples.push(bps);
  if (speedSamples.length > 40) speedSamples.shift();
  $("liveSpeedText").textContent = active.length ? fmtSpeed(bps) || "…" : "Ready";
  const parts = [];
  if (active.length) parts.push(`<b>${active.length}</b> downloading`);
  if (waiting) parts.push(`<b>${waiting}</b> waiting`);
  if (today) parts.push(`<b>${today}</b> finished today`);
  $("liveStatsMeta").innerHTML = parts.join(" · ") || "No downloads running";
  const path = $("sparkPath");
  const maxV = Math.max(1024, ...speedSamples);
  path.setAttribute("d", speedSamples.length < 2 ? "" : speedSamples.map((v, i) =>
    `${i ? "L" : "M"}${((i / (speedSamples.length - 1)) * 136 + 2).toFixed(1)},${(26 - (v / maxV) * 22).toFixed(1)}`).join(" "));
}
// While something downloads the speed graph keeps moving even between progress updates.
setInterval(() => { if (Object.values(jobs).some((j) => j.status === "downloading")) updateSpeedStats(Object.values(jobs)); }, 1000);

// Compare a finished file with a checksum the user pastes (from the site they downloaded from).
async function verifyChecksum(j) {
  const typed = prompt(`Paste the SHA-256 checksum for\n${j.fileName || j.title}\n\n(leave empty to just see this file's checksum)`, "");
  if (typed === null) return;
  const want = typed.replace(/^\s*sha-?256\s*[:=]?\s*/i, "").trim().toLowerCase();
  if (want && !/^[0-9a-f]{64}$/.test(want)) { alert("That isn't a SHA-256 checksum. It has 64 characters: the digits 0-9 and letters a-f."); return; }
  const res = await send({ type: "hashFile", id: j.id });
  if (!res?.ok) { alert(res?.error || "Couldn't read the file."); return; }
  if (!want) { prompt("SHA-256 of this file:", res.sha256); return; }
  alert(res.sha256 === want
    ? "✓ The checksum matches. The file is exactly the one the site published."
    : `✗ The checksum does NOT match.\n\nThis file:  ${res.sha256}\nExpected:  ${want}\n\nThe file may be damaged or changed. Download it again.`);
}

// ---------- export / import the list of links ----------
function saveTextFile(name, text) {
  const url = URL.createObjectURL(new Blob([text], { type: "application/json" }));
  const done = () => setTimeout(() => URL.revokeObjectURL(url), 30000);
  // In the extension Chrome saves it (and asks where); in the desktop app a normal link does.
  if (typeof chrome !== "undefined" && chrome.downloads?.download) {
    chrome.downloads.download({ url, filename: name, saveAs: true }).then(done, done);
  } else {
    const a = Object.assign(document.createElement("a"), { href: url, download: name });
    document.body.appendChild(a); a.click(); a.remove(); done();
  }
}
function exportItems() {
  return Object.values(jobs)
    .filter((j) => /^https?:\/\//i.test(j.url || "") && j.kind !== "convert")
    .sort((a, b) => (a.createdAt || 0) - (b.createdAt || 0))
    .map((j) => ({ url: j.url, title: j.title || "", kind: j.kind, status: j.status,
      ...(j.audioOnly ? { audioOnly: true } : {}), ...(j.format ? { format: j.format } : {}), ...(j.sort ? { sort: j.sort } : {}),
      ...(j.expectedSha256 || j.sha256 ? { sha256: j.expectedSha256 || j.sha256 } : {}) }));
}
// Reads an exported list, or any text file with links in it.
function parseImport(text) {
  try {
    const data = JSON.parse(text);
    const items = Array.isArray(data) ? data : data?.items;
    if (Array.isArray(items)) {
      return items.map((it) => (typeof it === "string" ? { url: it } : it))
        .filter((it) => it && /^https?:\/\//i.test(it.url || ""));
    }
  } catch {}
  return linksIn(text).map((url) => ({ url }));
}
$("exportList").addEventListener("click", () => {
  const items = exportItems();
  if (!items.length) { listMsg("There are no links to export yet.", true); return; }
  saveTextFile(`anysave-links-${new Date().toISOString().slice(0, 10)}.json`,
    JSON.stringify({ app: "AnySave", version: 1, exportedAt: new Date().toISOString(), items }, null, 2));
  listMsg(`Exported ${items.length} link${items.length === 1 ? "" : "s"}.`);
});
$("importList").addEventListener("click", () => $("importFile").click());
$("importFile").addEventListener("change", async (e) => {
  const file = e.target.files?.[0];
  if (!file) return;
  const text = await file.text();
  e.target.value = "";
  const items = parseImport(text);
  if (!items.length) { listMsg("No links found in that file.", true); return; }
  // Links already waiting or downloading aren't added twice.
  const busy = new Set(Object.values(jobs).filter((j) => !FINISHED.includes(j.status)).map((j) => j.url));
  let ok = 0, skipped = 0, failed = 0;
  for (const it of items.slice(0, 500)) {
    if (busy.has(it.url)) { skipped++; continue; }
    busy.add(it.url);
    let res;
    if (it.kind === "video" && it.format) res = await send({ type: "download", url: it.url, title: it.title, format: it.format, sort: it.sort, audioOnly: !!it.audioOnly });
    else if (it.kind === "file") res = await send({ type: "file", url: it.url, title: it.title || undefined });
    else res = await send({ type: "addLink", url: it.url, title: it.title || undefined });
    if (res?.ok) ok++; else failed++;
  }
  listMsg(`Imported ${ok} link${ok === 1 ? "" : "s"}` + (skipped ? `, ${skipped} already in the list` : "") + (failed ? `, ${failed} couldn't be added` : "") + ".", !ok);
});

// ---------- adding links ----------
// Expand numeric range patterns like https://example.com/file[01-10].zip into 10 URLs.
function expandUrlPattern(u) {
  const m = String(u).match(/^(https?:\/\/.*)\[(\d{1,5})-(\d{1,5})\](.*)$/i);
  if (!m) return [u];
  const [, prefix, startStr, endStr, suffix] = m;
  const start = parseInt(startStr, 10);
  const end = parseInt(endStr, 10);
  if (isNaN(start) || isNaN(end) || end < start || end - start > 200) return [u];
  const pad = startStr.startsWith("0") ? startStr.length : 0;
  const out = [];
  for (let i = start; i <= end; i++) {
    const num = pad ? String(i).padStart(pad, "0") : String(i);
    out.push(`${prefix}${num}${suffix}`);
  }
  return out;
}
// Paste one link or many (one per line, separated by spaces, or a pattern like file[01-05].zip): each is downloaded.
// A one-line box drops line breaks when pasting, so links are split where "http" starts.
function linksIn(text) {
  const rawLinks = [...new Set((String(text).match(/https?:\/\/.+?(?=https?:\/\/|\s|$)/g) || []).map((s) => s.replace(/[),.;]+$/, "")))];
  const expanded = [];
  for (const u of rawLinks) {
    for (const item of expandUrlPattern(u)) expanded.push(item);
  }
  return [...new Set(expanded)];
}
$("addUrl").addEventListener("paste", (e) => {
  const text = e.clipboardData?.getData("text") || "";
  if (!/\s/.test(text.trim())) return; // a single link: paste as usual
  e.preventDefault();
  $("addUrl").value = linksIn(text).join(" ");
  $("addUrl").dispatchEvent(new Event("input"));
});
$("addUrl").addEventListener("input", () => {
  const n = linksIn($("addUrl").value).length;
  $("addGo").textContent = n > 1 ? `Download ${n} links` : "Download";
});
let submitForceHandler = undefined;
$("addGo").addEventListener("click", (e) => {
  submitForceHandler = e.altKey ? "chrome" : (e.ctrlKey || e.metaKey) ? "anysave" : undefined;
});
$("addForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const forceHandler = submitForceHandler;
  submitForceHandler = undefined;
  let raw = $("addUrl").value.trim();
  if (!raw) return;
  // A SHA-256 checksum pasted after a link: AnySave checks the finished file against it.
  const shaMatch = raw.match(/(?:^|[\s,;])(?:sha-?256\s*[:=]?\s*)?([0-9a-f]{64})(?=$|[\s,;])/i);
  const sha256 = shaMatch ? shaMatch[1].toLowerCase() : "";
  if (shaMatch) raw = raw.replace(shaMatch[0], " ").trim();
  const links = linksIn(raw);
  const list = links.length ? links : [raw];
  if (sha256 && list.length !== 1) { toast("A checksum belongs to one file: paste one link with its SHA-256.", true); return; }
  // One ordinary link: show what it is first (quality, size, name), unless that's turned off.
  if (list.length === 1 && !sha256 && /^https?:\/\//i.test(list[0]) && currentSettings.askBeforeAdd !== false) {
    $("addUrl").value = "";
    $("addGo").textContent = "Download";
    askAndAdd(list[0], forceHandler);
    return;
  }
  const dupCount = list.filter((u) => Object.values(jobs).some((j) => j.url === u && j.status === "done")).length;
  const btn = $("addGo"); btn.disabled = true; btn.textContent = list.length > 1 ? `Adding ${list.length}…` : "Checking…";
  let ok = 0, lastErr = "";
  for (const url of list) {
    // Only AnySave's own engine can check a checksum, so that download doesn't go to Chrome.
    const res = sha256 && /^https?:\/\//i.test(url)
      ? await send({ type: "file", url, sha256, forceHandler: "anysave", queue: addQueue() })
      : await send({ type: "addLink", url, forceHandler, queue: addQueue() });
    if (res?.ok) ok++; else lastErr = res?.error || "Couldn't add this link";
  }
  btn.disabled = false; btn.textContent = "Download";
  const dupNote = dupCount > 0 ? ` (${dupCount === 1 ? "This link was" : `${dupCount} links were`} downloaded before: saving a fresh copy.)` : "";
  toast((list.length === 1
    ? (ok ? (sha256 ? "Added. The finished file will be checked against your checksum." : "Added to your downloads.") : lastErr)
    : `Added ${ok} of ${list.length} links.` + (ok < list.length ? ` (${lastErr})` : "")) + dupNote, !ok);
  if (ok) { $("addUrl").value = ""; showDownloadsFor(); }
});
// After adding, make sure the new download is visible.
function showDownloadsFor() {
  if (view !== "downloads") showTab("downloads");
  if (!["all", "active", "waiting"].includes(filter) && !filter.startsWith("q:")) { filter = "all"; highlightNav(); render(); }
}

// ---------- before you download ----------
let dlgSeq = 0;
let dlgChoice = null; // what "Start download" does
function dlgReset(url) {
  $("dlgTitle").textContent = url;
  $("dlgSub").textContent = "";
  $("dlgThumb").innerHTML = `<span class="ficon big" id="dlgIcon"><span class="spinner" style="border-color:rgba(255,255,255,.35);border-top-color:#fff"></span></span>`;
  $("dlgLoading").hidden = false;
  $("dlgLoading").lastChild.textContent = " Reading what this link is…";
  $("dlgQualityBox").hidden = true;
  $("dlgQual").innerHTML = "";
  $("dlgNameBox").hidden = true;
  $("dlgWhereBox").hidden = true;
  $("dlgErr").hidden = true;
  $("dlgStart").disabled = true;
  $("dlgStart").textContent = "Start download";
  $("dlgNoAsk").checked = false;
  const names = queueNames();
  $("dlgQueueBox").hidden = !names.length;
  $("dlgQueue").innerHTML = [["", "Main"], ...names.map((q) => [q, q])].map(([v, l]) => `<option value="${esc(v)}">${esc(l)}</option>`).join("");
  $("dlgQueue").value = addQueue();
  if (IN_EXTENSION) $("dlgLoginBox").hidden = true; // the login box only exists in Chrome
  $("addDlg").returnValue = "";
  dlgChoice = null;
}
function hostOf(u) { try { return new URL(u).hostname.replace(/^www\./, ""); } catch { return ""; } }
async function askAndAdd(url, forceHandler) {
  const seq = ++dlgSeq;
  dlgReset(url);
  $("dlgSub").textContent = hostOf(url);
  if (!$("addDlg").open) $("addDlg").showModal();
  const fallback = () => ({ type: "addLink", url, forceHandler });

  const p = await send({ type: "probe", url });
  if (seq !== dlgSeq) return;
  if (p?.hostMissing || p?.helperOutdated) { $("addDlg").close(); return startChoice(fallback()); }
  if (p?.ok && p.isFile) {
    const name = p.name || decodeURIComponent(url.split(/[?#]/)[0].split("/").pop() || "download");
    const cat = category({ fileName: name });
    $("dlgLoading").hidden = true;
    $("dlgTitle").textContent = name;
    $("dlgSub").textContent = [p.length > 0 ? size(p.length) : "Size not given by the site", hostOf(url)].join(" · ");
    $("dlgThumb").innerHTML = `<span class="ficon big t-${cat}">${esc((extOf(name) || "file").toUpperCase().slice(0, 4))}</span>`;
    $("dlgNameBox").hidden = false;
    $("dlgName").value = name;
    $("dlgWhereBox").hidden = false;
    $("dlgWhere").textContent = currentSettings.downloadHandler === "chrome" ? "Chrome's download folder" : "Downloads/AnySave";
    dlgChoice = () => ({ type: "file", url, name: $("dlgName").value.trim() || name, forceHandler });
    $("dlgStart").disabled = false;
    $("dlgStart").focus();
    return;
  }
  await dlgVideo(url, seq, fallback);
}
async function dlgVideo(url, seq, fallback) {
  $("dlgLoading").hidden = false;
  $("dlgLoading").lastChild.textContent = " Finding the video and its qualities…";
  $("dlgErr").hidden = true;
  $("dlgStart").disabled = true;
  const useCookies = IN_EXTENSION && $("dlgLogin").checked;
  const info = await send({ type: "info", url, useCookies });
  if (seq !== dlgSeq) return;
  $("dlgLoading").hidden = true;
  if (!info?.ok) {
    $("dlgErr").hidden = false;
    $("dlgErr").textContent = (info?.error || "Couldn't read this link.") + " You can still try to download it.";
    if (IN_EXTENSION) { $("dlgLoginBox").hidden = false; $("dlgLogin").onchange = () => dlgVideo(url, seq, fallback); }
    $("dlgStart").textContent = "Download anyway";
    dlgChoice = fallback;
    $("dlgStart").disabled = false;
    return;
  }
  $("dlgTitle").textContent = info.title || url;
  const dur = info.duration ? fmtDuration(info.duration) : "";
  $("dlgSub").textContent = [info.extractor && info.extractor !== "Generic" ? info.extractor : hostOf(url), info.uploader, dur].filter(Boolean).join(" · ");
  $("dlgThumb").innerHTML = info.thumbnail ? `<img src="${esc(info.thumbnail)}" alt="" referrerpolicy="no-referrer" />` : `<span class="ficon big t-Videos">${svg("Videos", 22)}</span>`;
  const opts = (info.options || []).filter((o) => o && o.format);
  if (IN_EXTENSION) $("dlgLoginBox").hidden = !/facebook|instagram|tiktok|twitter|x\.com|vimeo|patreon/i.test(url) && !useCookies;
  if (!opts.length) {
    dlgChoice = fallback;
    $("dlgStart").disabled = false;
    return;
  }
  $("dlgQualityBox").hidden = false;
  $("dlgQual").innerHTML = opts.map((o, i) => {
    const label = String(o.label).replace(/\s*\((MP4|MP3|M4A)\)\s*$/i, "").replace(/\s+MP4\b/, "");
    const note = [o.size ? "≈ " + size(o.size) : "", o.audioOnly ? (/(M4A)/i.test(o.label) ? "M4A" : "MP3") : ""].filter(Boolean).join(" · ");
    return `<button type="button" role="radio" aria-checked="${i === 0}" data-i="${i}">${esc(label)}${note ? `<small>${esc(note)}</small>` : "<small>&nbsp;</small>"}</button>`;
  }).join("");
  let pick = 0;
  $("dlgQual").querySelectorAll("button").forEach((b) => {
    b.onclick = () => {
      pick = Number(b.dataset.i);
      $("dlgQual").querySelectorAll("button").forEach((x) => x.setAttribute("aria-checked", String(x === b)));
    };
    b.ondblclick = () => { b.onclick(); $("dlgStart").click(); };
  });
  dlgChoice = () => {
    const o = opts[pick];
    return { type: "download", url, title: info.title, format: o.format, sort: o.sort, audioOnly: !!o.audioOnly, useCookies: IN_EXTENSION && $("dlgLogin").checked };
  };
  $("dlgStart").disabled = false;
  $("dlgStart").focus();
}
async function startChoice(msg) {
  const res = await send({ ...msg, queue: $("addDlg").open ? $("dlgQueue").value : addQueue() });
  if (res?.ok) { toast("Added to your downloads."); showDownloadsFor(); }
  else toast(res?.error || "Couldn't add this link.", true);
  return res;
}
$("addDlg").addEventListener("close", async () => {
  const ok = $("addDlg").returnValue === "ok" && dlgChoice;
  const msg = ok ? dlgChoice() : null;
  const queue = $("dlgQueue").value;
  dlgSeq++; // a slow answer for a closed window is ignored
  if ($("dlgNoAsk").checked && ok) send({ type: "setSettings", patch: { askBeforeAdd: false } });
  if (!msg) return;
  const res = await send({ ...msg, queue });
  if (res?.ok) { toast("Added to your downloads."); showDownloadsFor(); }
  else toast(res?.error || "Couldn't add this link.", true);
});
$("dlgCancel").onclick = () => $("addDlg").close("cancel");
$("dlgStart").onclick = () => { if (!$("dlgStart").disabled) $("addDlg").close("ok"); };
// Enter in the window means Start.
$("addDlg").addEventListener("keydown", (e) => {
  if (e.key !== "Enter" || e.target.closest("button")) return;
  e.preventDefault();
  if (!$("dlgStart").disabled) $("addDlg").close("ok");
});

// The converter lives in convert.js.

// ---------- live updates ----------
chrome.storage.onChanged.addListener((changes, area) => {
  if (area === "local" && changes.settings) {
    applySettingsUI(changes.settings.newValue);
  }
  if (area === "local" && changes.jobs) {
    jobs = changes.jobs.newValue || {};
    render();
    const old = changes.jobs.oldValue || {};
    if (Object.values(jobs).some((j) => j.status === "done" && old[j.id]?.status !== "done")) markSeen();
  }
});
// Finished downloads shown here count as seen: the ✓ on the toolbar icon clears.
let seenTimer = null;
function markSeen() {
  if (document.visibilityState !== "visible" || seenTimer) return;
  seenTimer = setTimeout(() => { seenTimer = null; send({ type: "seen" }); }, 800);
}
document.addEventListener("visibilitychange", markSeen);
$("copyDiag").addEventListener("click", async () => {
  const btn = $("copyDiag");
  const orig = btn.textContent;
  btn.disabled = true;
  btn.textContent = "Gathering…";
  const res = await send({ type: "diagnostics" });
  try {
    await navigator.clipboard.writeText(res?.text || "No diagnostics available");
    btn.textContent = "Copied ✓";
  } catch {
    btn.textContent = "Couldn't copy";
  }
  setTimeout(() => { btn.disabled = false; btn.textContent = orig; }, 2200);
});

// ---------- settings ----------
function applySettingsUI(settings = {}) {
  currentSettings = settings;
  if ($("captureAll")) $("captureAll").checked = settings.captureAll !== false;
  if ($("askBeforeAdd")) $("askBeforeAdd").checked = settings.askBeforeAdd !== false;
  const h = settings.downloadHandler || "anysave";
  document.querySelectorAll("#handlerChips .chip").forEach((c) => c.classList.toggle("active", c.dataset.h === h));
  if ($("minSizeMb")) $("minSizeMb").value = String(settings.minSizeMb || 0);
  if ($("fileTypes") && document.activeElement !== $("fileTypes")) $("fileTypes").value = settings.fileTypes || "";
  if ($("excludedSites") && document.activeElement !== $("excludedSites")) $("excludedSites").value = settings.excludedSites || "";
  if ($("loginFastSites")) $("loginFastSites").checked = !!settings.loginFastSites;
  const speed = String(settings.maxSpeedKbps || 0);
  for (const id of ["maxSpeedKbps", "sbSpeed"]) setSelect($(id), speed, (v) => `${Math.round(v / 1024 * 10) / 10} MB/s`);
  if ($("maxConcurrent")) $("maxConcurrent").value = String(settings.maxConcurrent || 3);
  for (const id of ["afterDoneAction", "sbAfter"]) if ($(id)) $(id).value = settings.afterDoneAction || "none";
  if ($("videoOverlay")) $("videoOverlay").checked = settings.videoOverlay !== false;
  const langs = new Set(String(settings.subLangs || "").split(/[\s,;]+/).filter(Boolean));
  document.querySelectorAll("#subLangsBox input").forEach((c) => { c.checked = langs.has(c.value); });
  if ($("proxy") && document.activeElement !== $("proxy")) $("proxy").value = settings.proxy || "";
  if ($("scheduleOn")) $("scheduleOn").checked = !!settings.scheduleOn;
  if ($("scheduleStart") && document.activeElement !== $("scheduleStart")) $("scheduleStart").value = settings.scheduleStart || "01:00";
  if ($("scheduleStop") && document.activeElement !== $("scheduleStop")) $("scheduleStop").value = settings.scheduleStop || "07:00";
  if ($("queues") && document.activeElement !== $("queues")) $("queues").value = settings.queues || "";
  syncQueueUI();
  if ($("embedChapters")) $("embedChapters").checked = settings.embedChapters !== false;
  if ($("notifications")) $("notifications").checked = settings.notifications !== false;
  if ($("embedCover")) $("embedCover").checked = !!settings.embedCover;
  render();
}
// A value set elsewhere that isn't in the list (e.g. 3000 KB/s) still shows.
function setSelect(sel, value, label) {
  if (!sel) return;
  if (![...sel.options].some((o) => o.value === value)) sel.add(new Option(label(Number(value)), value));
  sel.value = value;
}
const setting = (id, key, read) => $(id)?.addEventListener("change", (e) => send({ type: "setSettings", patch: { [key]: read(e.target) } }));
$("captureAll")?.addEventListener("change", (e) => send({ type: "setCapture", on: e.target.checked }));
setting("askBeforeAdd", "askBeforeAdd", (t) => t.checked);
document.querySelectorAll("#handlerChips .chip").forEach((c) => c.addEventListener("click", () => {
  document.querySelectorAll("#handlerChips .chip").forEach((x) => x.classList.toggle("active", x === c));
  send({ type: "setSettings", patch: { downloadHandler: c.dataset.h } });
}));
setting("minSizeMb", "minSizeMb", (t) => Number(t.value || 0));
setting("fileTypes", "fileTypes", (t) => t.value.trim());
setting("excludedSites", "excludedSites", (t) => t.value.trim());
setting("maxSpeedKbps", "maxSpeedKbps", (t) => Number(t.value || 0));
setting("sbSpeed", "maxSpeedKbps", (t) => Number(t.value || 0));
setting("maxConcurrent", "maxConcurrent", (t) => Number(t.value || 3));
setting("afterDoneAction", "afterDoneAction", (t) => t.value);
setting("sbAfter", "afterDoneAction", (t) => t.value);
setting("videoOverlay", "videoOverlay", (t) => t.checked);
setting("notifications", "notifications", (t) => t.checked);
setting("embedChapters", "embedChapters", (t) => t.checked);
setting("embedCover", "embedCover", (t) => t.checked);
// Subtitle languages: Bangla, English and the other most-used languages.
const SUB_LANGS = [["en", "English"], ["bn", "বাংলা (Bangla)"], ["hi", "हिन्दी (Hindi)"], ["ur", "اردو (Urdu)"], ["ar", "العربية (Arabic)"],
  ["es", "Español"], ["fr", "Français"], ["de", "Deutsch"], ["pt", "Português"], ["ru", "Русский"], ["tr", "Türkçe"],
  ["id", "Indonesia"], ["zh", "中文 (Chinese)"], ["ja", "日本語 (Japanese)"], ["ko", "한국어 (Korean)"]];
$("subLangsBox").innerHTML = SUB_LANGS.map(([code, name]) =>
  `<label><input type="checkbox" value="${code}" /> ${esc(name)}</label>`).join("");
$("subLangsBox").addEventListener("change", () => {
  const langs = [...document.querySelectorAll("#subLangsBox input:checked")].map((c) => c.value);
  send({ type: "setSettings", patch: { subLangs: langs.join(",") } });
});
// A proxy address is checked before it is saved, so a typo can't silently break every download.
$("proxy").addEventListener("change", (e) => {
  const v = e.target.value.trim();
  const ok = !v || /^(system|direct)$/i.test(v) || /^((https?|socks5h?):\/\/)?([^\s/:@]+(:[^\s/@]*)?@)?[^\s/:@]+:\d{1,5}\/?$/i.test(v);
  e.target.setCustomValidity(ok ? "" : "Use http://host:port, socks5://host:port, or leave blank");
  e.target.reportValidity();
  if (ok) send({ type: "setSettings", patch: { proxy: v } });
});
$("loginFastSites")?.addEventListener("change", async (e) => {
  if (e.target.checked) {
    try {
      const granted = await chrome.permissions.request({ origins: ["*://*/*"] });
      if (!granted) { e.target.checked = false; return; }
    } catch {
      e.target.checked = false; return;
    }
  }
  send({ type: "setSettings", patch: { loginFastSites: e.target.checked } });
});
// Some settings only make sense in the browser extension, others only in the desktop app.
document.querySelectorAll(IN_EXTENSION ? ".desktop-only" : ".extension-only").forEach((el) => el.remove());
try { const v = chrome.runtime.getManifest().version; if (v) $("aboutVersion").textContent = "version " + v; } catch {}

// ---------- helper out of date / missing ----------
async function showHelperNotice() {
  const el = $("helperNotice");
  if (!IN_EXTENSION) return; // the desktop app ships its own helper
  const { helperStatus: s } = await chrome.storage.local.get("helperStatus");
  if (s?.missing) {
    el.textContent = "The AnySave helper isn't installed, so videos and the fast engine can't work. Run the AnySave installer.";
  } else if (s?.outdated) {
    el.textContent = "Your AnySave helper is older than the extension" + (s.version ? ` (${s.version} vs ${s.extension})` : "") +
      ". Run the AnySave installer again to update it, so the newest features work.";
  }
  el.hidden = !(s?.missing || s?.outdated);
}
showHelperNotice();
chrome.storage?.onChanged?.addListener((ch, area) => { if (area === "local" && ch.helperStatus) showHelperNotice(); });

// ---------- transcripts: list and search ----------
const LANG_LABEL = { en: "English", bn: "Bangla", hi: "Hindi", ur: "Urdu", ar: "Arabic", es: "Spanish", fr: "French",
  de: "German", pt: "Portuguese", ru: "Russian", tr: "Turkish", id: "Indonesian", zh: "Chinese", ja: "Japanese", ko: "Korean", auto: "" };
// Highlights the searched words; works on Bangla too (no word boundaries assumed).
function highlight(text, q) {
  const words = String(q || "").toLowerCase().split(/[\s.,;:!?"'()]+/).filter(Boolean);
  let html = esc(text);
  for (const w of words.sort((a, b) => b.length - a.length)) {
    // \p{M}*: take in vowel signs joined to the last letter, so a Bangla letter is never split.
    const re = new RegExp(esc(w).replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "\\p{M}*", "giu");
    html = html.replace(re, (m) => `<mark>${m}</mark>`);
  }
  return html;
}
let txSearchSeq = 0;
async function searchTranscripts(q) {
  const seq = ++txSearchSeq;
  const dirs = [...new Set(Object.values(jobs).filter((j) => j.kind === "transcribe" && j.file)
    .map((j) => String(j.file).replace(/[\\/][^\\/]*$/, "")))];
  const m = $("txMsg");
  m.hidden = false; m.className = "msg"; m.textContent = q ? "Searching…" : "Loading your transcripts…";
  const res = await send({ type: "searchTranscripts", query: q, dirs });
  if (seq !== txSearchSeq) return; // a newer search is running
  const ul = $("txResults");
  ul.innerHTML = "";
  if (!res?.ok) { m.className = "msg err"; m.textContent = res?.error || "Couldn't search. Is the AnySave helper installed?"; return; }
  const list = res.results || [];
  const hits = list.reduce((a, r) => a + (r.count || 0), 0);
  m.textContent = q
    ? (list.length ? `"${q}" found ${hits} time${hits === 1 ? "" : "s"} in ${list.length} transcript${list.length === 1 ? "" : "s"}.`
      : `"${q}" wasn't found in your ${res.scanned} transcript${res.scanned === 1 ? "" : "s"}.`)
    : (list.length ? `${list.length} transcript${list.length === 1 ? "" : "s"}. Search above to find a word or phrase.`
      : "No transcripts yet. Select a finished video in Downloads and click Transcribe.");
  for (const r of list) {
    const li = document.createElement("li");
    const lang = LANG_LABEL[r.lang] ?? r.lang;
    const when = r.modified ? ago(r.modified) : "";
    li.innerHTML = `<div class="tx-head"><span class="tx-name" title="${esc(r.srt)}">${esc(r.name)}</span>
        <span class="tx-meta">${[lang, q && `${r.count} match${r.count === 1 ? "" : "es"}`, when].filter(Boolean).map(esc).join(" · ")}</span>
        <span class="btns"></span></div>` +
      (q
        ? `<ul class="tx-hits">${(r.matches || []).map((h) => `<li><span class="tx-time">${esc(h.time)}</span><span>${highlight(h.text, q)}</span></li>`).join("")}</ul>` +
          (r.count > (r.matches || []).length ? `<div class="tx-more">and ${r.count - r.matches.length} more</div>` : "")
        : `<p class="tx-preview">${esc(r.preview || "")}${(r.preview || "").length > 150 ? "…" : ""}</p>`);
    const btns = li.querySelector(".btns");
    const button = (icon, title, fn) => {
      const b = Object.assign(document.createElement("button"), { className: "ib", title });
      b.setAttribute("aria-label", title);
      b.innerHTML = svg(icon, 18);
      b.onclick = fn;
      btns.appendChild(b);
    };
    const open = (file) => send({ type: "openPath", file }).then((x) => { if (x && !x.ok) toast(x.error || "Couldn't open the file", true); });
    if (r.txt) button("text", "Open the text", () => open(r.txt));
    if (r.video) button("open", "Open the video", () => open(r.video));
    button("folder", "Show in folder", () => send({ type: "reveal", file: r.srt }));
    ul.appendChild(li);
  }
}
$("txSearchForm").addEventListener("submit", (e) => { e.preventDefault(); searchTranscripts($("txQuery").value.trim()); });
$("txQuery").addEventListener("search", () => { if (!$("txQuery").value) searchTranscripts(""); }); // the x in the box

// ---------- grab files from a site ----------
let crawlFiles = [];
function crawlShown() {
  const f = ($("crawlFilter")?.value || "").trim().toLowerCase();
  return crawlFiles.filter((x) => !f || x.name.toLowerCase().includes(f) || ("." + x.ext).includes(f));
}
function crawlMsg(text, isErr) {
  const m = $("crawlMsg");
  m.hidden = !text; m.className = "msg" + (isErr ? " err" : ""); m.textContent = text || "";
}
function renderCrawl() {
  const shown = crawlShown();
  $("crawlList").innerHTML = shown.map((x) => `<li><input type="checkbox" data-u="${esc(x.url)}"${x.picked ? " checked" : ""} aria-label="${esc(x.name)}" />
    <span class="tag">${esc(x.ext.toUpperCase().slice(0, 4))}</span><span class="cname" title="${esc(x.url)}">${esc(x.name)}</span>
    <span class="cfrom" title="Found on ${esc(x.from)}">${esc(x.from.replace(/^https?:\/\//, ""))}</span></li>`).join("");
  const n = crawlFiles.filter((x) => x.picked).length;
  $("crawlDownload").disabled = !n;
  $("crawlDownload").textContent = n ? `Download selected (${n})` : "Download selected";
  $("crawlAll").checked = shown.length > 0 && shown.every((x) => x.picked);
}
$("crawlList").addEventListener("change", (e) => {
  const item = crawlFiles.find((x) => x.url === e.target.dataset.u);
  if (item) { item.picked = e.target.checked; renderCrawl(); }
});
$("crawlAll").addEventListener("change", (e) => { crawlShown().forEach((x) => { x.picked = e.target.checked; }); renderCrawl(); });
$("crawlFilter").addEventListener("input", renderCrawl);
$("crawlForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  let url = $("crawlUrl").value.trim();
  if (url && !/^https?:\/\//i.test(url)) url = "https://" + url;
  const btn = $("crawlGo");
  btn.disabled = true; btn.textContent = "Scanning…";
  crawlMsg("Reading the website… this can take up to a minute.");
  $("crawlResults").hidden = true;
  const res = await send({ type: "crawl", url, depth: Number($("crawlDepth").value), types: $("crawlTypes").value, samePath: $("crawlSamePath").checked });
  btn.disabled = false; btn.textContent = "Scan";
  if (!res?.ok) { crawlFiles = []; crawlMsg(res?.error || "Couldn't scan that website.", true); return; }
  crawlFiles = (res.files || []).map((x) => ({ ...x, picked: false }));
  const why = { "page limit": " It stopped at the page limit, so there may be more.", "time limit": " It stopped at the time limit, so there may be more.", "file limit": " Only the first 2,000 files are listed." }[res.stopped] || "";
  crawlMsg(crawlFiles.length
    ? `Found ${crawlFiles.length} file${crawlFiles.length === 1 ? "" : "s"} on ${res.pages} page${res.pages === 1 ? "" : "s"}.${why}`
    : `No files found on ${res.pages} page${res.pages === 1 ? "" : "s"}. Try following more levels of links, or clear the file types.${why}`, !crawlFiles.length);
  $("crawlResults").hidden = !crawlFiles.length;
  renderCrawl();
});
$("crawlDownload").addEventListener("click", async () => {
  const picks = crawlFiles.filter((x) => x.picked);
  const btn = $("crawlDownload");
  btn.disabled = true; btn.textContent = `Adding ${picks.length}…`;
  let ok = 0;
  for (const x of picks) {
    const res = await send({ type: "file", url: x.url, name: x.name, referrer: x.from, queue: addQueue() });
    if (res?.ok) { ok++; x.picked = false; }
  }
  crawlMsg(`Added ${ok} of ${picks.length} file${picks.length === 1 ? "" : "s"}. They're in Downloads.`, ok < picks.length);
  renderCrawl();
});

// ---------- queues and schedule ----------
function queueNames() {
  return [...new Set(String(currentSettings.queues || "").split(",").map((q) => q.trim().slice(0, 24)).filter(Boolean))];
}
function addQueue() { return $("addQueue") && !$("addQueue").hidden ? $("addQueue").value : ""; }
function syncQueueUI() {
  const names = queueNames();
  const options = (first) => [["", first], ...names.map((q) => [q, q])];
  const fill = (sel, opts, keep) => {
    if (!sel) return;
    const want = keep ?? sel.value;
    sel.innerHTML = opts.map(([v, label]) => `<option value="${esc(v)}">${esc(label)}</option>`).join("");
    sel.value = opts.some(([v]) => v === want) ? want : "";
  };
  fill($("addQueue"), options("Main queue"));
  $("addQueue").hidden = !names.length;
  fill($("scheduleQueue"), [["", "all downloads"], ...names.map((q) => [q, `the "${q}" queue only`])], currentSettings.scheduleQueue || "");
  const s = currentSettings;
  const timeNote = (q) => (s.scheduleOn && (s.scheduleQueue || "") === q ? ` · ${s.scheduleStart || "01:00"}–${s.scheduleStop || "07:00"}` : "");
  $("queueChips").innerHTML = ["", ...names].map((q) =>
    `<button class="nav" data-f="q:${esc(q)}" title="${q ? `Downloads in the "${esc(q)}" queue` : "Downloads not in another queue"}">${esc(q || "Main queue")}<span class="muted small">${esc(timeNote(q))}</span><span class="n" data-count="q:${esc(q)}"></span></button>`).join("");
  if (filter.startsWith("q:") && filter !== "q:" && !names.includes(filter.slice(2))) filter = "all";
  highlightNav();
}
$("newQueue").onclick = () => {
  const name = (prompt("Name of the new queue (for example Night or Work):", "") || "").trim().slice(0, 24).replace(/,/g, " ");
  if (!name) return;
  const names = queueNames();
  if (!names.includes(name)) send({ type: "setSettings", patch: { queues: [...names, name].join(", ") } });
  filter = "q:" + name;
  showTab("downloads");
};
$("scheduleOn").addEventListener("change", (e) => send({ type: "setSettings", patch: { scheduleOn: e.target.checked } }));
$("scheduleStart").addEventListener("change", (e) => { if (e.target.value) send({ type: "setSettings", patch: { scheduleStart: e.target.value } }); });
$("scheduleStop").addEventListener("change", (e) => { if (e.target.value) send({ type: "setSettings", patch: { scheduleStop: e.target.value } }); });
$("scheduleQueue").addEventListener("change", (e) => send({ type: "setSettings", patch: { scheduleQueue: e.target.value } }));
$("queues").addEventListener("change", (e) => {
  const clean = [...new Set(e.target.value.split(",").map((q) => q.trim().slice(0, 24)).filter(Boolean))].join(", ");
  const patch = { queues: clean };
  // A schedule for a queue that no longer exists would silently do nothing: make it "all downloads".
  if (currentSettings.scheduleQueue && !clean.split(", ").includes(currentSettings.scheduleQueue)) patch.scheduleQueue = "";
  send({ type: "setSettings", patch });
});

// Drop a link anywhere on the page to download it.
document.addEventListener("dragover", (e) => {
  if (!dragging) e.preventDefault();
});
document.addEventListener("drop", (e) => {
  if (dragging) return;
  const text = e.dataTransfer?.getData("text/uri-list") || e.dataTransfer?.getData("text/plain") || "";
  const found = linksIn(text);
  if (found.length) {
    e.preventDefault();
    $("addUrl").value = found.join(" ");
    $("addUrl").dispatchEvent(new Event("input"));
    $("addForm").requestSubmit();
  }
});

(async () => {
  const { settings = {} } = await chrome.storage.local.get("settings");
  jobs = (await chrome.storage.local.get("jobs")).jobs || {};
  applySettingsUI(settings);
  markSeen();
  const h = location.hash.slice(1);
  if (h === "schedule") { showTab("settings"); $("scheduleCard").scrollIntoView(); }
  else showTab(VIEWS.includes(h) ? h : "downloads");
})();
