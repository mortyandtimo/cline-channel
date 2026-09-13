<#
.SYNOPSIS
  把 cline-channel 插件安装到 CPA。

.DESCRIPTION
  复制预编译的 dist/cline-channel.so 到 CPA 的插件目录，
  已存在的旧版本会先备份成 .bak-<时间戳>，方便回滚。

.EXAMPLE
  # CPA 跑在 Docker 里（推荐）
  ./install.ps1 -Container cli-proxy-api

.EXAMPLE
  # CPA 直接跑在主机上
  ./install.ps1 -PluginDir "C:\cpa\plugins"
#>
param(
  [string]$Container = '',
  [string]$PluginDir = '',
  [string]$Source = (Join-Path $PSScriptRoot 'dist/cline-channel.so'),
  [string]$ContainerPath = '/CLIProxyAPI/plugins/cline-channel.so'
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path -LiteralPath $Source)) {
  throw "找不到插件文件：$Source`n请先运行 ./build.ps1，或改用仓库自带的 dist/cline-channel.so。"
}
if ($Container -eq '' -and $PluginDir -eq '') {
  throw '请指定 -Container（Docker 部署）或 -PluginDir（主机部署）之一。'
}

$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'

if ($Container -ne '') {
  Write-Host "→ 安装到容器 $Container : $ContainerPath"
  # 备份容器内旧版本，避免覆盖后无法回退
  docker exec $Container sh -c "if [ -f '$ContainerPath' ]; then cp '$ContainerPath' '$ContainerPath.bak-$stamp'; fi" 2>$null | Out-Null
  docker cp $Source "${Container}:${ContainerPath}"
  Write-Host '→ 重启 CPA 使其加载新插件'
  docker restart $Container | Out-Null
} else {
  if (-not (Test-Path -LiteralPath $PluginDir)) {
    throw "插件目录不存在：$PluginDir"
  }
  $target = Join-Path $PluginDir 'cline-channel.so'
  if (Test-Path -LiteralPath $target) {
    Copy-Item $target "$target.bak-$stamp"
    Write-Host "→ 已备份旧版本：cline-channel.so.bak-$stamp"
  }
  Copy-Item $Source $target -Force
  Write-Host "→ 已安装到 $target"
  Write-Host '→ 请重启 CPA 使其加载新插件'
}

Write-Host ''
Write-Host '完成。接下来：'
Write-Host '  1) 确认 CPA 的 config.yaml 里 plugins.enabled = true 且 configs.cline-channel.enabled = true'
Write-Host '  2) 打开面板填写 Cline API Key：'
Write-Host '     http://<CPA 地址>/v0/resource/plugins/cline-channel/panel'
Write-Host '  3) 建议同时设置 disable-cooling: true（见 README「重要」一节）'
