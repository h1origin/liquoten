import SwiftUI

final class DashboardModel: ObservableObject {
    @Published var balances: [BalanceState] = []
    @Published var history: [String: [HistoryPoint]] = [:]
    @Published var loggingIn = false
    @Published var loginItemEnabled = false
    @Published var daemonMissing = false
    @Published var now = Date()
    @Published var providers: [ProviderInfo] = []
    @Published var providersLoaded = false
    @Published var showingServices = false
    @Published var togglingProvider: String?
    @Published var busyText: String?
    @Published var update: AvailableUpdate?
    @Published var order: [String] = UserDefaults.standard.stringArray(forKey: "providerOrder") ?? []
    @Published var draggingProvider: String?
    @Published var checkingUpdate = false

    var onRefresh: () -> Void = {}
    var onLogin: (String) -> Void = { _ in }
    var onOpenURL: (URL) -> Void = { _ in }
    var onOpenDataDir: () -> Void = {}
    var onToggleLoginItem: () -> Void = {}
    var onQuit: () -> Void = {}
    var onSetEnabled: (String, Bool) -> Void = { _, _ in }
    var onCheckUpdate: () -> Void = {}
    var onSkipUpdate: () -> Void = {}
    var onReorder: () -> Void = {}

    // 用户拖动排出的顺序；没排过的服务按名字排在后面。
    func rank(_ name: String) -> (Int, String) {
        (order.firstIndex(of: name) ?? Int.max, name)
    }

    var orderedBalances: [BalanceState] {
        balances.sorted { rank($0.provider) < rank($1.provider) }
    }

    var orderedProviders: [ProviderInfo] {
        providers.sorted { rank($0.name) < rank($1.name) }
    }

    func move(_ name: String, to target: String) {
        var names = orderedProviders.map(\.name)
        guard name != target, let from = names.firstIndex(of: name), let to = names.firstIndex(of: target) else { return }
        names.remove(at: from)
        names.insert(name, at: to)
        order = names
        UserDefaults.standard.set(names, forKey: "providerOrder")
        onReorder()
    }

    var noneEnabled: Bool {
        providersLoaded && !providers.contains(where: \.enabled)
    }

    func balance(_ provider: String) -> BalanceState? {
        balances.first { $0.provider == provider }
    }

    var okSections: [SectionState] {
        balances.filter { $0.error?.isEmpty ?? true }.flatMap(\.sections).filter { $0.usedPct >= 0 }
    }

    var peakUsage: Double? {
        okSections.map(\.usedPct).max()
    }

    var errorCount: Int {
        balances.filter { !($0.error?.isEmpty ?? true) }.count
    }

    var lastUpdated: Date? {
        balances.compactMap(\.fetchedAt).max()
    }

    var hasBailian: Bool {
        balances.contains { $0.provider == "bailian" }
    }
}

struct DashboardView: View {
    @ObservedObject var model: DashboardModel

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            if model.showingServices {
                ServicesView(model: model)
            } else {
                HeaderView(model: model)
                if let update = model.update {
                    UpdateBanner(model: model, update: update)
                }
                content
                FooterBar(model: model)
            }
        }
        .padding(14)
        .frame(width: 380)
    }

    @ViewBuilder private var content: some View {
        if model.daemonMissing {
            NoticeCard(icon: "xmark.octagon", text: "daemon 缺失，请重新安装 Liquoten.app")
        } else if model.noneEnabled {
            Card {
                HStack(spacing: 8) {
                    Image(systemName: "square.grid.2x2").foregroundStyle(.secondary)
                    Text("还没有选择要监控的服务").font(.callout.weight(.medium))
                }
                Text("选好之后，这里会显示每个服务的额度用量。").font(.callout).foregroundStyle(.secondary)
                Button("选择服务") { model.showingServices = true }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
            }
        } else if model.balances.isEmpty {
            NoticeCard(icon: "hourglass", text: "等待首次查询…")
        } else {
            ForEach(model.orderedBalances, id: \.provider) { b in
                if let err = b.error, !err.isEmpty {
                    ErrorCard(model: model, balance: b, message: err)
                } else {
                    let balances = b.sections.filter { $0.usedPct < 0 && $0.remaining >= 0 }
                    let quotas = b.sections.filter { !($0.usedPct < 0 && $0.remaining >= 0) }
                    ProviderHeader(provider: b.provider, extras: balances)
                    let columns = Array(repeating: GridItem(.flexible(), spacing: 10), count: max(1, min(quotas.count, 2)))
                    LazyVGrid(columns: columns, spacing: 10) {
                        ForEach(quotas, id: \.name) { s in
                            SectionCard(provider: b.provider, section: s,
                                        points: model.history[HistoryReader.key(b.provider, s.name)] ?? [],
                                        now: model.now)
                        }
                    }
                }
            }
        }
    }
}

