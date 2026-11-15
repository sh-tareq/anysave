// AnySave "Convert anything" page (Chrome extension and the Mac/Windows apps).
// Talks to the helper's preview server (host-go/cv_server.go) for file info and live previews, and
// starts the real conversion as a normal job, so it shows in the downloads list too.
(() => {
  "use strict";
  const isMac = /Mac/i.test(navigator.platform || navigator.userAgent);

  // ---------- the preview server ----------
  let sess = null;
  async function session() {
    if (sess) return sess;
    if (window.__ANYSAVE_TOKEN) return (sess = { base: location.origin, token: window.__ANYSAVE_TOKEN });
    sess = new Promise((resolve, reject) => {
      let port;
      try { port = chrome.runtime.connectNative("com.anysave.host"); } catch (e) { reject(e); return; }
      const t = setTimeout(() => reject(new Error("The AnySave helper didn't answer. Run the AnySave installer again.")), 15000);
      port.onMessage.addListener((m) => {
        if (m.type === "ready") { clearTimeout(t); resolve({ base: m.base, token: m.token, port }); }
        else if (m.type === "error") { clearTimeout(t); reject(new Error(m.message)); }
      });
      port.onDisconnect.addListener(() => {
        clearTimeout(t);
        const why = chrome.runtime.lastError?.message || "";
        sess = null;
        reject(new Error(/not found|forbidden/i.test(why) ? "Helper app not found. Run the AnySave installer again."
          : "The AnySave helper stopped. Reload this page to try again."));
      });
      port.postMessage({ action: "cvSession" });
    });
    try { return (sess = await sess); } catch (e) { sess = null; throw e; }
  }
  async function op(name, body = {}, signal) {
    const s = await session();
    const r = await fetch(s.base + "/cv/op", {
      method: "POST", signal, body: JSON.stringify({ op: name, ...body }),
      headers: { "X-AnySave-Token": s.token, "Content-Type": "application/json" },
    });
    if (!r.ok) throw new Error("The helper said: " + r.status);
    return r.json();
  }
  const full = (rel) => (sess && sess.base ? sess.base : "") + rel;
  async function upload(file, query) {
    const s = await session();
    const r = await fetch(`${s.base}/cv/upload?name=${encodeURIComponent(file.name)}${query || ""}`, {
      method: "POST", body: file, headers: { "X-AnySave-Token": s.token },
    });
    return r.json();
  }
  const thumbURL = (path, w = 640, t = 0) =>
    full(`/cv/thumb?path=${encodeURIComponent(path)}&w=${w}${t ? "&t=" + t : ""}&token=${encodeURIComponent(sess.token)}`);
  const fileURL = (path) => full(`/cv/file?path=${encodeURIComponent(path)}&token=${encodeURIComponent(sess.token)}`);

  // ---------- tools ----------
  const VIDEO_F = ["mp4", "mkv", "mov", "webm", "gif"];
  const AUDIO_F = ["mp3", "m4a", "wav", "flac", "opus", "ogg"];
  const media = ["video", "audio"];
  const all = ["video", "audio", "image", "pdf", "doc", "archive", "subs", "data", "other"];
  const TOOLS = [
    { id: "media", title: "Convert video or audio", desc: "Change format, size, shape or speed. Clean up the sound.", accepts: media, verb: "Convert" },
    { id: "join", title: "Join into one file", desc: "Put clips or songs one after another, in the list's order.", accepts: media, min: 2, verb: "Join" },
    { id: "frames", title: "Video to pictures", desc: "Save pictures from a video.", accepts: ["video"], max: 1, verb: "Save pictures" },
    { id: "addsubs", title: "Add subtitles to a video", desc: "Choose one video and one subtitle file.", accepts: ["video", "subs"], min: 2, max: 2, verb: "Add subtitles" },
    { id: "image", title: "Convert pictures", desc: "Change format, size or file size. Photo info such as location is removed.", accepts: ["image"], verb: "Convert" },
    { id: "images-pdf", title: "Pictures to PDF", desc: "One page per picture, in the list's order.", accepts: ["image"], verb: "Make PDF" },
    { id: "pdf-images", title: "PDF to pictures", desc: "One picture per page.", accepts: ["pdf"], max: 1, verb: "Save pictures" },
    { id: "pdf-merge", title: "Merge PDFs", desc: "Join PDFs in the list's order.", accepts: ["pdf"], min: 2, verb: "Merge" },
    { id: "pdf-pages", title: "Pick or split pages", desc: "Keep some pages, or save every page on its own.", accepts: ["pdf"], max: 1, verb: "Save pages" },
    { id: "pdf-compress", title: "Make a PDF smaller", desc: "Shrinks the pictures inside. Text stays sharp.", accepts: ["pdf"], max: 1, verb: "Make smaller" },
    { id: "pdf-rotate", title: "Rotate pages", desc: "Turn every page, or the ones you pick.", accepts: ["pdf"], max: 1, verb: "Rotate" },
    { id: "pdf-protect", title: "Add a password", desc: "Nobody can open it without the password.", accepts: ["pdf"], max: 1, verb: "Lock" },
    { id: "pdf-unlock", title: "Remove a password", desc: "Needs the password once.", accepts: ["pdf"], max: 1, verb: "Unlock" },
    { id: "doc", title: "Convert a document", desc: "Word, ODT, ebook, Markdown, web page, text or PDF to PDF, text, web page or ebook. Layout is kept simple.", accepts: ["doc", "pdf"], max: 1, verb: "Convert" },
    { id: "unpack", title: "Unpack", desc: "ZIP, 7z, TAR, GZ, BZ2 and XZ.", accepts: ["archive"], max: 1, verb: "Unpack" },
    { id: "pack", title: "Pack into an archive", desc: "Put the files in one ZIP or TAR.GZ.", accepts: all, verb: "Pack" },
    { id: "subs", title: "Convert subtitles", desc: "SRT, VTT, ASS or LRC. Fix subtitles that come too early or late.", accepts: ["subs"], max: 1, verb: "Convert" },
    { id: "data", title: "Convert a spreadsheet", desc: "CSV, TSV, JSON and Excel (first sheet).", accepts: ["data"], max: 1, verb: "Convert" },
  ];
  const tool = (id) => TOOLS.find((t) => t.id === id);
  function toolsFor(kinds) {
    if (!kinds.length) return [];
    return TOOLS.filter((t) => kinds.length >= (t.min || 1) && kinds.length <= (t.max || 1e9) && kinds.every((k) => t.accepts.includes(k)) &&
      (t.id !== "addsubs" || (kinds.filter((k) => k === "video").length === 1 && kinds.filter((k) => k === "subs").length === 1)) &&
      (t.id !== "join" || new Set(kinds).size === 1))
      .sort((a, b) => (a.id === "pack") - (b.id === "pack"));
  }

  // ---------- state ----------
  const st = { files: [], tool: null, active: 0, sets: {}, job: null, rendering: null };
  const kindsNow = () => st.files.map((f) => f.kind);
  const allAudio = () => st.files.length && st.files.every((f) => f.kind === "audio");
  function defaults(id) {
    switch (id) {
      case "media": return { format: allAudio() ? "mp3" : "mp4", height: "", targetMb: "", aspect: "", rotate: "", flip: "", codec: "h264",
        speed: "1", fps: "", hw: false, removeAudio: false, stabilize: false, denoise: false, loudnorm: false, abitrate: "", start: "", end: "" };
      case "frames": return { format: "jpg", every: "1" };
      case "addsubs": return { mode: "burn", size: "medium" };
      case "image": return { format: "jpg", maxSide: "", quality: 85, targetKb: "" };
      case "images-pdf": return { page: "a4", margin: false, quality: "80" };
      case "pdf-images": return { format: "jpg", dpi: "150", pages: "" };
      case "pdf-pages": return { mode: "keep", pages: "" };
      case "pdf-compress": return { level: "normal" };
      case "pdf-rotate": return { angle: "90", pages: "" };
      case "pdf-protect": return { password: "", password2: "" };
      case "pdf-unlock": return { password: "" };
      case "doc": return { format: "pdf", page: "a4", font: "normal" };
      case "unpack": return { password: "" };
      case "pack": return { format: "zip", name: "" };
      case "subs": return { format: "srt", shift: "0" };
      case "data": return { format: "xlsx" };
    }
    return {};
  }
  const set = () => (st.sets[st.tool] ||= defaults(st.tool));

  // ---------- helpers ----------
  const h = (s) => esc(s);
  function clock(sec) {
    if (sec == null || isNaN(sec)) return "";
    sec = Math.max(0, sec);
    const hh = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = Math.floor(sec % 60);
    return (hh ? hh + ":" + String(m).padStart(2, "0") : m) + ":" + String(s).padStart(2, "0");
  }
  function parseClock(s) {
    s = String(s || "").trim().replace(",", ".");
    if (!s) return null;
    const p = s.split(":").map(Number);
    if (p.some(isNaN)) return NaN;
    return p.reduce((a, v) => a * 60 + v, 0);
  }
  function rangesText(pages) {
    const s = [...new Set(pages)].sort((a, b) => a - b), out = [];
    for (let i = 0; i < s.length;) { let j = i; while (j + 1 < s.length && s[j + 1] === s[j] + 1) j++; out.push(i === j ? `${s[i]}` : `${s[i]}-${s[j]}`); i = j + 1; }
    return out.join(", ");
  }
  function parseRanges(text, count) {
    const pages = [];
    for (const part of String(text || "").split(/[,; ]+/).filter(Boolean)) {
      const m = part.match(/^(\d*)\s*[-–]\s*(\d*)$/);
      if (m) { const a = +m[1] || 1, b = +m[2] || count; for (let p = a; p <= b; p++) if (p >= 1 && p <= count) pages.push(p); }
      else if (/^\d+$/.test(part) && +part >= 1 && +part <= count) pages.push(+part);
    }
    return [...new Set(pages)];
  }
  const KIND_ICON = { video: "Videos", audio: "Music", image: "Pictures", pdf: "Documents", doc: "Documents", archive: "Archives",
    subs: "Documents", data: "Documents", other: "Other" };
  function icon(kind) {
    const d = (typeof P !== "undefined" && P[KIND_ICON[kind]]) || "";
    return `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="${d}"/></svg>`;
  }
  function fileFacts(f) {
    const m = f.info?.media, bits = [size(f.size)];
    if (m) {
      if (m.duration > 0.3) bits.push(clock(m.duration));
      if (m.width && f.kind !== "audio") bits.push(`${m.width}×${m.height}`);
    }
    if (f.info?.pages) bits.push(f.info.pages === 1 ? "1 page" : `${f.info.pages} pages`);
    if (f.info?.locked) bits.push("locked");
    return bits.filter(Boolean).join(" · ");
  }
  function msg(text, err) {
    const m = $("cvMsg");
    m.hidden = !text; m.className = "msg" + (err ? " err" : ""); m.textContent = text || "";
  }

  // ---------- adding files ----------
  async function addEntries(entries) {
    for (const e of entries) {
      if (!e || st.files.some((f) => f.path === e.path)) continue;
      const f = { ...e };
      st.files.push(f);
      op("info", { path: f.path }).then((r) => {
        if (!r?.ok) return;
        f.info = r;
        if (r.file?.kind) f.kind = r.file.kind;
        refresh();
      }).catch(() => {});
    }
    if (!st.files.length) return;
    st.active = Math.min(st.active, st.files.length - 1);
    refresh();
  }
  async function pick() {
    msg("");
    $("cvPick").disabled = $("cvAdd").disabled = true;
    const old = $("cvPick").textContent;
    $("cvPick").textContent = "Choose in the window that opened…";
    try {
      const r = await op("pick", { multi: true });
      if (r.ok) await addEntries(r.files || []);
      else msg(r.error, true);
    } catch (e) { msg(e.message, true); }
    $("cvPick").disabled = $("cvAdd").disabled = false;
    $("cvPick").textContent = old;
  }
  async function dropFiles(list) {
    const files = [...list].filter((f) => f.size > 0 || f.type);
    if (!files.length) return;
    msg(`Adding ${files.length === 1 ? files[0].name : files.length + " files"}…`);
    const got = [];
    for (const f of files) {
      try {
        const r = await upload(f);
        if (r.ok) got.push(r.file); else { msg(r.error, true); return; }
      } catch (e) { msg(e.message, true); return; }
    }
    msg("");
    addEntries(got);
  }
  window.fillDownloadsPicker = function () {
    const sel = $("cvFromDl");
    const done = Object.values(jobs).filter((j) => j.status === "done" && j.file && !/\/$/.test(j.file))
      .sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0)).slice(0, 200);
    sel.innerHTML = '<option value="">…or one of your downloads</option>' +
      done.map((j) => `<option value="${h(j.id)}">${h(displayName(j))}</option>`).join("");
  };
  window.choose = async function (p) { // "Convert" on a finished download
    showTab("convert");
    try {
      const r = await op("allow", { path: p.path });
      if (r.ok) { st.files = []; st.tool = null; await addEntries([r.file]); } else msg(r.error, true);
    } catch (e) { msg(e.message, true); }
  };

  // ---------- drawing the page ----------
  function refresh() {
    const has = st.files.length > 0;
    $("cvMain").hidden = !has;
    $("cvDrop").classList.toggle("small", has);
    if (!has) { stopPreview(); return; }
    // A locked PDF can only be unlocked.
    const tools = toolsFor(kindsNow());
    if (st.files.length === 1 && st.files[0].info?.locked && st.tool !== "pdf-unlock") st.tool = "pdf-unlock";
    if (!tools.some((t) => t.id === st.tool)) st.tool = tools[0]?.id || null;
    if (st.tool === "media" && st.sets.media) { // fit the format to what was chosen
      const s = st.sets.media;
      if (allAudio() && !AUDIO_F.includes(s.format)) s.format = "mp3";
    }
    drawFiles();
    drawTools(tools);
    drawSettings();
    drawSummary();
    schedulePreview(true);
  }

  function drawFiles() {
    $("cvCount").textContent = st.files.length > 1 ? `${st.files.length} files · ${size(st.files.reduce((a, f) => a + (f.size || 0), 0))}` : "";
    const ordered = ["join", "pdf-merge", "images-pdf"].includes(st.tool);
    $("cvFiles").innerHTML = st.files.map((f, i) => `
      <li class="${i === st.active ? "on" : ""}" data-i="${i}">
        <span class="cv-ico k-${h(f.kind)}">${icon(f.kind)}</span>
        <span class="cv-fname"><b title="${h(f.name)}">${h(f.name)}</b><span class="muted small">${h(fileFacts(f))}</span></span>
        ${ordered && st.files.length > 1 ? `<button class="ib" data-a="up" title="Move up" ${i ? "" : "disabled"}>↑</button>
          <button class="ib" data-a="down" title="Move down" ${i < st.files.length - 1 ? "" : "disabled"}>↓</button>` : ""}
        <button class="ib" data-a="rm" title="Remove">✕</button>
      </li>`).join("");
  }
  $("cvFiles").addEventListener("click", (e) => {
    const li = e.target.closest("li[data-i]");
    if (!li) return;
    const i = +li.dataset.i, a = e.target.closest("button")?.dataset.a;
    if (a === "rm") { st.files.splice(i, 1); st.active = Math.max(0, Math.min(st.active, st.files.length - 1)); }
    else if (a === "up" && i > 0) { [st.files[i - 1], st.files[i]] = [st.files[i], st.files[i - 1]]; st.active = i - 1; }
    else if (a === "down" && i < st.files.length - 1) { [st.files[i + 1], st.files[i]] = [st.files[i], st.files[i + 1]]; st.active = i + 1; }
    else st.active = i;
    refresh();
  });

  function drawTools(tools) {
    if (!tools.length) {
      $("cvTools").innerHTML = `<p class="muted">Nothing fits this mix of files. Remove some, or choose files of one kind.</p>`;
      return;
    }
    $("cvTools").innerHTML = tools.map((t) => `<button class="cv-tool${t.id === st.tool ? " on" : ""}" data-t="${t.id}">
      <b>${h(t.title)}</b><span>${h(t.desc)}</span></button>`).join("");
  }
  $("cvTools").addEventListener("click", (e) => {
    const b = e.target.closest("[data-t]");
    if (!b || b.dataset.t === st.tool) return;
    st.tool = b.dataset.t; sample = null; refresh();
  });

  // Settings: each tool lists its controls; "when" hides ones that don't apply.
  const chip = (key, label, options, help, when) => ({ type: "chips", key, label, options, help, when });
  const check = (key, label, help, when) => ({ type: "check", key, label, help, when });
  const text = (key, label, placeholder, help, when, extra = {}) => ({ type: "text", key, label, placeholder, help, when, ...extra });
  const isVideoOut = (s) => VIDEO_F.includes(s.format);
  const isAudioOut = (s) => AUDIO_F.includes(s.format);
  const PRESETS = [
    { label: "WhatsApp", help: "720p, under 16 MB", set: { format: "mp4", height: "720", targetMb: "16", codec: "h264" } },
    { label: "Email", help: "480p, under 10 MB", set: { format: "mp4", height: "480", targetMb: "10", codec: "h264" } },
    { label: "Reel / Short / TikTok", help: "vertical, 1080p", set: { format: "mp4", height: "1080", aspect: "9:16", targetMb: "", codec: "h264" } },
    { label: "YouTube", help: "1080p, high quality", set: { format: "mp4", height: "1080", targetMb: "0", codec: "h264", aspect: "" } },
    { label: "Small file", help: "H.265, half the size", set: { format: "mp4", codec: "h265", targetMb: "" } },
    { label: "Just the sound", help: "MP3", set: { format: "mp3" } },
  ];
  function controls() {
    const s = set(), n = st.files.length;
    switch (st.tool) {
      case "media": return [
        { group: "Quick choices", items: [{ type: "presets" }], when: () => !allAudio() },
        { group: "Format", items: [
          chip("format", allAudio() ? "Audio" : "Video", (allAudio() ? [] : VIDEO_F).map((f) => [f, f.toUpperCase()]), null, () => !allAudio()),
          chip("format", "Audio only", AUDIO_F.map((f) => [f, f.toUpperCase()]),
            { mp3: "MP3 plays everywhere.", m4a: "M4A: good quality, plays on phones and Macs.", wav: "WAV: no quality loss, very big files.",
              flac: "FLAC: no quality loss, about half the size of WAV.", opus: "Opus: the best sound for its size.", ogg: "OGG: an open format, plays in most apps." }[s.format]),
        ] },
        { group: "Picture", when: () => isVideoOut(s), items: [
          chip("height", "Size", [["", "Original"], ["2160", "4K"], ["1080", "1080p"], ["720", "720p"], ["480", "480p"], ["360", "360p"]],
            "Smaller sizes make smaller files. AnySave never makes a video bigger than it was."),
          chip("aspect", "Shape", [["", "Original"], ["9:16", "Vertical 9:16"], ["1:1", "Square"], ["4:5", "4:5"], ["16:9", "Wide 16:9"]],
            s.aspect ? "Cuts the edges off and keeps the middle." : null, () => s.format !== "gif" || true),
          chip("rotate", "Turn", [["", "No"], ["90", "Right"], ["180", "Upside down"], ["270", "Left"]]),
          chip("flip", "Mirror", [["", "No"], ["h", "Left–right"], ["v", "Top–bottom"]]),
          chip("codec", "Video type", [["h264", "H.264"], ["h265", "H.265"], ["av1", "AV1"]],
            { h264: "H.264 plays on everything.", h265: "H.265 makes files about half the size. Plays on phones and computers from the last few years.",
              av1: "AV1 makes the smallest files, but takes a long time and older devices can't play it." }[s.codec], () => s.format !== "gif" && s.format !== "webm"),
          chip("targetMb", "File size", [["", "Normal"], ["0", "High quality"], ["10", "Under 10 MB"], ["16", "Under 16 MB"], ["25", "Under 25 MB"], ["50", "Under 50 MB"], ["100", "Under 100 MB"]],
            s.targetMb && s.targetMb !== "0" ? "AnySave picks the best quality that fits. Long videos in a small size look blurry." : null, () => s.format !== "gif"),
          chip("fps", "Smoothness", [["", "Original"], ["60", "60 fps"], ["30", "30 fps"], ["24", "24 fps"], ["15", "15 fps"]], null),
          check("removeAudio", "Remove the sound", null, () => s.format !== "gif"),
          check("stabilize", "Steady a shaky video", "Takes longer. Crops the edges a little."),
          check("hw", "Use the computer's video chip", "Much faster, files a little bigger. Not used with a set file size.", () => isMac && s.format !== "gif" && s.format !== "webm" && s.codec !== "av1"),
        ] },
        { group: "Sound", when: () => s.format !== "gif" && !(isVideoOut(s) && s.removeAudio), items: [
          check("denoise", "Clean up background noise", "Removes hum and hiss. Best for speech."),
          check("loudnorm", "Even out the loudness", "Quiet parts louder, loud parts softer, like on streaming sites."),
          chip("abitrate", "Sound quality", [["", "Normal"], ["96", "Small"], ["192", "Good"], ["320", "Best"]], null, () => ["mp3", "m4a", "opus", "ogg"].includes(s.format)),
        ] },
        { group: "Speed and trim", items: [
          chip("speed", "Speed", [["0.5", "0.5×"], ["0.75", "0.75×"], ["1", "Normal"], ["1.25", "1.25×"], ["1.5", "1.5×"], ["2", "2×"]],
            s.speed !== "1" ? "The sound keeps its normal pitch." : null),
          { type: "trim" },
        ], when: () => n === 1 },
      ];
      case "join": return [{ group: "Order", items: [{ type: "note", text: "The files are joined from top to bottom. Use the arrows in the list to change the order. Videos of different shapes get black bars." }] }];
      case "frames": return [{ group: "Pictures", items: [
        chip("every", "One picture every", [["0.5", "½ second"], ["1", "1 second"], ["5", "5 seconds"], ["10", "10 seconds"], ["30", "30 seconds"], ["0", "Every frame"]],
          s.every === "0" ? "Every frame makes a lot of pictures (25–60 for each second)." : estimateFrames()),
        chip("format", "Format", [["jpg", "JPG"], ["png", "PNG"]], s.format === "png" ? "PNG keeps every detail; files are bigger." : "JPG: small files, good quality."),
      ] }];
      case "addsubs": return [{ group: "Subtitles", items: [
        chip("mode", "How", [["burn", "Always visible"], ["track", "Can be turned on and off"]],
          s.mode === "burn" ? "The words become part of the picture, so they show in every app (good for social media)." :
            "Adds the subtitles as a track. Quick, no quality loss; the player must support it."),
        chip("size", "Text size", [["small", "Small"], ["medium", "Medium"], ["large", "Large"]], null, () => s.mode === "burn"),
      ] }];
      case "image": return [{ group: "Pictures", items: [
        chip("format", "Format", [["jpg", "JPG"], ["png", "PNG"], ["webp", "WebP"], ["avif", "AVIF"], ["gif", "GIF"], ["bmp", "BMP"], ["tiff", "TIFF"], ["ico", "ICO"]],
          { jpg: "JPG works everywhere. No see-through parts (they become white).", png: "PNG keeps every detail and see-through parts. Bigger files.",
            webp: "WebP: much smaller than JPG at the same quality. Works in browsers and most apps.", avif: "AVIF: the smallest files. Newer apps only.",
            gif: "GIF: only 256 colours. Use it for simple drawings.", bmp: "BMP: no compression, very big files.", tiff: "TIFF: for printing and editing. Big files.",
            ico: "ICO: a website or app icon, at most 256×256." }[s.format]),
        chip("maxSide", "Size", [["", "Original"], ["4000", "4000 px"], ["2048", "2048 px"], ["1600", "1600 px"], ["1080", "1080 px"], ["800", "800 px"], ["400", "400 px"]],
          "The longest side. Pictures are never made bigger."),
        { type: "range", key: "quality", label: "Quality", min: 10, max: 100, step: 1, when: () => ["jpg", "webp", "avif"].includes(s.format) && !s.targetKb,
          help: s.quality >= 100 && s.format === "webp" ? "100 = no quality loss at all." : "85 looks the same as the original for most photos." },
        chip("targetKb", "File size", [["", "Any"], ["50", "Under 50 KB"], ["100", "Under 100 KB"], ["200", "Under 200 KB"], ["500", "Under 500 KB"], ["1000", "Under 1 MB"]],
          s.targetKb ? "AnySave finds the best quality that fits, and makes the picture smaller only if it has to." : null, () => ["jpg", "webp"].includes(s.format)),
      ] }];
      case "images-pdf": return [{ group: "PDF", items: [
        chip("page", "Page size", [["a4", "A4"], ["letter", "Letter"], ["fit", "Same as the picture"]], s.page === "fit" ? null : "Wide pictures get wide pages."),
        check("margin", "White border around each picture"),
        chip("quality", "Quality", [["70", "Smaller file"], ["80", "Normal"], ["92", "High (for printing)"]]),
      ] }];
      case "pdf-images": return [{ group: "Pictures", items: [
        chip("format", "Format", [["jpg", "JPG"], ["png", "PNG"]]),
        chip("dpi", "Sharpness", [["72", "Screen"], ["150", "Normal"], ["300", "Print"]], "Higher is sharper, with bigger files."),
        text("pages", "Pages", "all", "For example 1-3, 5, 8-. Or tap pages in the preview."),
      ] }];
      case "pdf-merge": return [{ group: "Order", items: [{ type: "note", text: "PDFs are joined from top to bottom. Use the arrows in the list to change the order." }] }];
      case "pdf-pages": return [{ group: "Pages", items: [
        chip("mode", "Do what", [["keep", "Keep these pages"], ["split", "Every page as its own PDF"]]),
        text("pages", "Pages", "all", s.mode === "keep" ? "In the order you write them, for example 3, 1-2. Or tap pages in the preview." : "Leave empty for every page."),
      ] }];
      case "pdf-compress": return [{ group: "How much smaller", items: [
        chip("level", "Level", [["light", "A little"], ["normal", "Normal"], ["strong", "As small as possible"]],
          { light: "Pictures stay sharp.", normal: "Good for email and sharing.", strong: "Pictures get soft; text stays sharp." }[s.level]),
        { type: "note", text: "Only pictures inside the PDF can shrink. A PDF that is only text is already small." },
      ] }];
      case "pdf-rotate": return [{ group: "Rotate", items: [
        chip("angle", "Turn", [["90", "Right"], ["180", "Upside down"], ["270", "Left"]]),
        text("pages", "Pages", "all", "Leave empty for every page, or tap pages in the preview."),
      ] }];
      case "pdf-protect": return [{ group: "Password", items: [
        text("password", "Password", "", "AES-256, the strongest PDF lock. If you forget it, nobody can open the PDF.", null, { inputType: "password" }),
        text("password2", "Type it again", "", s.password2 && s.password !== s.password2 ? "The two passwords are different." : null, null, { inputType: "password" }),
      ] }];
      case "pdf-unlock": return [{ group: "Password", items: [
        text("password", "The PDF's password", "", st.files[0]?.info?.locked ? "This PDF is locked. The copy AnySave saves opens without a password." :
          "This PDF opens without a password, but it may stop printing or copying. Unlocking removes that.", null, { inputType: "password" }),
      ] }];
      case "doc": {
        const ext = extOf(st.files[0]?.name);
        return [{ group: "Document", items: [
          chip("format", "Save as", [["pdf", "PDF"], ["txt", "Text"], ["html", "Web page"], ["epub", "Ebook (EPUB)"]].filter(([v]) => v !== ext && !(v === "html" && /^html?$/.test(ext)) && !(v === "txt" && ext === "txt")),
            { pdf: "Every language's letters come out right. Uses Chrome or Edge on this computer.", txt: "Just the words.",
              html: "Opens in any browser.", epub: "For ebook readers. The chapter list comes from the headings." }[s.format]),
          chip("page", "Page size", [["a4", "A4"], ["letter", "Letter"]], null, () => s.format === "pdf"),
          chip("font", "Text size", [["small", "Small"], ["normal", "Normal"], ["large", "Large"]], null, () => s.format === "pdf"),
          ...(ext === "pdf" ? [{ type: "note", text: "Only the text is taken from the PDF. Scanned pages (pictures of text) can't be read yet." }] : []),
        ] }];
      }
      case "unpack": return [{ group: "Unpack", items: [
        text("password", "Password (only for a locked 7z)", "", null, () => extOf(st.files[0]?.name) === "7z", { inputType: "password" }),
        { type: "note", text: "Files go into a new folder in Converted. A single file inside comes out as itself." },
      ] }];
      case "pack": return [{ group: "Archive", items: [
        chip("format", "Format", [["zip", "ZIP"], ["tar.gz", "TAR.GZ"]], s.format === "zip" ? "ZIP opens everywhere, with no extra app." : "TAR.GZ is common on Linux."),
        text("name", "Name", st.files.length === 1 ? st.files[0].name.replace(/\.[^.]+$/, "") : "", null),
      ] }];
      case "subs": return [{ group: "Subtitles", items: [
        chip("format", "Format", [["srt", "SRT"], ["vtt", "VTT"], ["ass", "ASS"], ["lrc", "LRC"]].filter(([v]) => v !== extOf(st.files[0]?.name) || s.shift !== "0"),
          { srt: "SRT works in almost every player.", vtt: "VTT is for web pages and YouTube.", ass: "ASS keeps styling for anime players.", lrc: "LRC: song lyrics for music players." }[s.format]),
        text("shift", "Move the timing (seconds)", "0", "Use a minus to show them earlier, e.g. -1.5. The preview shows the new times.", null, { inputType: "number", step: "0.1" }),
      ] }];
      case "data": return [{ group: "Spreadsheet", items: [
        chip("format", "Save as", [["xlsx", "Excel"], ["csv", "CSV"], ["tsv", "TSV"], ["json", "JSON"]].filter(([v]) => v !== extOf(st.files[0]?.name)),
          { xlsx: "Opens in Excel, Numbers and Google Sheets.", csv: "The simplest table file. Opens everywhere.", tsv: "Like CSV, with tabs between columns.", json: "For programs and websites." }[s.format]),
        ...(extOf(st.files[0]?.name) === "xlsx" ? [{ type: "note", text: "Only the first sheet is used." }] : []),
      ] }];
    }
    return [];
  }
  function estimateFrames() {
    const d = st.files[0]?.info?.media?.duration, e = +set().every;
    return d && e > 0 ? `About ${Math.max(1, Math.round(d / e))} pictures.` : null;
  }

  function drawSettings() {
    const s = set();
    const groups = controls().filter((g) => !g.when || g.when());
    $("cvSettings").innerHTML = groups.map((g) => {
      const items = g.items.filter((c) => !c.when || c.when()).map((c) => {
        const help = c.help ? `<div class="cv-help">${h(c.help)}</div>` : "";
        switch (c.type) {
          case "chips": return `<div class="label">${h(c.label)}</div><div class="chips" data-k="${c.key}">${c.options.map(([v, l]) =>
            `<button class="chip${String(s[c.key]) === v ? " active" : ""}" data-v="${h(v)}">${h(l)}</button>`).join("")}</div>${help}`;
          case "check": return `<label class="check"><input type="checkbox" data-k="${c.key}"${s[c.key] ? " checked" : ""}> ${h(c.label)}</label>${help}`;
          case "text": return `<div class="label">${h(c.label)}</div><input class="cv-input" data-k="${c.key}" type="${c.inputType || "text"}"${c.step ? ` step="${c.step}"` : ""}
            value="${h(s[c.key])}" placeholder="${h(c.placeholder || "")}" autocomplete="off">${help}`;
          case "range": return `<div class="label">${h(c.label)} <b data-show="${c.key}">${h(s[c.key])}</b></div>
            <input class="cv-range" type="range" data-k="${c.key}" min="${c.min}" max="${c.max}" step="${c.step}" value="${h(s[c.key])}">${help}`;
          case "note": return `<p class="cv-help">${h(c.text)}</p>`;
          case "presets": return `<div class="cv-presets">${PRESETS.filter((p) => !(allAudio() && p.set.format !== "mp3")).map((p, i) =>
            `<button class="cv-preset" data-p="${i}"><b>${h(p.label)}</b><span>${h(p.help)}</span></button>`).join("")}</div>`;
          case "trim": {
            const d = st.files[0]?.info?.media?.duration;
            return `<div class="label">Keep only a part</div><div class="row cv-trim">
              <label>From <input class="cv-input" data-k="start" value="${h(s.start)}" placeholder="0:00"></label>
              <label>To <input class="cv-input" data-k="end" value="${h(s.end)}" placeholder="${h(d ? clock(d) : "end")}"></label>
              <button class="btn small" data-a="clearTrim">Whole file</button></div>
              <div class="cv-help">Write minutes:seconds, or play the preview and use "Start here" and "End here".</div>`;
          }
        }
        return "";
      }).join("");
      return items ? `<div class="card"><h3>${h(g.group)}</h3>${items}</div>` : "";
    }).join("");
  }
  function changed(full) {
    sample = null;
    if (full) drawSettings();
    drawSummary();
    schedulePreview(false);
  }
  $("cvSettings").addEventListener("click", (e) => {
    const s = set();
    const c = e.target.closest(".chip[data-v]");
    if (c) { s[c.closest("[data-k]").dataset.k] = c.dataset.v; return changed(true); }
    const p = e.target.closest("[data-p]");
    if (p) {
      const pre = PRESETS[+p.dataset.p].set;
      Object.assign(s, { aspect: "", targetMb: "", height: "" }, pre);
      return changed(true);
    }
    if (e.target.closest('[data-a="clearTrim"]')) { s.start = s.end = ""; return changed(true); }
  });
  $("cvSettings").addEventListener("input", (e) => {
    const k = e.target.dataset?.k;
    if (!k) return;
    const s = set();
    if (e.target.type === "checkbox") { s[k] = e.target.checked; return changed(true); }
    s[k] = e.target.type === "range" ? +e.target.value : e.target.value;
    const show = $("cvSettings").querySelector(`[data-show="${k}"]`);
    if (show) show.textContent = e.target.value;
    if (k === "pages") markPdfPages();
    if (k === "password2" || k === "password") { drawSummary(); return; }
    changed(false);
  });
  $("cvSettings").addEventListener("change", (e) => { if (e.target.type === "range" || e.target.dataset?.k === "password2") drawSettings(); });

  // ---------- what will happen ----------
  function spec() {
    const s = set(), o = {};
    const out = { tool: st.tool, format: s.format || "", opts: o };
    switch (st.tool) {
      case "media": {
        if (isVideoOut(s)) {
          if (s.height) out.height = +s.height;
          if (s.targetMb !== "" && s.format !== "gif") out.targetMb = +s.targetMb;
          for (const k of ["aspect", "rotate", "flip", "fps"]) if (s[k]) o[k] = s[k];
          if (s.format !== "gif" && s.format !== "webm") o.codec = s.codec;
          if (s.format === "webm" && s.codec === "av1") o.codec = "av1";
          if (s.stabilize) o.stabilize = "1";
          if (s.hw) o.hw = "1";
          if (s.removeAudio) out.removeAudio = true;
        }
        if (s.denoise) o.denoise = "1";
        if (s.loudnorm) o.loudnorm = "1";
        if (s.abitrate && isAudioOut(s)) o.abitrate = s.abitrate;
        if (s.speed !== "1") o.speed = s.speed;
        const a = parseClock(s.start), b = parseClock(s.end);
        if (a) out.startSec = a;
        if (b != null) out.endSec = b;
        break;
      }
      case "image":
        if (s.maxSide) out.height = +s.maxSide;
        o.quality = String(s.quality);
        if (s.targetKb && ["jpg", "webp"].includes(s.format)) o.targetKb = s.targetKb;
        break;
      case "images-pdf": o.page = s.page; if (s.margin) o.margin = "1"; o.quality = s.quality; break;
      case "frames": o.every = s.every; break;
      case "addsubs": o.mode = s.mode; o.size = s.size; break;
      case "pdf-pages": o.mode = s.mode; if (s.pages.trim()) o.pages = s.pages.trim(); break;
      case "pdf-compress": o.level = s.level; break;
      case "pdf-rotate": o.angle = s.angle; if (s.pages.trim()) o.pages = s.pages.trim(); break;
      case "pdf-protect": case "pdf-unlock": case "unpack": if (s.password) o.password = s.password; break;
      case "doc": o.page = s.page; o.font = s.font; break;
      case "pack": if (s.name.trim()) o.name = s.name.trim(); break;
      case "subs": if (+s.shift) o.shift = String(+s.shift); break;
    }
    return out;
  }
  function label() {
    const s = set(), t = tool(st.tool);
    if (!t) return "";
    if (st.tool === "media") {
      const sp = spec();
      return [s.format.toUpperCase(), isVideoOut(s) && s.format !== "gif" && { h265: "H.265", av1: "AV1" }[s.codec], sp.height && `${sp.height}p`,
        { "9:16": "vertical", "1:1": "square", "4:5": "4:5", "16:9": "wide" }[s.aspect], sp.targetMb === 0 ? "high quality" : sp.targetMb && `under ${sp.targetMb} MB`,
        s.speed !== "1" && `${s.speed}×`, (sp.startSec || sp.endSec != null) && `${clock(sp.startSec || 0)}–${sp.endSec != null ? clock(sp.endSec) : "end"}`,
        sp.removeAudio && "no sound", s.denoise && "clean sound", s.loudnorm && "even loudness"].filter(Boolean).join(" · ");
    }
    const fmt = s.format ? s.format.toUpperCase() : "";
    return [t.title, fmt && st.tool !== "pdf-pages" ? fmt : "", st.files.length > 1 ? `${st.files.length} files` : ""].filter(Boolean).join(" · ");
  }
  function drawSummary() {
    const t = tool(st.tool);
    $("cvGo").disabled = !t || !!st.job || !!st.rendering;
    if (!t) { $("cvSummary").textContent = ""; $("cvGo").textContent = "Convert"; return; }
    const s = set();
    let verb = t.verb;
    if (st.tool === "media" && st.files.length > 1) verb = `Convert ${st.files.length} files`;
    else if (st.tool === "image" && st.files.length > 1) verb = `Convert ${st.files.length} pictures`;
    else if (st.tool === "join") verb = `Join ${st.files.length} files`;
    else if (st.tool === "pdf-merge") verb = `Merge ${st.files.length} PDFs`;
    else if (s.format && ["media", "image", "doc", "subs", "data", "pack", "frames", "pdf-images"].includes(st.tool)) verb += ` to ${s.format.toUpperCase()}`;
    $("cvGo").textContent = verb;
    $("cvSummary").textContent = label();
    drawEstimate();
  }
  function drawEstimate() {
    const s = set(), e = $("cvEstimate");
    let t = "";
    if (st.tool === "media" && sample?.ok) {
      const each = st.files.length > 1 ? " each (roughly)" : "";
      t = sample.estimate > 0 ? `Result about ${size(sample.estimate)}${each}, from a ${Math.round(sample.seconds)}-second sample (${size(st.files[0].size)} now).` : "";
      if (spec().targetMb > 0) t = `Under ${spec().targetMb} MB.`;
    } else if (st.tool === "media" && spec().targetMb > 0) t = `Under ${spec().targetMb} MB.`;
    else if (st.tool === "image" && imgRes?.ok) {
      t = `${size(st.files[st.active].size)} → ${size(imgRes.size)}${st.files.length > 1 ? " for this picture" : ""}${imgRes.width ? ` · ${imgRes.width}×${imgRes.height}` : ""}.`;
    } else if (st.tool === "frames") t = estimateFrames() || "";
    else if (st.tool === "pack") t = `${st.files.length} ${st.files.length === 1 ? "file" : "files"}, ${size(st.files.reduce((a, f) => a + (f.size || 0), 0))} before packing.`;
    else if (st.tool === "pdf-protect" && s.password && s.password !== s.password2) t = "Type the same password twice.";
    e.textContent = t;
  }

  // ---------- previews ----------
  let prevTimer = null, prevAbort = null, sample = null, imgRes = null, prevKey = "";
  function stopPreview() {
    clearTimeout(prevTimer);
    prevAbort?.abort();
    $("cvPreview").innerHTML = "";
    $("cvPrevTabs").hidden = true;
    $("cvPrevNote").textContent = "";
    prevKey = "";
  }
  // new file/tool: redraw the preview now; settings change: the live parts update at once, real samples after a pause.
  function schedulePreview(structural) {
    clearTimeout(prevTimer);
    const f = st.files[st.active];
    if (!f || !st.tool) return stopPreview();
    const key = `${st.tool}|${st.files.map((x) => x.path).join("|")}|${st.active}|${f.info ? 1 : 0}`;
    if (structural || key !== prevKey) { prevKey = key; drawPreview(); }
    else liveUpdate();
    prevTimer = setTimeout(realPreview, structural ? 300 : 800);
  }
  function tabs(list, on, onPick) {
    const el = $("cvPrevTabs");
    el.hidden = !list;
    if (!list) return;
    el.innerHTML = list.map(([v, l]) => `<button class="chip${v === on ? " active" : ""}" data-v="${v}">${h(l)}</button>`).join("");
    el.onclick = (e) => { const b = e.target.closest("[data-v]"); if (b) onPick(b.dataset.v); };
  }
  let mediaTab = "original";
  function drawPreview() {
    const f = st.files[st.active], box = $("cvPreview"), note = $("cvPrevNote");
    box.className = "cv-preview";
    note.textContent = "";
    tabs(null);
    if (!sess) { box.innerHTML = `<div class="cv-empty">Starting the preview…</div>`; session().then(drawPreview).catch((e) => { box.innerHTML = `<div class="cv-empty err">${h(e.message)}</div>`; }); return; }
    const t = st.tool;
    if (["media", "join", "frames", "addsubs"].includes(t)) {
      const v = t === "addsubs" ? st.files.find((x) => x.kind === "video") : f;
      return drawMedia(v);
    }
    if (t === "image") return drawImage(f);
    if (t === "images-pdf") {
      box.innerHTML = `<div class="cv-grid">${st.files.map((x, i) => `<figure><img loading="lazy" src="${h(thumbURL(x.path, 300))}" alt=""><figcaption>Page ${i + 1}</figcaption></figure>`).join("")}</div>`;
      return;
    }
    if (t.startsWith("pdf-") || (t === "doc" && f.kind === "pdf")) return drawPdf(t === "pdf-merge" ? st.files : [f]);
    if (t === "doc") return drawDoc(f);
    if (t === "subs") return drawSubs(f);
    if (t === "unpack") return drawArchive(f);
    if (t === "data") return drawData(f);
    if (t === "pack") {
      box.innerHTML = `<ul class="cv-list">${st.files.map((x) => `<li><span>${h(x.name)}</span><span class="muted">${h(size(x.size))}</span></li>`).join("")}</ul>`;
      return;
    }
    box.innerHTML = `<div class="cv-empty">No preview for this.</div>`;
  }
  function liveUpdate() {
    const t = st.tool;
    if (["media", "frames", "addsubs", "join"].includes(t)) applyVideoLook();
    if (t === "pdf-rotate" || t === "pdf-pages" || t === "pdf-images") markPdfPages();
    if (t === "subs") drawCues();
  }
  function realPreview() {
    const t = st.tool, f = st.files[st.active];
    if (!f) return;
    if (t === "media" && f.kind !== "image") runSample(false);
    if (t === "image") runImage(f);
    drawEstimate();
  }

  // video / audio
  function drawMedia(f) {
    const box = $("cvPreview"), m = f.info?.media, audio = f.kind === "audio";
    if (!f.info) { box.innerHTML = `<div class="cv-empty">Reading the file…</div>`; return; }
    const src = fileURL(f.path);
    box.innerHTML = `
      <div class="cv-stage${audio ? " audio" : ""}" id="cvStage">
        ${audio ? `<div class="cv-audio-art">${icon("audio")}<b>${h(f.name)}</b></div><audio id="cvVideo" controls preload="metadata" src="${h(src)}"></audio>`
        : `<div class="cv-frame" id="cvFrame"><video id="cvVideo" controls preload="metadata" playsinline src="${h(src)}" poster="${h(thumbURL(f.path, 960, Math.min(3, (m?.duration || 0) / 3)))}"></video>
          <div class="cv-cue" id="cvCue"></div></div>`}
      </div>
      ${st.tool === "media" && st.files.length === 1 ? `<div class="row cv-trimbar"><button class="btn small" data-a="start">Start here</button><button class="btn small" data-a="end">End here</button>
        <span class="muted small" id="cvTrimInfo"></span></div>` : ""}`;
    const v = $("cvVideo");
    v.addEventListener("error", () => { // Chrome can't play this kind (AVI, WMV, some MKV): show a converted clip instead
      if (st.tool === "media" && !sample) { $("cvPrevNote").textContent = "Chrome can't play this file itself, so the preview is a converted 10-second clip."; runSample(true, true); }
      else if (!audio) $("cvPrevNote").textContent = "Chrome can't play this file itself. The poster shows a picture from it.";
    }, { once: true });
    if (st.tool === "media") tabs([["original", "Original"], ["sample", "Real result"]], mediaTab, (v2) => { mediaTab = v2; showMediaTab(); });
    box.querySelectorAll("[data-a]").forEach((b) => b.onclick = () => {
      const s = set(), tnow = v.currentTime;
      if (b.dataset.a === "start") s.start = clock(tnow); else s.end = clock(tnow);
      changed(true);
    });
    if (st.tool === "addsubs") loadCueOverlay();
    applyVideoLook();
  }
  function applyVideoLook() {
    const v = $("cvVideo"), frame = $("cvFrame");
    if (!v) return;
    const s = st.tool === "media" ? set() : {};
    const m = st.files[st.active]?.info?.media;
    v.playbackRate = +(s.speed || 1);
    v.preservesPitch = true;
    if (frame && m?.width) {
      // The frame takes the result's shape; the video inside is cropped (cover), turned and mirrored.
      let ratio = m.width / m.height;
      const shape = { "9:16": 9 / 16, "1:1": 1, "4:5": 4 / 5, "16:9": 16 / 9 }[s.aspect];
      if (shape) ratio = shape;
      const turned = s.rotate === "90" || s.rotate === "270";
      frame.style.aspectRatio = String(turned ? 1 / ratio : ratio);
      v.style.objectFit = shape ? "cover" : "contain";
      const fx = s.flip === "h" ? -1 : 1, fy = s.flip === "v" ? -1 : 1, rot = +(s.rotate || 0);
      if (turned) {
        Object.assign(v.style, { position: "absolute", left: "50%", top: "50%" });
        fitTurned(v, frame);
        v.style.transform = `translate(-50%,-50%) rotate(${rot}deg) scale(${fx},${fy})`;
      } else {
        Object.assign(v.style, { position: "", left: "", top: "", width: "100%", height: "100%" });
        v.style.transform = `rotate(${rot}deg) scale(${fx},${fy})`;
      }
    }
    const info = $("cvTrimInfo");
    if (info && st.tool === "media") {
      const sp = spec(), d = m?.duration;
      info.textContent = sp.startSec || sp.endSec != null ? `Keeping ${clock(sp.startSec || 0)} to ${sp.endSec != null ? clock(sp.endSec) : clock(d)}` : "";
    }
  }
  // A turned video: its width must equal the frame's height and its height the frame's width.
  function fitTurned(v, frame) {
    const r = frame.getBoundingClientRect();
    v.style.width = r.height + "px";
    v.style.height = r.width + "px";
  }
  function showMediaTab() {
    const box = $("cvStage");
    if (!box) return;
    let res = $("cvSample");
    if (mediaTab === "sample") {
      box.hidden = true;
      if (!res) { res = document.createElement("div"); res.id = "cvSample"; res.className = "cv-sample"; $("cvPreview").prepend(res); }
      res.hidden = false;
      drawSampleBox();
      if (!sample) runSample(false);
    } else { box.hidden = false; if (res) res.hidden = true; }
    tabs([["original", "Original"], ["sample", "Real result"]], mediaTab, (v2) => { mediaTab = v2; showMediaTab(); });
  }
  function drawSampleBox() {
    const res = $("cvSample");
    if (!res) return;
    if (!sample) { res.innerHTML = `<div class="cv-empty"><span class="spin"></span> Converting 10 seconds with these settings…</div>`; return; }
    if (!sample.ok) { res.innerHTML = `<div class="cv-empty err">${h(sample.error)}</div>`; return; }
    const isA = AUDIO_F.includes(set().format), isGif = set().format === "gif";
    const u = full(sample.url);
    res.innerHTML = (isGif ? `<img src="${h(u)}" alt="Result">` : isA ? `<audio controls autoplay src="${h(u)}"></audio>` : `<video controls autoplay muted playsinline src="${h(u)}"></video>`) +
      `<div class="muted small">The real result: ${Math.round(sample.seconds)} seconds from ${clock(sample.start)} · ${h(size(sample.size))}${sample.media?.width && !isA ? ` · ${sample.media.width}×${sample.media.height}` : ""} · made in ${sample.took.toFixed(1)} s</div>`;
  }
  async function runSample(force, proxy) {
    const f = st.files[st.active];
    if (!f?.info || st.tool !== "media") return;
    if (!force && mediaTab !== "sample" && f.info.media?.duration > 0 && sample) return;
    prevAbort?.abort();
    const ac = (prevAbort = new AbortController());
    sample = null;
    drawSampleBox();
    const sp = spec();
    if (proxy) sp.opts = { ...sp.opts, preview: "proxy" };
    const at = $("cvVideo")?.currentTime || 0;
    try {
      const r = await op("sample", { path: f.path, spec: sp, at: sp.startSec ? 0 : Math.max(0, at - 1) }, ac.signal);
      if (ac.signal.aborted) return;
      sample = r;
      if (proxy && r.ok) {
        const v = $("cvVideo");
        if (v) { v.src = full(r.url); v.load(); }
        sample = null; // not the real result
        return;
      }
      drawSampleBox();
      drawEstimate();
    } catch (e) { if (e.name !== "AbortError") { sample = { ok: false, error: e.message }; drawSampleBox(); } }
  }
  async function loadCueOverlay() {
    const subs = st.files.find((x) => x.kind === "subs");
    const v = $("cvVideo"), cue = $("cvCue");
    if (!subs || !v || !cue) return;
    const r = await op("subs", { path: subs.path }).catch(() => null);
    if (!r?.ok) return;
    $("cvPrevNote").textContent = `${r.cues.length} subtitle lines. Play the video to see them.`;
    v.addEventListener("timeupdate", () => {
      const t = v.currentTime, c = r.cues.find((x) => t >= x.start && t <= x.end);
      cue.textContent = c ? c.text : "";
      cue.className = "cv-cue " + (set().size || "medium");
    });
  }

  // pictures: before/after slider with the real encoded result
  function drawImage(f) {
    const box = $("cvPreview");
    const native = /^(jpe?g|png|gif|webp|avif|bmp)$/.test(extOf(f.name));
    const before = native ? fileURL(f.path) : thumbURL(f.path, 2000);
    box.innerHTML = `<div class="cv-compare" id="cvCompare">
        <img class="before" src="${h(before)}" alt="Before">
        <div class="after-wrap" id="cvAfterWrap"><img class="after" id="cvAfter" alt=""></div>
        <span class="tag l">Before · ${h(size(f.size))}</span><span class="tag r" id="cvAfterTag">After</span>
        <input type="range" min="0" max="100" value="50" id="cvSlide" aria-label="Compare before and after">
        <div class="cv-handle" id="cvHandle"></div>
      </div>`;
    const slide = () => { const v = $("cvSlide").value; $("cvAfterWrap").style.clipPath = `inset(0 0 0 ${v}%)`; $("cvHandle").style.left = v + "%"; };
    $("cvSlide").oninput = slide;
    slide();
    $("cvPrevNote").textContent = st.files.length > 1 ? "Showing the picture selected in the list. Click another one to see it." : "Drag across the picture to compare.";
    imgRes = null;
  }
  async function runImage(f) {
    if (!$("cvAfter")) return;
    prevAbort?.abort();
    const ac = (prevAbort = new AbortController());
    $("cvAfterTag").innerHTML = `<span class="spin"></span> After`;
    try {
      const r = await op("image", { path: f.path, spec: spec() }, ac.signal);
      if (ac.signal.aborted) return;
      imgRes = r;
      if (!r.ok) { $("cvAfterTag").textContent = r.error; return; }
      $("cvAfter").src = full(r.url);
      $("cvAfterTag").textContent = `After · ${size(r.size)}`;
      drawEstimate();
    } catch (e) { if (e.name !== "AbortError") $("cvAfterTag").textContent = e.message; }
  }

  // PDFs: real page pictures with pdf.js; tap pages to pick them
  let pdfjs = null;
  async function loadPdfjs() {
    if (pdfjs) return pdfjs;
    pdfjs = await import(new URL("vendor/pdfjs/pdf.min.mjs", location.href).href);
    pdfjs.GlobalWorkerOptions.workerSrc = new URL("vendor/pdfjs/pdf.worker.min.mjs", location.href).href;
    return pdfjs;
  }
  async function openPdf(f, password) {
    const lib = await loadPdfjs();
    return lib.getDocument({ url: fileURL(f.path), password: password || undefined, isEvalSupported: false }).promise;
  }
  let pdfDrawId = 0, pdfCount = 0;
  async function drawPdf(files) {
    const box = $("cvPreview"), id = ++pdfDrawId;
    box.innerHTML = `<div class="cv-pages" id="cvPages"><div class="cv-empty"><span class="spin"></span> Drawing pages…</div></div>`;
    const merge = files.length > 1;
    const html = [];
    let total = 0;
    try {
      const pages = $("cvPages");
      pages.innerHTML = "";
      for (const f of files) {
        if (f.info?.locked) {
          const pw = st.tool === "pdf-unlock" ? set().password : "";
          if (!pw) { pages.innerHTML += `<div class="cv-empty">🔒 ${h(f.name)} is locked. Type its password to see the pages.</div>`; continue; }
        }
        const doc = await openPdf(f, f.info?.locked ? set().password : "");
        if (id !== pdfDrawId) return;
        const n = merge ? Math.min(doc.numPages, 1) : doc.numPages;
        total += doc.numPages;
        for (let p = 1; p <= Math.min(n, 120); p++) {
          const fig = document.createElement("figure");
          fig.dataset.p = p;
          fig.innerHTML = `<canvas></canvas><figcaption>${merge ? h(f.name) + ` · ${doc.numPages} p.` : "Page " + p}</figcaption>`;
          pages.appendChild(fig);
          const page = await doc.getPage(p);
          if (id !== pdfDrawId) return;
          const vp1 = page.getViewport({ scale: 1 });
          const vp = page.getViewport({ scale: 220 / vp1.width });
          const c = fig.querySelector("canvas");
          c.width = vp.width; c.height = vp.height;
          await page.render({ canvasContext: c.getContext("2d"), viewport: vp }).promise;
        }
        if (n < doc.numPages && !merge) pages.insertAdjacentHTML("beforeend", `<div class="cv-empty">…and ${doc.numPages - 120} more pages</div>`);
        if (st.tool === "doc") $("cvPrevNote").textContent = "The words on these pages are what will be saved.";
      }
      pdfCount = total;
      if (["pdf-pages", "pdf-images", "pdf-rotate"].includes(st.tool)) $("cvPrevNote").textContent = "Tap pages to pick them (or write the pages in the settings).";
      markPdfPages();
    } catch (e) {
      if (id !== pdfDrawId) return;
      $("cvPages").innerHTML = `<div class="cv-empty err">${h(/password/i.test(e.message) ? "That password is wrong." : "Couldn't draw the pages: " + e.message)}</div>`;
    }
  }
  function markPdfPages() {
    const pages = $("cvPages");
    if (!pages) return;
    const s = set();
    const pickable = ["pdf-pages", "pdf-images", "pdf-rotate"].includes(st.tool);
    const chosen = pickable && s.pages?.trim() ? parseRanges(s.pages, pdfCount || 9999) : null;
    pages.querySelectorAll("figure[data-p]").forEach((fig) => {
      const p = +fig.dataset.p, on = !chosen || chosen.includes(p);
      fig.classList.toggle("off", pickable && !on);
      fig.classList.toggle("pick", pickable && !!chosen && on);
      const c = fig.querySelector("canvas");
      c.style.transform = st.tool === "pdf-rotate" && on ? `rotate(${s.angle}deg)` : "";
      const order = st.tool === "pdf-pages" && s.mode === "keep" && chosen ? chosen.indexOf(p) : -1;
      fig.querySelector("figcaption").textContent = `Page ${p}${order >= 0 && chosen.length > 1 ? ` → ${order + 1}.` : ""}`;
    });
  }
  $("cvPreview").addEventListener("click", (e) => {
    const fig = e.target.closest("#cvPages figure[data-p]");
    if (!fig || !["pdf-pages", "pdf-images", "pdf-rotate"].includes(st.tool)) return;
    const s = set(), p = +fig.dataset.p, n = pdfCount || 1;
    let cur = s.pages.trim() ? parseRanges(s.pages, n) : [];
    if (!s.pages.trim()) cur = [];
    cur = cur.includes(p) ? cur.filter((x) => x !== p) : [...cur, p];
    s.pages = st.tool === "pdf-pages" && s.mode === "keep" ? cur.join(", ") : rangesText(cur);
    const inp = $("cvSettings").querySelector('[data-k="pages"]');
    if (inp) inp.value = s.pages;
    markPdfPages();
    drawSummary();
  });

  // documents: the text as it will come out, in a locked-down frame
  async function drawDoc(f) {
    const box = $("cvPreview");
    box.innerHTML = `<div class="cv-empty"><span class="spin"></span> Reading…</div>`;
    const r = await op("doc", { path: f.path }).catch((e) => ({ ok: false, error: e.message }));
    if (!r.ok) { box.innerHTML = `<div class="cv-empty err">${h(r.error)}</div>`; return; }
    const css = `body{font:14px/1.6 system-ui,-apple-system,"Segoe UI",sans-serif;margin:16px;color:#17181c;background:#fff}h1,h2,h3{line-height:1.25}pre{white-space:pre-wrap;background:#f4f5f7;padding:8px}img{display:none}`;
    box.innerHTML = `<iframe class="cv-doc" sandbox="" title="Document preview"></iframe>`;
    box.querySelector("iframe").srcdoc = `<!doctype html><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'"><style>${css}</style>${r.html}`;
    $("cvPrevNote").textContent = `${r.words.toLocaleString()} words. Headings, lists, bold and italic are kept; pictures and complex layouts are left out.`;
  }

  // subtitles: the lines with their (moved) times
  let cues = null;
  async function drawSubs(f) {
    const box = $("cvPreview");
    box.innerHTML = `<div class="cv-empty"><span class="spin"></span> Reading…</div>`;
    const r = await op("subs", { path: f.path }).catch((e) => ({ ok: false, error: e.message }));
    if (!r.ok) { box.innerHTML = `<div class="cv-empty err">${h(r.error)}</div>`; return; }
    cues = r.cues;
    drawCues();
  }
  function drawCues() {
    if (!cues) return;
    const shift = +set().shift || 0;
    const ts = (x) => { x = Math.max(0, x); return `${clock(x)}.${String(Math.round((x % 1) * 10)) % 10}`; };
    $("cvPreview").innerHTML = `<ol class="cv-cues">${cues.slice(0, 400).map((c) =>
      `<li><span class="t${shift ? " moved" : ""}">${ts(c.start + shift)} → ${ts(c.end + shift)}</span><span>${h(c.text)}</span></li>`).join("")}</ol>`;
    $("cvPrevNote").textContent = `${cues.length} lines${shift ? `, all moved ${shift > 0 ? "later" : "earlier"} by ${Math.abs(shift)} s` : ""}.`;
  }

  async function drawArchive(f) {
    const box = $("cvPreview");
    box.innerHTML = `<div class="cv-empty"><span class="spin"></span> Looking inside…</div>`;
    const r = await op("archive", { path: f.path }).catch((e) => ({ ok: false, error: e.message }));
    if (!r.ok) { box.innerHTML = `<div class="cv-empty err">${h(r.error)}</div>`; return; }
    if (!r.entries?.length) { box.innerHTML = `<div class="cv-empty">${r.locked ? "🔒 This archive is locked, so its contents are hidden. Type the password and unpack it." : "Nothing inside."}</div>`; return; }
    const total = r.entries.reduce((a, x) => a + Math.max(0, x.size), 0);
    box.innerHTML = `<ul class="cv-list">${r.entries.map((x) => `<li><span>${h(x.name)}</span><span class="muted">${h(size(x.size))}</span></li>`).join("")}</ul>`;
    $("cvPrevNote").textContent = `${r.entries.length}${r.entries.length >= 500 ? "+" : ""} files${total ? `, ${size(total)} when unpacked` : ""}.${r.locked ? " Some files are locked with a password." : ""}`;
  }

  async function drawData(f) {
    const box = $("cvPreview");
    box.innerHTML = `<div class="cv-empty"><span class="spin"></span> Reading…</div>`;
    const r = await op("data", { path: f.path }).catch((e) => ({ ok: false, error: e.message }));
    if (!r.ok) { box.innerHTML = `<div class="cv-empty err">${h(r.error)}</div>`; return; }
    box.innerHTML = `<div class="cv-table"><table><thead><tr>${(r.header || []).map((c) => `<th>${h(c)}</th>`).join("")}</tr></thead>
      <tbody>${(r.rows || []).map((row) => `<tr>${row.map((c) => `<td>${h(c)}</td>`).join("")}</tr>`).join("")}</tbody></table></div>`;
    $("cvPrevNote").textContent = `${r.total} rows${r.total > 100 ? " (showing the first 100)" : ""}, ${(r.header || []).length} columns.`;
  }

  // ---------- converting ----------
  function problems() {
    const s = set(), sp = spec();
    if (st.tool === "media") {
      if (Number.isNaN(sp.startSec) || Number.isNaN(sp.endSec)) return "The trim times look wrong. Write minutes:seconds, for example 1:30.";
      if (sp.endSec != null && sp.endSec <= (sp.startSec || 0)) return "The end of the trim must come after the start.";
    }
    if (st.tool === "pdf-protect") {
      if (!s.password) return "Type a password first.";
      if (s.password !== s.password2) return "The two passwords are different.";
    }
    if (st.tool === "pdf-unlock" && st.files[0]?.info?.locked && !s.password) return "Type the PDF's password first.";
    return "";
  }
  $("cvGo").onclick = async () => {
    const problem = problems();
    if (problem) return msg(problem, true);
    msg("");
    $("cvResult").hidden = true;
    if (st.tool === "pdf-images") return renderPdfPages();
    const sp = spec(), paths = st.files.map((f) => f.path);
    $("cvGo").disabled = true;
    try {
      if (st.tool === "doc" && st.files[0].kind === "pdf") {
        progress(5, "Reading the PDF's text…");
        sp.opts.textFile = await pdfTextFile(st.files[0]);
      }
      const name = st.files.length > 1 ? `${st.files[0].name} and ${st.files.length - 1} more` : st.files[0].name;
      const r = await send({ type: "convert", path: paths[0], paths, name, spec: sp, label: label() });
      if (!r?.ok) throw new Error(r?.error || "Couldn't start the conversion.");
      st.job = r.id;
      progress(0, "Starting…");
      watchJob();
    } catch (e) { progress(null); msg(e.message, true); }
    drawSummary();
  };
  function progress(pct, text) {
    const p = $("cvProgress");
    p.hidden = pct == null;
    if (pct == null) return;
    p.querySelector("i").style.width = Math.max(2, pct) + "%";
    p.querySelector("span").textContent = text;
  }
  let watchTimer = null;
  function watchJob() {
    clearInterval(watchTimer);
    watchTimer = setInterval(() => {
      const j = jobs[st.job];
      if (!j) return;
      if (j.status === "done") { clearInterval(watchTimer); finished(j.file, j.id); }
      else if (j.status === "error" || j.status === "cancelled") {
        clearInterval(watchTimer); st.job = null; progress(null);
        msg(j.status === "cancelled" ? "Cancelled." : (j.error || "Couldn't convert."), j.status === "error");
        drawSummary();
      } else progress(j.percent || 0, `${Math.round(j.percent || 0)}%${j.eta && j.eta !== "?" ? ` · ${j.eta} left` : ""}`);
    }, 400);
  }
  function finished(file, jobId) {
    st.job = null; st.rendering = null;
    progress(null);
    const name = String(file || "").split(/[\\/]/).pop();
    const r = $("cvResult");
    r.hidden = false;
    r.innerHTML = `<div class="cv-done">✓ Done: <b>${h(name)}</b></div><div class="row">
      <button class="btn" data-a="open">Open</button><button class="btn" data-a="reveal">Show in folder</button>
      <button class="btn" data-a="again">Convert something else</button></div>`;
    r.onclick = (e) => {
      const a = e.target.closest("[data-a]")?.dataset.a;
      if (a === "open") send(jobId ? { type: "open", id: jobId } : { type: "reveal", file }).then((x) => { if (x && !x.ok) toast(x.error || "Couldn't open it.", true); });
      if (a === "reveal") send(jobId ? { type: "reveal", id: jobId } : { type: "reveal", file });
      if (a === "again") { st.files = []; st.tool = null; r.hidden = true; refresh(); }
    };
    drawSummary();
  }

  // PDF pages to pictures, drawn here with pdf.js and saved by the helper.
  async function renderPdfPages() {
    const f = st.files[0], s = set();
    st.rendering = true; drawSummary();
    let dir = "";
    try {
      const doc = await openPdf(f);
      const pages = s.pages.trim() ? parseRanges(s.pages, doc.numPages) : Array.from({ length: doc.numPages }, (_, i) => i + 1);
      if (!pages.length) throw new Error(`Those pages aren't in this PDF (it has ${doc.numPages} pages).`);
      const r = await op("renderStart", { path: f.path });
      if (!r.ok) throw new Error(r.error);
      dir = r.dir;
      const digits = Math.max(2, String(doc.numPages).length), base = f.name.replace(/\.pdf$/i, "");
      for (let i = 0; i < pages.length; i++) {
        if (st.rendering === "cancel") throw new Error("Cancelled.");
        progress((i / pages.length) * 100, `Page ${i + 1} of ${pages.length}`);
        const page = await doc.getPage(pages[i]);
        let scale = +s.dpi / 72;
        const vp1 = page.getViewport({ scale: 1 });
        if (Math.max(vp1.width, vp1.height) * scale > 6000) scale = 6000 / Math.max(vp1.width, vp1.height);
        const vp = page.getViewport({ scale });
        const c = document.createElement("canvas");
        c.width = Math.ceil(vp.width); c.height = Math.ceil(vp.height);
        const ctx = c.getContext("2d");
        ctx.fillStyle = "#fff"; ctx.fillRect(0, 0, c.width, c.height);
        await page.render({ canvasContext: ctx, viewport: vp, intent: "print" }).promise;
        const blob = await new Promise((res) => c.toBlob(res, s.format === "png" ? "image/png" : "image/jpeg", 0.9));
        const file = new File([blob], `${base} page ${String(pages[i]).padStart(digits, "0")}.${s.format}`);
        const u = await upload(file, "&dest=" + encodeURIComponent(dir));
        if (!u.ok) throw new Error(u.error);
        page.cleanup();
      }
      const d = await op("renderDone", { dir });
      finished(d.file || dir, null);
    } catch (e) {
      if (dir) op("renderDone", { dir, cancel: true }).catch(() => {});
      st.rendering = null; progress(null);
      msg(/password/i.test(e.message) ? "This PDF is locked. Use \"Remove a password\" first." : e.message, true);
      drawSummary();
    }
  }
  // All of a PDF's text, saved for the document converter.
  async function pdfTextFile(f) {
    const doc = await openPdf(f);
    const parts = [];
    for (let p = 1; p <= doc.numPages; p++) {
      const page = await doc.getPage(p);
      const tc = await page.getTextContent();
      let line = "", lastY = null, out = [];
      for (const it of tc.items) {
        const y = it.transform?.[5];
        if (lastY != null && Math.abs(y - lastY) > 2) { out.push(line); line = ""; }
        line += it.str + (it.hasEOL ? "\n" : "");
        lastY = y;
      }
      out.push(line);
      parts.push(out.join("\n").replace(/\n{3,}/g, "\n\n"));
      progress((p / doc.numPages) * 30, `Reading page ${p} of ${doc.numPages}…`);
    }
    const text = parts.join("\n\n");
    if (!text.trim()) throw new Error("No text found. This PDF may be scanned pictures of pages.");
    const r = await upload(new File([text], f.name.replace(/\.pdf$/i, "") + ".txt", { type: "text/plain" }));
    if (!r.ok) throw new Error(r.error);
    return r.file.path;
  }

  // ---------- wiring ----------
  $("cvPick").onclick = pick;
  $("cvAdd").onclick = pick;
  $("cvClear").onclick = () => { st.files = []; st.tool = null; st.sets = {}; sample = null; $("cvResult").hidden = true; msg(""); refresh(); };
  $("cvFromDl").onchange = async (e) => {
    const j = jobs[e.target.value];
    e.target.value = "";
    if (!j) return;
    try {
      const r = await op("allow", { path: j.file });
      if (r.ok) addEntries([r.file]); else msg(r.error, true);
    } catch (err) { msg(err.message, true); }
  };
  const page = $("convert");
  let dragDepth = 0;
  page.addEventListener("dragenter", (e) => { if ([...(e.dataTransfer?.types || [])].includes("Files")) { e.preventDefault(); dragDepth++; page.classList.add("dragging"); } });
  page.addEventListener("dragover", (e) => { if ([...(e.dataTransfer?.types || [])].includes("Files")) e.preventDefault(); });
  page.addEventListener("dragleave", () => { if (--dragDepth <= 0) { dragDepth = 0; page.classList.remove("dragging"); } });
  page.addEventListener("drop", (e) => {
    if (!e.dataTransfer?.files?.length) return;
    e.preventDefault(); e.stopPropagation();
    dragDepth = 0; page.classList.remove("dragging");
    dropFiles(e.dataTransfer.files);
  });
  window.addEventListener("resize", () => { if (st.tool) applyVideoLook(); });
  // Start the helper's preview server as soon as the page is opened, so the first preview is quick.
  if (location.hash === "#convert") { session().catch(() => {}); window.fillDownloadsPicker(); }
})();
