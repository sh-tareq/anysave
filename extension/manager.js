// AnySave download manager + converter page.
const $ = (id) => document.getElementById(id);
const send = (msg) => chrome.runtime.sendMessage(msg);
let jobs = {};
let filter = "all";

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
  folder: "M10 4H4a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-8l-2-2z",
  convert: "M6.99 11 3 15l3.99 4v-3H14v-2H6.99v-3zM21 9l-3.99-4v3H10v2h7.01v3L21 9z",
  retry: "M17.65 6.35A7.96 7.96 0 0 0 12 4a8 8 0 1 0 7.73 10h-2.08A6 6 0 1 1 12 6c1.66 0 3.14.69 4.22 1.78L13 11h7V4l-2.35 2.35z",
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

// ---------- downloads list ----------
function render() {
  const all = Object.values(jobs).sort((a, b) => (b.createdAt || b.id) - (a.createdAt || a.id));
  const activeCount = all.filter((j) => ACTIVE.includes(j.status)).length;
  $("activeBadge").hidden = !activeCount;
  $("activeBadge").textContent = activeCount;
  const shown = all.filter((j) => {
    const c = category(j);
    switch (filter) {
      case "all": return j.status !== "cancelled";
      case "failed": return j.status === "error" || j.status === "cancelled";
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
  let line;
  if (running) {
    const prog = j.kind === "file" && j.totalBytes > 0 ? `${size(j.doneBytes)} of ${size(j.totalBytes)}` : "";
    line = j.status === "starting" ? "Starting…"
      : [`${pct.toFixed(0)}%`, prog, j.speed, j.eta && `ETA ${j.eta}`].filter(Boolean).join(" · ");
  } else if (paused) line = ["Paused", `${pct.toFixed(0)}%`].join(" · ");
  else if (j.status === "done") line = ["Done", j.qualityLabel, size(j.totalBytes), j.site, ago(j.finishedAt || j.createdAt)].filter(Boolean).join(" · ");
  else if (j.status === "error") line = `<span class="err">${esc(j.error || "Failed")}</span>`;
  else line = "Cancelled";

  li.innerHTML = `
    <div class="icon">${svg(category(j), 22)}${j.status === "done" ? `<span class="done" title="Finished">${svg("check", 13)}</span>` : ""}</div>
    <div class="info">
      <div class="name" title="${esc(j.fileName || j.title || j.url)}">${esc(displayName(j))}</div>
      ${running || paused ? `<div class="bar ${paused ? "paused" : ""}"><div style="width:${pct}%"></div></div>` : ""}
      <div class="meta">${j.status === "done" && extOf(j.fileName || j.file) ? `<span class="tag">${esc(extOf(j.fileName || j.file).toUpperCase().slice(0, 4))}</span>` : ""}${j.status === "error" ? line : j.status === "done" ? esc(line).replace(/^Done/, '<span class="ok">Done</span>') : esc(line)}</div>
    </div>
    <div class="btns"></div>`;
  const btns = li.querySelector(".btns");
  const add = (icon, title, fn) => {
    const b = Object.assign(document.createElement("button"), { className: "ib", title });
    b.setAttribute("aria-label", title);
    b.innerHTML = svg(icon, 18);
    b.onclick = fn; btns.appendChild(b);
  };
  if (running && j.kind !== "convert") add("pause", "Pause", () => send({ type: "pause", id: j.id }));
  if (paused) add("play", "Resume", () => send({ type: "resume", id: j.id }));
  if (running || paused) add("close", "Cancel", () => send({ type: "cancel", id: j.id }));
  if (j.status === "done" && j.file) {
    add("folder", "Show in folder", () => send({ type: "reveal", id: j.id }));
    if (["Videos", "Music"].includes(category(j)))
      add("convert", "Convert", () => { showTab("convert"); choose({ path: j.file, name: j.fileName || j.title }); });
  }
  if (j.status === "error" || j.status === "cancelled") add("retry", "Retry", () => send({ type: "retry", id: j.id }));
  if (FINISHED.includes(j.status)) add("remove", "Remove from list", () => send({ type: "remove", id: j.id }));
  return li;
}

document.querySelectorAll("#filters .chip").forEach((c) => c.addEventListener("click", () => {
  document.querySelectorAll("#filters .chip").forEach((x) => x.classList.toggle("active", x === c));
  filter = c.dataset.f; render();
}));
$("pauseAll").onclick = () => Object.values(jobs).filter((j) => ACTIVE.includes(j.status) && j.kind !== "convert").forEach((j) => send({ type: "pause", id: j.id }));
$("resumeAll").onclick = () => Object.values(jobs).filter((j) => j.status === "paused").forEach((j) => send({ type: "resume", id: j.id }));
$("clearDone").onclick = () => send({ type: "clear" });

$("addForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const url = $("addUrl").value.trim();
  const btn = e.submitter; btn.disabled = true; btn.textContent = "Checking…";
  const res = await send({ type: "addLink", url });
  btn.disabled = false; btn.textContent = "Download";
  const m = $("addMsg");
  m.hidden = false;
  m.className = "msg" + (res?.ok ? "" : " err");
  m.textContent = res?.ok ? "Added. It's downloading below." : (res?.error || "Couldn't add this link");
  if (res?.ok) $("addUrl").value = "";
  setTimeout(() => { m.hidden = true; }, 4000);
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

(async () => {
  const { settings = {} } = await chrome.storage.local.get("settings");
  $("captureAll").checked = settings.captureAll !== false;
  jobs = (await chrome.storage.local.get("jobs")).jobs || {};
  render();
  markSeen();
  updateLabel();
  showTab(location.hash === "#convert" ? "convert" : "downloads");
})();
