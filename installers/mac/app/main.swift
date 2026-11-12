import Cocoa
import WebKit

struct DaemonInfo: Decodable {
    let port: Int
    let token: String
    let pid: Int
    let version: String
}

class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    var statusItem: NSStatusItem!
    var mainWindow: NSWindow?
    var mainWebView: WKWebView?
    var dropPanel: NSPanel?
    var dropWebView: WKWebView?
    var daemonInfo: DaemonInfo?
    var statusTimer: Timer?
    var spawnedDaemon: Process?

    func applicationDidFinishLaunching(_ notification: Notification) {
        setupMainMenu()
        setupStatusBar()
        setupGlobalShortcut()

        DispatchQueue.global(qos: .userInitiated).async {
            let info = self.ensureDaemonRunning()
            DispatchQueue.main.async {
                self.daemonInfo = info
                self.showMainWindow()
                self.startStatusPolling()
            }
        }
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showMainWindow()
        return true
    }

    func application(_ application: NSApplication, open urls: [URL]) {
        for url in urls {
            var raw = url.absoluteString
            if raw.lowercased().hasPrefix("anysave://") {
                if let comps = URLComponents(url: url, resolvingAgainstBaseURL: false),
                   let target = comps.queryItems?.first(where: { $0.name == "url" })?.value {
                    raw = target
                } else {
                    raw = String(raw.dropFirst("anysave://".count))
                }
            }
            sendDaemonAction(["type": "addLink", "url": raw])
        }
        showMainWindow()
    }

    func applicationWillTerminate(_ notification: Notification) {
        statusTimer?.invalidate()
    }

    // MARK: - Daemon lifecycle

    func appSupportDir() -> String {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let primary = (home as NSString).appendingPathComponent("Library/Application Support/AnySave")
        let legacy = (home as NSString).appendingPathComponent("VidGrab")
        let fm = FileManager.default
        if fm.fileExists(atPath: (primary as NSString).appendingPathComponent("vidgrab-host")) {
            return primary
        }
        if fm.fileExists(atPath: (legacy as NSString).appendingPathComponent("vidgrab-host")) {
            return legacy
        }
        return primary
    }

    func daemonJsonPath() -> String {
        return (appSupportDir() as NSString).appendingPathComponent("daemon.json")
    }

    func readDaemonInfo() -> DaemonInfo? {
        guard let data = try? Data(contentsOf: URL(fileURLWithPath: daemonJsonPath())),
              let info = try? JSONDecoder().decode(DaemonInfo.self, from: data) else {
            return nil
        }
        let sem = DispatchSemaphore(value: 0)
        var alive = false
        if let url = URL(string: "http://127.0.0.1:\(info.port)/api/ping?token=\(info.token)") {
            var req = URLRequest(url: url, timeoutInterval: 1.2)
            req.httpMethod = "GET"
            URLSession.shared.dataTask(with: req) { _, resp, _ in
                if let http = resp as? HTTPURLResponse, http.statusCode == 200 {
                    alive = true
                }
                sem.signal()
            }.resume()
            _ = sem.wait(timeout: .now() + 1.5)
        }
        return alive ? info : nil
    }

    func ensureDaemonRunning() -> DaemonInfo? {
        if let existing = readDaemonInfo() {
            return existing
        }
        let fm = FileManager.default
        let supportDir = appSupportDir()
        let installedHost = (supportDir as NSString).appendingPathComponent("vidgrab-host")
        let bundledHost = Bundle.main.bundlePath + "/Contents/MacOS/vidgrab-host"

        // Ensure ~/Library/Application Support/AnySave is populated from the app bundle if not yet installed.
        if !fm.isExecutableFile(atPath: installedHost), fm.isExecutableFile(atPath: bundledHost) {
            try? fm.createDirectory(atPath: supportDir, withIntermediateDirectories: true)
            try? fm.copyItem(atPath: bundledHost, toPath: installedHost)
            let bundledResExt = Bundle.main.bundlePath + "/Contents/Resources/extension"
            let home = fm.homeDirectoryForCurrentUser.path
            let destExt = (home as NSString).appendingPathComponent("AnySave/extension")
            if fm.fileExists(atPath: bundledResExt), !fm.fileExists(atPath: destExt) {
                try? fm.createDirectory(atPath: (destExt as NSString).deletingLastPathComponent, withIntermediateDirectories: true)
                try? fm.copyItem(atPath: bundledResExt, toPath: destExt)
            }
        }

        let hostBin = fm.isExecutableFile(atPath: installedHost) ? installedHost : bundledHost
        guard fm.isExecutableFile(atPath: hostBin) else { return nil }

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: hostBin)
        proc.arguments = ["--daemon"]
        try? proc.run()
        spawnedDaemon = proc

        for _ in 0..<25 {
            Thread.sleep(forTimeInterval: 0.15)
            if let info = readDaemonInfo() {
                return info
            }
        }
        return nil
    }

    func sendDaemonAction(_ payload: [String: Any], completion: (([String: Any]?) -> Void)? = nil) {
        guard let info = daemonInfo ?? readDaemonInfo(),
              let url = URL(string: "http://127.0.0.1:\(info.port)/api/action") else {
            completion?(nil)
            return
        }
        self.daemonInfo = info
        var req = URLRequest(url: url, timeoutInterval: 10)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue(info.token, forHTTPHeaderField: "X-AnySave-Token")
        req.httpBody = try? JSONSerialization.data(withJSONObject: payload)
        URLSession.shared.dataTask(with: req) { data, _, _ in
            guard let data = data,
                  let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
                completion?(nil)
                return
            }
            completion?(obj)
        }.resume()
    }

    // MARK: - Menu Bar (NSStatusItem)

    func setupStatusBar() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let btn = statusItem.button {
            btn.title = "⬇ AnySave"
            btn.toolTip = "AnySave Download Manager"
        }
        let menu = NSMenu()
        menu.addItem(withTitle: "Open AnySave", action: #selector(onOpenMain), keyEquivalent: "o")
        let clipItem = NSMenuItem(title: "Download Copied Link", action: #selector(onDownloadClipboard), keyEquivalent: "Y")
        clipItem.keyEquivalentModifierMask = [.command, .shift]
        menu.addItem(clipItem)
        menu.addItem(withTitle: "Toggle Floating Drop Box", action: #selector(onToggleDropBox), keyEquivalent: "d")
        menu.addItem(NSMenuItem.separator())
        menu.addItem(withTitle: "Pause All Downloads", action: #selector(onPauseAll), keyEquivalent: "")
        menu.addItem(withTitle: "Resume All Downloads", action: #selector(onResumeAll), keyEquivalent: "")
        menu.addItem(NSMenuItem.separator())
        menu.addItem(withTitle: "Quit AnySave", action: #selector(onQuitAll), keyEquivalent: "q")
        statusItem.menu = menu
    }

    func startStatusPolling() {
        statusTimer = Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { [weak self] _ in
            self?.refreshStatusTitle()
        }
    }

    func refreshStatusTitle() {
        guard let info = daemonInfo,
              let url = URL(string: "http://127.0.0.1:\(info.port)/api/state?token=\(info.token)") else { return }
        URLSession.shared.dataTask(with: url) { [weak self] data, _, _ in
            guard let data = data,
                  let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let jobs = obj["jobs"] as? [String: [String: Any]] else { return }
            var active = 0
            var sumPct = 0.0
            for (_, j) in jobs {
                let st = (j["status"] as? String) ?? ""
                if st == "starting" || st == "downloading" {
                    active += 1
                    sumPct += (j["percent"] as? Double) ?? 0.0
                }
            }
            let dropOn = ((obj["settings"] as? [String: Any])?["dropBox"] as? Bool) ?? false
            DispatchQueue.main.async {
                if active > 0 {
                    let avg = Int(sumPct / Double(active))
                    self?.statusItem.button?.title = "⬇ \(active) · \(avg)%"
                } else {
                    self?.statusItem.button?.title = "⬇ AnySave"
                }
                if dropOn && self?.dropPanel == nil {
                    self?.showDropBox()
                } else if !dropOn, let panel = self?.dropPanel, panel.isVisible {
                    panel.orderOut(nil)
                }
            }
        }.resume()
    }

    // MARK: - Windows (Main WKWebView & Floating Drop Box NSPanel)

    func showMainWindow() {
        guard let info = daemonInfo ?? ensureDaemonRunning() else { return }
        self.daemonInfo = info

        if mainWindow == nil {
            let rect = NSRect(x: 0, y: 0, width: 1020, height: 700)
            let w = NSWindow(
                contentRect: rect,
                styleMask: [.titled, .closable, .miniaturizable, .resizable],
                backing: .buffered,
                defer: false
            )
            w.title = "AnySave"
            w.center()
            w.delegate = self
            w.isReleasedWhenClosed = false
            w.backgroundColor = NSColor(calibratedRed: 0.07, green: 0.07, blue: 0.09, alpha: 1.0)

            let cfg = WKWebViewConfiguration()
            let wv = WKWebView(frame: rect, configuration: cfg)
            wv.autoresizingMask = [.width, .height]
            w.contentView = wv
            if let u = URL(string: "http://127.0.0.1:\(info.port)/?token=\(info.token)") {
                wv.load(URLRequest(url: u))
            }
            mainWindow = w
            mainWebView = wv
        }
        NSApp.activate(ignoringOtherApps: true)
        mainWindow?.makeKeyAndOrderFront(nil)
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        if sender == mainWindow {
            // Hide main window so downloads continue in the Menu Bar.
            sender.orderOut(nil)
            return false
        }
        return true
    }

    func showDropBox() {
        guard let info = daemonInfo else { return }
        if dropPanel == nil {
            let screen = NSScreen.main?.visibleFrame ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
            let rect = NSRect(x: screen.maxX - 310, y: screen.minY + 40, width: 290, height: 195)
            let p = NSPanel(
                contentRect: rect,
                styleMask: [.titled, .closable, .utilityWindow, .nonactivatingPanel],
                backing: .buffered,
                defer: false
            )
            p.title = "AnySave Drop Box"
            p.level = .floating
            p.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
            p.isFloatingPanel = true
            p.hidesOnDeactivate = false
            p.alphaValue = 0.96

            let wv = WKWebView(frame: rect, configuration: WKWebViewConfiguration())
            wv.autoresizingMask = [.width, .height]
            p.contentView = wv
            if let u = URL(string: "http://127.0.0.1:\(info.port)/drop?token=\(info.token)") {
                wv.load(URLRequest(url: u))
            }
            dropPanel = p
            dropWebView = wv
        }
        dropPanel?.orderFront(nil)
    }

    // MARK: - Actions & Shortcuts

    func setupGlobalShortcut() {
        NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            if event.modifierFlags.contains([.command, .shift]),
               event.charactersIgnoringModifiers?.lowercased() == "y" {
                self?.onDownloadClipboard()
                return nil
            }
            return event
        }
    }

    @objc func onOpenMain() {
        showMainWindow()
    }

    @objc func onDownloadClipboard() {
        sendDaemonAction(["type": "downloadClipboard"]) { [weak self] _ in
            DispatchQueue.main.async {
                self?.showMainWindow()
            }
        }
    }

    @objc func onToggleDropBox() {
        if let panel = dropPanel, panel.isVisible {
            panel.orderOut(nil)
            sendDaemonAction(["type": "setSettings", "patch": ["dropBox": false]])
        } else {
            showDropBox()
            sendDaemonAction(["type": "setSettings", "patch": ["dropBox": true]])
        }
    }

    @objc func onPauseAll() {
        guard let info = daemonInfo,
              let url = URL(string: "http://127.0.0.1:\(info.port)/api/state?token=\(info.token)") else { return }
        URLSession.shared.dataTask(with: url) { [weak self] data, _, _ in
            guard let data = data,
                  let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let jobs = obj["jobs"] as? [String: [String: Any]] else { return }
            for (id, j) in jobs {
                let st = (j["status"] as? String) ?? ""
                if st == "starting" || st == "downloading" {
                    self?.sendDaemonAction(["type": "pause", "id": id])
                }
            }
        }.resume()
    }

    @objc func onResumeAll() {
        guard let info = daemonInfo,
              let url = URL(string: "http://127.0.0.1:\(info.port)/api/state?token=\(info.token)") else { return }
        URLSession.shared.dataTask(with: url) { [weak self] data, _, _ in
            guard let data = data,
                  let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let jobs = obj["jobs"] as? [String: [String: Any]] else { return }
            for (id, j) in jobs {
                if ((j["status"] as? String) ?? "") == "paused" {
                    self?.sendDaemonAction(["type": "resume", "id": id])
                }
            }
        }.resume()
    }

    @objc func onQuitAll() {
        sendDaemonAction(["type": "quit"]) { _ in
            DispatchQueue.main.async {
                NSApp.terminate(nil)
            }
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
            NSApp.terminate(nil)
        }
    }

    func setupMainMenu() {
        let mainMenu = NSMenu()
        let appItem = NSMenuItem()
        mainMenu.addItem(appItem)
        let appMenu = NSMenu()
        appMenu.addItem(withTitle: "Open AnySave", action: #selector(onOpenMain), keyEquivalent: "o")
        let clipItem = NSMenuItem(title: "Download Copied Link", action: #selector(onDownloadClipboard), keyEquivalent: "Y")
        clipItem.keyEquivalentModifierMask = [.command, .shift]
        appMenu.addItem(clipItem)
        appMenu.addItem(withTitle: "Toggle Floating Drop Box", action: #selector(onToggleDropBox), keyEquivalent: "d")
        appMenu.addItem(NSMenuItem.separator())
        appMenu.addItem(withTitle: "Quit AnySave", action: #selector(onQuitAll), keyEquivalent: "q")
        appItem.submenu = appMenu

        let editItem = NSMenuItem()
        mainMenu.addItem(editItem)
        let editMenu = NSMenu(title: "Edit")
        editMenu.addItem(withTitle: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        editMenu.addItem(withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        editMenu.addItem(withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        editMenu.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        editItem.submenu = editMenu

        NSApp.mainMenu = mainMenu
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
