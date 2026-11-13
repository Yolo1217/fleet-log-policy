# 题目 09 回滚脚本：恢复到上一次可用版本并做健康检查
# 用法：powershell -ExecutionPolicy Bypass -File deploy/rollback.ps1 [-Port 18080]
param([int]$Port = 18080)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$releaseDir = Join-Path $PSScriptRoot "releases"
$currentFile = Join-Path $releaseDir "current.json"
$historyFile = Join-Path $releaseDir "history.json"
Set-Location $root

if (-not (Test-Path $historyFile)) { throw "没有回滚历史（deploy/releases/history.json 不存在），无法回滚" }
$parsed = Get-Content $historyFile -Raw -Encoding UTF8 | ConvertFrom-Json
$history = @()
if ($parsed -is [System.Array]) { $history = @($parsed | Where-Object { $_ }) }
elseif ($parsed) { $history = @($parsed) }
if ($history.Count -eq 0) { throw "回滚历史为空" }
$target = $history[-1]
if (-not (Test-Path $target.binary)) { throw "上一版本制品不存在：$($target.binary)" }

if (Test-Path $currentFile) {
    $current = Get-Content $currentFile -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($current.pid) { Stop-Process -Id $current.pid -Force -ErrorAction SilentlyContinue }
}
Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue |
    Select-Object -ExpandProperty OwningProcess -Unique |
    ForEach-Object { Stop-Process -Id $_ -Force -ErrorAction SilentlyContinue }
Start-Sleep -Seconds 1

$env:FLEETLOG_DB_PATH = ""
$env:FLEETLOG_DB_DSN = $target.dsn
$env:FLEETLOG_HTTP_PORT = "$Port"
$env:FLEETLOG_TIMEOUT_SECONDS = "$($target.timeout)"
$proc = Start-Process -FilePath $target.binary -WorkingDirectory $root -PassThru `
    -RedirectStandardOutput (Join-Path $releaseDir "rollback-$($target.tag).out.log") `
    -RedirectStandardError (Join-Path $releaseDir "rollback-$($target.tag).err.log")

$base = "http://127.0.0.1:$Port"
$ok = $false
for ($i = 1; $i -le 30; $i++) {
    Start-Sleep -Milliseconds 500
    try {
        if ((Invoke-RestMethod "$base/healthz" -TimeoutSec 2).status -eq "ok") { $ok = $true; break }
    } catch { }
    if ($proc.HasExited) { break }
}
if (-not $ok) { throw "回滚后健康检查未通过，请人工介入" }

$target | Add-Member -NotePropertyName pid -NotePropertyValue $proc.Id -Force
$target | ConvertTo-Json -Depth 5 | Set-Content $currentFile -Encoding UTF8
$history = @($history | Select-Object -First ($history.Count - 1) | Where-Object { $_ })
$history | ConvertTo-Json -Depth 5 | Set-Content $historyFile -Encoding UTF8

$line = "| $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss') | $($target.tag) | $($target.commit) | 回滚 |"
Add-Content (Join-Path $root "docs/evidence/deploy-rollback.log") $line -Encoding UTF8
Write-Host "[rollback] 已恢复到 $($target.tag)（$base）" -ForegroundColor Yellow
