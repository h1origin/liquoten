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
