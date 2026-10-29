import CryptoKit
import Foundation

enum InstallerError: LocalizedError {
    case invalidDownloadURL
    case downloadFailed(String)
    case invalidSHAFile
    case checksumMismatch
    case cannotUpdate
    case commandFailed(String)
    case appNotFound
    case invalidBundle(String)
    case replaceFailed(String)
    case relaunchFailed(String)

    var errorDescription: String? {
        switch self {
        case .invalidDownloadURL:
            return "下载地址无效"
        case .downloadFailed(let message):
            return "下载失败：\(message)"
        case .invalidSHAFile:
            return "校验文件无效"
        case .checksumMismatch:
            return "下载的 dmg 校验失败"
        case .cannotUpdate:
            return "无法自动更新：请把 liquoten 拖到「应用程序」文件夹后再试，或手动下载新版。"
        case .commandFailed(let message):
            return message
        case .appNotFound:
            return "dmg 中没有找到 liquoten.app"
        case .invalidBundle(let message):
            return "新版本校验失败：\(message)"
        case .replaceFailed(let message):
            return "替换 App 失败：\(message)"
        case .relaunchFailed(let message):
            return "重新启动失败：\(message)"
        }
    }
}

enum Installer {
    static let bundleIdentifier = "com.liquoten.app"
    static let appName = "liquoten.app"

    static func downloadURLs(version: String) -> (dmg: URL, sha256: URL)? {
        let base = "https://github.com/h1origin/liquoten/releases/download/v\(version)/liquoten-\(version).dmg"
        guard let dmg = URL(string: base), let sha = URL(string: base + ".sha256") else { return nil }
        return (dmg, sha)
    }

    static func parseSHA256(_ text: String) -> String? {
        let token = text
            .split(whereSeparator: { $0 == " " || $0 == "\t" || $0 == "\n" || $0 == "\r" })
            .first
            .map(String.init)
        guard let token, token.count == 64, token.allSatisfy(\.isHexDigit) else { return nil }
        return token.lowercased()
    }

    static func canAutoUpdate(appPath: URL) -> Bool {
        let path = appPath.standardizedFileURL.path
        if path.contains("/Volumes/") || path.contains("AppTranslocation") {
            return false
        }
        return FileManager.default.isWritableFile(atPath: appPath.deletingLastPathComponent().path)
    }

    static func validateBundle(info: [String: Any], expectedVersion: String) -> String? {
        guard info["CFBundleIdentifier"] as? String == bundleIdentifier else {
            return "Bundle ID 不是 \(bundleIdentifier)"
        }
        guard info["CFBundleShortVersionString"] as? String == expectedVersion else {
            return "版本号不是 \(expectedVersion)"
        }
        return nil
    }

