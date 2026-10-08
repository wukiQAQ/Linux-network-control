<#
.SYNOPSIS
  在 Windows 上单机启动 netmon 引擎，对本地 pcap 文件做离线流量分析，并打开浏览器仪表盘。
.DESCRIPTION
  对应《双模式设计文档》的 M3（离线 pcap 分析）：
  - 零权限：不需要管理员、不需要安装驱动、不抓实时流量，只读你指定的 pcap 文件；
  - 采集端 0.23.4 起，离线回放的查询窗口跟随 pcap 报文时间（不是墙钟），
    所以 TOP 榜 / 历史曲线 / 会话明细的数字彼此一致；
  - 数据落在独立数据目录（默认 %LOCALAPPDATA%\MeTD\local-data），
    与远程 Linux 采集端的数据、与其它本机会话互不混存。
.EXAMPLE
  .\start-local-analysis.ps1 -Pcap D:\samples\demo.pcap
  .\start-local-analysis.ps1 -Pcap .\demo.pcap -Port 18090 -NoBrowser
  .\start-local-analysis.ps1 -Stop
#>
param(
  [string]$Pcap = "",
  [int]$Port = 18080,
  [string]$DataDir = "",
  [string]$Netmon = "",
  [int]$TimeoutSec = 20,
  [switch]$NoBrowser,
  [switch]$Stop
)

$ErrorActionPreference = "Stop"
$scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
$repoRoot = Split-Path -Parent (Split-Path -Parent $scriptDir)

function Get-DefaultDataDir {
  if (-not [string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
    return (Join-Path $env:LOCALAPPDATA "MeTD\local-data")
  }
  if (-not [string]::IsNullOrWhiteSpace($env:USERPROFILE)) {
    return (Join-Path $env:USERPROFILE "AppData\Local\MeTD\local-data")
  }
  throw "无法确定本机数据目录：环境变量 LOCALAPPDATA / USERPROFILE 都为空，请显式指定 -DataDir。"
}

if ([string]::IsNullOrWhiteSpace($DataDir)) { $DataDir = Get-DefaultDataDir }
$DataDir = [System.IO.Path]::GetFullPath($DataDir)
$pidFile = Join-Path $DataDir "local-engine.pid"
$outLog = Join-Path $DataDir "local-engine.log"
$errLog = Join-Path $DataDir "local-engine.err.log"
$healthUrl = "http://127.0.0.1:$Port/api/v1/health"
$baseUrl = "http://127.0.0.1:$Port"

# -Stop：停掉上一次用本脚本启动的引擎（按 pid 文件，不动别的 netmon 进程）
if ($Stop) {
  if (-not (Test-Path -LiteralPath $pidFile)) {
    Write-Host "没有找到运行记录（$pidFile），本机引擎可能未启动。" -ForegroundColor Yellow
    exit 0
  }
  $oldPid = 0
  [void][int]::TryParse((Get-Content -LiteralPath $pidFile -Raw).Trim(), [ref]$oldPid)
  if ($oldPid -gt 0) {
    $proc = Get-Process -Id $oldPid -ErrorAction SilentlyContinue
    if ($proc) {
      Stop-Process -Id $oldPid -Force
      Write-Host "已停止本机引擎（PID $oldPid）。" -ForegroundColor Green
    } else {
      Write-Host "PID $oldPid 已不在运行。" -ForegroundColor Yellow
    }
  }
  Remove-Item -LiteralPath $pidFile -Force -ErrorAction SilentlyContinue
  exit 0
}

if ([string]::IsNullOrWhiteSpace($Pcap)) {
  throw "必须用 -Pcap 指定要分析的 pcap 文件，例如：.\start-local-analysis.ps1 -Pcap D:\samples\demo.pcap"
}
if (-not (Test-Path -LiteralPath $Pcap)) { throw "找不到 pcap 文件：$Pcap" }
$pcapFull = (Resolve-Path -LiteralPath $Pcap).Path

function Resolve-NetmonExe {
  param([string]$Explicit)
  $candidates = @()
  if (-not [string]::IsNullOrWhiteSpace($Explicit)) { $candidates += $Explicit }
  $candidates += @(
    (Join-Path $scriptDir "netmon.exe"),
    (Join-Path $repoRoot "bin\netmon.exe"),
    (Join-Path $repoRoot "netmon.exe")
  )
  foreach ($c in $candidates) {
    if (Test-Path -LiteralPath $c) { return (Resolve-Path -LiteralPath $c).Path }
  }
  throw ("找不到 netmon.exe。请先构建 Windows 采集端，例如：`n" +
    "  cd `"$repoRoot`"`n" +
    "  `$env:GOOS=`"windows`"; `$env:GOARCH=`"amd64`"; go build -o `"$scriptDir\netmon.exe`" ./cmd/netmon`n" +
    "或用 -Netmon 指定已有可执行文件的路径。")
}

$exe = Resolve-NetmonExe -Explicit $Netmon

# 端口占用检查：先在环回上试绑一次，避免"启动了却连不上"
$listener = $null
try {
  $listener = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, $Port)
  $listener.Start()
} catch {
  throw "端口 $Port 已被占用（或被拒绝绑定），请用 -Port 换一个端口。原始错误：$($_.Exception.Message)"
} finally {
  if ($listener) { $listener.Stop() }
}

