import Foundation

struct AvailableUpdate: Equatable {
    let version: String
    let url: URL
}

// 查询 GitHub Releases 最新正式版，与当前版本比较；只提示，不自动下载安装。
final class Updater {
    static let releasesAPI = URL(string: "https://api.github.com/repos/hi-origin/liquoten/releases/latest")!
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

    func check(completion: @escaping (Result) -> Void) {
        var req = URLRequest(url: Self.releasesAPI, timeoutInterval: 20)
        req.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        req.setValue("Liquoten/\(Self.currentVersion)", forHTTPHeaderField: "User-Agent")
        URLSession.shared.dataTask(with: req) { [weak self] data, resp, err in
            let result: Result
            if let err {
                result = .failed(err.localizedDescription)
            } else if let http = resp as? HTTPURLResponse, http.statusCode != 200 {
                result = .failed("GitHub 返回 HTTP \(http.statusCode)")
            } else if let data, let release = try? JSONDecoder().decode(Release.self, from: data) {
                self?.defaults.set(Date(), forKey: self?.lastCheckKey ?? "")
                let latest = Self.normalize(release.tagName)
                if Self.isNewer(latest, than: Self.currentVersion) {
                    result = .available(AvailableUpdate(version: latest, url: URL(string: release.htmlURL) ?? Self.releasesPage))
                } else {
                    result = .upToDate(latest)
                }
            } else {
                result = .failed("无法解析 GitHub 返回的版本信息")
            }
            DispatchQueue.main.async { completion(result) }
        }.resume()
    }

    private struct Release: Decodable {
        let tagName: String
        let htmlURL: String
        enum CodingKeys: String, CodingKey {
            case tagName = "tag_name"
            case htmlURL = "html_url"
        }
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
