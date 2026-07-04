# macOS 部署

#### **简体中文** | [English](macos_EN.md)

> [!WARNING]
> 以下内容由 AI 生成，仅供参考。

适用于 Intel 与 Apple Silicon。程序为单个可执行文件，无运行时依赖，内置服务安装命令，可一键注册为 launchd 守护进程。

## 一键安装（推荐）

终端执行：

```bash
curl -fsSL https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases/latest/download/install.sh | sh
```

脚本会自动识别架构、下载对应二进制，交互引导填写路由器地址 / 账号 / 通知渠道，并注册为开机自启的 launchd 守护进程。下面是等价的手动步骤，供需要自定义的用户参考。

## 1. 获取程序

从 [Releases](https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases) 下载，并安装到固定路径：

- Apple Silicon（M 系列）：`cpe-sms-forwarder-darwin-arm64`
- Intel：`cpe-sms-forwarder-darwin-amd64`

```bash
sudo install -m 0755 cpe-sms-forwarder-darwin-arm64 /usr/local/bin/cpe-sms-forwarder
```

或从源码编译（需安装 Go）：`make darwin-arm64`（或 `darwin-amd64`）。

## 2. 解除下载隔离

从浏览器下载的可执行文件会被 Gatekeeper 标记隔离，首次使用前解除（自行编译的二进制无此限制）：

```bash
sudo xattr -dr com.apple.quarantine /usr/local/bin/cpe-sms-forwarder
```

## 3. 准备配置

```bash
sudo mkdir -p /usr/local/etc/cpe-sms-forwarder
sudo cp config.yaml /usr/local/etc/cpe-sms-forwarder/config.yaml
sudo vi /usr/local/etc/cpe-sms-forwarder/config.yaml      # 填写 CPE 地址/账号/密码与通知渠道(notify.urls)
```

完整配置项见 [项目 README 的配置章节](../README.md#配置)。

## 4. 前台试运行

```bash
/usr/local/bin/cpe-sms-forwarder -config /usr/local/etc/cpe-sms-forwarder/config.yaml
```

终端出现「CPE 登录成功」即正常；同时会收到一条「CPE 服务已上线」推送，可顺便确认通知配置无误。按 `Ctrl + C` 退出。

## 5. 注册为 launchd 服务

用内置命令安装并启动。`-config` 须为绝对路径：

```bash
sudo /usr/local/bin/cpe-sms-forwarder -service install -config /usr/local/etc/cpe-sms-forwarder/config.yaml
sudo /usr/local/bin/cpe-sms-forwarder -service start
```

将注册为系统级 LaunchDaemon（`/Library/LaunchDaemons/cpe-sms-forwarder.plist`），开机自启并在退出后自动拉起。

## 6. 服务管理

```bash
sudo /usr/local/bin/cpe-sms-forwarder -service status
sudo /usr/local/bin/cpe-sms-forwarder -service stop
sudo /usr/local/bin/cpe-sms-forwarder -service restart
sudo /usr/local/bin/cpe-sms-forwarder -service uninstall
```

## 7. 查看日志

launchd 会将程序输出写入 `/var/log/`。运行日志走标准错误，落在 `cpe-sms-forwarder.err.log`：

```bash
tail -f /var/log/cpe-sms-forwarder.err.log
```

## 8. 更新

```bash
sudo /usr/local/bin/cpe-sms-forwarder -service stop
sudo install -m 0755 新版二进制 /usr/local/bin/cpe-sms-forwarder
sudo /usr/local/bin/cpe-sms-forwarder -service start
```

配置文件无需改动。
