#!/bin/sh
# CPE Pro 2 SMS Forwarder 一键安装脚本（Linux / macOS / OpenWrt）。
# 交互引导：选择通知渠道、录入路由器地址与账号密码，自动下载对应架构二进制、
# 写入配置并注册为系统服务。支持 curl -fsSL <url> | sh（交互从 /dev/tty 读取）。
# Windows 请改用 install.ps1，见 doc/windows.md。
#
# 可选环境变量：
#   BASE_URL  自定义二进制下载前缀（局域网部署用，如 http://192.168.1.2:8000）；默认走 GitHub Releases。
#   VERSION   指定发布版本 tag；默认 latest。
#   BIN_DIR   自定义二进制安装目录；默认按平台取 /usr/bin 或 /usr/local/bin。
#   CONF_DIR  自定义配置目录；默认按平台取 /etc/... 或 /usr/local/etc/...。
set -eu

# 顶层进程 PID：交互输入在 $(...) 子 shell 中读取，遇 EOF 时子 shell 内的 exit 无法终止主流程，
# 故改由子 shell kill 此 PID 强制中止，避免必填项在 EOF 下被循环重问而无限刷屏。
TOP_PID=$$

REPO="AHCorn/CPE-Pro-2-SMS-Forwarder"
BIN_NAME="cpe-sms-forwarder"
VERSION="${VERSION:-latest}"

# 仅在输出到终端时上色，避免重定向 / 管道里混入转义码。
if [ -t 1 ]; then
  C_GREEN='\033[32m'; C_YELLOW='\033[33m'; C_RED='\033[31m'; C_CYAN='\033[36m'; C_BOLD='\033[1m'; C_OFF='\033[0m'
else
  C_GREEN=''; C_YELLOW=''; C_RED=''; C_CYAN=''; C_BOLD=''; C_OFF=''
fi

log()  { printf '%b[安装]%b %s\n' "$C_GREEN" "$C_OFF" "$*"; }
warn() { printf '%b[注意]%b %s\n' "$C_YELLOW" "$C_OFF" "$*"; }
die()  { printf '%b[错误]%b %s\n' "$C_RED" "$C_OFF" "$*" >&2; exit 1; }
hr()   { printf '%b============================================================%b\n' "$C_CYAN" "$C_OFF"; }
step() { printf '\n%b[%s]%b %b%s%b\n' "$C_CYAN" "$1" "$C_OFF" "$C_BOLD" "$2" "$C_OFF"; }

[ -e /dev/tty ] || die "需要交互式终端运行本脚本（若用管道安装，请确保终端可用）。"

hr
printf '  %bCPE Pro 2 SMS Forwarder · 一键安装%b\n' "$C_BOLD" "$C_OFF"
printf '  自动下载二进制、引导配置、注册为开机自启的系统服务\n'
hr

