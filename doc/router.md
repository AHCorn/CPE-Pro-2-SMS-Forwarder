# 软路由部署（OpenWrt / iStoreOS）

#### **简体中文** | [English](router_EN.md)

> [!WARNING]
> 以下内容由 AI 生成，仅供参考。

适用于 OpenWrt 系软路由（含 iStoreOS、ImmortalWrt 等），程序内置 procd 服务安装命令。静态单文件，可常驻在低性能设备上。

## 一键安装（推荐）

SSH 登录路由器后（以 root 运行）执行：

```bash
curl -fsSL https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases/latest/download/install.sh | sh
```

脚本会自动识别架构、下载对应二进制，交互引导填写路由器地址 / 账号 / 通知渠道，并注册为开机自启的 procd 服务。下面是等价的手动步骤，供需要自定义的用户参考。

## 1. 选择架构

按软路由 CPU 选择对应二进制（可用 `uname -m` 确认）：

| 设备 | `uname -m` | 二进制 |
| --- | --- | --- |
| x86 软路由（J4125、N100 等） | `x86_64` | `cpe-sms-forwarder-linux-amd64` |
| ARM64 软路由（RK3568、树莓派等） | `aarch64` | `cpe-sms-forwarder-linux-arm64` |
| 32 位 ARM 设备 | `armv7l` | `cpe-sms-forwarder-linux-armv7` |
| 老旧 MIPS 路由器 | `mips` / `mipsel` | `cpe-sms-forwarder-linux-mipsle` |

## 2. 上传程序与配置

在本机准备好二进制与 `config.yaml`，scp 到路由器（架构按实际替换）：

```bash
ssh root@路由器IP 'mkdir -p /etc/cpe-sms-forwarder'
scp build/cpe-sms-forwarder-linux-arm64 root@路由器IP:/usr/bin/cpe-sms-forwarder
scp config.yaml root@路由器IP:/etc/cpe-sms-forwarder/config.yaml
```

二进制可从 [Releases](https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases) 下载，或本机 `make release` 编译得到。

## 3. 安装并启动

SSH 登录路由器：

```bash
chmod +x /usr/bin/cpe-sms-forwarder
vi /etc/cpe-sms-forwarder/config.yaml      # 填写 CPE 地址/账号/密码与通知渠道(notify.urls)

/usr/bin/cpe-sms-forwarder -service install -config /etc/cpe-sms-forwarder/config.yaml
/usr/bin/cpe-sms-forwarder -service start
```

安装命令会自动写入 `/etc/init.d/cpe-sms-forwarder`（procd），设为开机自启并在退出后自动拉起。

## 4. 服务管理

```bash
/usr/bin/cpe-sms-forwarder -service status
/usr/bin/cpe-sms-forwarder -service restart
/usr/bin/cpe-sms-forwarder -service uninstall
```

等价于 `/etc/init.d/cpe-sms-forwarder {status,restart,...}`。

## 5. 查看日志

```bash
logread -e cpe-sms-forwarder        # 查看历史
logread -f                          # 实时跟随
```

看到「CPE 登录成功」即正常；启动时还会推送一条「CPE 服务已上线」。

## 6. 更新

```bash
/usr/bin/cpe-sms-forwarder -service stop
# scp 新版二进制覆盖 /usr/bin/cpe-sms-forwarder 后
/usr/bin/cpe-sms-forwarder -service start
```

配置文件无需改动。

## 7. 关于自动避让

软路由长期在线轮询时，若您手动登录路由器后台，程序会自动退避、把后台让给您，超时后再恢复转发。该机制由 `poll.yield_minutes` 控制，详见 [项目 README 的配置章节](../README.md#配置)。
