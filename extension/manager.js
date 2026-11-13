// AnySave download manager + converter page.
const $ = (id) => document.getElementById(id);
const send = (msg) => chrome.runtime.sendMessage(msg);
let jobs = {};
let filter = "all";
let currentSettings = {}; // the latest settings (queues, schedule)

// ---------- tabs ----------
function showTab(name) {
  document.querySelectorAll(".tab").forEach((t) => t.classList.toggle("active", t.dataset.tab === name));
  $("downloads").hidden = name !== "downloads";
  $("convert").hidden = name !== "convert";
  history.replaceState(null, "", "#" + name);
  if (name === "convert") fillDownloadsPicker();
}
document.querySelectorAll(".tab").forEach((t) => t.addEventListener("click", () => showTab(t.dataset.tab)));

// ---------- helpers ----------
function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}
function extOf(n) { const m = String(n || "").split(/[?#]/)[0].match(/\.([a-z0-9]{1,8})$/i); return m ? m[1].toLowerCase() : ""; }
function category(j) {
  if (j.kind === "convert") return "Converted";
  const e = extOf(j.fileName || j.file || j.title);
  if (j.audioOnly || /^(mp3|m4a|aac|wav|flac|ogg|opus|wma)$/.test(e)) return "Music";
  if (j.kind === "video" || /^(mp4|mkv|webm|mov|avi|m4v|flv|wmv|ts|mpe?g|3gp)$/.test(e)) return "Videos";
  if (/^(jpe?g|png|gif|webp|heic|bmp|svg|tiff?)$/.test(e)) return "Pictures";
  if (/^(pdf|docx?|xlsx?|pptx?|txt|csv|rtf|odt|ods|epub)$/.test(e)) return "Documents";
  if (/^(zip|rar|7z|tar|gz|tgz|bz2|xz|iso|dmg)$/.test(e)) return "Archives";
  if (/^(apk|exe|msi|pkg|deb|rpm|appimage)$/.test(e)) return "Apps";
  return "Other";
}
// Material-style line icons (24×24), drawn with currentColor so they follow the theme.
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
  retry: "M17.65 6.35A7.96 7.96 0 0 0 12 4a8 8 0 1 0 7.73 10h-2.08A6 6 0 1 1 12 6c1.66 0 3.14.69 4.22 1.78L13 11h7V4l-2.35 2.35z",
  link: "M3.9 12c0-1.71 1.39-3.1 3.1-3.1h4V7H7c-2.76 0-5 2.24-5 5s2.24 5 5 5h4v-1.9H7c-1.71 0-3.1-1.39-3.1-3.1zM8 13h8v-2H8v2zm9-6h-4v1.9h4c1.71 0 3.1 1.39 3.1 3.1s-1.39 3.1-3.1 3.1h-4V17h4c2.76 0 5-2.24 5-5s-2.24-5-5-5z",
  check: "M9 16.17 4.83 12l-1.42 1.41L9 19 21 7l-1.41-1.41z",
  remove: "M6 19a2 2 0 0 0 2 2h8a2 2 0 0 0 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z",
};
const svg = (name, size = 20) =>
  `<svg viewBox="0 0 24 24" width="${size}" height="${size}" aria-hidden="true"><path fill="currentColor" d="${P[name] || P.Other}"/></svg>`;

/** "Me at the zoo [jNQXAC9IVRw] 240p.mp4" -> "Me at the zoo 240p" (IDs and extension hidden). */
function displayName(j) {
  const raw = j.fileName || j.title || j.url || "";
  const e = extOf(raw);
  const noExt = e && e.length <= 5 ? raw.slice(0, -(e.length + 1)) : raw;
  return noExt.replace(/\s*\[[^\]]{4,}\]/g, "").replace(/\s+/g, " ").trim() || raw;
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
  return new Date(t).toLocaleDateString();
}
const ACTIVE = ["starting", "downloading"];
const FINISHED = ["done", "error", "cancelled"];

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

// ---------- downloads list ----------
let openRefreshJobId = null;
const speedSamples = [];

function parseSpeedBps(s) {
  const m = String(s || "").match(/([\d.]+)\s*(B|KB|MB|GB)\/s/i);
  if (!m) return 0;
  const v = parseFloat(m[1]) || 0;
  const u = m[2].toUpperCase();
  const mult = u === "GB" ? 1073741824 : u === "MB" ? 1048576 : u === "KB" ? 1024 : 1;
  return v * mult;
}

function updateSpeedStats(all) {
  const bar = $("speedStatsBar");
  if (!bar) return;
  const active = all.filter((j) => j.status === "downloading");
  const done = all.filter((j) => j.status === "done");
  const totalSavedBytes = done.reduce((acc, j) => acc + (j.totalBytes || 0), 0);
  const curBps = active.reduce((acc, j) => acc + parseSpeedBps(j.speed), 0);

  if (active.length === 0 && done.length === 0) {
    bar.hidden = true;
    return;
  }
  bar.hidden = false;
  if (active.length > 0) {
    speedSamples.push(curBps);
    if (speedSamples.length > 28) speedSamples.shift();
  }
  const speedStr = curBps >= 1048576
    ? `${(curBps / 1048576).toFixed(1)} MB/s`
    : curBps > 0 ? `${Math.round(curBps / 1024)} KB/s` : "Idle";
  $("liveSpeedText").textContent = active.length > 0 ? `⬇ ${speedStr}` : "Ready";
  const parts = [];
  if (active.length > 0) parts.push(`${active.length} active`);
  if (done.length > 0) parts.push(`${done.length} saved${totalSavedBytes > 0 ? ` (${size(totalSavedBytes)})` : ""}`);
  $("liveStatsMeta").textContent = parts.join(" · ");

  const path = $("sparkPath");
  if (path && speedSamples.length >= 2) {
    const maxV = Math.max(1024, ...speedSamples);
    const pts = speedSamples.map((v, i) => {
      const x = (i / (speedSamples.length - 1)) * 136 + 2;
      const y = 26 - (v / maxV) * 22;
      return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
    });
    path.setAttribute("d", pts.join(" "));
  }
}

function render() {
  if (dragging) { redrawAfterDrag = render; return; }
  if (openRefreshJobId && document.activeElement?.closest(".refresh-box")) return;
  const all = Object.values(jobs).sort((a, b) => (b.createdAt || b.id) - (a.createdAt || a.id));
  const activeCount = all.filter((j) => ACTIVE.includes(j.status)).length;
  $("activeBadge").hidden = !activeCount;
  $("activeBadge").textContent = activeCount;
  updateSpeedStats(all);
  const shown = all.filter((j) => {
    const c = category(j);
    switch (filter) {
      case "all": return j.status !== "cancelled";
      case "failed": return j.status === "error" || j.status === "cancelled";
      default: if (filter.startsWith("q:")) return (j.queue || "") === filter.slice(2) && j.status !== "cancelled";
    }
    switch (filter) {
      case "files": return j.status === "done" && !["Videos", "Music", "Converted"].includes(c);
      default: return j.status === "done" && c === filter;
    }
  });
  // Running and paused items always stay on top.
  shown.sort((a, b) => (FINISHED.includes(a.status) - FINISHED.includes(b.status)));
  $("empty").hidden = shown.length > 0;
  const ul = $("list");
  ul.innerHTML = "";
  for (const j of shown) ul.appendChild(row(j));
}

function row(j) {
  const li = document.createElement("li");
  li.className = "item";
  const pct = Math.max(0, Math.min(100, j.percent || 0));
  const running = ACTIVE.includes(j.status);
  const paused = j.status === "paused";
  const scheduled = j.status === "scheduled";
  const queued = j.status === "queued" || scheduled;
  const ask = j.status === "ask";
  let line;
  if (running) {
    const prog = j.kind === "file" && j.totalBytes > 0 ? `${size(j.doneBytes)} of ${size(j.totalBytes)}` : "";
    line = j.status === "starting" ? "Starting…"
      : [`${pct.toFixed(0)}%`, prog, j.speed, j.eta && `ETA ${j.eta}`].filter(Boolean).join(" · ");
  } else if (paused) line = ["Paused", `${pct.toFixed(0)}%`].join(" · ");
  else if (scheduled) line = `Scheduled · starts at ${currentSettings.scheduleStart || "the set time"}`;
  else if (queued) line = "Queued · starts when a download finishes";
  else if (ask) line = ["Choose who downloads this file", size(j.totalBytes), j.site].filter(Boolean).join(" · ");
  else if (j.status === "done") line = ["Done", j.qualityLabel, size(j.totalBytes), j.site, ago(j.finishedAt || j.createdAt)].filter(Boolean).join(" · ");
  else if (j.status === "error") line = `<span class="err">${esc(j.error || "Failed")}</span>`;
  else line = "Cancelled";

  let progBarHtml = "";
  if (running || paused || queued) {
    if (Array.isArray(j.segments) && j.segments.length > 1 && j.totalBytes > 0) {
      const slices = j.segments.map((s) => {
        const leftPct = Math.max(0, Math.min(100, (s.start / j.totalBytes) * 100));
        const doneLen = Math.max(0, (s.current - s.start));
        const widthPct = Math.max(0, Math.min(100 - leftPct, (doneLen / j.totalBytes) * 100));
        return `<div class="seg-slice" style="left:${leftPct.toFixed(2)}%;width:${widthPct.toFixed(2)}%"></div>`;
      }).join("");
      progBarHtml = `<div class="seg-bar ${paused || queued ? "paused" : ""}" title="${j.segments.length} parallel segments">${slices}</div>`;
    } else {
      progBarHtml = `<div class="bar ${paused || queued ? "paused" : ""}"><div style="width:${pct}%"></div></div>`;
    }
  }

  li.innerHTML = `
    <div class="icon">${svg(category(j), 22)}${j.status === "done" ? `<span class="done" title="Finished">${svg("check", 13)}</span>` : ""}</div>
    <div class="info">
      <div class="name" title="${esc(j.fileName || j.title || j.url)}">${esc(displayName(j))}</div>
      ${progBarHtml}
      <div class="meta">${j.queue ? `<span class="tag queue" title="Queue">${esc(j.queue)}</span>` : ""}${j.status === "done" && extOf(j.fileName || j.file) ? `<span class="tag">${esc(extOf(j.fileName || j.file).toUpperCase().slice(0, 4))}</span>` : ""}${j.status === "error" ? line : j.status === "done" ? esc(line).replace(/^Done/, '<span class="ok">Done</span>') : esc(line)}</div>
      <div class="refresh-slot"></div>
    </div>
    <div class="btns"></div>`;
  const btns = li.querySelector(".btns");
  const add = (icon, title, fn) => {
    const b = Object.assign(document.createElement("button"), { className: "ib", title });
    b.setAttribute("aria-label", title);
    b.innerHTML = svg(icon, 18);
    b.onclick = fn; btns.appendChild(b);
  };
  if (ask) {
    const fastBtn = Object.assign(document.createElement("button"), { className: "ask-btn fast", textContent: "AnySave (Fast)" });
    fastBtn.onclick = () => send({ type: "chooseHandler", id: j.id, handler: "anysave" });
    const chrBtn = Object.assign(document.createElement("button"), { className: "ask-btn", textContent: "Chrome" });
    chrBtn.onclick = () => send({ type: "chooseHandler", id: j.id, handler: "chrome" });
    btns.append(fastBtn, chrBtn);
    add("close", "Cancel", () => send({ type: "cancel", id: j.id }));
  }
  if (running && j.kind !== "convert") add("pause", "Pause", () => send({ type: "pause", id: j.id }));
  if (paused) add("play", "Resume", () => send({ type: "resume", id: j.id }));
  if (scheduled) add("play", "Start now", () => send({ type: "startNow", id: j.id }));
  if (queued) add("pause", "Pause", () => send({ type: "pause", id: j.id }));
  // Waiting downloads can be moved to another queue.
  if ((queued || paused) && queueNames().length) {
    const sel = document.createElement("select");
    sel.className = "row-queue"; sel.title = "Move to queue";
    sel.innerHTML = ["", ...queueNames()].map((q) => `<option value="${esc(q)}"${(j.queue || "") === q ? " selected" : ""}>${esc(q || "Main queue")}</option>`).join("");
    sel.onchange = () => send({ type: "moveQueue", id: j.id, queue: sel.value });
    btns.appendChild(sel);
  }
  if (running || paused || queued) add("close", "Cancel", () => send({ type: "cancel", id: j.id }));
  if (j.status === "done" && (j.file || j.downloadId)) {
    const open = () => openDownload(j).then((r) => { if (r && !r.ok) alert(r.error || "Couldn't open the file"); });
    add("open", "Open", open);
    add("folder", "Show in folder", () => send({ type: "reveal", id: j.id }));
    // Drag the row out (to the desktop, a folder, a chat…), or double-click to open.
    li.draggable = true;
    li.classList.add("drag");
    li.title = "Drag the file anywhere, or double-click to open";
    li.ondragstart = (e) => startDrag(e, j);
    li.ondblclick = (e) => { if (!e.target.closest("button")) open(); };
  }
  if (j.status === "done" && j.file) {
    add("check", "Check against a SHA-256 checksum", () => verifyChecksum(j));
    if (["Videos", "Music"].includes(category(j)))
      add("convert", "Convert", () => { showTab("convert"); choose({ path: j.file, name: j.fileName || j.title }); });
  }
  if (paused || j.status === "error") {
    add("link", "Refresh expired link", () => {
      openRefreshJobId = openRefreshJobId === j.id ? null : j.id;
      render();
    });
  }
  if (openRefreshJobId === j.id && (paused || j.status === "error")) {
    const slot = li.querySelector(".refresh-slot");
    slot.innerHTML = `<div class="refresh-box">
      <input type="url" class="refresh-input" placeholder="Paste fresh link to continue from saved parts…" value="${esc(j.url || "")}" />
      <button type="button" class="refresh-go">Continue</button>
    </div>`;
    const inp = slot.querySelector(".refresh-input");
    const go = slot.querySelector(".refresh-go");
    const submitRefresh = async () => {
      const u = inp.value.trim();
      if (!u) return;
      openRefreshJobId = null;
      await send({ type: "refreshUrl", id: j.id, url: u });
      render();
    };
    go.onclick = submitRefresh;
    inp.onkeydown = (ev) => { if (ev.key === "Enter") { ev.preventDefault(); submitRefresh(); } };
  }
  if (j.status === "error" || j.status === "cancelled") add("retry", "Retry", () => send({ type: "retry", id: j.id }));
  if (FINISHED.includes(j.status)) add("remove", "Remove from list", () => send({ type: "remove", id: j.id }));
  return li;
}

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
function listMsg(text, isErr) {
  const m = $("addMsg");
  m.hidden = false; m.className = "msg" + (isErr ? " err" : ""); m.textContent = text;
  setTimeout(() => { m.hidden = true; }, 7000);
}
$("exportList")?.addEventListener("click", () => {
  const items = exportItems();
  if (!items.length) { listMsg("There are no links to export yet.", true); return; }
  saveTextFile(`anysave-links-${new Date().toISOString().slice(0, 10)}.json`,
    JSON.stringify({ app: "AnySave", version: 1, exportedAt: new Date().toISOString(), items }, null, 2));
  listMsg(`Exported ${items.length} link${items.length === 1 ? "" : "s"}.`);
});
$("importList")?.addEventListener("click", () => $("importFile").click());
$("importFile")?.addEventListener("change", async (e) => {
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

document.querySelectorAll("#filters .chip").forEach((c) => c.addEventListener("click", () => {
  document.querySelectorAll("#filters .chip").forEach((x) => x.classList.toggle("active", x === c));
  filter = c.dataset.f; render();
}));
// With a queue chip selected, Pause all / Resume all act on that queue only.
const inShownQueue = (j) => !filter.startsWith("q:") || (j.queue || "") === filter.slice(2);
$("pauseAll").onclick = () => Object.values(jobs).filter((j) => inShownQueue(j) && [...ACTIVE, "queued", "scheduled"].includes(j.status) && j.kind !== "convert").forEach((j) => send({ type: "pause", id: j.id }));
$("resumeAll").onclick = () => Object.values(jobs).filter((j) => inShownQueue(j) && j.status === "paused").forEach((j) => send({ type: "resume", id: j.id }));
$("clearDone").onclick = () => send({ type: "clear" });

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
  $("addForm").querySelector("button[type=submit], button:not([type])").textContent = n > 1 ? `Download ${n} links` : "Download";
});
let submitForceHandler = undefined;
$("addForm").querySelector("button[type=submit], button:not([type])").addEventListener("click", (e) => {
  submitForceHandler = e.altKey ? "chrome" : (e.ctrlKey || e.metaKey) ? "anysave" : undefined;
});
$("addForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const forceHandler = submitForceHandler;
  submitForceHandler = undefined;
  let raw = $("addUrl").value.trim();
  // A SHA-256 checksum pasted after a link: AnySave checks the finished file against it.
  const shaMatch = raw.match(/(?:^|[\s,;])(?:sha-?256\s*[:=]?\s*)?([0-9a-f]{64})(?=$|[\s,;])/i);
  const sha256 = shaMatch ? shaMatch[1].toLowerCase() : "";
  if (shaMatch) raw = raw.replace(shaMatch[0], " ").trim();
  const links = linksIn(raw);
  const list = links.length ? links : [raw];
  if (sha256 && list.length !== 1) {
    const m0 = $("addMsg");
    m0.hidden = false; m0.className = "msg err";
    m0.textContent = "A checksum belongs to one file: paste one link with its SHA-256.";
    return;
  }
  const dupCount = list.filter((u) => Object.values(jobs).some((j) => j.url === u && j.status === "done")).length;
  const btn = e.submitter; btn.disabled = true; btn.textContent = list.length > 1 ? `Adding ${list.length}…` : "Checking…";
  let ok = 0, lastErr = "";
  for (const url of list) {
    // Only AnySave's own engine can check a checksum, so that download doesn't go to Chrome.
    const res = sha256 && /^https?:\/\//i.test(url)
      ? await send({ type: "file", url, sha256, forceHandler: "anysave", queue: addQueue() })
      : await send({ type: "addLink", url, forceHandler, queue: addQueue() });
    if (res?.ok) ok++; else lastErr = res?.error || "Couldn't add this link";
  }
  btn.disabled = false; btn.textContent = "Download";
  const m = $("addMsg");
  m.hidden = false;
  m.className = "msg" + (ok ? "" : " err");
  const dupNote = dupCount > 0 ? ` (Note: ${dupCount === 1 ? "this link was" : `${dupCount} links were`} already downloaded earlier — saving a fresh copy.)` : "";
  m.textContent = (list.length === 1
    ? (ok ? (sha256 ? "Added. The finished file will be checked against your checksum." : "Added. It's downloading below.") : lastErr)
    : `Added ${ok} of ${list.length} links. They're downloading below.` + (ok < list.length ? ` (${lastErr})` : "")) + dupNote;
  if (ok) $("addUrl").value = "";
  setTimeout(() => { m.hidden = true; }, 5000);
});