# 交互输入统一从 /dev/tty 读取，保证 curl | sh 下仍可交互。
# abort_eof 在交互输入流结束（EOF）时提示并强制中止整个安装：在 $(...) 子 shell 里仅靠 exit
# 无法终止主流程，必须 kill 顶层进程，否则无默认值的必填项会被无限重问刷屏。
abort_eof() {
  printf '\n%b[错误]%b 未读到输入（输入流已结束）。请在交互式终端中运行安装。\n' "$C_RED" "$C_OFF" > /dev/tty 2>/dev/null || true
  kill -TERM "$TOP_PID" 2>/dev/null || true
  exit 1
}
ask() { # ask 提示 默认值 -> stdout
  _d="${2:-}"
  if [ -n "$_d" ]; then printf '%s [%s]: ' "$1" "$_d" > /dev/tty; else printf '%s: ' "$1" > /dev/tty; fi
  IFS= read -r _ans < /dev/tty || abort_eof
  [ -z "$_ans" ] && _ans="$_d"
  printf '%s' "$_ans"
}
ask_required() { # ask_required 提示 默认值 -> stdout（空则重问）
  while :; do
    _r="$(ask "$1" "${2:-}")"
    [ -n "$_r" ] && { printf '%s' "$_r"; return; }
    printf '%b  此项不能为空，请重新输入。%b\n' "$C_YELLOW" "$C_OFF" > /dev/tty
  done
}
ask_num() { # ask_num 提示 默认值 -> stdout（非正整数则重问）
  while :; do
    _n="$(ask "$1" "$2")"
    case "$_n" in
      ''|*[!0-9]*) printf '%b  请输入数字。%b\n' "$C_YELLOW" "$C_OFF" > /dev/tty ;;
      *) printf '%s' "$_n"; return ;;
    esac
  done
}
ask_secret() { # ask_secret 提示 -> stdout（输入隐藏，空则重问）
  while :; do
    printf '%s: ' "$1" > /dev/tty
    stty -echo < /dev/tty 2>/dev/null || true
    if ! IFS= read -r _s < /dev/tty; then
      stty echo < /dev/tty 2>/dev/null || true
      abort_eof
    fi
    stty echo < /dev/tty 2>/dev/null || true
    printf '\n' > /dev/tty
    [ -n "$_s" ] && { printf '%s' "$_s"; return; }
    printf '%b  密码不能为空，请重新输入。%b\n' "$C_YELLOW" "$C_OFF" > /dev/tty
  done
}
confirm() { # confirm 提示 默认(Y/N) -> 0=是 1=否
  case "$(ask "$1" "$2")" in
    y|Y|yes|YES) return 0 ;;
    n|N|no|NO)   return 1 ;;
    *) [ "$2" = "Y" ] && return 0 || return 1 ;;
  esac
}
mktmp() { mktemp 2>/dev/null || echo "/tmp/${BIN_NAME}.$$.$1"; }
# esc 转义 YAML 双引号标量里的反斜杠与引号，避免特殊字符的密码/地址破坏配置文件。
esc() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }
# urlenc 对要嵌入 shoutrrr URL 的用户输入做 RFC3986 百分号编码（仅保留 unreserved）。
# 邮箱账号含 @、密码含 @ : / ? # 等会破坏 URL 解析；shoutrrr 用 net/url 解析会自动解码还原。
urlenc() {
  _old_lc="${LC_ALL:-}"; LC_ALL=C; _s="$1"; _o=""
  while [ -n "$_s" ]; do
    _c="${_s%"${_s#?}"}"  # 取首字节（LC_ALL=C 下逐字节处理）
    case "$_c" in
      [a-zA-Z0-9.~_-]) _o="$_o$_c" ;;
      # & 255 取无符号字节值，避免高位字节（如中文 UTF-8）被符号扩展成 FFFF.. 而编码错误。
      *) _o="$_o$(printf '%%%02X' "$(( $(printf '%d' "'$_c") & 255 ))")" ;;
    esac
    _s="${_s#?}"
  done
  LC_ALL="$_old_lc"
  printf '%s' "$_o"
}

# ---- 1. 环境检测 ----
step "1/4" "检测运行环境"
OS="$(uname -s)"; ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)        GOARCH="amd64" ;;
  aarch64|arm64)       GOARCH="arm64" ;;
  armv7l|armv7|armhf)  GOARCH="armv7" ;;
  mips|mipsel|mipsle)  GOARCH="mipsle" ;;
  *) die "暂不支持的架构: $ARCH" ;;
esac

IS_OPENWRT=0
if [ -f /etc/openwrt_release ] || grep -qi openwrt /etc/os-release 2>/dev/null; then IS_OPENWRT=1; fi

