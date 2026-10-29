# 任务 09：App 内自动升级（下载 dmg、校验、替换、重启）

## 背景

现在“有新版本”只会用浏览器打开 Release 页面。浏览器下载的 dmg 带 `com.apple.quarantine`，App 没有 Developer ID 签名和公证，
所以每次手动升级第一次打开都会被 Gatekeeper 拦（只能“移到废纸篓”，要去系统设置“仍要打开”）。
App 自己用 `URLSession` 下载的文件不会被打隔离标记（Info.plist 没有 `LSFileQuarantineEnabled`），所以 App 内升级后不会再被拦。

开始前必须先读仓库根目录的 `AGENTS.md`，所有硬性规则都适用。本任务在任务 08（改名为小写 liquoten）之后做。

## 范围

- `.github/workflows/release.yml`：Release 附带校验文件。
- Swift：`app/Updater.swift`（下载、校验、安装）、`app/AppDelegate.swift`、`app/Dashboard.swift`（横幅和弹窗）。可以新建 `app/Installer.swift`，记得加进 `app/build.sh` 的 `SOURCES`。
- 不改 Go 代码，不引入第三方依赖（不用 Sparkle）。

## 1. 发版附带 SHA-256

`release.yml` 打完 dmg 后生成 `liquoten-<版本>.dmg.sha256`（内容是 `shasum -a 256` 的输出，一行），和 dmg 一起作为 Release 附件上传。

## 2. 下载地址

不走 api.github.com。按版本号直接拼：

- `https://github.com/h1origin/liquoten/releases/download/v<版本>/liquoten-<版本>.dmg`
- `https://github.com/h1origin/liquoten/releases/download/v<版本>/liquoten-<版本>.dmg.sha256`

## 3. 升级流程

点“立即更新”后（横幅按钮和手动检查更新弹窗的第一个按钮都改成“立即更新”，“跳过此版本”“稍后”保留）：

1. 下载 `.sha256` 和 `.dmg` 到 `FileManager` 的临时目录（`URLSession.downloadTask`，超时合理，横幅显示进度百分比和“正在下载…/正在安装…”，期间按钮禁用）。
2. 校验 dmg 的 SHA-256（`CryptoKit`）与 `.sha256` 文件一致；**`.sha256` 下载失败或不一致就中止**，不安装。
3. `hdiutil attach -nobrowse -readonly -noautoopen -mountpoint <临时目录> <dmg>` 挂载。
4. 在挂载点里找 `liquoten.app`，读它的 `Info.plist`：`CFBundleIdentifier` 必须是 `com.liquoten.app`，`CFBundleShortVersionString` 必须等于要升级的版本，否则中止。
5. 用 `ditto` 把新 App 复制到**当前 App 所在目录**下的临时名字（如 `.liquoten-update.app`，保证同一个卷，后面可以原子替换），然后 `hdiutil detach`。
6. 对复制出来的 App 执行 `xattr -dr com.apple.quarantine`（保险起见，正常不会有）。
7. 替换：把当前 App 改名为 `.liquoten-old.app`，把新 App 改名为 `<目录>/liquoten.app`，成功后删掉旧的；任何一步失败就把旧的改回来并报错。
8. 重启：用 `Process` 启动一个脱离的 `/bin/sh -c 'while kill -0 <当前 pid> 2>/dev/null; do sleep 0.2; done; open "<新 App 路径>"'`，然后 `NSApp.terminate(nil)`
   （退出时现有逻辑会停掉 daemon）。

任何一步失败：清理临时文件、卸载 dmg，弹窗说明失败原因，并提供“打开下载页面”按钮（现在的浏览器方案）作为后备。

## 4. 不能自动替换时的后备

以下情况不尝试自动替换，直接提示并提供“打开下载页面”：

- 当前 App 路径在 `/Volumes/` 下（从 dmg 里直接运行）或路径里含 `AppTranslocation`（被系统隔离运行）；
- 当前 App 所在目录不可写（`FileManager.isWritableFile(atPath:)` 为 false）。

提示文案：`无法自动更新：请把 liquoten 拖到「应用程序」文件夹后再试，或手动下载新版。`

## 5. 升级后保持开机自启

- 用户每次切换“开机自动启动”时，把期望状态存进 `UserDefaults`（如 `loginItem.wanted`）。
- App 启动时如果 `loginItem.wanted == true` 但 `SMAppService.mainApp.status != .enabled`，静默调用一次 `register()`（失败只记日志，不弹窗）。
- 首次运行时（没有这个键）用当前 `SMAppService` 状态初始化它。

## 6. 文案

- `promptUpdate` 的说明改为：`当前版本 v<当前>。点「立即更新」会自动下载、校验并替换，完成后重新打开 liquoten。`
- README 两个语言版本里“检查更新：只提示，不自动下载替换”之类的描述同步更新；FAQ 里说明首次从浏览器安装仍需放行一次，之后 App 内更新不会再被拦。

## 测试

- Swift 没有单测框架，不强求。把纯逻辑抽成静态函数（下载地址拼接、`.sha256` 文件解析、路径是否可自动替换的判断、版本/bundle id 校验），
  在 `app/build.sh` 之外写一个临时脚本或用 `swiftc` 编一个小测试可执行文件验证这些函数（不提交也可以，在汇报里贴结果）。
- 手动验证：本地 `./app/package-dmg.sh 9.9.9` 生成 dmg 和 sha256，用一个临时目录模拟“当前 App 目录”，跑一遍校验 → 挂载 → 复制 → 替换的流程函数（不要动 `/Applications` 里用户正在用的 App），在汇报里贴结果。

## 验收

```bash
go test ./... && go vet ./... && test -z "$(gofmt -l .)" && ./app/build.sh && ./app/package-dmg.sh 0.0.0-test
```

全部通过，并且 `build/` 下同时生成了 `liquoten-0.0.0-test.dmg.sha256`（如果 sha256 是在 release.yml 里生成的，就在 package-dmg.sh 里生成，保持本地和 CI 一致）。
按 AGENTS.md 提交一次 commit（本任务说明一起提交，`build/` 不提交）。