// ---------- converter ----------
let picked = null; // { path, name, duration }
const opts = { format: "mp4", height: "", targetMb: "" };

function parseTime(s) {
  s = String(s || "").trim();
  if (!s || /^end$/i.test(s)) return null;
  const parts = s.split(":").map(Number);
  if (parts.some(isNaN)) return NaN;
  return parts.reduce((acc, v) => acc * 60 + v, 0);
}
function fmt(sec) { sec = Math.round(sec); return `${Math.floor(sec / 60)}:${String(sec % 60).padStart(2, "0")}`; }

function choose(p) {
  picked = p;
  // Files from the download list don't carry their length; "Under N MB" needs it.
  if (!p.duration && p.path) {
    send({ type: "fileInfo", path: p.path }).then((r) => {
      if (picked === p && r?.ok) { p.duration = r.duration || 0; p.size = p.size || r.size; choose(p); }
    });
  }
  $("picked").classList.remove("muted");
  $("picked").textContent = p.name + (p.size ? ` · ${size(p.size)}` : "") + (p.duration ? ` · ${fmt(p.duration)}` : "");
  $("duration").textContent = p.duration ? `Length ${fmt(p.duration)}` : "";
  $("convertOptions").hidden = false;
  $("convertMsg").hidden = true;
  const isAudio = /^(mp3|m4a|aac|wav|flac|ogg|opus)$/.test(extOf(p.name));
  if (isAudio && !["mp3", "m4a", "wav", "opus"].includes(opts.format)) setOpt("format", "mp3");
  updateLabel();
}

