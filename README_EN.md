<div align="center">

# CPE Pro 2 SMS Forwarder

#### [简体中文](README.md) | **English**

An SMS forwarder built for the **Fiberhome CPE Pro 2** that pushes new SMS from the router to Bark, Telegram, ntfy, Gotify, email, and more.

![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20Linux%20%7C%20macOS%20%7C%20OpenWrt-blue?style=for-the-badge)
![GitHub stars](https://img.shields.io/github/stars/AHCorn/CPE-Pro-2-SMS-Forwarder?style=for-the-badge)
![GitHub issues](https://img.shields.io/github/issues/AHCorn/CPE-Pro-2-SMS-Forwarder?style=for-the-badge)

</div>

<br>

Notifications are delivered via [shoutrrr](https://github.com/nicholas-fedor/shoutrrr). It fully implements the panel's encrypted login protocol, is written in pure Go with no browser dependency, ships as a single binary, and is well suited to running long-term on low-power devices such as soft routers.

> [!NOTE]
> Only the Fiberhome CPE Pro 2 is supported; other CPE models use different admin protocols and will not work.
>
> Most of the work in this project was done by AI. It runs fine in my personal setup; if you run into any issues, feedback is always welcome.

## Features

- Pure HTTP API interaction with no browser or headless dependency; runs as a single static binary
- Multi-channel notifications: built on shoutrrr, supporting Bark, Telegram, ntfy, Gotify, email, Slack, and 20+ other services, with delivery to multiple channels at once
- Cross-platform: Windows, Linux, macOS, and OpenWrt routers, covering amd64 / arm64 / armv7 / mipsle
- SHA256 deduplication with persisted state; no missed or duplicated pushes across restarts
- Automatic reconnect and periodic re-login to avoid silent misses from stale long-lived sessions
- Auto-yield: backs off when you log into the router admin panel yourself, leaving the single admin session to you
- Service-level alerts: startup, shutdown, consecutive failures, and prolonged stalls all trigger notifications
- Failure containment and burst merging: a failing channel is circuit-broken for the rest of the polling cycle so polling stays fast, and a backlog of more than 5 messages is merged into digest pushes instead of flooding your phone
- Built-in service installer: one command registers it as a system service (Windows service / systemd / launchd / OpenWrt procd) with auto-start and automatic restart on failure

## One-Line Install (recommended)

The script auto-detects your architecture, downloads the matching binary, interactively walks you through the router address / credentials / notification channels, and registers an auto-starting system service.

**Linux / macOS / soft router (OpenWrt)** — run in a terminal (as root on OpenWrt, otherwise with `sudo` as needed):

```bash
curl -fsSL https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases/latest/download/install.sh | sh
```

**Windows** — run in an Administrator PowerShell:

```powershell
irm https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases/latest/download/install.ps1 | iex
```

> For air-gapped LANs, host the binaries internally and point `BASE_URL` at that address (`install.ps1` uses `$env:BASE_URL`).

For service management and logs after install, see the per-platform guides under [Deployment](#deployment); for manual deployment or building from source, see [Quick Start](#quick-start).

## Quick Start

With a Go toolchain installed, build and run from source:

```bash
# Build for the current platform
make build

# Print version (derived from the git tag; source builds show the commit hash, releases show e.g. v0.1.0)
./cpe-sms-forwarder -version

# Run after preparing the config
cp config.yaml my-config.yaml      # edit host, credentials, notification channels
./cpe-sms-forwarder -config my-config.yaml
```

Prebuilt binaries are also available on the [Releases](https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases) page; no Go toolchain required.

## Deployment

The program ships with a built-in service installer. Once the binary and config are in place, a single command registers it as an auto-starting system service:

```bash
cpe-sms-forwarder -service install -config /absolute/path/config.yaml
cpe-sms-forwarder -service start
```

`-service` accepts `install`, `uninstall`, `start`, `stop`, `restart`, and `status`. Install paths, privileges, and log locations differ per platform; pick the guide for your target:

| Platform | Method |
| --- | --- |
| [Windows](doc/windows_EN.md) | Windows service |
| [Linux](doc/linux_EN.md) | systemd |
| [macOS](doc/macos_EN.md) | launchd |
| [Soft router](doc/router_EN.md) | OpenWrt / iStoreOS (procd) |

Running into issues after deployment (logs, common errors, filing reports)? See [Troubleshooting](doc/troubleshooting_EN.md).

## Configuration

Both a YAML file and environment variables are supported. Precedence: **environment variables > config file > defaults**. Inject secrets via environment variables when possible.

| Key | Env | Default | Description |
| --- | --- | --- | --- |
| cpe.host | CPE_HOST | - | CPE router address |
| cpe.username | CPE_USERNAME | - | Login username |
| cpe.password | CPE_PASSWORD | - | Login password |
| notify.urls | NOTIFY_URLS | - | Notification channel URLs (shoutrrr), e.g. `bark://:key@api.day.app`; one per list item in YAML, space-separated in the env var |
| notify.notify_yield | NOTIFY_YIELD | true | Notify on yield / resume; false to disable (other alerts unaffected) |
| notify.retry_failed | NOTIFY_RETRY_FAILED | false | Multi-channel reliable delivery: when some channels fail but another delivers, retry the failed ones and, if still failing, send a degradation alert via a working channel; off by default (send to all at once, mark read only when all succeed) |
| notify.sms_title | NOTIFY_SMS_TITLE | `CPE短信 - {phone}` | SMS title template; placeholders `{phone}` `{content}` `{time}` |
| notify.sms_body | NOTIFY_SMS_BODY | `{content}\n\n{time}` | SMS body template; same placeholders, literal `\n` becomes a newline |
| notify.fallback.url | NOTIFY_FALLBACK_URL | empty (disabled) | Fallback notification channel (shoutrrr URL), used only when every primary channel fails to deliver a message |
| notify.fallback.forward | NOTIFY_FALLBACK_FORWARD | false | true forwards the message itself via the fallback channel (delivery there counts as forwarded); false only sends a rate-limited "primary channels offline" alert while SMS queue up and are backfilled once a primary channel recovers |
| poll.interval_seconds | CPE_POLL_INTERVAL | 60 | Poll interval in seconds |
| poll.relogin_minutes | CPE_RELOGIN_MINUTES | 30 | Periodic forced re-login interval in minutes |
| poll.yield_minutes | CPE_YIELD_MINUTES | 5 | Max minutes to back off before forcing a login while another device holds the admin panel; negative disables yielding |
| state.file | CPE_STATE_FILE | seen.json next to config | Deduplication state file path |
| log.file | LOG_FILE | - | Log file path; empty means console when foreground, system log when running as a service (Windows defaults to a file next to the binary) |

Notifications are powered by [shoutrrr](https://github.com/nicholas-fedor/shoutrrr): list one service URL per entry under `notify.urls`. Common ones: `bark://:deviceKey@api.day.app`, `telegram://<bot-token>@telegram?chats=@channel`, `ntfy://ntfy.sh/topic`, `gotify://host/token`, `smtp://...` (email). See the [shoutrrr docs](https://shoutrrr.nickfedor.com/) for the full list.

Channel-specific options (such as Bark group, sound, icon) are set via URL query parameters, e.g. `bark://:deviceKey@api.day.app?group=cpe-sms&sound=bell`; the forwarded SMS title prefix and body layout are controlled by the `notify.sms_title` / `notify.sms_body` templates.

## How It Works

- **Encrypted protocol**: before login it negotiates a handshake value with the router (obtained via RSA decryption), derives an AES-128 key from the dynamic session id, and encrypts both requests and responses with AES-128-CBC, exactly matching the admin panel's own encryption flow.
- **Session keep-alive**: the router binds a session to the TCP connection and client IP, so a single keep-alive connection is reused and periodically re-logged in to avoid stale sessions.
- **Deduplication and catch-up**: messages are deduplicated by a fingerprint (SHA256 of phone, content, time) persisted to disk; messages arriving while offline are pushed after recovery rather than swallowed as history.
- **Auto-yield**: the router admin panel allows only one user online at a time. Before each re-login the program probes the occupancy with a non-destructive endpoint; if another device holds the panel it backs off for up to `poll.yield_minutes` before forcing a login, leaving room for manual operations.

## Build

```bash
make build        # current platform
make release      # cross-compile all platforms (Windows / Linux / macOS / routers)
```

Build a single platform with `make linux-arm64`, `make windows-amd64`, `make darwin-arm64`, etc.

## License

[GPL-3.0](LICENSE)
