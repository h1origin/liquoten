import Foundation
import UserNotifications

// NotificationConsumer 消费 daemon 追加写入的 notifications.jsonl，
// 通过 UNUserNotificationCenter 投递系统通知。用 UserDefaults 里的
// 最后一条事件 id 当游标去重——重启 App 后不重发。
final class NotificationConsumer {
    private let center = UNUserNotificationCenter.current()
    private let defaultsKey = "lastNotificationID"

    func requestAuthorization() {
        center.requestAuthorization(options: [.alert, .sound]) { _, _ in }
    }

    func consume() {
        let url = URL(fileURLWithPath: DaemonManager.cacheDir)
            .appendingPathComponent("notifications.jsonl")
        guard let handle = try? FileHandle(forReadingFrom: url) else { return }
        let data = handle.readDataToEndOfFile()
        try? handle.close()
        guard let text = String(data: data, encoding: .utf8), !text.isEmpty else { return }

        let decoder = JSONDecoder()
        let last = UserDefaults.standard.string(forKey: defaultsKey)
        var seenLast = (last == nil)
        var lastSeen: String?

        for line in text.split(separator: "\n") {
            guard let ev = try? decoder.decode(NotifEvent.self, from: Data(line.utf8)) else {
                continue
            }
            if !seenLast {
                if ev.id == last { seenLast = true }
                continue
            }
            post(ev)
            lastSeen = ev.id
        }
        if let lastSeen {
            UserDefaults.standard.set(lastSeen, forKey: defaultsKey)
        }
    }

    private func post(_ ev: NotifEvent) {
        let content = UNMutableNotificationContent()
        content.title = ev.title
        content.body = ev.body
        content.sound = .default
        center.add(UNNotificationRequest(identifier: ev.id, content: content, trigger: nil))
    }
}