    static func sha256(file: URL) throws -> String {
        let data = try Data(contentsOf: file, options: .mappedIfSafe)
        return SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    static func installLocalSync(
        dmgURL: URL,
        shaURL: URL,
        currentAppURL: URL,
        expectedVersion: String,
        phase: (String) -> Void = { _ in }
    ) throws -> URL {
        guard canAutoUpdate(appPath: currentAppURL) else { throw InstallerError.cannotUpdate }
        let shaText: String
        do {
            shaText = try String(contentsOf: shaURL, encoding: .utf8)
        } catch {
            throw InstallerError.invalidSHAFile
        }
        guard let expected = parseSHA256(shaText) else { throw InstallerError.invalidSHAFile }
        let actual = try sha256(file: dmgURL)
        guard actual.caseInsensitiveCompare(expected) == .orderedSame else {
            throw InstallerError.checksumMismatch
        }

        phase("正在安装…")
        let fm = FileManager.default
        let work = fm.temporaryDirectory.appendingPathComponent("liquoten-update-\(UUID().uuidString)", isDirectory: true)
        try fm.createDirectory(at: work, withIntermediateDirectories: true)
        let mount = work.appendingPathComponent("mnt", isDirectory: true)
        try fm.createDirectory(at: mount, withIntermediateDirectories: true)
        var mounted = false
        var staged: URL?
        defer {
            if mounted {
                _ = try? run("/usr/bin/hdiutil", ["detach", mount.path])
            }
            try? fm.removeItem(at: work)
            if let staged {
                try? fm.removeItem(at: staged)
            }
        }

        _ = try run("/usr/bin/hdiutil", [
            "attach", "-nobrowse", "-readonly", "-noautoopen",
            "-mountpoint", mount.path, dmgURL.path,
        ])
        mounted = true

        let source = mount.appendingPathComponent(appName)
        guard fm.fileExists(atPath: source.path) else { throw InstallerError.appNotFound }
        let infoURL = source.appendingPathComponent("Contents/Info.plist")
        let infoData = try Data(contentsOf: infoURL)
        let plist = try PropertyListSerialization.propertyList(from: infoData, options: [], format: nil)
        guard let info = plist as? [String: Any] else {
            throw InstallerError.invalidBundle("Info.plist 无法解析")
        }
        if let reason = validateBundle(info: info, expectedVersion: expectedVersion) {
            throw InstallerError.invalidBundle(reason)
        }

        let stagedURL = currentAppURL.deletingLastPathComponent().appendingPathComponent(".liquoten-update.app")
        staged = stagedURL
        if fm.fileExists(atPath: stagedURL.path) {
            try? fm.removeItem(at: stagedURL)
        }
        _ = try run("/usr/bin/ditto", [source.path, stagedURL.path])
        _ = try run("/usr/bin/hdiutil", ["detach", mount.path])
        mounted = false
        _ = try run("/usr/bin/xattr", ["-dr", "com.apple.quarantine", stagedURL.path])

        let old = currentAppURL.deletingLastPathComponent().appendingPathComponent(".liquoten-old.app")
        if fm.fileExists(atPath: old.path) {
            try? fm.removeItem(at: old)
        }
        try fm.moveItem(at: currentAppURL, to: old)
        do {
            try fm.moveItem(at: stagedURL, to: currentAppURL)
        } catch {
            let restored = (try? fm.moveItem(at: old, to: currentAppURL)) != nil
            let suffix = restored ? "" : "；恢复旧版本也失败，请重新安装 liquoten"
            throw InstallerError.replaceFailed(error.localizedDescription + suffix)
        }
        staged = nil
        try? fm.removeItem(at: old)
        return currentAppURL
    }

    static func install(
        version: String,
        currentAppURL: URL,
        progress: @escaping (Double) -> Void,
        phase: @escaping (String) -> Void,
        completion: @escaping (Result<URL, Error>) -> Void
    ) {
        guard let urls = downloadURLs(version: version) else {
            DispatchQueue.main.async { completion(.failure(InstallerError.invalidDownloadURL)) }
            return
        }
        let fm = FileManager.default
        let work = fm.temporaryDirectory.appendingPathComponent("liquoten-update-\(UUID().uuidString)", isDirectory: true)
        do {
            try fm.createDirectory(at: work, withIntermediateDirectories: true)
        } catch {
            DispatchQueue.main.async { completion(.failure(error)) }
            return
        }
        let shaFile = work.appendingPathComponent("liquoten-\(version).dmg.sha256")
        let dmgFile = work.appendingPathComponent("liquoten-\(version).dmg")
        var finished = false
        let finish: (Result<URL, Error>) -> Void = { result in
            guard !finished else { return }
            finished = true
            try? fm.removeItem(at: work)
            DispatchQueue.main.async { completion(result) }
        }

        phase("正在下载…")
        download(url: urls.sha256, to: shaFile) { error in
            if let error {
                finish(.failure(InstallerError.downloadFailed(error.localizedDescription)))
                return
            }
            guard let text = try? String(contentsOf: shaFile, encoding: .utf8),
                  parseSHA256(text) != nil else {
                finish(.failure(InstallerError.invalidSHAFile))
                return
            }
            download(url: urls.dmg, to: dmgFile, progress: { value in
                DispatchQueue.main.async { progress(value) }
            }) { error in
                if let error {
                    finish(.failure(InstallerError.downloadFailed(error.localizedDescription)))
                    return
                }
                DispatchQueue.main.async { progress(1) }
                DispatchQueue.global(qos: .userInitiated).async {
                    do {
                        let installed = try installLocalSync(
                            dmgURL: dmgFile,
                            shaURL: shaFile,
                            currentAppURL: currentAppURL,
                            expectedVersion: version,
                            phase: { text in
                                DispatchQueue.main.async { phase(text) }
                            }
                        )
                        finish(.success(installed))
                    } catch {
                        finish(.failure(error))
                    }
                }
            }
        }
    }

    static func relaunch(appURL: URL) throws {
        let pid = ProcessInfo.processInfo.processIdentifier
        let script = "while kill -0 \(pid) 2>/dev/null; do sleep 0.2; done; open \(shellQuote(appURL.path))"
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/sh")
        process.arguments = ["-c", script]
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        do {
            try process.run()
        } catch {
            throw InstallerError.relaunchFailed(error.localizedDescription)
        }
    }

    private static func shellQuote(_ value: String) -> String {
        "'" + value.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }

    private static func download(
        url: URL,
        to destination: URL,
        progress: @escaping (Double) -> Void = { _ in },
        completion: @escaping (Error?) -> Void
    ) {
        FileDownloader(url: url, destination: destination, progress: progress, completion: completion).start()
    }

    @discardableResult
    private static func run(_ executable: String, _ arguments: [String]) throws -> String {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = arguments
        process.standardInput = FileHandle.nullDevice
        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = pipe
        do {
            try process.run()
        } catch {
            throw InstallerError.commandFailed("\(URL(fileURLWithPath: executable).lastPathComponent) 启动失败：\(error.localizedDescription)")
        }
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        guard process.terminationStatus == 0 else {
            let message = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
            throw InstallerError.commandFailed("\(URL(fileURLWithPath: executable).lastPathComponent) 失败：\(message)")
        }
        return String(decoding: data, as: UTF8.self)
    }
}

private final class FileDownloader: NSObject, URLSessionDownloadDelegate {
    private let url: URL
    private let destination: URL
    private let progress: (Double) -> Void
    private let completion: (Error?) -> Void
    private var session: URLSession?
    private var finished = false

