import AppKit
import ServiceManagement
import SwiftUI

final class AppDelegate: NSObject, NSApplicationDelegate, NSPopoverDelegate {
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private let daemon = DaemonManager()
    private let updater = Updater()
    private let notifier = NotificationConsumer()
    private let model = DashboardModel()
    private let popover = NSPopover()
    private var timer: Timer?
    private var outsideClickMonitor: Any?
    private var balances: [BalanceState] = []

    // MARK: - 生命周期

    func applicationDidFinishLaunching(_ notification: Notification) {
        model.onRefresh = { [weak self] in self?.refreshNow() }
        model.onOpenURL = { NSWorkspace.shared.open($0) }
        model.onOpenDataDir = { NSWorkspace.shared.open(URL(fileURLWithPath: DaemonManager.cacheDir)) }
        model.onToggleLoginItem = { [weak self] in self?.toggleLoginItem() }
        model.onQuit = { NSApp.terminate(nil) }
        model.onSetEnabled = { [weak self] name, enabled in self?.setEnabled(name, enabled) }
        model.onSetAccessKey = { [weak self] name, id, secret, completion in
            guard let self else { return completion(false) }
            self.setAccessKey(name, id: id, secret: secret, completion: completion)
        }
        model.onClearAccessKey = { [weak self] name in self?.clearAccessKey(name) }
        model.onCheckUpdate = { [weak self] in self?.checkUpdate(manual: true) }
        model.onInstallUpdate = { [weak self] in self?.installAvailableUpdate() }
        model.onSkipUpdate = { [weak self] in self?.skipUpdate() }
        model.onReorder = { [weak self] in self?.rebuildUI() }

        let host = NSHostingController(rootView: DashboardView(model: model))
        host.sizingOptions = [.preferredContentSize]
        popover.contentViewController = host
        popover.behavior = .transient
        popover.animates = true
        popover.delegate = self

        if let button = statusItem.button {
            button.target = self
            button.action = #selector(togglePopover(_:))
        }

        notifier.requestAuthorization()
        syncLoginItemWanted()
        daemon.start()
        refresh()
        loadProviders()

        // 60 秒刷新一次菜单栏与通知；daemon 自己按 1 分钟节奏轮询。
        timer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in
            self?.refresh()
            self?.checkUpdate(manual: false)
        }
        checkUpdate(manual: false)
    }

    func applicationWillTerminate(_ notification: Notification) {
        daemon.stop()
    }

    // MARK: - 刷新

    private func refresh(completion: (() -> Void)? = nil) {
        DispatchQueue.global(qos: .utility).async { [weak self] in
            let balances = StateReader.read() ?? []
            let history = HistoryReader.read(since: Date().addingTimeInterval(-35 * 86400))
            self?.notifier.consume()
            DispatchQueue.main.async {
                guard let self else {
                    completion?()
                    return
                }
                self.balances = balances
                self.model.history = history
                self.rebuildUI()
                completion?()
            }
        }
    }

    private func rebuildUI() {
        model.balances = balances
        model.daemonMissing = daemon.isMissing
        model.loginItemEnabled = Self.loginItemEnabled()
        model.now = Date()

        guard let button = statusItem.button else { return }
        let ordered = model.orderedBalances
        button.toolTip = Self.summaryTitle(ordered, daemonMissing: daemon.isMissing)
        button.image = Self.statusImage(balances, badge: model.update != nil)
        if let pct = Self.topUsage(ordered) {
            button.attributedTitle = NSAttributedString(string: " \(pct)%", attributes: [
                .font: NSFont.monospacedDigitSystemFont(ofSize: NSFont.systemFontSize, weight: .regular),
            ])
            button.imagePosition = .imageLeading
        } else {
            button.title = ""
            button.imagePosition = .imageOnly
        }
    }

    @objc private func togglePopover(_ sender: Any?) {
        guard let button = statusItem.button else { return }
        if popover.isShown {
            popover.performClose(sender)
            return
        }
        refresh()
        loadProviders()
        popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
        NSApp.activate(ignoringOtherApps: true)
        popover.contentViewController?.view.window?.makeKey()
        outsideClickMonitor = NSEvent.addGlobalMonitorForEvents(matching: [.leftMouseDown, .rightMouseDown]) { [weak self] _ in
            self?.popover.performClose(nil)
        }
    }

    func popoverDidClose(_ notification: Notification) {
        if let m = outsideClickMonitor {
            NSEvent.removeMonitor(m)
            outsideClickMonitor = nil
        }
    }

    func applicationDidResignActive(_ notification: Notification) {
        popover.performClose(nil)
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
            let sections = b.sections.filter { $0.usedPct >= 0 }.map { "\(shortName($0.name)) \(Int($0.usedPct.rounded()))%" }
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

    // 排在最上面的服务中最紧张那一项的已用百分比；该服务出错或没有百分比数据时不显示。
    static func topUsage(_ ordered: [BalanceState]) -> Int? {
        guard let top = ordered.first, top.error?.isEmpty ?? true,
              let used = top.sections.map(\.usedPct).filter({ $0 >= 0 }).max() else { return nil }
        return Int(used.rounded())
    }

    static func displayName(_ provider: String) -> String {
        switch provider {
        case "bailian": return "百炼"
        case "claudecode": return "Claude"
        case "codex": return "Codex"
        case "qoder": return "Qoder"
        case "qodercn": return "Qoder CN"
        default: return provider
        }
    }

    static let consoles: [(provider: String, title: String, url: String)] = [
        ("bailian", "打开百炼控制台", "https://bailian.console.aliyun.com/"),
        ("claudecode", "打开 Claude 用量页", "https://claude.ai/settings/usage"),
        ("codex", "打开 Codex 用量页", "https://chatgpt.com/codex/settings/usage"),
        ("qoder", "打开 Qoder 官网", "https://qoder.com"),
        ("qodercn", "打开 Qoder CN 官网", "https://qoder.com.cn"),
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
        case "reset-credits": return "重置卡"
        case "addon-credits": return "额度补充"
        case "subscription": return "套餐剩余"
        case "credits": return "Credits 余额"
        default: return name
        }
    }

    // MARK: - 菜单栏图标

    static func statusImage(_ balances: [BalanceState], badge: Bool = false) -> NSImage {
        let used = balances.filter { $0.error?.isEmpty ?? true }
            .flatMap(\.sections).map(\.usedPct).filter { $0 >= 0 }.max()
        guard let used else { return Gourd.menuBarImage(remaining: nil, tint: nil, badge: badge) }
        let tint: NSColor?
        switch used {
        case 90...: tint = .systemRed
        case 70..<90: tint = .systemOrange
        default: tint = nil
        }
        return Gourd.menuBarImage(remaining: max(0, 100 - used), tint: tint, badge: badge)
    }

    // MARK: - 更新

    // 自动检查每 24 小时一次，跳过的版本不再提示；手动检查总是查询并弹窗告知结果。
    private func checkUpdate(manual: Bool) {
        guard !model.updating, !model.checkingUpdate, manual || updater.isDue else { return }
        model.checkingUpdate = true
        updater.check { [weak self] result in
            guard let self else { return }
            self.model.checkingUpdate = false
            switch result {
            case .available(let update):
                if manual || update.version != self.updater.skippedVersion {
                    self.model.update = update
                }
                if manual { self.promptUpdate(update) }
            case .upToDate(let latest):
                self.model.update = nil
                if manual { self.showInfo("已是最新版本", "当前版本 v\(Updater.currentVersion)，最新发布 v\(latest)。") }
            case .failed(let msg):
                if manual { self.showInfo("检查更新失败", msg) }
            }
            self.rebuildUI()
        }
    }

    private func promptUpdate(_ update: AvailableUpdate) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = "有新版本 v\(update.version)"
        alert.informativeText = "当前版本 v\(Updater.currentVersion)。点「立即更新」会自动下载、校验并替换，完成后重新打开 liquoten。"
        alert.addButton(withTitle: "立即更新")
        alert.addButton(withTitle: "稍后")
        alert.addButton(withTitle: "跳过此版本")
        switch alert.runModal() {
        case .alertFirstButtonReturn: installUpdate(update)
        case .alertThirdButtonReturn: skipUpdate()
        default: break
        }
    }

    private func installAvailableUpdate() {
        guard let update = model.update else { return }
        installUpdate(update)
    }

    private func installUpdate(_ update: AvailableUpdate) {
        guard !model.updating else { return }
        let appURL = Bundle.main.bundleURL
        guard Installer.canAutoUpdate(appPath: appURL) else {
            showUpdateFallback(update.url)
            return
        }
        model.updating = true
        model.updatePhase = "正在下载…"
        model.updateProgress = nil
        Installer.install(
            version: update.version,
            currentAppURL: appURL,
            progress: { [weak self] value in self?.model.updateProgress = value },
            phase: { [weak self] text in
                self?.model.updatePhase = text
                if text == "正在安装…" { self?.model.updateProgress = nil }
            },
            completion: { [weak self] result in
                guard let self else { return }
                self.model.updating = false
                self.model.updateProgress = nil
                self.model.updatePhase = nil
                switch result {
                case .success(let installed):
                    do {
                        try Installer.relaunch(appURL: installed)
                        NSApp.terminate(nil)
                    } catch {
                        self.showUpdateError("更新已安装，但自动重启失败：\(error.localizedDescription)", releasePage: update.url)
                    }
                case .failure(let error):
                    self.showUpdateError(error.localizedDescription, releasePage: update.url)
                }
            }
        )
    }

    private func showUpdateFallback(_ releasePage: URL) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "无法自动更新"
        alert.informativeText = "无法自动更新：请把 liquoten 拖到「应用程序」文件夹后再试，或手动下载新版。"
        alert.addButton(withTitle: "打开下载页面")
        alert.addButton(withTitle: "取消")
        if alert.runModal() == .alertFirstButtonReturn {
            NSWorkspace.shared.open(releasePage)
        }
    }

    private func showUpdateError(_ message: String, releasePage: URL) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "自动更新失败"
        alert.informativeText = message + "\n\n也可以打开下载页面手动安装。"
        alert.addButton(withTitle: "打开下载页面")
        alert.addButton(withTitle: "取消")
        if alert.runModal() == .alertFirstButtonReturn {
            NSWorkspace.shared.open(releasePage)
        }
    }

    private func skipUpdate() {
        guard !model.updating else { return }
        updater.skippedVersion = model.update?.version
        model.update = nil
        rebuildUI()
    }

    private func showInfo(_ title: String, _ message: String) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = message
        alert.runModal()
    }

    // MARK: - 动作

    private func loadProviders() {
        daemon.listProviders { [weak self] list in
            guard let self else { return }
            self.model.providers = list
            self.model.providersLoaded = true
        }
    }

    // 开关服务：需要 API Key 的先弹窗输入并验证。
    private func setEnabled(_ name: String, _ enabled: Bool) {
        guard model.togglingProvider == nil else { return }
        guard enabled else {
            begin(name, "正在关闭…")
            daemon.disable(provider: name) { [weak self] failure in
                self?.finish(failure.map { ("关闭服务失败", $0) })
            }
            return
        }
        let info = model.providers.first { $0.name == name }
        var apiKey: String?
        if info?.requiresApiKey == true {
            guard let key = Self.promptAPIKey(for: name) else { return }
            apiKey = key
        }
        begin(name, apiKey == nil ? "正在检查…" : "正在验证 API Key…")
        daemon.enable(provider: name, apiKey: apiKey) { [weak self] failure in
            guard let self else { return }
            self.finish(failure.map { (apiKey == nil ? "开启服务失败" : "API Key 验证失败", $0) })
        }
    }

    private func begin(_ name: String, _ text: String) {
        model.togglingProvider = name
        model.busyText = text
    }

    private func setAccessKey(_ name: String, id: String, secret: String, completion: @escaping (Bool) -> Void) {
        guard model.togglingProvider == nil else { return completion(false) }
        begin(name, "正在验证 AccessKey…")
        daemon.setAccessKey(provider: name, id: id, secret: secret) { [weak self] failure in
            guard let self else { return completion(false) }
            self.finish(failure.map { ("AccessKey 验证失败", $0) })
            completion(failure == nil)
        }
    }

    private func clearAccessKey(_ name: String) {
        guard model.togglingProvider == nil else { return }
        begin(name, "正在清除 AccessKey…")
        daemon.clearAccessKey(provider: name) { [weak self] failure in
            guard let self else { return }
            self.finish(failure.map { ("清除 AccessKey 失败", $0) })
        }
    }

    private func finish(_ failure: (title: String, message: String)?) {
        model.togglingProvider = nil
        model.busyText = nil
        if let failure {
            NSApp.activate(ignoringOtherApps: true)
            let alert = NSAlert()
            alert.alertStyle = .warning
            alert.messageText = failure.title
            alert.informativeText = failure.message
            alert.runModal()
        }
        loadProviders()
        restartDaemon()
    }

    static func keyURL(_ provider: String) -> URL? {
        switch provider {
        case "qoder": return URL(string: "https://qoder.com/account/integrations")
        case "qodercn": return URL(string: "https://qoder.com.cn/account/integrations")
        default: return nil
        }
    }

    // 弹窗输入 API Key；取消返回 nil。“获取 API Key”打开令牌页后继续停留在弹窗。
    static func promptAPIKey(for provider: String) -> String? {
        NSApp.activate(ignoringOtherApps: true)
        let field = NSSecureTextField(frame: NSRect(x: 0, y: 0, width: 300, height: 24))
        field.placeholderString = "粘贴个人访问令牌"
        while true {
            let alert = NSAlert()
            alert.messageText = "开启\(displayName(provider))需要 API Key"
            alert.informativeText = "在\(displayName(provider))账号的「集成」页面生成个人访问令牌（Personal Access Token），粘贴到下面。Key 只保存在本机配置文件里，关闭服务时会删除。"
            alert.accessoryView = field
            alert.addButton(withTitle: "开启")
            alert.addButton(withTitle: "取消")
            if keyURL(provider) != nil { alert.addButton(withTitle: "获取 API Key") }
            alert.window.initialFirstResponder = field
            switch alert.runModal() {
            case .alertFirstButtonReturn:
                let key = field.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
                if !key.isEmpty { return key }
            case .alertThirdButtonReturn:
                if let url = keyURL(provider) { NSWorkspace.shared.open(url) }
            default:
                return nil
            }
        }
    }

    private func refreshNow() {
        guard !model.refreshing else { return }
        model.refreshing = true
        daemon.refreshNow()
        refresh()
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) { [weak self] in
            self?.refresh {
                self?.model.refreshing = false
            }
        }
    }

    private func restartDaemon() {
        daemon.restart()
        refresh()
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) { [weak self] in self?.refresh() }
    }

    private func toggleLoginItem() {
        if #available(macOS 13.0, *) {
            let wanted = !Self.loginItemEnabled()
            do {
                if wanted {
                    try SMAppService.mainApp.register()
                } else {
                    try SMAppService.mainApp.unregister()
                }
                UserDefaults.standard.set(wanted, forKey: Self.loginItemWantedKey)
                rebuildUI()
            } catch {
                let alert = NSAlert()
                alert.messageText = "开机自启设置失败"
                alert.informativeText = error.localizedDescription
                alert.runModal()
            }
        }
    }

    private static let loginItemWantedKey = "loginItem.wanted"

    private func syncLoginItemWanted() {
        if #available(macOS 13.0, *) {
            let defaults = UserDefaults.standard
            if let wanted = defaults.object(forKey: Self.loginItemWantedKey) as? Bool {
                if wanted && !Self.loginItemEnabled() {
                    do {
                        try SMAppService.mainApp.register()
                    } catch {
                        NSLog("恢复开机自启失败: \(error)")
                    }
                }
            } else {
                defaults.set(Self.loginItemEnabled(), forKey: Self.loginItemWantedKey)
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
