# macOS Deployment

#### [简体中文](macos.md) | **English**

> [!WARNING]
> The following content is AI-generated and provided for reference only.

Works on both Intel and Apple Silicon. The program is a single executable with no runtime dependencies and a built-in service installer that registers it as a launchd daemon in one command.

## One-Line Install (recommended)

Run in a terminal:

```bash
curl -fsSL https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases/latest/download/install.sh | sh
```

The script auto-detects your architecture, downloads the matching binary, interactively walks you through the router address / credentials / notification channels, and registers an auto-starting launchd daemon. The manual steps below are equivalent, for anyone who needs customization.

## 1. Get the Binary

Download from [Releases](https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases) and install it to a fixed path:

- Apple Silicon (M series): `cpe-sms-forwarder-darwin-arm64`
- Intel: `cpe-sms-forwarder-darwin-amd64`

```bash
sudo install -m 0755 cpe-sms-forwarder-darwin-arm64 /usr/local/bin/cpe-sms-forwarder
```

Or build from source (Go required): `make darwin-arm64` (or `darwin-amd64`).

## 2. Clear the Download Quarantine

Executables downloaded from a browser are flagged with the quarantine attribute by Gatekeeper; clear it before first use (binaries you built yourself are not affected):

```bash
sudo xattr -dr com.apple.quarantine /usr/local/bin/cpe-sms-forwarder
```

## 3. Prepare the Config

```bash
sudo mkdir -p /usr/local/etc/cpe-sms-forwarder
sudo cp config.yaml /usr/local/etc/cpe-sms-forwarder/config.yaml
sudo vi /usr/local/etc/cpe-sms-forwarder/config.yaml      # fill in CPE address/credentials and notification channels (notify.urls)
```

See the [Configuration section of the project README](../README_EN.md#configuration) for the full list of options.

## 4. Foreground Trial Run

```bash
/usr/local/bin/cpe-sms-forwarder -config /usr/local/etc/cpe-sms-forwarder/config.yaml
```

If the terminal shows "CPE 登录成功" (CPE login succeeded), it is working; you should also receive a "CPE service online" push, which conveniently confirms the notification config too. Press `Ctrl + C` to exit.

## 5. Register as a launchd Service

Install and start via the built-in command. `-config` must be an absolute path:

```bash
sudo /usr/local/bin/cpe-sms-forwarder -service install -config /usr/local/etc/cpe-sms-forwarder/config.yaml
sudo /usr/local/bin/cpe-sms-forwarder -service start
```

This registers a system-level LaunchDaemon (`/Library/LaunchDaemons/cpe-sms-forwarder.plist`) that auto-starts on boot and is relaunched after exit.

## 6. Service Management

```bash
sudo /usr/local/bin/cpe-sms-forwarder -service status
sudo /usr/local/bin/cpe-sms-forwarder -service stop
sudo /usr/local/bin/cpe-sms-forwarder -service restart
sudo /usr/local/bin/cpe-sms-forwarder -service uninstall
```

## 7. Viewing Logs

launchd writes the program's output to `/var/log/`. Runtime logs go to stderr, landing in `cpe-sms-forwarder.err.log`:

```bash
tail -f /var/log/cpe-sms-forwarder.err.log
```

## 8. Updating

```bash
sudo /usr/local/bin/cpe-sms-forwarder -service stop
sudo install -m 0755 new-binary /usr/local/bin/cpe-sms-forwarder
sudo /usr/local/bin/cpe-sms-forwarder -service start
```

The config file needs no changes.