    init(url: URL, destination: URL, progress: @escaping (Double) -> Void, completion: @escaping (Error?) -> Void) {
        self.url = url
        self.destination = destination
        self.progress = progress
        self.completion = completion
    }

    func start() {
        let config = URLSessionConfiguration.ephemeral
        config.requestCachePolicy = .reloadIgnoringLocalCacheData
        config.timeoutIntervalForRequest = 60
        config.timeoutIntervalForResource = 600
        let session = URLSession(configuration: config, delegate: self, delegateQueue: nil)
        self.session = session
        var request = URLRequest(url: url, timeoutInterval: 60)
        request.setValue("liquoten/\(Updater.currentVersion)", forHTTPHeaderField: "User-Agent")
        session.downloadTask(with: request).resume()
    }

    func urlSession(
        _ session: URLSession,
        downloadTask: URLSessionDownloadTask,
        didWriteData bytesWritten: Int64,
        totalBytesWritten: Int64,
        totalBytesExpectedToWrite: Int64
    ) {
        guard totalBytesExpectedToWrite > 0 else { return }
        let value = min(max(Double(totalBytesWritten) / Double(totalBytesExpectedToWrite), 0), 1)
        DispatchQueue.main.async { self.progress(value) }
    }

    func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask, didFinishDownloadingTo location: URL) {
        guard let http = downloadTask.response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            finish(InstallerError.downloadFailed("HTTP \((downloadTask.response as? HTTPURLResponse)?.statusCode ?? 0)"))
            return
        }
        do {
            try? FileManager.default.removeItem(at: destination)
            try FileManager.default.moveItem(at: location, to: destination)
            finish(nil)
        } catch {
            finish(error)
        }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        if let error {
            finish(error)
        } else if !finished {
            finish(InstallerError.downloadFailed("下载未完成"))
        }
    }

    private func finish(_ error: Error?) {
        guard !finished else { return }
        finished = true
        session?.invalidateAndCancel()
        session = nil
        DispatchQueue.main.async { self.completion(error) }
    }
}