$("pickBtn").onclick = async () => {
  $("pickBtn").disabled = true; $("pickBtn").textContent = "Choose in the window that opened…";
  const res = await send({ type: "pickFile" });
  $("pickBtn").disabled = false; $("pickBtn").textContent = "Choose from computer…";
  if (res?.ok) choose({ path: res.path, name: res.name, size: res.size, duration: res.duration });
  else if (!res?.cancelled) showConvertMsg(res?.hostMissing ? "Helper app not found. Re-run the AnySave installer." : (res?.error || "Couldn't open the file"), true);
};

function fillDownloadsPicker() {
  const sel = $("fromDownloads");
  const media = Object.values(jobs).filter((j) => j.status === "done" && j.file && ["Videos", "Music", "Converted"].includes(category(j)))
    .sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0));
  sel.innerHTML = '<option value="">…or pick one of your downloads</option>' +
    media.map((j) => `<option value="${esc(j.id)}">${esc(j.fileName || j.title)}</option>`).join("");
}
$("fromDownloads").onchange = (e) => {
  const j = jobs[e.target.value];
  if (j) choose({ path: j.file, name: j.fileName || j.title, size: j.totalBytes });
};

function setOpt(group, v) {
  opts[group] = v;
  document.querySelectorAll(`.chips[data-group="${group}"] .chip`).forEach((c) => c.classList.toggle("active", c.dataset.v === v));
  const audio = ["mp3", "m4a", "wav", "opus"].includes(opts.format);
  $("sizeCard").hidden = audio;
  $("targetBox").hidden = opts.format === "gif";
  updateLabel();
}
document.querySelectorAll(".chips[data-group] .chip").forEach((c) =>
  c.addEventListener("click", () => setOpt(c.closest(".chips").dataset.group, c.dataset.v)));
