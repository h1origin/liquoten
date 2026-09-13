import AppKit

// 菜单栏常驻 App：只做展示层，数据全部来自 daemon 写出的 state.json /
// notifications.jsonl。LSUIElement 保证不出现在 Dock。
let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