enum Style {
    static func color(_ pct: Double) -> Color {
        switch pct {
        case 90...: return .red
        case 70..<90: return .orange
        default: return .blue
        }
    }

    static func status(_ pct: Double?) -> String {
        guard let pct else { return "暂无数据" }
        switch pct {
        case 90...: return "紧张"
        case 70..<90: return "注意"
        default: return "充足"
        }
    }

    static func providerIcon(_ provider: String) -> String {
        switch provider {
        case "bailian": return "cloud.fill"
        case "claudecode": return "sparkle"
        case "codex": return "chevron.left.forwardslash.chevron.right"
        case "qoder", "qodercn": return "q.circle.fill"
        default: return "circle.grid.2x2"
        }
    }

    static func providerTitle(_ provider: String) -> String {
        switch provider {
        case "bailian": return "阿里云百炼 Token Plan"
        case "claudecode": return "Claude Code 订阅"
        case "codex": return "ChatGPT Codex 订阅"
        case "qoder": return "Qoder（国际版）"
        case "qodercn": return "Qoder CN（国内版）"
        default: return provider
        }
    }

    static func dataTime(_ date: Date, now: Date) -> String {
        if Calendar.current.isDate(date, inSameDayAs: now) {
            return date.formatted(date: .omitted, time: .shortened)
        }
        let f = DateFormatter()
        f.dateFormat = "M/d HH:mm"
        return f.string(from: date)
    }

    static func providerDescription(_ provider: String) -> String {
        switch provider {
        case "bailian": return "用阿里云官方命令行工具 bl 查询 Token Plan 用量。需要先安装 bailian-cli，并登录百炼控制台。"
        case "claudecode": return "读取 Claude Code 状态栏传来的订阅用量（Pro / Max）。需要把 Claude Code 的状态栏命令设为 liquoten。"
        case "codex": return "读取 Codex 本地会话记录（~/.codex/sessions）里的订阅用量和 credits 余额。只在使用 Codex 时更新，无需额外配置。"
        case "qoder": return "用个人访问令牌查询 qoder.com 账号本计费周期的 Credits 用量和加购余额。开启时需要填写 API Key。"
        case "qodercn": return "用个人访问令牌查询 qoder.com.cn 账号本计费周期的 Credits 用量和加购余额。开启时需要填写 API Key。"
        default: return ""
        }
    }

    static func claudeStatusLineSnippet() -> String {
        let path = Bundle.main.resourceURL?.appendingPathComponent("liquoten").path
            ?? "/Applications/Liquoten.app/Contents/Resources/liquoten"
        return """
        "statusLine": {
          "type": "command",
          "command": "\(path) statusline"
        }
        """
    }

    static func historyLabel(_ section: String) -> String {
        switch section {
        case "1-week quota": return "近 7 天"
        case "1-month quota": return "近 30 天"
        default: return "近 24 小时"
        }
    }

    static func historyWindow(_ section: String) -> TimeInterval {
        switch section {
        case "1-week quota": return 7 * 86400
        case "1-month quota": return 30 * 86400
        default: return 86400
        }
    }

    static func countdown(to date: Date, now: Date) -> String {
        let secs = max(0, Int(date.timeIntervalSince(now)))
        let d = secs / 86400, h = (secs % 86400) / 3600, m = (secs % 3600) / 60
        if d > 0 { return "\(d) 天 \(h) 小时后重置" }
        if h > 0 { return "\(h) 小时 \(m) 分后重置" }
        return "\(m) 分钟后重置"
    }

