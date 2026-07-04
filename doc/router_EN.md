# Soft Router Deployment (OpenWrt / iStoreOS)

#### [简体中文](router.md) | **English**

> [!WARNING]
> The following content is AI-generated and provided for reference only.

Works on OpenWrt-based soft routers (including iStoreOS, ImmortalWrt, etc.). The program ships with a built-in procd service installer. As a static single binary, it can run long-term on low-power devices.

## One-Line Install (recommended)

After logging into the router over SSH (run as root):

```bash
curl -fsSL https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases/latest/download/install.sh | sh
```

The script auto-detects your architecture, downloads the matching binary, interactively walks you through the router address / credentials / notification channels, and registers an auto-starting procd service. The manual steps below are equivalent, for anyone who needs customization.

## 1. Pick the Architecture

Choose the binary matching your router's CPU (confirm with `uname -m`):

| Device | `uname -m` | Binary |
| --- | --- | --- |
| x86 soft router (J4125, N100, etc.) | `x86_64` | `cpe-sms-forwarder-linux-amd64` |
| ARM64 soft router (RK3568, Raspberry Pi, etc.) | `aarch64` | `cpe-sms-forwarder-linux-arm64` |
| 32-bit ARM device | `armv7l` | `cpe-sms-forwarder-linux-armv7` |
| Older MIPS router | `mips` / `mipsel` | `cpe-sms-forwarder-linux-mipsle` |

## 2. Upload the Binary and Config

Prepare the binary and `config.yaml` locally, then scp them to the router (replace the architecture as appropriate):

```bash
ssh root@router-ip 'mkdir -p /etc/cpe-sms-forwarder'
scp build/cpe-sms-forwarder-linux-arm64 root@router-ip:/usr/bin/cpe-sms-forwarder
scp config.yaml root@router-ip:/etc/cpe-sms-forwarder/config.yaml
```

The binary can be downloaded from [Releases](https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases) or built locally with `make release`.

## 3. Install and Start

Log into the router over SSH:

```bash
chmod +x /usr/bin/cpe-sms-forwarder
vi /etc/cpe-sms-forwarder/config.yaml      # fill in CPE address/credentials and notification channels (notify.urls)

/usr/bin/cpe-sms-forwarder -service install -config /etc/cpe-sms-forwarder/config.yaml
/usr/bin/cpe-sms-forwarder -service start
```

The install command writes `/etc/init.d/cpe-sms-forwarder` (procd), sets it to auto-start on boot, and relaunches it after exit.

## 4. Service Management

```bash
/usr/bin/cpe-sms-forwarder -service status
/usr/bin/cpe-sms-forwarder -service restart
/usr/bin/cpe-sms-forwarder -service uninstall
```

Equivalent to `/etc/init.d/cpe-sms-forwarder {status,restart,...}`.

## 5. Viewing Logs

```bash
logread -e cpe-sms-forwarder        # view history
logread -f                          # follow live
```

"CPE 登录成功" (CPE login succeeded) means it is working; on startup it also sends a "CPE service online" push.

## 6. Updating

```bash
/usr/bin/cpe-sms-forwarder -service stop
# after scp-ing the new binary over /usr/bin/cpe-sms-forwarder
/usr/bin/cpe-sms-forwarder -service start
```

The config file needs no changes.

## 7. About Auto-Yield

When a soft router polls around the clock and you log into the router admin panel yourself, the program automatically backs off and leaves the panel to you, resuming forwarding after a timeout. This is controlled by `poll.yield_minutes`; see the [Configuration section of the project README](../README_EN.md#configuration) for details.
