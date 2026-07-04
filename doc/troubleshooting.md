# 排障与反馈

#### **简体中文** | [English](troubleshooting_EN.md)

> [!WARNING]
> 以下内容由 AI 生成，仅供参考。

遇到问题时，先看日志定位现象，再对照常见问题；仍无法解决时按末尾指引提交 issue。

## 1. 查看日志

按部署方式选择对应命令：

| 部署方式 | 查看日志 |
| --- | --- |
| 软路由（OpenWrt / iStoreOS） | `logread -e cpe-sms-forwarder`，实时跟随 `logread -f` |
| Linux（systemd） | `journalctl -u cpe-sms-forwarder -f` |
| macOS（launchd） | `tail -f /var/log/cpe-sms-forwarder.err.log` |
| Windows 服务 | 可执行文件同目录的 `cpe-sms-forwarder.log` |
| 前台运行 | 直接看终端输出 |

正常运行时会周期性出现「开始轮询短信...」「CPE 登录成功」「获取到 N 个联系人, M 条短信」。

## 2. 常见问题

- **登录失败 `result=1`（已在别处登录）**：后台同一时间只允许一个用户在线，当前正被其他设备占用。程序会自动退避，超过 `poll.yield_minutes` 才强制登录；若希望更快接管，调小该值。
- **登录失败 `result=4`（用户名或密码错误）**：核对 `cpe.username` / `cpe.password`，与浏览器登录后台所用的一致。
- **收不到通知**：日志若出现「通知推送失败 [...]」，多为 `notify.urls` 写法有误或通知服务器不可达。对照 [shoutrrr 文档](https://shoutrrr.nickfedor.com/) 校验 URL；Bark 自建服务器需确认地址与端口可达。
- **启动后没有推送历史短信**：设计如此。首次启动只记录已有短信作为基线、不推送，之后到达的新短信才会推送，避免重启后重复轰炸。
- **「CPE 服务长时间无响应」告警**：程序仍在运行但持续拉不到数据，常见于后台被长期占用或路由器异常。检查是否一直处于退避、以及路由器后台是否正常。
- **一直在退避**：说明后台长期被其他设备占用。退避超过 `poll.yield_minutes` 会强制登录一次；可调小该值，或排查是谁在占用后台。

## 3. 提交问题

到 [GitHub Issues](https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/issues) 反馈，附上以下信息便于定位：

- 系统与架构（Linux/macOS 用 `uname -a`，Windows 注明版本）；
- 程序版本或对应的 commit；
- 复现步骤与预期 / 实际现象；
- 相关日志片段。

> [!IMPORTANT]
> 贴日志或配置前请先脱敏：移除 Bark 设备 Key、账号密码、完整手机号、公网地址与各类 Token。
