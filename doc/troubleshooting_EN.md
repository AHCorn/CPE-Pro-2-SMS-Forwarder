# Troubleshooting and Feedback

#### [简体中文](troubleshooting.md) | **English**

> [!WARNING]
> The following content is AI-generated and provided for reference only.

When you hit a problem, check the logs first to pin down the symptom, then compare against the common issues below; if it still is not resolved, file an issue following the guidance at the end.

## 1. Viewing Logs

Pick the command matching your deployment:

| Deployment | View logs |
| --- | --- |
| Soft router (OpenWrt / iStoreOS) | `logread -e cpe-sms-forwarder`, follow live with `logread -f` |
| Linux (systemd) | `journalctl -u cpe-sms-forwarder -f` |
| macOS (launchd) | `tail -f /var/log/cpe-sms-forwarder.err.log` |
| Windows service | `cpe-sms-forwarder.log` next to the executable |
| Foreground | read the terminal output directly |

During normal operation you will periodically see "开始轮询短信..." (start polling SMS), "CPE 登录成功" (CPE login succeeded), and "获取到 N 个联系人, M 条短信" (fetched N contacts, M messages).

## 2. Common Issues

- **Login fails with `result=1` (already logged in elsewhere)**: the admin panel allows only one user online at a time, and it is currently held by another device. The program backs off automatically and only forces a login after `poll.yield_minutes`; lower that value if you want it to take over sooner.
- **Login fails with `result=4` (wrong username or password)**: check `cpe.username` / `cpe.password` match what you use to log into the admin panel in a browser.
- **No notifications received**: if the log shows "通知推送失败 [...]" (notification push failed), it is usually a malformed `notify.urls` or an unreachable notification server. Verify the URL against the [shoutrrr docs](https://shoutrrr.nickfedor.com/); for a self-hosted Bark server, confirm the address and port are reachable.
- **No historical SMS pushed after startup**: this is by design. On first startup it only records existing SMS as a baseline without pushing; only messages arriving afterward are pushed, avoiding a flood of duplicates after a restart.
- **"CPE service unresponsive for a long time" alert**: the program is still running but keeps failing to fetch data, commonly because the panel is held by another session for a long time or the router is malfunctioning. Check whether it is stuck yielding and whether the router admin panel is healthy.
- **Stuck yielding continuously**: the admin panel is held by another device long-term. Yielding beyond `poll.yield_minutes` forces a single login; lower that value, or investigate who is holding the panel.

## 3. Filing an Issue

Report at [GitHub Issues](https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/issues), including the following to help diagnose:

- OS and architecture (Linux/macOS: `uname -a`; Windows: note the version);
- program version or the corresponding commit;
- reproduction steps with expected vs. actual behavior;
- relevant log snippets.

> [!IMPORTANT]
> Before pasting logs or config, redact sensitive data: remove Bark device keys, credentials, full phone numbers, public addresses, and any tokens.
