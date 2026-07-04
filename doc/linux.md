# Linux 部署

#### **简体中文** | [English](linux_EN.md)

> [!WARNING]
> 以下内容由 AI 生成，仅供参考。

适用于使用 systemd 的常见发行版（Debian、Ubuntu、CentOS 等）。程序内置服务安装命令，可一键注册为 systemd 服务。软路由（OpenWrt / iStoreOS）请参阅 [router.md](router.md)。

## 一键安装（推荐）

终端执行（非 root 时按需在前面加 `sudo`）：

```bash
curl -fsSL https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases/latest/download/install.sh | sh
```

脚本会自动识别架构、下载对应二进制，交互引导填写路由器地址 / 账号 / 通知渠道，并注册为开机自启的 systemd 服务。下面是等价的手动步骤，供需要自定义的用户参考。

## 1. 获取程序

从 [Releases](https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases) 下载对应架构的可执行文件，并安装到固定路径：

- x86_64：`cpe-sms-forwarder-linux-amd64`
- ARM64：`cpe-sms-forwarder-linux-arm64`
- ARMv7：`cpe-sms-forwarder-linux-armv7`

```bash
sudo install -m 0755 cpe-sms-forwarder-linux-amd64 /usr/local/bin/cpe-sms-forwarder
```

或从源码编译（需安装 Go）：`make linux-amd64`（或 `linux-arm64` / `linux-arm`）。

## 2. 准备配置

```bash
sudo mkdir -p /etc/cpe-sms-forwarder
sudo cp config.yaml /etc/cpe-sms-forwarder/config.yaml
sudo vi /etc/cpe-sms-forwarder/config.yaml      # 填写 CPE 地址/账号/密码与通知渠道(notify.urls)
```

完整配置项见 [项目 README 的配置章节](../README.md#配置)。

## 3. 前台试运行

```bash
/usr/local/bin/cpe-sms-forwarder -config /etc/cpe-sms-forwarder/config.yaml
```

终端出现「CPE 登录成功」即正常；同时会收到一条「CPE 服务已上线」推送，可顺便确认通知配置无误。按 `Ctrl + C` 退出。

## 4. 注册为 systemd 服务

用内置命令安装并启动。`-config` 须为绝对路径：

```bash
sudo /usr/local/bin/cpe-sms-forwarder -service install -config /etc/cpe-sms-forwarder/config.yaml
sudo /usr/local/bin/cpe-sms-forwarder -service start
```

服务已设为开机自启，异常退出后自动重启。

## 5. 服务管理

```bash
sudo /usr/local/bin/cpe-sms-forwarder -service status
sudo /usr/local/bin/cpe-sms-forwarder -service stop
sudo /usr/local/bin/cpe-sms-forwarder -service restart
sudo /usr/local/bin/cpe-sms-forwarder -service uninstall
```

等价于 `systemctl {status,stop,restart} cpe-sms-forwarder`。

## 6. 查看日志

```bash
journalctl -u cpe-sms-forwarder -f
```

出现「CPE 登录成功」即正常运行。

## 7. 更新

```bash
sudo /usr/local/bin/cpe-sms-forwarder -service stop
sudo install -m 0755 新版二进制 /usr/local/bin/cpe-sms-forwarder
sudo /usr/local/bin/cpe-sms-forwarder -service start
```

配置文件无需改动。
