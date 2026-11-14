// AnySave Desktop Bridge (Phase 2)
// Polyfills chrome.runtime, chrome.storage.local, and chrome.downloads when manager.html
// runs inside the standalone AnySave Desktop App (macOS WKWebView / Windows App Window),
// and adds Desktop-App-specific controls (Clipboard Watch, Floating Drop Box, Global Shortcut,
// Drag-and-Drop links/files, and Self-Update).
(() => {
  if (window.chrome && window.chrome.runtime && window.chrome.runtime.id) {
    return; // Running inside the Chrome/Edge/Brave extension
  }

  const params = new URLSearchParams(location.search);
  const token = params.get("token") || window.__ANYSAVE_TOKEN || localStorage.getItem("anysave_token") || "";
  if (token) {
    try { localStorage.setItem("anysave_token", token); } catch {}
  }

  const headers = () => ({
    "Content-Type": "application/json",
    "X-AnySave-Token": token,
  });

  const listeners = new Set();
  let appVersion = ""; // the helper's version, from the first state message
  const store = {
    jobs: {},
    settings: {
      captureAll: true,
      downloadHandler: "anysave",
      minSizeMb: 0,
      excludedSites: "",
      fileTypes: "",
      loginFastSites: false,
      clipboardWatch: false,
      dropBox: false,
      notifications: true,
      releaseChannel: "stable",
    },
    acceptedTermsVersion: 2,
    seenAt: Date.now(),
  };

  function emitStorageChange(changes) {
    for (const fn of listeners) {
      try { fn(changes, "local"); } catch (e) { console.error(e); }
    }
  }

  async function apiAction(payload) {
    try {
      const r = await fetch("/api/action", {
        method: "POST",
        headers: headers(),
        body: JSON.stringify(payload),
      });
      return await r.json();
    } catch (e) {
      return { ok: false, error: String(e.message || e) };
    }
  }

  // Initial state fetch + live Server-Sent Events stream from the Go daemon
  async function connectDaemon() {
    try {
      const r = await fetch("/api/state", { headers: headers() });
      if (r.ok) {
        const data = await r.json();
        const oldJobs = store.jobs;
        const oldSettings = store.settings;
        store.jobs = data.jobs || {};
        store.settings = { ...store.settings, ...(data.settings || {}) };
        emitStorageChange({
          jobs: { oldValue: oldJobs, newValue: store.jobs },
          settings: { oldValue: oldSettings, newValue: store.settings },
        });
        syncDesktopBar(store.settings, data.version, data.ytdlp);
      }
    } catch {}

    const es = new EventSource(`/api/events?token=${encodeURIComponent(token)}`);
    es.onmessage = (ev) => {
      try {
        const data = JSON.parse(ev.data);
        const changes = {};
        if (data.jobs) {
          changes.jobs = { oldValue: store.jobs, newValue: data.jobs };
          store.jobs = data.jobs;
        }
        if (data.settings) {
          changes.settings = { oldValue: store.settings, newValue: data.settings };
          store.settings = { ...store.settings, ...data.settings };
          syncDesktopBar(store.settings, data.version, data.ytdlp);
        }
        if (Object.keys(changes).length) emitStorageChange(changes);
      } catch {}
    };
    es.onerror = () => {
      es.close();
      setTimeout(connectDaemon, 1500);
    };
  }

  window.chrome = {
    runtime: {
      getManifest: () => ({ version: appVersion }),
      getURL: (p) => "/" + String(p).replace(/^\//, ""),
      sendMessage: (msg) => apiAction(msg),
    },
    storage: {
      local: {
        get: async (keys) => {
          if (!keys) return { ...store };
          if (typeof keys === "string") return { [keys]: store[keys] };
          if (Array.isArray(keys)) {
            const out = {};
            for (const k of keys) out[k] = store[k];
            return out;
          }
          const out = { ...keys };
          for (const k of Object.keys(keys)) {
            if (store[k] !== undefined) out[k] = store[k];
          }
          return out;
        },
        set: async (patch) => {
          const changes = {};
          for (const [k, v] of Object.entries(patch)) {
            changes[k] = { oldValue: store[k], newValue: v };
            store[k] = v;
          }
          if (patch.settings) {
            await apiAction({ type: "setSettings", patch: patch.settings });
          }
          emitStorageChange(changes);
        },
      },
      onChanged: {
        addListener: (fn) => listeners.add(fn),
      },
    },
    downloads: {
      open: async () => { throw new Error("use helper open"); },
      drag: () => {},
      show: () => {},
    },
    permissions: {
      contains: async () => true,
      request: async () => true,
    },
  };

  function syncDesktopBar(settings = {}, appVer, ytdlpVer) {
    const cb = document.getElementById("deskClipboardWatch");
    if (cb) cb.checked = !!settings.clipboardWatch;
    const db = document.getElementById("deskDropBox");
    if (db) db.checked = !!settings.dropBox;
    const nt = document.getElementById("deskNotifications");
    if (nt) nt.checked = settings.notifications !== false;
    const ch = document.getElementById("deskChannel");
    if (ch) ch.value = settings.releaseChannel || "stable";
    if (appVer) {
      appVersion = appVer;
      const about = document.getElementById("aboutVersion");
      if (about) about.textContent = "version " + appVer;
    }
    const verEl = document.getElementById("deskVerInfo");
    if (verEl && (appVer || ytdlpVer)) {
      verEl.textContent = [appVer && `App v${appVer}`, ytdlpVer && `yt-dlp ${ytdlpVer}`].filter(Boolean).join(" · ");
    }
  }

  window.addEventListener("DOMContentLoaded", () => {
    document.title = "AnySave Desktop";
    const isMac = /Mac/i.test(navigator.platform);
    const shortcutLabel = isMac ? "⌘+Shift+Y" : "Ctrl+Shift+Y";

    // Desktop-only settings: a card at the top of Settings (older pages: above the download rules).
    const host = document.getElementById("desktopSettings");
    const engineBar = document.querySelector(".engine-bar");
    if (host || engineBar) {
      const deskCard = document.createElement("div");
      deskCard.className = host ? "card set desktop-bar" : "engine-bar desktop-bar";
      deskCard.innerHTML = `
        <h3>Desktop app</h3>
        <div class="setrow">
          <div><b>Runs in your ${isMac ? "menu bar" : "system tray"}</b><span>Downloads keep going when every browser and window is closed.</span></div>
          <button type="button" id="deskPasteClipboard" class="btn" title="Download the link that's on your clipboard">Download copied link (${shortcutLabel})</button>
        </div>
        <label class="setrow switch-row"><div><b>Watch the clipboard</b><span>Copy a video or file link in any app and AnySave downloads it.</span></div>
          <input type="checkbox" class="switch" id="deskClipboardWatch" /></label>
        <label class="setrow switch-row"><div><b>Floating drop box</b><span>A small always-on-top target to drag links onto.</span></div>
          <input type="checkbox" class="switch" id="deskDropBox" /></label>
        <label class="setrow switch-row"><div><b>Sound and notification when a download finishes</b></div>
          <input type="checkbox" class="switch" id="deskNotifications" checked /></label>
        <div class="setrow">
          <div><b>Updates</b><span id="deskVerInfo">AnySave Desktop</span></div>
          <div class="row">
            <label class="small">Channel <select id="deskChannel"><option value="stable">Stable</option><option value="beta">Beta</option></select></label>
            <button type="button" id="deskUpdateBtn" class="btn">Check for updates</button>
          </div>
        </div>
      `;
      if (host) host.appendChild(deskCard);
      else engineBar.parentNode.insertBefore(deskCard, engineBar);

      document.getElementById("deskClipboardWatch")?.addEventListener("change", (e) => {
        apiAction({ type: "setSettings", patch: { clipboardWatch: e.target.checked } });
      });
      document.getElementById("deskDropBox")?.addEventListener("change", (e) => {
        apiAction({ type: "setSettings", patch: { dropBox: e.target.checked } });
      });
      document.getElementById("deskNotifications")?.addEventListener("change", (e) => {
        apiAction({ type: "setSettings", patch: { notifications: e.target.checked } });
      });
      document.getElementById("deskChannel")?.addEventListener("change", (e) => {
        apiAction({ type: "setSettings", patch: { releaseChannel: e.target.value } });
      });
      document.getElementById("deskPasteClipboard")?.addEventListener("click", async () => {
        const btn = document.getElementById("deskPasteClipboard");
        const orig = btn.textContent;
        btn.disabled = true;
        btn.textContent = "Reading clipboard…";
        const res = await apiAction({ type: "downloadClipboard" });
        btn.disabled = false;
        btn.textContent = res?.ok ? "Added ✓" : (res?.error || "No link on clipboard");
        setTimeout(() => { btn.textContent = orig; }, 2200);
      });
      document.getElementById("deskUpdateBtn")?.addEventListener("click", async () => {
        const btn = document.getElementById("deskUpdateBtn");
        const orig = btn.textContent;
        btn.disabled = true;
        btn.textContent = "Checking updates…";
        const res = await apiAction({ type: "selfUpdate" });
        btn.disabled = false;
        btn.textContent = res?.ok ? (res.message || "Up to date ✓") : (res?.error || "Update failed");
        setTimeout(() => { btn.textContent = orig; }, 3500);
      });
    }

    // Drag and drop any link or text onto the Desktop window to download
    window.addEventListener("dragover", (e) => { e.preventDefault(); });
    window.addEventListener("drop", async (e) => {
      if (e.defaultPrevented) return; // the page already took this link
      e.preventDefault();
      const uri = e.dataTransfer?.getData("text/uri-list") || e.dataTransfer?.getData("text/plain") || "";
      if (/^https?:\/\//i.test(uri.trim())) {
        const input = document.getElementById("addUrl");
        if (input) {
          input.value = uri.trim();
          document.getElementById("addForm")?.requestSubmit();
        } else {
          await apiAction({ type: "addLink", url: uri.trim() });
        }
      }
    });

    connectDaemon();
  });
})();