New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
if (Test-Path -LiteralPath $pidFile) {
  $oldPid = 0
  [void][int]::TryParse((Get-Content -LiteralPath $pidFile -Raw).Trim(), [ref]$oldPid)
  if ($oldPid -gt 0 -and (Get-Process -Id $oldPid -ErrorAction SilentlyContinue)) {
    throw "本机引擎已在运行（PID $oldPid）。请先执行：.\start-local-analysis.ps1 -Stop"
  }
  Remove-Item -LiteralPath $pidFile -Force -ErrorAction SilentlyContinue
}

# 与客户端 sidecar 完全相同的参数契约（见 client/src/localsource.js）：
#   -replay-file <pcap> -offline -data-dir <目录> -listen 127.0.0.1:<端口>
$argLine = "-replay-file `"$pcapFull`" -offline -data-dir `"$DataDir`" -listen 127.0.0.1:$Port"
Write-Host "本机引擎：$exe" -ForegroundColor Cyan
Write-Host "分析文件：$pcapFull"
Write-Host "数据目录：$DataDir"
Write-Host "监听地址：$baseUrl"

$proc = Start-Process -FilePath $exe -ArgumentList $argLine -PassThru -WindowStyle Hidden `
  -RedirectStandardOutput $outLog -RedirectStandardError $errLog
Set-Content -LiteralPath $pidFile -Value $proc.Id -Encoding Ascii

# 轮询健康检查，等引擎起来（离线回放可能要先读完 pcap 头部）
$deadline = (Get-Date).AddSeconds([Math]::Max(5, $TimeoutSec))
$ready = $false
while ((Get-Date) -lt $deadline) {
  Start-Sleep -Milliseconds 400
  if ($proc.HasExited) {
    Write-Host "引擎启动失败（退出码 $($proc.ExitCode)）。日志尾部：" -ForegroundColor Red
    if (Test-Path -LiteralPath $errLog) { Get-Content -LiteralPath $errLog -Tail 20 }
    if (Test-Path -LiteralPath $outLog) { Get-Content -LiteralPath $outLog -Tail 20 }
    Remove-Item -LiteralPath $pidFile -Force -ErrorAction SilentlyContinue
    exit 1
  }
  try {
    $resp = Invoke-WebRequest -Uri $healthUrl -UseBasicParsing -TimeoutSec 3
    if ($resp.StatusCode -eq 200) { $ready = $true; break }
  } catch { }
}
if (-not $ready) {
  Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
  Remove-Item -LiteralPath $pidFile -Force -ErrorAction SilentlyContinue
  throw "等待引擎就绪超时（$TimeoutSec 秒）：$healthUrl 无响应。引擎已停止，请查看 $errLog"
}

# 打印一段概览，便于命令行直接确认"确实分析出了东西"
try {
  $now = Invoke-WebRequest -Uri "$baseUrl/api/v1/traffic/now" -UseBasicParsing -TimeoutSec 5 | Select-Object -ExpandProperty Content
  $nowObj = $now | ConvertFrom-Json
  Write-Host ("引擎已就绪：采集端 {0}，数据源 {1}，平台 {2}，数据时间 {3}" -f $nowObj.version, $nowObj.source, $nowObj.os, $nowObj.ts) -ForegroundColor Green
  if ($nowObj.features -notcontains "capture.offline") {
    Write-Host "提示：该引擎未声明 capture.offline 能力，离线分析可能不完整（建议使用采集端 0.23.4 及以上的 netmon.exe）。" -ForegroundColor Yellow
  }
  $topn = Invoke-WebRequest -Uri "$baseUrl/api/v1/topn?since=3600&n=3" -UseBasicParsing -TimeoutSec 10 | Select-Object -ExpandProperty Content
  $topnObj = $topn | ConvertFrom-Json
  Write-Host ("近一小时：会话 {0} 条，字节 {1}" -f $topnObj.total_flows, $topnObj.total_bytes)
  if ($topnObj.by_ip) {
    foreach ($e in ($topnObj.by_ip | Select-Object -First 3)) {
      Write-Host ("  TOP IP {0}：{1} 条会话 / {2} 字节" -f $e.key, $e.flows, $e.bytes)
    }
  }
} catch {
  Write-Host "（概览查询失败，不影响使用：$($_.Exception.Message)）" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "浏览器打开：$baseUrl" -ForegroundColor Green
Write-Host "停止本机引擎：.\start-local-analysis.ps1 -Stop   （PID $($proc.Id)）" -ForegroundColor DarkGray
if (-not $NoBrowser) { Start-Process $baseUrl }
