# CPE Pro 2 SMS Forwarder Windows 一键安装脚本。
# 交互引导：录入路由器地址与账号密码、选择通知渠道，自动下载对应架构的 .exe、
# 写入配置并注册为 Windows 服务（开机自启、异常自动重启）。
#
# 用法（在「以管理员身份运行」的 PowerShell 中）：
#   irm https://github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/releases/latest/download/install.ps1 | iex
#
# 可选环境变量：
#   $env:BASE_URL     自定义 .exe 下载前缀（局域网部署用，如 http://192.168.1.2:8000）；默认走 GitHub Releases。
#   $env:VERSION      指定发布版本 tag；默认 latest。
#   $env:INSTALL_DIR  自定义安装目录；默认 %ProgramFiles%\cpe-sms-forwarder。

$ErrorActionPreference = 'Stop'
# PowerShell 7.4+ 默认会因原生命令（如 -service 子命令）非零退出码直接抛异常；
# 这里关闭，改用显式 $LASTEXITCODE 判定，使 stop/uninstall 的预期失败可被忽略、安装/启动判定可控。
# 该变量在 Windows PowerShell 5.1 下不存在，赋值仅创建普通变量、无副作用。
$PSNativeCommandUseErrorActionPreference = $false
# Windows PowerShell 5.1 下 Invoke-WebRequest 渲染进度条会拖慢大文件下载，关闭以提速。
$ProgressPreference = 'SilentlyContinue'
try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch {}

$Repo    = 'AHCorn/CPE-Pro-2-SMS-Forwarder'
$BinName = 'cpe-sms-forwarder'
$Version = if ($env:VERSION) { $env:VERSION } else { 'latest' }
$OneLiner = "irm https://github.com/$Repo/releases/latest/download/install.ps1 | iex"

