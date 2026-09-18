<#
.SYNOPSIS
  从 Windows 一键上传并更新 Linux 上的 netmon 采集端。
.EXAMPLE
  .\upload-netmon.ps1 -Server 192.168.161.128 -User wuki
  .\upload-netmon.ps1 -Server 192.168.161.128 -DryRun
#>
param(
  [Parameter(Mandatory = $true)][string]$Server,
  [string]$User = "wuki",
  [string]$RemoteDir = "~",
  [string]$Binary = "",
  [string]$Config = "~/config.toml",
  [string]$Service = "netmon",
  [int]$Port = 8080,
  [switch]$DryRun
)

$ErrorActionPreference = "Stop"
# 兼容不同调用方式：$PSScriptRoot 为空时用脚本自身路径推导
$scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
$script = Join-Path $scriptDir "update-netmon.sh"
if ([string]::IsNullOrWhiteSpace($Binary)) {
  $Binary = Join-Path (Split-Path -Parent $scriptDir) "netmon-linux"
}
if (-not (Test-Path -LiteralPath $Binary)) { throw "找不到本地二进制：$Binary" }
if (-not (Test-Path -LiteralPath $script)) { throw "找不到脚本：$script" }

# Windows 检出可能把 .sh 变成 CRLF，会导致 Linux 报 "bad interpreter"：
# 这里先转成 LF 的临时副本再上传。
$lfScript = Join-Path $env:TEMP "update-netmon-lf.sh"
$text = [System.IO.File]::ReadAllText($script).Replace("`r`n", "`n").Replace("`r", "`n")
[System.IO.File]::WriteAllText($lfScript, $text, (New-Object System.Text.UTF8Encoding($false)))

$target = "$User@$Server"
$cmds = @(
  "scp `"$Binary`" ${target}:$RemoteDir/netmon-linux",
  "scp `"$lfScript`" ${target}:$RemoteDir/update-netmon.sh",
  "ssh $target `"chmod +x $RemoteDir/update-netmon.sh; $RemoteDir/update-netmon.sh -b $RemoteDir/netmon-linux -c $Config -s $Service -p $Port`""
)

Write-Host "目标服务器：$target" -ForegroundColor Cyan
Write-Host "本地二进制：$Binary"
foreach ($c in $cmds) {
  Write-Host "  $c" -ForegroundColor DarkGray
  if (-not $DryRun) { Invoke-Expression $c }
}
if ($DryRun) { Write-Host "（演练模式：未实际执行）" -ForegroundColor Yellow }
else { Write-Host "上传并更新完成；如失败可运行：ssh $target '$RemoteDir/update-netmon.sh --rollback'" -ForegroundColor Green }