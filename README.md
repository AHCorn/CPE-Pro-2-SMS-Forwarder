<div align="center">

# CPE Pro 2 SMS Forwarder

#### **简体中文** | [English](README_EN.md)

专为 **烽火 CPE Pro 2** 打造的短信转发工具，自动将新短信推送到 Bark、Telegram、邮件等渠道。

![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20Linux%20%7C%20macOS%20%7C%20OpenWrt-blue?style=for-the-badge)
![GitHub stars](https://img.shields.io/github/stars/AHCorn/CPE-Pro-2-SMS-Forwarder?style=for-the-badge)
![GitHub issues](https://img.shields.io/github/issues/AHCorn/CPE-Pro-2-SMS-Forwarder?style=for-the-badge)

</div>

<br>

## 提示

本工具仅适配烽火 CPE Pro 2，其他型号的后台协议不同、无法直接使用。

本项目绝大部分工作由 AI 完成，个人自用环境下工作正常，若有问题欢迎您随时反馈。

## 特性

完整实现了后台的加密登录协议，适合在软路由等低功耗设备上长期稳定运行。

- 纯 HTTP API 交互，不依赖浏览器，单个静态二进制即可运行
- 多渠道通知：基于 shoutrrr，支持 Bark、Telegram、ntfy、Gotify、邮件、Slack 等二十余种渠道，可同时推送
- 跨平台：Windows、Linux、macOS、OpenWrt 软路由，覆盖 amd64 / arm64 / armv7 / mipsle
- SHA256 去重并持久化状态，重启不漏推、不重复推
- 自动重连与定期重新登录，规避长连接会话僵死导致的静默漏推
- 自动避让：检测到您手动登录路由器后台时主动退避，不与您争抢后台会话
- 服务级告警：上线、下线、连续失败、长时间无响应均会推送通知
- 故障熔断与洪峰合并：通知渠道故障时本轮熔断，不拖慢轮询；积压短信超过 5 条自动合并为摘要推送，避免通知轰炸
- 内置服务安装：一条命令注册为系统服务（Windows 服务 / systemd / launchd / OpenWrt procd），开机自启、异常自动重启

## 一键安装（推荐）

脚本会自动识别架构、下载对应二进制，交互引导填写路由器地址 / 账号 / 通知渠道，并注册为开机自启的系统服务。

**Linux / macOS / 软路由（OpenWrt）** — 在终端执行（OpenWrt 默认即 root，其他系统按需加 `sudo`）：

```bash
curl -fsSL https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases/latest/download/install.sh | sh
```

**Windows** — 以管理员身份打开 PowerShell 执行：

```powershell
irm https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases/latest/download/install.ps1 | iex
```

> 无外网的内网环境，可先把二进制托管到内网 HTTP 服务，再用 `BASE_URL` 指向该地址（`install.ps1` 用 `$env:BASE_URL`）。

安装完成后如何管理服务、查看日志，见下方[部署](#部署)的各平台指南；偏好手动部署或从源码构建，见[快速开始](#快速开始)。

## 快速开始

从源码编译并运行（需要 Go 环境）：

```bash
# 编译当前平台
make build

# 查看版本（版本号取自 git tag，源码构建显示提交哈希，发布版显示如 v0.1.0）
./cpe-sms-forwarder -version

# 准备配置后运行
cp config.yaml my-config.yaml      # 编辑填入真实地址、账号、通知渠道
./cpe-sms-forwarder -config my-config.yaml
```

也可直接从 [Releases](https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases) 下载预编译版本，无需安装 Go。

## 部署

程序内置服务安装命令，放好二进制与配置后，一条命令即可注册为开机自启的系统服务：

```bash
cpe-sms-forwarder -service install -config /绝对路径/config.yaml
cpe-sms-forwarder -service start
```

`-service` 支持 `install`、`uninstall`、`start`、`stop`、`restart`、`status`。各平台的安装路径、权限与日志位置略有差异，按目标平台查看对应指南：

| 平台 | 部署方式 |
| --- | --- |
| [Windows](doc/windows.md) | 注册为 Windows 服务 |
| [Linux](doc/linux.md) | systemd |
| [macOS](doc/macos.md) | launchd |
| [软路由](doc/router.md) | OpenWrt / iStoreOS（procd） |

部署后如遇问题（查看日志、常见报错、提交反馈），见 [排障与反馈](doc/troubleshooting.md)。

## 配置

支持 YAML 配置文件与环境变量，优先级：**环境变量 > 配置文件 > 默认值**。敏感信息建议通过环境变量注入。

| 配置项 | 环境变量 | 默认值 | 说明 |
| --- | --- | --- | --- |
| cpe.host | CPE_HOST | - | CPE 路由器地址 |
| cpe.username | CPE_USERNAME | - | 登录用户名 |
| cpe.password | CPE_PASSWORD | - | 登录密码 |
| notify.urls | NOTIFY_URLS | - | 通知渠道 URL（shoutrrr），如 `bark://:key@api.day.app`；YAML 下每行一个，环境变量用空格分隔多个 |
| notify.notify_yield | NOTIFY_YIELD | true | 是否在进入退避 / 恢复时推送提醒，false 关闭（不影响其他通知） |
| notify.retry_failed | NOTIFY_RETRY_FAILED | false | 多渠道可靠投递：部分渠道失败而另有渠道送达时自动重试，仍失败则经送达渠道发降级提示；默认关闭（全渠道一次性发送、全部成功才标记已读） |
| notify.sms_title | NOTIFY_SMS_TITLE | `CPE短信 - {phone}` | 转发短信的标题模板，占位符 `{phone}` `{content}` `{time}` |
| notify.sms_body | NOTIFY_SMS_BODY | `{content}\n\n{time}` | 转发短信的正文模板，占位符同上；字面 `\n` 转为换行 |
| notify.fallback.url | NOTIFY_FALLBACK_URL | 空（禁用） | 备用通知渠道（shoutrrr URL），仅当所有主渠道对同一条消息全部推送失败时启用 |
| notify.fallback.forward | NOTIFY_FALLBACK_FORWARD | false | true 经备用渠道转发消息本体（送达即视为已转发）；false 仅发一条限流的"主渠道离线"提示，短信积压待主渠道恢复后补发 |
| poll.interval_seconds | CPE_POLL_INTERVAL | 60 | 轮询间隔（秒） |
| poll.relogin_minutes | CPE_RELOGIN_MINUTES | 30 | 定期强制重新登录间隔（分钟） |
| poll.yield_minutes | CPE_YIELD_MINUTES | 5 | 后台被他人占用时，最多退避多少分钟后才强制登录；负数禁用退避 |
| state.file | CPE_STATE_FILE | 配置同目录 seen.json | 去重状态持久化路径 |
| log.file | LOG_FILE | - | 日志文件路径；留空时前台输出到终端，服务模式由系统日志收集（Windows 默认写到可执行文件同目录） |

通知渠道基于 [shoutrrr](https://github.com/nicholas-fedor/shoutrrr)，`notify.urls` 每行一个服务 URL，可同时配置多个。常见：`bark://:设备Key@api.day.app`、`telegram://<bot-token>@telegram?chats=@频道`、`ntfy://ntfy.sh/主题`、`gotify://host/token`、`smtp://...`（邮件）。完整渠道与参数见 [shoutrrr 文档](https://shoutrrr.nickfedor.com/)。

渠道自身的特性（如 Bark 的分组、铃声、图标）通过 URL 查询参数设置，例如 `bark://:设备Key@api.day.app?group=cpe-sms&sound=bell`；转发短信的标题前缀与正文排版则由 `notify.sms_title` / `notify.sms_body` 模板控制。

## 工作原理

- **加密协议**：登录前先与路由器协商出握手值（RSA 解密获得），再由动态 sessionid 派生 AES-128 密钥，请求与响应均以 AES-128-CBC 加密，与路由器后台前端的加密流程完全一致。
- **会话保活**：路由器把会话绑定到 TCP 连接与客户端 IP，因此复用单条 keep-alive 连接，并定期重新登录以防会话僵死。
- **去重补推**：以短信指纹（手机号、内容、时间的 SHA256）去重并落盘；停机期间到达的短信在恢复后补推，而非当作历史短信吞掉。
- **自动避让**：路由器后台同一时间仅允许一个用户在线。每次重新登录前先用非破坏性接口探测占用状态，若后台正被其他设备占用则本轮退避，超过 `poll.yield_minutes` 才强制登录，给手动操作留出时间。

## 构建

```bash
make build        # 当前平台
make release      # 全平台交叉编译（Windows / Linux / macOS / 软路由）
```

单独编译某平台可用 `make linux-arm64`、`make windows-amd64`、`make darwin-arm64` 等。

## License

[GPL-3.0](LICENSE)