["trimStart", "trimEnd", "removeAudio"].forEach((id) => $(id).addEventListener("input", updateLabel));

function spec() {
  const audio = ["mp3", "m4a", "wav", "opus"].includes(opts.format);
  const s = { format: opts.format };
  if (!audio && opts.height) s.height = Number(opts.height);
  if (!audio && opts.format !== "gif" && opts.targetMb) s.targetMb = Number(opts.targetMb);
  const st = parseTime($("trimStart").value), en = parseTime($("trimEnd").value);
  if (st) s.startSec = st;
  if (en != null) s.endSec = en;
  if (!audio && $("removeAudio").checked) s.removeAudio = true;
  return s;
}
function updateLabel() {
  const s = spec();
  const parts = [s.format.toUpperCase(), s.height && `${s.height}p`, s.targetMb && `under ${s.targetMb} MB`,
    (s.startSec || s.endSec != null) && `${fmt(s.startSec || 0)}–${s.endSec != null ? fmt(s.endSec) : "end"}`,
    s.removeAudio && "no sound"].filter(Boolean);
  $("convertBtn").textContent = "Convert · " + parts.join(" · ");
}
function showConvertMsg(text, err) {
  const m = $("convertMsg"); m.hidden = false; m.className = "msg" + (err ? " err" : ""); m.textContent = text;
}

