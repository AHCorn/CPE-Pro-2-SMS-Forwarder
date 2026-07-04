# Linux Deployment

#### [简体中文](linux.md) | **English**

> [!WARNING]
> The following content is AI-generated and provided for reference only.

Works on common systemd-based distributions (Debian, Ubuntu, CentOS, etc.). The program has a built-in service installer that registers it as a systemd service in one command. For soft routers (OpenWrt / iStoreOS), see [router_EN.md](router_EN.md).

## One-Line Install (recommended)

Run in a terminal (prefix with `sudo` as needed when not root):

```bash
curl -fsSL https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases/latest/download/install.sh | sh
```

The script auto-detects your architecture, downloads the matching binary, interactively walks you through the router address / credentials / notification channels, and registers an auto-starting systemd service. The manual steps below are equivalent, for anyone who needs customization.

## 1. Get the Binary

Download the executable for your architecture from [Releases](https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases) and install it to a fixed path:

- x86_64: `cpe-sms-forwarder-linux-amd64`
- ARM64: `cpe-sms-forwarder-linux-arm64`
- ARMv7: `cpe-sms-forwarder-linux-armv7`

```bash
sudo install -m 0755 cpe-sms-forwarder-linux-amd64 /usr/local/bin/cpe-sms-forwarder
```

Or build from source (Go required): `make linux-amd64` (or `linux-arm64` / `linux-arm`).

## 2. Prepare the Config

```bash
sudo mkdir -p /etc/cpe-sms-forwarder
sudo cp config.yaml /etc/cpe-sms-forwarder/config.yaml
sudo vi /etc/cpe-sms-forwarder/config.yaml      # fill in CPE address/credentials and notification channels (notify.urls)
```

See the [Configuration section of the project README](../README_EN.md#configuration) for the full list of options.

## 3. Foreground Trial Run

```bash
/usr/local/bin/cpe-sms-forwarder -config /etc/cpe-sms-forwarder/config.yaml
```

If the terminal shows "CPE 登录成功" (CPE login succeeded), it is working; you should also receive a "CPE service online" push, which conveniently confirms the notification config too. Press `Ctrl + C` to exit.

## 4. Register as a systemd Service

Install and start via the built-in command. `-config` must be an absolute path:

```bash
sudo /usr/local/bin/cpe-sms-forwarder -service install -config /etc/cpe-sms-forwarder/config.yaml
sudo /usr/local/bin/cpe-sms-forwarder -service start
```

The service is set to auto-start and restarts automatically after an abnormal exit.

## 5. Service Management

```bash
sudo /usr/local/bin/cpe-sms-forwarder -service status
sudo /usr/local/bin/cpe-sms-forwarder -service stop
sudo /usr/local/bin/cpe-sms-forwarder -service restart
sudo /usr/local/bin/cpe-sms-forwarder -service uninstall
```

Equivalent to `systemctl {status,stop,restart} cpe-sms-forwarder`.

## 6. Viewing Logs

```bash
journalctl -u cpe-sms-forwarder -f
```

"CPE 登录成功" (CPE login succeeded) means it is running normally.

## 7. Updating

```bash
sudo /usr/local/bin/cpe-sms-forwarder -service stop
sudo install -m 0755 new-binary /usr/local/bin/cpe-sms-forwarder
sudo /usr/local/bin/cpe-sms-forwarder -service start
```

The config file needs no changes.
