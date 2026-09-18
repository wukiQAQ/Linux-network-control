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
  [string]$Binary = (Join-Path (Split-Path -Parent $PSScriptRoot) "netmon-linux"),
  [string]$Config = "~/config.toml",
  [string]$Service = "netmon",
  [int]$Port = 8080,
  [switch]$DryRun
)

$ErrorActionPreference = "Stop"
$script = Join-Path $PSScriptRoot "update-netmon.sh"
if (-not (Test-Path -LiteralPath $Binary)) { throw "找不到本地二进制：$Binary" }
if (-not (Test-Path -LiteralPath $script)) { throw "找不到脚本：$script" }

$target = "$User@$Server"
$cmds = @(
  "scp `"$Binary`" `"$script`" ${target}:$RemoteDir/",
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