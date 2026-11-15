// AnySave "Voice Lab" page (Chrome extension and the Mac/Windows apps): clean up a voice recording
// (noise removal and even loudness) and read text aloud into an audio file, all on this computer.
// Uses the Convert page's helper connection (window.AnySaveCv, convert.js); the work is in voice.go.
(() => {
  "use strict";
  const cv = () => window.AnySaveCv;
  const h = (s) => esc(s);
  const MEDIA_EXT = /\.(mp3|m4a|aac|wav|flac|ogg|oga|opus|wma|amr|aiff?|caf|mka|mp4|m4v|mov|webm|mkv|avi|3gp|flv|wmv|ts|mts|m2ts|mpg|mpeg|ogv)$/i;
  const VIDEO_EXT = /\.(mp4|m4v|mov|webm|mkv|avi|3gp|flv|wmv|ts|mts|m2ts|mpg|mpeg|ogv)$/i;
  const TEXT_EXT = /\.(txt|srt|vtt|md)$/i;
  const STRENGTHS = [
    { id: "strong", pack: "clean-strong", title: "Strong", desc: "Best result" },
    { id: "light", pack: "clean-light", title: "Light", desc: "Quicker, a little duller" },
    { id: "basic", pack: "", title: "Basic", desc: "No download needed" },
  ];
  const MAX_TEXT = 100000;

  const st = {
    tab: "clean",
    file: null, dur: 0, strength: "strong",
    packs: {}, voices: [],
    cleanJob: null, speakJob: null,
  };
  const saved = (() => { try { return JSON.parse(localStorage.getItem("anysave.voice") || "{}"); } catch { return {}; } })();
  function remember() {
    try {
      localStorage.setItem("anysave.voice", JSON.stringify({ voice: $("vlVoice").value, speed: $("vlSpeed").value,
        strength: st.strength, level: $("vlLevel").checked, text: $("vlText").value.slice(0, 20000), tab: st.tab }));
    } catch { /* private window */ }
  }

  function msg(text, bad) {
    const m = $("vlMsg");
    m.hidden = !text;
    m.textContent = text || "";
    m.classList.toggle("err", !!bad);
  }
  const mb = (n) => (n < 1 << 20 ? "under 1 MB" : Math.round(n / (1 << 20)) + " MB");
  const clock = (s) => `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, "0")}`;

  // ---------- what's downloaded ----------
  let stateTimer = null;
  async function refresh() {
    try {
      const r = await cv().op("voices");
      if (!r.ok) return msg(r.error, true);
      st.packs = r.packs;
      setVoices(r.voices);
      drawNeeds();
      const busy = Object.values(r.packs).some((p) => p.done != null && !p.ready && !p.error);
      clearTimeout(stateTimer);
      if (busy) stateTimer = setTimeout(refresh, 700);
    } catch (e) { msg(e.message, true); }
  }
  // needBox shows "this needs a one-time download" for the engine plus a pack, with progress.
  function needBox(box, pack, what) {
    const ids = pack ? ["engine", pack] : [];
    const missing = ids.filter((id) => st.packs[id] && !st.packs[id].ready);
    box.hidden = !missing.length;
    if (!missing.length) return true;
    if (missing.some((id) => st.packs[id].works === false)) {
      box.innerHTML = `<span class="muted small">This isn't available on this kind of computer yet.</span>`;
      return false;
    }
    const size = missing.reduce((n, id) => n + (st.packs[id].size || 0), 0);
    const done = missing.reduce((n, id) => n + (st.packs[id].ready ? st.packs[id].size : st.packs[id].done || 0), 0);
    const getting = missing.some((id) => st.packs[id].done != null && !st.packs[id].error);
    const err = missing.map((id) => st.packs[id].error).find(Boolean);
    box.innerHTML = getting
      ? `<div class="cv-progress"><div class="cv-progress-bar"><i style="width:${Math.max(2, done * 100 / size)}%"></i></div><span>Downloading ${h(what)}… ${mb(done)} of ${mb(size)}</span></div>`
      : `<span class="small">${h(what[0].toUpperCase() + what.slice(1))} needs a one-time download of ${mb(size)}.</span>
         <button class="btn small" data-get="${h(pack)}">Download now</button>${err ? `<span class="small err">${h(err)}</span>` : ""}
         <span class="muted small">Or just save: it downloads first.</span>`;
    return false;
  }
  function drawNeeds() {
    const s = STRENGTHS.find((x) => x.id === st.strength);
    needBox($("vlNeed"), s.pack, "the " + s.title.toLowerCase() + " clean-up");
    const v = voice();
    if (v) needBox($("vlVoiceNeed"), v.pack, packWhat(v));
    $("vlForget").hidden = v?.pack !== "pocket";
  }
  const packWhat = (v) => (v.pack === "bangla" ? "the Bangla voices" : v.pack === "pocket" ? "voice copying" : "these voices");
  async function getPack(pack) {
    try {
      const r = await cv().op("voiceGet", { pack });
      if (!r.ok) return msg(r.error, true);
      st.packs = r.packs;
      setTimeout(refresh, 300);
    } catch (e) { msg(e.message, true); }
  }
  for (const id of ["vlNeed", "vlVoiceNeed"]) {
    $(id).addEventListener("click", (e) => { const b = e.target.closest("[data-get]"); if (b) { b.disabled = true; getPack(b.dataset.get); } });
  }

  // ---------- clean up ----------
  function drawStrength() {
    $("vlStrength").innerHTML = STRENGTHS.map((s) => {
      const p = s.pack && st.packs[s.pack];
      const note = !s.pack ? s.desc : p && !p.ready ? `${s.desc} · ${mb((p.size || 0) + (st.packs.engine?.ready ? 0 : st.packs.engine?.size || 0))} once` : s.desc;
      return `<button class="vl-opt${s.id === st.strength ? " on" : ""}" data-s="${s.id}" role="radio" aria-checked="${s.id === st.strength}"><b>${h(s.title)}</b><small>${h(note)}</small></button>`;
    }).join("");
  }
  $("vlStrength").addEventListener("click", (e) => {
    const b = e.target.closest("[data-s]");
    if (!b) return;
    st.strength = b.dataset.s;
    $("vlAB").hidden = true;
    drawStrength(); drawNeeds(); remember();
  });
  $("vlLevel").onchange = () => { $("vlAB").hidden = true; remember(); };

  async function openFile(f) {
    if (!f) return;
    if (!MEDIA_EXT.test(f.name)) return msg("Choose a recording or a video (MP3, M4A, WAV, MP4, MOV…).", true);
    msg("");
    st.file = f;
    $("vlName").textContent = f.name;
    $("vlFile").hidden = false;
    $("vlDrop").classList.add("small");
    $("vlAB").hidden = true;
    $("vlResult").hidden = true;
    const video = VIDEO_EXT.test(f.name);
    const ext = (f.name.match(/\.(\w+)$/) || [, ""])[1].toLowerCase();
    $("vlFormat").innerHTML = (video
      ? [["same", `Video with clean sound (${ext.toUpperCase()})`], ["mp3", "Just the sound, MP3"], ["m4a", "Just the sound, M4A"], ["wav", "Just the sound, WAV"]]
      : [["same", `Same kind of file (${ext.toUpperCase()})`], ["mp3", "MP3"], ["m4a", "M4A"], ["wav", "WAV"]])
      .map(([v, l]) => `<option value="${v}">${h(l)}</option>`).join("");
    st.dur = 0;
    try {
      const r = await cv().op("info", { path: f.path });
      st.dur = r.info?.duration || r.duration || 0;
      if (r.ok && r.info && !r.info.hasAudio) msg(f.name + " has no sound to clean.", true);
    } catch { /* the preview still works */ }
    const at = $("vlAt");
    at.max = Math.max(0, Math.floor(st.dur - 12));
    at.value = Math.min(+at.max, Math.floor(st.dur * 0.2));
    at.disabled = st.dur <= 13;
    atText();
  }
  function atText() { $("vlAtText").textContent = st.dur ? `${clock(+$("vlAt").value)} of ${clock(st.dur)}` : ""; }
  $("vlAt").oninput = atText;

  async function tryClean() {
    msg("");
    const s = STRENGTHS.find((x) => x.id === st.strength);
    if (s.pack && !needBox($("vlNeed"), s.pack, "the " + s.title.toLowerCase() + " clean-up")) {
      getPack(s.pack);
      return msg("Downloading the clean-up first. The preview starts when it's done.");
    }
    $("vlTry").disabled = true;
    $("vlTryProg").hidden = false;
    try {
      const r = await cv().op("voiceClean", { path: st.file.path, strength: st.strength, level: $("vlLevel").checked ? "1" : "0", at: +$("vlAt").value });
      if (!r.ok) {
        if (r.need) { getPack(r.need); return msg("Downloading the clean-up first. Try the preview again when it's done."); }
        return msg(r.error, true);
      }
      const base = (await cv().session()).base;
      $("vlBefore").src = base + r.before;
      $("vlAfter").src = base + r.after;
      $("vlAB").hidden = false;
      $("vlAfter").play().catch(() => {});
    } catch (e) { msg(e.message, true); }
    finally { $("vlTry").disabled = false; $("vlTryProg").hidden = true; }
  }
  // Only one of the two plays at a time, from the same moment, so they're easy to compare.
  for (const [a, b] of [["vlBefore", "vlAfter"], ["vlAfter", "vlBefore"]]) {
    $(a).addEventListener("play", () => { const o = $(b); if (!o.paused) { $(a).currentTime = o.currentTime; o.pause(); } });
  }
  $("vlTry").onclick = tryClean;

  async function saveClean() {
    msg("");
    $("vlResult").hidden = true;
    const btn = $("vlGo");
    btn.disabled = true;
    try {
      const r = await send({ type: "convert", path: st.file.path, paths: [st.file.path], name: st.file.name,
        spec: { tool: "voiceclean", format: $("vlFormat").value, opts: { strength: st.strength, level: $("vlLevel").checked ? "1" : "0" } },
        label: `Clean voice · ${STRENGTHS.find((x) => x.id === st.strength).title}` });
      if (!r?.ok) throw new Error(r?.error || "Couldn't start.");
      st.cleanJob = r.id;
      watch(r.id, "vlProg", "vlResult", btn, () => refresh());
    } catch (e) { btn.disabled = false; msg(e.message, true); }
  }
  $("vlGo").onclick = saveClean;

  function watch(id, progId, resultId, btn, after) {
    const p = $(progId);
    p.hidden = false;
    p.querySelector("i").style.width = "2%";
    p.querySelector("span").textContent = "Starting…";
    const timer = setInterval(() => {
      const j = jobs[id];
      if (!j) return;
      if (j.status === "done") { clearInterval(timer); p.hidden = true; btn.disabled = false; result(resultId, j); after?.(); }
      else if (j.status === "error" || j.status === "cancelled") {
        clearInterval(timer); p.hidden = true; btn.disabled = false; after?.();
        msg(j.status === "cancelled" ? "Stopped." : (j.error || "Something went wrong."), j.status === "error");
      } else {
        p.querySelector("i").style.width = Math.max(2, j.percent || 0) + "%";
        p.querySelector("span").textContent = `${j.speed || "Working"} · ${Math.round(j.percent || 0)}%${j.eta && j.eta !== "?" ? ` · ${j.eta} left` : ""}`;
      }
    }, 400);
  }
  function result(id, j) {
    const r = $(id);
    r.hidden = false;
    const audio = /\.(mp3|m4a|wav)$/i.test(j.file || "");
    r.innerHTML = `<div class="cv-done">✓ Saved: <b>${h(String(j.file).split(/[\\/]/).pop())}</b></div>
      ${audio ? `<audio controls preload="metadata" class="vl-result-audio"></audio>` : ""}
      <div class="row"><button class="btn" data-a="open">Open</button><button class="btn" data-a="reveal">Show in folder</button></div>`;
    if (audio) {
      cv().op("allow", { path: j.file }).then((x) => { if (x.ok) r.querySelector("audio").src = cv().fileURL(x.file.path); }).catch(() => {});
    }
    r.onclick = (e) => {
      const a = e.target.closest("[data-a]")?.dataset.a;
      if (a === "open") send({ type: "open", id: j.id }).then((x) => { if (x && !x.ok) toast(x.error || "Couldn't open it.", true); });
      if (a === "reveal") send({ type: "reveal", id: j.id });
    };
  }

  // ---------- read aloud ----------
  const voice = () => st.voices.find((v) => v.id === $("vlVoice").value);
  // setVoices takes a fresh list from the helper (copied voices come and go) and keeps the choice.
  function setVoices(list, pick) {
    const same = list.map((v) => v.id).join() === st.voices.map((v) => v.id).join();
    st.voices = list;
    if (!same || pick) fillVoices(pick || $("vlVoice").value || saved.voice);
  }
  function fillVoices(pick = saved.voice) {
    const langs = [...new Set(st.voices.map((v) => v.lang))];
    const want = st.voices.find((v) => v.id === pick) || st.voices.find((v) => v.id === "k3");
    $("vlLang").innerHTML = langs.map((l) => `<option${l === want?.lang ? " selected" : ""}>${h(l)}</option>`).join("");
    fillVoiceList(want?.id);
  }
  function fillVoiceList(pick) {
    const lang = $("vlLang").value;
    const list = st.voices.filter((v) => v.lang === lang);
    $("vlVoice").innerHTML = list.map((v) => `<option value="${h(v.id)}"${v.id === pick ? " selected" : ""}>${h(v.name)}${v.gender ? ` (${v.gender})` : ""}</option>`).join("");
    drawNeeds();
  }
  $("vlLang").onchange = () => { fillVoiceList(); remember(); };
  $("vlVoice").onchange = () => { drawNeeds(); remember(); };
  $("vlSpeed").oninput = () => { $("vlSpeedText").textContent = (+$("vlSpeed").value).toFixed(2).replace(/0$/, "") + "×"; remember(); };
  function count() {
    const t = $("vlText").value;
    const words = (t.match(/\S+/g) || []).length;
    // About 150 words a minute at normal speed.
    const mins = words / 150 / (+$("vlSpeed").value || 1);
    $("vlCount").textContent = words ? `${words.toLocaleString()} words · ${mins < 1 ? "under a minute" : "about " + Math.round(mins) + " min"} of audio` : "";
    if (t.length > MAX_TEXT) $("vlCount").textContent += ` · too long: up to ${MAX_TEXT.toLocaleString()} characters at a time`;
  }
  let saveTimer = null;
  $("vlText").addEventListener("input", () => { count(); clearTimeout(saveTimer); saveTimer = setTimeout(remember, 800); });
  $("vlClear").onclick = () => { $("vlText").value = ""; count(); remember(); };

  // Subtitles become plain text: no numbers or times, and lines of one caption joined.
  function fromSubs(text) {
    return text.replace(/\r/g, "").replace(/^WEBVTT.*\n/, "").split(/\n\s*\n/)
      .map((block) => block.split("\n").filter((l) => !/^\d+$/.test(l.trim()) && !/-->/.test(l) && !/^(NOTE|STYLE)\b/.test(l))
        .map((l) => l.replace(/<[^>]+>/g, "").replace(/\{[^}]*\}/g, "").trim()).filter(Boolean).join(" "))
      .filter(Boolean).join("\n");
  }
  function loadText(name, text) {
    if (/\.(srt|vtt)$/i.test(name)) text = fromSubs(text);
    $("vlText").value = text.slice(0, MAX_TEXT);
    count(); remember();
    if (text.length > MAX_TEXT) msg(`Only the first ${MAX_TEXT.toLocaleString()} characters were kept. Save the rest in a second go.`, true);
  }
  $("vlOpenText").onclick = async () => {
    try {
      const r = await cv().op("pick", { multi: false });
      if (!r.ok) return msg(r.error, true);
      const f = r.files?.[0];
      if (!f) return;
      if (!TEXT_EXT.test(f.name)) return msg("Choose a text or subtitle file (TXT, SRT, VTT).", true);
      const t = await fetch(cv().fileURL(f.path)).then((x) => x.text());
      loadText(f.name, t);
    } catch (e) { msg(e.message, true); }
  };

  async function listen() {
    msg("");
    const v = voice();
    if (!v) return;
    const ta = $("vlText");
    let text = ta.value.slice(ta.selectionStart, ta.selectionEnd).trim() || ta.value.trim();
    if (!text) text = v.pack === "bangla" ? "আমি AnySave। আমি আপনার লেখা পড়ে শোনাতে পারি।" : sampleFor(v.lang);
    if (!needBox($("vlVoiceNeed"), v.pack, packWhat(v))) {
      getPack(v.pack);
      return msg("Downloading the voices first. Press Listen again when it's done.");
    }
    const b = $("vlListen");
    b.disabled = true;
    b.textContent = "…";
    try {
      const r = await cv().op("voiceSay", { voice: v.id, speed: +$("vlSpeed").value, text });
      if (!r.ok) {
        if (r.need) { getPack(r.need); return msg("Downloading the voices first. Press Listen again when it's done."); }
        return msg(r.error, true);
      }
      const a = $("vlSay");
      a.src = (await cv().session()).base + r.url;
      a.play().catch(() => {});
    } catch (e) { msg(e.message, true); }
    finally { b.disabled = false; b.textContent = "▶ Listen"; }
  }
  function sampleFor(lang) {
    return ({
      Spanish: "Hola, soy una voz de AnySave. Puedo leer tu texto en voz alta.",
      French: "Bonjour, je suis une voix d'AnySave. Je peux lire votre texte à voix haute.",
      Hindi: "नमस्ते, मैं AnySave की आवाज़ हूँ। मैं आपका लेख पढ़कर सुना सकती हूँ।",
      Italian: "Ciao, sono una voce di AnySave. Posso leggere il tuo testo ad alta voce.",
      "Portuguese (Brazil)": "Olá, eu sou uma voz do AnySave. Posso ler o seu texto em voz alta.",
      Chinese: "你好，我是 AnySave 的声音。我可以为你朗读文字。",
    })[lang] || "Hello, I'm a voice in AnySave. I can read your text aloud, right here on this computer.";
  }
  $("vlListen").onclick = listen;

  async function saveSpeech() {
    msg("");
    $("vlSpeakResult").hidden = true;
    const v = voice();
    const text = $("vlText").value.trim();
    if (!text) return msg("Type or paste some text first.", true);
    if (text.length > MAX_TEXT) return msg(`That's too long for one file: up to ${MAX_TEXT.toLocaleString()} characters at a time.`, true);
    const btn = $("vlSpeakGo");
    btn.disabled = true;
    try {
      const name = text.split(/\s+/).slice(0, 6).join(" ").slice(0, 60);
      const r = await send({ type: "convert", paths: [], name, spec: { tool: "speak", format: $("vlSpeakFormat").value,
        opts: { voice: v.id, speed: String($("vlSpeed").value), text } }, label: `Read aloud · ${v.name} (${v.lang})` });
      if (!r?.ok) throw new Error(r?.error || "Couldn't start.");
      st.speakJob = r.id;
      watch(r.id, "vlSpeakProg", "vlSpeakResult", btn, () => refresh());
    } catch (e) { btn.disabled = false; msg(e.message, true); }
  }
  $("vlSpeakGo").onclick = saveSpeech;

  // ---------- copy a voice ----------
  // The sample comes from the microphone, a file, or a drop; the helper keeps 5–20 seconds of it.
  const copy = { file: null, rec: null, chunks: [], timer: null, t0: 0 };
  function copyReady() { $("vlCopyGo").disabled = !(copy.file && $("vlConsent").checked); }
  function copyOpen(on) {
    $("vlCopyBox").hidden = !on;
    $("vlCopyOpen").hidden = on;
    if (!on) { stopRec(true); copy.file = null; $("vlSampleAudio").hidden = true; $("vlSampleAudio").removeAttribute("src"); $("vlConsent").checked = false; $("vlSample").textContent = "Or drop a recording here."; copyReady(); }
  }
  function setSample(f, note) {
    copy.file = f;
    $("vlSample").textContent = note || f.name;
    const a = $("vlSampleAudio");
    a.hidden = false;
    a.src = cv().fileURL(f.path);
    copyReady();
  }
  $("vlCopyOpen").onclick = () => { copyOpen(true); msg(""); };
  $("vlCopyCancel").onclick = () => copyOpen(false);
  $("vlConsent").onchange = copyReady;
  $("vlCopyPick").onclick = async () => {
    try {
      const r = await cv().op("pick", { multi: false });
      if (!r.ok) return msg(r.error, true);
      const f = r.files?.[0];
      if (!f) return;
      if (!MEDIA_EXT.test(f.name)) return msg("Choose a recording or a video (MP3, M4A, WAV, MP4…).", true);
      setSample(f);
    } catch (e) { msg(e.message, true); }
  };
  async function startRec() {
    msg("");
    let stream;
    const b = $("vlRec");
    b.disabled = true;
    $("vlSample").textContent = "Waiting for the microphone… allow it if you're asked.";
    try { stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: false, noiseSuppression: true } }); }
    catch {
      $("vlSample").textContent = "Or drop a recording here.";
      return msg("Couldn't use the microphone. Allow it for AnySave, or choose a recording instead.", true);
    } finally { b.disabled = false; }
    copy.chunks = [];
    const rec = new MediaRecorder(stream);
    copy.rec = rec;
    rec.ondataavailable = (e) => { if (e.data.size) copy.chunks.push(e.data); };
    rec.onstop = async () => {
      stream.getTracks().forEach((t) => t.stop());
      clearInterval(copy.timer);
      $("vlRec").classList.remove("rec");
      $("vlRec").textContent = "● Record again";
      if (copy.cancelled) return;
      const secs = (Date.now() - copy.t0) / 1000;
      if (secs < 5) return msg("That was too short. Talk for 10–20 seconds.", true);
      try {
        const type = rec.mimeType || "audio/webm";
        const f = new File(copy.chunks, `Recording ${new Date().toLocaleTimeString().replace(/[:\s]/g, "-")}.${/mp4/.test(type) ? "m4a" : "webm"}`, { type });
        const r = await cv().upload(f);
        if (!r.ok) return msg(r.error, true);
        setSample(r.file, `Recorded ${Math.round(secs)} seconds`);
      } catch (e) { msg(e.message, true); }
    };
    copy.cancelled = false;
    copy.t0 = Date.now();
    rec.start(500);
    $("vlRec").classList.add("rec");
    const tick = () => {
      const s = Math.floor((Date.now() - copy.t0) / 1000);
      $("vlRec").textContent = `■ Stop (${s}s)`;
      $("vlSample").textContent = s < 10 ? "Keep talking… (10–20 seconds is best)" : "Good. Stop when you like.";
      if (s >= 20) stopRec();
    };
    tick();
    copy.timer = setInterval(tick, 250);
  }
  function stopRec(cancel) {
    if (!copy.rec || copy.rec.state === "inactive") return;
    copy.cancelled = !!cancel;
    copy.rec.stop();
  }
  $("vlRec").onclick = () => (copy.rec && copy.rec.state === "recording" ? stopRec() : startRec());
  $("vlCopyGo").onclick = async () => {
    if (!copy.file || !$("vlConsent").checked) return;
    const b = $("vlCopyGo");
    b.disabled = true;
    b.textContent = "Making the voice…";
    msg("");
    try {
      const r = await cv().op("voiceCopy", { path: copy.file.path, name: $("vlCopyName").value, consent: $("vlConsent").checked });
      if (!r.ok) return msg(r.error, true);
      st.packs = r.packs;
      setVoices(r.voices, r.made);
      remember();
      copyOpen(false);
      $("vlCopyName").value = "";
      const v = voice();
      msg(`"${v?.name || "Your voice"}" is ready. Type some English text and press Listen.`);
      drawNeeds();
    } catch (e) { msg(e.message, true); }
    finally { b.textContent = "Make this voice"; copyReady(); }
  };
  $("vlForget").onclick = async () => {
    const v = voice();
    if (!v || v.pack !== "pocket") return;
    if (!confirm(`Delete the copied voice "${v.name}"? Its sample is removed from this computer.`)) return;
    try {
      const r = await cv().op("voiceForget", { voice: v.id });
      if (!r.ok) return msg(r.error, true);
      setVoices(r.voices, "k3");
      remember();
      msg(`Deleted "${v.name}".`);
    } catch (e) { msg(e.message, true); }
  };

  // ---------- tabs, picking, dropping ----------
  function tab(name) {
    st.tab = name;
    document.querySelectorAll("[data-vltab]").forEach((b) => b.classList.toggle("active", b.dataset.vltab === name));
    $("vlClean").hidden = name !== "clean";
    $("vlSpeak").hidden = name !== "speak";
    msg("");
    remember();
  }
  document.querySelectorAll("[data-vltab]").forEach((b) => { b.onclick = () => tab(b.dataset.vltab); });

  $("vlPick").onclick = async () => {
    try {
      const r = await cv().op("pick", { multi: false });
      if (!r.ok) return msg(r.error, true);
      openFile(r.files?.[0]);
    } catch (e) { msg(e.message, true); }
  };
  $("vlFromDl").onchange = (e) => {
    const j = jobs[e.target.value];
    e.target.value = "";
    if (j) window.openVoiceLab(j.file);
  };
  $("vlClose").onclick = () => {
    st.file = null;
    $("vlFile").hidden = true;
    $("vlDrop").classList.remove("small");
    for (const a of ["vlBefore", "vlAfter"]) { $(a).pause(); $(a).removeAttribute("src"); }
    msg("");
  };
  function fillPicker() {
    const done = Object.values(jobs).filter((j) => j.status === "done" && j.file && MEDIA_EXT.test(j.file))
      .sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0)).slice(0, 200);
    $("vlFromDl").innerHTML = '<option value="">…or one of your downloads</option>' + done.map((j) => `<option value="${h(j.id)}">${h(displayName(j))}</option>`).join("");
  }
  window.showVoiceLab = () => {
    fillPicker();
    cv()?.session().then(() => { refresh().then(drawStrength); }).catch((e) => msg(e.message, true));
  };
  window.openVoiceLab = async (path) => { // "Clean up voice" on a finished download
    showTab("voice");
    tab("clean");
    try {
      const r = await cv().op("allow", { path });
      if (r.ok) openFile(r.file); else msg(r.error, true);
    } catch (e) { msg(e.message, true); }
  };

  const page = $("voice");
  let depth = 0;
  const hasFiles = (e) => [...(e.dataTransfer?.types || [])].includes("Files");
  page.addEventListener("dragenter", (e) => { if (hasFiles(e)) { e.preventDefault(); depth++; page.classList.add("dragging"); } });
  page.addEventListener("dragover", (e) => { if (hasFiles(e)) e.preventDefault(); });
  page.addEventListener("dragleave", () => { if (--depth <= 0) { depth = 0; page.classList.remove("dragging"); } });
  page.addEventListener("drop", async (e) => {
    if (!e.dataTransfer?.files?.length) return;
    e.preventDefault(); e.stopPropagation();
    depth = 0; page.classList.remove("dragging");
    const f = e.dataTransfer.files[0];
    if (TEXT_EXT.test(f.name)) { tab("speak"); return loadText(f.name, await f.text()); }
    if (!MEDIA_EXT.test(f.name)) return msg("Drop a recording or video, or a text or subtitle file to read aloud.", true);
    const forCopy = st.tab === "speak" && !$("vlCopyBox").hidden;
    if (!forCopy) tab("clean");
    try {
      msg(`Adding ${f.name}…`);
      const r = await cv().upload(f);
      if (!r.ok) return msg(r.error, true);
      msg("");
      if (forCopy) return setSample(r.file);
      openFile(r.file);
    } catch (err) { msg(err.message, true); }
  });

  // ---------- start ----------
  if (saved.strength && STRENGTHS.some((s) => s.id === saved.strength)) st.strength = saved.strength;
  if (saved.level === false) $("vlLevel").checked = false;
  if (saved.speed) { $("vlSpeed").value = saved.speed; $("vlSpeed").oninput(); }
  if (saved.text) $("vlText").value = saved.text;
  count();
  drawStrength();
  tab(saved.tab === "speak" ? "speak" : "clean");
  window.__voice = { st, refresh, fromSubs, copy };
  if (location.hash === "#voice") window.showVoiceLab();
})();
