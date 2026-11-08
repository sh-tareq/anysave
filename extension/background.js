// VidGrab service worker: talks to the native host (yt-dlp wrapper) and
// keeps download jobs alive while the popup is closed.
const HOST = "com.vidgrab.host";
const ports = new Map(); // jobId -> native Port

async function getJobs() {
  return (await chrome.storage.session.get("jobs")).jobs || {};
}
async function updateJob(id, patch) {
  const jobs = await getJobs();
  jobs[id] = { ...(jobs[id] || {}), ...patch };
  await chrome.storage.session.set({ jobs });
}

// Login cookies for the site of `url`, in Netscape cookies.txt format.
// macOS blocks other programs from reading Chrome's cookie database, so the
// extension reads them through the chrome.cookies API instead, only for the
// site being downloaded from.
const COOKIE_DOMAINS = {
  "youtube.com": ["youtube.com"],
  "youtu.be": ["youtube.com"],
  "facebook.com": ["facebook.com"],
  "fb.watch": ["facebook.com"],
  "instagram.com": ["instagram.com"],
  "tiktok.com": ["tiktok.com"],
};

async function cookiesFor(url) {
  const host = new URL(url).hostname;
  const base = host.split(".").slice(-2).join(".");
  const domains = COOKIE_DOMAINS[base] || [];
  const lines = ["# Netscape HTTP Cookie File"];
  for (const domain of domains) {
    for (const c of await chrome.cookies.getAll({ domain })) {
      const d = c.hostOnly ? c.domain : (c.domain.startsWith(".") ? c.domain : "." + c.domain);
      lines.push([
        (c.httpOnly ? "#HttpOnly_" : "") + d,
        c.hostOnly ? "FALSE" : "TRUE",
        c.path,
        c.secure ? "TRUE" : "FALSE",
        Math.floor(c.expirationDate || 0),
        c.name,
        c.value,
      ].join("\t"));
    }
  }
  return lines.join("\n") + "\n";
}

async function startDownload({ url, format, sort, audioOnly, title, useCookies }) {
  const id = String(Date.now());
  updateJob(id, { id, url, title, status: "starting", percent: 0 });

  const port = chrome.runtime.connectNative(HOST);
  ports.set(id, port);
  let finished = false;

  port.onMessage.addListener((msg) => {
    if (msg.type === "progress") {
      updateJob(id, { status: "downloading", percent: msg.percent, speed: msg.speed, eta: msg.eta });
    } else if (msg.type === "status") {
      updateJob(id, { status: msg.status });
    } else if (msg.type === "done") {
      finished = true;
      updateJob(id, { status: "done", percent: 100, file: msg.file });
    } else if (msg.type === "error") {
      finished = true;
      updateJob(id, { status: "error", error: msg.message });
    }
  });
  port.onDisconnect.addListener(() => {
    ports.delete(id);
    if (!finished) {
      const err = chrome.runtime.lastError?.message;
      updateJob(id, { status: err ? "error" : "cancelled", error: err });
    }
  });
  const cookies = useCookies ? await cookiesFor(url) : null;
  port.postMessage({ action: "download", url, format, sort, audioOnly, cookies });
  return id;
}

async function agreed() {
  const { acceptedTermsVersion = 0 } = await chrome.storage.local.get("acceptedTermsVersion");
  return acceptedTermsVersion >= 1;
}

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  // Nothing is looked up or downloaded before the user accepts the legal notice in the popup.
  if (msg.type === "info" || msg.type === "download" || msg.type === "directDownload") {
    agreed().then((ok) => {
      if (!ok) sendResponse({ ok: false, error: "Please accept the legal notice in the VidGrab popup first." });
      else handle(msg, sendResponse);
    });
    return true;
  }
  return handle(msg, sendResponse);
});