    static func resetChip(_ date: Date, now: Date) -> String {
        let cal = Calendar.current
        let f = DateFormatter()
        f.dateFormat = "HH:mm"
        let time = f.string(from: date)
        if cal.isDate(date, inSameDayAs: now) { return "今天 \(time)" }
        if let tomorrow = cal.date(byAdding: .day, value: 1, to: now), cal.isDate(date, inSameDayAs: tomorrow) {
            return "明天 \(time)"
        }
        f.dateFormat = "M/d HH:mm"
        return f.string(from: date)
    }

    static func friendlyError(_ provider: String, _ err: String) -> String {
        if err.contains("No console access token") { return "还没有登录百炼控制台，请点下方按钮登录。" }
        if err.contains("command not found") || err.contains("executable file not found") {
            return "找不到 bl 命令，请安装 bailian-cli 或在配置里写 bl 的完整路径。"
        }
        if let r = err.range(of: #""message":\s*"([^"]*)""#, options: .regularExpression) {
            return String(err[r]).replacingOccurrences(of: #"^"message":\s*""#, with: "", options: .regularExpression)
                .trimmingCharacters(in: CharacterSet(charactersIn: "\""))
        }
        return err.count > 160 ? String(err.prefix(160)) + "…" : err
    }
}

struct Chip: View {
    let text: String
    var tint: Color = .secondary

    var body: some View {
        Text(text)
            .font(.caption.weight(.medium))
            .foregroundStyle(tint)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(Capsule().fill(Color.primary.opacity(0.08)))
    }
}

struct Card<Content: View>: View {
    var tint: Color = .primary
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 8) { content }
            .padding(12)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(RoundedRectangle(cornerRadius: 12).fill(tint.opacity(0.07)))
            .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(tint.opacity(0.10)))
    }
}

struct HeaderView: View {
    @ObservedObject var model: DashboardModel

    var body: some View {
        let peak = model.peakUsage
        let tint = peak.map(Style.color) ?? .secondary
        VStack(alignment: .leading, spacing: 8) {
            Text("所有额度中最高已用").font(.caption).foregroundStyle(.secondary)
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Image(systemName: "gauge.with.dots.needle.33percent")
                    .font(.title2)
                    .foregroundStyle(tint)
                Text(peak.map { "\(Int($0.rounded()))" } ?? "--")
                    .font(.system(size: 36, weight: .bold, design: .rounded))
                if peak != nil {
                    Text("%").font(.title3).foregroundStyle(.secondary)
                }
                Text(Style.status(peak)).font(.title3.weight(.medium)).foregroundStyle(tint)
            }
            HStack(spacing: 6) {
                Chip(text: "\(model.balances.count) 个服务")
                if let t = model.lastUpdated {
                    Chip(text: "更新于 " + t.formatted(date: .omitted, time: .shortened))
                }
                if model.errorCount > 0 {
                    Chip(text: "\(model.errorCount) 个出错", tint: .red)
                }
            }
        }
    }
}

struct SectionCard: View {
    let provider: String
    let section: SectionState
    let points: [HistoryPoint]
    let now: Date

    var body: some View {
        let unlimited = section.usedPct < 0
        let tint = unlimited ? Color.green : Style.color(section.usedPct)
        Card {
            HStack(spacing: 5) {
                Circle().fill(tint).frame(width: 7, height: 7)
                Text(AppDelegate.sectionTitle(section.name))
                    .font(.callout.weight(.medium))
                    .lineLimit(1)
            }
            HStack(alignment: .firstTextBaseline, spacing: 3) {
                if unlimited {
                    Text("无限制").font(.system(size: 26, weight: .semibold, design: .rounded))
                } else {
                    Text("\(Int(section.usedPct.rounded()))")
                        .font(.system(size: 30, weight: .semibold, design: .rounded))
                    Text("%").font(.callout).foregroundStyle(.secondary)
                    Text("已用").font(.caption).foregroundStyle(.secondary)
                }
            }
            .lineLimit(1)
            .fixedSize()
            if !unlimited {
                HStack(spacing: 4) {
                    Text(chartLabel)
                    Spacer(minLength: 4)
                    if let reset = section.resetAt, reset > now {
                        Text(resetLabel(reset) + " 重置")
                    }
                }
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                chart
                UsageBar(pct: section.usedPct, color: tint)
            }
            Text(footer).font(.caption).foregroundStyle(.secondary).lineLimit(1)
        }
    }

