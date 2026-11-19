// AnySave "Captions" page (Chrome extension and the Mac/Windows apps): make captions for a video
// offline, fix them, style them with a live preview, then save subtitles or a video with the
// captions drawn on. Uses the Convert page's helper connection (window.AnySaveCv, convert.js).
//
// The preview and the burned-in video use the same fonts (built into the helper) and the same
// numbers: sizes are a share of the video's height, and the ASS file FFmpeg draws is made here
// from exactly what the preview shows.
(() => {
  "use strict";
  const cv = () => window.AnySaveCv;
  const h = (s) => esc(s);

  // The bundled fonts. ratio = the font's "win" line height per em: FFmpeg's font size is that line
  // height, the browser's is the em, so ASS size = CSS size × ratio (measured, see cv_captions.go).
  const FONTS = {
    noto: { name: "Noto Sans", file: "NotoSans-Bold.ttf", ratio: 1.519, bold: 1, label: "Noto Sans", bn: "notobn" },
    mont: { name: "Montserrat Black", file: "Montserrat-Black.ttf", ratio: 1.562, bold: 0, label: "Montserrat", bn: "hind" },
    anton: { name: "Anton", file: "Anton-Regular.ttf", ratio: 1.7334, bold: 0, label: "Anton (tall)", bn: "hind" },
    hind: { name: "Hind Siliguri", file: "HindSiliguri-Bold.ttf", ratio: 1.617, bold: 1, label: "Hind Siliguri", bn: "hind" },
    poppins: { name: "Poppins ExtraBold", file: "Poppins-ExtraBold.ttf", ratio: 1.762, bold: 0, label: "Poppins", bn: "hind" },
    bebas: { name: "Bebas Neue", file: "BebasNeue-Regular.ttf", ratio: 1.3, bold: 0, label: "Bebas Neue (tall)", bn: "hind" },
    bangers: { name: "Bangers", file: "Bangers-Regular.ttf", ratio: 1.757, bold: 0, label: "Bangers (comic)", bn: "hind" },
    marker: { name: "Permanent Marker", file: "PermanentMarker-Regular.ttf", ratio: 1.4268, bold: 0, label: "Marker (hand-drawn)", bn: "hind" },
    lobster: { name: "Lobster", file: "Lobster-Regular.ttf", ratio: 1.49, bold: 0, label: "Lobster (script)", bn: "hind" },
    notobn: { name: "Noto Sans Bengali", file: "NotoSansBengali-Bold.ttf", ratio: 1.403, bold: 1, hidden: true },
  };
  const BN_RUN = /([ঀ-৿।॥‌‍]+)/;
  const PRESETS = [
    { id: "clean", title: "Clean", desc: "White, thin outline", s: { font: "noto", size: 5.2, upper: false, color: "#ffffff", outline: 9, outlineColor: "#000000", shadow: true, box: false, mode: "none", hl: "#ffd400", pos: "bottom", offset: 8 } },
    { id: "pop", title: "Bold pop", desc: "Big, word lights up", s: { font: "mont", size: 6.2, upper: true, color: "#ffffff", outline: 14, outlineColor: "#000000", shadow: false, box: false, mode: "pop", hl: "#ffd400", pos: "middle", offset: 0 } },
    { id: "headline", title: "Headline", desc: "Tall letters, green word", s: { font: "anton", size: 7.5, upper: true, color: "#ffffff", outline: 10, outlineColor: "#000000", shadow: true, box: false, mode: "word", hl: "#3ce37b", pos: "bottom", offset: 16 } },
    { id: "boxed", title: "Boxed", desc: "Text on a dark box", s: { font: "noto", size: 4.8, upper: false, color: "#ffffff", outline: 0, outlineColor: "#000000", shadow: false, box: true, boxColor: "#000000", boxOpacity: 70, mode: "none", hl: "#ffd400", pos: "bottom", offset: 8 } },
    { id: "reveal", title: "Word by word", desc: "Words appear as spoken", s: { font: "mont", size: 5.6, upper: false, color: "#ffffff", outline: 12, outlineColor: "#111111", shadow: false, box: false, mode: "reveal", hl: "#ffffff", pos: "bottom", offset: 14 } },
    { id: "bangla", title: "Bangla", desc: "Hind Siliguri, word lights up", s: { font: "hind", size: 5.6, upper: false, color: "#ffffff", outline: 11, outlineColor: "#000000", shadow: true, box: false, mode: "word", hl: "#ffd400", pos: "bottom", offset: 9 } },
    { id: "comic", title: "Comic", desc: "Bangers, yellow word pops", s: { font: "bangers", size: 7.2, upper: true, color: "#ffffff", outline: 16, outlineColor: "#000000", shadow: true, box: false, mode: "pop", hl: "#ffe14d", pos: "middle", offset: 0, spacing: 4, anim: "pop" } },
    { id: "neon", title: "Neon", desc: "Glowing outline", s: { font: "poppins", size: 5.4, upper: false, color: "#ffffff", outline: 5, outlineColor: "#00e5ff", shadow: false, box: false, mode: "word", hl: "#ff4fd8", glow: 14, pos: "bottom", offset: 12, anim: "fade" } },
    { id: "tall", title: "Tall caps", desc: "Bebas Neue, red word", s: { font: "bebas", size: 9, upper: true, color: "#ffffff", outline: 8, outlineColor: "#000000", shadow: true, box: false, mode: "word", hl: "#ff3b3b", spacing: 3, pos: "bottom", offset: 14, anim: "slide" } },
    { id: "marker", title: "Marker", desc: "Hand-drawn look", s: { font: "marker", size: 6, upper: false, color: "#ffffff", outline: 9, outlineColor: "#000000", shadow: true, box: false, mode: "none", pos: "bottom", offset: 10, anim: "pop" } },
    { id: "script", title: "Script", desc: "Lobster, soft and warm", s: { font: "lobster", size: 6.4, upper: false, color: "#fff4e0", outline: 8, outlineColor: "#5a1f33", shadow: true, box: false, mode: "none", pos: "bottom", offset: 10, anim: "fade" } },
    { id: "bubble", title: "Bubble", desc: "Dark text on white", s: { font: "poppins", size: 4.8, upper: false, color: "#111111", outline: 0, outlineColor: "#000000", shadow: false, box: true, boxColor: "#ffffff", boxOpacity: 100, mode: "word", hl: "#e5243b", pos: "bottom", offset: 12, anim: "pop" } },
    { id: "lower", title: "Lower third", desc: "White on a red bar", s: { font: "poppins", size: 4.2, upper: false, color: "#ffffff", outline: 0, outlineColor: "#000000", shadow: false, box: true, boxColor: "#e5243b", boxOpacity: 100, mode: "none", pos: "bottom", offset: 12, width: 70, anim: "slide" } },
    { id: "tv", title: "Classic TV", desc: "Yellow, easy to read", s: { font: "noto", size: 4.6, upper: false, color: "#ffe600", outline: 8, outlineColor: "#000000", shadow: true, box: false, mode: "none", pos: "bottom", offset: 6 } },
    { id: "minimal", title: "Minimal", desc: "Small, soft shadow only", s: { font: "poppins", size: 4.2, upper: false, color: "#ffffff", outline: 0, outlineColor: "#000000", shadow: true, box: false, mode: "none", pos: "bottom", offset: 8, anim: "fade" } },
  ];
  // Values every style has (presets only list what they change).
  const BASE = { upper: false, color: "#ffffff", outline: 9, outlineColor: "#000000", shadow: false, box: false, boxColor: "#000000", boxOpacity: 70, mode: "none", hl: "#ffd400", spacing: 0, glow: 0, anim: "none", width: 86 };
  // Settings that are about the words, not the look: a new look keeps them.
  const SPK0 = ["#ffffff", "#ffd400", "#5ce1ff", "#ff7ab6"];
  const WORDISH = { auto: false, autoColor: "#ffd400", autoBig: false, autoWords: "", spk: SPK0 };
  // Where a preset puts the caption: x is the middle of the text, y the edge named by anchor (both % of the video).
  const posFrom = (pos, off = 8) => pos === "top" ? { x: 50, y: off, anchor: "top" } : pos === "middle" ? { x: 50, y: 50, anchor: "middle" } : { x: 50, y: 100 - off, anchor: "bottom" };
  function normalize(s) {
    s = { ...BASE, ...WORDISH, ...PRESETS[0].s, preset: "clean", ...(s || {}) };
    if (!Array.isArray(s.spk) || s.spk.length !== 4) s.spk = [...SPK0];
    if (!(s.x >= 0 && s.y >= 0 && ["top", "middle", "bottom"].includes(s.anchor))) Object.assign(s, posFrom(s.pos, s.offset));
    if (!FONTS[s.font] || FONTS[s.font].hidden) s.font = "noto";
    return s;
  }
  // A preset's look; it keeps the caption where the user put it.
  function applyPreset(cur, p) {
    const next = normalize({ ...BASE, ...p.s, preset: p.id });
    if (cur.placed && !p.mine) Object.assign(next, { x: cur.x, y: cur.y, anchor: cur.anchor, width: cur.width, placed: true });
    if (!p.mine) for (const k of Object.keys(WORDISH)) next[k] = cur[k];
    return next;
  }
  const STYLE0 = normalize({});
  const CHUNKS = [["", "As spoken"], ["1", "1 word"], ["2", "2 words"], ["3", "3 words"], ["4", "4 words"], ["6", "6 words"], ["8", "8 words"]];
  const VIDEO_EXT = /\.(mp4|m4v|mov|webm|mkv|avi|3gp|flv|wmv|ts|mts|m2ts|mpg|mpeg|ogv)$/i;
  const SUBS_EXT = /\.(srt|vtt|ass|ssa)$/i;

  // ---------- state ----------
  const st = {
    video: null, // {path, name, size}
    w: 1920, h: 1080, dur: 0,
    lang: "", cues: [], orig: null, chunk: "",
    style: normalize(loadJSON("anysave.cap.style")),
    job: null, burnJob: null, sel: -1, follow: true,
    dub: null, // {from, to, model, voice, keep, lines: [{s, e, src, t}]}
    vw: 1920, vh: 1080, // the video's own size; w × h is the finished frame (see the shape)
    frame: { ar: "orig", fit: "crop", cx: 50 },
  };
  let undo = [], redo = [];

  function loadJSON(k) { try { return JSON.parse(localStorage.getItem(k) || "null"); } catch { return null; } }
  function saveJSON(k, v) { try { localStorage.setItem(k, JSON.stringify(v)); } catch {} }
  const projectKey = () => st.video ? `anysave.cap.p.${st.video.name}|${st.video.size}` : "";
  let saveTimer = null;
  function persist() {
    clearTimeout(saveTimer);
    saveTimer = setTimeout(() => {
      saveJSON("anysave.cap.style", st.style);
      if (st.video && st.cues.length) saveJSON(projectKey(), { lang: st.lang, cues: st.cues, orig: st.orig, chunk: st.chunk, style: st.style, dub: st.dub, frame: st.frame, at: Date.now() });
    }, 400);
  }

  // ---------- time ----------
  function clock(t, cs = true) {
    t = Math.max(0, t || 0);
    const m = Math.floor(t / 60), s = t - m * 60;
    return `${m}:${s.toFixed(cs ? 2 : 0).padStart(cs ? 5 : 2, "0")}`;
  }
  function parseClock(v) {
    v = String(v).trim().replace(",", ".");
    if (!v) return NaN;
    const p = v.split(":").map(Number);
    if (p.some(isNaN)) return NaN;
    return p.reduce((a, x) => a * 60 + x, 0);
  }

  // ---------- words ----------
  const tokens = (text) => String(text).split(/\s+/).filter(Boolean);
  // Spread a line's time over its words by length (used when the speech engine's times aren't known).
  function spread(c) {
    const t = tokens(c.text);
    const wt = t.map((x) => [...x].length + 2), sum = wt.reduce((a, b) => a + b, 0) || 1;
    let at = c.s;
    c.words = t.map((x, i) => { const d = (c.e - c.s) * wt[i] / sum; const w = { t: x, s: at, e: at + d }; at += d; return w; });
  }
  function words(c) {
    if (!c.words || c.words.length !== tokens(c.text).length || c.words.map((w) => w.t).join(" ") !== tokens(c.text).join(" ")) {
      if (c.words && c.words.length === tokens(c.text).length) tokens(c.text).forEach((t, i) => { c.words[i].t = t; });
      else spread(c);
    }
    return c.words;
  }
  function retime(c, s, e) { // move a line's words along with its new start and end
    const os = c.s, od = c.e - c.s || 1;
    c.s = s; c.e = e;
    if (c.words) for (const w of c.words) { w.s = s + (w.s - os) / od * (e - s); w.e = s + (w.e - os) / od * (e - s); }
  }
  // New lines of n words each, also cut at pauses and at the end of sentences.
  function chunk(n) {
    const all = st.cues.flatMap((c) => words(c).map((w) => ({ ...w })));
    const out = [];
    let cur = [];
    const flush = () => { if (cur.length) out.push({ s: cur[0].s, e: cur[cur.length - 1].e, text: cur.map((w) => w.t).join(" "), words: cur }); cur = []; };
    for (const w of all) {
      const prev = cur[cur.length - 1];
      if (prev && (cur.length >= n || w.s - prev.e > 0.6 || /[.?!।]$/.test(prev.t) || cur.map((x) => x.t).join(" ").length + w.t.length > 34)) flush();
      cur.push(w);
    }
    flush();
    for (let i = 0; i + 1 < out.length; i++) if (out[i + 1].s - out[i].e < 0.3) out[i].e = out[i + 1].s; // no flicker between lines
    return out;
  }

  // ---------- undo ----------
  function snapshot() {
    undo.push(JSON.stringify({ cues: st.cues, chunk: st.chunk, style: st.style, frame: st.frame }));
    if (undo.length > 100) undo.shift();
    redo = [];
  }
  function restore(from, to) {
    if (!from.length) return;
    to.push(JSON.stringify({ cues: st.cues, chunk: st.chunk, style: st.style, frame: st.frame }));
    const s = JSON.parse(from.pop());
    st.cues = s.cues; st.chunk = s.chunk;
    if (s.style) st.style = normalize(s.style);
    if (s.frame && JSON.stringify(s.frame) !== JSON.stringify(st.frame)) { st.frame = s.frame; applyFrame(); }
    drawAll(); persist();
  }
  let typingSnap = null; // one undo step per burst of typing
  function snapTyping() {
    if (!typingSnap) snapshot();
    clearTimeout(typingSnap);
    typingSnap = setTimeout(() => { typingSnap = null; }, 900);
  }

  // ---------- page messages ----------
  function msg(text, err) {
    const m = $("capMsg");
    m.hidden = !text; m.className = "msg" + (err ? " err" : ""); m.textContent = text || "";
  }

  // ---------- fonts for the preview ----------
  let fontsAdded = false;
  async function addFonts() {
    if (fontsAdded) return;
    const s = await cv().session();
    const css = Object.entries(FONTS).map(([k, f]) =>
      `@font-face{font-family:"AS ${k}";src:url("${s.base}/cv/font?name=${f.file}&token=${encodeURIComponent(s.token)}") format("truetype");font-display:block}`).join("");
    const el = document.createElement("style");
    el.textContent = css;
    document.head.appendChild(el);
    fontsAdded = true;
  }

  // ---------- opening a video ----------
  async function openVideo(file) {
    msg("");
    if (!VIDEO_EXT.test(file.name || file.path)) return msg("Captions need a video. For the words of a song or recording, use Transcribe in the downloads list.", true);
    stopJobWatch();
    st.video = { path: file.path, name: file.name || file.path.split(/[\\/]/).pop(), size: file.size || 0 };
    st.cues = []; st.orig = null; st.chunk = ""; st.lang = ""; st.sel = -1; st.dub = null; undo = []; redo = [];
    st.frame = { ar: "orig", fit: "crop", cx: 50 }; closeWordMenu();
    $("capResult").hidden = true;
    const saved = loadJSON(projectKey());
    if (saved?.cues?.length) {
      Object.assign(st, { lang: saved.lang, cues: saved.cues, orig: saved.orig, chunk: saved.chunk || "", dub: saved.dub || null });
      if (saved.style) st.style = normalize(saved.style);
      if (saved.frame?.ar) st.frame = { ...st.frame, ...saved.frame };
      msg("Your edits from last time were brought back.");
    }
    addFonts().then(() => document.fonts.ready).then(() => { lastKey = ""; tick(true); }).catch(() => {});
    drawAll();
    const v = $("capVideo");
    v.src = cv().fileURL(file.path);
    if (!st.cues.length) findExisting();
  }
  async function findExisting() {
    try {
      const r = await cv().op("captionsFind", { path: st.video.path });
      st.found = r.ok ? r.found : [];
    } catch { st.found = []; }
    drawMake();
  }
  async function loadSrt(path, lang) {
    try {
      const r = await cv().op("captionsLoad", { path, video: st.video?.path });
      if (!r.ok) return msg(r.error, true);
      snapshot();
      st.cues = r.cues.map((c) => ({ s: c.s, e: c.e, text: c.text.replace(/\s+/g, " ").trim(), words: c.words }));
      st.orig = JSON.parse(JSON.stringify(st.cues));
      st.chunk = "";
      if (lang) st.lang = lang;
      msg(r.words ? "" : "These captions have no word timings, so words are timed by their length. Make captions here for exact timing.");
      drawAll();
      persist();
      syncToSpeech(true); // captions made elsewhere (or by an older AnySave) often start too early
    } catch (e) { msg(e.message, true); }
  }
  async function pick() {
    try {
      const r = await cv().op("pick", { multi: false });
      if (!r.ok) return msg(r.error, true);
      const f = r.files?.[0];
      if (!f) return;
      if (SUBS_EXT.test(f.name)) return st.video ? loadSrt(f.path) : msg("Open the video first, then add its subtitle file.", true);
      openVideo(f);
    } catch (e) { msg(e.message, true); }
  }
  async function dropped(list) {
    const files = [...list];
    const vid = files.find((f) => VIDEO_EXT.test(f.name)), sub = files.find((f) => SUBS_EXT.test(f.name));
    if (!vid && !sub) return msg("Drop a video (MP4, MOV, MKV, WEBM…) or a subtitle file (SRT, VTT, ASS).", true);
    try {
      if (vid) {
        msg(`Adding ${vid.name}…`);
        const r = await cv().upload(vid);
        if (!r.ok) return msg(r.error, true);
        await openVideo(r.file);
      }
      if (sub) {
        if (!st.video) return msg("Open the video first, then add its subtitle file.", true);
        const r = await cv().upload(sub);
        if (!r.ok) return msg(r.error, true);
        await loadSrt(r.file.path);
      }
    } catch (e) { msg(e.message, true); }
  }
  window.openCaptions = async (path) => { // "Edit captions" on a finished download
    showTab("captions");
    try {
      const r = await cv().op("allow", { path });
      if (r.ok) openVideo(r.file); else msg(r.error, true);
    } catch (e) { msg(e.message, true); }
  };
  window.fillCaptionsPicker = () => {
    const sel = $("capFromDl");
    const done = Object.values(jobs).filter((j) => j.status === "done" && j.file && VIDEO_EXT.test(j.file))
      .sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0)).slice(0, 200);
    sel.innerHTML = '<option value="">…or one of your downloaded videos</option>' + done.map((j) => `<option value="${h(j.id)}">${h(displayName(j))}</option>`).join("");
  };

  // ---------- making captions (the speech engine) ----------
  function drawMake() {
    const box = $("capMake");
    box.hidden = !!st.cues.length || !st.video;
    if (box.hidden) return;
    const last = currentSettings.transcribeLang || "en";
    const running = st.job && jobs[st.job];
    box.innerHTML = `<h3>Make captions</h3>
      ${(st.found || []).length ? `<div class="cap-found"><span class="muted small">Captions already made for this video:</span>
        ${st.found.map((f, i) => `<button class="btn small" data-found="${i}">Open ${h(f.path.split(/[\\/]/).pop())}</button>`).join("")}</div>` : ""}
      <div class="row">
        <label class="small">Spoken language
          <select id="capLang">${TX_LANGS.map(([c, n]) => `<option value="${c}"${c === last ? " selected" : ""}>${h(n)}</option>`).join("")}</select></label>
        <button id="capMakeGo" class="btn primary"${running ? " disabled" : ""}>Make captions</button>
      </div>
      <div class="cv-progress" id="capMakeProg" hidden><div class="cv-progress-bar"><i></i></div><span></span></div>
      <p class="hint">Runs on this computer; nothing is uploaded. The first time, a speech model (252–547 MB) is downloaded once.
        You can also drop a subtitle file (SRT, VTT, ASS) here to style it.</p>`;
    box.querySelectorAll("[data-found]").forEach((b) => { b.onclick = () => { const f = st.found[+b.dataset.found]; loadSrt(f.path, f.lang); }; });
    $("capMakeGo").onclick = makeCaptions;
    if (running) watchJob();
  }
  async function makeCaptions() {
    const lang = $("capLang").value;
    msg("");
    try {
      const r = await send({ type: "transcribe", path: st.video.path, name: st.video.name, lang });
      if (!r?.ok) throw new Error(r?.error || "Couldn't start the speech engine.");
      st.job = r.id; st.lang = lang;
      $("capMakeGo").disabled = true;
      watchJob();
    } catch (e) { msg(e.message, true); }
  }
  let jobTimer = null;
  function stopJobWatch() { clearInterval(jobTimer); jobTimer = null; st.job = null; }
  function watchJob() {
    clearInterval(jobTimer);
    jobTimer = setInterval(() => {
      const j = jobs[st.job], p = $("capMakeProg");
      if (!j || !p) return;
      p.hidden = false;
      if (j.status === "done") {
        clearInterval(jobTimer); st.job = null;
        loadSrt(j.srt || j.file, j.lang || st.lang);
      } else if (j.status === "error" || j.status === "cancelled") {
        clearInterval(jobTimer); st.job = null; p.hidden = true;
        $("capMakeGo").disabled = false;
        msg(j.status === "cancelled" ? "Stopped." : (j.error || "The speech engine stopped."), j.status === "error");
      } else {
        p.querySelector("i").style.width = Math.max(2, j.percent || 0) + "%";
        p.querySelector("span").textContent = `${j.speed || "Working"} · ${Math.round(j.percent || 0)}%${j.eta && j.eta !== "?" ? ` · ${j.eta} left` : ""}`;
      }
    }, 500);
  }

  // ---------- drawing ----------
  function drawAll() {
    const has = !!st.video;
    $("capMain").hidden = !has;
    $("capDrop").classList.toggle("small", has);
    if (!has) return;
    $("capName").textContent = st.video.name;
    drawMake();
    $("capEditor").hidden = !st.cues.length;
    $("capExport").hidden = !st.cues.length;
    drawDub();
    drawStyle();
    drawList();
    drawTimeline();
    lastKey = "";
    tick(true);
  }

  // The caption on the video, the same way FFmpeg will draw it.
  // glow: FFmpeg's \\blur (px), drawn here as a CSS blur (radius ≈ 2σ).
  function textShadow(r, color, shadow, glow = 0) {
    const out = [];
    const b = glow > 0 ? (glow * 2).toFixed(1) + "px" : "0";
    if (r > 0) for (let i = 0; i < 16; i++) { const a = i * Math.PI / 8; out.push(`${(Math.cos(a) * r).toFixed(2)}px ${(Math.sin(a) * r).toFixed(2)}px ${b} ${color}`); }
    if (glow > 0) out.push(`0 0 ${b} ${color}`, `0 0 ${b} ${color}`);
    if (shadow) out.push(`${Math.max(1, r * 0.6 + 2).toFixed(1)}px ${Math.max(1, r * 0.6 + 2).toFixed(1)}px 2px rgba(0,0,0,.55)`);
    return out.join(",") || "none";
  }
  const upper = (t) => (st.style.upper ? t.toLocaleUpperCase(st.lang && st.lang !== "auto" ? st.lang : undefined) : t);
  function rgba(hex, op) { const n = parseInt(hex.slice(1), 16); return `rgba(${n >> 16},${(n >> 8) & 255},${n & 255},${op})`; }
  // ---------- how each word looks ----------
  // Key words (when on): numbers, words in CAPITALS, names (a capital letter inside a sentence) and the
  // user's own list. A word's own colour or size (clicked on the video) wins over everything.
  const bare = (t) => String(t).replace(/^[^\p{L}\p{N}$€£৳₹]+|[^\p{L}\p{N}%]+$/gu, "");
  function keyFlags(ws) {
    const s = st.style;
    if (!s.auto) return [];
    const extra = new Set(String(s.autoWords || "").toLowerCase().split(/[,;\n]/).map((x) => x.trim()).filter(Boolean));
    return ws.map((w, k) => {
      const b = bare(w.t);
      if (!b) return false;
      if (extra.has(b.toLowerCase())) return true;
      if (/[\d০-৯]/.test(b)) return true;
      if (b.length > 1 && /^\p{Lu}+$/u.test(b)) return true;
      return k > 0 && /^\p{Lu}/u.test(b) && !/[.?!:।]$/.test(ws[k - 1].t) && b !== "I" && !/^I['’]/.test(b);
    });
  }
  const spkColor = (c) => (c && c.sp >= 0 && c.sp < 4 ? st.style.spk[c.sp] : null);
  function wordLook(c, w, key) {
    const s = st.style;
    return { color: w.c || (key ? s.autoColor : null) || spkColor(c) || s.color, z: w.z || (key && s.autoBig ? 1.2 : 1) };
  }

  // Where the caption sits: the line's own place (moved on its own) or the shared one.
  let shownI = -1;
  const posOf = (i) => (i >= 0 && st.cues[i]?.pos) || st.style;

  // x: the middle of the text; y: its top, middle or bottom (anchor); width: where lines wrap. All % of the video.
  function layoutOverlay() {
    const stage = $("capStage"), ov = $("capOverlay"), s = st.style, F = FONTS[s.font] || FONTS.noto, P = posOf(shownI);
    const scale = stage.clientHeight / st.h;
    const px = s.size / 100 * st.h * scale;
    ov.style.fontFamily = `"AS ${s.font}", "AS ${F.bn}", sans-serif`;
    ov.style.fontSize = px + "px";
    ov.style.lineHeight = String(F.ratio);
    ov.style.letterSpacing = (s.spacing || 0) / 100 + "em";
    ov.style.color = s.color;
    ov.style.left = P.x + "%";
    ov.style.top = P.y + "%";
    ov.style.maxWidth = P.width + "%";
    ov.dataset.anchor = P.anchor;
    ov.style.setProperty("--hl", s.hl);
    ov.classList.toggle("boxed", !!s.box);
    ov.style.setProperty("--box", rgba(s.boxColor || "#000000", (s.boxOpacity ?? 70) / 100));
    ov.style.setProperty("--pad", (px * 0.22).toFixed(1) + "px");
    ov.style.setProperty("--slide", (0.03 * stage.clientHeight).toFixed(1) + "px");
    ov.style.textShadow = s.box ? "none" : textShadow(s.outline / 100 * px, s.outlineColor, s.shadow, (s.glow || 0) / 100 * px);
    const phone = $("capGuides");
    phone.classList.toggle("vertical", st.h > st.w);
    phone.classList.toggle("showphone", st.h > st.w && !!s.phoneZone);
    syncFrame();
  }
  let lastKey = "", lastCue = -1;
  function cueAt(t) {
    let lo = 0, hi = st.cues.length - 1;
    while (lo <= hi) { const m = (lo + hi) >> 1; if (st.cues[m].e <= t) lo = m + 1; else if (st.cues[m].s > t) hi = m - 1; else return m; }
    return -1;
  }
  function tick(force) {
    const v = $("capVideo"), t = v.currentTime || 0;
    const i = cueAt(t);
    let wi = -1;
    if (i >= 0 && st.style.mode !== "none") {
      const ws = words(st.cues[i]);
      for (let k = 0; k < ws.length; k++) if (ws[k].s <= t) wi = k;
      if (wi < 0 && st.style.mode !== "reveal") wi = 0;
    }
    // While paused (or moving the caption) with nothing on screen, show a faint caption so it can be placed.
    const ghost = i < 0 && st.cues.length && (v.paused || drag) ? Math.max(0, Math.min(st.cues.length - 1, st.cues.findIndex((c) => c.s > t))) : -1;
    const key = `${i}|${wi}|${st.style.mode}|${ghost}`;
    if (force || key !== lastKey) {
      const newLine = i >= 0 && i !== lastCue && !v.paused && st.style.anim && st.style.anim !== "none";
      lastKey = key; lastCue = i; shownI = i >= 0 ? i : ghost;
      const ov = $("capOverlay");
      ov.classList.toggle("ghost", ghost >= 0);
      ov.classList.toggle("empty", i < 0 && ghost < 0);
      if (i < 0 && ghost < 0) ov.innerHTML = "";
      else if (i < 0) ov.innerHTML = `<span class="cap-line">${h(upper(st.cues[ghost].text))}</span>`;
      else {
        const c = st.cues[i], ws = words(c), m = st.style.mode, keys = keyFlags(ws);
        ov.innerHTML = `<span class="cap-line${newLine ? " in-" + st.style.anim : ""}">${ws.map((w, k) => {
          const on = (m === "word" || m === "pop") && k === wi;
          const hidden = m === "reveal" && k > wi;
          const L = wordLook(c, w, keys[k]), z = L.z * (on && m === "pop" ? 1.15 : 1);
          const css = `color:${on ? st.style.hl : L.color};${z !== 1 ? `font-size:${z}em;` : ""}`;
          return `<span class="w${hidden ? " hid" : ""}${m === "sweep" ? " sw" : ""}" data-k="${k}"${m === "sweep" ? ` data-t="${h(upper(w.t))}"` : ""} style="${css}">${h(upper(w.t))}</span>`;
        }).join(" ")}</span>`;
      }
      layoutOverlay();
      document.querySelectorAll("#capList .cap-cue.now").forEach((r) => r.classList.remove("now"));
      if (i >= 0) {
        const row = document.querySelector(`#capList .cap-cue[data-i="${i}"]`);
        if (row) {
          row.classList.add("now");
          if (st.follow && !v.paused && !row.contains(document.activeElement)) row.scrollIntoView({ block: "nearest" });
        }
      }
    }
    if (i >= 0 && st.style.mode === "sweep") { // the colour runs through each word as it is said
      const ws = words(st.cues[i]);
      for (const el of $("capOverlay").querySelectorAll(".w.sw")) {
        const w = ws[+el.dataset.k];
        if (w) el.style.setProperty("--p", Math.max(0, Math.min(100, (t - w.s) / Math.max(0.01, w.e - w.s) * 100)).toFixed(1) + "%");
      }
    }
    $("capTime").textContent = `${clock(t, false)} / ${clock(st.dur || v.duration || 0, false)}`;
    const ph = $("capPlayhead");
    if (ph && st.dur) ph.style.left = (t / st.dur * 100) + "%";
  }
  function loop() { tick(); requestAnimationFrame(loop); }

  // The line list.
  function drawList() {
    const list = $("capList");
    $("capCount").textContent = st.cues.length ? `${st.cues.length} lines` : "";
    list.innerHTML = st.cues.map((c, i) => `<div class="cap-cue${i === st.sel ? " sel" : ""}${c.sp >= 0 ? " has-spk" : ""}" data-i="${i}"${c.sp >= 0 ? ` style="--spk:${st.style.spk[c.sp]}"` : ""}>
      <div class="cap-cue-t">
        <button class="cap-play" data-a="play" title="Play from here" aria-label="Play from here">▶</button>
        <input class="cap-t" data-k="s" value="${clock(c.s)}" aria-label="Start time" />
        <span class="muted">→</span>
        <input class="cap-t" data-k="e" value="${clock(c.e)}" aria-label="End time" />
        ${c.sp >= 0 ? `<span class="cap-badge" title="Speaker ${c.sp + 1}">Speaker ${c.sp + 1}</span>` : ""}${c.pos ? `<span class="cap-badge" title="This line has its own place on the video">own place</span>` : ""}
        <span class="cap-acts">
          <button data-a="merge" title="Join with the next line"${i + 1 >= st.cues.length ? " disabled" : ""}>Join ↓</button>
          <button data-a="add" title="Add a new line after this one">+ Line</button>
          <button data-a="spk" class="cap-spk" title="Who is speaking: gives the line that speaker's colour (click again for the next speaker)">${c.sp >= 0 ? `<i style="background:${st.style.spk[c.sp]}"></i>Speaker ${c.sp + 1}` : "Speaker"}</button>
          <button data-a="own" class="${c.pos ? "on" : ""}" title="${c.pos ? "This line has its own place on the video. Click to put it back with the others." : "Give this line its own place: then dragging it on the video moves only this line"}">${c.pos ? "Own place ✓" : "Own place"}</button>
          <button data-a="del" title="Delete this line">Delete</button>
        </span>
      </div>
      <textarea class="cap-text" rows="1" spellcheck="true" aria-label="Caption text">${h(c.text)}</textarea>
    </div>`).join("");
    list.querySelectorAll(".cap-text").forEach(autosize);
  }
  function autosize(t) { t.style.height = "auto"; t.style.height = t.scrollHeight + "px"; }

  function drawTimeline() {
    const tl = $("capTimeline");
    const d = st.dur || 0;
    if (!d) { tl.innerHTML = ""; return; }
    tl.innerHTML = st.cues.map((c, i) => `<i data-i="${i}" style="left:${c.s / d * 100}%;width:${Math.max(0.15, (c.e - c.s) / d * 100)}%"></i>`).join("") +
      `<b id="capPlayhead"></b>`;
  }

  // The style panel.
  const mine = () => (loadJSON("anysave.cap.mine") || []).filter((p) => p && p.id && p.s);
  // A small sample of a style, for its button.
  function sample(ps) {
    const p = normalize({ ...BASE, ...ps }), F = FONTS[p.font] || FONTS.noto, px = 17;
    const hl = p.mode === "word" || p.mode === "pop";
    const look = `font-family:'AS ${p.font}','AS ${F.bn}',sans-serif;font-size:${px}px;line-height:${F.ratio};color:${p.color};letter-spacing:${(p.spacing || 0) / 100}em;` +
      (p.box ? `background:${rgba(p.boxColor, p.boxOpacity / 100)};padding:1px 5px;` : `text-shadow:${textShadow(p.outline / 100 * px, p.outlineColor, p.shadow, (p.glow || 0) / 100 * px)};`) +
      (p.upper ? "text-transform:uppercase;" : "");
    return `<span class="pv"><span style="${h(look)}">Say <span style="${hl ? `color:${h(p.hl)}` : ""}">hello</span></span></span>`;
  }
  let naming = false;
  function drawStyle() {
    const s = st.style;
    const fontOpts = Object.entries(FONTS).filter(([, f]) => !f.hidden).map(([k, f]) => `<option value="${k}"${k === s.font ? " selected" : ""}>${h(f.label)}</option>`).join("");
    const seg = (key, opts, cur = s[key]) => `<span class="chips seg">${opts.map(([v, l]) => `<button class="chip${String(cur) === v ? " active" : ""}" data-set="${key}" data-v="${v}">${h(l)}</button>`).join("")}</span>`;
    const tile = (p, del) => `<button class="cap-preset${s.preset === p.id ? " on" : ""}" data-preset="${h(p.id)}" title="${h(p.desc || p.title)}">
        ${sample(p.s)}<b>${h(p.title)}</b>${del ? `<span class="x" data-del="${h(p.id)}" title="Delete this style" role="button" aria-label="Delete ${h(p.title)}">×</span>` : ""}</button>`;
    const my = mine();
    $("capStyle").innerHTML = `
      <div class="cap-presets">${PRESETS.map((p) => tile(p)).join("")}</div>
      ${my.length ? `<div class="cap-sub">Your styles</div><div class="cap-presets">${my.map((p) => tile(p, true)).join("")}</div>` : ""}
      <div class="cap-mine">${naming
        ? `<input id="capMineName" class="cv-input" maxlength="30" placeholder="Name this style" /> <button class="btn small primary" data-mine="save">Save</button> <button class="btn small" data-mine="cancel">Cancel</button>`
        : `<button class="btn small" data-mine="new" title="Keep this look (font, colours, place on the video) to use again">+ Save as my style</button>`}</div>
      <p class="muted small cap-tip">Drag the caption on the video to move it. Drag a corner to make it bigger or smaller, a side to change where lines wrap. Click a word to give it its own colour or size. Lines snap to the middle and safe edges; hold Alt (Option) to place freely. Arrow keys nudge it.</p>
      <div class="cap-grid">
        <label>Font <select data-k="font">${fontOpts}</select></label>
        <label>Size <input type="range" min="2.5" max="14" step="0.1" data-k="size" value="${s.size}" /></label>
        <label>Quick place ${seg("where", [["top", "Top"], ["middle", "Middle"], ["bottom", "Bottom"]], s.anchor)}</label>
        <label>Line width <input type="range" min="15" max="100" step="1" data-k="width" value="${s.width}" /></label>
        <label>Text colour <input type="color" data-k="color" value="${s.color}" /></label>
        <label class="chk"><input type="checkbox" data-k="upper"${s.upper ? " checked" : ""} /> CAPITAL LETTERS</label>
        <label>Letter spacing <input type="range" min="-5" max="40" step="1" data-k="spacing" value="${s.spacing || 0}" /></label>
        <label>Background ${seg("box", [["false", "Outline"], ["true", "Box"]])}</label>
        ${s.box ? `<label>Box colour <input type="color" data-k="boxColor" value="${s.boxColor}" /></label>
          <label>Box see-through <input type="range" min="20" max="100" step="5" data-k="boxOpacity" value="${s.boxOpacity}" /></label>`
        : `<label>Outline <input type="range" min="0" max="25" step="0.5" data-k="outline" value="${s.outline}" /></label>
          <label>Outline colour <input type="color" data-k="outlineColor" value="${s.outlineColor}" /></label>
          <label>Glow <input type="range" min="0" max="30" step="1" data-k="glow" value="${s.glow || 0}" /></label>
          <label class="chk"><input type="checkbox" data-k="shadow"${s.shadow ? " checked" : ""} /> Soft shadow</label>`}
        <label class="wide">Words ${seg("mode", [["none", "Still"], ["word", "Light up"], ["pop", "Pop"], ["sweep", "Colour sweep"], ["reveal", "Appear"]])}</label>
        ${["word", "pop", "sweep"].includes(s.mode) ? `<label>${s.mode === "sweep" ? "Sweep colour" : "Highlight colour"} <input type="color" data-k="hl" value="${s.hl}" /></label>` : ""}
        <label class="chk wide"><input type="checkbox" data-k="auto"${s.auto ? " checked" : ""} /> Colour the key words (numbers, names, CAPITALS)</label>
        ${s.auto ? `<label>Key word colour <input type="color" data-k="autoColor" value="${s.autoColor}" /></label>
          <label class="chk"><input type="checkbox" data-k="autoBig"${s.autoBig ? " checked" : ""} /> Make them bigger</label>
          <label class="wide">Also these words <input type="text" class="cv-input" data-k="autoWords" value="${h(s.autoWords || "")}" placeholder="free, sale, today" /></label>` : ""}
        <label class="wide">Speaker colours <span class="cap-spks">${s.spk.map((x, i) => `<span>${i + 1} <input type="color" data-k="spk${i}" value="${x}" title="Speaker ${i + 1}" /></span>`).join("")}</span>
          <span class="muted small">Pick the speaker on each line (the Speaker button in the list).</span></label>
        <label class="wide">Each line ${seg("anim", [["none", "Just shows"], ["fade", "Fades in"], ["pop", "Pops in"], ["slide", "Slides up"]])}</label>
        ${st.h > st.w ? `<label class="chk wide"><input type="checkbox" data-k="phoneZone"${s.phoneZone ? " checked" : ""} /> Show where short-video apps put their buttons (keep captions out of the shaded parts)</label>` : ""}
      </div>`;
    if (naming) setTimeout(() => $("capMineName")?.focus(), 0);
    layoutOverlay();
  }
  function setStyle(k, v, redraw) {
    const s = st.style;
    snapTyping(); // one undo step per burst of changes
    if (k === "where") {
      Object.assign(s, posFrom(v, v === "top" ? 8 : 8), { placed: true });
      drawStyle(); lastKey = ""; tick(true); persist();
      return;
    }
    if (["size", "offset", "outline", "boxOpacity", "spacing", "glow", "width"].includes(k)) v = +v;
    if (v === "true") v = true; else if (v === "false") v = false;
    if (/^spk\d$/.test(k)) { s.spk = [...s.spk]; s.spk[+k[3]] = v; drawList(); }
    else s[k] = v;
    if (!["phoneZone", "width", "auto", "autoColor", "autoBig", "autoWords"].includes(k) && !k.startsWith("spk")) s.preset = "";
    if (redraw) drawStyle(); else layoutOverlay();
    lastKey = ""; tick(true);
    persist();
  }
  function saveMine() {
    const title = ($("capMineName")?.value || "").trim() || "My style";
    const { preset, placed, ...look } = st.style;
    const list = mine();
    const p = { id: "m" + Date.now().toString(36), title, mine: true, s: look };
    list.push(p);
    saveJSON("anysave.cap.mine", list.slice(-24));
    st.style.preset = p.id;
    naming = false; drawStyle(); persist();
  }

  // ---------- moving and resizing the caption on the video ----------
  let drag = null;
  const stageBox = () => $("capStage").getBoundingClientRect();
  // A wrapped line box is as wide as the space it may use; FFmpeg's box hugs the longest line.
  // So the caption is made exactly as wide as its longest line (measured without the entry animation).
  function fitWidth(ov) {
    ov.style.width = "";
    const line = ov.firstElementChild;
    if (!line) return;
    line.style.animationPlayState = "paused";
    const anim = line.style.animation;
    line.style.animation = "none";
    const r = document.createRange();
    r.selectNodeContents(line);
    let l = Infinity, rt = -Infinity;
    for (const b of r.getClientRects()) if (b.width > 0.5) { l = Math.min(l, b.left); rt = Math.max(rt, b.right); }
    line.style.animation = anim;
    line.style.animationPlayState = "";
    if (rt > l) ov.style.width = Math.ceil(rt - l + (st.style.box ? 2 * parseFloat(ov.style.getPropertyValue("--pad") || 0) : 0) + 1) + "px";
  }
  // The handles sit on the caption's box.
  function syncFrame() {
    const fr = $("capFrame"), ov = $("capOverlay");
    if (!fr || !ov) return;
    fitWidth(ov);
    if (!ov.firstElementChild) { fr.hidden = true; return; }
    fr.hidden = false;
    const r = stageBox(), b = ov.getBoundingClientRect();
    Object.assign(fr.style, { left: b.left - r.left + "px", top: b.top - r.top + "px", width: b.width + "px", height: b.height + "px" });
  }
  // The caption box's middle and size, in % of the video.
  function boxPct() {
    const r = stageBox(), b = $("capOverlay").getBoundingClientRect();
    return { cx: (b.left + b.width / 2 - r.left) / r.width * 100, cy: (b.top + b.height / 2 - r.top) / r.height * 100, bw: b.width / r.width * 100, bh: b.height / r.height * 100, r };
  }
  function padPct() {
    return st.style.box ? st.style.size / 100 * 0.22 * 100 : 0; // the box's padding, % of the video height
  }
  // Put the box's middle at (cx, cy): the anchor follows the half of the screen it is in, so a
  // two-line caption at the bottom grows upwards and one at the top grows downwards.
  function placeAt(cx, cy, bw, bh) {
    const s = posOf(shownI); // the shared place, or this line's own
    cx = bw >= 100 ? 50 : Math.min(100 - bw / 2, Math.max(bw / 2, cx));
    cy = bh >= 100 ? 50 : Math.min(100 - bh / 2, Math.max(bh / 2, cy));
    const anchor = cy > 60 ? "bottom" : cy < 40 ? "top" : "middle", pad = padPct();
    s.x = Math.round(cx * 10) / 10;
    s.anchor = anchor;
    s.y = Math.round((anchor === "bottom" ? cy + bh / 2 - pad : anchor === "top" ? cy - bh / 2 + pad : cy) * 10) / 10;
    s.placed = true;
  }
  // Snap the box to the middle and the safe edges (title-safe 10% top and bottom, 5% at the sides).
  function snap(cx, cy, bw, bh, r) {
    const tx = 8 / r.width * 100, ty = 8 / r.height * 100, hits = [];
    const phone = st.h > st.w && st.style.phoneZone;
    const xs = [[50, cx, "gv"], [5 + bw / 2, cx, "safe"], [95 - bw / 2, cx, "safe"]];
    if (phone) xs.push([86 - bw / 2, cx, "phone"]);
    const ys = [[50, cy, "gh"], [10 + bh / 2, cy, "safe"], [90 - bh / 2, cy, "safe"]];
    if (phone) ys.push([78 - bh / 2, cy, "phone"], [12 + bh / 2, cy, "phone"]);
    const best = (list, th) => list.reduce((a, c) => Math.abs(c[0] - c[1]) < th && (!a || Math.abs(c[0] - c[1]) < Math.abs(a[0] - a[1])) ? c : a, null);
    const bx = best(xs, tx), by = best(ys, ty);
    if (bx) { cx = bx[0]; hits.push(bx[2]); }
    if (by) { cy = by[0]; hits.push(by[2]); }
    return { cx, cy, hits };
  }
  function showGuides(hits) {
    const g = $("capGuides");
    g.classList.toggle("on", !!hits);
    for (const k of ["gv", "gh", "safe", "phone"]) g.querySelector("." + k).classList.toggle("hit", !!hits && hits.includes(k));
  }
  function readout(text) {
    const r = document.querySelector("#capFrame .cap-readout");
    if (r) { r.textContent = text || ""; r.hidden = !text; }
  }
  function startDrag(e) {
    if (e.button !== 0 || !st.video) return;
    const handle = e.target.closest("#capFrame [data-h]");
    if (!handle && !e.target.closest("#capOverlay")) return;
    if (!$("capOverlay").firstElementChild) return;
    e.preventDefault();
    e.currentTarget.setPointerCapture?.(e.pointerId);
    $("capOverlay").focus({ preventScroll: true });
    snapshot();
    const b = boxPct();
    drag = { mode: handle ? (["w", "e"].includes(handle.dataset.h) ? "width" : "scale") : "move", x0: e.clientX, y0: e.clientY, ...b,
      size0: st.style.size, moved: false, word: handle ? null : e.target.closest("#capOverlay .w")?.dataset.k, cue: cueAt($("capVideo").currentTime || 0), ccx: b.r.left + b.cx / 100 * b.r.width, ccy: b.r.top + b.cy / 100 * b.r.height };
    drag.d0 = Math.hypot(e.clientX - drag.ccx, e.clientY - drag.ccy) || 1;
    $("capStage").classList.add("dragging");
  }
  function moveDrag(e) {
    if (!drag) return;
    const s = st.style, r = drag.r;
    if (Math.abs(e.clientX - drag.x0) + Math.abs(e.clientY - drag.y0) > 2) drag.moved = true;
    if (drag.mode === "move") {
      let cx = drag.cx + (e.clientX - drag.x0) / r.width * 100, cy = drag.cy + (e.clientY - drag.y0) / r.height * 100;
      let hits = [];
      if (!e.altKey) ({ cx, cy, hits } = snap(cx, cy, drag.bw, drag.bh, r));
      placeAt(cx, cy, drag.bw, drag.bh);
      showGuides(hits);
      readout(hits.includes("gv") && hits.includes("gh") ? "Centred" : hits.includes("gv") ? "Centred across" : hits.includes("gh") ? "Middle" : hits.length ? "Safe edge" : "");
    } else if (drag.mode === "scale") {
      const d = Math.hypot(e.clientX - drag.ccx, e.clientY - drag.ccy);
      s.size = Math.round(Math.min(14, Math.max(2.5, drag.size0 * d / drag.d0)) * 10) / 10;
      readout("Size " + s.size);
    } else {
      let w = Math.abs(e.clientX - drag.ccx) * 2 / r.width * 100;
      if (!e.altKey && Math.abs(w - 86) < 2) w = 86;
      posOf(shownI).width = Math.round(Math.min(100, Math.max(15, w)));
      readout("Line width " + posOf(shownI).width + "%");
    }
    layoutOverlay();
    lastKey = ""; tick(true);
  }
  let wordTimer = null;
  function endDrag() {
    if (!drag) return;
    const moved = drag.moved, wk = drag.word, wc = drag.cue;
    drag = null;
    // A click (no drag) on a word opens its menu; a double-click edits the line instead.
    if (!moved && wk != null && wc >= 0) { clearTimeout(wordTimer); wordTimer = setTimeout(() => openWordMenu(wc, +wk), 260); }
    $("capStage").classList.remove("dragging");
    showGuides(null); readout("");
    if (!moved) undo.pop(); // just a click: nothing to undo
    else { drawStyle(); persist(); }
    lastKey = ""; tick(true);
  }
  // The line that is showing (or the faint one), for double-click editing.
  function shownCue() {
    const t = $("capVideo").currentTime || 0, i = cueAt(t);
    if (i >= 0) return i;
    return Math.max(0, Math.min(st.cues.length - 1, st.cues.findIndex((c) => c.s > t)));
  }
  function nudge(e) {
    const s = st.style, k = e.key, step = e.shiftKey ? 2.5 : 0.5;
    if (["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(k)) {
      snapTyping();
      const s = posOf(shownI);
      if (k === "ArrowLeft") s.x = Math.max(0, s.x - step);
      if (k === "ArrowRight") s.x = Math.min(100, s.x + step);
      if (k === "ArrowUp") s.y = Math.max(0, s.y - step);
      if (k === "ArrowDown") s.y = Math.min(100, s.y + step);
      s.x = Math.round(s.x * 10) / 10; s.y = Math.round(s.y * 10) / 10; s.placed = true;
    } else if (k === "+" || k === "=" || k === "-") {
      snapTyping();
      s.size = Math.round(Math.min(14, Math.max(2.5, s.size + (k === "-" ? -0.2 : 0.2))) * 10) / 10;
    } else if (k === "Enter") {
      focusRow(shownCue(), e);
      return;
    } else return;
    e.preventDefault();
    layoutOverlay(); lastKey = ""; tick(true); persist();
  }

  // ---------- the shape of the finished video ----------
  // Original, or made vertical (9:16), square or 4:5 for phone apps: cropped (choose which part stays)
  // or fitted with black bars. Captions are placed on the new frame; the helper crops the same way.
  const RATIOS = { "9:16": 9 / 16, "1:1": 1, "4:5": 4 / 5 };
  const even = (x) => Math.max(2, Math.round(x / 2) * 2);
  function frameSize() {
    const A = RATIOS[st.frame.ar], vw = st.vw, vh = st.vh;
    if (!A) return [vw, vh];
    const wide = vw / vh > A;
    if (st.frame.fit === "fit") return wide ? [even(vw), even(vw / A)] : [even(vh * A), even(vh)];
    return wide ? [even(vh * A), even(vh)] : [even(vw), even(vw / A)];
  }
  function applyFrame() {
    [st.w, st.h] = frameSize();
    const stage = $("capStage"), v = $("capVideo"), f = st.frame;
    stage.style.aspectRatio = `${st.w} / ${st.h}`;
    stage.style.width = `min(100%, calc(${st.h > st.w ? 66 : 56}vh * ${st.w / st.h}))`;
    v.style.objectFit = f.ar !== "orig" && f.fit === "crop" ? "cover" : "contain";
    const wide = st.vw / st.vh > (RATIOS[f.ar] || 0);
    v.style.objectPosition = f.ar !== "orig" && f.fit === "crop" ? (wide ? `${f.cx}% 50%` : `50% ${f.cx}%`) : "50% 50%";
    drawShape(); drawStyle(); lastKey = ""; tick(true);
  }
  function drawShape() {
    const f = st.frame, box = $("capShape");
    if (!box) return;
    const wide = st.vw / st.vh > (RATIOS[f.ar] || 0);
    box.innerHTML = `<label>Shape <select id="capAr">${[["orig", "Original"], ["9:16", "Vertical 9:16 (Reels, Shorts, TikTok)"], ["1:1", "Square 1:1"], ["4:5", "Portrait 4:5"]]
      .map(([v, l]) => `<option value="${v}"${f.ar === v ? " selected" : ""}>${l}</option>`).join("")}</select></label>
      ${f.ar !== "orig" ? `<span class="chips seg">${[["crop", "Fill (crop)"], ["fit", "Fit (bars)"]].map(([v, l]) => `<button class="chip${f.fit === v ? " active" : ""}" data-fit="${v}">${l}</button>`).join("")}</span>` : ""}
      ${f.ar !== "orig" && f.fit === "crop" ? `<label>Keep the ${wide ? "left ↔ right" : "top ↕ bottom"} part <input type="range" id="capCx" min="0" max="100" step="1" value="${f.cx}" /></label>` : ""}
      <span class="muted small">${st.w} × ${st.h}</span>`;
  }
  function setFrame(patch) {
    snapTyping();
    st.frame = { ...st.frame, ...patch };
    applyFrame(); persist();
  }

  // ---------- one word's own look (click a word on the video) ----------
  const SWATCH = ["#ffffff", "#ffd400", "#ff3b3b", "#3ce37b", "#5ce1ff", "#ff7ab6", "#a77bff", "#111111"];
  let wm = null; // {i, k, snapped}
  function closeWordMenu() { wm = null; const m = $("capWordMenu"); if (m) m.hidden = true; }
  function openWordMenu(i, k) {
    const c = st.cues[i], w = c && words(c)[k];
    if (!w) return;
    wm = { i, k, snapped: false };
    drawWordMenu();
    const m = $("capWordMenu"), el = document.querySelector(`#capOverlay .w[data-k="${k}"]`);
    const card = m.offsetParent?.getBoundingClientRect(), r = el?.getBoundingClientRect();
    if (card && r) {
      m.style.left = Math.max(4, Math.min(card.width - m.offsetWidth - 4, r.left - card.left + r.width / 2 - m.offsetWidth / 2)) + "px";
      const below = r.bottom - card.top + 8;
      m.style.top = (below + m.offsetHeight < card.height ? below : Math.max(4, r.top - card.top - m.offsetHeight - 8)) + "px";
    }
  }
  function drawWordMenu() {
    const m = $("capWordMenu");
    if (!wm) { m.hidden = true; return; }
    const c = st.cues[wm.i], w = c && words(c)[wm.k];
    if (!w) return closeWordMenu();
    const z = w.z || 1, b = bare(w.t) || w.t;
    m.hidden = false;
    m.innerHTML = `<div class="wm-head"><b>${h(upper(w.t))}</b><button class="wm-x" data-w="close" aria-label="Close">×</button></div>
      <div class="wm-row">${SWATCH.map((x) => `<button class="wm-sw${w.c === x ? " on" : ""}" data-wc="${x}" style="background:${x}" title="${x}"></button>`).join("")}
        <input type="color" data-wc-pick value="${w.c || st.style.color}" title="Any colour" /></div>
      <div class="wm-row"><span class="muted small">Size</span><span class="chips seg">${[[1, "Normal"], [1.3, "Big"], [1.6, "Huge"]].map(([v, l]) => `<button class="chip${z === v ? " active" : ""}" data-wz="${v}">${l}</button>`).join("")}</span></div>
      <label class="wm-all"><input type="checkbox" id="capWordAll" /> Every “${h(b)}” in the captions</label>
      <div class="wm-row"><button class="btn small" data-w="reset">Back to normal</button><button class="btn small primary" data-w="close">Done</button></div>`;
  }
  function setWord(patch) {
    if (!wm) return;
    if (!wm.snapped) { snapshot(); wm.snapped = true; }
    const c = st.cues[wm.i], w = words(c)[wm.k];
    const all = $("capWordAll")?.checked, key = (bare(w.t) || w.t).toLowerCase();
    const targets = all ? st.cues.flatMap((x) => words(x).filter((y) => (bare(y.t) || y.t).toLowerCase() === key)) : [w];
    for (const t of targets) for (const [k, v] of Object.entries(patch)) { if (v == null) delete t[k]; else t[k] = v; }
    const keepAll = all;
    persist(); lastKey = ""; tick(true); drawWordMenu();
    if (keepAll) $("capWordAll").checked = true;
  }

  // ---------- editing ----------
  function rowIndex(el) { const r = el.closest(".cap-cue"); return r ? +r.dataset.i : -1; }
  function seek(t, play) { const v = $("capVideo"); v.currentTime = Math.max(0, t + 0.001); if (play) v.play().catch(() => {}); }
  function splitAt(i, pos) {
    const c = st.cues[i], text = c.text;
    const a = text.slice(0, pos).trim(), b = text.slice(pos).trim();
    if (!a || !b) return;
    snapshot();
    const ws = words(c), na = tokens(a).length;
    const cut = ws[na] ? ws[na].s : c.s + (c.e - c.s) * a.length / text.length;
    const c1 = { s: c.s, e: cut, text: a, words: ws.slice(0, na) }, c2 = { s: cut, e: c.e, text: b, words: ws.slice(na) };
    st.cues.splice(i, 1, c1, c2);
    st.sel = i + 1;
    drawList(); drawTimeline(); persist();
    const t = document.querySelector(`#capList .cap-cue[data-i="${i + 1}"] .cap-text`);
    if (t) { t.focus(); t.setSelectionRange(0, 0); }
  }
  function merge(i) {
    if (i < 0 || i + 1 >= st.cues.length) return;
    snapshot();
    const a = st.cues[i], b = st.cues[i + 1];
    const caret = a.text.length + 1;
    st.cues.splice(i, 2, { s: a.s, e: b.e, text: `${a.text} ${b.text}`.trim(), words: [...words(a), ...words(b)] });
    drawList(); drawTimeline(); persist();
    const t = document.querySelector(`#capList .cap-cue[data-i="${i}"] .cap-text`);
    if (t) { t.focus(); t.setSelectionRange(caret, caret); }
  }
  function onListClick(e) {
    const b = e.target.closest("button[data-a]");
    const i = rowIndex(e.target);
    if (i < 0) return;
    if (!b) { if (!e.target.closest("input,textarea")) { st.sel = i; seek(st.cues[i].s); } return; }
    const c = st.cues[i];
    switch (b.dataset.a) {
      case "play": seek(c.s, true); break;
      case "spk":
        snapshot();
        c.sp = c.sp >= 3 ? undefined : (c.sp >= 0 ? c.sp + 1 : 0);
        drawList(); persist(); lastKey = ""; tick(true); break;
      case "own":
        snapshot();
        if (c.pos) { delete c.pos; msg("The line is back in the shared place."); }
        else {
          const P = st.style;
          c.pos = { x: P.x, y: P.y, anchor: P.anchor, width: P.width };
          msg("This line now has its own place: drag it on the video and only this line moves.");
        }
        seek(c.s + Math.min(0.2, (c.e - c.s) / 2));
        drawList(); persist(); lastKey = ""; tick(true); break;
      case "merge": merge(i); break;
      case "del":
        snapshot(); st.cues.splice(i, 1); drawList(); drawTimeline(); persist(); lastKey = ""; break;
      case "add": {
        snapshot();
        const next = st.cues[i + 1], s = c.e, e2 = Math.min(next ? next.s : s + 2, s + 2);
        st.cues.splice(i + 1, 0, { s, e: Math.max(e2, s + 0.5), text: "New line", words: null });
        drawList(); drawTimeline(); persist();
        const t = document.querySelector(`#capList .cap-cue[data-i="${i + 1}"] .cap-text`);
        if (t) { t.focus(); t.select(); }
        break;
      }
    }
  }
  function onListInput(e) {
    const i = rowIndex(e.target);
    if (i < 0) return;
    const c = st.cues[i];
    if (e.target.classList.contains("cap-text")) {
      snapTyping();
      c.text = e.target.value.replace(/\s+/g, " ").trimStart();
      words(c);
      autosize(e.target);
      lastKey = ""; persist();
    }
  }
  function onListChange(e) {
    const i = rowIndex(e.target);
    if (i < 0 || !e.target.classList.contains("cap-t")) return;
    const c = st.cues[i], v = parseClock(e.target.value);
    if (isNaN(v)) { e.target.value = clock(c[e.target.dataset.k]); return; }
    snapshot();
    let s = c.s, en = c.e;
    if (e.target.dataset.k === "s") s = Math.min(v, en - 0.1); else en = Math.max(v, s + 0.1);
    retime(c, s, en);
    st.cues.sort((a, b) => a.s - b.s);
    drawList(); drawTimeline(); persist(); lastKey = "";
  }
  function onListKey(e) {
    if (!e.target.classList.contains("cap-text")) {
      if (e.target.classList.contains("cap-t") && e.key === "Enter") e.target.blur();
      return;
    }
    const i = rowIndex(e.target), t = e.target;
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); splitAt(i, t.selectionStart); }
    else if (e.key === "Backspace" && t.selectionStart === 0 && t.selectionEnd === 0 && i > 0) { e.preventDefault(); merge(i - 1); }
    else if (e.key === "ArrowDown" && t.selectionStart === t.value.length) focusRow(i + 1, e);
    else if (e.key === "ArrowUp" && t.selectionStart === 0) focusRow(i - 1, e);
  }
  function focusRow(i, e) {
    const t = document.querySelector(`#capList .cap-cue[data-i="${i}"] .cap-text`);
    if (!t) return;
    e.preventDefault(); t.focus(); seek(st.cues[i].s);
  }

  // Tools above the list.
  function rechunk(n) {
    snapshot();
    if (!n) { if (st.orig) st.cues = JSON.parse(JSON.stringify(st.orig)); }
    else { if (!st.chunk) st.orig = JSON.parse(JSON.stringify(st.cues)); st.cues = chunk(+n); }
    st.chunk = n;
    drawList(); drawTimeline(); persist(); lastKey = "";
  }
  function replaceAll() {
    const f = $("capFind").value, r = $("capReplace").value;
    if (!f) return;
    let n = 0;
    const re = new RegExp(f.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "gi");
    snapshot();
    for (const c of st.cues) {
      const t = c.text.replace(re, () => { n++; return r; }).replace(/\s+/g, " ").trim();
      if (t !== c.text) { c.text = t; words(c); }
    }
    st.cues = st.cues.filter((c) => c.text);
    drawList(); drawTimeline(); persist(); lastKey = "";
    msg(n ? `Replaced ${n} time${n === 1 ? "" : "s"}.` : `"${f}" wasn't found.`);
  }
  function shiftAll() {
    const d = parseFloat(String($("capShift").value).replace(",", "."));
    if (!d) return;
    snapshot();
    for (const c of st.cues) retime(c, Math.max(0, c.s + d), Math.max(0.1, c.e + d));
    st.cues = st.cues.filter((c) => c.e > c.s);
    drawList(); drawTimeline(); persist(); lastKey = "";
    msg(`All captions moved ${d > 0 ? "later" : "earlier"} by ${Math.abs(d)} s.`);
  }

  // Sync to speech: the helper finds where the voice is in the sound (cv_sync.go); a line or word
  // that starts in a quiet stretch is moved to just before the speech that follows (same rules as
  // when captions are made). A line also leaves half a second after its last sound, not after a long pause.
  let speech = null;
  async function speechOf(video) {
    if (speech?.video === video) return speech.spans;
    const r = await cv().op("captionsSync", { path: video });
    if (!r.ok) throw new Error(r.error);
    speech = { video, spans: r.speech || [] };
    return speech.spans;
  }
  function speechAfter(sp, t, limit) {
    for (const [a, b] of sp) {
      if (b <= t) continue;
      if (a <= t) return t;
      return a < limit ? a : -1;
    }
    return -1;
  }
  const syncStart = (sp, s, e) => { const on = speechAfter(sp, s, e - 0.04); return on <= s ? s : Math.max(s, on - 0.05); };
  function syncEnd(sp, s, e) {
    let last = -1;
    for (const [a, b] of sp) { if (a >= e) break; if (b > s) last = Math.min(b, e); }
    return last > s && e - last > 0.7 ? last + 0.5 : e;
  }
  async function syncToSpeech(auto) {
    if (!st.video || !st.cues.length) return;
    const btn = $("capSyncGo"), video = st.video.path;
    btn.disabled = true;
    if (!auto) msg("Listening for the speech…");
    try {
      const sp = await speechOf(video);
      if (st.video?.path !== video) return;
      let moved = 0;
      const next = st.cues.map((c) => {
        const ws = words(c).map((w) => ({ ...w }));
        const s = syncStart(sp, c.s, c.e), e = syncEnd(sp, s, c.e);
        if (s > c.s + 0.15 || e < c.e - 0.3) moved++;
        if (ws.some((w) => w.e <= s + 0.02)) {
          // Words timed by length (no real timings) spread over the old, too-early line: spread them over the new one.
          const od = c.e - c.s || 1;
          for (const w of ws) { w.s = s + (w.s - c.s) / od * (e - s); w.e = s + (w.e - c.s) / od * (e - s); }
        } else {
          for (const w of ws) { w.s = Math.min(Math.max(syncStart(sp, w.s, w.e), s), w.e - 0.02); w.e = Math.min(w.e, e); }
        }
        return { ...c, s, e, words: ws };
      });
      if (!moved) { if (!auto) msg("The captions already start when the speaking starts."); return; }
      snapshot();
      st.cues = next;
      drawList(); drawTimeline(); persist(); lastKey = ""; tick(true);
      msg(`${moved} ${moved === 1 ? "line now starts" : "lines now start"} when the speaking starts${auto ? " (fixed automatically; Undo puts the old times back)" : ""}.`);
    } catch (e) { if (!auto) msg(e.message, true); }
    finally { btn.disabled = false; }
  }

  // ---------- saving ----------
  function srtTime(t, sep = ",") {
    const ms = Math.round(Math.max(0, t) * 1000);
    const hh = Math.floor(ms / 3600000), mm = Math.floor(ms / 60000) % 60, ss = Math.floor(ms / 1000) % 60;
    return `${String(hh).padStart(2, "0")}:${String(mm).padStart(2, "0")}:${String(ss).padStart(2, "0")}${sep}${String(ms % 1000).padStart(3, "0")}`;
  }
  const toSrt = () => st.cues.map((c, i) => `${i + 1}\n${srtTime(c.s)} --> ${srtTime(c.e)}\n${c.text}\n`).join("\n");
  const toVtt = () => "WEBVTT\n\n" + st.cues.map((c) => `${srtTime(c.s, ".")} --> ${srtTime(c.e, ".")}\n${c.text}\n`).join("\n");
  const toTxt = () => st.cues.map((c) => c.text).join(" ").replace(/\s+([.,!?।])/g, "$1") + "\n";

  // ASS: what the preview shows, for FFmpeg (or any player with these fonts).
  function assColor(hex, alpha = 0) {
    const n = (hex || "#ffffff").replace("#", "");
    return `&H${alpha.toString(16).padStart(2, "0")}${n.slice(4, 6)}${n.slice(2, 4)}${n.slice(0, 2)}`.toUpperCase();
  }
  function assTime(t) {
    const cs = Math.round(Math.max(0, t) * 100);
    return `${Math.floor(cs / 360000)}:${String(Math.floor(cs / 6000) % 60).padStart(2, "0")}:${String(Math.floor(cs / 100) % 60).padStart(2, "0")}.${String(cs % 100).padStart(2, "0")}`;
  }
  const assText = (t) => t.replace(/\\/g, "∖").replace(/\{/g, "(").replace(/\}/g, ")");
  function toAss() {
    const s = st.style, W = st.w, H = st.h, F = FONTS[s.font] || FONTS.noto, BN = FONTS[F.bn];
    const px = s.size / 100 * H;
    const fs = (f) => Math.round(px * f.ratio * 10) / 10;
    const bord = s.box ? Math.round(px * 0.22 * 10) / 10 : Math.round(s.outline / 100 * px * 10) / 10;
    const shad = !s.box && s.shadow ? Math.round((bord * 0.6 + 2) * 10) / 10 : 0;
    // Placed with \\pos: x is the middle of the text, y its top, middle or bottom (as the preview).
    // Lines wrap inside the margins, so they set the width.
    const place = (P) => ({ an: P.anchor === "top" ? 8 : P.anchor === "middle" ? 5 : 2, ml: Math.round(W * (100 - P.width) / 200),
      X: Math.round(P.x / 100 * W), Y: Math.round(P.y / 100 * H) });
    const { an, ml } = place(s);
    const sp = Math.round((s.spacing || 0) / 100 * px * 10) / 10;
    const blur = !s.box && s.glow > 0 ? Math.round(s.glow / 100 * px * 10) / 10 : 0;
    // How each line comes in (only the first moment of a line, not every word change).
    const dy = Math.round(0.03 * H);
    const enter = (first, { an, X, Y }) => {
      const a = first ? s.anim : "none";
      const at = a === "slide" ? `\\move(${X},${Y + dy},${X},${Y},0,160)\\fad(100,0)` : `\\pos(${X},${Y})`;
      return `{\\an${an}${at}${a === "fade" ? "\\fad(120,0)" : ""}${a === "pop" ? "\\fscx70\\fscy70\\t(0,140,\\fscx100\\fscy100)" : ""}${blur ? `\\blur${blur}` : ""}}`;
    };
    // Box: libass's BorderStyle 4 draws one box around the whole caption in BackColour, padded by Outline.
    const outlineC = s.box ? "&HFF000000" : assColor(s.outlineColor);
    const backC = s.box ? assColor(s.boxColor, Math.round((1 - (s.boxOpacity ?? 70) / 100) * 255)) : "&H73000000";
    const head = `[Script Info]
; Made by AnySave
ScriptType: v4.00+
PlayResX: ${W}
PlayResY: ${H}
WrapStyle: 0
ScaledBorderAndShadow: yes
YCbCr Matrix: None

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,${F.name},${fs(F)},${assColor(s.color)},${assColor(s.color)},${outlineC},${backC},${F.bold ? -1 : 0},0,0,0,100,100,${sp},0,${s.box ? 4 : 1},${bord},${shad},${an},${ml},${ml},0,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`;
    const color = assColor(s.color), hl = assColor(s.hl);
    // One word: its font (Bangla letters use the Bangla font), size and colours. In "Colour sweep" the
    // fill colour (\\c) runs over the word's own colour (\\2c) while it is said (\\kf).
    const word = (c, w, key, on, hidden, kara) => {
      const L = wordLook(c, w, key), z = L.z * (on && s.mode === "pop" ? 1.15 : 1);
      const base = assColor(L.color);
      const parts = upper(w.t).split(BN_RUN).filter(Boolean);
      return parts.map((p, n) => {
        const f = BN_RUN.test(p) ? BN : F;
        // Only the letters' and outline's see-through (\\1a, \\3a): \\alpha would also change the box.
        const a = hidden ? "FF" : "00";
        const col = s.mode === "sweep" ? `\\c${hl}\\2c${base}` : `\\c${on ? hl : base}`;
        const tag = `${n === 0 && kara ? kara : ""}\\fn${f.name}\\fs${Math.round(fs(f) * z * 10) / 10}\\b${f.bold}${col}\\1a&H${a}&${s.box ? "" : `\\3a&H${a}&\\4a&H${hidden ? "FF" : "73"}&`}`; // in a box the outline stays see-through
        return `{${tag}}${assText(p)}`;
      }).join("");
    };
    const lines = [];
    let first = true;
    for (const c of st.cues) {
      const ws = words(c);
      if (!ws.length) continue;
      first = true;
      const P = place(c.pos || s), keys = keyFlags(ws);
      const add = (a, b, text) => {
        if (b - a < 0.01) return;
        lines.push(`Dialogue: 0,${assTime(a)},${assTime(b)},Default,,${P.ml},${P.ml},0,,${enter(first, P)}${text}`);
        first = false;
      };
      const all = (on, hid) => ws.map((w, x) => word(c, w, keys[x], on === x, hid(x))).join(" ");
      if (s.mode === "none") { add(c.s, c.e, all(-1, () => false)); continue; }
      if (s.mode === "sweep") {
        // One line; each word gets a pause (\\k) up to when it is said, then its sweep (\\kf), in hundredths.
        let at = c.s;
        const text = ws.map((w, x) => {
          const ws0 = Math.max(at, Math.min(w.s, c.e)), we = Math.max(ws0, Math.min(w.e, c.e));
          const gap = Math.round((ws0 - at) * 100), dur = Math.max(1, Math.round((we - ws0) * 100));
          at = ws0 + dur / 100;
          return word(c, w, keys[x], false, false, `${gap > 0 ? `\\k${gap}` : ""}\\kf${dur}`);
        }).join(" ");
        add(c.s, c.e, text);
        continue;
      }
      if (s.mode === "reveal" && ws[0].s > c.s) add(c.s, ws[0].s, all(-1, () => true));
      for (let k = 0; k < ws.length; k++) {
        const a = k === 0 && s.mode !== "reveal" ? c.s : Math.max(c.s, ws[k].s);
        const b = k + 1 < ws.length ? Math.max(c.s, ws[k + 1].s) : c.e;
        add(a, Math.min(b, c.e), all(s.mode !== "reveal" ? k : -1, (x) => s.mode === "reveal" && x > k));
      }
    }
    return head + lines.join("\n") + "\n";
  }

  async function save(format) {
    msg("");
    const text = { srt: toSrt, vtt: toVtt, ass: toAss, txt: toTxt }[format]();
    try {
      const r = await cv().op("captionsSave", { path: st.video.path, format, text, lang: st.lang });
      if (!r.ok) return msg(r.error, true);
      result(r.file, null);
    } catch (e) { msg(e.message, true); }
  }
  async function burn() {
    msg("");
    $("capResult").hidden = true;
    const btn = $("capBurn");
    btn.disabled = true;
    try {
      const a = await cv().op("captionsSave", { path: st.video.path, format: "ass", text: toAss(), temp: true });
      if (!a.ok) throw new Error(a.error);
      const r = await send({ type: "convert", path: st.video.path, paths: [st.video.path, a.file], name: st.video.name,
        spec: { tool: "captions", format: "mp4", opts: st.frame.ar === "orig" ? {} : { frame: st.frame.ar, fit: st.frame.fit, cx: String(st.frame.cx) } },
        label: st.frame.ar === "orig" ? "With captions" : `With captions, ${st.frame.ar}` });
      if (!r?.ok) throw new Error(r?.error || "Couldn't start.");
      st.burnJob = r.id;
      const p = $("capProg");
      p.hidden = false;
      const timer = setInterval(() => {
        const j = jobs[st.burnJob];
        if (!j) return;
        if (j.status === "done") { clearInterval(timer); p.hidden = true; btn.disabled = false; result(j.file, j.id); }
        else if (j.status === "error" || j.status === "cancelled") {
          clearInterval(timer); p.hidden = true; btn.disabled = false;
          msg(j.status === "cancelled" ? "Stopped." : (j.error || "Couldn't make the video."), j.status === "error");
        } else {
          p.querySelector("i").style.width = Math.max(2, j.percent || 0) + "%";
          p.querySelector("span").textContent = `${Math.round(j.percent || 0)}%${j.eta && j.eta !== "?" ? ` · ${j.eta} left` : ""}`;
        }
      }, 400);
    } catch (e) { btn.disabled = false; msg(e.message, true); }
  }
  function result(file, jobId) {
    const r = $("capResult");
    r.hidden = false;
    r.innerHTML = `<div class="cv-done">✓ Saved: <b>${h(String(file).split(/[\\/]/).pop())}</b></div><div class="row">
      ${jobId ? '<button class="btn" data-a="open">Open</button>' : ""}<button class="btn" data-a="reveal">Show in folder</button></div>`;
    r.onclick = (e) => {
      const a = e.target.closest("[data-a]")?.dataset.a;
      if (a === "open") send({ type: "open", id: jobId }).then((x) => { if (x && !x.ok) toast(x.error || "Couldn't open it.", true); });
      if (a === "reveal") send(jobId ? { type: "reveal", id: jobId } : { type: "reveal", file });
    };
  }

  // ---------- dubbing: translate the captions, then speak them over the video (dub.go) ----------
  // Languages the voices speak, as the helper's voice list names them.
  let vstate = null;
  async function voiceState() {
    try { const r = await cv().op("voices"); if (r.ok) vstate = r; } catch { /* drawn again later */ }
    return vstate;
  }
  // Whole sentences with their times, from the words (the lines may be cut into a few words each).
  function sentences() {
    const all = st.cues.flatMap((c) => words(c).map((w) => ({ ...w })));
    const out = [];
    let cur = [];
    const flush = () => { if (cur.length) out.push({ s: cur[0].s, e: cur[cur.length - 1].e, src: cur.map((w) => w.t).join(" ") }); cur = []; };
    for (const w of all) {
      const prev = cur[cur.length - 1];
      if (prev && (w.s - prev.e > 1.0 || /[.?!।。？！]["'”’)]*$/.test(prev.t) || cur.map((x) => x.t).join(" ").length > 220)) flush();
      cur.push(w);
    }
    flush();
    return out;
  }
  const langLabel = (c) => (vstate?.langs?.[c]) || c;
  function dubNeed(ids) {
    const p = vstate?.packs || {};
    const missing = ids.filter((id) => p[id] && !p[id].ready);
    return missing.reduce((n, id) => n + (p[id].size || 0), 0);
  }
  const gb = (n) => n >= 1 << 30 ? (n / (1 << 30)).toFixed(1) + " GB" : Math.max(1, Math.round(n / (1 << 20))) + " MB";
  async function drawDub() {
    const box = $("capDub");
    box.hidden = !st.cues.length;
    if (box.hidden) return;
    if (!vstate) { box.innerHTML = "<h3>Dub into another language</h3><p class=\"muted small\">Loading…</p>"; await voiceState(); }
    if (!vstate) { box.innerHTML = "<h3>Dub into another language</h3><p class=\"muted small\">The AnySave helper isn't answering.</p>"; return; }
    const d = st.dub || {};
    const langs = Object.entries(vstate.langs).filter(([c]) => c !== "no").sort((a, b) => a[1].localeCompare(b[1]));
    const voiced = new Set(vstate.voices.map((v) => v.code));
    const from = d.from || (st.lang && vstate.langs[st.lang] ? st.lang : "en");
    const to = d.to || (from === "bn" ? "en" : from === "en" ? "bn" : "en");
    const model = d.model || "large";
    const opt = (c, n, sel) => `<option value="${h(c)}"${c === sel ? " selected" : ""}>${h(n)}${voiced.has(c) ? "" : " (subtitles only)"}</option>`;
    const need = dubNeed(["llama", model === "small" ? "mt-small" : "mt-large"]);
    const busy = st.dubJob && jobs[st.dubJob] && !["done", "error", "cancelled"].includes(jobs[st.dubJob].status);
    let html = `<h3>Dub into another language</h3>
      <div class="row cap-dub-row">
        <label class="small">From <select id="dubFrom">${langs.map(([c, n]) => `<option value="${h(c)}"${c === from ? " selected" : ""}>${h(n)}</option>`).join("")}</select></label>
        <label class="small">Into <select id="dubTo">${langs.map(([c, n]) => opt(c, n, to)).join("")}</select></label>
        <label class="small">Translation <select id="dubModel"><option value="large"${model === "large" ? " selected" : ""}>Better</option><option value="small"${model === "small" ? " selected" : ""}>Smaller download</option></select></label>
        <button id="dubGo" class="btn${d.lines ? "" : " primary"}"${busy ? " disabled" : ""}>${d.lines ? "Translate again" : "Translate"}</button>
      </div>
      <div class="cv-progress" id="dubTrProg" hidden><div class="cv-progress-bar"><i></i></div><span></span></div>
      <p class="hint">${need ? `The first time, the translation engine and model are downloaded once (${gb(need)}). ` : ""}Runs on this computer; nothing is uploaded. Check the translation before you make the video: machines make mistakes.</p>`;
    if (d.lines) {
      const vs = vstate.voices.filter((v) => v.code === d.to);
      const voice = vs.find((v) => v.id === d.voice) || vs[0];
      html += `<div class="cap-dub-lines" id="dubLines">${d.lines.map((l, i) => `<div class="cap-dub-line" data-i="${i}">
          <div class="cap-dub-src"><button class="btn small cap-dub-time" data-seek="${i}" title="Jump there">${clock(l.s, false)}</button><span class="muted small">${h(l.src)}</span></div>
          <div class="cap-dub-tr"><textarea class="cv-input" data-tr="${i}" rows="1" dir="auto">${h(l.t)}</textarea>${vs.length ? `<button class="btn small" data-say="${i}" title="Hear this line">▶</button>` : ""}</div></div>`).join("")}</div>`;
      if (vs.length) {
        const vneed = dubNeed(["engine", voice.pack]);
        html += `<div class="row cap-dub-row">
          <label class="small">Voice <select id="dubVoice">${vs.map((v) => `<option value="${h(v.id)}"${v.id === voice.id ? " selected" : ""}>${h(v.name)}${v.gender ? ` (${v.gender})` : ""}${vs.some((x) => x.lang !== v.lang) ? ` · ${h(v.lang)}` : ""}</option>`).join("")}</select></label>
          <label class="small">Original sound <select id="dubKeep"><option value="quiet"${d.keep !== "half" && d.keep !== "off" ? " selected" : ""}>Quietly underneath</option><option value="half"${d.keep === "half" ? " selected" : ""}>Half</option><option value="off"${d.keep === "off" ? " selected" : ""}>Off</option></select></label>
        </div>
        <button id="dubMake" class="btn primary big"${busy ? " disabled" : ""}>Make dubbed video</button>
        ${vneed ? `<p class="hint">The voice is downloaded once first (${gb(vneed)}).</p>` : ""}`;
      } else {
        html += `<p class="hint">There's no voice for ${h(langLabel(d.to))} yet, so this can't be spoken. You can still save the translated subtitles.</p>`;
      }
      html += `<div class="cv-progress" id="dubProg" hidden><div class="cv-progress-bar"><i></i></div><span></span></div>
        <div class="cap-saveas"><button class="btn small" id="dubSrt">Save translated subtitles (SRT)</button></div>
        <div id="dubResult" class="cv-result" hidden></div>
        <audio id="dubSay" hidden></audio>`;
    }
    box.innerHTML = html;
    box.querySelectorAll("textarea[data-tr]").forEach(fit);
    if (busy) watchDub(st.dubJob, st.dubKind);
  }
  const fit = (t) => { t.style.height = "auto"; t.style.height = t.scrollHeight + 2 + "px"; };
  function dubSettings() {
    const d = st.dub || (st.dub = {});
    d.from = $("dubFrom").value; d.to = d.lines ? d.to : $("dubTo").value; d.model = $("dubModel").value;
    if ($("dubVoice")) d.voice = $("dubVoice").value;
    if ($("dubKeep")) d.keep = $("dubKeep").value;
    persist();
  }
  async function translate() {
    msg("");
    const from = $("dubFrom").value, to = $("dubTo").value;
    if (from === to) return msg("Pick a different language to translate into.", true);
    const lines = sentences();
    if (!lines.length) return msg("There are no captions to translate.", true);
    st.dub = { ...(st.dub || {}), from, to, model: $("dubModel").value, lines: null };
    const base = st.video.name.replace(/\.[^.]+$/, "");
    try {
      const r = await send({ type: "convert", paths: [], name: `${base} → ${langLabel(to)}`, label: `Translate · ${langLabel(from)} → ${langLabel(to)}`,
        spec: { tool: "translate", format: "srt", opts: { lines: JSON.stringify(lines.map((l) => ({ s: l.s, e: l.e, t: l.src }))), from, to, model: st.dub.model, name: base } } });
      if (!r?.ok) throw new Error(r?.error || "Couldn't start.");
      st.dubJob = r.id; st.dubKind = "translate"; st.dubSrc = lines;
      $("dubGo").disabled = true;
      watchDub(r.id, "translate");
    } catch (e) { msg(e.message, true); }
  }
  // A small SRT reader for the translated file (same order as the sentences sent).
  function parseSrt(text) {
    return text.replace(/^﻿/, "").replace(/\r/g, "").split(/\n\s*\n/).map((b) => b.split("\n"))
      .filter((l) => l.some((x) => x.includes("-->"))).map((l) => l.slice(l.findIndex((x) => x.includes("-->")) + 1).join(" ").trim());
  }
  let dubTimer = null;
  function watchDub(id, kind) {
    clearInterval(dubTimer);
    const prog = () => $(kind === "translate" ? "dubTrProg" : "dubProg");
    dubTimer = setInterval(async () => {
      const j = jobs[id], p = prog();
      if (!j || !p) return;
      p.hidden = false;
      if (j.status === "done") {
        clearInterval(dubTimer); st.dubJob = null; p.hidden = true;
        if (kind === "translate") {
          try {
            const a = await cv().op("allow", { path: j.file });
            const text = await fetch(cv().fileURL(a.file.path)).then((x) => x.text());
            const tr = parseSrt(text), src = st.dubSrc || sentences();
            st.dub.lines = src.map((l, i) => ({ s: l.s, e: l.e, src: l.src, t: tr[i] || "" }));
            persist(); await voiceState(); drawDub();
            msg(`Translated into ${langLabel(st.dub.to)}. Check the lines, then make the dubbed video.`);
          } catch (e) { msg(e.message, true); drawDub(); }
        } else {
          drawDub();
          const r = $("dubResult");
          r.hidden = false;
          r.innerHTML = `<div class="cv-done">✓ Saved: <b>${h(String(j.file).split(/[\\/]/).pop())}</b></div><div class="row">
            <button class="btn" data-a="open">Open</button><button class="btn" data-a="reveal">Show in folder</button></div>`;
          r.onclick = (e) => {
            const a = e.target.closest("[data-a]")?.dataset.a;
            if (a === "open") send({ type: "open", id: j.id }).then((x) => { if (x && !x.ok) toast(x.error || "Couldn't open it.", true); });
            if (a === "reveal") send({ type: "reveal", id: j.id });
          };
        }
      } else if (j.status === "error" || j.status === "cancelled") {
        clearInterval(dubTimer); st.dubJob = null; p.hidden = true; drawDub();
        msg(j.status === "cancelled" ? "Stopped." : (j.error || "Something went wrong."), j.status === "error");
      } else {
        p.querySelector("i").style.width = Math.max(2, j.percent || 0) + "%";
        p.querySelector("span").textContent = `${j.speed || "Working"} · ${Math.round(j.percent || 0)}%${j.eta && j.eta !== "?" ? ` · ${j.eta} left` : ""}`;
      }
    }, 400);
  }
  const dubSrt = () => st.dub.lines.map((l, i) => `${i + 1}\n${srtTime(l.s)} --> ${srtTime(l.e)}\n${l.t.replace(/\s*\n\s*/g, " ").trim()}\n`).join("\n");
  async function makeDub() {
    msg("");
    dubSettings();
    if (!st.dub.lines.some((l) => l.t.trim())) return msg("There are no translated lines to speak.", true);
    try {
      const a = await cv().op("captionsSave", { path: st.video.path, format: "srt", text: dubSrt(), temp: true });
      if (!a.ok) throw new Error(a.error);
      const r = await send({ type: "convert", path: st.video.path, paths: [st.video.path, a.file], name: st.video.name,
        label: `${langLabel(st.dub.to)} dub`, spec: { tool: "dub", format: "", opts: { voice: st.dub.voice, keep: st.dub.keep || "quiet", lang: st.dub.to } } });
      if (!r?.ok) throw new Error(r?.error || "Couldn't start.");
      st.dubJob = r.id; st.dubKind = "dub";
      $("dubMake").disabled = true; $("dubResult").hidden = true;
      watchDub(r.id, "dub");
    } catch (e) { msg(e.message, true); }
  }
  async function sayLine(i, btn) {
    dubSettings();
    const l = st.dub.lines[i];
    if (!l.t.trim()) return;
    btn.disabled = true;
    try {
      const r = await cv().op("voiceSay", { voice: st.dub.voice || $("dubVoice")?.value, text: l.t, speed: 1 });
      if (!r.ok) {
        if (r.need) { await cv().op("voiceGet", { pack: r.need }); return msg("Downloading the voice first. Try ▶ again in a minute.", false); }
        return msg(r.error, true);
      }
      const au = $("dubSay");
      au.src = (await cv().session()).base + r.url;
      au.play().catch(() => {});
    } catch (e) { msg(e.message, true); }
    finally { btn.disabled = false; }
  }
  const dubBox = $("capDub");
  dubBox.addEventListener("click", (e) => {
    const t = e.target;
    if (t.id === "dubGo") return translate();
    if (t.id === "dubMake") return makeDub();
    if (t.id === "dubSrt") {
      dubSettings();
      return cv().op("captionsSave", { path: st.video.path, format: "srt", text: dubSrt(), lang: st.dub.to })
        .then((r) => r.ok ? result(r.file, null) : msg(r.error, true)).catch((x) => msg(x.message, true));
    }
    const say = t.closest("[data-say]");
    if (say) return sayLine(+say.dataset.say, say);
    const sk = t.closest("[data-seek]");
    if (sk) seek(st.dub.lines[+sk.dataset.seek].s);
  });
  dubBox.addEventListener("change", (e) => {
    if (e.target.id === "dubTo" && st.dub?.lines && e.target.value !== st.dub.to) {
      st.dub = { ...st.dub, to: e.target.value, lines: null }; // a new language needs a new translation
      persist(); return drawDub();
    }
    if (["dubFrom", "dubModel", "dubVoice", "dubKeep", "dubTo"].includes(e.target.id)) { dubSettings(); if (e.target.id === "dubModel") drawDub(); }
  });
  dubBox.addEventListener("input", (e) => {
    const t = e.target.closest("textarea[data-tr]");
    if (!t) return;
    st.dub.lines[+t.dataset.tr].t = t.value;
    fit(t); persist();
  });

  // ---------- wiring ----------
  const v = $("capVideo");
  v.addEventListener("loadedmetadata", () => {
    st.vw = v.videoWidth || 1920; st.vh = v.videoHeight || 1080; st.dur = v.duration || 0;
    applyFrame();
    drawTimeline();
  });
  v.addEventListener("error", () => {
    if (st.video) msg("This video can't play here, but captions can still be made and burned in. To preview it, convert it to MP4 first.", true);
  });
  for (const ev of ["seeked", "timeupdate"]) v.addEventListener(ev, () => tick()); // also when the window isn't being drawn
  v.addEventListener("play", () => { $("capPlayBtn").textContent = "❚❚"; });
  v.addEventListener("pause", () => { $("capPlayBtn").textContent = "▶"; });
  $("capPlayBtn").onclick = () => (v.paused ? v.play().catch(() => {}) : v.pause());
  $("capTimeline").addEventListener("click", (e) => {
    const r = e.currentTarget.getBoundingClientRect();
    seek((e.clientX - r.left) / r.width * (st.dur || 0));
  });
  new ResizeObserver(() => { layoutOverlay(); lastKey = ""; }).observe($("capStage"));
  const stageEl = $("capStage");
  stageEl.addEventListener("pointerdown", startDrag);
  stageEl.addEventListener("pointermove", moveDrag);
  stageEl.addEventListener("pointerup", endDrag);
  stageEl.addEventListener("pointercancel", endDrag);
  $("capOverlay").addEventListener("dblclick", (e) => { clearTimeout(wordTimer); closeWordMenu(); if (st.cues.length) focusRow(shownCue(), e); });
  $("capOverlay").addEventListener("keydown", nudge);
  const shapeEl = $("capShape");
  shapeEl.addEventListener("change", (e) => { if (e.target.id === "capAr") setFrame({ ar: e.target.value }); });
  shapeEl.addEventListener("input", (e) => {
    if (e.target.id !== "capCx") return;
    st.frame.cx = +e.target.value;
    const wide = st.vw / st.vh > (RATIOS[st.frame.ar] || 0);
    $("capVideo").style.objectPosition = wide ? `${st.frame.cx}% 50%` : `50% ${st.frame.cx}%`;
    persist();
  });
  shapeEl.addEventListener("click", (e) => { const b = e.target.closest("[data-fit]"); if (b) setFrame({ fit: b.dataset.fit }); });
  const wmEl = $("capWordMenu");
  wmEl.addEventListener("click", (e) => {
    const sw = e.target.closest("[data-wc]"), z = e.target.closest("[data-wz]"), a = e.target.closest("[data-w]");
    if (sw) setWord({ c: sw.dataset.wc });
    else if (z) setWord({ z: +z.dataset.wz === 1 ? null : +z.dataset.wz });
    else if (a?.dataset.w === "reset") setWord({ c: null, z: null });
    else if (a?.dataset.w === "close") closeWordMenu();
  });
  wmEl.addEventListener("input", (e) => { if (e.target.matches("[data-wc-pick]")) setWord({ c: e.target.value }); });
  document.addEventListener("pointerdown", (e) => { if (wm && !e.target.closest("#capWordMenu, #capOverlay")) closeWordMenu(); });
  document.addEventListener("keydown", (e) => { if (wm && e.key === "Escape") closeWordMenu(); });
  // Pinch (or Ctrl + scroll) on the caption changes its size.
  let wheelSnap = 0;
  stageEl.addEventListener("wheel", (e) => {
    if (!e.ctrlKey || !e.target.closest("#capOverlay, #capFrame") || !st.video) return;
    e.preventDefault();
    if (Date.now() - wheelSnap > 800) snapshot();
    wheelSnap = Date.now();
    st.style.size = Math.round(Math.min(14, Math.max(2.5, st.style.size * Math.exp(-e.deltaY * 0.01))) * 10) / 10;
    layoutOverlay(); lastKey = ""; tick(true); persist();
    clearTimeout(stageEl._w); stageEl._w = setTimeout(drawStyle, 300);
  }, { passive: false });

  $("capPick").onclick = pick;
  $("capFromDl").onchange = async (e) => {
    const j = jobs[e.target.value];
    e.target.value = "";
    if (j) window.openCaptions(j.file);
  };
  $("capClose").onclick = () => { v.removeAttribute("src"); v.load(); st.video = null; stopJobWatch(); msg(""); drawAll(); };
  $("capStyle").addEventListener("click", (e) => {
    const del = e.target.closest("[data-del]");
    if (del) {
      e.stopPropagation();
      saveJSON("anysave.cap.mine", mine().filter((p) => p.id !== del.dataset.del));
      return drawStyle();
    }
    const mb = e.target.closest("[data-mine]");
    if (mb) {
      if (mb.dataset.mine === "save") return saveMine();
      naming = mb.dataset.mine === "new";
      return drawStyle();
    }
    const p = e.target.closest("[data-preset]");
    if (p) {
      const pr = PRESETS.find((x) => x.id === p.dataset.preset) || mine().find((x) => x.id === p.dataset.preset);
      if (!pr) return;
      snapshot();
      st.style = { ...applyPreset(st.style, pr), phoneZone: st.style.phoneZone };
      drawStyle(); lastKey = ""; tick(true); persist();
      return;
    }
    const c = e.target.closest("[data-set]");
    if (c) setStyle(c.dataset.set, c.dataset.v, true);
  });
  $("capStyle").addEventListener("keydown", (e) => {
    if (e.target.id === "capMineName" && e.key === "Enter") saveMine();
    if (e.target.id === "capMineName" && e.key === "Escape") { naming = false; drawStyle(); }
  });
  $("capStyle").addEventListener("input", (e) => {
    const k = e.target.dataset.k;
    if (!k) return;
    setStyle(k, e.target.type === "checkbox" ? e.target.checked : e.target.value, e.target.tagName === "SELECT" || k === "auto");
  });
  const list = $("capList");
  list.addEventListener("click", onListClick);
  list.addEventListener("input", onListInput);
  list.addEventListener("change", onListChange);
  list.addEventListener("keydown", onListKey);
  list.addEventListener("focusin", (e) => {
    const i = rowIndex(e.target);
    if (i >= 0 && i !== st.sel) {
      document.querySelector("#capList .cap-cue.sel")?.classList.remove("sel");
      st.sel = i; e.target.closest(".cap-cue").classList.add("sel");
      if (v.paused) seek(st.cues[i].s);
    }
  });
  $("capChunk").innerHTML = CHUNKS.map(([n, l]) => `<option value="${n}">${h(l)}</option>`).join("");
  $("capChunk").onchange = (e) => rechunk(e.target.value);
  $("capReplaceGo").onclick = replaceAll;
  $("capShiftGo").onclick = shiftAll;
  $("capSyncGo").onclick = () => syncToSpeech(false);
  $("capRemake").onclick = () => {
    if (!confirm("Make the captions again from the speech? Your changes to the text will be replaced.")) return;
    snapshot(); st.cues = []; drawAll();
  };
  $("capFollow").onchange = (e) => { st.follow = e.target.checked; };
  $("capBurn").onclick = burn;
  document.querySelectorAll("[data-capsave]").forEach((b) => { b.onclick = () => save(b.dataset.capsave); });

  document.addEventListener("keydown", (e) => {
    if ($("captions").hidden || !st.video) return;
    const typing = e.target.closest("input,textarea,select,[contenteditable]");
    const mod = e.metaKey || e.ctrlKey;
    if (mod && e.key.toLowerCase() === "z" && !(typing && typing.tagName === "INPUT" && typing.type === "text" && typing.id)) {
      e.preventDefault();
      clearTimeout(typingSnap); typingSnap = null;
      e.shiftKey ? restore(redo, undo) : restore(undo, redo);
    } else if (e.key === " " && !typing) { e.preventDefault(); v.paused ? v.play().catch(() => {}) : v.pause(); }
  });

  const page = $("captions");
  let depth = 0;
  page.addEventListener("dragenter", (e) => { if ([...(e.dataTransfer?.types || [])].includes("Files")) { e.preventDefault(); depth++; page.classList.add("dragging"); } });
  page.addEventListener("dragover", (e) => { if ([...(e.dataTransfer?.types || [])].includes("Files")) e.preventDefault(); });
  page.addEventListener("dragleave", () => { if (--depth <= 0) { depth = 0; page.classList.remove("dragging"); } });
  page.addEventListener("drop", (e) => {
    if (!e.dataTransfer?.files?.length) return;
    e.preventDefault(); e.stopPropagation();
    depth = 0; page.classList.remove("dragging");
    dropped(e.dataTransfer.files);
  });

  // For tests and the desktop app: the current captions as files.
  window.__captions = { st, toAss, toSrt, toVtt, sentences, dubSrt, PRESETS, FONTS, normalize, drawAll };
  requestAnimationFrame(loop);
  if (location.hash === "#captions") { cv()?.session().catch(() => {}); window.fillCaptionsPicker(); }
})();
