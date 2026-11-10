// AnySave service worker (2.0): video downloads through the native helper (yt-dlp),
// any-file downloads through Chrome's downloader, conversions through the helper (FFmpeg),
// and a right-click menu. Jobs are kept in chrome.storage.local so the history survives restarts.
const HOST = "com.vidgrab.host";
const ports = new Map(); // jobId -> native Port (running video downloads and conversions)

// ---------- job store ----------

async function getJobs() {
  return (await chrome.storage.local.get("jobs")).jobs || {};
}
let writeChain = Promise.resolve();
function updateJob(id, patch) {
  // Serialise writes so fast progress updates don't overwrite each other.
  writeChain = writeChain.then(async () => {
    const jobs = await getJobs();
    jobs[id] = { ...(jobs[id] || { id, createdAt: Date.now() }), ...patch };
    const ids = Object.keys(jobs).sort((a, b) => (jobs[b].createdAt || 0) - (jobs[a].createdAt || 0));
    for (const old of ids.slice(400)) delete jobs[old]; // keep the newest 400
    await chrome.storage.local.set({ jobs });
  }).catch((e) => console.warn("AnySave: saving a job failed", e)); // never leave the chain broken
  return writeChain;
}
// Any other change to the whole list (remove, clear) goes through the same chain, so a
// progress write that was already queued can't bring removed items back.
function mutateJobs(fn) {
  writeChain = writeChain.then(async () => {
    const jobs = await getJobs();
    fn(jobs);
    await chrome.storage.local.set({ jobs });
  }).catch((e) => console.warn("AnySave: saving the list failed", e));
  return writeChain;
}
function newId() { return String(Date.now()) + Math.floor(Math.random() * 1000); }

// ---------- helpers ----------

const VIDEO_SITES = /(^|\.)(youtube\.com|youtu\.be|facebook\.com|fb\.watch|instagram\.com|tiktok\.com|vimeo\.com)$/;
const FILE_EXT = /\.(mp4|mkv|webm|mov|avi|m4v|3gp|flv|wmv|ts|mpe?g|mp3|m4a|aac|wav|flac|ogg|opus|wma|jpe?g|png|gif|webp|heic|bmp|svg|tiff?|pdf|docx?|xlsx?|pptx?|txt|csv|rtf|odt|ods|epub|zip|rar|7z|tar|gz|tgz|bz2|xz|iso|dmg|apk|exe|msi|pkg|deb|rpm|appimage)$/i;