    private var window: (start: Date, end: Date) {
        let span: TimeInterval = section.name == "1-week quota" ? 7 * 86400 : 5 * 3600
        if section.name == "1-month quota" {
            let cal = Calendar.current
            if let reset = section.resetAt, reset > now, let start = cal.date(byAdding: .month, value: -1, to: reset) {
                return (start, reset)
            }
            let start = cal.startOfDay(for: now).addingTimeInterval(-29 * 86400)
            return (start, start.addingTimeInterval(30 * 86400))
        }
        if let reset = section.resetAt, reset > now {
            return (reset.addingTimeInterval(-span), reset)
        }
        if section.name == "1-week quota" {
            let today = Calendar.current.startOfDay(for: now)
            let start = today.addingTimeInterval(-6 * 86400)
            return (start, start.addingTimeInterval(span))
        }
        return (now.addingTimeInterval(-span), now)
    }

    private var chartLabel: String {
        switch section.name {
        case "5-hour quota":
            return window.start.formatted(date: .omitted, time: .shortened) + " 开始"
        case "1-week quota", "1-month quota":
            return "每天用量"
        default:
            return Style.historyLabel(section.name)
        }
    }

    @ViewBuilder private var chart: some View {
        let tint = Style.color(section.usedPct)
        switch section.name {
        case "5-hour quota":
            MinuteLine(points: points, start: window.start, end: window.end, now: now,
                       current: section.usedPct, color: tint)
                .frame(height: 76)
        case "1-week quota":
            DayBars(points: points, start: window.start, days: 7, now: now, current: section.usedPct,
                    color: tint, compact: false)
                .frame(height: 76)
        case "1-month quota":
            DayBars(points: points, start: window.start,
                    days: max(1, Int((window.end.timeIntervalSince(window.start) / 86400).rounded())),
                    now: now, current: section.usedPct, color: tint, compact: true)
                .frame(height: 46)
        default:
            Sparkline(points: points, window: Style.historyWindow(section.name), now: now, color: tint)
                .frame(height: 30)
        }
    }

    private func resetLabel(_ reset: Date) -> String {
        let f = DateFormatter()
        switch section.name {
        case "5-hour quota":
            f.dateFormat = "HH:mm"
        case "1-week quota":
            f.locale = Locale(identifier: "zh_CN")
            f.dateFormat = "EEE HH:mm"
        default:
            return Style.resetChip(reset, now: now)
        }
        return f.string(from: reset)
    }

    private var footer: String {
        var parts: [String] = []
        if section.usedPct >= 0 {
            parts.append("剩余 \(Int((100 - section.usedPct).rounded()))%")
        }
        if let reset = section.resetAt, reset > now {
            parts.append(Style.countdown(to: reset, now: now))
        }
        return parts.isEmpty ? " " : parts.joined(separator: " · ")
    }
}

struct ProviderHeader: View {
    let provider: String
    var extras: [SectionState] = []

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: Style.providerIcon(provider)).foregroundStyle(.secondary)
            Text(Style.providerTitle(provider)).font(.callout.weight(.semibold)).foregroundStyle(.secondary)
            Spacer(minLength: 4)
            ForEach(extras, id: \.name) { e in
                Chip(text: "\(AppDelegate.sectionTitle(e.name)) \(e.remaining.formatted(.number.precision(.fractionLength(0...2))))")
                    .fixedSize()
            }
        }
        .padding(.top, 2)
    }
}

struct UsageBar: View {
    let pct: Double
    let color: Color

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .leading) {
                Capsule().fill(Color.primary.opacity(0.10))
                Capsule().fill(color.gradient)
                    .frame(width: max(pct > 0 ? 6 : 0, geo.size.width * min(max(pct, 0), 100) / 100))
            }
        }
        .frame(height: 6)
    }
}

struct Sparkline: View {
    let points: [HistoryPoint]
    let window: TimeInterval
    let now: Date
    let color: Color

