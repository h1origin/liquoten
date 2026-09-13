import Foundation

// DaemonManager 负责把 app bundle 内的 liquoten daemon 作为子进程拉起，
// 退出后自动重启（5 秒防抖）。App 退出时随之一并终止。
final class DaemonManager {
    private var process: Process?
    private var quitting = false

    var binaryURL: URL? {
        Bundle.main.resourceURL?.appendingPathComponent("liquoten")
    }

    var isMissing: Bool {
        guard let u = binaryURL else { return true }
        return !FileManager.default.isExecutableFile(atPath: u.path)
    }

    func start() {
        guard process == nil else { return }
        spawn()
    }

    // 立即刷新 = 重启 daemon（daemon 启动即先轮询一次）。
    func restart() {
        let old = process
        process = nil
        old?.terminate()
        old?.waitUntilExit()
        spawn()
    }

    func stop() {
        quitting = true
        process?.terminate()
        process = nil
    }

    private func spawn() {
        guard let bin = binaryURL,
              FileManager.default.isExecutableFile(atPath: bin.path) else { return }
        let p = Process()
        p.executableURL = bin
        p.arguments = ["-config", Self.configPath, "-cache-dir", Self.cacheDir]

        // daemon 日志追加到 ~/Library/Logs/liquoten.log
        let logURL = Self.logURL
        if !FileManager.default.fileExists(atPath: logURL.path) {
            FileManager.default.createFile(atPath: logURL.path, contents: nil)
        }
        if let fh = try? FileHandle(forWritingTo: logURL) {
            _ = try? fh.seekToEnd()
            p.standardOutput = fh
            p.standardError = fh
        }

        p.terminationHandler = { [weak self] _ in
            DispatchQueue.main.async {
                guard let self, !self.quitting else { return }
                self.process = nil
                DispatchQueue.main.asyncAfter(deadline: .now() + 5) { self.spawn() }
            }
        }
        do {
            try p.run()
            process = p
        } catch {
            NSLog("liquoten daemon 启动失败: \(error)")
        }
    }

    // 与 daemon 的 config.DefaultPath / store.DefaultDir 保持一致（含环境变量覆盖）。
    static var configPath: String {
        if let c = ProcessInfo.processInfo.environment["LIQUOTEN_CONFIG"], !c.isEmpty {
            return c
        }
        return NSHomeDirectory() + "/.config/liquoten/config.toml"
    }

    static var cacheDir: String {
        if let c = ProcessInfo.processInfo.environment["LIQUOTEN_CACHE_DIR"], !c.isEmpty {
            return c
        }
        return NSHomeDirectory() + "/.cache/liquoten"
    }

    static var logURL: URL {
        URL(fileURLWithPath: NSHomeDirectory() + "/Library/Logs")
            .appendingPathComponent("liquoten.log")
    }
}