case "$OS" in
  Linux)
    GOOS="linux"
    DEF_CONF_DIR="/etc/$BIN_NAME"
    if [ "$IS_OPENWRT" -eq 1 ]; then DEF_BIN_DIR="/usr/bin"; else DEF_BIN_DIR="/usr/local/bin"; fi ;;
  Darwin)
    GOOS="darwin"; DEF_BIN_DIR="/usr/local/bin"; DEF_CONF_DIR="/usr/local/etc/$BIN_NAME"
    case "$GOARCH" in armv7|mipsle) die "macOS 无 $GOARCH 版本" ;; esac ;;
  *) die "暂不支持的系统: $OS（Windows 请用 install.ps1，见 doc/windows.md）" ;;
esac
BIN_DIR="${BIN_DIR:-$DEF_BIN_DIR}"
CONF_DIR="${CONF_DIR:-$DEF_CONF_DIR}"
CONF="$CONF_DIR/config.yaml"
ASSET="${BIN_NAME}-${GOOS}-${GOARCH}"

PLATFORM_LABEL="$GOOS/$GOARCH"
[ "$IS_OPENWRT" -eq 1 ] && PLATFORM_LABEL="$PLATFORM_LABEL (OpenWrt)"
printf '  系统架构: %b%s%b  ->  二进制 %b%s%b\n' "$C_BOLD" "$PLATFORM_LABEL" "$C_OFF" "$C_BOLD" "$ASSET" "$C_OFF"
printf '  安装目录: %s\n  配置目录: %s\n' "$BIN_DIR" "$CONF_DIR"

# ---- 提权 ----
SUDO=""
if [ "$(id -u)" -ne 0 ]; then
  if command -v sudo >/dev/null 2>&1; then SUDO="sudo"; else die "需要 root 权限（用 root 运行或安装 sudo）。"; fi
fi

# ---- 下载工具 ----
dl() { # dl url out
  # https 链接限定只走 https，避免被（TLS 内的）重定向降级到明文 http；
  # BASE_URL 允许用户显式用 http（内网/离线），故按 scheme 判定、不一刀切。
  _proto=""
  case "$1" in https://*) _proto="--proto =https" ;; esac
  if   command -v curl >/dev/null 2>&1; then curl -fL $_proto --progress-bar "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then wget -q "$1" -O "$2"
  elif command -v uclient-fetch >/dev/null 2>&1; then uclient-fetch -q "$1" -O "$2"
  else die "未找到 curl / wget / uclient-fetch，无法下载。"; fi
}
# sha256_of 计算文件 sha256（取首个可用工具）；无工具返回非 0。
sha256_of() {
  if   command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  elif command -v shasum     >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  elif command -v openssl    >/dev/null 2>&1; then openssl dgst -sha256 "$1" | awk '{print $NF}'
  else return 1; fi
}
# verify_sha256 file asset sumsurl：能取到校验和文件且含该条目则强校验，不匹配即中止；
# 取不到校验和文件（旧 Release / 自定义 BASE_URL 未提供）或无 sha256 工具则告警跳过，不阻断安装。
verify_sha256() {
  _sums="$(mktmp sums)"
  if ! dl "$3" "$_sums" 2>/dev/null || [ ! -s "$_sums" ]; then
    warn "未获取到校验和文件，跳过完整性校验"; rm -f "$_sums"; return 0
  fi
  _want="$(awk -v a="$2" '{f=$2; sub(/^\*/,"",f)} f==a {print $1}' "$_sums" | head -n1)"
  rm -f "$_sums"
  [ -n "$_want" ] || { warn "校验和文件中无 $2 条目，跳过校验"; return 0; }
  _got="$(sha256_of "$1")" || { warn "未找到 sha256 工具，跳过校验"; return 0; }
  [ "$_want" = "$_got" ] || die "二进制校验和不匹配，已中止安装（期望 $_want，实得 $_got）"
  log "完整性校验通过 (sha256)"
}
if [ -n "${BASE_URL:-}" ]; then URL="${BASE_URL%/}/${ASSET}"; SUMS_URL="${BASE_URL%/}/SHA256SUMS"
elif [ "$VERSION" = "latest" ]; then URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"; SUMS_URL="https://github.com/${REPO}/releases/latest/download/SHA256SUMS"
else URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"; SUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/SHA256SUMS"; fi