    var body: some View {
        GeometryReader { geo in
            let start = now.addingTimeInterval(-window)
            let visible = points.filter { $0.time >= start }
            if visible.count < 2 {
                Text("历史数据积累中").font(.caption2).foregroundStyle(.tertiary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                let pts = visible.map { p -> CGPoint in
                    let x = geo.size.width * CGFloat(p.time.timeIntervalSince(start) / window)
                    let y = geo.size.height * (1 - CGFloat(min(max(p.usedPct, 0), 100) / 100))
                    return CGPoint(x: x, y: max(1, min(geo.size.height - 1, y)))
                }
                ZStack {
                    Path { path in
                        path.move(to: CGPoint(x: pts[0].x, y: geo.size.height))
                        pts.forEach { path.addLine(to: $0) }
                        path.addLine(to: CGPoint(x: pts[pts.count - 1].x, y: geo.size.height))
                        path.closeSubpath()
                    }
                    .fill(LinearGradient(colors: [color.opacity(0.35), color.opacity(0.02)], startPoint: .top, endPoint: .bottom))
                    Path { path in
                        path.move(to: pts[0])
                        pts.dropFirst().forEach { path.addLine(to: $0) }
                    }
                    .stroke(color, style: StrokeStyle(lineWidth: 1.5, lineJoin: .round))
                }
            }
        }
    }
}

struct ErrorCard: View {
    @ObservedObject var model: DashboardModel
    let balance: BalanceState
    let message: String

    var body: some View {
        Card(tint: .red) {
            HStack(spacing: 5) {
                Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.red)
                Text("\(AppDelegate.displayName(balance.provider)) 查询出错").font(.callout.weight(.medium))
                Spacer()
                Chip(text: "出错", tint: .red)
            }
            Text(Style.friendlyError(balance.provider, message))
                .font(.callout)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)
            if balance.provider == "bailian" {
                if model.loggingIn {
                    HStack(spacing: 6) {
                        ProgressView().controlSize(.small)
                        Text("等待浏览器完成登录…").font(.callout).foregroundStyle(.secondary)
                    }
                } else {
                    HStack {
                        Button("登录国内站") { model.onLogin("domestic") }
                        Button("登录国际站") { model.onLogin("international") }
                    }
                    .controlSize(.small)
                }
            }
        }
    }
}

struct NoticeCard: View {
    let icon: String
    let text: String

    var body: some View {
        Card {
            HStack(spacing: 8) {
                Image(systemName: icon).foregroundStyle(.secondary)
                Text(text).font(.callout).foregroundStyle(.secondary)
            }
        }
    }
}

struct UpdateBanner: View {
    @ObservedObject var model: DashboardModel
    let update: AvailableUpdate

    var body: some View {
        Card(tint: .blue) {
            HStack(spacing: 8) {
                Image(systemName: "arrow.down.circle.fill").foregroundStyle(.blue)
                Text("有新版本 v\(update.version)").font(.callout.weight(.medium))
                Spacer()
                Text("当前 v\(Updater.currentVersion)").font(.caption).foregroundStyle(.secondary)
            }
            HStack(spacing: 10) {
                Button("下载") { model.onOpenURL(update.url) }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                Button("跳过此版本", action: model.onSkipUpdate)
                    .buttonStyle(.borderless)
                    .controlSize(.small)
            }
        }
    }
}

struct FooterBar: View {
    @ObservedObject var model: DashboardModel

