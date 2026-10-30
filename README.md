<div align="center">

<img src="docs/logo.png" width="128" alt="liquoten logo">

# liquoten

**Keep an eye on your LLM subscription quotas, right from the macOS menu bar.**

English | [简体中文](README.zh-CN.md)

[![Release](https://img.shields.io/github/v/release/h1origin/liquoten?sort=semver)](https://github.com/h1origin/liquoten/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/h1origin/liquoten/total)](https://github.com/h1origin/liquoten/releases)
[![Build](https://img.shields.io/github/actions/workflow/status/h1origin/liquoten/release.yml?label=build)](https://github.com/h1origin/liquoten/actions/workflows/release.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Platform](https://img.shields.io/badge/platform-macOS%2013%2B-lightgrey)
![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)
![Swift](https://img.shields.io/badge/Swift-native-F05138?logo=swift&logoColor=white)

</div>

liquoten shows how much of each LLM plan you have used as a water droplet in the menu bar: the less quota you have left, the lower the water. A small Go daemon reads each provider's usage from local files or read-only endpoints. It never sits in the path of your API requests.

## ✨ Features

- **Everything in one place**: Claude Code, ChatGPT Codex, Alibaba Cloud Bailian (Model Studio) Token Plan and Qoder all appear in one panel.
- **At-a-glance menu bar**: the droplet's water level and color (blue → orange → red) follow your tightest quota, with the remaining percentage shown next to it.
- **Trends**: each quota card has a usage chart (per-minute for 5-hour windows, per-day for weekly and monthly windows) and a reset countdown.
- **Notifications**: you get a macOS notification when a quota drops below 50% and 20% remaining (configurable), when a provider keeps failing, when a Codex reset credit is about to expire, and when the Codex team announces a reset.
- **In-app updates**: downloads the release dmg, checks its SHA-256, replaces the app and relaunches itself. Browser-downloaded first installs still need the one-time Gatekeeper approval.
- **Private and lightweight**: data stays on your Mac. Credentials are only read locally or kept in a `0600` config file. Uses about 0% CPU, and the only third-party dependency is a TOML parser.
- **Native**: a SwiftUI menu bar app, Universal binary (Apple silicon + Intel), with a launch-at-login switch.

## 📦 Supported providers

| Provider | What is shown | How it is read | Setup |
|---|---|---|---|
| **Claude Code** (Pro / Max) | 5-hour and weekly usage | Claude Code's `statusLine` input, saved by `liquoten statusline` | [Add a statusLine command](#claude-code) |
| **ChatGPT Codex** | 5-hour and weekly usage, credits, reset credits | Local `~/.codex/sessions` logs, corrected every 30 minutes against the live usage endpoint with Codex's own login | None |
| **Alibaba Cloud Bailian** Token Plan | 5-hour / weekly / monthly usage (depends on plan) | Signed Alibaba Cloud OpenAPI call + console gateway | Enter an AccessKey in the Services page |
| **Qoder / Qoder CN** | Credits used this billing cycle and add-on credits | Personal access token → quota API | Enter a token in the Services page |

## 🚀 Installation

### Download (recommended)

1. Download the latest `liquoten-x.y.z.dmg` from [Releases](https://github.com/h1origin/liquoten/releases/latest).
2. Open it and drag **liquoten** into **Applications**.
3. The app is not notarized by Apple yet, so macOS will block it the first time it opens. Allow it with either option:
   - Go to **System Settings → Privacy & Security**, scroll down and click **Open Anyway**, or
   - run:

     ```bash
     xattr -dr com.apple.quarantine /Applications/liquoten.app
     ```

Requires macOS 13 Ventura or later.

### Build from source

Requirements: Go 1.21+ (the 1.27 toolchain is fetched automatically) and Xcode Command Line Tools.

```bash
git clone https://github.com/h1origin/liquoten.git && cd liquoten
./app/build.sh                         # produces build/liquoten.app
mv build/liquoten.app /Applications/
open /Applications/liquoten.app
```

## 🏁 Quick start

1. Click the droplet in the menu bar, then **Services**.
2. Turn on the providers you use. Nothing is enabled by default.
3. Allow notifications when macOS asks.
4. Optional: turn on **Launch at login** from the gear menu.

### Claude Code

Claude Code passes subscription usage to its [statusLine](https://code.claude.com/docs/en/statusline) command. Point it at liquoten in `~/.claude/settings.json` (the Services page has a one-click **Copy** button):

```json
"statusLine": {
  "type": "command",
  "command": "/Applications/liquoten.app/Contents/Resources/liquoten statusline"
}
```

Claude Code's status bar then shows something like `[Opus] 5h 23% · 周 41%`, and the daemon reads the same snapshot. No OAuth tokens or private APIs are involved. Data updates only while you use Claude Code.

### ChatGPT Codex

No setup needed. liquoten reads the latest usage from `~/.codex/sessions`.
- **30-minute correction**: if you are signed in to Codex with ChatGPT, liquoten also calls the read-only usage endpoint every 30 minutes, so numbers stay current when you have not used the CLI for a while.
- **Reset credits**: checked every 6 hours. You get a notification when one will expire within 12 hours.
- **Official resets**: every 15 minutes liquoten reads the public archive at [willcodexresets.com](https://willcodexresets.com/api/docs), which tracks reset announcements by the Codex team on X. It waits at most 5 seconds, retries once after a minute on failure, then skips until the next round. When a new reset or reset-credit grant is announced you get a notification, and the Codex card shows the latest official reset. No account data is sent.
- **Login**: liquoten only reads `~/.codex/auth.json`. It never refreshes tokens itself and never redeems reset credits.

### Alibaba Cloud Bailian

Enter an AccessKey ID and Secret on the Bailian row in **Services**. liquoten checks the key once before saving it.
- **Use a dedicated key**: we recommend a RAM user with Bailian permissions only.
- **Storage**: the key is stored in plain text in `~/.config/liquoten/config.toml` (mode `0600`) and is deleted when you turn the service off.

## ⚙️ Configuration

The config file is `~/.config/liquoten/config.toml`. The Services page edits it for you; see [config.example.toml](config.example.toml) for all options.

```toml
poll_interval = "1m"              # with ±10% jitter
alert_remaining = [50.0, 20.0]    # notify when remaining drops below these; [] disables
notify = "app"                    # or "osascript" when running without the app

[providers.codex]                 # a [providers.<name>] table enables a provider

[thresholds.claudecode]           # extra alert points, as used %
"5-hour quota" = 80.0
```

| Environment variable | Purpose |
|---|---|
| `LIQUOTEN_CONFIG` | Config file path (default `~/.config/liquoten/config.toml`) |
| `LIQUOTEN_CACHE_DIR` | Cache directory (default `~/.cache/liquoten`) |
| `CODEX_HOME` | Codex data directory (default `~/.codex`) |

## 🔧 How it works

```
providers ──► daemon (polls every ~1 min) ──► state.json / history.jsonl
                                         └──► notifications.jsonl (threshold crossings)
liquoten.app ◄── reads state.json, renders the menu bar, delivers notifications
```

- **The daemon** is written in Go. The app starts it and restarts it if it exits. It writes results atomically, appends history (kept for 35 days), and runs a small alert state machine. Each alert fires once per threshold and is persisted across restarts.
- **The app** is written in Swift. It only reads those files, so neither process depends on the other's memory or IPC.
- **Every provider is normalized** to `Section{Name, UsedPct, Remaining, ResetAt}`, so the UI does not need to know about specific providers.

<details>
<summary><b>CLI reference</b></summary>

```bash
liquoten -once                         # poll once and exit
liquoten providers                     # list providers and status (JSON)
liquoten enable|disable <provider>     # toggle a provider; disable also removes its keys
liquoten set-access-key <provider>     # read AccessKey ID and Secret from stdin, verify, save
liquoten clear-access-key <provider>
liquoten statusline                    # Claude Code statusLine command
```

The binary is at `/Applications/liquoten.app/Contents/Resources/liquoten`. Flags: `-config`, `-cache-dir`.

</details>

## ❓ FAQ

<details>
<summary><b>macOS says the app can't be opened / the developer can't be verified</b></summary>

The app is not notarized yet. See the [installation](#-installation) steps above. The first install from a browser-downloaded dmg still needs this one-time approval. Updates installed from inside liquoten are not quarantined by macOS, so they are not blocked.
</details>

<details>
<summary><b>No notifications</b></summary>

Allow liquoten in **System Settings → Notifications** and check that Focus mode is off. Also check that `notify` in the config is `"app"`.
</details>

<details>
<summary><b>Bailian says the AccessKey is missing or not authorised</b></summary>

Enter the key on the Services page. If it is rejected with `NotAuthorised`, grant the RAM user Bailian permissions and wait a few minutes for them to take effect.
</details>

<details>
<summary><b>Numbers differ from the provider's console</b></summary>

There can be up to one poll interval of delay. Claude Code data updates only while Claude Code is in use. Codex is corrected against the live data every 30 minutes.
</details>

<details>
<summary><b>Logs and data</b></summary>

- Logs: `~/Library/Logs/liquoten.log`
- State: `~/.cache/liquoten/state.json`
- Config: `~/.config/liquoten/config.toml`
</details>

<details>
<summary><b>Uninstall</b></summary>

```bash
osascript -e 'quit app "liquoten"' && rm -rf /Applications/liquoten.app
rm -rf ~/.cache/liquoten ~/.config/liquoten
```
</details>

## 🗺️ Roadmap

- [ ] Bailian usage add-on packages and per-model free quota
- [ ] Desktop widget (WidgetKit)
- [ ] Signed and notarized builds
- [ ] More providers: OpenAI API, Anthropic API, DeepSeek, OpenRouter

## 🤝 Contributing

Issues and pull requests are welcome.

**Adding a provider:**
1. Create `internal/providers/<name>/` and implement `providers.Provider`.
2. Call `providers.Register` in `init()`.
3. Add a blank import in `cmd/liquoten/main.go`.
4. Add a parsing test with a recorded fixture in `testdata/`.

See [AGENTS.md](AGENTS.md) for layering and conventions. Before opening a PR, run:

```bash
go test ./... && go vet ./... && test -z "$(gofmt -l .)"
```

Pushing a `v*` tag builds a Universal dmg and publishes a GitHub Release ([release.yml](.github/workflows/release.yml)).

## 📄 License

[MIT](LICENSE) © 2026 Origin
