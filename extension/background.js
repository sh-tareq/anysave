// AnySave service worker (2.0): video downloads through the native helper (yt-dlp),
// any-file downloads through Chrome's downloader, conversions through the helper (FFmpeg),
// and a right-click menu. Jobs are kept in chrome.storage.local so the history survives restarts.
const HOST = "com.anysave.host";
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
    const before = jobs[id]?.status;
    jobs[id] = { ...(jobs[id] || { id, createdAt: Date.now() }), ...patch };
    const now = jobs[id].status;
    if (now === "starting" && before !== "starting") cancelPower(); // a download started: call off a pending sleep / shut down
    if (before && before !== now) {
      if (now === "done" || now === "error") queueNotification({ ...jobs[id] });
      // Every kind of download ends here, so "when all downloads finish" works for all of them.
      if (now === "done") setTimeout(() => checkAfterDoneAction(id), 400);
    }
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
const FILE_EXT = /\.(mp4|mkv|webm|mov|avi|m4v|3gp|flv|wmv|ts|mpe?g|mp3|m4a|aac|wav|flac|ogg|opus|wma|jpe?g|png|gif|webp|heic|bmp|svg|tiff?|pdf|docx?|xlsx?|pptx?|txt|csv|rtf|odt|ods|epub|zip|rar|7z|tar|gz|tgz|bz2|xz|iso|dmg|apk|exe|msi|pkg|deb|rpm|appimage|bin)$/i;

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
    if (/^[\d.]+$/.test(host) || host.includes(":")) return host; // an IP address has no site name
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
  const seen = new Set();
  const lines = ["# Netscape HTTP Cookie File"];
  const addCookie = (c) => {
    const key = `${c.domain}|${c.path}|${c.name}`;
    if (seen.has(key)) return;
    seen.add(key);
    const d = c.hostOnly ? c.domain : (c.domain.startsWith(".") ? c.domain : "." + c.domain);
    lines.push([(c.httpOnly ? "#HttpOnly_" : "") + d, c.hostOnly ? "FALSE" : "TRUE", c.path,
      c.secure ? "TRUE" : "FALSE", Math.floor(c.expirationDate || 0), c.name, c.value].join("\t"));
  };
  try {
    const base = new URL(url).hostname.split(".").slice(-2).join(".");
    for (const domain of COOKIE_DOMAINS[base] || []) {
      try {
        for (const c of await chrome.cookies.getAll({ domain })) addCookie(c);
      } catch {}
    }
    try {
      for (const c of await chrome.cookies.getAll({ url })) addCookie(c);
    } catch {}
  } catch {}
  return lines.length > 1 ? lines.join("\n") + "\n" : "";
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
        fileName: msg.file ? msg.file.split(/[\\/]/).pop() : undefined, ...(msg.txt ? { txt: msg.txt } : {}) });
      onDone && onDone(msg);
    } else if (msg.type === "paused") {
      finished = true;
      markPaused(id);
    } else if (msg.type === "error") {
      finished = true;
      updateJob(id, { status: msg.message === "Cancelled" ? "cancelled" : "error", error: msg.message, speed: "", eta: "" });
    }
  });
  port.onDisconnect.addListener(() => {
    ports.delete(id);
    pausing.delete(id);
    setTimeout(pumpQueue, 300); // a slot is free: start the next queued video
    if (!finished) {
      const err = chrome.runtime.lastError?.message;
      getJobs().then((jobs) => {
        if (["paused", "cancelled", "scheduled"].includes(jobs[id]?.status)) return;
        updateJob(id, { status: err ? "error" : "cancelled", speed: "", eta: "",
          error: err ? (helperMissing(err) ? "AnySave helper not found. Run the AnySave installer again, then restart Chrome." : "The AnySave helper stopped unexpectedly. Try again.") : null });
      });
    }
  });
  port.postMessage(payload);
}
const pausing = new Set(); // job ids whose Pause was sent but not yet confirmed
// The helper confirmed a pause. Paused by the schedule (not by the user) means "waiting for the
// next time window", which shows as Scheduled and starts again by itself.
async function markPaused(id) {
  const held = (await getJobs())[id]?.heldBySchedule;
  return updateJob(id, { status: held ? "scheduled" : "paused", speed: "", eta: "" });
}

// Video downloads wait in line: a few run at once, the rest show "Queued" and start as others
// finish (so picking a whole playlist doesn't start hundreds of downloads together).
const MAX_VIDEOS_AT_ONCE = 3;

async function startVideo({ url, format, sort, audioOnly, title, useCookies, id, queue }) {
  id = id || newId();
  await updateJob(id, {
    ...(queue !== undefined ? { queue: String(queue || "").trim() } : {}),
    kind: "video", url, title: title || `${siteLabel(url)} video`, site: siteLabel(url),
    format, sort, audioOnly: !!audioOnly, useCookies: !!useCookies,
    status: "queued", queuedAt: Date.now(), percent: 0, error: null, speed: "", eta: "",
  });
  pumpQueue();
  return id;
}

let pumping = Promise.resolve();
function pumpQueue() {
  pumping = pumping.then(async () => {
    const settings = await getSettings();
    const maxConc = Math.max(1, Number(settings.maxConcurrent || MAX_VIDEOS_AT_ONCE));
    const maxSpeedKbps = Number(settings.maxSpeedKbps || 0);
    const jobs = Object.values(await getJobs());
    let free = maxConc - jobs.filter((j) => j.kind === "video" && ports.has(j.id)).length;
    const waiting = jobs.filter((j) => j.kind === "video" && (j.status === "queued" || j.status === "scheduled"))
      .sort((a, b) => (a.queuedAt || a.createdAt || 0) - (b.queuedAt || b.createdAt || 0));
    for (const j of waiting) {
      if (scheduleBlocks(settings, j)) { // has to wait for its time window
        if (j.status !== "scheduled") await updateJob(j.id, { status: "scheduled" });
        continue;
      }
      if (free <= 0) {
        if (j.status === "scheduled") await updateJob(j.id, { status: "queued" });
        continue;
      }
      free--;
      await updateJob(j.id, { status: "starting" });
      const cookies = j.useCookies ? await cookiesFor(j.url) : null;
      // `job` lets the helper keep this download's unfinished pieces apart, so Resume continues them.
      runNative(j.id, { action: "download", job: j.id, url: j.url, format: j.format, sort: j.sort,
        audioOnly: j.audioOnly, cookies, folder: j.audioOnly ? "Music" : "Videos", maxSpeedKbps,
        subLangs: settings.subLangs || "", chapters: settings.embedChapters !== false, cover: !!settings.embedCover,
        proxy: settings.proxy || "" });
    }
  }).catch((e) => console.warn("AnySave: queue", e));
  return pumping;
}

