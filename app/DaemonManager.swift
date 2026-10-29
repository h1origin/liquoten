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

    func login(provider: String, site: String, completion: @escaping (String?) -> Void) {
        run(["login", "-config", Self.configPath, "-site", site, provider]) { _, failure, _ in completion(failure) }
    }

    func listProviders(completion: @escaping ([ProviderInfo]) -> Void) {
        run(["providers", "-config", Self.configPath]) { output, failure, _ in
            guard failure == nil,
                  let list = try? JSONDecoder().decode([ProviderInfo].self, from: Data(output.utf8)) else {
                completion([])
                return
            }
            completion(list)
        }
    }

    enum EnableResult {
        case ok
        case notInstalled(String)
        case failed(String)
    }

    // 与 daemon 的 exitNotInstalled 保持一致。
    private static let exitNotInstalled: Int32 = 3

    // API Key 经标准输入传给 daemon，不出现在进程参数里。
    func enable(provider: String, apiKey: String?, completion: @escaping (EnableResult) -> Void) {
        var args = ["enable", "-config", Self.configPath]
        if apiKey != nil { args.append("-api-key-stdin") }
        run(args + [provider], stdin: apiKey) { _, failure, status in
            guard let failure else { return completion(.ok) }
            completion(status == Self.exitNotInstalled ? .notInstalled(failure) : .failed(failure))
        }
    }

    func disable(provider: String, completion: @escaping (String?) -> Void) {
        run(["disable", "-config", Self.configPath, provider]) { _, failure, _ in completion(failure) }
    }

    func setAccessKey(provider: String, id: String, secret: String, completion: @escaping (String?) -> Void) {
        run(["set-access-key", "-config", Self.configPath, provider], stdin: id + "\n" + secret) { _, failure, _ in
            completion(failure)
        }
    }

    func clearAccessKey(provider: String, completion: @escaping (String?) -> Void) {
        run(["clear-access-key", "-config", Self.configPath, provider]) { _, failure, _ in completion(failure) }
    }

    func install(provider: String, completion: @escaping (String?) -> Void) {
        run(["install", "-config", Self.configPath, provider]) { _, failure, _ in completion(failure) }
    }

    private func run(_ args: [String], stdin: String? = nil, completion: @escaping (String, String?, Int32) -> Void) {
        guard let bin = binaryURL, !isMissing else {
            completion("", "daemon 缺失，请重新安装 Liquoten.app", -1)
            return
        }
        DispatchQueue.global(qos: .userInitiated).async {
            let p = Process()
            p.executableURL = bin
            p.arguments = args
            let pipe = Pipe()
            p.standardOutput = pipe
            p.standardError = pipe
            let input = Pipe()
            p.standardInput = input
            var output = ""
            var failure: String?
            var status: Int32 = -1
            do {
                try p.run()
                if let stdin { input.fileHandleForWriting.write(Data(stdin.utf8)) }
                try? input.fileHandleForWriting.close()
                let data = pipe.fileHandleForReading.readDataToEndOfFile()
                p.waitUntilExit()
                status = p.terminationStatus
                output = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
                if status != 0 { failure = output }
            } catch {
                failure = error.localizedDescription
            }
            DispatchQueue.main.async { completion(output, failure, status) }
        }
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

        // 只对当前托管的进程自动重启；restart() 主动结束的旧进程不再触发重启。
        p.terminationHandler = { [weak self] exited in
            DispatchQueue.main.async {
                guard let self, !self.quitting, self.process === exited else { return }
                self.process = nil
                DispatchQueue.main.asyncAfter(deadline: .now() + 5) {
                    guard !self.quitting, self.process == nil else { return }
                    self.spawn()
                }
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
