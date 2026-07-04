# Windows Deployment

#### [简体中文](windows.md) | **English**

> [!WARNING]
> The following content is AI-generated and provided for reference only.

Works on Windows 10 / 11 and Windows Server. The program is a single `.exe` with no runtime dependencies and a built-in service installer, so it can register itself as a Windows service in one command (no NSSM or other third-party tools required).

## One-Line Install (recommended)

Open PowerShell **as Administrator** (search PowerShell in the Start menu, right-click, "Run as administrator") and run:

```powershell
irm https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases/latest/download/install.ps1 | iex
```

The script auto-detects your architecture, downloads the matching `.exe`, interactively walks you through the router address / credentials / notification channels, and registers an auto-starting Windows service. Once done, skip to [5. Service Management](#5-service-management). The manual steps below are equivalent, for anyone who needs a custom install location.

## 1. Get the Binary

Download the executable for your architecture from [Releases](https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases), place it in a fixed directory (e.g. `C:\cpe-sms-forwarder`), and rename it to `cpe-sms-forwarder.exe`:

- Most machines: `cpe-sms-forwarder-windows-amd64.exe`
- Windows on ARM: `cpe-sms-forwarder-windows-arm64.exe`

Or build from source (Go required):

```powershell
make windows-amd64
# Output: build\cpe-sms-forwarder-windows-amd64.exe
```

## 2. Prepare the Config

Create `config.yaml` in the same directory as the executable:

```yaml
cpe:
  host: "192.168.2.1"
  username: "admin"
  password: "your admin password"

notify:
  urls:
    - "bark://:yourDeviceKey@api.day.app"

poll:
  interval_seconds: 60
```

`notify.urls` also accepts Telegram, ntfy, Gotify, email, and other channels. See the [Configuration section of the project README](../README_EN.md#configuration) for the full list of options.

## 3. Foreground Trial Run

Open PowerShell in the program directory and run it in the foreground first to confirm the config is correct:

```powershell
cd C:\cpe-sms-forwarder
.\cpe-sms-forwarder.exe -config config.yaml
```

If the terminal shows "CPE 登录成功" (CPE login succeeded), it is working; you should also receive a "CPE service online" push, which conveniently confirms the notification config too. Press `Ctrl + C` to exit.

## 4. Register as a Windows Service

Open PowerShell **as Administrator** and install and start via the built-in command. `-config` must be an absolute path:

```powershell
cd C:\cpe-sms-forwarder
.\cpe-sms-forwarder.exe -service install -config C:\cpe-sms-forwarder\config.yaml
.\cpe-sms-forwarder.exe -service start
```

The service is set to auto-start and restarts automatically after an abnormal exit. You can also find `CPE Pro 2 SMS Forwarder` in the Services manager.

## 5. Service Management

```powershell
.\cpe-sms-forwarder.exe -service status      # check status
.\cpe-sms-forwarder.exe -service stop         # stop
.\cpe-sms-forwarder.exe -service restart      # restart
.\cpe-sms-forwarder.exe -service uninstall    # uninstall
```

## 6. Viewing Logs

A Windows service has no console, so the program writes logs to `cpe-sms-forwarder.log` in the same directory as the executable by default; you can also set a path via `log.file` in the config.

## 7. Updating

```powershell
.\cpe-sms-forwarder.exe -service stop
# Replace cpe-sms-forwarder.exe with the new version
.\cpe-sms-forwarder.exe -service start
```

The config file needs no changes.
