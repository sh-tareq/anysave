// AnySave "Archives" page (Chrome extension and the Mac/Windows apps): look inside ZIP, 7z, RAR, TAR
// and ISO files, unpack all or some of them, test, repair, convert, change ZIP/7z archives, make new
// ZIP or 7z archives (with AES-256 passwords and parts), and split or join big files.
// The page is only the screen: the AnySave helper does the work (cv_archive*.go and the arc package),
// through the Convert page's connection (window.AnySaveCv, convert.js) and helper jobs.
(() => {
  "use strict";
  const cv = () => window.AnySaveCv;
  const h = (s) => esc(s);
  const isMac = /Mac/i.test(navigator.platform || navigator.userAgent);
  const SHOW = isMac ? "Show in Finder" : "Show in folder";
  const TRASH = isMac ? "Trash" : "Recycle Bin";
  const ARCHIVE_RE = /\.(zip|7z|rar|tar|tgz|tbz2?|txz|tzst|gz|bz2|xz|zst|iso|cbz|cbr|cb7|001|z01|\d{3})$/i;
  const IMG_RE = /\.(png|jpe?g|gif|webp|bmp|svg|avif|ico)$/i;
  const AUDIO_RE = /\.(mp3|m4a|aac|wav|ogg|oga|opus|flac)$/i;
  const VIDEO_RE = /\.(mp4|m4v|webm|mov)$/i;

  const st = {
    tab: "open",
    a: null,          // the open archive: {path, first, name, info, entries, safety, copy, parts, more}
    pw: "",           // its password (kept only while it is open, never saved)
    dir: "",          // folder shown inside it ("" = top)
    sel: new Set(),   // ticked names (folders without the trailing /)
    sort: { key: "name", up: true },
    q: "",
    tree: null,
    destPath: "",     // a folder chosen with "Choose a folder…"
    job: null,
    mk: [],           // items for a new archive
    mkFormat: "zip",
    mkDestPath: "",
    spFile: null, jnFile: null,
  };
  const saved = (() => { try { return JSON.parse(localStorage.getItem("anysave.archives") || "{}"); } catch { return {}; } })();
  function remember() {
    try {
      localStorage.setItem("anysave.archives", JSON.stringify({ tab: st.tab, mode: $("arMode").value, policy: $("arPolicy").value,
        dest: $("arDest").value === "pick" ? "next" : $("arDest").value, mkFormat: st.mkFormat, mkLevel: $("arMkLevel").value }));
    } catch { /* private window */ }
  }

  function msg(text, bad) {
    const m = $("arMsg");
    m.hidden = !text;
    m.textContent = text || "";
    m.classList.toggle("err", !!bad);
  }
  function bytes(n) {
    if (n == null || n < 0) return "?";
    if (n < 1024) return n + " B";
    const u = ["KB", "MB", "GB", "TB"];
    let i = -1;
    do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
    return (n < 10 ? n.toFixed(1) : Math.round(n)) + " " + u[i];
  }
  const baseName = (p) => String(p || "").split(/[\\/]/).pop();
  const fmtName = (f) => ({ zip: "ZIP", "7z": "7z", rar: "RAR", tar: "TAR", iso: "ISO", gz: "GZ", bz2: "BZ2", xz: "XZ", zst: "ZST" })[f] || String(f || "").toUpperCase();
  function when(t) {
    const d = new Date(t);
    if (!t || isNaN(d) || d.getFullYear() < 1981) return "";
    return d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) + " " +
      d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  }

  // ---------- jobs (unpack, test, repair, convert, change, make, split, join) ----------
  async function startJob(paths, spec, label, ui) {
    msg("");
    const r = await send({ type: "convert", paths, name: baseName(paths[0]), spec, label });
    if (!r?.ok) throw new Error(r?.error || "Couldn't start.");
    watch(r.id, ui);
    return r.id;
  }
  // watch follows a job: ui = {prog, result, btns: [buttons to disable], done(j), fail(j)}
  function watch(id, ui) {
    const p = $(ui.prog);
    p.hidden = false;
    $(ui.result).hidden = true;
    p.querySelector("i").style.width = "2%";
    p.querySelector("span").textContent = "Starting…";
    const stop = p.querySelector("button");
    if (stop) { stop.disabled = false; stop.onclick = () => { stop.disabled = true; send({ type: "cancel", id }); }; }
    (ui.btns || []).forEach((b) => { $(b).disabled = true; });
    st.job = id;
    const timer = setInterval(() => {
      const j = jobs[id];
      if (!j) return;
      const end = () => { clearInterval(timer); p.hidden = true; st.job = null; (ui.btns || []).forEach((b) => { $(b).disabled = false; }); ui.always?.(); syncButtons(); };
      if (j.status === "done") { end(); ui.done?.(j); }
      else if (j.status === "error" || j.status === "cancelled") {
        end();
        if (j.status === "cancelled") msg("Stopped. Nothing was left half-done.");
        else if (ui.again && st.a && /locked|wrong password/i.test(j.error || "")) {
          // Only the file contents were locked (the list wasn't): ask now, like WinRAR, then try again.
          st.again = ui.again;
          askPassword({ path: st.a.path, name: st.a.name, wrong: /wrong/i.test(j.error) }, true);
        } else { msg(j.error || "Something went wrong.", true); ui.fail?.(j); }
      } else {
        p.querySelector("i").style.width = Math.max(2, j.percent || 0) + "%";
        p.querySelector("span").textContent = `${j.speed || "Working"} · ${Math.round(j.percent || 0)}%${j.eta && j.eta !== "?" ? ` · ${j.eta} left` : ""}`;
      }
    }, 300);
  }
  // result shows what a job made, with buttons: open (folder), show, trash (the archive), look (open it here)
  function result(id, j, opts = {}) {
    const r = $(id);
    r.hidden = false;
    const file = j.file || "";
    r.innerHTML = `<div class="cv-done">✓ ${h(opts.title || "Saved")}${opts.noName ? "" : `: <b>${h(baseName(file))}</b>`}</div>
      ${j.note ? `<div class="small ar-note">${h(j.note)}</div>` : ""}
      <div class="row">${opts.folder ? `<button class="btn" data-a="open">Open folder</button>` : ""}
        ${opts.noShow ? "" : `<button class="btn" data-a="show">${SHOW}</button>`}
        ${opts.look ? `<button class="btn" data-a="look">Open it here</button>` : ""}
        ${opts.trash ? `<button class="btn" data-a="trash" title="You can put it back from the ${TRASH}">Move the archive to the ${TRASH}</button>` : ""}</div>`;
    r.onclick = async (e) => {
      const a = e.target.closest("[data-a]")?.dataset.a;
      if (!a) return;
      try {
        if (a === "open" || a === "show") {
          const x = await cv().op("arcShow", { path: file, how: a });
          if (!x.ok) msg(x.error, true);
        }
        if (a === "look") { tab("open"); openPath(file); }
        if (a === "trash") trashArchive(opts.trash, e.target);
      } catch (err) { msg(err.message, true); }
    };
  }
  async function trashArchive(path, btn) {
    const parts = st.a && st.a.first === path ? st.a.parts : 1;
    if (!confirm(`Move ${baseName(path)}${parts > 1 ? ` and its other ${parts - 1} parts` : ""} to the ${TRASH}?`)) return;
    try {
      const r = await cv().op("arcTrash", { path });
      if (!r.ok) return msg(r.error, true);
      if (btn) { btn.disabled = true; btn.textContent = `Moved to the ${TRASH}`; }
      if (st.a && st.a.first === path) closeArchive();
      msg(`Moved ${r.count > 1 ? r.count + " parts" : baseName(path)} to the ${TRASH}.`);
      loadRecent();
    } catch (e) { msg(e.message, true); }
  }

  // ---------- open an archive ----------
  async function openPath(path, pw) {
    if (!path) return;
    msg("Opening…");
    $("arPw").hidden = true;
    try {
      const r = await cv().op("arcOpen", { path, password: pw || "" });
      if (!r.ok) { msg(r.error, true); return; }
      msg("");
      if (r.needPassword) return askPassword(r);
      st.a = r;
      st.pw = pw || "";
      st.dir = "";
      st.q = "";
      $("arSearch").value = "";
      st.sel.clear();
      st.tree = buildTree(r.entries);
      drawArchive();
    } catch (e) { msg(e.message, true); }
  }
  function askPassword(r, keep) {
    if (!keep) closeArchive(true);
    st.pending = r.path;
    $("arPw").hidden = false;
    $("arPwTitle").textContent = `${r.name} is locked`;
    $("arPwText").textContent = r.wrong ? "That password is wrong. Check it (capital letters count) and try again." : "Type its password to see what's inside.";
    $("arPwText").classList.toggle("err", !!r.wrong);
    $("arPwInput").value = "";
    $("arPwInput").focus();
  }
  $("arPwForm").onsubmit = (e) => {
    e.preventDefault();
    const pw = $("arPwInput").value;
    if (!pw) return $("arPwInput").focus();
    if (st.again && st.a && st.a.path === st.pending) { // the list is open already: try the job again
      st.pw = pw;
      $("arPw").hidden = true;
      const again = st.again;
      st.again = null;
      return again();
    }
    openPath(st.pending, pw);
  };
  $("arPwShow").onchange = (e) => { $("arPwInput").type = e.target.checked ? "text" : "password"; };
  $("arPwCancel").onclick = () => { $("arPw").hidden = true; st.pending = null; st.again = null; drawStart(); };

  function closeArchive(keepMsg) {
    st.a = null;
    st.pw = "";
    st.sel.clear();
    $("arView").hidden = true;
    $("arResult").hidden = true;
    $("arPreview").innerHTML = `<div class="muted small">Click a file to take a quick look at it here (text and pictures). Nothing is saved.</div>`;
    if (!keepMsg) msg("");
    drawStart();
  }
  $("arClose").onclick = () => closeArchive();

  function drawStart() {
    const open = !!st.a || !$("arPw").hidden;
    $("arDrop").classList.toggle("small", open);
    $("arRecentCard").hidden = open || !$("arRecent").children.length;
  }

  // The list as folders: each folder knows its sub-folders and files. Folders that only appear in
  // paths ("a/b/c.txt" with no "a/" entry) are made up here.
  function buildTree(entries) {
    const dirs = new Map([["", { dirs: new Set(), files: [] }]]);
    const dir = (p) => {
      if (!dirs.has(p)) {
        dirs.set(p, { dirs: new Set(), files: [] });
        const i = p.lastIndexOf("/");
        dir(i < 0 ? "" : p.slice(0, i)).dirs.add(p);
      }
      return dirs.get(p);
    };
    const info = new Map();
    for (const e of entries) {
      const name = e.name.replace(/\/+$/, "");
      if (!name) continue;
      if (e.unsafe) { dir("").files.push(e); continue; } // shown as it is, at the top, flagged
      if (e.dir) { dir(name); info.set(name, e); continue; }
      const i = name.lastIndexOf("/");
      dir(i < 0 ? "" : name.slice(0, i)).files.push(e);
    }
    // folder sizes (what's inside)
    const total = new Map();
    const sum = (p) => {
      if (total.has(p)) return total.get(p);
      const d = dirs.get(p);
      let n = 0, c = 0;
      for (const f of d.files) { n += Math.max(0, f.size); c++; }
      for (const s of d.dirs) { const t = sum(s); n += t.size; c += t.count; }
      const t = { size: n, count: c };
      total.set(p, t);
      return t;
    };
    sum("");
    return { dirs, info, total };
  }

  function drawArchive() {
    const a = st.a;
    $("arView").hidden = false;
    drawStart();
    $("arName").textContent = a.name;
    const i = a.info;
    const chips = [fmtName(i.format) + (i.version ? ` (${i.version})` : ""), bytes(i.size),
      `${a.safety.files.toLocaleString()} file${a.safety.files === 1 ? "" : "s"}` + (a.safety.total >= 0 ? `, ${bytes(a.safety.total)} unpacked` : "")];
    if (a.parts > 1) chips.push(`${a.parts} parts`);
    if (i.encrypted) chips.push(i.namesEncrypted ? "Locked, names hidden" : "Locked");
    if (i.solid) chips.push("Solid");
    if (i.recovered) chips.push("Damaged: list rebuilt");
    if (a.copy) chips.push("Dropped copy");
    $("arChips").innerHTML = chips.map((c) => `<span class="ar-chip${/Damaged/.test(c) ? " bad" : /Locked/.test(c) ? " lock" : ""}">${h(c)}</span>`).join("");
    // safety
    const s = a.safety;
    const lines = [];
    if (s.block) lines.push(`<b>AnySave won't unpack this.</b> ${h(s.block)}`);
    for (const w of s.warn || []) lines.push(h(w));
    $("arSafety").hidden = !lines.length;
    $("arSafety").className = "ar-safety" + (s.block ? " bad" : "");
    $("arSafety").innerHTML = lines.map((l) => `<div>${l}</div>`).join("");
    $("arCommentBox").hidden = !i.comment;
    $("arComment").textContent = i.comment || "";
    $("arCommentBox").open = !!i.comment && i.comment.length < 400;
    $("arConvertBox").hidden = true;
    $("arCommentEditBox").hidden = true;
    $("arResult").hidden = true;
    $("arUnpack").disabled = !!s.block;
    destNote();
    syncButtons();
    drawList();
  }
  function syncButtons() {
    const a = st.a;
    if (!a) return;
    const busy = !!st.job;
    const edit = a.info.editable && !a.copy;
    $("arUnpackSel").disabled = busy || !st.sel.size || !!a.safety.block;
    $("arUnpack").disabled = busy || !!a.safety.block;
    for (const b of ["arTest", "arRepair", "arConvertBtn"]) $(b).disabled = busy;
    $("arAdd").disabled = busy || !edit;
    $("arDelete").disabled = busy || !edit || !st.sel.size;
    $("arRename").disabled = busy || !edit || st.sel.size !== 1;
    $("arCommentEdit").disabled = busy || !edit || a.info.format !== "zip";
    $("arTrash").disabled = busy || a.copy;
    const note = $("arEditNote");
    note.hidden = edit;
    note.textContent = a.copy ? "This is a copy of a dropped file, so it can't be changed. Use \"Choose an archive…\" to change the real one."
      : a.parts > 1 ? "Split archives can't be changed. Join the parts first (Split or join)."
      : !["zip", "7z"].includes(a.info.format) ? `${fmtName(a.info.format)} archives can't be changed. Convert it to ZIP or 7z first.` : "";
  }

  // ---------- the file list ----------
  function shown() {
    const t = st.tree;
    if (!t) return [];
    let rows = [];
    if (st.q) {
      const q = st.q.toLowerCase();
      for (const e of st.a.entries) if (!e.dir && e.name.toLowerCase().includes(q)) rows.push({ kind: "file", e, name: e.name, label: e.name });
      if (rows.length > 3000) rows = rows.slice(0, 3000);
    } else {
      const d = t.dirs.get(st.dir) || { dirs: new Set(), files: [] };
      for (const p of d.dirs) {
        const tot = t.total.get(p) || { size: 0, count: 0 };
        rows.push({ kind: "dir", name: p, label: p.slice(p.lastIndexOf("/") + 1), size: tot.size, count: tot.count, e: t.info.get(p) });
      }
      for (const e of d.files) rows.push({ kind: "file", e, name: e.name, label: e.unsafe ? e.name : baseName(e.name) });
    }
    const k = st.sort.key, up = st.sort.up ? 1 : -1;
    const val = (r) => k === "name" ? r.label.toLowerCase() : k === "size" ? (r.kind === "dir" ? r.size : r.e.size)
      : k === "packed" ? (r.e?.packed ?? -1) : new Date(r.e?.mtime || 0).getTime();
    rows.sort((x, y) => (x.kind !== y.kind && !st.q ? (x.kind === "dir" ? -1 : 1) : 0) ||
      (val(x) < val(y) ? -up : val(x) > val(y) ? up : 0));
    return rows;
  }
  function drawList() {
    const rows = shown();
    st.rows = rows;
    // breadcrumb
    const parts = st.dir ? st.dir.split("/") : [];
    $("arCrumbs").innerHTML = (st.q ? `<span class="muted">Search results</span>` :
      `<button data-dir="">${h(st.a.name)}</button>` + parts.map((p, i) => `<span>›</span><button data-dir="${h(parts.slice(0, i + 1).join("/"))}">${h(p)}</button>`).join(""));
    $("arList").innerHTML = rows.length ? rows.map((r, i) => {
      const e = r.e || {};
      const flags = [e.enc ? `<span class="ar-flag" title="Locked">🔒</span>` : "", e.unsafe ? `<span class="ar-flag bad" title="${h(e.unsafe)}">unsafe, won't be unpacked</span>` : "",
        e.link ? `<span class="ar-flag" title="A link to ${h(e.link)}">link, not unpacked</span>` : ""].join("");
      return `<div class="ar-row${st.sel.has(r.name) ? " on" : ""}" role="row" data-i="${i}">
        <span><input type="checkbox" ${st.sel.has(r.name) ? "checked" : ""} aria-label="Tick ${h(r.label)}" /></span>
        <span class="ar-nm ${r.kind}" title="${h(r.name)}"><i class="ar-ic">${r.kind === "dir" ? "📁" : icon(r.name)}</i><b>${h(r.label)}</b>${flags}</span>
        <span>${r.kind === "dir" ? `${bytes(r.size)} <small class="muted">(${r.count.toLocaleString()})</small>` : bytes(e.size)}</span>
        <span class="muted">${r.kind === "dir" || e.packed < 0 ? "" : bytes(e.packed)}</span>
        <span class="muted">${when(e.mtime)}</span></div>`;
    }).join("") : `<div class="muted small ar-empty">${st.q ? "No names match." : "This folder is empty."}</div>`;
    const total = st.a.safety.files;
    $("arCount").textContent = (st.sel.size ? `${st.sel.size} ticked · ` : "") +
      (st.q ? `${rows.length.toLocaleString()} match${rows.length === 1 ? "" : "es"}` : `${total.toLocaleString()} files in all`) +
      (st.a.more ? ` · the list shows the first ${st.a.entries.length.toLocaleString()} (unpacking still gets everything)` : "");
    $("arAll").checked = rows.length > 0 && rows.every((r) => st.sel.has(r.name));
    syncButtons();
  }
  function icon(n) {
    if (IMG_RE.test(n)) return "🖼️";
    if (AUDIO_RE.test(n)) return "🎵";
    if (VIDEO_RE.test(n) || /\.(mkv|avi)$/i.test(n)) return "🎬";
    if (ARCHIVE_RE.test(n)) return "🗜️";
    if (/\.(exe|msi|bat|cmd|com|scr|ps1|vbs|jar|app|pkg|dmg|apk|sh|command)$/i.test(n)) return "⚙️";
    if (/\.pdf$/i.test(n)) return "📕";
    return "📄";
  }
  $("arCrumbs").onclick = (e) => {
    const b = e.target.closest("[data-dir]");
    if (!b) return;
    st.dir = b.dataset.dir;
    drawList();
  };
  $("arList").addEventListener("click", (e) => {
    const row = e.target.closest(".ar-row");
    if (!row) return;
    const r = st.rows[+row.dataset.i];
    if (e.target.matches("input[type=checkbox]")) {
      e.target.checked ? st.sel.add(r.name) : st.sel.delete(r.name);
      row.classList.toggle("on", e.target.checked);
      return drawList();
    }
    if (r.kind === "dir") { st.dir = r.name; st.q = ""; $("arSearch").value = ""; return drawList(); }
    $("arList").querySelectorAll(".ar-row.look").forEach((x) => x.classList.remove("look"));
    row.classList.add("look");
    preview(r.e);
  });
  $("arAll").onchange = (e) => {
    for (const r of st.rows || []) e.target.checked ? st.sel.add(r.name) : st.sel.delete(r.name);
    drawList();
  };
  document.querySelectorAll("#archives [data-sort]").forEach((b) => {
    b.onclick = () => {
      st.sort = { key: b.dataset.sort, up: st.sort.key === b.dataset.sort ? !st.sort.up : b.dataset.sort === "name" };
      drawList();
    };
  });
  let searchT;
  $("arSearch").oninput = () => { clearTimeout(searchT); searchT = setTimeout(() => { st.q = $("arSearch").value.trim(); drawList(); }, 150); };

  let lookSeq = 0;
  async function preview(e) {
    const box = $("arPreview");
    const seq = ++lookSeq;
    if (e.link || e.unsafe || e.special) { box.innerHTML = `<div class="muted small">There's nothing to show for this item.</div>`; return; }
    box.innerHTML = `<div class="muted small"><span class="spin"></span> Opening ${h(baseName(e.name))}…</div>`;
    try {
      const r = await cv().op("arcPreview", { path: st.a.path, password: st.pw, i: e.i });
      if (seq !== lookSeq) return;
      if (!r.ok) { box.innerHTML = `<div class="small err">${h(r.error)}</div>`; return; }
      const base = (await cv().session()).base;
      const head = `<div class="ar-look-head"><b title="${h(e.name)}">${h(r.name)}</b><span class="muted small">${bytes(r.size)}</span></div>`;
      if (r.text != null) box.innerHTML = head + `<pre class="ar-text"></pre>${r.cut ? `<div class="hint">Showing the start only.</div>` : ""}`;
      else if (IMG_RE.test(r.name)) box.innerHTML = head + `<img class="ar-img" alt="" src="${h(base + r.url)}" />`;
      else if (AUDIO_RE.test(r.name)) box.innerHTML = head + `<audio controls src="${h(base + r.url)}"></audio>`;
      else if (VIDEO_RE.test(r.name)) box.innerHTML = head + `<video controls class="ar-img" src="${h(base + r.url)}"></video>`;
      else if (/\.pdf$/i.test(r.name)) box.innerHTML = head + `<iframe class="ar-pdf" src="${h(base + r.url)}" title="PDF preview"></iframe>`;
      else box.innerHTML = head + `<div class="muted small">No quick look for this kind of file. Tick it and press "Unpack selected" to save it.</div>`;
      if (r.text != null) box.querySelector("pre").textContent = r.text;
    } catch (err) { if (seq === lookSeq) box.innerHTML = `<div class="small err">${h(err.message)}</div>`; }
  }

  // ---------- unpack ----------
  function destNote() {
    const d = $("arDest").value;
    const where = !st.a ? "" : d === "pick" ? (st.destPath ? `Unpacks into ${st.destPath}` : "Choose a folder.")
      : d === "downloads" || st.a.copy ? "Unpacks into Downloads/AnySave/Unpacked" + (st.a.copy && d !== "downloads" ? " (a dropped file is only a copy; use \"Choose an archive…\" to unpack next to the real one)" : "")
      : `Unpacks next to the archive, in ${st.a.folder}`;
    $("arDestNote").textContent = where;
  }
  $("arDest").onchange = async () => {
    if ($("arDest").value === "pick") {
      try {
        const r = await cv().op("pickFolder", { prompt: "Where should the files go?" });
        if (r.ok && r.path) st.destPath = r.path;
        else if (!st.destPath) $("arDest").value = "next";
        if (!r.ok) msg(r.error, true);
      } catch (e) { msg(e.message, true); }
    }
    destNote();
    remember();
  };
  $("arMode").onchange = remember;
  $("arPolicy").onchange = remember;
  function unpackOpts(extra = {}) {
    const d = $("arDest").value;
    return { password: st.pw, dest: d === "pick" ? st.destPath || "next" : d, mode: $("arMode").value, policy: $("arPolicy").value,
      keepJunk: $("arJunk").checked ? "1" : "", recover: $("arRecover").checked ? "1" : "", ...extra };
  }
  const ARC_UI = { prog: "arProg", result: "arResult" };
  async function unpack(only) {
    if (!st.a) return;
    const a = st.a;
    const extra = {};
    let label = "Unpack";
    if (only) {
      extra.only = JSON.stringify(only);
      if (!st.q && st.dir) extra.base = st.dir;
      label = `Unpack ${only.length} item${only.length === 1 ? "" : "s"}`;
    }
    try {
      await startJob([a.path], { tool: "unpack", opts: unpackOpts(extra) }, label, {
        ...ARC_UI, again: () => unpack(only),
        done: (j) => result("arResult", j, { title: "Unpacked", folder: true, trash: !a.copy && !only ? a.first : "" }),
      });
    } catch (e) { msg(e.message, true); }
  }
  $("arUnpack").onclick = () => unpack(null);
  $("arUnpackSel").onclick = () => unpack([...st.sel]);
  $("arTest").onclick = async () => {
    try {
      await startJob([st.a.path], { tool: "arc-test", opts: { password: st.pw } }, "Test archive", {
        ...ARC_UI, again: () => $("arTest").click(),
        done: (j) => {
          const bad = /damaged/i.test(j.note || "");
          const r = $("arResult");
          r.hidden = false;
          r.innerHTML = `<div class="cv-done${bad ? " bad" : ""}">${bad ? "✗ Problems found" : "✓ No problems found"}</div><div class="small ar-note">${h(j.note || "")}</div>
            ${bad ? `<div class="small">Try <b>Repair</b> to save what can still be read.</div>` : ""}`;
          r.onclick = null;
        },
      });
    } catch (e) { msg(e.message, true); }
  };
  $("arRepair").onclick = async () => {
    const a = st.a;
    const zip = a.info.format === "zip";
    try {
      await startJob([a.path], { tool: "arc-repair", opts: unpackOpts({ mode: "folder", name: "" }) }, zip ? "Repair ZIP" : "Recover files", {
        ...ARC_UI,
        done: (j) => result("arResult", j, zip ? { title: "Repaired copy saved", look: true } : { title: "Saved what could be read", folder: true }),
      });
    } catch (e) { msg(e.message, true); }
  };
  $("arConvertBtn").onclick = () => { $("arConvertBox").hidden = !$("arConvertBox").hidden; $("arCommentEditBox").hidden = true; };
  $("arConvGo").onclick = async () => {
    const f = $("arConvFormat").value;
    try {
      const d = $("arDest").value;
      await startJob([st.a.path], { tool: "arc-convert", format: f, opts: { password: st.pw, newPassword: $("arConvPw").value,
        encryptNames: f === "7z" && $("arConvPw").value ? "1" : "", dest: d === "pick" ? st.destPath || "next" : d } }, `Convert to ${fmtName(f)}`, {
        ...ARC_UI, again: () => $("arConvGo").click(),
        done: (j) => { $("arConvertBox").hidden = true; $("arConvPw").value = ""; result("arResult", j, { title: "Converted", look: true }); },
      });
    } catch (e) { msg(e.message, true); }
  };

  // ---------- change a ZIP / 7z ----------
  function afterEdit(j) {
    result("arResult", j, { title: "Archive updated", noName: true, noShow: true });
    const keep = st.pw;
    openPath(st.a.path, keep).then(() => { if (j.note) $("arResult").hidden = false; });
  }
  $("arAdd").onclick = async () => {
    try {
      const r = await cv().op("pick", { multi: true });
      if (!r.ok) return msg(r.error, true);
      const files = (r.files || []).map((f) => f.path);
      if (!files.length) return;
      await startJob([st.a.path, ...files], { tool: "arc-edit", opts: { password: st.pw, addTo: st.q ? "" : st.dir } },
        `Add ${files.length} file${files.length === 1 ? "" : "s"}`, { ...ARC_UI, done: afterEdit });
    } catch (e) { msg(e.message, true); }
  };
  $("arDelete").onclick = async () => {
    const del = [...st.sel];
    if (!del.length) return;
    if (!confirm(`Delete ${del.length === 1 ? `"${baseName(del[0])}"` : del.length + " items"} from the archive? This can't be undone.`)) return;
    try {
      await startJob([st.a.path], { tool: "arc-edit", opts: { password: st.pw, delete: JSON.stringify(del) } },
        `Delete ${del.length} item${del.length === 1 ? "" : "s"}`, { ...ARC_UI, done: (j) => { st.sel.clear(); afterEdit(j); } });
    } catch (e) { msg(e.message, true); }
  };
  $("arRename").onclick = async () => {
    const [from] = [...st.sel];
    if (!from) return;
    const i = from.lastIndexOf("/");
    const dir = i < 0 ? "" : from.slice(0, i + 1);
    const name = prompt("New name:", from.slice(i + 1));
    if (!name || name === from.slice(i + 1)) return;
    if (/[\\/]/.test(name)) return msg("A name can't contain / or \\.", true);
    try {
      await startJob([st.a.path], { tool: "arc-edit", opts: { password: st.pw, rename: JSON.stringify({ [from]: dir + name }) } },
        "Rename inside archive", { ...ARC_UI, done: (j) => { st.sel.clear(); afterEdit(j); } });
    } catch (e) { msg(e.message, true); }
  };
  $("arCommentEdit").onclick = () => {
    $("arCommentEditBox").hidden = !$("arCommentEditBox").hidden;
    $("arConvertBox").hidden = true;
    $("arCommentText").value = st.a.info.comment || "";
  };
  $("arCommentCancel").onclick = () => { $("arCommentEditBox").hidden = true; };
  $("arCommentSave").onclick = async () => {
    try {
      await startJob([st.a.path], { tool: "arc-edit", opts: { password: st.pw, setComment: "1", comment: $("arCommentText").value } },
        "Archive comment", { ...ARC_UI, done: (j) => { $("arCommentEditBox").hidden = true; afterEdit(j); } });
    } catch (e) { msg(e.message, true); }
  };
  $("arTrash").onclick = () => trashArchive(st.a.first, null);

  // ---------- recent ----------
  async function loadRecent() {
    try {
      const r = await cv().op("arcHistory");
      if (!r.ok) return;
      drawRecent(r.items);
    } catch { /* no helper yet */ }
  }
  function drawRecent(items) {
    $("arRecent").innerHTML = items.slice(0, 12).map((x) => `<li data-p="${h(x.path)}"><span class="ar-ic">🗜️</span>
      <span class="cv-fname"><b>${h(x.name)}</b><span class="muted small">${h(fmtName(x.format))} · ${bytes(x.size)} · ${h(x.path.replace(/[\\/][^\\/]*$/, ""))}</span></span>
      <button class="btn small" data-forget title="Take it off this list (the file stays)">✕</button></li>`).join("");
    drawStart();
  }
  $("arRecent").onclick = async (e) => {
    const li = e.target.closest("li");
    if (!li) return;
    if (e.target.closest("[data-forget]")) {
      const r = await cv().op("arcForget", { path: li.dataset.p });
      if (r.ok) drawRecent(r.items);
      return;
    }
    openPath(li.dataset.p);
  };
  $("arRecentClear").onclick = async () => {
    const r = await cv().op("arcForget", {});
    if (r.ok) drawRecent(r.items);
  };

  // ---------- make an archive ----------
  function drawMake() {
    $("arMkItems").innerHTML = st.mk.map((f, i) => `<li><span class="ar-ic">${f.dir ? "📁" : icon(f.name)}</span>
      <span class="cv-fname"><b>${h(f.name)}</b><span class="muted small">${f.dir ? "Folder" : bytes(f.size)}${f.copy ? " · dropped copy" : ""}</span></span>
      <button class="btn small" data-rm="${i}" title="Take it out">✕</button></li>`).join("");
    $("arMkEmpty").hidden = !!st.mk.length;
    $("arMkGo").disabled = !st.mk.length || !!st.mkJob;
    if (!$("arMkName").dataset.touched) {
      $("arMkName").value = st.mk.length === 1 ? st.mk[0].name.replace(/\.[^.]+$/, "") : st.mk.length ? "Archive" : "";
    }
    const z = st.mkFormat === "zip";
    document.querySelectorAll("#arMkFormat .chip").forEach((c) => c.classList.toggle("active", c.dataset.f === st.mkFormat));
    const pw = !!$("arMkPw").value;
    $("arMkPw2Row").hidden = !pw;
    $("arMkNamesRow").hidden = z || !pw;
    $("arMkSolidRow").hidden = z;
    $("arMkSplitRow").hidden = $("arMkSplit").value !== "custom";
    $("arMkFormatNote").textContent = z
      ? (pw ? "A locked ZIP opens in AnySave, 7-Zip, WinRAR and Keka. The Mac's built-in Archive Utility can't open locked ZIPs." : "ZIP opens on every computer without extra apps.")
      : "7z is usually smaller. Windows 11 and the Mac need an app such as AnySave, 7-Zip or Keka to open it.";
  }
  function addItems(files, copy) {
    for (const f of files) if (!st.mk.some((x) => x.path === f.path)) st.mk.push({ ...f, copy });
    drawMake();
  }
  $("arMkItems").onclick = (e) => {
    const b = e.target.closest("[data-rm]");
    if (b) { st.mk.splice(+b.dataset.rm, 1); drawMake(); }
  };
  $("arMkFiles").onclick = async () => {
    try {
      const r = await cv().op("pick", { multi: true });
      if (!r.ok) return msg(r.error, true);
      addItems(r.files || []);
    } catch (e) { msg(e.message, true); }
  };
  $("arMkFolder").onclick = async () => {
    try {
      const r = await cv().op("pickFolder", { prompt: "Choose a folder to pack" });
      if (!r.ok) return msg(r.error, true);
      if (r.path) addItems([{ path: r.path, name: baseName(r.path), dir: true }]);
    } catch (e) { msg(e.message, true); }
  };
  $("arMkName").oninput = () => { $("arMkName").dataset.touched = "1"; };
  $("arMkFormat").onclick = (e) => {
    const c = e.target.closest("[data-f]");
    if (c) { st.mkFormat = c.dataset.f; drawMake(); remember(); }
  };
  for (const id of ["arMkPw", "arMkSplit"]) $(id).addEventListener("input", drawMake);
  $("arMkLevel").onchange = remember;
  $("arMkDest").onchange = async () => {
    if ($("arMkDest").value !== "pick") return;
    try {
      const r = await cv().op("pickFolder", { prompt: "Where should the archive go?" });
      if (r.ok && r.path) st.mkDestPath = r.path;
      else if (!st.mkDestPath) $("arMkDest").value = "next";
    } catch (e) { msg(e.message, true); }
  };
  $("arMkGo").onclick = async () => {
    msg("");
    const pw = $("arMkPw").value;
    if (pw && pw !== $("arMkPw2").value) return msg("The two passwords don't match.", true);
    let split = $("arMkSplit").value;
    if (split === "custom") split = String(Math.max(1, Math.round(+$("arMkSplitMb").value || 0)));
    const z = st.mkFormat === "zip";
    const d = $("arMkDest").value;
    const name = $("arMkName").value.trim() || "Archive";
    const opts = { name, level: $("arMkLevel").value, password: pw, encryptNames: !z && pw && $("arMkNames").checked ? "1" : "",
      solid: !z && $("arMkSolid").checked ? "1" : "", splitMb: split === "0" ? "" : split, comment: $("arMkComment").value,
      dest: d === "pick" ? st.mkDestPath || "next" : d, keepJunk: $("arMkJunk").checked ? "1" : "" };
    try {
      st.mkJob = true;
      $("arMkGo").disabled = true;
      await startJob(st.mk.map((f) => f.path), { tool: "pack", format: st.mkFormat, opts }, `Make ${fmtName(st.mkFormat)}${pw ? " (locked)" : ""}`, {
        prog: "arMkProg", result: "arMkResult",
        always: () => { st.mkJob = false; drawMake(); },
        done: (j) => {
          $("arMkPw").value = ""; $("arMkPw2").value = "";
          drawMake();
          result("arMkResult", j, { title: "Made", look: true });
        },
      });
    } catch (e) { st.mkJob = false; drawMake(); msg(e.message, true); }
  };

  // ---------- split / join ----------
  async function pickOne(set, nameId, goId, test, bad) {
    try {
      const r = await cv().op("pick", { multi: false });
      if (!r.ok) return msg(r.error, true);
      const f = r.files?.[0];
      if (!f) return;
      if (test && !test(f.name)) return msg(bad, true);
      msg("");
      st[set] = f;
      $(nameId).textContent = `${f.name} (${bytes(f.size)})`;
      $(goId).disabled = false;
    } catch (e) { msg(e.message, true); }
  }
  $("arSpPick").onclick = () => pickOne("spFile", "arSpName", "arSpGo");
  $("arJnPick").onclick = () => pickOne("jnFile", "arJnName", "arJnGo", (n) => /\.(\d{3}|z\d{2}|zip|rar)$/i.test(n),
    "Choose a part of a split set: a file ending in .001, .z01 or .part1.rar.");
  $("arSpGo").onclick = async () => {
    try {
      await startJob([st.spFile.path], { tool: "arc-split", opts: { splitMb: $("arSpSize").value } }, "Split file",
        { prog: "arSpProg", result: "arSpResult", btns: ["arSpGo"], done: (j) => result("arSpResult", j, { title: "Split" }) });
    } catch (e) { msg(e.message, true); }
  };
  $("arJnGo").onclick = async () => {
    try {
      await startJob([st.jnFile.path], { tool: "arc-join", opts: { dest: "next" } }, "Join parts",
        { prog: "arJnProg", result: "arJnResult", btns: ["arJnGo"], done: (j) => result("arJnResult", j, { title: "Joined", look: ARCHIVE_RE.test(j.file || "") }) });
    } catch (e) { msg(e.message, true); }
  };

  // ---------- tabs, picking, dropping ----------
  function tab(name) {
    st.tab = name;
    document.querySelectorAll("[data-artab]").forEach((b) => b.classList.toggle("active", b.dataset.artab === name));
    $("arOpenPane").hidden = name !== "open";
    $("arMakePane").hidden = name !== "make";
    $("arSplitPane").hidden = name !== "split";
    msg("");
    remember();
  }
  document.querySelectorAll("[data-artab]").forEach((b) => { b.onclick = () => tab(b.dataset.artab); });
  $("arPick").onclick = async () => {
    try {
      const r = await cv().op("pick", { multi: false });
      if (!r.ok) return msg(r.error, true);
      if (r.files?.[0]) openPath(r.files[0].path);
    } catch (e) { msg(e.message, true); }
  };
  $("arFromDl").onchange = (e) => {
    const j = jobs[e.target.value];
    e.target.value = "";
    if (j) openPath(j.file);
  };
  function fillPicker() {
    const done = Object.values(jobs).filter((j) => j.status === "done" && j.file && ARCHIVE_RE.test(j.file))
      .sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0)).slice(0, 200);
    $("arFromDl").innerHTML = '<option value="">…or one of your downloads</option>' + done.map((j) => `<option value="${h(j.id)}">${h(displayName(j))}</option>`).join("");
    $("arFromDl").hidden = !done.length;
  }

  const page = $("archives");
  let depth = 0;
  const hasFiles = (e) => [...(e.dataTransfer?.types || [])].includes("Files");
  page.addEventListener("dragenter", (e) => { if (hasFiles(e)) { e.preventDefault(); depth++; page.classList.add("dragging"); } });
  page.addEventListener("dragover", (e) => { if (hasFiles(e)) e.preventDefault(); });
  page.addEventListener("dragleave", () => { if (--depth <= 0) { depth = 0; page.classList.remove("dragging"); } });
  page.addEventListener("drop", async (e) => {
    if (!e.dataTransfer?.files?.length) return;
    e.preventDefault(); e.stopPropagation();
    depth = 0; page.classList.remove("dragging");
    const items = [...e.dataTransfer.items || []];
    const files = [...e.dataTransfer.files].filter((f, i) => !items[i]?.webkitGetAsEntry?.()?.isDirectory);
    if (files.length < e.dataTransfer.files.length) msg("Folders can't be dropped here. Use \"Add a folder…\" instead.", true);
    if (!files.length) return;
    const toMake = st.tab === "make" || (files.length > 1 && !files.every((f) => ARCHIVE_RE.test(f.name))) || !ARCHIVE_RE.test(files[0].name);
    try {
      if (toMake) {
        tab("make");
        const up = [];
        for (const f of files) {
          msg(`Adding ${f.name}…`);
          const r = await cv().upload(f);
          if (!r.ok) return msg(r.error, true);
          up.push(r.file);
        }
        msg("");
        return addItems(up, true);
      }
      tab("open");
      msg(`Reading ${files[0].name}…`);
      const r = await cv().upload(files[0]);
      if (!r.ok) return msg(r.error, true);
      openPath(r.file.path);
    } catch (err) { msg(err.message, true); }
  });

  // ---------- from other pages ----------
  window.showArchives = () => {
    fillPicker();
    cv()?.session().then(loadRecent).catch((e) => msg(e.message, true));
  };
  window.openArchive = (path) => { // "Open in Archives" on a finished download
    showTab("archives");
    tab("open");
    openPath(path);
  };
  window.unpackDownload = async (j) => { // "Unpack here" on a finished download
    try {
      const r = await send({ type: "convert", paths: [j.file], name: baseName(j.file), label: "Unpack",
        spec: { tool: "unpack", opts: { dest: "next", mode: "smart" } } });
      if (!r?.ok) throw new Error(r?.error || "Couldn't start.");
      toast("Unpacking next to the file…");
    } catch (e) { toast(e.message, true); }
  };

  // ---------- start ----------
  if (saved.mode) $("arMode").value = saved.mode;
  if (saved.policy) $("arPolicy").value = saved.policy;
  if (saved.dest) $("arDest").value = saved.dest;
  if (saved.mkFormat) st.mkFormat = saved.mkFormat;
  if (saved.mkLevel) $("arMkLevel").value = saved.mkLevel;
  drawMake();
  tab(["open", "make", "split"].includes(saved.tab) ? saved.tab : "open");
  window.__archives = { st, openPath };
  if (location.hash === "#archives") window.showArchives();
})();