// Speech to text (whisper.cpp in the helper): saves <name>.<lang>.srt and .txt next to the file.
const LANG_NAMES = { auto: "Auto-detected", en: "English", bn: "Bangla", hi: "Hindi", ur: "Urdu", ar: "Arabic", es: "Spanish",
  fr: "French", de: "German", pt: "Portuguese", ru: "Russian", tr: "Turkish", id: "Indonesian", zh: "Chinese", ja: "Japanese", ko: "Korean" };
async function startTranscribe({ path, name, lang, id }) {
  id = id || newId();
  lang = /^(auto|[a-z]{2,3})$/.test(lang || "") ? lang : "en";
  await updateSettings({ transcribeLang: lang });
  await updateJob(id, { kind: "transcribe", url: path, title: name, lang, site: "Local file", status: "starting", percent: 0,
    error: null, speed: "", eta: "", qualityLabel: `${LANG_NAMES[lang] || lang} text`, file: null, txt: null });
  runNative(id, { action: "transcribe", path, lang, proxy: (await getSettings()).proxy || "" });
  return id;
}

// After "Transcribe": the subtitles into the video, as a track ("embed") or drawn on the picture ("burn").
async function startSubs({ video, srt, lang, mode, name, id }) {
  id = id || newId();
  mode = mode === "burn" ? "burn" : "embed";
  await updateJob(id, { kind: "subs", url: video, srt, lang, mode, title: name, site: "Local file", status: "starting",
    percent: 0, error: null, speed: "", eta: "", file: null,
    qualityLabel: mode === "burn" ? "Captions drawn on the video" : "Subtitles inside (switchable)" });
  runNative(id, { action: "subs", video, srt, lang, mode });
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

// ---------- any file: AnySave fast multi-connection engine or Chrome's downloader ----------

const DEFAULT_SETTINGS = {
  captureAll: true,
  downloadHandler: "anysave", // "anysave" (fast engine + fallback) | "chrome" | "ask"
  minSizeMb: 0,
  excludedSites: "",
  fileTypes: "",
  loginFastSites: false,
  maxSpeedKbps: 0,
  maxConcurrent: 3,
  afterDoneAction: "none",
  videoOverlay: true,
  notifications: true, // tell the user when a download finishes or fails
  subLangs: "",        // subtitles to put inside videos, e.g. "en" or "en,fr"
  embedChapters: true, // chapter marks inside videos
  embedCover: false,   // thumbnail as cover art inside videos and music
  scheduleOn: false,   // only download inside the time window below
  scheduleStart: "01:00",
  scheduleStop: "07:00",
  scheduleQueue: "",   // "" = the window applies to every download, or the name of one queue
  queues: "",          // names of the user's extra queues, comma separated
  transcribeLang: "en", // the spoken language last chosen for "Transcribe"
  proxy: "",           // "" = system proxy, "direct", or http://host:port / socks5://host:port
};

async function getSettings() {
  const { settings = {} } = await chrome.storage.local.get("settings");
  return { ...DEFAULT_SETTINGS, ...settings };
}
let settingsChain = Promise.resolve();
function updateSettings(patch) {
  let result = DEFAULT_SETTINGS;
  settingsChain = settingsChain.then(async () => {
    const cur = await getSettings();
    result = { ...cur, ...patch };
    await chrome.storage.local.set({ settings: result });
  }).catch((e) => console.warn("AnySave: saving settings failed", e));
  return settingsChain.then(() => result);
}

// Alt-click = Chrome for this download; Ctrl/Cmd-click = force AnySave fast engine.
let lastClickModifier = null; // { url, altKey, ctrlKey, at }
function recordClickModifier({ url, altKey, ctrlKey }) {
  lastClickModifier = { url, altKey: !!altKey, ctrlKey: !!ctrlKey, at: Date.now() };
}
function consumeClickModifier(url) {
  if (!lastClickModifier || Date.now() - lastClickModifier.at > 5000) return null;
  if (lastClickModifier.url && url && lastClickModifier.url !== url) return null;
  const mod = lastClickModifier;
  lastClickModifier = null;
  return mod.altKey ? "chrome" : mod.ctrlKey ? "anysave" : null;
}

function isHostExcluded(url, excludedStr) {
  if (!excludedStr) return false;
  try {
    const host = new URL(url).hostname.toLowerCase().replace(/^www\./, "");
    const list = String(excludedStr).toLowerCase().split(/[\s,;]+/).map((s) => s.trim().replace(/^www\./, "")).filter(Boolean);
    return list.some((d) => host === d || host.endsWith("." + d));
  } catch { return false; }
}

function matchesFileTypes(nameOrUrl, typesStr) {
  if (!typesStr || !String(typesStr).trim()) return true;
  const e = extOf(nameOrUrl);
  if (!e) return true;
  const list = String(typesStr).toLowerCase().split(/[\s,;]+/).map((s) => s.replace(/^\./, "").trim()).filter(Boolean);
  return !list.length || list.includes(e);
}

const ownUrls = new Set();
// Where each of our own downloads should be saved (url -> "AnySave/<type>/<name>"). Chrome drops
// the folder given to downloads.download() when a filename listener exists, unless that listener
// suggests it again (see onDeterminingFilename below).
const ownTargets = new Map();
function chromeDownload(url, target) {
  ownUrls.add(url);
  ownTargets.set(url, target);
  setTimeout(() => { ownUrls.delete(url); ownTargets.delete(url); }, 15000);
  return chrome.downloads.download({ url, filename: target, conflictAction: "uniquify" });
}
// Waits until Chrome has finished writing a download (or gave up on it).
async function chromeDownloadSettled(downloadId, ms = 20000) {
  const t0 = Date.now();
  for (;;) {
    const [d] = await chrome.downloads.search({ id: downloadId });
    if (!d || d.state !== "in_progress" || Date.now() - t0 > ms) return d;
    await new Promise((r) => setTimeout(r, 150));
  }
}

async function startChromeFile({ url, name, title, id, qualityLabel = "Chrome" }) {
  id = id || newId();
  const fileName = safeFile(name || decodeURIComponent(new URL(url).pathname.split("/").pop() || "") || "download");
  const folder = categoryOf(fileName, "file");
  await updateJob(id, { kind: "file", engine: "chrome", url, title: title || fileName, fileName, site: siteLabel(url),
    status: "starting", percent: 0, qualityLabel, error: null, downloadId: null, doneBytes: 0, totalBytes: 0 });
  try {
    // chromeDownload also marks the URL as ours, so onCreated doesn't adopt it a second time.
    const downloadId = await chromeDownload(url, `AnySave/${folder}/${fileName}`);
    await updateJob(id, { downloadId, status: "downloading" });
    pollFiles();
  } catch (e) {
    await updateJob(id, { status: "error", error: String(e.message || e) });
  }
  return id;
}

async function startFastFile({ url, name, title, id, referrer, sha256 }) {
  id = id || newId();
  const fileName = safeFile(name || decodeURIComponent(new URL(url).pathname.split("/").pop() || "") || "download");
  const folder = categoryOf(fileName, "file");
  const settings = await getSettings();
  const cookies = settings.loginFastSites ? await cookiesFor(url) : "";
  await updateJob(id, {
    kind: "file", engine: "fast", url, title: title || fileName, fileName, site: siteLabel(url),
    status: "starting", percent: 0, qualityLabel: "Fast engine", error: null,
    downloadId: null, doneBytes: 0, totalBytes: 0, referrer: referrer || "", expectedSha256: sha256 || "",
  });

  const port = chrome.runtime.connectNative(HOST);
  ports.set(id, port);
  let finished = false;
  let handedOverId = null;

  port.onMessage.addListener(async (msg) => {
    if (msg.type === "progress") {
      if (pausing.has(id)) return;
      const conns = msg.activeConns > 1 ? `Fast · ${msg.activeConns}x` : "Fast engine";
      updateJob(id, {
        status: "downloading", percent: msg.percent || 0,
        doneBytes: msg.doneBytes || 0, totalBytes: msg.totalBytes || 0,
        speed: msg.speed || "", eta: msg.eta || "?", qualityLabel: conns,
        segments: Array.isArray(msg.segments) ? msg.segments : undefined,
      });
    } else if (msg.type === "handover" && msg.url) {
      // Offer the verified file from 127.0.0.1:<port>/<one-time-token> to Chrome so it appears
      // in Chrome's native download list (drag/open/show work natively).
      try {
        const dlName = safeFile(msg.fileName || fileName);
        const dlFolder = categoryOf(dlName, "file");
        handedOverId = await chromeDownload(msg.url, `AnySave/${dlFolder}/${dlName}`);
        await updateJob(id, { downloadId: handedOverId, fileName: dlName });
      } catch {}
    } else if (msg.type === "fallback") {
      finished = true;
      ports.delete(id);
      try { port.disconnect(); } catch {}
      await startChromeFile({ url, name: fileName, title, id, qualityLabel: "Chrome (auto-fallback)" });
    } else if (msg.type === "done") {
      finished = true;
      const jobs = await getJobs();
      const cur = jobs[id] || {};
      let finalFile = msg.file || cur.file;
      let finalName = msg.fileName || cur.fileName || fileName;
      const dlId = handedOverId || cur.downloadId;
      if (dlId) {
        // The helper has sent the last byte; give Chrome a moment to finish writing the file.
        const d = await chromeDownloadSettled(dlId);
        if (d?.filename) {
          finalFile = d.filename;
          finalName = d.filename.split(/[\\/]/).pop();
        }
      }
      const label = msg.multiConn && msg.peakConns > 1 ? `Fast (${msg.peakConns}x)` : "Fast engine";
      await updateJob(id, {
        status: "done", percent: 100, file: finalFile, fileName: finalName,
        totalBytes: msg.totalBytes || cur.totalBytes || 0, doneBytes: msg.totalBytes || cur.totalBytes || 0,
        sha256: msg.sha256 || "", qualityLabel: label, speed: "", eta: "", finishedAt: Date.now(),
        segments: undefined,
      });
    } else if (msg.type === "paused") {
      finished = true;
      await markPaused(id);
    } else if (msg.type === "error") {
      finished = true;
      await updateJob(id, { status: msg.message === "Cancelled" ? "cancelled" : "error", error: msg.message, speed: "", eta: "" });
    }
  });

  port.onDisconnect.addListener(() => {
    ports.delete(id);
    pausing.delete(id);
    if (!finished) {
      const err = chrome.runtime.lastError?.message;
      // Extension works standalone: if the helper isn't installed, fall back to Chrome's downloader automatically.
      if (helperMissing(err)) {
        startChromeFile({ url, name: fileName, title, id, qualityLabel: "Chrome" });
        return;
      }
      getJobs().then((jobs) => {
        if (["paused", "cancelled", "done", "scheduled"].includes(jobs[id]?.status)) return;
        updateJob(id, { status: err ? "error" : "cancelled", speed: "", eta: "",
          error: err ? "The AnySave helper stopped unexpectedly. Try again." : null });
      });
    }
  });

  port.postMessage({
    action: "fastDownload", job: id, url, fileName, folder,
    cookies, referrer: referrer || "", userAgent: navigator.userAgent, sha256: sha256 || "", handover: true,
    maxSpeedKbps: Number(settings.maxSpeedKbps || 0), proxy: settings.proxy || "",
  });
  return id;
}

// ---------- notifications ----------
// One notification per finished or failed download; several at once are summed up in one.
const BUSY = ["starting", "downloading", "queued", "scheduled"];
let notifyBatch = [];
let notifyTimer = null;
function queueNotification(job) {
  notifyBatch.push(job);
  if (!notifyTimer) notifyTimer = setTimeout(flushNotifications, 1500);
}
function jobName(j) { return j.fileName || j.title || j.url || "Download"; }
async function flushNotifications() {
  notifyTimer = null;
  const batch = notifyBatch;
  notifyBatch = [];
  if ((await getSettings()).notifications === false) return;
  const iconUrl = chrome.runtime.getURL("icons/icon128.png");
  const show = (id, title, message) => chrome.notifications.create(id, { type: "basic", iconUrl, title, message });
  const names = (list) => list.slice(0, 3).map(jobName).join(", ") + (list.length > 3 ? ` and ${list.length - 3} more` : "");
  const done = batch.filter((j) => j.status === "done");
  const failed = batch.filter((j) => j.status === "error");
  if (done.length === 1) show(`job:${done[0].id}`, "Download finished", jobName(done[0]));
  else if (done.length > 1) show(`done:${Date.now()}`, `${done.length} downloads finished`, names(done));
  if (failed.length === 1) show(`job:${failed[0].id}`, "Download failed", `${jobName(failed[0])}: ${failed[0].error || "unknown error"}`);
  else if (failed.length > 1) show(`failed:${Date.now()}`, `${failed.length} downloads failed`, names(failed));
}
chrome.notifications.onClicked.addListener(async (nid) => {
  chrome.notifications.clear(nid);
  if (nid === POWER_ID) return;
  const j = nid.startsWith("job:") ? (await getJobs())[nid.slice(4)] : null;
  if (j?.status === "done" && j.downloadId) chrome.downloads.show(j.downloadId);
  else if (j?.status === "done" && j.file) native({ action: "reveal", file: j.file });
  else openManager("downloads");
});

// ---------- sleep / shut down when everything has finished ----------
// A one-minute warning with a Cancel button first; starting a download also calls it off.
const POWER_ID = "anysave-power";
async function schedulePower(act) {
  await chrome.storage.session.set({ pendingPower: { act, at: Date.now() } });
  chrome.notifications.create(POWER_ID, {
    type: "basic", iconUrl: chrome.runtime.getURL("icons/icon128.png"), priority: 2, requireInteraction: true,
    title: "All downloads finished",
    message: `This computer will ${act === "sleep" ? "go to sleep" : "shut down"} in 60 seconds.`,
    buttons: [{ title: "Cancel" }],
  });
  chrome.alarms.create(POWER_ID, { delayInMinutes: 1 });
}
async function cancelPower() {
  const { pendingPower } = await chrome.storage.session.get("pendingPower");
  if (!pendingPower) return;
  await chrome.storage.session.remove("pendingPower");
  chrome.alarms.clear(POWER_ID);
  chrome.notifications.clear(POWER_ID);
}
// Runs when the minute is over: only if nothing changed in the meantime.
async function runPendingPower() {
  const { pendingPower } = await chrome.storage.session.get("pendingPower");
  if (!pendingPower) return { ok: false, why: "cancelled" };
  await chrome.storage.session.remove("pendingPower");
  chrome.notifications.clear(POWER_ID);
  const busy = Object.values(await getJobs()).some((j) => BUSY.includes(j.status));
  if (busy || (await getSettings()).afterDoneAction !== pendingPower.act) return { ok: false, why: "changed" };
  return native({ action: "power", do: pendingPower.act });
}
chrome.notifications.onButtonClicked.addListener((nid) => { if (nid === POWER_ID) cancelPower(); });
chrome.alarms.onAlarm.addListener((alarm) => { if (alarm.name === POWER_ID) runPendingPower(); });

async function checkAfterDoneAction(completedId) {
  try {
    const settings = await getSettings();
    const act = settings.afterDoneAction || "none";
    if (act === "none") return;
    const jobs = await getJobs();
    const busy = Object.values(jobs).some((j) => BUSY.includes(j.status));
    if (busy) return;
    const j = jobs[completedId];
    if (!j || j.status !== "done") return;
    if (act === "reveal") {
      if (j.downloadId) chrome.downloads.show(j.downloadId);
      else if (j.file) await native({ action: "reveal", file: j.file });
    } else if (act === "open") {
      if (j.downloadId) {
        try { await chrome.downloads.open(j.downloadId); } catch {}
      } else if (j.file) {
        await native({ action: "open", file: j.file });
      }
    } else if (act === "sleep" || act === "shutdown") {
      await schedulePower(act);
    }
  } catch {}
}

async function startFile({ url, name, title, id, forceHandler, referrer, sha256, queue }) {
  id = id || newId();
  const fileName = safeFile(name || decodeURIComponent(new URL(url).pathname.split("/").pop() || "") || "download");
  const settings = await getSettings();
  const clickChoice = consumeClickModifier(url);
  let handler = forceHandler || clickChoice || settings.downloadHandler || "anysave";

  const isHttp = /^https?:\/\//i.test(url) && !/^https?:\/\/127\.0\.0\.1:\d+\/handover\//i.test(url);
  if (!isHttp) handler = "chrome";
  else if (!forceHandler && !clickChoice) {
    if (isHostExcluded(url, settings.excludedSites) || !matchesFileTypes(fileName || url, settings.fileTypes)) {
      handler = "chrome";
    }
  }

  // Queue and schedule: a download that has to wait for its time window is kept as "Scheduled".
  const existing = (await getJobs())[id];
  const q = queue !== undefined ? String(queue || "").trim() : (existing?.queue || "");
  if (scheduleBlocks(settings, { queue: q, ignoreSchedule: existing?.ignoreSchedule })) {
    await updateJob(id, {
      kind: "file", url, title: title || fileName, fileName, site: siteLabel(url), queue: q,
      status: "scheduled", percent: 0, qualityLabel: "Scheduled", error: null, speed: "", eta: "",
      referrer: referrer || "", expectedSha256: sha256 || "", wantHandler: handler,
    });
    return id;
  }
  if (q || existing) await updateJob(id, { queue: q });

  if (handler === "ask") {
    await updateJob(id, {
      kind: "file", url, title: title || fileName, fileName, site: siteLabel(url),
      status: "ask", percent: 0, qualityLabel: "Choose downloader", error: null,
      referrer: referrer || "", expectedSha256: sha256 || "",
    });
    return id;
  }
  if (handler === "chrome") {
    return startChromeFile({ url, name: fileName, title, id });
  }
  return startFastFile({ url, name: fileName, title, id, referrer, sha256 });
}

// Chrome sometimes holds a download until the user confirms it in Chrome's own download bubble
// ("Keep" for a file from a plain http site, or one it calls dangerous). All bytes are there but
// the download never completes. AnySave hides that bubble, so say it on the row and bring the
// bubble back until the user has answered.
const heldSince = new Map(); // download id -> when it was first seen waiting
const needsChromeOk = new Set();
function chromeIsHolding(d) {
  const waiting = (d.totalBytes > 0 && d.bytesReceived >= d.totalBytes) || (d.danger && !["safe", "accepted"].includes(d.danger));
  if (!waiting) { heldSince.delete(d.id); return false; }
  if (!heldSince.has(d.id)) heldSince.set(d.id, Date.now());
  return Date.now() - heldSince.get(d.id) > 2500;
}
function setNeedsChromeOk(downloadId, on) {
  if (on === needsChromeOk.has(downloadId)) return;
  if (on) needsChromeOk.add(downloadId); else { needsChromeOk.delete(downloadId); heldSince.delete(downloadId); }
  applyChromeBubble();
}

let polling = null;
function pollFiles() {
  if (polling) return;
  polling = setInterval(async () => {
    const jobs = await getJobs();
    const active = Object.values(jobs).filter((j) => j.downloadId && j.engine !== "fast" && ["starting", "downloading", "paused"].includes(j.status));
    if (!active.some((j) => j.status !== "paused")) { clearInterval(polling); polling = null; }
    for (const j of active) {
      const [d] = await chrome.downloads.search({ id: j.downloadId });
      if (!d) continue;
      if (d.state !== "in_progress") setNeedsChromeOk(d.id, false);
      if (d.state === "complete") {
        await updateJob(j.id, { status: "done", percent: 100, file: d.filename, fileName: d.filename.split(/[\\/]/).pop(),
          totalBytes: d.totalBytes, doneBytes: d.totalBytes, speed: "", eta: "", finishedAt: j.finishedAt || Date.now(),
          qualityLabel: j.heldLabel || j.qualityLabel, heldLabel: undefined });
      } else if (chromeIsHolding(d)) {
        setNeedsChromeOk(d.id, true);
        if (!j.heldLabel) {
          await updateJob(j.id, { status: "downloading", percent: 100, doneBytes: d.bytesReceived, totalBytes: d.totalBytes,
            speed: "", eta: "", heldLabel: j.qualityLabel || "Chrome", qualityLabel: "Click Keep in Chrome's downloads" });
        }
      } else if (d.state === "interrupted") {
        await updateJob(j.id, { status: d.error === "USER_CANCELED" ? "cancelled" : "error", error: d.error, speed: "", eta: "" });
      } else if (d.paused) {
        if (j.status !== "paused" && j.status !== "scheduled") await updateJob(j.id, { status: "paused", speed: "", eta: "" });
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
  if (!j || j.engine === "fast") return;
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

async function addLink(url, title, forceHandler, queue) {
  let host = "";
  try { host = new URL(url).hostname; } catch { return { ok: false, error: "That isn't a web link." }; }
  if (VIDEO_SITES.test(host)) return { ok: true, id: await startVideo({ url, format: "bv*+ba/b", sort: "res,fps,vcodec:h264,acodec:m4a", queue }) };
  if (FILE_EXT.test(new URL(url).pathname)) return { ok: true, id: await startFile({ url, title, forceHandler, queue }) };
  // Unknown: ask the helper what the server says it is.
  const p = await native({ action: "probe", url, proxy: (await getSettings()).proxy || "" });
  // Without the helper, video pages can't be read anyway: let Chrome download the link.
  if ((p.ok && p.isFile) || p.hostMissing) return { ok: true, id: await startFile({ url, name: p.name, title, forceHandler: p.hostMissing ? "chrome" : forceHandler, queue }) };
  return { ok: true, id: await startVideo({ url, format: "bv*+ba/b", sort: "res,fps,vcodec:h264,acodec:m4a", title, queue }) };
}

// ---------- pause / resume / cancel ----------

async function pause(id) {
  const j = (await getJobs())[id];
  if (!j) return;
  if (j.status === "scheduled" && !ports.has(id)) { // waiting for its time window: just don't start it
    await updateJob(id, { status: "paused", heldBySchedule: false });
    return;
  }
  if (ports.has(id)) {
    pausing.add(id);
    if (j.engine === "fast" && j.downloadId) {
      try {
        await chrome.downloads.cancel(j.downloadId);
        await chrome.downloads.erase({ id: j.downloadId });
      } catch {}
    }
    await updateJob(id, { status: "paused", speed: "", eta: "", downloadId: j.engine === "fast" ? null : j.downloadId });
    ports.get(id).postMessage({ action: "pause" });
  } else if (j.kind === "file" && j.downloadId) {
    await chrome.downloads.pause(j.downloadId);
    await updateJob(id, { status: "paused", speed: "", eta: "" });
  } else if (j.kind === "video" && j.status === "queued") {
    await updateJob(id, { status: "paused" });
  }
}
async function resume(id) {
  const j = (await getJobs())[id];
  if (!j) return;
  if (j.kind === "file" && j.engine === "fast" && !j.downloadId) {
    await startFastFile({ url: j.url, name: j.fileName, title: j.title, id, referrer: j.referrer, sha256: j.expectedSha256 });
  } else if (j.kind === "file" && j.downloadId) {
    const [d] = await chrome.downloads.search({ id: j.downloadId });
    if (d?.canResume) { await chrome.downloads.resume(j.downloadId); await updateJob(id, { status: "downloading" }); pollFiles(); }
    else { await updateJob(id, { status: "cancelled" }); await startFile({ url: j.url, name: j.fileName, title: j.title }); }
  } else if (j.kind === "video") {
    await startVideo({ ...j, id }); // yt-dlp continues the .part file
  } else if (j.kind === "file") {
    // Never started (it was waiting for its time window): start it now.
    await startFile({ url: j.url, name: j.fileName, title: j.title, id, forceHandler: j.wantHandler, referrer: j.referrer, sha256: j.expectedSha256 });
  }
}

// ---------- scheduler and queues ----------
// "Only download between 01:00 and 07:00", for every download or for one named queue. A download
// that has to wait shows as Scheduled; when the window opens everything scheduled starts, and
// when it closes running downloads are paused until the next window. "Start now" on a row lets
// that one download run anyway.
function clockMinutes(s) {
  const m = /^(\d{1,2}):(\d{2})$/.exec(String(s || "").trim());
  if (!m || +m[1] > 23 || +m[2] > 59) return null;
  return +m[1] * 60 + +m[2];
}
function inWindow(now, start, stop) {
  const s = clockMinutes(start), e = clockMinutes(stop);
  if (s == null || e == null || s === e) return true; // unreadable or empty window: never blocks
  const t = now.getHours() * 60 + now.getMinutes();
  return s < e ? (t >= s && t < e) : (t >= s || t < e); // the window may cross midnight
}
function scheduleClock() { return self.__anysaveTestNow ? new Date(self.__anysaveTestNow) : new Date(); }
function scheduleBlocks(settings, job) {
  if (!settings.scheduleOn || job?.ignoreSchedule) return false;
  if (settings.scheduleQueue && settings.scheduleQueue !== (job?.queue || "")) return false;
  return !inWindow(scheduleClock(), settings.scheduleStart, settings.scheduleStop);
}
let ticking = Promise.resolve();
function scheduleTick() {
  ticking = ticking.then(async () => {
    const settings = await getSettings();
    for (const j of Object.values(await getJobs())) {
      if (j.kind !== "file" && j.kind !== "video") continue;
      const blocked = scheduleBlocks(settings, j);
      if (j.status === "scheduled" && !blocked) {
        await updateJob(j.id, { heldBySchedule: false });
        if (j.kind === "video") await updateJob(j.id, { status: "queued" }); // the queue pump starts it
        else await resume(j.id);
      } else if (["starting", "downloading"].includes(j.status) && blocked) {
        await updateJob(j.id, { heldBySchedule: true });
        await pause(j.id);
        await updateJob(j.id, { status: "scheduled", speed: "", eta: "" });
      }
    }
    await pumpQueue();
  }).catch((e) => console.warn("AnySave: schedule", e));
  return ticking;
}
const SCHEDULE_ALARM = "anysave-schedule";
chrome.alarms.create(SCHEDULE_ALARM, { periodInMinutes: 1 });
chrome.alarms.onAlarm.addListener((alarm) => { if (alarm.name === SCHEDULE_ALARM) scheduleTick(); });
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
  if (j.kind === "file") return startFile({ url: j.url, name: j.fileName, title: j.title, id, forceHandler: j.engine === "chrome" ? "chrome" : "anysave", referrer: j.referrer, sha256: j.expectedSha256 });
  if (j.kind === "convert") return startConvert({ path: j.url, name: j.title, spec: j.spec, id });
  if (j.kind === "transcribe") return startTranscribe({ path: j.url, name: j.title, lang: j.lang, id });
  if (j.kind === "subs") return startSubs({ video: j.url, srt: j.srt, lang: j.lang, mode: j.mode, name: j.title, id });
  return startVideo({ ...j, id });
}
async function chooseHandler(id, handler) {
  const j = (await getJobs())[id];
  if (!j) return;
  if (handler === "chrome" && j.downloadId) {
    try { await chrome.downloads.resume(j.downloadId); } catch {}
    await updateJob(id, { engine: "chrome", status: "downloading", qualityLabel: "From website" });
    pollFiles();
    return id;
  }
  if (j.downloadId) {
    try {
      await chrome.downloads.cancel(j.downloadId);
      await chrome.downloads.erase({ id: j.downloadId });
    } catch {}
    await updateJob(id, { downloadId: null });
  }
  return startFile({ url: j.url, name: j.fileName, title: j.title, id, forceHandler: handler === "chrome" ? "chrome" : "anysave", referrer: j.referrer, sha256: j.expectedSha256 });
}

// ---------- terms gate + messages ----------

async function agreed() {
  const { acceptedTermsVersion = 0 } = await chrome.storage.local.get("acceptedTermsVersion");
  return acceptedTermsVersion >= 2; // keep in sync with TERMS_VERSION in popup.js
}
const GATED = new Set(["info", "playlist", "download", "directDownload", "file", "addLink", "convert", "crawl", "transcribe", "subs"]);

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  (async () => {
    if (GATED.has(msg.type) && !(await agreed())) {
      return { ok: false, error: "Please accept the legal notice in the AnySave popup first." };
    }
    switch (msg.type) {
      case "info": {
        const cookies = msg.useCookies ? await cookiesFor(msg.url) : null;
        return native({ action: "info", url: msg.url, cookies, proxy: (await getSettings()).proxy || "" });
      }
      case "playlist": {
        const cookies = msg.useCookies ? await cookiesFor(msg.url) : null;
        return native({ action: "playlist", url: msg.url, cookies, proxy: (await getSettings()).proxy || "" });
      }
      case "download": return { ok: true, id: await startVideo(msg) };
      case "directDownload": return { ok: true, id: await startFile({ url: msg.src, name: safeFile(msg.title) + ".mp4", title: msg.title, forceHandler: msg.forceHandler, queue: msg.queue }) };
      case "file": return { ok: true, id: await startFile(msg) };
      case "addLink": return addLink(msg.url, msg.title, msg.forceHandler, msg.queue);
      case "chooseHandler": await chooseHandler(msg.id, msg.handler); return { ok: true };
      case "clickModifier": recordClickModifier(msg); return { ok: true };
      case "convert": return { ok: true, id: await startConvert(msg) };
      case "transcribe": return { ok: true, id: await startTranscribe(msg) };
      case "subs": return { ok: true, id: await startSubs(msg) };
      case "pickFile": return native({ action: "pickFile" });
      case "pause": await pause(msg.id); return { ok: true };
      case "resume":
      case "startNow":
        // Asked for by hand: this one runs even outside the time window.
        await updateJob(msg.id, { ignoreSchedule: true, heldBySchedule: false });
        if (((await getJobs())[msg.id] || {}).status === "scheduled") await scheduleTick();
        else await resume(msg.id);
        return { ok: true };
      case "moveQueue":
        await updateJob(msg.id, { queue: String(msg.queue || "").trim(), ignoreSchedule: false });
        await scheduleTick();
        return { ok: true };
      case "cancel": await cancel(msg.id); return { ok: true };
      case "retry": await retry(msg.id); return { ok: true };
      case "refreshUrl": {
        const u = String(msg.url || "").trim();
        if (!/^https?:\/\//i.test(u)) return { ok: false, error: "Please enter a valid http(s) link." };
        const j = (await getJobs())[msg.id];
        if (!j) return { ok: false, error: "Download not found" };
        await updateJob(msg.id, { url: u, site: siteLabel(u), error: null, downloadId: null });
        if (j.kind === "file") {
          await startFastFile({ url: u, name: j.fileName, title: j.title, id: msg.id, referrer: j.referrer, sha256: j.expectedSha256 });
        } else {
          await resume(msg.id);
        }
        return { ok: true };
      }
      case "remove": await mutateJobs((jobs) => { delete jobs[msg.id]; }); return { ok: true };
      case "clear":
        await mutateJobs((jobs) => {
          for (const [k, j] of Object.entries(jobs)) if (["done", "error", "cancelled"].includes(j.status)) delete jobs[k];
        });
        return { ok: true };
      case "crawl":
        return native({ action: "crawl", url: msg.url, depth: msg.depth, types: msg.types, samePath: !!msg.samePath,
          proxy: (await getSettings()).proxy || "" });
      case "fileInfo": return native({ action: "fileInfo", path: msg.path });
      case "hashFile": {
        const j = (await getJobs())[msg.id];
        if (!j?.file) return { ok: false, error: "File not found" };
        const res = await native({ action: "hash", path: j.file });
        if (res?.ok && res.sha256) await updateJob(msg.id, { sha256: res.sha256 });
        return res;
      }
      case "reveal": {
        const j = msg.id ? (await getJobs())[msg.id] : null;
        if (j?.downloadId) { chrome.downloads.show(j.downloadId); return { ok: true }; }
        return native({ action: "reveal", file: msg.file || j?.file });
      }
      case "revealDirect": chrome.downloads.show(msg.downloadId); return { ok: true };
      case "open": {
        const j = (await getJobs())[msg.id];
        if (!j?.file) return { ok: false, error: "File not found" };
        return native({ action: "open", file: j.txt || j.file }); // a transcript opens as text
      }
      case "openManager": openManager(msg.tab); return { ok: true };
      case "seen": await markSeen(); return { ok: true };
      case "setCapture": {
        await updateSettings({ captureAll: !!msg.on });
        return { ok: true };
      }
      case "setSettings": {
        const next = await updateSettings(msg.patch || {});
        syncClickKeyScript();
        if (Object.keys(msg.patch || {}).some((k) => k.startsWith("schedule"))) scheduleTick();
        return { ok: true, settings: next };
      }
      case "getSettings": {
        return { ok: true, settings: await getSettings() };
      }
      case "diagnostics": {
        const extVer = chrome.runtime.getManifest().version;
        const res = await native({ action: "diagnostics", extensionVersion: extVer });
        const jobs = Object.values(await getJobs());
        const failed = jobs.filter((j) => j.status === "error").slice(0, 5)
          .map((j) => `  - [${j.kind}] ${j.site || ""}: ${j.error || "unknown"}`).join("\n");
        let text = res?.ok && res.text
          ? res.text
          : `AnySave Diagnostics\n===================\nExtension: ${extVer}\nHelper:    not reachable (${res?.error || "missing"})\n`;
        if (failed) text += `\nRecent failed jobs:\n${failed}\n`;
        return { ok: true, text, helperOk: !!res?.ok };
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
    chrome.contextMenus.create({ id: "as-link", title: "Download link with AnySave", contexts: ["link"] });
    chrome.contextMenus.create({ id: "as-media", title: "Download with AnySave", contexts: ["video", "audio", "image"] });
    chrome.contextMenus.create({ id: "as-page", title: "Download this page's video with AnySave", contexts: ["page"] });
    chrome.contextMenus.create({ id: "as-manager", title: "Open AnySave download manager", contexts: ["action"] });
  });
  // Move pre-2.0 session jobs into the persistent store.
  const old = (await chrome.storage.session.get("jobs")).jobs;
  if (old) await mutateJobs((jobs) => { for (const [k, j] of Object.entries(old)) if (!jobs[k]) jobs[k] = j; });
});

chrome.contextMenus.onClicked.addListener(async (info, tab) => {
  if (info.menuItemId === "as-manager") return openManager();
  if (!(await agreed())) { chrome.action.openPopup?.(); return; }
  let target = null;
  if (info.menuItemId === "as-link") target = info.linkUrl;
  if (info.menuItemId === "as-media") target = info.srcUrl?.startsWith("blob:") ? info.pageUrl : info.srcUrl;
  if (info.menuItemId === "as-page") target = info.pageUrl;
  if (!target) return;
  // Images and plain media files go straight to Chrome's downloader.
  if (info.mediaType === "image" || (info.srcUrl && FILE_EXT.test(new URL(info.srcUrl).pathname))) {
    await startFile({ url: target, title: tab?.title });
  } else {
    await addLink(target, info.menuItemId === "as-page" ? tab?.title : undefined);
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
  const hide = captureCache && !needsChromeOk.size && (await iconPinned());
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

// Names Chrome has decided for downloads (download id -> file name). When a download is created
// Chrome often doesn't know the name yet (it comes with the server's answer, e.g. "report.zip"
// for a link ending in /download); it arrives a moment later in onDeterminingFilename.
const determinedNames = new Map();
async function settledItem(item, ms = 1500) {
  const t0 = Date.now();
  while (!determinedNames.has(item.id) && Date.now() - t0 < ms) await new Promise((r) => setTimeout(r, 50));
  const [fresh] = await chrome.downloads.search({ id: item.id });
  const name = determinedNames.get(item.id);
  determinedNames.delete(item.id);
  return { ...item, ...(fresh || {}), filename: name || fresh?.filename || item.filename };
}

async function adoptDownload(item) {
  if (!isNewDownload(item) || (await isOurs(item)) || !(await captureOn())) return;
  if (!(item.filename || "").split(/[\\/]/).pop()) item = await settledItem(item);
  const url = item.finalUrl || item.url || "";
  const clickChoice = consumeClickModifier(url) || consumeClickModifier(item.url);
  if (clickChoice === "chrome") return; // Alt-click: leave this one untouched to Chrome

  const settings = await getSettings();
  const name = (item.filename || "").split(/[\\/]/).pop() || decodeURIComponent(url.split(/[?#]/)[0].split("/").pop() || "") || "download";

  if (clickChoice !== "anysave") {
    if (isHostExcluded(url, settings.excludedSites)) return;
    if (!matchesFileTypes(name || url, settings.fileTypes)) return;
    const minBytes = Number(settings.minSizeMb || 0) * 1048576;
    if (minBytes > 0 && item.totalBytes > 0 && item.totalBytes < minBytes) return;
  }

  const handler = clickChoice || settings.downloadHandler || "anysave";
  const canFastAccelerate = /^https?:\/\//i.test(url) &&
    !/^https?:\/\/127\.0\.0\.1:\d+\/handover\//i.test(url) &&
    (clickChoice === "anysave" || item.totalBytes >= 1024 * 1024 || FILE_EXT.test(new URL(url).pathname));

  if (handler === "ask" && canFastAccelerate) {
    try { await chrome.downloads.pause(item.id); } catch {}
    const id = newId();
    await updateJob(id, {
      kind: "file", url, title: name, fileName: name, site: /^https?:/.test(url) ? siteLabel(url) : "Web",
      downloadId: item.id, status: "ask", percent: 0, referrer: item.referrer || "",
      totalBytes: item.totalBytes > 0 ? item.totalBytes : 0, qualityLabel: "Choose downloader",
    });
    return;
  }

  if (handler === "anysave" && canFastAccelerate) {
    try {
      await chrome.downloads.cancel(item.id);
      await chrome.downloads.erase({ id: item.id });
    } catch {}
    await startFile({ url, name, title: name, referrer: item.referrer || "", forceHandler: "anysave" }); // through the schedule check
    return;
  }

  const id = newId();
  await updateJob(id, {
    kind: "file", engine: "chrome", url, title: name, fileName: name, site: /^https?:/.test(url) ? siteLabel(url) : "Web",
    downloadId: item.id, status: item.paused ? "paused" : "downloading", percent: 0,
    totalBytes: item.totalBytes > 0 ? item.totalBytes : 0, qualityLabel: "From website",
  });
  pollFiles();
}
chrome.downloads.onCreated.addListener(adoptDownload);

async function syncClickKeyScript() {
  try {
    const settings = await getSettings();
    const hasAll = await chrome.permissions.contains({ origins: ["*://*/*"] });
    const matches = hasAll
      ? ["*://*/*"]
      : ["*://*.youtube.com/*", "*://*.facebook.com/*", "*://*.instagram.com/*", "*://*.tiktok.com/*", "*://*.vimeo.com/*"];
    await chrome.scripting.unregisterContentScripts({ ids: ["anysave-click-keys", "anysave-video-overlay"] }).catch(() => {});
    const scripts = [{
      id: "anysave-click-keys",
      js: ["click-keys.js"],
      matches,
      runAt: "document_start",
      allFrames: true,
    }];
    if (settings.videoOverlay !== false) {
      scripts.push({
        id: "anysave-video-overlay",
        js: ["video-overlay.js"],
        matches,
        runAt: "document_idle",
        allFrames: false,
      });
    }
    await chrome.scripting.registerContentScripts(scripts);
  } catch {}
}
syncClickKeyScript();

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
  const target = ownTargets.get(item.url) || ownTargets.get(item.finalUrl);
  if (target) { suggest({ filename: target, conflictAction: "uniquify" }); return; }
  if (item.byExtensionId === chrome.runtime.id || ownUrls.has(item.url) || !captureCache) { suggest(); return; }
  const base = (item.filename || "download").split(/[\\/]/).pop();
  determinedNames.set(item.id, base);
  setTimeout(() => determinedNames.delete(item.id), 10000);
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

function drawRing(img, size, fraction, spinAt = null) {
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
  if (spinAt != null) { // a bright arc chasing round the ring: "working on it"
    g.strokeStyle = "#ffb36b";
    g.beginPath(); g.arc(cx, cy, r, spinAt, spinAt + Math.PI * 0.45); g.stroke();
  }
  return g.getImageData(0, 0, size, size);
}

// The ring glides to the real progress instead of jumping, and while a download is still
// starting (no progress yet) a bright arc spins round it, so the icon shows activity at once.
const ring = { target: 0, shown: 0, drawn: null, spin: false, phase: 0, timer: null };
async function ringTick() {
  ring.shown += (ring.target - ring.shown) * 0.3;
  if (Math.abs(ring.target - ring.shown) < 0.003) ring.shown = ring.target;
  ring.phase = (ring.phase + 0.55) % (Math.PI * 2);
  if (!ring.spin && ring.drawn === ring.shown) return; // nothing moves: don't redraw
  ring.drawn = ring.shown;
  try {
    const icons = await loadBaseIcons();
    if (!ring.timer) return; // downloads ended meanwhile; the plain icon is already back
    const at = ring.spin ? ring.phase : null;
    await chrome.action.setIcon({ imageData: { 16: drawRing(icons[16], 16, ring.shown, at), 32: drawRing(icons[32], 32, ring.shown, at) } });
  } catch {}
}
function startRing() { if (!ring.timer) ring.timer = setInterval(ringTick, 120); }
function stopRing() { clearInterval(ring.timer); ring.timer = null; ring.shown = 0; ring.drawn = null; }

// Finished downloads the user hasn't looked at yet keep a green ✓ (with their number) on the
// icon until the popup or the manager is opened, like Chrome's own download icon. "Seen" is
// kept in storage, so this survives the service worker being put to sleep.
let lastIconState = "";
async function updateActionIcon() {
  const jobs = Object.values(await getJobs());
  const active = jobs.filter((j) => ["starting", "downloading"].includes(j.status));
  if (!active.length) {
    stopRing();
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
  ring.target = Math.max(0, Math.min(1, fraction || 0));
  ring.spin = active.some((j) => j.status === "starting" || (!(j.percent > 0) && !(j.doneBytes > 0)));
  startRing();
  const state = `${pct}|${active.length}`;
  if (state === lastIconState) return;
  lastIconState = state;
  chrome.action.setBadgeBackgroundColor({ color: "#e5484d" });
  chrome.action.setBadgeTextColor?.({ color: "#ffffff" });
  // The overall percent, for one download or several (how many is in the tooltip).
  chrome.action.setBadgeText({ text: `${pct}%` });
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
    if (j.kind === "video" || (j.kind === "file" && j.engine === "fast" && !j.downloadId)) Object.assign(j, { status: "paused", speed: "", eta: "" });
    else if (j.kind === "convert") Object.assign(j, { status: "error", error: "Stopped when Chrome closed. Retry to convert again.", speed: "", eta: "" });
    else if (j.kind === "transcribe" || j.kind === "subs") Object.assign(j, { status: "error", error: "Stopped when Chrome closed. Retry to start again.", speed: "", eta: "" });
  }
});
// Start queued videos again (the queue lives in storage, so it survives restarts), and apply
// the schedule (the time window may have opened or closed while Chrome was shut).
scheduleTick();
// Resume polling file downloads after the service worker restarts.
pollFiles();
updateActionIcon();