    var body: some View {
        HStack(spacing: 14) {
            Button(action: model.onRefresh) {
                Label("刷新", systemImage: "arrow.clockwise")
            }
            .buttonStyle(.borderless)

            Button { model.showingServices = true } label: {
                Label("服务", systemImage: "square.grid.2x2")
            }
            .buttonStyle(.borderless)

            Menu {
                ForEach(AppDelegate.consoles.filter { c in model.balances.contains { $0.provider == c.provider } }, id: \.provider) { c in
                    Button(c.title) {
                        if let url = URL(string: c.url) { model.onOpenURL(url) }
                    }
                }
                Divider()
                Button("打开数据目录", action: model.onOpenDataDir)
            } label: {
                Label("打开", systemImage: "safari")
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()

            Spacer()

            Menu {
                Toggle("开机自动启动", isOn: Binding(
                    get: { model.loginItemEnabled },
                    set: { _ in model.onToggleLoginItem() }))
                Button(model.checkingUpdate ? "正在检查更新…" : "检查更新…", action: model.onCheckUpdate)
                    .disabled(model.checkingUpdate)
                Text("当前版本 v\(Updater.currentVersion)")
                Divider()
                Button("退出 Liquoten", action: model.onQuit)
            } label: {
                Image(systemName: "gearshape")
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
        }
        .font(.callout)
        .padding(.top, 2)
    }
}

struct ServicesView: View {
    @ObservedObject var model: DashboardModel

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Button { model.showingServices = false } label: {
                    Label("返回", systemImage: "chevron.left")
                }
                .buttonStyle(.borderless)
                Spacer()
                Text("选择要监控的服务").font(.headline)
                Spacer()
                Label("返回", systemImage: "chevron.left").hidden()
            }
            if !model.providersLoaded {
                NoticeCard(icon: "hourglass", text: "正在读取服务列表…")
            } else if model.providers.isEmpty {
                NoticeCard(icon: "xmark.octagon", text: "读取服务列表失败，请重新安装 Liquoten.app")
            }
            ForEach(model.orderedProviders, id: \.name) { info in
                ServiceRow(model: model, info: info)
                    .opacity(model.draggingProvider == info.name ? 0.4 : 1)
                    .onDrop(of: [.text], delegate: ReorderDropDelegate(model: model, target: info.name))
            }
            Text("按住左侧手柄上下拖动调整顺序，面板按此顺序显示，排在最上面的服务用量会显示在菜单栏。开关会写入配置文件 ~/.config/liquoten/config.toml，并立即重新查询。")
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .onDrop(of: [.text], isTargeted: nil) { _ in
            model.draggingProvider = nil
            return true
        }
        .onDisappear { model.draggingProvider = nil }
    }
}

struct ServiceRow: View {
    @ObservedObject var model: DashboardModel
    let info: ProviderInfo
    @State private var copied = false

