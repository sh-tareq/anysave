import Cocoa
import WebKit

struct DaemonInfo: Decodable {
    let port: Int
    let token: String
    let pid: Int
    let version: String
}

// BARICON-BEGIN
/// Draws the menu-bar icon: the AnySave logo; a progress ring around it while downloading
/// (with a bright arc chasing round while a download is still starting); a green tick badge
/// that pops in when a download finishes.
enum BarIcon {
    static let side: CGFloat = 20
    static let tickSeconds = 4.0

    /// How visible the tick is, 0...1.15, at `t` seconds after a download finished:
    /// pops in with a small overshoot, stays, then fades out.
    static func tickAmount(_ t: Double) -> Double {
        if t < 0 || t >= tickSeconds { return 0 }
        if t < 0.18 { return t / 0.18 * 1.15 }
        if t < 0.32 { return 1.15 - (t - 0.18) / 0.14 * 0.15 }
        if t > tickSeconds - 0.5 { return (tickSeconds - t) / 0.5 }
        return 1
    }

    static func draw(logo: NSImage?, fraction: Double?, spin: Double?, tick: Double) -> NSImage {
        let img = NSImage(size: NSSize(width: side, height: side), flipped: false) { rect in
            let c = NSPoint(x: rect.midX, y: rect.midY)
            // The logo: full size when idle, a little smaller inside the ring.
            let inset: CGFloat = fraction == nil ? 1.5 : 4.2
            // The app icon file has an empty margin round the red square; cut it away.
            if let logo = logo {
                let m = logo.size.width * 0.07
                logo.draw(in: rect.insetBy(dx: inset, dy: inset),
                          from: NSRect(x: m, y: m, width: logo.size.width - 2 * m, height: logo.size.height - 2 * m),
                          operation: .sourceOver, fraction: 1)
            }
            if let f = fraction {
                let r = side / 2 - 1.3
                let track = NSBezierPath()
                track.appendArc(withCenter: c, radius: r, startAngle: 0, endAngle: 360)
                track.lineWidth = 2
                NSColor.gray.withAlphaComponent(0.4).setStroke()
                track.stroke()
                let arc = NSBezierPath()
                arc.appendArc(withCenter: c, radius: r, startAngle: 90,
                              endAngle: 90 - 360 * CGFloat(max(0.03, min(1, f))), clockwise: true)
                arc.lineWidth = 2
                arc.lineCapStyle = .round
                NSColor(srgbRed: 0.90, green: 0.28, blue: 0.30, alpha: 1).setStroke()
                arc.stroke()
                if let s = spin {
                    let deg = 90 - CGFloat(s) * 180 / .pi
                    let chase = NSBezierPath()
                    chase.appendArc(withCenter: c, radius: r, startAngle: deg, endAngle: deg - 80, clockwise: true)
                    chase.lineWidth = 2
                    chase.lineCapStyle = .round
                    NSColor(srgbRed: 1.0, green: 0.70, blue: 0.42, alpha: 1).setStroke()
                    chase.stroke()
                }
            }
            if tick > 0.01 {
                let k = CGFloat(min(tick, 1.15))
                let br: CGFloat = 4.3 * k
                let bc = NSPoint(x: rect.maxX - 6.1, y: rect.minY + 6.1)
                let alpha = CGFloat(min(1, tick))
                let ring = NSBezierPath(ovalIn: NSRect(x: bc.x - br - 1, y: bc.y - br - 1, width: 2 * br + 2, height: 2 * br + 2))
                NSColor.white.withAlphaComponent(alpha).setFill()
                ring.fill()
                let dot = NSBezierPath(ovalIn: NSRect(x: bc.x - br, y: bc.y - br, width: 2 * br, height: 2 * br))
                NSColor(srgbRed: 0.18, green: 0.62, blue: 0.36, alpha: alpha).setFill()
                dot.fill()
                let mark = NSBezierPath()
                mark.move(to: NSPoint(x: bc.x - 2.0 * k, y: bc.y - 0.1 * k))
                mark.line(to: NSPoint(x: bc.x - 0.6 * k, y: bc.y - 1.5 * k))
                mark.line(to: NSPoint(x: bc.x + 2.1 * k, y: bc.y + 1.6 * k))
                mark.lineWidth = 1.3 * k
                mark.lineCapStyle = .round
                mark.lineJoinStyle = .round
                NSColor.white.withAlphaComponent(alpha).setStroke()
                mark.stroke()
            }
            return true
        }
        img.isTemplate = false
        return img
    }
}
// BARICON-END