# ---- 2. 下载程序 ----
step "2/4" "下载程序"
TMP="$(mktmp bin)"
log "下载 $ASSET ..."
dl "$URL" "$TMP" || die "下载失败: $URL"
[ -s "$TMP" ] || die "下载内容为空: $URL"
verify_sha256 "$TMP" "$ASSET" "$SUMS_URL"

log "安装到 $BIN_DIR/$BIN_NAME"
$SUDO mkdir -p "$BIN_DIR" "$CONF_DIR"
$SUDO cp "$TMP" "$BIN_DIR/$BIN_NAME"
$SUDO chmod 0755 "$BIN_DIR/$BIN_NAME"
rm -f "$TMP"

# ---- 3. 配置 ----
WRITE_CONF=1
if [ -f "$CONF" ]; then
  warn "已存在配置 $CONF"
  if confirm '覆盖现有配置吗? (y/N)' 'N'; then WRITE_CONF=1; else WRITE_CONF=0; log "保留现有配置，跳过录入。"; fi
fi

if [ "$WRITE_CONF" -eq 1 ]; then
  step "3/4" "配置路由器与通知"
  CPE_HOST="$(ask_required 'CPE 路由器地址' '192.168.2.1')"
  CPE_USER="$(ask '登录用户名' 'admin')"
  CPE_PASS="$(ask_secret '登录密码（输入时不显示）')"

  printf '\n  %b选择通知渠道%b（可多选，空格分隔编号）:\n' "$C_BOLD" "$C_OFF" > /dev/tty
  cat > /dev/tty <<'MENU'
    1) Bark        2) Telegram     3) ntfy
    4) Gotify      5) 邮件 (SMTP)   6) 自定义 shoutrrr URL
