import Foundation

struct AvailableUpdate: Equatable {
    let version: String
    let url: URL
}

// 查询 GitHub Releases 最新正式版，与当前版本比较；只提示，不自动下载安装。
// 不走 api.github.com（匿名每小时 60 次，共用出口 IP 时容易 403），而是读
// releases/latest 的 302 跳转地址 …/releases/tag/vX.Y.Z 得到最新版本号。
final class Updater: NSObject, URLSessionTaskDelegate {
    static let releasesPage = URL(string: "https://github.com/hi-origin/liquoten/releases/latest")!
    static let interval: TimeInterval = 24 * 3600

    private let defaults = UserDefaults.standard
    private let lastCheckKey = "update.lastCheck"
    private let skippedKey = "update.skippedVersion"

    static var currentVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0"
    }

    var skippedVersion: String? {
        get { defaults.string(forKey: skippedKey) }
        set { defaults.set(newValue, forKey: skippedKey) }
    }

    var isDue: Bool {
        guard let last = defaults.object(forKey: lastCheckKey) as? Date else { return true }
        return Date().timeIntervalSince(last) >= Self.interval
    }

    enum Result {
        case available(AvailableUpdate)
        case upToDate(String)
        case failed(String)
    }

    private lazy var session = URLSession(configuration: .ephemeral, delegate: self, delegateQueue: nil)

    func check(completion: @escaping (Result) -> Void) {
        var req = URLRequest(url: Self.releasesPage, timeoutInterval: 20)
        req.httpMethod = "HEAD"
        req.setValue("Liquoten/\(Self.currentVersion)", forHTTPHeaderField: "User-Agent")
        session.dataTask(with: req) { [weak self] _, resp, err in
            let result: Result
            let http = resp as? HTTPURLResponse
            if let err {
                result = .failed(err.localizedDescription)
            } else if let location = http?.value(forHTTPHeaderField: "Location"),
                      let url = URL(string: location, relativeTo: Self.releasesPage)?.absoluteURL,
                      let latest = Self.version(fromTagURL: url) {
                self?.defaults.set(Date(), forKey: self?.lastCheckKey ?? "")
                if Self.isNewer(latest, than: Self.currentVersion) {
                    result = .available(AvailableUpdate(version: latest, url: url))
                } else {
                    result = .upToDate(latest)
                }
            } else {
                result = .failed("GitHub 返回 HTTP \(http?.statusCode ?? 0)，没有拿到最新版本")
            }
            DispatchQueue.main.async { completion(result) }
        }.resume()
    }

    // 不跟随跳转，直接读 Location。
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }

    // …/releases/tag/v1.0.5 → "1.0.5"
    static func version(fromTagURL url: URL) -> String? {
        let parts = url.pathComponents
        guard let i = parts.firstIndex(of: "tag"), i + 1 < parts.count else { return nil }
        let v = normalize(parts[i + 1])
        return v.first?.isNumber == true ? v : nil
    }

    static func normalize(_ tag: String) -> String {
        tag.hasPrefix("v") || tag.hasPrefix("V") ? String(tag.dropFirst()) : tag
    }

    // 按数字逐段比较，忽略 "-beta" 之类的后缀。
    static func isNewer(_ a: String, than b: String) -> Bool {
        func parts(_ s: String) -> [Int] {
            normalize(s).split(separator: "-").first.map {
                $0.split(separator: ".").map { Int($0) ?? 0 }
            } ?? []
        }
        let x = parts(a), y = parts(b)
        for i in 0..<max(x.count, y.count) {
            let l = i < x.count ? x[i] : 0, r = i < y.count ? y[i] : 0
            if l != r { return l > r }
        }
        return false
    }
}