class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    var statusItem: NSStatusItem!
    var mainWindow: NSWindow?
    var mainWebView: WKWebView?
    var dropPanel: NSPanel?
    var dropWebView: WKWebView?
    var daemonInfo: DaemonInfo?
    var statusTimer: Timer?
    var spawnedDaemon: Process?

    // Menu-bar icon animation state.
    var barLogo: NSImage?
    var barTimer: Timer?
    var barActive = false          // something is downloading
    var barTarget = 0.0            // real overall progress, 0...1
    var barShown = 0.0             // what the ring shows; glides towards barTarget
    var barSpinning = false        // a download is still starting (no progress yet)
    var barPhase = 0.0             // angle of the spinning arc
    var barTickStart: Date?        // when the "finished" tick started
    var lastDoneCount = -1         // finished downloads at the last check (-1 = not checked yet)
    // For testing: with ANYSAVE_BAR_DUMP=<folder> every icon put in the menu bar is also saved there.
    let barDumpDir = ProcessInfo.processInfo.environment["ANYSAVE_BAR_DUMP"]
    var barDumpCount = 0

    func setBarImage(_ img: NSImage) {
        statusItem.button?.image = img
        guard let dir = barDumpDir, barDumpCount < 600, let tiff = img.tiffRepresentation,
              let png = NSBitmapImageRep(data: tiff)?.representation(using: .png, properties: [:]) else { return }
        barDumpCount += 1
        let title = (statusItem.button?.title ?? "").trimmingCharacters(in: .whitespaces).replacingOccurrences(of: "%", with: "pct")
        try? png.write(to: URL(fileURLWithPath: String(format: "%@/f%04d-%@.png", dir, barDumpCount, title.isEmpty ? "idle" : title)))
    }

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
            // The AnySave logo, icon only like other menu-bar apps. It is redrawn with a progress
            // ring while downloading and a green tick when a download finishes (see BarIcon).
            barLogo = Bundle.main.image(forResource: "AppIcon") ?? NSApp.applicationIconImage
            btn.image = BarIcon.draw(logo: barLogo, fraction: nil, spin: nil, tick: 0)
            btn.imagePosition = .imageLeading
            btn.title = ""
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
            var starting = 0
            var doneCount = 0
            var sumPct = 0.0
            for (_, j) in jobs {
                let st = (j["status"] as? String) ?? ""
                if st == "done" { doneCount += 1 }
                if st == "starting" || st == "downloading" {
                    active += 1
                    let pct = (j["percent"] as? Double) ?? 0.0
                    sumPct += pct
                    if st == "starting" || pct <= 0 { starting += 1 }
                }
            }
            let dropOn = ((obj["settings"] as? [String: Any])?["dropBox"] as? Bool) ?? false
            DispatchQueue.main.async {
                guard let self = self else { return }
                if active > 0 {
                    let avg = Int(sumPct / Double(active))
                    // While downloading: the ring fills, with the overall percent beside it.
                    self.statusItem.button?.title = " \(avg)%"
                    self.statusItem.button?.toolTip = "AnySave: \(active) downloading · \(avg)%"
                } else {
                    self.statusItem.button?.title = ""
                    self.statusItem.button?.toolTip = "AnySave Download Manager"
                }
                self.barActive = active > 0
                self.barTarget = active > 0 ? min(1, max(0, sumPct / Double(active) / 100)) : 0
                self.barSpinning = starting > 0
                // A download finished since the last look: show the tick.
                if self.lastDoneCount >= 0 && doneCount > self.lastDoneCount { self.barTickStart = Date() }
                self.lastDoneCount = doneCount
                self.updateBarAnimation()
                if dropOn && self.dropPanel == nil {
                    self.showDropBox()
                } else if !dropOn, let panel = self.dropPanel, panel.isVisible {
                    panel.orderOut(nil)
                }
            }
        }.resume()
    }

    /// Runs the icon animation only while there is something to animate (downloading, or the tick).
    func updateBarAnimation() {
        let tickOn = barTickStart.map { Date().timeIntervalSince($0) < BarIcon.tickSeconds } ?? false
        if barActive || tickOn {
            if barTimer == nil {
                barTimer = Timer.scheduledTimer(withTimeInterval: 1.0 / 15.0, repeats: true) { [weak self] _ in self?.barFrame() }
            }
        } else {
            barTimer?.invalidate(); barTimer = nil
            barShown = 0; barTickStart = nil
            setBarImage(BarIcon.draw(logo: barLogo, fraction: nil, spin: nil, tick: 0))
        }
    }

    func barFrame() {
        barShown += (barTarget - barShown) * 0.2 // glide, don't jump
        barPhase = (barPhase + 0.35).truncatingRemainder(dividingBy: 2 * Double.pi)
        var tick = 0.0
        if let t0 = barTickStart {
            tick = BarIcon.tickAmount(Date().timeIntervalSince(t0))
            if Date().timeIntervalSince(t0) >= BarIcon.tickSeconds { barTickStart = nil }
        }
        setBarImage(BarIcon.draw(logo: barLogo, fraction: barActive ? barShown : nil,
                                 spin: barActive && barSpinning ? barPhase : nil, tick: tick))
        if !barActive && barTickStart == nil { updateBarAnimation() }
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