    var body: some View {
        Card {
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: "line.3.horizontal")
                    .font(.callout)
                    .foregroundStyle(.tertiary)
                    .frame(width: 14, height: 22)
                    .contentShape(Rectangle())
                    .help("拖动调整顺序")
                    .onDrag {
                        model.draggingProvider = info.name
                        return NSItemProvider(object: info.name as NSString)
                    }
                Image(systemName: Style.providerIcon(info.name))
                    .font(.title3)
                    .foregroundStyle(info.enabled ? Color.accentColor : .secondary)
                    .frame(width: 22)
                VStack(alignment: .leading, spacing: 4) {
                    Text(Style.providerTitle(info.name)).font(.callout.weight(.semibold))
                    Text(Style.providerDescription(info.name))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer(minLength: 8)
                if model.togglingProvider == info.name {
                    ProgressView().controlSize(.small)
                } else {
                    Toggle("", isOn: Binding(
                        get: { info.enabled },
                        set: { model.onSetEnabled(info.name, $0) }))
                        .toggleStyle(.switch)
                        .labelsHidden()
                        .controlSize(.small)
                        .disabled(model.togglingProvider != nil)
                }
            }
            if model.togglingProvider == info.name, let text = model.busyText {
                Text(text).font(.caption).foregroundStyle(.secondary)
            }
            if info.enabled {
                status
                actions
            }
        }
    }

    @ViewBuilder private var status: some View {
        if let b = model.balance(info.name) {
            if let err = b.error, !err.isEmpty {
                Label(Style.friendlyError(info.name, err), systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.red)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                Label("已连接" + (b.fetchedAt.map { " · 数据时间 " + Style.dataTime($0, now: model.now) } ?? ""),
                      systemImage: "checkmark.circle.fill")
                    .font(.caption)
                    .foregroundStyle(.green)
            }
        } else {
            Label("等待查询…", systemImage: "hourglass").font(.caption).foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private var actions: some View {
        switch info.name {
        case "bailian":
            if model.loggingIn {
                HStack(spacing: 6) {
                    ProgressView().controlSize(.small)
                    Text("等待浏览器完成登录…").font(.caption).foregroundStyle(.secondary)
                }
            } else {
                HStack {
                    Button("登录国内站") { model.onLogin("domestic") }
                    Button("登录国际站") { model.onLogin("international") }
                }
                .controlSize(.small)
            }
        case "claudecode":
            VStack(alignment: .leading, spacing: 4) {
                Button(copied ? "已复制" : "复制状态栏配置") {
                    let pb = NSPasteboard.general
                    pb.clearContents()
                    pb.setString(Style.claudeStatusLineSnippet(), forType: .string)
                    copied = true
                }
                .controlSize(.small)
                Text("粘贴到 ~/.claude/settings.json 里，然后在 Claude Code 里发一句话即可有数据。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        default:
            EmptyView()
        }
    }
}

struct ReorderDropDelegate: DropDelegate {
    let model: DashboardModel
    let target: String

    func dropEntered(info: DropInfo) {
        guard let dragging = model.draggingProvider else { return }
        withAnimation(.easeInOut(duration: 0.15)) { model.move(dragging, to: target) }
    }

    func dropUpdated(info: DropInfo) -> DropProposal? {
        DropProposal(operation: .move)
    }

    func performDrop(info: DropInfo) -> Bool {
        model.draggingProvider = nil
        return true
    }
}

struct MinuteLine: View {
    let points: [HistoryPoint]
    let start: Date
    let end: Date
    let now: Date
    let current: Double
    let color: Color

    var body: some View {
        GeometryReader { geo in
            let gutter: CGFloat = 24
            let w = geo.size.width, h = geo.size.height
            let span = max(end.timeIntervalSince(start), 60)
            let inWindow = points.filter { $0.time > start && $0.time <= now }
            let x = { (t: Date) -> CGFloat in gutter + (w - gutter) * CGFloat(min(max(t.timeIntervalSince(start) / span, 0), 1)) }
            let y = { (v: Double) -> CGFloat in h - 1 - (h - 6) * CGFloat(min(max(v, 0), 100) / 100) }
            let steps: [CGPoint] = {
                var out = [CGPoint(x: x(start), y: y(0))]
                var last = 0.0
                for p in inWindow {
                    out.append(CGPoint(x: x(p.time), y: y(last)))
                    out.append(CGPoint(x: x(p.time), y: y(p.usedPct)))
                    last = p.usedPct
                }
                out.append(CGPoint(x: x(now), y: y(last)))
                out.append(CGPoint(x: x(now), y: y(current)))
                return out
            }()
            ZStack(alignment: .topLeading) {
                ForEach(1..<Int(span / 3600) + 1, id: \.self) { k in
                    let tx = x(start.addingTimeInterval(Double(k) * 3600))
                    Path { p in
                        p.move(to: CGPoint(x: tx, y: y(100)))
                        p.addLine(to: CGPoint(x: tx, y: y(0)))
                    }
                    .stroke(Color.primary.opacity(0.06), lineWidth: 1)
                }
                ForEach([20, 40, 60, 80, 100], id: \.self) { v in
                    Path { p in
                        p.move(to: CGPoint(x: gutter, y: y(Double(v))))
                        p.addLine(to: CGPoint(x: w, y: y(Double(v))))
                    }
                    .stroke(Color.primary.opacity(0.12), lineWidth: 0.5)
                    Text("\(v)%")
                        .font(.system(size: 8))
                        .foregroundStyle(.tertiary)
                        .fixedSize()
                        .frame(width: gutter - 4, alignment: .trailing)
                        .position(x: (gutter - 4) / 2, y: y(Double(v)))
                }
                Path { p in
                    p.move(to: CGPoint(x: x(now), y: y(0)))
                    p.addLine(to: CGPoint(x: w, y: y(0)))
                }
                .stroke(Color.primary.opacity(0.2), style: StrokeStyle(lineWidth: 1, dash: [2, 3]))
                Path { p in
                    p.move(to: CGPoint(x: steps[0].x, y: h))
                    steps.forEach { p.addLine(to: $0) }
                    p.addLine(to: CGPoint(x: steps[steps.count - 1].x, y: h))
                    p.closeSubpath()
                }
                .fill(LinearGradient(colors: [color.opacity(0.35), color.opacity(0.03)], startPoint: .top, endPoint: .bottom))
                Path { p in
                    p.move(to: steps[0])
                    steps.dropFirst().forEach { p.addLine(to: $0) }
                }
                .stroke(color, style: StrokeStyle(lineWidth: 1.5, lineJoin: .round))
                Circle().fill(color).frame(width: 5, height: 5)
                    .position(x: x(now), y: y(current))
            }
        }
    }
}

struct DayBars: View {
    let points: [HistoryPoint]
    let start: Date
    let days: Int
    let now: Date
    let current: Double
    let color: Color
    let compact: Bool

    private struct Slot {
        let label: String
        let isToday: Bool
        let isFuture: Bool
        let delta: Double?
    }

    private var slots: [Slot] {
        let names = ["日", "一", "二", "三", "四", "五", "六"]
        let cal = Calendar.current
        let hasBefore = points.contains { $0.time < start }
        var ends: [Double?] = []
        var out: [Slot] = []
        for i in 0..<days {
            let s = start.addingTimeInterval(Double(i) * 86400)
            let e = s.addingTimeInterval(86400)
            let label = names[cal.component(.weekday, from: s) - 1]
            if s > now {
                ends.append(nil)
                out.append(Slot(label: label, isToday: false, isFuture: true, delta: nil))
                continue
            }
            let isToday = e > now
            var end: Double?
            if isToday {
                end = current
            } else if let p = points.last(where: { $0.time >= start && $0.time < e }) {
                end = p.usedPct
            } else if hasBefore {
                end = 0
            }
            let base: Double? = i == 0 ? 0 : ends[i - 1]
            ends.append(end)
            var delta: Double?
            if let end, let base { delta = max(0, end - base) }
            out.append(Slot(label: label, isToday: isToday, isFuture: false, delta: delta))
        }
        return out
    }

    var body: some View {
        let slots = self.slots
        let peak = max(1, slots.compactMap(\.delta).max() ?? 1)
        GeometryReader { geo in
            let labelH: CGFloat = 11, valueH: CGFloat = compact ? 0 : 10
            let barArea = geo.size.height - labelH - valueH - 1
            VStack(spacing: 1) {
                HStack(alignment: .bottom, spacing: compact ? 2 : 4) {
                    ForEach(Array(slots.enumerated()), id: \.offset) { _, slot in
                        VStack(spacing: 1) {
                            if !compact {
                                Text(slot.delta.map { $0 >= 0.5 ? "\(Int($0.rounded()))%" : "" } ?? (slot.isFuture ? "" : "?"))
                                    .font(.system(size: 8))
                                    .lineLimit(1)
                                    .fixedSize()
                                    .foregroundStyle(.secondary)
                                    .frame(height: valueH)
                            }
                            ZStack(alignment: .bottom) {
                                RoundedRectangle(cornerRadius: compact ? 1.5 : 2)
                                    .fill(Color.primary.opacity(slot.delta == nil && !slot.isFuture ? 0.03 : 0.06))
                                if let d = slot.delta, d > 0 {
                                    RoundedRectangle(cornerRadius: compact ? 1.5 : 2)
                                        .fill(color.opacity(slot.isToday ? 1 : 0.55))
                                        .frame(height: max(2, barArea * CGFloat(d / peak)))
                                }
                                if slot.isToday {
                                    RoundedRectangle(cornerRadius: compact ? 1.5 : 2)
                                        .strokeBorder(color.opacity(0.6), lineWidth: 1)
                                }
                            }
                            .frame(height: barArea)
                            if !compact {
                                Text(slot.label)
                                    .font(.system(size: 9, weight: slot.isToday ? .bold : .regular))
                                    .foregroundStyle(slot.isToday ? .primary : .secondary)
                                    .frame(height: labelH)
                            }
                        }
                        .frame(maxWidth: .infinity)
                    }
                }
                if compact {
                    HStack {
                        Text(start.formatted(.dateTime.month(.defaultDigits).day()))
                        Spacer()
                        if let peakDay = slots.compactMap(\.delta).max(), peakDay >= 0.5 {
                            Text("单日最多 \(Int(peakDay.rounded()))%")
                            Spacer()
                        }
                        Text(start.addingTimeInterval(Double(days - 1) * 86400).formatted(.dateTime.month(.defaultDigits).day()))
                    }
                    .font(.system(size: 9))
                    .foregroundStyle(.secondary)
                    .frame(height: labelH)
                }
            }
        }
    }
}
