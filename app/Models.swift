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