$("convertBtn").onclick = async () => {
  if (!picked) return;
  const s = spec();
  if (Number.isNaN(s.startSec) || Number.isNaN(s.endSec)) return showConvertMsg("Trim times look wrong. Use minutes:seconds, e.g. 1:30.", true);
  if (s.targetMb && !picked.duration) return showConvertMsg("Can't target a file size: the file's length is unknown.", true);
  const res = await send({ type: "convert", path: picked.path, name: picked.name, spec: s });
  if (!res?.ok) return showConvertMsg(res?.error || "Couldn't start the conversion", true);
  showConvertMsg("Converting… Follow the progress in the Downloads tab.");
  setTimeout(() => showTab("downloads"), 1200);
};

// ---------- start ----------
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
$("captureAll").addEventListener("change", (e) => send({ type: "setCapture", on: e.target.checked }));
$("copyDiag")?.addEventListener("click", async () => {
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

function applySettingsUI(settings = {}) {
  $("captureAll").checked = settings.captureAll !== false;
  const h = settings.downloadHandler || "anysave";
  document.querySelectorAll("#handlerChips .chip").forEach((c) => c.classList.toggle("active", c.dataset.h === h));
  if ($("minSizeMb")) $("minSizeMb").value = String(settings.minSizeMb || 0);
  if ($("fileTypes") && document.activeElement !== $("fileTypes")) $("fileTypes").value = settings.fileTypes || "";
  if ($("excludedSites") && document.activeElement !== $("excludedSites")) $("excludedSites").value = settings.excludedSites || "";
  if ($("loginFastSites")) $("loginFastSites").checked = !!settings.loginFastSites;
  if ($("maxSpeedKbps")) $("maxSpeedKbps").value = String(settings.maxSpeedKbps || 0);
  if ($("maxConcurrent")) $("maxConcurrent").value = String(settings.maxConcurrent || 3);
  if ($("afterDoneAction")) $("afterDoneAction").value = settings.afterDoneAction || "none";
  if ($("videoOverlay")) $("videoOverlay").checked = settings.videoOverlay !== false;
  const langs = new Set(String(settings.subLangs || "").split(/[\s,;]+/).filter(Boolean));
  document.querySelectorAll("#subLangsBox input").forEach((c) => { c.checked = langs.has(c.value); });
  if ($("proxy") && document.activeElement !== $("proxy")) $("proxy").value = settings.proxy || "";
  currentSettings = settings;
  if ($("scheduleOn")) $("scheduleOn").checked = !!settings.scheduleOn;
  if ($("scheduleStart") && document.activeElement !== $("scheduleStart")) $("scheduleStart").value = settings.scheduleStart || "01:00";
  if ($("scheduleStop") && document.activeElement !== $("scheduleStop")) $("scheduleStop").value = settings.scheduleStop || "07:00";
  if ($("queues") && document.activeElement !== $("queues")) $("queues").value = settings.queues || "";
  syncQueueUI();
  if ($("embedChapters")) $("embedChapters").checked = settings.embedChapters !== false;
  if ($("notifications")) $("notifications").checked = settings.notifications !== false;
  if ($("embedCover")) $("embedCover").checked = !!settings.embedCover;
}

document.querySelectorAll("#handlerChips .chip").forEach((c) => c.addEventListener("click", () => {
  document.querySelectorAll("#handlerChips .chip").forEach((x) => x.classList.toggle("active", x === c));
  send({ type: "setSettings", patch: { downloadHandler: c.dataset.h } });
}));
$("minSizeMb")?.addEventListener("change", (e) => send({ type: "setSettings", patch: { minSizeMb: Number(e.target.value || 0) } }));
$("fileTypes")?.addEventListener("change", (e) => send({ type: "setSettings", patch: { fileTypes: e.target.value.trim() } }));
$("excludedSites")?.addEventListener("change", (e) => send({ type: "setSettings", patch: { excludedSites: e.target.value.trim() } }));
$("maxSpeedKbps")?.addEventListener("change", (e) => send({ type: "setSettings", patch: { maxSpeedKbps: Number(e.target.value || 0) } }));
$("maxConcurrent")?.addEventListener("change", (e) => send({ type: "setSettings", patch: { maxConcurrent: Number(e.target.value || 3) } }));
$("afterDoneAction")?.addEventListener("change", (e) => send({ type: "setSettings", patch: { afterDoneAction: e.target.value } }));
$("videoOverlay")?.addEventListener("change", (e) => send({ type: "setSettings", patch: { videoOverlay: e.target.checked } }));
// Subtitle languages: Bangla, English and the other most-used languages.
const SUB_LANGS = [["en", "English"], ["bn", "বাংলা (Bangla)"], ["hi", "हिन्दी (Hindi)"], ["ur", "اردو (Urdu)"], ["ar", "العربية (Arabic)"],
  ["es", "Español"], ["fr", "Français"], ["de", "Deutsch"], ["pt", "Português"], ["ru", "Русский"], ["tr", "Türkçe"],
  ["id", "Indonesia"], ["zh", "中文 (Chinese)"], ["ja", "日本語 (Japanese)"], ["ko", "한국어 (Korean)"]];
if ($("subLangsBox")) {
  $("subLangsBox").innerHTML = SUB_LANGS.map(([code, name]) =>
    `<label><input type="checkbox" value="${code}" /> ${esc(name)}</label>`).join("");
  $("subLangsBox").addEventListener("change", () => {
    const picked = [...document.querySelectorAll("#subLangsBox input:checked")].map((c) => c.value);
    send({ type: "setSettings", patch: { subLangs: picked.join(",") } });
  });
}
// ---------- site crawler ----------
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
$("crawlList")?.addEventListener("change", (e) => {
  const item = crawlFiles.find((x) => x.url === e.target.dataset.u);
  if (item) { item.picked = e.target.checked; renderCrawl(); }
});
$("crawlAll")?.addEventListener("change", (e) => { crawlShown().forEach((x) => { x.picked = e.target.checked; }); renderCrawl(); });
$("crawlFilter")?.addEventListener("input", renderCrawl);
$("crawlForm")?.addEventListener("submit", async (e) => {
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
$("crawlDownload")?.addEventListener("click", async () => {
  const picked = crawlFiles.filter((x) => x.picked);
  const btn = $("crawlDownload");
  btn.disabled = true; btn.textContent = `Adding ${picked.length}…`;
  let ok = 0;
  for (const x of picked) {
    const res = await send({ type: "file", url: x.url, name: x.name, referrer: x.from, queue: addQueue() });
    if (res?.ok) { ok++; x.picked = false; }
  }
  crawlMsg(`Added ${ok} of ${picked.length} file${picked.length === 1 ? "" : "s"}. They're in the list below.`, ok < picked.length);
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
  if ($("addQueue")) $("addQueue").hidden = !names.length;
  fill($("scheduleQueue"), [["", "all downloads"], ...names.map((q) => [q, `the "${q}" queue only`])], currentSettings.scheduleQueue || "");
  const chips = $("queueChips");
  if (chips) {
    chips.innerHTML = names.map((q) => `<button class="chip${filter === "q:" + q ? " active" : ""}" data-f="q:${esc(q)}">${esc(q)}</button>`).join("");
    chips.querySelectorAll(".chip").forEach((c) => c.addEventListener("click", () => {
      document.querySelectorAll("#filters .chip").forEach((x) => x.classList.toggle("active", x === c));
      filter = c.dataset.f;
      render();
    }));
    if (filter.startsWith("q:") && !names.includes(filter.slice(2))) { filter = "all"; }
  }
}
$("scheduleOn")?.addEventListener("change", (e) => send({ type: "setSettings", patch: { scheduleOn: e.target.checked } }));
$("scheduleStart")?.addEventListener("change", (e) => { if (e.target.value) send({ type: "setSettings", patch: { scheduleStart: e.target.value } }); });
$("scheduleStop")?.addEventListener("change", (e) => { if (e.target.value) send({ type: "setSettings", patch: { scheduleStop: e.target.value } }); });
$("scheduleQueue")?.addEventListener("change", (e) => send({ type: "setSettings", patch: { scheduleQueue: e.target.value } }));
$("queues")?.addEventListener("change", (e) => {
  const clean = [...new Set(e.target.value.split(",").map((q) => q.trim().slice(0, 24)).filter(Boolean))].join(", ");
  const patch = { queues: clean };
  // A schedule for a queue that no longer exists would silently do nothing: make it "all downloads".
  if (currentSettings.scheduleQueue && !clean.split(", ").includes(currentSettings.scheduleQueue)) patch.scheduleQueue = "";
  send({ type: "setSettings", patch });
});

// Some settings only make sense in the browser extension, others only in the desktop app.
const IN_EXTENSION = !!(window.chrome && chrome.runtime && chrome.runtime.id);
document.querySelectorAll(IN_EXTENSION ? ".desktop-only" : ".extension-only").forEach((el) => el.remove());
$("notifications")?.addEventListener("change", (e) => send({ type: "setSettings", patch: { notifications: e.target.checked } }));
$("embedChapters")?.addEventListener("change", (e) => send({ type: "setSettings", patch: { embedChapters: e.target.checked } }));
$("embedCover")?.addEventListener("change", (e) => send({ type: "setSettings", patch: { embedCover: e.target.checked } }));
// A proxy address is checked before it is saved, so a typo can't silently break every download.
$("proxy")?.addEventListener("change", (e) => {
  const v = e.target.value.trim();
  const ok = !v || /^(system|direct)$/i.test(v) || /^((https?|socks5h?):\/\/)?([^\s/:@]+(:[^\s/@]*)?@)?[^\s/:@]+:\d{1,5}\/?$/i.test(v);
  e.target.setCustomValidity(ok ? "" : "Use http://host:port, socks5://host:port, or leave blank");
  e.target.reportValidity();
  if (ok) send({ type: "setSettings", patch: { proxy: v } });
});

// Allow dragging and dropping any link onto the Manager page to download it.
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

(async () => {
  const { settings = {} } = await chrome.storage.local.get("settings");
  applySettingsUI(settings);
  jobs = (await chrome.storage.local.get("jobs")).jobs || {};
  render();
  markSeen();
  updateLabel();
  showTab(location.hash === "#convert" ? "convert" : "downloads");
})();
