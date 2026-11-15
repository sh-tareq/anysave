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
  ];
  const STYLE0 = { preset: "clean", boxColor: "#000000", boxOpacity: 70, ...PRESETS[0].s };
  const CHUNKS = [["", "As spoken"], ["1", "1 word"], ["2", "2 words"], ["3", "3 words"], ["4", "4 words"], ["6", "6 words"], ["8", "8 words"]];
  const VIDEO_EXT = /\.(mp4|m4v|mov|webm|mkv|avi|3gp|flv|wmv|ts|mts|m2ts|mpg|mpeg|ogv)$/i;
  const SUBS_EXT = /\.(srt|vtt|ass|ssa)$/i;

  // ---------- state ----------
  const st = {
    video: null, // {path, name, size}
    w: 1920, h: 1080, dur: 0,
    lang: "", cues: [], orig: null, chunk: "",
    style: loadJSON("anysave.cap.style") || { ...STYLE0 },
    job: null, burnJob: null, sel: -1, follow: true,
    dub: null, // {from, to, model, voice, keep, lines: [{s, e, src, t}]}
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
      if (st.video && st.cues.length) saveJSON(projectKey(), { lang: st.lang, cues: st.cues, orig: st.orig, chunk: st.chunk, style: st.style, dub: st.dub, at: Date.now() });
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
    undo.push(JSON.stringify({ cues: st.cues, chunk: st.chunk }));
    if (undo.length > 100) undo.shift();
    redo = [];
  }
  function restore(from, to) {
    if (!from.length) return;
    to.push(JSON.stringify({ cues: st.cues, chunk: st.chunk }));
    const s = JSON.parse(from.pop());
    st.cues = s.cues; st.chunk = s.chunk;
    drawAll();
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
    $("capResult").hidden = true;
    const saved = loadJSON(projectKey());
    if (saved?.cues?.length) {
      Object.assign(st, { lang: saved.lang, cues: saved.cues, orig: saved.orig, chunk: saved.chunk || "", dub: saved.dub || null });
      if (saved.style) st.style = { ...STYLE0, ...saved.style };
      msg("Your edits from last time were brought back.");
    }
    addFonts().catch(() => {});
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
  function textShadow(r, color, shadow) {
    const out = [];
    if (r > 0) for (let i = 0; i < 16; i++) { const a = i * Math.PI / 8; out.push(`${(Math.cos(a) * r).toFixed(2)}px ${(Math.sin(a) * r).toFixed(2)}px 0 ${color}`); }
    if (shadow) out.push(`${Math.max(1, r * 0.6 + 2).toFixed(1)}px ${Math.max(1, r * 0.6 + 2).toFixed(1)}px 2px rgba(0,0,0,.55)`);
    return out.join(",") || "none";
  }
  const upper = (t) => (st.style.upper ? t.toLocaleUpperCase(st.lang && st.lang !== "auto" ? st.lang : undefined) : t);
  function rgba(hex, op) { const n = parseInt(hex.slice(1), 16); return `rgba(${n >> 16},${(n >> 8) & 255},${n & 255},${op})`; }
  function layoutOverlay() {
    const stage = $("capStage"), ov = $("capOverlay"), s = st.style, F = FONTS[s.font] || FONTS.noto;
    const scale = stage.clientHeight / st.h;
    const px = s.size / 100 * st.h * scale;
    ov.style.fontFamily = `"AS ${s.font}", "AS ${F.bn}", sans-serif`;
    ov.style.fontSize = px + "px";
    ov.style.lineHeight = String(F.ratio);
    ov.style.color = s.color;
    ov.style.top = ov.style.bottom = ov.style.transform = "";
    if (s.pos === "top") ov.style.top = s.offset + "%";
    else if (s.pos === "middle") { ov.style.top = "50%"; ov.style.transform = "translateY(-50%)"; }
    else ov.style.bottom = s.offset + "%";
    ov.classList.toggle("boxed", !!s.box);
    ov.style.setProperty("--box", rgba(s.boxColor || "#000000", (s.boxOpacity ?? 70) / 100));
    ov.style.setProperty("--pad", (px * 0.22).toFixed(1) + "px");
    ov.style.textShadow = s.box ? "none" : textShadow(s.outline / 100 * px, s.outlineColor, s.shadow);
  }
  let lastKey = "";
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
    const key = `${i}|${wi}|${st.style.mode}`;
    if (force || key !== lastKey) {
      lastKey = key;
      const ov = $("capOverlay");
      if (i < 0) ov.innerHTML = "";
      else {
        const ws = words(st.cues[i]), m = st.style.mode;
        ov.innerHTML = `<span class="cap-line">${ws.map((w, k) => {
          const on = m !== "none" && m !== "reveal" && k === wi;
          const hidden = m === "reveal" && k > wi;
          return `<span class="w${on ? " on" : ""}${on && m === "pop" ? " pop" : ""}${hidden ? " hid" : ""}"${on ? ` style="color:${st.style.hl}"` : ""}>${h(upper(w.t))}</span>`;
        }).join(" ")}</span>`;
      }
      document.querySelectorAll("#capList .cap-cue.now").forEach((r) => r.classList.remove("now"));
      if (i >= 0) {
        const row = document.querySelector(`#capList .cap-cue[data-i="${i}"]`);
        if (row) {
          row.classList.add("now");
          if (st.follow && !v.paused && !row.contains(document.activeElement)) row.scrollIntoView({ block: "nearest" });
        }
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
    list.innerHTML = st.cues.map((c, i) => `<div class="cap-cue${i === st.sel ? " sel" : ""}" data-i="${i}">
      <div class="cap-cue-t">
        <button class="cap-play" data-a="play" title="Play from here" aria-label="Play from here">▶</button>
        <input class="cap-t" data-k="s" value="${clock(c.s)}" aria-label="Start time" />
        <span class="muted">→</span>
        <input class="cap-t" data-k="e" value="${clock(c.e)}" aria-label="End time" />
        <span class="cap-acts">
          <button data-a="merge" title="Join with the next line"${i + 1 >= st.cues.length ? " disabled" : ""}>Join ↓</button>
          <button data-a="add" title="Add a new line after this one">+ Line</button>
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
  function drawStyle() {
    const s = st.style;
    const fontOpts = Object.entries(FONTS).filter(([, f]) => !f.hidden).map(([k, f]) => `<option value="${k}"${k === s.font ? " selected" : ""}>${h(f.label)}</option>`).join("");
    const seg = (key, opts) => `<span class="chips seg">${opts.map(([v, l]) => `<button class="chip${String(s[key]) === v ? " active" : ""}" data-set="${key}" data-v="${v}">${h(l)}</button>`).join("")}</span>`;
    $("capStyle").innerHTML = `
      <div class="cap-presets">${PRESETS.map((p) => `<button class="cap-preset${s.preset === p.id ? " on" : ""}" data-preset="${p.id}">
        <b style="font-family:'AS ${p.s.font}';${p.s.upper ? "text-transform:uppercase;" : ""}">${h(p.title)}</b><span>${h(p.desc)}</span></button>`).join("")}</div>
      <div class="cap-grid">
        <label>Font <select data-k="font">${fontOpts}</select></label>
        <label>Size <input type="range" min="2.5" max="11" step="0.1" data-k="size" value="${s.size}" /></label>
        <label>Where ${seg("pos", [["top", "Top"], ["middle", "Middle"], ["bottom", "Bottom"]])}</label>
        <label>Distance from edge <input type="range" min="0" max="35" step="0.5" data-k="offset" value="${s.offset}"${s.pos === "middle" ? " disabled" : ""} /></label>
        <label>Text colour <input type="color" data-k="color" value="${s.color}" /></label>
        <label class="chk"><input type="checkbox" data-k="upper"${s.upper ? " checked" : ""} /> CAPITAL LETTERS</label>
        <label>Background ${seg("box", [["false", "Outline"], ["true", "Box"]])}</label>
        ${s.box ? `<label>Box colour <input type="color" data-k="boxColor" value="${s.boxColor}" /></label>
          <label>Box see-through <input type="range" min="20" max="100" step="5" data-k="boxOpacity" value="${s.boxOpacity}" /></label>`
        : `<label>Outline <input type="range" min="0" max="25" step="0.5" data-k="outline" value="${s.outline}" /></label>
          <label>Outline colour <input type="color" data-k="outlineColor" value="${s.outlineColor}" /></label>
          <label class="chk"><input type="checkbox" data-k="shadow"${s.shadow ? " checked" : ""} /> Soft shadow</label>`}
        <label class="wide">Words ${seg("mode", [["none", "Still"], ["word", "Light up"], ["pop", "Pop"], ["reveal", "Appear"]])}</label>
        ${s.mode === "word" || s.mode === "pop" ? `<label>Highlight colour <input type="color" data-k="hl" value="${s.hl}" /></label>` : ""}
      </div>`;
    layoutOverlay();
  }
  function setStyle(k, v, redraw) {
    const s = st.style;
    if (["size", "offset", "outline", "boxOpacity"].includes(k)) v = +v;
    if (v === "true") v = true; else if (v === "false") v = false;
    s[k] = v; s.preset = "";
    if (redraw) drawStyle(); else layoutOverlay();
    lastKey = ""; tick(true);
    persist();
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
    const an = s.pos === "top" ? 8 : s.pos === "middle" ? 5 : 2;
    const mv = s.pos === "middle" ? 0 : Math.round(s.offset / 100 * H);
    const ml = Math.round(W * 0.07);
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
Style: Default,${F.name},${fs(F)},${assColor(s.color)},${assColor(s.color)},${outlineC},${backC},${F.bold ? -1 : 0},0,0,0,100,100,0,0,${s.box ? 4 : 1},${bord},${shad},${an},${ml},${ml},${mv},1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`;
    const color = assColor(s.color), hl = assColor(s.hl);
    const word = (w, on, hidden) => {
      const parts = upper(w.t).split(BN_RUN).filter(Boolean);
      return parts.map((p) => {
        const f = BN_RUN.test(p) ? BN : F;
        // Only the letters' and outline's see-through (\\1a, \\3a): \\alpha would also change the box.
        const a = hidden ? "FF" : "00";
        const tag = `\\fn${f.name}\\fs${fs(f) * (on && s.mode === "pop" ? 1.15 : 1)}\\b${f.bold}\\c${on ? hl : color}\\1a&H${a}&\\3a&H${a}&${s.box ? "" : `\\4a&H${hidden ? "FF" : "73"}&`}`;
        return `{${tag}}${assText(p)}`;
      }).join("");
    };
    const lines = [];
    const add = (a, b, text) => { if (b - a >= 0.01) lines.push(`Dialogue: 0,${assTime(a)},${assTime(b)},Default,,0,0,0,,${text}`); };
    for (const c of st.cues) {
      const ws = words(c);
      if (!ws.length) continue;
      if (s.mode === "none") { add(c.s, c.e, ws.map((w) => word(w, false, false)).join(" ")); continue; }
      if (s.mode === "reveal" && ws[0].s > c.s) add(c.s, ws[0].s, ws.map((w) => word(w, false, true)).join(" "));
      for (let k = 0; k < ws.length; k++) {
        const a = k === 0 && s.mode !== "reveal" ? c.s : Math.max(c.s, ws[k].s);
        const b = k + 1 < ws.length ? Math.max(c.s, ws[k + 1].s) : c.e;
        add(a, Math.min(b, c.e), ws.map((w, x) => word(w, s.mode !== "reveal" && x === k, s.mode === "reveal" && x > k)).join(" "));
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
        spec: { tool: "captions", format: "mp4", opts: {} }, label: "With captions" });
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
    st.w = v.videoWidth || 1920; st.h = v.videoHeight || 1080; st.dur = v.duration || 0;
    const stage = $("capStage");
    stage.style.aspectRatio = `${st.w} / ${st.h}`;
    stage.style.width = `min(100%, calc(56vh * ${st.w / st.h}))`;
    drawTimeline(); layoutOverlay(); tick(true);
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

  $("capPick").onclick = pick;
  $("capFromDl").onchange = async (e) => {
    const j = jobs[e.target.value];
    e.target.value = "";
    if (j) window.openCaptions(j.file);
  };
  $("capClose").onclick = () => { v.removeAttribute("src"); v.load(); st.video = null; stopJobWatch(); msg(""); drawAll(); };
  $("capStyle").addEventListener("click", (e) => {
    const p = e.target.closest("[data-preset]");
    if (p) {
      const pr = PRESETS.find((x) => x.id === p.dataset.preset);
      st.style = { ...st.style, ...pr.s, preset: pr.id };
      drawStyle(); lastKey = ""; tick(true); persist();
      return;
    }
    const c = e.target.closest("[data-set]");
    if (c) setStyle(c.dataset.set, c.dataset.v, true);
  });
  $("capStyle").addEventListener("input", (e) => {
    const k = e.target.dataset.k;
    if (!k) return;
    setStyle(k, e.target.type === "checkbox" ? e.target.checked : e.target.value, e.target.tagName === "SELECT");
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
  window.__captions = { st, toAss, toSrt, toVtt, sentences, dubSrt };
  requestAnimationFrame(loop);
  if (location.hash === "#captions") { cv()?.session().catch(() => {}); window.fillCaptionsPicker(); }
})();