MENU
  URLS=""
  add_url() { URLS="${URLS}    - \"$1\"
"; }

  _sel_rounds=0
  while :; do
    _sel_rounds=$((_sel_rounds + 1))
    [ "$_sel_rounds" -gt 30 ] && die "多次未选择到有效通知渠道，已中止。请重新运行安装并按编号选择渠道。"
    CHOICES="$(ask '输入编号 (如: 1 3)' '1')"
    for c in $CHOICES; do
      case "$c" in
        1) k="$(ask '  Bark 设备 Key' '')"; [ -n "$k" ] || { warn '跳过 Bark：Key 为空'; continue; }
           h="$(ask '  Bark 服务器' 'api.day.app')"; g="$(ask '  分组(可空)' '')"; s="$(ask '  铃声(可空)' '')"
           q=""; [ -n "$g" ] && q="group=$(urlenc "$g")"
           [ -n "$s" ] && { _se="sound=$(urlenc "$s")"; [ -n "$q" ] && q="$q&$_se" || q="$_se"; }
           u="bark://:$(urlenc "$k")@$h"; [ -n "$q" ] && u="$u?$q"; add_url "$u"; log "  + Bark" ;;
        2) t="$(ask '  Telegram Bot Token' '')"; [ -n "$t" ] || { warn '跳过 Telegram'; continue; }
           ch="$(ask '  目标 chat (如 @频道 或 chatID)' '')"; [ -n "$ch" ] || { warn '跳过 Telegram'; continue; }
           # token 含 ":"（shoutrrr 据此拆分），不编码；chats 的 @ 在 query 中合法。
           add_url "telegram://$t@telegram?chats=$ch"; log "  + Telegram" ;;
        3) h="$(ask '  ntfy 服务器' 'ntfy.sh')"; tp="$(ask '  主题 topic' '')"; [ -n "$tp" ] || { warn '跳过 ntfy'; continue; }
           add_url "ntfy://$h/$(urlenc "$tp")"; log "  + ntfy" ;;
        4) h="$(ask '  Gotify 地址 host[:port]' '')"; [ -n "$h" ] || { warn '跳过 Gotify'; continue; }
           tk="$(ask '  应用 token' '')"; [ -n "$tk" ] || { warn '跳过 Gotify'; continue; }
           add_url "gotify://$h/$(urlenc "$tk")"; log "  + Gotify" ;;
        5) h="$(ask '  SMTP 服务器' '')"; [ -n "$h" ] || { warn '跳过邮件'; continue; }
           p="$(ask '  端口' '587')"; us="$(ask '  邮箱账号' '')"; ps="$(ask_secret '  邮箱密码/授权码')"
           fr="$(ask '  发件地址' "$us")"; to="$(ask '  收件地址' "$us")"
           # 账号/密码进 userinfo、收发件地址进 query，均编码（邮箱含 @ 必须编码否则破坏 URL）。
           add_url "smtp://$(urlenc "$us"):$(urlenc "$ps")@$h:$p/?from=$(urlenc "$fr")&to=$(urlenc "$to")"; log "  + 邮件" ;;
        6) cu="$(ask '  粘贴完整 shoutrrr URL' '')"; [ -n "$cu" ] && { add_url "$cu"; log "  + 自定义"; } || warn '跳过自定义' ;;
        *) warn "忽略无效编号: $c" ;;
      esac
    done
    [ -n "$URLS" ] && break
    warn "尚未配置任何有效通知渠道，请重新选择。"
  done
  NURL=$(printf '%s' "$URLS" | grep -c '://' 2>/dev/null || true)

  INTERVAL="$(ask_num '轮询间隔(秒)' '60')"
  YIELDM="$(ask_num '后台被占用时最长退避分钟数(给手动登录留时间)' '5')"
  LOG_PATH="$(ask '日志文件路径(留空=随系统日志 journalctl/log show/logread)' '')"

  # 多渠道时询问是否启用可靠投递（失败渠道自动重试 + 降级提示）；单渠道无意义，默认关闭。
  RETRY_FAILED=""
  if [ "${NURL:-0}" -gt 1 ] && confirm '某渠道发送失败时自动重试，仍失败则经其他渠道发降级提示? (Y/n)' 'Y'; then
    RETRY_FAILED="true"
  fi

  printf '\n'; hr
  printf '  %b配置预览%b\n' "$C_BOLD" "$C_OFF"
  printf '  路由器  : %s    用户: %s    密码: ******\n' "$CPE_HOST" "$CPE_USER"
  printf '  通知渠道: %s 个\n' "$NURL"
  printf '  轮询间隔: %s 秒    退避上限: %s 分钟\n' "$INTERVAL" "$YIELDM"
  [ -n "$RETRY_FAILED" ] && printf '  可靠投递: 已启用（失败渠道自动重试 + 降级提示）\n'
  if [ -n "$LOG_PATH" ]; then printf '  日志文件: %s\n' "$LOG_PATH"; else printf '  日志输出: 随系统日志\n'; fi
  printf '  配置文件: %s\n' "$CONF"
  hr
  confirm '确认写入并安装? (Y/n)' 'Y' || die "已取消，未做改动。"

  log "写入配置 $CONF"
  TMPC="$(mktmp conf)"
  {
    printf 'cpe:\n  host: "%s"\n  username: "%s"\n  password: "%s"\n\n' "$(esc "$CPE_HOST")" "$(esc "$CPE_USER")" "$(esc "$CPE_PASS")"
    printf 'notify:\n  urls:\n%s  notify_yield: true\n' "$URLS"
    [ -n "$RETRY_FAILED" ] && printf '  retry_failed: %s\n' "$RETRY_FAILED"
    printf '\n'
    # relogin_minutes 不写死：留空用程序内默认值，避免与 config.go 的默认重复后两边漂移。
    # 需自定义见 config.yaml 注释或设 CPE_RELOGIN_MINUTES。
    printf 'poll:\n  interval_seconds: %s\n  yield_minutes: %s\n' "$INTERVAL" "$YIELDM"
    # 日志留空时不写 log 段：服务由系统日志收集（journald/launchd/logread）；填写则落地到文件（自动轮转）。
    [ -n "$LOG_PATH" ] && printf '\nlog:\n  file: "%s"\n' "$(esc "$LOG_PATH")"
  } > "$TMPC"
  # 配置含明文密码：用 install -m 0600 让目标文件一创建即为 0600，避免 cp 后再 chmod
  # 之间存在短暂 world-readable 窗口；install 不可用（个别精简 busybox）才回退 cp+chmod。
  if command -v install >/dev/null 2>&1; then
    $SUDO install -m 0600 "$TMPC" "$CONF"
  else
    $SUDO cp "$TMPC" "$CONF"
    $SUDO chmod 0600 "$CONF"
  fi
  rm -f "$TMPC"
