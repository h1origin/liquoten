import AppKit
import ServiceManagement
import SwiftUI

final class AppDelegate: NSObject, NSApplicationDelegate {
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private let daemon = DaemonManager()
    private let notifier = NotificationConsumer()
    private let model = DashboardModel()
    private let popover = NSPopover()
    private var timer: Timer?
    private var balances: [BalanceState] = []

    // MARK: - 生命周期

    func applicationDidFinishLaunching(_ notification: Notification) {
        model.onRefresh = { [weak self] in self?.forceRefresh() }
        model.onLogin = { [weak self] site in self?.loginBailian(site: site) }
        model.onOpenURL = { NSWorkspace.shared.open($0) }
        model.onOpenDataDir = { NSWorkspace.shared.open(URL(fileURLWithPath: DaemonManager.cacheDir)) }
        model.onToggleLoginItem = { [weak self] in self?.toggleLoginItem() }
        model.onQuit = { NSApp.terminate(nil) }

        let host = NSHostingController(rootView: DashboardView(model: model))
        host.sizingOptions = [.preferredContentSize]
        popover.contentViewController = host
        popover.behavior = .transient
        popover.animates = true

        if let button = statusItem.button {
            button.target = self
            button.action = #selector(togglePopover(_:))
        }

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
            let history = HistoryReader.read(since: Date().addingTimeInterval(-30 * 86400))
            self?.notifier.consume()
            DispatchQueue.main.async {
                self?.balances = balances
                self?.model.history = history
                self?.rebuildUI()
            }
        }
    }

    private func rebuildUI() {
        model.balances = balances
        model.daemonMissing = daemon.isMissing
        model.loginItemEnabled = Self.loginItemEnabled()
        model.now = Date()

        guard let button = statusItem.button else { return }
        button.toolTip = Self.summaryTitle(balances, daemonMissing: daemon.isMissing)
        if let rings = Self.ringsImage(balances) {
            button.image = rings
        } else {
            let symbol = balances.isEmpty ? "gauge.with.dots.needle.33percent" : "exclamationmark.triangle"
            let image = NSImage(systemSymbolName: symbol, accessibilityDescription: "Liquoten")
            image?.isTemplate = true
            button.image = image
        }
        button.title = ""
        button.imagePosition = .imageOnly
    }

    @objc private func togglePopover(_ sender: Any?) {
        guard let button = statusItem.button else { return }
        if popover.isShown {
            popover.performClose(sender)
            return
        }
        refresh()
        popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
        NSApp.activate(ignoringOtherApps: true)
        popover.contentViewController?.view.window?.makeKey()
    }

    // MARK: - 文本

    static func summaryTitle(_ balances: [BalanceState], daemonMissing: Bool) -> String {
        if balances.isEmpty { return daemonMissing ? "liquoten ✕" : "liquoten" }
        var parts: [String] = []
        for b in balances {
            if let err = b.error, !err.isEmpty {
                parts.append("\(displayName(b.provider)) 出错")
                continue
            }
            let sections = b.sections.map { "\(shortName($0.name)) \(Int($0.usedPct.rounded()))%" }
            if sections.isEmpty { continue }
            if balances.count > 1 {
                parts.append("\(displayName(b.provider)) " + sections.joined(separator: " "))
            } else {
                parts.append(contentsOf: sections)
            }
        }
        if parts.isEmpty { return "liquoten" }
        let title = parts.joined(separator: " · ")
        return title.count > 48 ? String(title.prefix(45)) + "…" : title
    }

    static func displayName(_ provider: String) -> String {
        switch provider {
        case "bailian": return "百炼"
        case "claudecode": return "Claude"
        default: return provider
        }
    }

    static let consoles: [(provider: String, title: String, url: String)] = [
        ("bailian", "打开百炼控制台", "https://bailian.console.aliyun.com/"),
        ("claudecode", "打开 Claude 用量页", "https://claude.ai/settings/usage"),
    ]

    static func shortName(_ name: String) -> String {
        switch name {
        case "5-hour quota": return "5h"
        case "1-week quota": return "周"
        case "1-month quota": return "月"
        default: return name
        }
    }

    static func sectionTitle(_ name: String) -> String {
        switch name {
        case "5-hour quota": return "5 小时额度"
        case "1-week quota": return "每周额度"
        case "1-month quota": return "月度额度"
        default: return name
        }
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

    private func loginBailian(site: String) {
        guard !model.loggingIn else { return }
        model.loggingIn = true
        daemon.login(provider: "bailian", site: site) { [weak self] failure in
            guard let self else { return }
            self.model.loggingIn = false
            if let failure {
                let alert = NSAlert()
                alert.messageText = "百炼登录失败"
                alert.informativeText = failure
                alert.runModal()
                self.refresh()
            } else {
                self.forceRefresh()
            }
        }
    }

    private func forceRefresh() {
        daemon.restart()
        refresh()
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) { [weak self] in self?.refresh() }
    }

    private func toggleLoginItem() {
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