function extOf(name) {
  const m = String(name || "").split(/[?#]/)[0].match(/\.([a-z0-9]{1,8})$/i);
  return m ? m[1].toLowerCase() : "";
}
function categoryOf(name, kind, audioOnly) {
  if (kind === "convert") return "Converted";
  const e = extOf(name);
  if (audioOnly || /^(mp3|m4a|aac|wav|flac|ogg|opus|wma|amr)$/.test(e)) return "Music";
  if (/^(mp4|mkv|webm|mov|avi|m4v|3gp|flv|wmv|ts|mpe?g)$/.test(e) || kind === "video") return "Videos";
  if (/^(jpe?g|png|gif|webp|heic|bmp|svg|tiff?)$/.test(e)) return "Pictures";
  if (/^(pdf|docx?|xlsx?|pptx?|txt|csv|rtf|odt|ods|epub|md|json|xml)$/.test(e)) return "Documents";
  if (/^(zip|rar|7z|tar|gz|tgz|bz2|xz|iso|dmg)$/.test(e)) return "Archives";
  if (/^(apk|xapk|exe|msi|pkg|deb|rpm|appimage)$/.test(e)) return "Apps";
  return "Other";
}
function safeFile(s) {
  return String(s || "download").replace(/[\\/:*?"<>|\n\r\t~]+/g, " ").replace(/\s+/g, " ").trim().slice(0, 120) || "download";
}
function siteLabel(url) {
  try {
    const host = new URL(url).hostname.replace(/^(www|m)\./, "");
    const parts = host.split(".");
    const name = parts.length >= 2 ? parts[parts.length - 2] : parts[0];
    return name.charAt(0).toUpperCase() + name.slice(1);
  } catch { return "Web"; }
}
function native(msg) {
  return new Promise((resolve) => {
    chrome.runtime.sendNativeMessage(HOST, msg, (res) => {
      if (chrome.runtime.lastError) resolve(nativeError(chrome.runtime.lastError.message));
      else resolve(res || { ok: false, error: "No answer from helper" });
    });
  });
}

// Chrome's message when the helper isn't installed/registered, vs. when it crashed.
function helperMissing(msg) { return /not found|forbidden/i.test(msg || ""); }
function nativeError(msg) {
  return helperMissing(msg)
    ? { ok: false, error: msg, hostMissing: true }
    : { ok: false, error: "The AnySave helper stopped unexpectedly. Please try again." };
}

// Login cookies for the site of `url`, in Netscape cookies.txt format. macOS blocks other
// programs from reading Chrome's cookie database, so they're read through chrome.cookies,
// only for the site being downloaded from.
const COOKIE_DOMAINS = {
  "youtube.com": ["youtube.com"], "youtu.be": ["youtube.com"],
  "facebook.com": ["facebook.com"], "fb.watch": ["facebook.com"],
  "instagram.com": ["instagram.com"], "tiktok.com": ["tiktok.com"], "vimeo.com": ["vimeo.com"],
};
async function cookiesFor(url) {
  const base = new URL(url).hostname.split(".").slice(-2).join(".");
  const domains = COOKIE_DOMAINS[base] || [];
  const lines = ["# Netscape HTTP Cookie File"];
  for (const domain of domains) {
    try {
      for (const c of await chrome.cookies.getAll({ domain })) {
        const d = c.hostOnly ? c.domain : (c.domain.startsWith(".") ? c.domain : "." + c.domain);
        lines.push([(c.httpOnly ? "#HttpOnly_" : "") + d, c.hostOnly ? "FALSE" : "TRUE", c.path,
          c.secure ? "TRUE" : "FALSE", Math.floor(c.expirationDate || 0), c.name, c.value].join("\t"));
      }
    } catch {}
  }
  return lines.join("\n") + "\n";
}

// ---------- helper-backed jobs (video downloads and conversions) ----------

function runNative(id, payload, onDone) {
  const port = chrome.runtime.connectNative(HOST);
  ports.set(id, port);
  let finished = false;
  port.onMessage.addListener((msg) => {
    if (msg.type === "progress") {
      if (pausing.has(id)) return; // progress still in flight after Pause was pressed
      updateJob(id, { status: "downloading", percent: msg.percent, speed: msg.speed, eta: msg.eta });
    } else if (msg.type === "done") {
      finished = true;
      updateJob(id, { status: "done", percent: 100, file: msg.file, speed: "", eta: "", finishedAt: Date.now(),
        fileName: msg.file ? msg.file.split(/[\\/]/).pop() : undefined });
      onDone && onDone(msg);
    } else if (msg.type === "paused") {
      finished = true;
      updateJob(id, { status: "paused", speed: "", eta: "" });
    } else if (msg.type === "error") {
      finished = true;
      updateJob(id, { status: msg.message === "Cancelled" ? "cancelled" : "error", error: msg.message, speed: "", eta: "" });
    }
  });
  port.onDisconnect.addListener(() => {
    ports.delete(id);
    pausing.delete(id);
    if (!finished) {
      const err = chrome.runtime.lastError?.message;
      getJobs().then((jobs) => {
        if (["paused", "cancelled"].includes(jobs[id]?.status)) return;
        updateJob(id, { status: err ? "error" : "cancelled", speed: "", eta: "",
          error: err ? (helperMissing(err) ? "AnySave helper not found. Run the AnySave installer again, then restart Chrome." : "The AnySave helper stopped unexpectedly. Try again.") : null });
      });
    }
  });
  port.postMessage(payload);
}
const pausing = new Set(); // job ids whose Pause was sent but not yet confirmed

async function startVideo({ url, format, sort, audioOnly, title, useCookies, id }) {
  id = id || newId();
  await updateJob(id, {
    kind: "video", url, title: title || `${siteLabel(url)} video`, site: siteLabel(url),
    format, sort, audioOnly: !!audioOnly, useCookies: !!useCookies,
    status: "starting", percent: 0, error: null,
  });
  const cookies = useCookies ? await cookiesFor(url) : null;
  // `job` lets the helper keep this download's unfinished pieces apart, so Resume continues them.
  runNative(id, { action: "download", job: id, url, format, sort, audioOnly, cookies, folder: audioOnly ? "Music" : "Videos" });
  return id;
}

async function startConvert({ path, name, spec, id }) {
  id = id || newId();
  await updateJob(id, { kind: "convert", url: path, title: name, spec, status: "starting", percent: 0, error: null,
    qualityLabel: specLabel(spec) });
  runNative(id, { action: "convert", path, spec });
  return id;
}
function specLabel(s) {
  const fmt = (x) => `${Math.floor(x / 60)}:${String(Math.floor(x % 60)).padStart(2, "0")}`;
  return [s.format?.toUpperCase(), s.height && `${s.height}p`, s.targetMb && `under ${s.targetMb} MB`,
    (s.startSec != null || s.endSec != null) && `${fmt(s.startSec || 0)}–${s.endSec != null ? fmt(s.endSec) : "end"}`,
    s.removeAudio && "no sound"].filter(Boolean).join(" · ");
}

// ---------- any file (Chrome's downloader: pause/resume built in) ----------

async function startFile({ url, name, title, id }) {
  id = id || newId();
  const fileName = safeFile(name || decodeURIComponent(new URL(url).pathname.split("/").pop() || "") || "download");
  const folder = categoryOf(fileName, "file");
  await updateJob(id, { kind: "file", url, title: title || fileName, fileName, site: siteLabel(url),
    status: "starting", percent: 0, qualityLabel: "File", error: null, downloadId: null, doneBytes: 0, totalBytes: 0 });
  ownUrls.add(url); // so onCreated doesn't adopt it a second time as a "website download"
  try {
    const downloadId = await chrome.downloads.download({
      url, filename: `AnySave/${folder}/${fileName}`, conflictAction: "uniquify",
    });
    await updateJob(id, { downloadId, status: "downloading" });
    pollFiles();
  } catch (e) {
    await updateJob(id, { status: "error", error: String(e.message || e) });
  }
  setTimeout(() => ownUrls.delete(url), 5000);
  return id;
}
const ownUrls = new Set();

let polling = null;
function pollFiles() {
  if (polling) return;
  polling = setInterval(async () => {
    const jobs = await getJobs();
    const active = Object.values(jobs).filter((j) => j.downloadId && ["starting", "downloading", "paused"].includes(j.status));
    if (!active.some((j) => j.status !== "paused")) { clearInterval(polling); polling = null; }
    for (const j of active) {
      const [d] = await chrome.downloads.search({ id: j.downloadId });
      if (!d) continue;
      if (d.state === "complete") {
        await updateJob(j.id, { status: "done", percent: 100, file: d.filename, fileName: d.filename.split(/[\\/]/).pop(),
          totalBytes: d.totalBytes, doneBytes: d.totalBytes, speed: "", eta: "", finishedAt: j.finishedAt || Date.now() });
      } else if (d.state === "interrupted") {
        await updateJob(j.id, { status: d.error === "USER_CANCELED" ? "cancelled" : "error", error: d.error, speed: "", eta: "" });
      } else if (d.paused) {
        if (j.status !== "paused") await updateJob(j.id, { status: "paused", speed: "", eta: "" });
      } else {
        const prev = j.doneBytes || 0;
        const bps = Math.max(0, (d.bytesReceived - prev) / 0.7);
        const pct = d.totalBytes > 0 ? (d.bytesReceived / d.totalBytes) * 100 : 0;
        const etaS = d.estimatedEndTime ? Math.max(0, Math.round((new Date(d.estimatedEndTime) - Date.now()) / 1000)) : null;
        await updateJob(j.id, { status: "downloading", percent: pct, doneBytes: d.bytesReceived, totalBytes: d.totalBytes,
          speed: humanSpeed(bps), eta: etaS == null ? "?" : humanEta(etaS) });
      }
    }
  }, 700);
}
// Finishes are also caught here, in case the service worker slept during a long download.
chrome.downloads.onChanged.addListener(async (delta) => {
  if (!delta.state) return;
  const jobs = await getJobs();
  const j = Object.values(jobs).find((x) => x.downloadId === delta.id);
  if (!j) return;
  const [d] = await chrome.downloads.search({ id: delta.id });
  if (delta.state.current === "complete") {
    const bytes = d?.totalBytes > 0 ? d.totalBytes : d?.bytesReceived;
    await updateJob(j.id, { status: "done", percent: 100, file: d?.filename, fileName: d?.filename?.split(/[\\/]/).pop(),
      totalBytes: bytes, doneBytes: bytes, speed: "", eta: "", finishedAt: j.finishedAt || Date.now() });
  } else if (delta.state.current === "interrupted") {
    await updateJob(j.id, { status: d?.error === "USER_CANCELED" ? "cancelled" : "error", error: d?.error || "Download failed" });
  }
});

function humanSpeed(bps) {
  if (!bps) return "";
  for (const u of ["B/s", "KB/s", "MB/s", "GB/s"]) { if (bps < 1024) return `${bps.toFixed(1)} ${u}`; bps /= 1024; }
  return `${bps.toFixed(1)} TB/s`;
}
function humanEta(s) {
  s = Math.round(s);
  return s < 3600 ? `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`
    : `${Math.floor(s / 3600)}:${String(Math.floor(s % 3600 / 60)).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
}

// ---------- add any link: video page or file? ----------

async function addLink(url, title) {
  let host = "";
  try { host = new URL(url).hostname; } catch { return { ok: false, error: "That isn't a web link." }; }
  if (VIDEO_SITES.test(host)) return { ok: true, id: await startVideo({ url, format: "bv*+ba/b", sort: "res,fps,vcodec:h264,acodec:m4a" }) };
  if (FILE_EXT.test(new URL(url).pathname)) return { ok: true, id: await startFile({ url, title }) };
  // Unknown: ask the helper what the server says it is.
  const p = await native({ action: "probe", url });
  // Without the helper, video pages can't be read anyway: let Chrome download the link.
  if ((p.ok && p.isFile) || p.hostMissing) return { ok: true, id: await startFile({ url, name: p.name, title }) };
  return { ok: true, id: await startVideo({ url, format: "bv*+ba/b", sort: "res,fps,vcodec:h264,acodec:m4a", title }) };
}

// ---------- pause / resume / cancel ----------

async function pause(id) {
  const j = (await getJobs())[id];
  if (!j) return;
  if (j.kind === "file" && j.downloadId) { await chrome.downloads.pause(j.downloadId); await updateJob(id, { status: "paused", speed: "", eta: "" }); }
  else if (j.kind === "video" && ports.has(id)) { pausing.add(id); await updateJob(id, { status: "paused", speed: "", eta: "" }); ports.get(id).postMessage({ action: "pause" }); }
}
async function resume(id) {
  const j = (await getJobs())[id];
  if (!j) return;
  if (j.kind === "file" && j.downloadId) {
    const [d] = await chrome.downloads.search({ id: j.downloadId });
    if (d?.canResume) { await chrome.downloads.resume(j.downloadId); await updateJob(id, { status: "downloading" }); pollFiles(); }
    else { await updateJob(id, { status: "cancelled" }); await startFile({ url: j.url, name: j.fileName, title: j.title }); }
  } else if (j.kind === "video") {
    await startVideo({ ...j, id }); // yt-dlp continues the .part file
  }
}
async function cancel(id) {
  const j = (await getJobs())[id];
  if (!j) return;
  if (j.downloadId) { try { await chrome.downloads.cancel(j.downloadId); } catch {} }
  if (ports.has(id)) {
    // Tell the helper it's a real cancel (delete the pieces); closing alone means "pause".
    try { ports.get(id).postMessage({ action: "cancel" }); } catch {}
    const port = ports.get(id); ports.delete(id);
    setTimeout(() => { try { port.disconnect(); } catch {} }, 300);
  }
  await updateJob(id, { status: "cancelled", speed: "", eta: "" });
}
async function retry(id) {
  const j = (await getJobs())[id];
  if (!j) return;
  // Retry restarts the same row, so the failed one doesn't stay behind in "Failed".
  if (j.kind === "file") return startFile({ url: j.url, name: j.fileName, title: j.title, id });
  if (j.kind === "convert") return startConvert({ path: j.url, name: j.title, spec: j.spec, id });
  return startVideo({ ...j, id });
}

// ---------- terms gate + messages ----------

async function agreed() {
  const { acceptedTermsVersion = 0 } = await chrome.storage.local.get("acceptedTermsVersion");
  return acceptedTermsVersion >= 2; // keep in sync with TERMS_VERSION in popup.js
}
const GATED = new Set(["info", "download", "directDownload", "file", "addLink", "convert"]);

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  (async () => {
    if (GATED.has(msg.type) && !(await agreed())) {
      return { ok: false, error: "Please accept the legal notice in the AnySave popup first." };
    }
    switch (msg.type) {
      case "info": {
        const cookies = msg.useCookies ? await cookiesFor(msg.url) : null;
        return native({ action: "info", url: msg.url, cookies });
      }
      case "download": return { ok: true, id: await startVideo(msg) };
      case "directDownload": return { ok: true, id: await startFile({ url: msg.src, name: safeFile(msg.title) + ".mp4", title: msg.title }) };
      case "file": return { ok: true, id: await startFile(msg) };
      case "addLink": return addLink(msg.url, msg.title);
      case "convert": return { ok: true, id: await startConvert(msg) };
      case "pickFile": return native({ action: "pickFile" });
      case "pause": await pause(msg.id); return { ok: true };
      case "resume": await resume(msg.id); return { ok: true };
      case "cancel": await cancel(msg.id); return { ok: true };
      case "retry": await retry(msg.id); return { ok: true };
      case "remove": await mutateJobs((jobs) => { delete jobs[msg.id]; }); return { ok: true };
      case "clear":
        await mutateJobs((jobs) => {
          for (const [k, j] of Object.entries(jobs)) if (["done", "error", "cancelled"].includes(j.status)) delete jobs[k];
        });
        return { ok: true };
      case "fileInfo": return native({ action: "fileInfo", path: msg.path });
      case "reveal": {
        const j = msg.id ? (await getJobs())[msg.id] : null;
        if (j?.downloadId) { chrome.downloads.show(j.downloadId); return { ok: true }; }
        return native({ action: "reveal", file: msg.file || j?.file });
      }
      case "revealDirect": chrome.downloads.show(msg.downloadId); return { ok: true };
      case "open": {
        const j = (await getJobs())[msg.id];
        if (!j?.file) return { ok: false, error: "File not found" };
        return native({ action: "open", file: j.file });
      }
      case "openManager": openManager(msg.tab); return { ok: true };
      case "seen": await markSeen(); return { ok: true };
      case "setCapture": {
        const { settings = {} } = await chrome.storage.local.get("settings");
        await chrome.storage.local.set({ settings: { ...settings, captureAll: !!msg.on } });
        return { ok: true };
      }
    }
  })().then(sendResponse, (e) => sendResponse({ ok: false, error: String(e) }));
  return true;
});

function openManager(tab) {
  chrome.tabs.create({ url: chrome.runtime.getURL("manager.html" + (tab ? "#" + tab : "")) });
}

// ---------- right-click menu ----------

chrome.runtime.onInstalled.addListener(async () => {
  chrome.contextMenus.removeAll(() => {
    chrome.contextMenus.create({ id: "vg-link", title: "Download link with AnySave", contexts: ["link"] });
    chrome.contextMenus.create({ id: "vg-media", title: "Download with AnySave", contexts: ["video", "audio", "image"] });
    chrome.contextMenus.create({ id: "vg-page", title: "Download this page's video with AnySave", contexts: ["page"] });
    chrome.contextMenus.create({ id: "vg-manager", title: "Open AnySave download manager", contexts: ["action"] });
  });
  // Move pre-2.0 session jobs into the persistent store.
  const old = (await chrome.storage.session.get("jobs")).jobs;
  if (old) await mutateJobs((jobs) => { for (const [k, j] of Object.entries(old)) if (!jobs[k]) jobs[k] = j; });
});

chrome.contextMenus.onClicked.addListener(async (info, tab) => {
  if (info.menuItemId === "vg-manager") return openManager();
  if (!(await agreed())) { chrome.action.openPopup?.(); return; }
  let target = null;
  if (info.menuItemId === "vg-link") target = info.linkUrl;
  if (info.menuItemId === "vg-media") target = info.srcUrl?.startsWith("blob:") ? info.pageUrl : info.srcUrl;
  if (info.menuItemId === "vg-page") target = info.pageUrl;
  if (!target) return;
  // Images and plain media files go straight to Chrome's downloader.
  if (info.mediaType === "image" || (info.srcUrl && FILE_EXT.test(new URL(info.srcUrl).pathname))) {
    await startFile({ url: target, title: tab?.title });
  } else {
    await addLink(target, info.menuItemId === "vg-page" ? tab?.title : undefined);
  }
  // The job's own progress shows on the badge as soon as it starts.
  lastIconState = "";
  updateActionIcon();
});

// ---------- catch downloads started by websites ----------
// When a site's own download button starts a download, Chrome downloads it with the page's
// login/session. AnySave adopts it: it shows in the manager (pause/resume/cancel) and is
// saved into Downloads/AnySave/<type>. Switch: "Catch downloads from websites" in the manager.

async function captureOn() {
  const { settings = {} } = await chrome.storage.local.get("settings");
  return settings.captureAll !== false; // on by default
}
let captureCache = true;

// While AnySave catches downloads, hide Chrome's own download bubble (like other download
// managers do), so each download shows in one place. It comes back when catching is off, and
// also while the AnySave icon isn't pinned to the toolbar: Chrome doesn't show an unpinned
// extension's badge, so without the bubble there'd be no progress visible anywhere.
async function iconPinned() {
  try { return (await chrome.action.getUserSettings()).isOnToolbar !== false; } catch { return true; }
}
async function applyChromeBubble() {
  const hide = captureCache && (await iconPinned());
  try { await chrome.downloads.setUiOptions({ enabled: !hide }); } catch {}
}
captureOn().then((v) => { captureCache = v; applyChromeBubble(); });
chrome.storage.onChanged.addListener((ch, area) => {
  if (area === "local" && ch.settings) {
    captureCache = ch.settings.newValue?.captureAll !== false;
    applyChromeBubble();
  }
});
chrome.action.onUserSettingsChanged?.addListener(() => applyChromeBubble());

// Chrome doesn't always fill byExtensionId when onCreated fires, so our own downloads are
// also recognised by URL (startFile) and by an existing job holding the same download id.
async function isOurs(item) {
  if (item.byExtensionId === chrome.runtime.id || ownUrls.has(item.url) || ownUrls.has(item.finalUrl)) return true;
  return Object.values(await getJobs()).some((j) => j.downloadId === item.id);
}

// When Chrome starts it loads its download history and may announce every old download to
// extensions as if it were new. Only downloads actually running and started just now are
// ones a website started; anything else is history and must not show up in AnySave.
const ADOPT_WINDOW_MS = 60 * 1000;
function isNewDownload(item) {
  const started = Date.parse(item.startTime || "");
  return item.state === "in_progress" && !(started && Date.now() - started > ADOPT_WINDOW_MS);
}

async function adoptDownload(item) {
  if (!isNewDownload(item) || (await isOurs(item)) || !(await captureOn())) return;
  const url = item.finalUrl || item.url || "";
  const name = (item.filename || "").split(/[\\/]/).pop() || decodeURIComponent(url.split(/[?#]/)[0].split("/").pop() || "") || "download";
  const id = newId();
  await updateJob(id, {
    kind: "file", url, title: name, fileName: name, site: /^https?:/.test(url) ? siteLabel(url) : "Web",
    downloadId: item.id, status: item.paused ? "paused" : "downloading", percent: 0,
    totalBytes: item.totalBytes > 0 ? item.totalBytes : 0, qualityLabel: "From website",
  });
  pollFiles();
}
chrome.downloads.onCreated.addListener(adoptDownload);

// One-time cleanup (1.0.2): remove old Chrome downloads that 1.0.0–1.0.1 wrongly took over
// after a restart. A real catch is added within seconds of the download starting.
async function removeAdoptedHistory() {
  const { cleanedHistory } = await chrome.storage.local.get("cleanedHistory");
  if (cleanedHistory) return;
  const jobs = await getJobs();
  const wrong = [];
  for (const j of Object.values(jobs)) {
    if (j.qualityLabel !== "From website" || !j.downloadId) continue;
    const [d] = await chrome.downloads.search({ id: j.downloadId });
    const started = Date.parse(d?.startTime || "");
    if (started && (j.createdAt || 0) - started > 2 * ADOPT_WINDOW_MS) wrong.push(j.id);
  }
  if (wrong.length) await mutateJobs((all) => { for (const id of wrong) delete all[id]; });
  await chrome.storage.local.set({ cleanedHistory: true });
}
removeAdoptedHistory().catch(() => {});

// Route website downloads into Downloads/AnySave/<type>/ (Chrome asks every listener for a name).
chrome.downloads.onDeterminingFilename.addListener((item, suggest) => {
  if (item.byExtensionId === chrome.runtime.id || ownUrls.has(item.url) || !captureCache) { suggest(); return; }
  const base = (item.filename || "download").split(/[\\/]/).pop();
  if (/^AnySave[\\/]/.test(item.filename || "")) { suggest(); return; }
  suggest({ filename: `AnySave/${categoryOf(base, "file")}/${safeFile(base)}`, conflictAction: "uniquify" });
  // Update the job's name once Chrome has decided it.
  getJobs().then((jobs) => {
    const j = Object.values(jobs).find((x) => x.downloadId === item.id);
    if (j) updateJob(j.id, { fileName: base, title: base });
  });
});

// ---------- progress on the toolbar icon ----------
// A ring around the AnySave icon fills as downloads progress, with the percent on the badge
// (like Chrome's own download icon). A ✓ shows briefly when everything has finished.

let baseIcons = null;
async function loadBaseIcons() {
  if (baseIcons) return baseIcons;
  baseIcons = {};
  for (const size of [16, 32]) {
    const blob = await (await fetch(chrome.runtime.getURL(`icons/icon${size}.png`))).blob();
    baseIcons[size] = await createImageBitmap(blob);
  }
  return baseIcons;
}

function drawRing(img, size, fraction) {
  const c = new OffscreenCanvas(size, size);
  const g = c.getContext("2d");
  const inset = size * 0.16;
  g.drawImage(img, inset, inset, size - inset * 2, size - inset * 2); // shrink logo to fit inside the ring
  const r = size / 2 - size * 0.06, cx = size / 2, cy = size / 2;
  g.lineWidth = Math.max(1.5, size * 0.11);
  g.strokeStyle = "rgba(128,128,128,0.35)";
  g.beginPath(); g.arc(cx, cy, r, 0, Math.PI * 2); g.stroke();
  g.strokeStyle = "#e5484d";
  g.lineCap = "round";
  g.beginPath(); g.arc(cx, cy, r, -Math.PI / 2, -Math.PI / 2 + Math.PI * 2 * Math.max(0.03, fraction)); g.stroke();
  return g.getImageData(0, 0, size, size);
}

// Finished downloads the user hasn't looked at yet keep a green ✓ (with their number) on the
// icon until the popup or the manager is opened, like Chrome's own download icon. "Seen" is
// kept in storage, so this survives the service worker being put to sleep.
let lastIconState = "";
async function updateActionIcon() {
  const jobs = Object.values(await getJobs());
  const active = jobs.filter((j) => ["starting", "downloading"].includes(j.status));
  if (!active.length) {
    const { seenAt = 0 } = await chrome.storage.local.get("seenAt");
    const unseen = jobs.filter((j) => j.status === "done" && (j.finishedAt || 0) > seenAt).length;
    const state = `idle|${unseen}`;
    if (state === lastIconState) return;
    lastIconState = state;
    await chrome.action.setIcon({ path: { 16: "icons/icon16.png", 32: "icons/icon32.png", 48: "icons/icon48.png" } });
    chrome.action.setBadgeBackgroundColor({ color: "#2f9e5b" });
    chrome.action.setBadgeTextColor?.({ color: "#ffffff" });
    chrome.action.setBadgeText({ text: unseen ? (unseen > 1 ? `${Math.min(unseen, 99)}✓` : "✓") : "" });
    chrome.action.setTitle({ title: unseen ? `AnySave: ${unseen} download${unseen > 1 ? "s" : ""} finished` : "AnySave" });
    return;
  }
  // Overall progress: by bytes when known, otherwise the average percent.
  const withBytes = active.filter((j) => j.totalBytes > 0);
  const fraction = withBytes.length === active.length
    ? withBytes.reduce((a, j) => a + (j.doneBytes || 0), 0) / withBytes.reduce((a, j) => a + j.totalBytes, 0)
    : active.reduce((a, j) => a + (j.percent || 0), 0) / active.length / 100;
  const pct = Math.max(0, Math.min(99, Math.floor((fraction || 0) * 100)));
  const state = `${pct}|${active.length}`;
  if (state === lastIconState) return;
  lastIconState = state;
  try {
    const icons = await loadBaseIcons();
    await chrome.action.setIcon({ imageData: { 16: drawRing(icons[16], 16, fraction), 32: drawRing(icons[32], 32, fraction) } });
  } catch {}
  chrome.action.setBadgeBackgroundColor({ color: "#e5484d" });
  chrome.action.setBadgeTextColor?.({ color: "#ffffff" });
  // One download: its percent. Several: how many are running.
  chrome.action.setBadgeText({ text: active.length > 1 ? `${active.length}↓` : `${pct}%` });
  chrome.action.setTitle({ title: `AnySave: ${active.length} downloading · ${pct}%` });
}
async function markSeen() {
  await chrome.storage.local.set({ seenAt: Date.now() });
  lastIconState = "";
  updateActionIcon();
}

let iconTimer = null;
chrome.storage.onChanged.addListener((ch, area) => {
  if (area !== "local" || !(ch.jobs || ch.seenAt)) return;
  if (iconTimer) return; // at most every 500 ms
  iconTimer = setTimeout(() => { iconTimer = null; updateActionIcon(); }, 500);
});

// Video downloads and conversions run through a connection that ends when the service worker
// stops (Chrome quit, extension updated). Any still marked running were cut off: videos can be
// resumed (the helper kept their pieces), conversions have to be started again.
mutateJobs((jobs) => {
  for (const j of Object.values(jobs)) {
    if (!["starting", "downloading"].includes(j.status) || ports.has(j.id)) continue;
    if (j.kind === "video") Object.assign(j, { status: "paused", speed: "", eta: "" });
    else if (j.kind === "convert") Object.assign(j, { status: "error", error: "Stopped when Chrome closed. Retry to convert again.", speed: "", eta: "" });
  }
});
// Resume polling file downloads after the service worker restarts.
pollFiles();
updateActionIcon();