function handle(msg, sendResponse) {
  if (msg.type === "directDownload") {
    startDirect(msg).then((id) => sendResponse({ ok: true, id }), (e) => sendResponse({ ok: false, error: String(e) }));
    return true;
  }
  if (msg.type === "revealDirect") {
    chrome.downloads.show(msg.downloadId);
    sendResponse({ ok: true });
    return;
  }
  if (msg.type === "info") {
    (async () => {
      const cookies = msg.useCookies ? await cookiesFor(msg.url) : null;
      chrome.runtime.sendNativeMessage(HOST, { action: "info", url: msg.url, cookies }, (res) => {
        if (chrome.runtime.lastError) {
          sendResponse({ ok: false, error: chrome.runtime.lastError.message, hostMissing: true });
        } else {
          sendResponse(res);
        }
      });
    })();
    return true; // async response
  }
  if (msg.type === "download") {
    startDownload(msg).then((id) => sendResponse({ ok: true, id }));
    return true;
  }
  if (msg.type === "cancel") {
    getJobs().then((jobs) => { const d = jobs[msg.id]?.downloadId; if (d) chrome.downloads.cancel(d); });
    ports.get(msg.id)?.disconnect();
    ports.delete(msg.id);
    updateJob(msg.id, { status: "cancelled" });
    sendResponse({ ok: true });
  }
  if (msg.type === "clear") {
    getJobs().then((jobs) => {
      for (const [k, j] of Object.entries(jobs)) {
        if (["done", "error", "cancelled"].includes(j.status)) delete jobs[k];
      }
      chrome.storage.session.set({ jobs }).then(() => sendResponse({ ok: true }));
    });
    return true;
  }
  if (msg.type === "reveal") {
    chrome.runtime.sendNativeMessage(HOST, { action: "reveal", file: msg.file }, () => {
      void chrome.runtime.lastError;
      sendResponse({ ok: true });
    });
    return true;
  }
}

// ---------- Direct downloads (ad libraries and other pages with plain video files) ----------
// These are saved by Chrome itself into Downloads/VidGrab, so no helper app is needed.

function safeFile(s) {
  return String(s || "video").replace(/[\\/:*?"<>|\n\r\t~]+/g, " ").replace(/\s+/g, " ").trim().slice(0, 80) || "video";
}

async function startDirect({ src, title }) {
  const id = String(Date.now()) + Math.floor(Math.random() * 1000);
  await updateJob(id, { id, url: src, title, status: "starting", percent: 0 });
  const downloadId = await chrome.downloads.download({
    url: src, filename: `VidGrab/${safeFile(title)}.mp4`, conflictAction: "uniquify",
  });
  await updateJob(id, { downloadId, status: "downloading" });
  pollDirect();
  return id;
}

let polling = null;
function pollDirect() {
  if (polling) return;
  polling = setInterval(async () => {
    const jobs = await getJobs();
    const active = Object.values(jobs).filter((j) => j.downloadId && ["starting", "downloading"].includes(j.status));
    if (!active.length) { clearInterval(polling); polling = null; return; }
    for (const j of active) {
      const [d] = await chrome.downloads.search({ id: j.downloadId });
      if (!d) continue;
      if (d.state === "complete") await updateJob(j.id, { status: "done", percent: 100, file: d.filename });
      else if (d.state === "interrupted") await updateJob(j.id, { status: d.error === "USER_CANCELED" ? "cancelled" : "error", error: d.error });
      else if (d.totalBytes > 0) {
        const pct = (d.bytesReceived / d.totalBytes) * 100;
        await updateJob(j.id, { percent: pct, speed: "", eta: d.estimatedEndTime ? Math.max(0, Math.round((new Date(d.estimatedEndTime) - Date.now()) / 1000)) + "s" : "?" });
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
  if (delta.state.current === "complete") {
    const [d] = await chrome.downloads.search({ id: delta.id });
    await updateJob(j.id, { status: "done", percent: 100, file: d?.filename });
  } else if (delta.state.current === "interrupted") {
    const [d] = await chrome.downloads.search({ id: delta.id });
    await updateJob(j.id, { status: d?.error === "USER_CANCELED" ? "cancelled" : "error", error: d?.error || "Download failed" });
  }
});
