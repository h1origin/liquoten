import AppKit
import ServiceManagement

final class AppDelegate: NSObject, NSApplicationDelegate {
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private let daemon = DaemonManager()
    private let notifier = NotificationConsumer()
    private var timer: Timer?
    private var balances: [BalanceState] = []

    // MARK: - 生命周期

    func applicationDidFinishLaunching(_ notification: Notification) {
        statusItem.button?.font = NSFont.monospacedDigitSystemFont(
            ofSize: NSFont.smallSystemFontSize, weight: .regular)

        notifier.requestAuthorization()
        daemon.start()
        refresh()

        // 60 秒刷新一次菜单栏与通知；daemon 自己按 15 分钟节奏轮询。
        timer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in
            self?.refresh()
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        daemon.stop()
    }

    // MARK: - 刷新

    private func refresh() {
        DispatchQueue.global(qos: .utility).async { [weak self] in
            let balances = StateReader.read() ?? []
            self?.notifier.consume()
            DispatchQueue.main.async {
                self?.balances = balances
                self?.rebuildUI()
            }
        }
    }

    private func rebuildUI() {
        guard let button = statusItem.button else { return }
        button.title = Self.summaryTitle(balances, daemonMissing: daemon.isMissing)
        button.image = Self.ringsImage(balances)
        button.imagePosition = button.image == nil ? .noImage : .imageLeft

        let menu = NSMenu()
        buildContent(in: menu)
        menu.addItem(.separator())

        let console = NSMenuItem(title: "打开百炼控制台", action: #selector(openConsole), keyEquivalent: "")
        console.target = self
        menu.addItem(console)

        let cache = NSMenuItem(title: "打开数据目录", action: #selector(openCacheDir), keyEquivalent: "")
        cache.target = self
        menu.addItem(cache)

        let refreshItem = NSMenuItem(title: "立即刷新", action: #selector(forceRefresh), keyEquivalent: "")
        refreshItem.target = self
        menu.addItem(refreshItem)

        let login = NSMenuItem(title: "开机自动启动", action: #selector(toggleLoginItem), keyEquivalent: "")
        login.target = self
        login.state = Self.loginItemEnabled() ? .on : .off
        menu.addItem(login)

        menu.addItem(.separator())
        let quit = NSMenuItem(title: "退出 Liquoten", action: #selector(quit), keyEquivalent: "q")
        quit.target = self
        menu.addItem(quit)

        statusItem.menu = menu
    }

    private func buildContent(in menu: NSMenu) {
        if daemon.isMissing {
            menu.addItem(Self.disabled("daemon 缺失，请重新安装 Liquoten.app"))
            return
        }
        if balances.isEmpty {
            menu.addItem(Self.disabled("等待首次轮询…"))
            return
        }
        for b in balances {
            if let err = b.error, !err.isEmpty {
                menu.addItem(Self.disabled("\(b.provider): \(err)"))
                continue
            }
            for s in b.sections {
                menu.addItem(Self.disabled(Self.sectionLine(provider: b.provider, s)))
            }
        }
        if let updated = Self.lastUpdatedText(balances) {
            menu.addItem(.separator())
            menu.addItem(Self.disabled("上次更新: \(updated)"))
        }
    }

    // MARK: - 文本

    static func summaryTitle(_ balances: [BalanceState], daemonMissing: Bool) -> String {
        if balances.isEmpty { return daemonMissing ? "liquoten ✕" : "liquoten" }
        var parts: [String] = []
        for b in balances {
            if let err = b.error, !err.isEmpty {
                parts.append("\(b.provider) 出错")
                continue
            }
            for s in b.sections {
                let prefix = balances.count > 1 ? "\(b.provider) " : ""
                parts.append("\(prefix)\(shortName(s.name)) \(Int(s.usedPct.rounded()))%")
            }
        }
        if parts.isEmpty { return "liquoten" }
        let title = parts.joined(separator: " · ")
        return title.count > 36 ? String(title.prefix(33)) + "…" : title
    }

    static func shortName(_ name: String) -> String {
        switch name {
        case "5-hour quota": return "5h"
        case "1-week quota": return "周"
        default: return name
        }
    }

    static func sectionLine(provider: String, _ s: SectionState) -> String {
        var line = "\(provider) · \(s.name): \(fmtPct(s.usedPct))%"
        if s.usedPct < 0 {
            line = "\(provider) · \(s.name): 无限制"
        }
        if let reset = s.resetAt, reset > Date() {
            line += "（\(countdown(to: reset)) 后重置）"
        }
        return line
    }

    static func fmtPct(_ v: Double) -> String {
        String(format: "%.1f", v)
    }

    static func countdown(to date: Date) -> String {
        let secs = max(0, Int(date.timeIntervalSinceNow))
        let h = secs / 3600, m = (secs % 3600) / 60
        if h > 0 { return "\(h)h\(m)m" }
        return "\(m)m"
    }

    static func lastUpdatedText(_ balances: [BalanceState]) -> String? {
        let latest = balances.compactMap(\.fetchedAt).max()
        guard let latest else { return nil }
        let f = DateFormatter()
        f.dateFormat = "HH:mm:ss"
        return f.string(from: latest)
    }

    static func disabled(_ text: String) -> NSMenuItem {
        let item = NSMenuItem(title: text, action: nil, keyEquivalent: "")
        item.isEnabled = false
        return item
    }

    // MARK: - 环形图标

    // 每个有百分比语义的 section 一个环形图标，横向排列（最多 4 个）。
    static func ringsImage(_ balances: [BalanceState]) -> NSImage? {
        let ringSize: CGFloat = 13, gap: CGFloat = 3
        var rings: [(Double, NSColor)] = []
        for b in balances where b.error == nil {
            for s in b.sections where s.usedPct >= 0 {
                rings.append((s.usedPct, ringColor(s.usedPct)))
            }
        }
        guard !rings.isEmpty else { return nil }
        if rings.count > 4 { rings = Array(rings.prefix(4)) }
        let width = CGFloat(rings.count) * ringSize + CGFloat(rings.count - 1) * gap
        return NSImage(size: NSSize(width: width, height: ringSize), flipped: false) { rect in
            for (i, r) in rings.enumerated() {
                let x = CGFloat(i) * (ringSize + gap)
                drawRing(percent: r.0, color: r.1, in: NSRect(x: x, y: 0, width: ringSize, height: ringSize))
            }
            return true
        }
    }

    static func ringColor(_ usedPct: Double) -> NSColor {
        switch usedPct {
        case 90...: return .systemRed
        case 70..<90: return .systemOrange
        default: return .systemBlue
        }
    }

    static func drawRing(percent: Double, color: NSColor, in rect: NSRect) {
        let lineWidth: CGFloat = 1.5
        let circleRect = rect.insetBy(dx: lineWidth / 2, dy: lineWidth / 2)
        NSColor.labelColor.withAlphaComponent(0.55).setStroke()
        let outline = NSBezierPath(ovalIn: circleRect)
        outline.lineWidth = lineWidth
        outline.stroke()

        let p = min(max(percent, 0), 100) / 100
        guard p > 0 else { return }
        let center = NSPoint(x: rect.midX, y: rect.midY)
        let radius = circleRect.width / 2
        let pie = NSBezierPath()
        pie.move(to: center)
        pie.appendArc(withCenter: center, radius: radius, startAngle: 90, endAngle: 90 - 360 * p, clockwise: true)
        pie.close()
        color.setFill()
        pie.fill()
    }

    // MARK: - 动作

    @objc private func openConsole() {
        if let url = URL(string: "https://bailian.console.aliyun.com/") {
            NSWorkspace.shared.open(url)
        }
    }

    @objc private func openCacheDir() {
        NSWorkspace.shared.open(URL(fileURLWithPath: DaemonManager.cacheDir))
    }

    @objc private func forceRefresh() {
        daemon.restart()
    }

    @objc private func toggleLoginItem() {
        if #available(macOS 13.0, *) {
            do {
                if Self.loginItemEnabled() {
                    try SMAppService.mainApp.unregister()
                } else {
                    try SMAppService.mainApp.register()
                }
                rebuildUI()
            } catch {
                let alert = NSAlert()
                alert.messageText = "开机自启设置失败"
                alert.informativeText = error.localizedDescription
                alert.runModal()
            }
        }
    }

    static func loginItemEnabled() -> Bool {
        if #available(macOS 13.0, *) {
            return SMAppService.mainApp.status == .enabled
        }
        return false
    }

    @objc private func quit() {
        NSApp.terminate(nil)
    }
}

// 读 state.json 的纯函数，放这里避免和 UI 逻辑混在一起。
enum StateReader {
    static func read() -> [BalanceState]? {
        let url = URL(fileURLWithPath: DaemonManager.cacheDir)
            .appendingPathComponent("state.json")
        guard let data = try? Data(contentsOf: url) else { return nil }
        return try? liquotenDecoder().decode([BalanceState].self, from: data)
    }
}
