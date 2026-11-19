// "Recent work" on the Tools pages (Convert, Captions, Voice Lab, Archives): what each tool made, with
// Open, Show in folder, Remove from list (the file stays) and Move to Trash, plus "Clear list".
// The items are the same jobs as in the downloads list (kind "convert" / "subs"), sorted by tool.
(() => {
  "use strict";
  const cv = () => window.AnySaveCv;
  const isMac = /Mac/i.test(navigator.platform || navigator.userAgent);
  const TRASH = isMac ? "Trash" : "Recycle Bin";
  const SHOW = isMac ? "Show in Finder" : "Show in folder";
  const ARCHIVE = new Set(["unpack", "pack", "arc-test", "arc-repair", "arc-convert", "arc-edit", "arc-split", "arc-join"]);
  const VOICE = new Set(["voiceclean", "speak"]);
  const CAPTIONS = new Set(["captions", "dub"]);
  const HIDDEN = new Set(["translate", "arc-test"]); // they make nothing to keep (a test only reports)
  const tool = (j) => j.spec?.tool || "media";
  const PAGES = {
    convert: (j) => j.kind === "convert" && !ARCHIVE.has(tool(j)) && !VOICE.has(tool(j)) && !CAPTIONS.has(tool(j)) && !HIDDEN.has(tool(j)),
    captions: (j) => j.kind === "subs" || (j.kind === "convert" && CAPTIONS.has(tool(j))),
    voice: (j) => j.kind === "convert" && VOICE.has(tool(j)),
    archives: (j) => j.kind === "convert" && ARCHIVE.has(tool(j)) && !HIDDEN.has(tool(j)),
  };
  const FINISHED = ["done", "error", "cancelled"];
  const base = (p) => String(p || "").split(/[\\/]/).pop();

  function items(page) {
    return Object.values(jobs).filter((j) => FINISHED.includes(j.status) && PAGES[page](j))
      .sort((a, b) => (b.finishedAt || b.createdAt || 0) - (a.finishedAt || a.createdAt || 0));
  }
  const shown = {}; // page -> how many rows are shown
  function draw(page) {
    const box = document.getElementById("work-" + page);
    if (!box) return;
    const list = items(page);
    box.hidden = !list.length;
    if (!list.length) return;
    const n = shown[page] || 8;
    box.innerHTML = `<div class="cv-head"><h3>Recent work <span class="muted small">${list.length}</span></h3>
        <button class="btn small" data-w="clear" title="Take everything off this list. The files stay on your computer.">Clear list</button></div>
      <ul class="work-list">${list.slice(0, n).map((j) => {
        const ok = j.status === "done";
        const what = [j.qualityLabel, ago(j.finishedAt || j.createdAt)].filter(Boolean).join(" · ");
        const note = ok ? j.note || "" : j.status === "cancelled" ? "Stopped" : j.error || "Failed";
        return `<li data-id="${esc(j.id)}" class="${ok ? "" : "bad"}">
          <span class="work-nm"><b title="${esc(j.file || j.title || "")}">${esc(ok && j.file ? base(j.file) : j.title || base(j.url) || "Untitled")}</b>
            <span class="muted small">${esc(what)}${note ? ` · <span class="${ok ? "" : "err"}">${esc(note)}</span>` : ""}</span></span>
          <span class="work-btns">
            ${ok && j.file ? `<button class="ib" data-w="open" title="Open" aria-label="Open">${svg("open", 17)}</button>
              <button class="ib" data-w="show" title="${SHOW}" aria-label="${SHOW}">${svg("folder", 17)}</button>
              <button class="ib" data-w="trash" title="Move to the ${TRASH}" aria-label="Move to the ${TRASH}">${svg("remove", 17)}</button>` : ""}
            <button class="ib" data-w="remove" title="Remove from this list (the file stays)" aria-label="Remove from this list">${svg("close", 17)}</button>
          </span></li>`;
      }).join("")}</ul>
      ${list.length > n ? `<button class="btn small work-more" data-w="more">Show ${Math.min(20, list.length - n)} more</button>` : ""}`;
  }
  function drawAll() {
    if (typeof view === "undefined" || !PAGES[view]) return; // only the page on screen
    draw(view);
  }
  const removeJobs = (ids) => send({ type: "remove", ids });

  async function act(page, a, j) {
    try {
      if (a === "more") { shown[page] = (shown[page] || 8) + 20; return draw(page); }
      if (a === "clear") {
        const list = items(page);
        if (!confirm(`Take ${list.length === 1 ? "this item" : `all ${list.length} items`} off the list? The files stay on your computer.`)) return;
        await removeJobs(list.map((x) => x.id));
        return toast("List cleared. The files are still on your computer.");
      }
      if (a === "remove") return removeJobs([j.id]);
      if (a === "open") { // the downloads list's own Open first (files and folders in Downloads/AnySave)
        const x = await send({ type: "open", id: j.id });
        if (x?.ok) return;
        const r = await cv().op("arcShow", { path: j.file, how: "open" }); // a folder somewhere else
        if (!r.ok) toast(r.error || x?.error || "Couldn't find it. It may have been moved or deleted.", true);
        return;
      }
      if (a === "show") {
        const r = await cv().op("arcShow", { path: j.file, how: "show" });
        if (r.ok) return;
        const x = await send({ type: "reveal", id: j.id });
        if (x && !x.ok) toast(r.error || "Couldn't find it. It may have been moved or deleted.", true);
        return;
      }
      if (a === "trash") {
        const parts = /\.0*1$/.test(j.file) ? " and its other parts" : "";
        if (!confirm(`Move "${base(j.file)}"${parts} to the ${TRASH}? You can put ${parts ? "them" : "it"} back from the ${TRASH}.`)) return;
        const r = await cv().op("trashPaths", { paths: [j.file] });
        if (!r.ok) return toast(r.error, true);
        await removeJobs([j.id]);
        toast(r.count ? `Moved to the ${TRASH}.` : "It was already gone, so it's off the list now.");
      }
    } catch (e) { toast(e.message, true); }
  }

  for (const page of Object.keys(PAGES)) {
    const sec = document.getElementById(page);
    if (!sec) continue;
    const box = document.createElement("div");
    box.id = "work-" + page;
    box.className = "card work";
    box.hidden = true;
    sec.appendChild(box);
    box.addEventListener("click", (e) => {
      const b = e.target.closest("[data-w]");
      if (!b) return;
      const j = jobs[b.closest("li")?.dataset.id];
      act(page, b.dataset.w, j);
    });
  }
  // Redraw when the jobs change (manager.js sends this after every update) and when a page opens.
  document.addEventListener("anysave-jobs", drawAll);
  window.drawRecentWork = drawAll;
})();
