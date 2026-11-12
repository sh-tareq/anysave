// Records Alt-click (use Chrome for this download) and Ctrl-click (force AnySave fast engine)
// on links so the service worker knows the user's choice when the download starts.
(() => {
  if (window.__vgClickKeys) return;
  window.__vgClickKeys = true;
  const record = (e) => {
    if (!e.altKey && !e.ctrlKey && !e.metaKey) return;
    const a = e.target?.closest?.("a[href]");
    if (!a?.href || !/^https?:/.test(a.href)) return;
    try {
      chrome.runtime.sendMessage({
        type: "clickModifier",
        url: a.href,
        altKey: !!e.altKey,
        ctrlKey: !!(e.ctrlKey || e.metaKey),
      });
    } catch {}
  };
  window.addEventListener("click", record, true);
  window.addEventListener("auxclick", record, true);
})();

