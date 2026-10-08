<#
.SYNOPSIS
  在 Windows 上构建采集端 Windows 可执行文件 netmon.exe（供本机离线分析 / 客户端 sidecar 使用）。
.DESCRIPTION
  - 纯 Go 构建，无需 CGO、无需管理员权限、不需要安装驱动；
  - 默认输出 <仓库>\bin\netmon.exe（bin/ 已在 .gitignore 中，不入库）；
  - 加 -StageForClient 时额外复制到 client\src-tauri\binaries\netmon.exe，
    供打包进 MeTD 安装包（该目录同样不入库）。
.EXAMPLE
  .\build-netmon.ps1
  .\build-netmon.ps1 -StageForClient
  .\build-netmon.ps1 -OutDir D:\tmp\netmon
#>
param(
  [string]$OutDir = "",
  [switch]$StageForClient
)

$ErrorActionPreference = "Stop"
$scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
$repoRoot = Split-Path -Parent (Split-Path -Parent $scriptDir)
if ([string]::IsNullOrWhiteSpace($OutDir)) { $OutDir = Join-Path $repoRoot "bin" }
$OutDir = [System.IO.Path]::GetFullPath($OutDir)
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
  throw "找不到 go 命令，请先安装 Go 1.25+ 并加入 PATH。"
}

$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
$target = Join-Path $OutDir "netmon.exe"

Push-Location $repoRoot
try {
  Write-Host "构建：GOOS=$env:GOOS GOARCH=$env:GOARCH CGO_ENABLED=$env:CGO_ENABLED go build -trimpath -o `"$target`" ./cmd/netmon" -ForegroundColor Cyan
  & go build -trimpath -o $target ./cmd/netmon
  if ($LASTEXITCODE -ne 0) { throw "go build 失败（退出码 $LASTEXITCODE）" }
} finally {
  Pop-Location
}
if (-not (Test-Path -LiteralPath $target)) { throw "构建结束但未找到产物：$target" }

$item = Get-Item -LiteralPath $target
$hash = (Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash
Write-Host ("产物：{0}（{1:N0} 字节）" -f $item.FullName, $item.Length) -ForegroundColor Green
Write-Host "SHA256：$hash"

if ($StageForClient) {
  $stageDir = Join-Path $repoRoot "client\src-tauri\binaries"
  New-Item -ItemType Directory -Force -Path $stageDir | Out-Null
  $staged = Join-Path $stageDir "netmon.exe"
  Copy-Item -LiteralPath $target -Destination $staged -Force
  Write-Host "已复制到客户端 sidecar 位置：$staged" -ForegroundColor Green
  Write-Host "打包前请在 client\src-tauri\tauri.conf.json 的 bundle 中加入：" -ForegroundColor Yellow
  Write-Host '  "resources": ["binaries/netmon.exe"]'
}