fi

# ---- 4. 注册并启动服务 ----
step "4/4" "注册并启动服务"
INITD="/etc/init.d/$BIN_NAME"
if [ "$IS_OPENWRT" -eq 1 ]; then
  # OpenWrt 用 procd：直接写原生 init 脚本而非走 kardianos。
  # kardianos 的 Linux 探测顺序里 rcs 排在 procd 前，而 OpenWrt 同时满足 rcs 命中条件
  # （/etc/init.d/rcS + /etc/inittab 含 ::sysinit:..rcS 且通常无 service 命令），会被注册成
  # 非 procd 的 sysv/rcs 脚本，开机不被 procd 拉起。故此处手写 procd 脚本，确保开机自启与自动重启。
  log "写入 procd 服务 $INITD"
  TMPI="$(mktmp initd)"
  cat > "$TMPI" <<EOF
#!/bin/sh /etc/rc.common
USE_PROCD=1
START=95
STOP=10
PROG="$BIN_DIR/$BIN_NAME"
CONF="$CONF"
start_service() {
    procd_open_instance
    procd_set_param command "\$PROG" -config "\$CONF"
    procd_set_param respawn 3600 5 0
    procd_set_param stdout 1
    procd_set_param stderr 1
    procd_close_instance
}
EOF
  $SUDO "$INITD" stop >/dev/null 2>&1 || true
  $SUDO cp "$TMPI" "$INITD"
  $SUDO chmod 0755 "$INITD"
  rm -f "$TMPI"
  $SUDO "$INITD" enable || warn "enable 返回非 0，可能影响开机自启"
  $SUDO "$INITD" restart || warn "启动返回非 0，请稍后查看 logread"
else
  # systemd / launchd 由 kardianos 正确注册。先停止并卸载旧服务，使重复运行（升级 / 改配置）幂等。
  $SUDO "$BIN_DIR/$BIN_NAME" -service stop >/dev/null 2>&1 || true
  $SUDO "$BIN_DIR/$BIN_NAME" -service uninstall >/dev/null 2>&1 || true
  $SUDO "$BIN_DIR/$BIN_NAME" -service install -config "$CONF" || die "服务安装失败"
  $SUDO "$BIN_DIR/$BIN_NAME" -service start || warn "启动命令返回非 0，请稍后查看状态/日志"
fi

printf '\n'; hr
printf '  %b安装完成%b，服务已设为开机自启、异常自动重启。\n' "$C_GREEN" "$C_OFF"
if   [ "$IS_OPENWRT" -eq 1 ]; then
  printf '  状态: %s%s status\n' "${SUDO:+$SUDO }" "$INITD"
  printf '  日志: logread -e %s -f\n' "$BIN_NAME"
elif [ "$GOOS" = "darwin" ]; then
  printf '  状态: %s%s -service status\n' "${SUDO:+$SUDO }" "$BIN_NAME"
  printf '  日志: tail -f /var/log/%s.err.log\n' "$BIN_NAME"
else
  printf '  状态: %s%s -service status\n' "${SUDO:+$SUDO }" "$BIN_NAME"
  printf '  日志: journalctl -u %s -f\n' "$BIN_NAME"
fi
hr
