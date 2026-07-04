# Windows 部署

#### **简体中文** | [English](windows_EN.md)

> [!WARNING]
> 以下内容由 AI 生成，仅供参考。

适用于 Windows 10 / 11 及 Windows Server。程序为单个 `.exe`，无运行时依赖，内置服务安装命令，可一键注册为 Windows 服务（无需 NSSM 等第三方工具）。

## 一键安装（推荐）

以**管理员身份**打开 PowerShell（开始菜单搜索 PowerShell → 右键「以管理员身份运行」），执行：

```powershell
irm https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases/latest/download/install.ps1 | iex
```

脚本会自动识别架构、下载对应 `.exe`，交互引导填写路由器地址 / 账号 / 通知渠道，并注册为开机自启的 Windows 服务。完成后可直接跳到 [5. 服务管理](#5-服务管理)。下面是等价的手动步骤，供需要自定义安装位置的用户参考。

## 1. 获取程序

从 [Releases](https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases) 下载对应架构的可执行文件，放入固定目录（如 `C:\cpe-sms-forwarder`），并重命名为 `cpe-sms-forwarder.exe`：

- 绝大多数机器：`cpe-sms-forwarder-windows-amd64.exe`
- Windows on ARM：`cpe-sms-forwarder-windows-arm64.exe`

或从源码编译（需安装 Go）：

```powershell
make windows-amd64
# 产物位于 build\cpe-sms-forwarder-windows-amd64.exe
```

## 2. 准备配置

在可执行文件同目录新建 `config.yaml`：

```yaml
cpe:
  host: "192.168.2.1"
  username: "admin"
  password: "你的后台密码"

notify:
  urls:
    - "bark://:你的设备Key@api.day.app"

poll:
  interval_seconds: 60
```

`notify.urls` 也可填 Telegram、ntfy、Gotify、邮件等渠道，完整配置项见 [项目 README 的配置章节](../README.md#配置)。

## 3. 前台试运行

以 PowerShell 进入程序目录，先前台运行确认配置无误：

```powershell
cd C:\cpe-sms-forwarder
.\cpe-sms-forwarder.exe -config config.yaml
```

终端出现「CPE 登录成功」即正常；同时会收到一条「CPE 服务已上线」推送，可顺便确认通知配置无误。按 `Ctrl + C` 退出。

## 4. 注册为 Windows 服务

以**管理员身份**打开 PowerShell，用内置命令安装并启动。`-config` 须为绝对路径：

```powershell
cd C:\cpe-sms-forwarder
.\cpe-sms-forwarder.exe -service install -config C:\cpe-sms-forwarder\config.yaml
.\cpe-sms-forwarder.exe -service start
```

服务已设为开机自启，异常退出后自动重启。也可在「服务」管理器中找到 `CPE Pro 2 SMS Forwarder`。

## 5. 服务管理

```powershell
.\cpe-sms-forwarder.exe -service status      # 查看状态
.\cpe-sms-forwarder.exe -service stop         # 停止
.\cpe-sms-forwarder.exe -service restart      # 重启
.\cpe-sms-forwarder.exe -service uninstall    # 卸载
```

## 6. 查看日志

Windows 服务没有控制台，程序默认将日志写入可执行文件同目录的 `cpe-sms-forwarder.log`，也可在配置中通过 `log.file` 指定路径。

## 7. 更新

```powershell
.\cpe-sms-forwarder.exe -service stop
# 用新版替换 cpe-sms-forwarder.exe
.\cpe-sms-forwarder.exe -service start
```

配置文件无需改动。
