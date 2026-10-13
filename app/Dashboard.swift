import SwiftUI

final class DashboardModel: ObservableObject {
    @Published var balances: [BalanceState] = []
    @Published var history: [String: [HistoryPoint]] = [:]
    @Published var loggingIn = false
    @Published var loginItemEnabled = false
    @Published var daemonMissing = false
    @Published var now = Date()

    var onRefresh: () -> Void = {}
    var onLogin: (String) -> Void = { _ in }
    var onOpenURL: (URL) -> Void = { _ in }
    var onOpenDataDir: () -> Void = {}
    var onToggleLoginItem: () -> Void = {}
    var onQuit: () -> Void = {}

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
            HeaderView(model: model)
            content
            FooterBar(model: model)
        }
        .padding(14)
        .frame(width: 380)
    }

    @ViewBuilder private var content: some View {
        if model.daemonMissing {
            NoticeCard(icon: "xmark.octagon", text: "daemon 缺失，请重新安装 Liquoten.app")
        } else if model.balances.isEmpty {
            NoticeCard(icon: "hourglass", text: "等待首次查询…")
        } else {
            ForEach(model.balances, id: \.provider) { b in
                if let err = b.error, !err.isEmpty {
                    ErrorCard(model: model, balance: b, message: err)
                } else {
                    ProviderHeader(provider: b.provider)
                    let columns = Array(repeating: GridItem(.flexible(), spacing: 10), count: max(1, min(b.sections.count, 2)))
                    LazyVGrid(columns: columns, spacing: 10) {
                        ForEach(b.sections, id: \.name) { s in
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
        default: return "circle.grid.2x2"
        }
    }

    static func providerTitle(_ provider: String) -> String {
        switch provider {
        case "bailian": return "阿里云百炼 Token Plan"
        case "claudecode": return "Claude Code 订阅"
        default: return provider
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
                Text("%").font(.title3).foregroundStyle(.secondary)
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
                Spacer(minLength: 4)
                if let reset = section.resetAt, reset > now {
                    Chip(text: Style.resetChip(reset, now: now)).fixedSize()
                }
            }
            if !unlimited {
                Sparkline(points: points, window: Style.historyWindow(section.name), now: now, color: tint)
                    .frame(height: 30)
                UsageBar(pct: section.usedPct, color: tint)
            }
            Text(footer).font(.caption).foregroundStyle(.secondary).lineLimit(1)
        }
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

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: Style.providerIcon(provider)).foregroundStyle(.secondary)
            Text(Style.providerTitle(provider)).font(.callout.weight(.semibold)).foregroundStyle(.secondary)
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

struct FooterBar: View {
    @ObservedObject var model: DashboardModel

    var body: some View {
        HStack(spacing: 14) {
            Button(action: model.onRefresh) {
                Label("刷新", systemImage: "arrow.clockwise")
            }
            .buttonStyle(.borderless)

            if model.hasBailian {
                Menu {
                    Button("国内站") { model.onLogin("domestic") }
                    Button("国际站") { model.onLogin("international") }
                } label: {
                    Label(model.loggingIn ? "登录中…" : "登录百炼", systemImage: "person.crop.circle.badge.checkmark")
                }
                .disabled(model.loggingIn)
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
            }

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
