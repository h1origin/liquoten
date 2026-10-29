import Foundation

// 与 daemon 落盘的 state.json（store.WriteState）对应的解码模型。
struct SectionState: Decodable {
    let name: String
    let usedPct: Double
    let remaining: Double
    let resetAt: Date?
}

struct BalanceState: Decodable {
    let provider: String
    let sections: [SectionState]
    let fetchedAt: Date?
    let error: String?

    // 查询失败时 daemon 写出 "sections": null，按空数组处理，避免整份 state 解码失败。
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        provider = try c.decode(String.self, forKey: .provider)
        sections = try c.decodeIfPresent([SectionState].self, forKey: .sections) ?? []
        fetchedAt = try c.decodeIfPresent(Date.self, forKey: .fetchedAt)
        error = try c.decodeIfPresent(String.self, forKey: .error)
    }

    private enum CodingKeys: String, CodingKey {
        case provider, sections, fetchedAt, error
    }
}

// notifications.jsonl 里的一行（alert.FileNotifier 写入）。
struct NotifEvent: Decodable {
    let id: String
    let title: String
    let body: String
    let at: Int64
}

// Go time.Time 序列化为 RFC3339（带纳秒），ISO8601DateFormatter 需要分两档尝试。
func liquotenDecoder() -> JSONDecoder {
    let d = JSONDecoder()
    d.keyDecodingStrategy = .convertFromSnakeCase
    d.dateDecodingStrategy = .custom { decoder in
        let s = try decoder.singleValueContainer().decode(String.self)
        let withFrac = ISO8601DateFormatter()
        withFrac.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = withFrac.date(from: s) { return date }
        let plain = ISO8601DateFormatter()
        if let date = plain.date(from: s) { return date }
        throw DecodingError.dataCorrupted(.init(
            codingPath: decoder.codingPath,
            debugDescription: "无法解析时间: \(s)"))
    }
    return d
}

struct HistoryPoint {
    let time: Date
    let usedPct: Double
}

private struct HistoryEntry: Decodable {
    let time: Date
    let provider: String
    let sections: [SectionState]?
}

enum HistoryReader {
    static func read(since: Date) -> [String: [HistoryPoint]] {
        let url = URL(fileURLWithPath: DaemonManager.cacheDir).appendingPathComponent("history.jsonl")
        guard let text = try? String(contentsOf: url, encoding: .utf8) else { return [:] }
        let decoder = liquotenDecoder()
        var out: [String: [HistoryPoint]] = [:]
        for line in text.split(separator: "\n") {
            guard let e = try? decoder.decode(HistoryEntry.self, from: Data(line.utf8)), e.time >= since else { continue }
            for s in e.sections ?? [] where s.usedPct >= 0 {
                out[key(e.provider, s.name), default: []].append(HistoryPoint(time: e.time, usedPct: s.usedPct))
            }
        }
        return out
    }

    static func key(_ provider: String, _ section: String) -> String {
        "\(provider)/\(section)"
    }
}

struct ProviderInfo: Decodable {
    let name: String
    let enabled: Bool
    var requiresApiKey = false
    var accessKeySource = ""
    var accessKeyMasked = ""

    var hasAccessKey: Bool { !accessKeySource.isEmpty }

    private enum CodingKeys: String, CodingKey {
        case name, enabled
        case requiresApiKey = "requires_api_key"
        case accessKeySource = "access_key_source"
        case accessKeyMasked = "access_key_masked"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        enabled = try c.decode(Bool.self, forKey: .enabled)
        requiresApiKey = try c.decodeIfPresent(Bool.self, forKey: .requiresApiKey) ?? false
        accessKeySource = try c.decodeIfPresent(String.self, forKey: .accessKeySource) ?? ""
        accessKeyMasked = try c.decodeIfPresent(String.self, forKey: .accessKeyMasked) ?? ""
    }
}