function Hr   { Write-Host ('=' * 60) -ForegroundColor Cyan }
function Step($n, $t) { Write-Host ''; Write-Host "[$n] $t" -ForegroundColor Cyan }
function Log($m)  { Write-Host "[安装] $m" -ForegroundColor Green }
function Warn($m) { Write-Host "[注意] $m" -ForegroundColor Yellow }

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Read-Default($prompt, $default) {
    if ($default) { $v = Read-Host "$prompt [$default]" } else { $v = Read-Host $prompt }
    if ([string]::IsNullOrEmpty($v)) { $default } else { $v }
}
function Read-RequiredDefault($prompt, $default) {
    while ($true) {
        $v = Read-Default $prompt $default
        if (-not [string]::IsNullOrEmpty($v)) { return $v }
        Warn '  此项不能为空，请重新输入。'
    }
}
function Read-Num($prompt, $default) {
    while ($true) {
        $v = Read-Default $prompt $default
        if ($v -match '^\d+$') { return $v }
        Warn '  请输入数字。'
    }
}
function Read-Secret($prompt) {
    while ($true) {
        $sec = Read-Host $prompt -AsSecureString
        $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec)
        try { $p = [Runtime.InteropServices.Marshal]::PtrToStringAuto($bstr) }
        finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr) }
        if (-not [string]::IsNullOrEmpty($p)) { return $p }
        # 非交互（stdin 重定向且读到空=EOF）时立即中止，避免必填项被无限重问刷屏。
        if ([Console]::IsInputRedirected) { throw '未读到输入（输入流已结束）。请在交互式 PowerShell 窗口中运行安装。' }
        Warn '  密码不能为空，请重新输入。'
    }
}
function Confirm-Default($prompt, $default) {
    switch -Regex (Read-Default $prompt $default) {
        '^(y|yes)$' { return $true }
        '^(n|no)$'  { return $false }
        default     { return ($default -eq 'Y') }
    }
}
# 转义 YAML 双引号标量里的反斜杠与引号，避免特殊字符的密码/地址破坏配置文件。
function ConvertTo-YamlScalar($s) { $s.Replace('\', '\\').Replace('"', '\"') }
# Enc 对嵌入 shoutrrr URL 的用户输入做 RFC3986 百分号编码（邮箱含 @、密码含特殊字符必须编码，
# 否则破坏 URL 解析）；shoutrrr 用 net/url 解析会自动解码还原。
function Enc($s) { [uri]::EscapeDataString($s) }

# Test-Checksum 校验下载二进制的 sha256：能取到校验和文件且含该条目则强校验，不匹配即抛错中止；
# 取不到（旧 Release / 自定义 BASE_URL 未提供）则告警跳过，不阻断安装。
function Test-Checksum($file, $asset, $sumUrl) {
    try {
        $text = (Invoke-WebRequest -Uri $sumUrl -UseBasicParsing).Content
    } catch {
        Warn '未获取到校验和文件，跳过完整性校验'
        return
    }
    $want = $null
    foreach ($line in ($text -split "`n")) {
        $parts = $line.Trim() -split '\s+'
        if ($parts.Count -ge 2 -and (($parts[-1] -replace '^\*', '') -eq $asset)) {
            $want = $parts[0].ToLower()
            break
        }
    }
    if (-not $want) { Warn "校验和文件中无 $asset 条目，跳过校验"; return }
    $got = (Get-FileHash -Algorithm SHA256 -Path $file).Hash.ToLower()
    if ($want -ne $got) { throw "二进制校验和不匹配，已中止安装（期望 $want，实得 $got）" }
    Log '完整性校验通过 (sha256)'
}

function Invoke-Install {
    Hr
    Write-Host '  CPE Pro 2 SMS Forwarder · 一键安装' -ForegroundColor White
    Write-Host '  自动下载 .exe、引导配置、注册为开机自启的 Windows 服务'
    Hr

    if (-not (Test-Admin)) {
        Warn '需要管理员权限，正在请求提权（将弹出 UAC 并打开新的管理员窗口继续）...'
        # RunAs 提权启动的是全新环境，不继承当前会话的自定义变量；若设置了 BASE_URL/VERSION/
        # INSTALL_DIR（局域网部署 / 指定版本 / 自定义目录），显式透传到提权进程，否则会丢失。
        # 未设置任何变量时 $pre 为空，下方命令与不透传时逐字节一致。
        $pre = ''
        foreach ($name in 'BASE_URL', 'VERSION', 'INSTALL_DIR') {
            $val = [Environment]::GetEnvironmentVariable($name)
            if ($val) { $pre += "`$env:$name = '$val'; " }
        }
        try {
            $spArgs = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-NoExit')
            if ($PSCommandPath) {
                # 从脚本文件运行：以管理员重跑该文件。
                if ($pre) { $spArgs += @('-Command', "$pre& `"$PSCommandPath`"") }
                else { $spArgs += @('-File', "`"$PSCommandPath`"") }
            }
            else {
                # irm | iex 场景无脚本文件：在管理员窗口里重跑一键命令。
                $spArgs += @('-Command', "$pre$OneLiner")
            }
            Start-Process -FilePath 'powershell.exe' -Verb RunAs -ArgumentList $spArgs
        }
        catch {
            Warn '提权被取消或失败。请「以管理员身份运行」PowerShell 后重新执行：'
            Write-Host "  $OneLiner"
        }
        return
    }

    # ---- 1. 环境检测 ----
    Step '1/4' '检测运行环境'
    $arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    switch ($arch) {
        'AMD64' { $goarch = 'amd64' }
        'ARM64' { $goarch = 'arm64' }
        default { throw "暂不支持的架构: $arch（仅提供 amd64 / arm64）" }
    }
    $asset      = "$BinName-windows-$goarch.exe"
    $installDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { Join-Path $env:ProgramFiles $BinName }
    $exe        = Join-Path $installDir "$BinName.exe"
    $conf       = Join-Path $installDir 'config.yaml'
    Write-Host "  系统架构: windows/$goarch  ->  二进制 $asset"
    Write-Host "  安装目录: $installDir"

    if ($env:BASE_URL) {
        $base = $env:BASE_URL.TrimEnd('/')
        $url = "$base/$asset"; $sumUrl = "$base/SHA256SUMS"
    }
    elseif ($Version -eq 'latest') {
        $url = "https://github.com/$Repo/releases/latest/download/$asset"
        $sumUrl = "https://github.com/$Repo/releases/latest/download/SHA256SUMS"
    }
    else {
        $url = "https://github.com/$Repo/releases/download/$Version/$asset"
        $sumUrl = "https://github.com/$Repo/releases/download/$Version/SHA256SUMS"
    }

    # ---- 2. 下载程序 ----
    Step '2/4' '下载程序'
    New-Item -ItemType Directory -Force -Path $installDir | Out-Null
    if (Test-Path $exe) {
        # Windows 无法覆盖正在运行的 .exe，先停止旧服务释放文件占用（未运行则忽略）。
        Log '检测到已有安装，先停止服务以便覆盖二进制 ...'
        try { & $exe -service stop *> $null } catch {}
        Start-Sleep -Seconds 1
    }
    Log "下载 $asset ..."
    # 先下到临时文件并校验，再 Move 覆盖正式路径：校验失败时保留原有可用二进制，
    # 不让未通过校验（损坏/被篡改）的下载顶替掉能正常运行的旧版本。
    $tmp = "$exe.download"
    Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing
    if (-not (Test-Path $tmp) -or (Get-Item $tmp).Length -le 0) {
        Remove-Item $tmp -Force -ErrorAction SilentlyContinue
        throw "下载内容为空: $url"
    }
    try { Test-Checksum $tmp $asset $sumUrl }
    catch { Remove-Item $tmp -Force -ErrorAction SilentlyContinue; throw }
    Move-Item -Force -Path $tmp -Destination $exe
    Log "已安装到 $exe"

    # ---- 3. 配置 ----
    $writeConf = $true
    if (Test-Path $conf) {
        Warn "已存在配置 $conf"
        $writeConf = Confirm-Default '覆盖现有配置吗? (y/N)' 'N'
        if (-not $writeConf) { Log '保留现有配置，跳过录入。' }
    }

    if ($writeConf) {
        Step '3/4' '配置路由器与通知'
        $cpeHost = Read-RequiredDefault 'CPE 路由器地址' '192.168.2.1'
        $cpeUser = Read-Default '登录用户名' 'admin'
        $cpePass = Read-Secret '登录密码（输入时不显示）'

        Write-Host ''
        Write-Host '  选择通知渠道（可多选，空格分隔编号）:' -ForegroundColor White
        Write-Host '    1) Bark        2) Telegram     3) ntfy'
        Write-Host '    4) Gotify      5) 邮件 (SMTP)   6) 自定义 shoutrrr URL'

        $urls = New-Object System.Collections.Generic.List[string]
        $selRounds = 0
        while ($true) {
            $choices = Read-Default '输入编号 (如: 1 3)' '1'
            foreach ($c in ($choices -split '\s+' | Where-Object { $_ })) {
                switch ($c) {
                    '1' {
                        $k = Read-Default '  Bark 设备 Key' ''
                        if (-not $k) { Warn '跳过 Bark：Key 为空'; continue }
                        $h = Read-Default '  Bark 服务器' 'api.day.app'
                        $g = Read-Default '  分组(可空)' ''
                        $s = Read-Default '  铃声(可空)' ''
                        $q = @(); if ($g) { $q += "group=$(Enc $g)" }; if ($s) { $q += "sound=$(Enc $s)" }
                        $u = "bark://:$(Enc $k)@$h"; if ($q.Count) { $u += '?' + ($q -join '&') }
                        $urls.Add($u); Log '  + Bark'
                    }
                    '2' {
                        $t = Read-Default '  Telegram Bot Token' ''
                        if (-not $t) { Warn '跳过 Telegram'; continue }
                        $ch = Read-Default '  目标 chat (如 @频道 或 chatID)' ''
                        if (-not $ch) { Warn '跳过 Telegram'; continue }
                        # token 含 ":"（shoutrrr 据此拆分），不编码；chats 的 @ 在 query 中合法。
                        $urls.Add("telegram://$t@telegram?chats=$ch"); Log '  + Telegram'
                    }
                    '3' {
                        $h = Read-Default '  ntfy 服务器' 'ntfy.sh'
                        $tp = Read-Default '  主题 topic' ''
                        if (-not $tp) { Warn '跳过 ntfy'; continue }
                        $urls.Add("ntfy://$h/$(Enc $tp)"); Log '  + ntfy'
                    }
                    '4' {
                        $h = Read-Default '  Gotify 地址 host[:port]' ''
                        if (-not $h) { Warn '跳过 Gotify'; continue }
                        $tk = Read-Default '  应用 token' ''
                        if (-not $tk) { Warn '跳过 Gotify'; continue }
                        $urls.Add("gotify://$h/$(Enc $tk)"); Log '  + Gotify'
                    }
                    '5' {
                        $h = Read-Default '  SMTP 服务器' ''
                        if (-not $h) { Warn '跳过邮件'; continue }
                        $p = Read-Default '  端口' '587'
                        $us = Read-Default '  邮箱账号' ''
                        $ps = Read-Secret '  邮箱密码/授权码'
                        $fr = Read-Default '  发件地址' $us
                        $to = Read-Default '  收件地址' $us
                        # 账号/密码进 userinfo、收发件地址进 query，均编码（邮箱含 @ 必须编码否则破坏 URL）。
                        $urls.Add("smtp://$(Enc $us):$(Enc $ps)@${h}:$p/?from=$(Enc $fr)&to=$(Enc $to)"); Log '  + 邮件'
                    }
                    '6' {
                        $cu = Read-Default '  粘贴完整 shoutrrr URL' ''
                        if ($cu) { $urls.Add($cu); Log '  + 自定义' } else { Warn '跳过自定义' }
                    }
                    default { Warn "忽略无效编号: $c" }
                }
            }
            if ($urls.Count -gt 0) { break }
            # 非交互（EOF）或异常多轮仍未配置出有效渠道时中止，避免无限重问刷屏。
            if ([Console]::IsInputRedirected) { throw '未读到输入（输入流已结束）。请在交互式 PowerShell 窗口中运行安装。' }
            $selRounds++
            if ($selRounds -ge 30) { throw '多次未选择到有效通知渠道，已中止。请重新运行安装并按编号选择渠道。' }
            Warn '尚未配置任何有效通知渠道，请重新选择。'
        }

        $interval = Read-Num '轮询间隔(秒)' '60'
        $yieldm   = Read-Num '后台被占用时最长退避分钟数(给手动登录留时间)' '5'
        $logPath  = Read-Default '日志文件路径(留空=安装目录下 cpe-sms-forwarder.log)' ''

        # 多渠道时询问是否启用可靠投递（失败渠道自动重试 + 降级提示）；单渠道无意义，默认关闭。
        $retryFailed = $false
        if ($urls.Count -gt 1) {
            $retryFailed = Confirm-Default '某渠道发送失败时自动重试，仍失败则经其他渠道发降级提示? (Y/n)' 'Y'
        }

        Write-Host ''; Hr
        Write-Host '  配置预览' -ForegroundColor White
        Write-Host "  路由器  : $cpeHost    用户: $cpeUser    密码: ******"
        Write-Host "  通知渠道: $($urls.Count) 个"
        Write-Host "  轮询间隔: $interval 秒    退避上限: $yieldm 分钟"
        if ($retryFailed) { Write-Host '  可靠投递: 已启用（失败渠道自动重试 + 降级提示）' }
        if ($logPath) { Write-Host "  日志文件: $logPath" } else { Write-Host '  日志文件: 安装目录下 cpe-sms-forwarder.log' }
        Write-Host "  配置文件: $conf"
        Hr
        if (-not (Confirm-Default '确认写入并安装? (Y/n)' 'Y')) { throw '已取消，未做改动。' }

        Log "写入配置 $conf"
        $urlLines = ($urls | ForEach-Object { '    - "' + $_ + '"' }) -join "`n"
        # 可选项按需拼接：retry_failed 仅多渠道启用时写入；log 段仅填了路径时写入（留空走默认日志）。
        $retryLine  = if ($retryFailed) { "`n  retry_failed: true" } else { '' }
        $logSection = if ($logPath) { "`n`nlog:`n  file: `"$(ConvertTo-YamlScalar $logPath)`"" } else { '' }
        # relogin_minutes 不写死：留空用程序内默认值，避免与 config.go 的默认重复后两边漂移。
        # 需自定义见 config.yaml 注释或设 CPE_RELOGIN_MINUTES。
        $content = @"
cpe:
  host: "$(ConvertTo-YamlScalar $cpeHost)"
  username: "$(ConvertTo-YamlScalar $cpeUser)"
  password: "$(ConvertTo-YamlScalar $cpePass)"

notify:
  urls:
$urlLines
  notify_yield: true$retryLine

poll:
  interval_seconds: $interval
  yield_minutes: $yieldm$logSection
"@
        [System.IO.File]::WriteAllText($conf, $content, (New-Object System.Text.UTF8Encoding($false)))
        # 配置含明文密码，而 Program Files 默认允许本机 Users 读取：移除继承 ACL，
        # 仅授予 SYSTEM(服务账户，只读) 与 Administrators(完全控制)，对齐 Unix 侧 chmod 0600。
        # 用 SID 而非组名，避免非英文 Windows 上 "Administrators" 被本地化导致 icacls 匹配失败。
        icacls $conf /inheritance:r /grant:r '*S-1-5-18:(R)' '*S-1-5-32-544:(F)' *> $null
        if ($LASTEXITCODE -ne 0) { Warn '锁定配置文件权限失败，请自行确保 config.yaml 不被其他用户读取。' }
    }

    # ---- 4. 注册并启动服务 ----
    Step '4/4' '注册并启动服务'
    # 先停止并卸载可能存在的旧服务，使重复运行（升级 / 改配置）保持幂等；不存在则忽略。
    try { & $exe -service stop *> $null } catch {}
    try { & $exe -service uninstall *> $null } catch {}
    & $exe -service install -config $conf
    if ($LASTEXITCODE -ne 0) { throw '服务安装失败' }
    & $exe -service start
    if ($LASTEXITCODE -ne 0) { Warn '启动命令返回非 0，请稍后查看状态/日志' }

    Write-Host ''; Hr
    Write-Host '  安装完成，服务已设为开机自启、异常自动重启。' -ForegroundColor Green
    Write-Host "  状态: `"$exe`" -service status"
    Write-Host "  日志: $installDir\$BinName.log"
    Hr
}

try { Invoke-Install }
catch { Write-Host "[错误] $($_.Exception.Message)" -ForegroundColor Red }
