# 题目 09 本机教学环境部署脚本（Windows / PowerShell）
# 流程：质量门禁 → 构建 → 停旧版 → 起新版 → 健康检查 → 业务冒烟 → 写部署记录；任一步失败自动回滚。
# 用法：powershell -ExecutionPolicy Bypass -File deploy/deploy.ps1 -Port 18080
param(
    [int]$Port = 18080,
    [string]$Dsn = "",
    [int]$TimeoutSeconds = 60,
    [string]$Environment = "local"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$releaseDir = Join-Path $PSScriptRoot "releases"
$currentFile = Join-Path $releaseDir "current.json"
$historyFile = Join-Path $releaseDir "history.json"
$evidenceDir = Join-Path $root "docs/evidence"
New-Item -ItemType Directory $releaseDir -Force | Out-Null
New-Item -ItemType Directory $evidenceDir -Force | Out-Null
Set-Location $root

function Write-Step($msg) { Write-Host "[deploy] $msg" -ForegroundColor Cyan }
function Fail($msg) { throw "[deploy] $msg" }

# 读取配置中的 MySQL DSN（未显式传参时）
if (-not $Dsn) {
    $cfg = Get-Content (Join-Path $root "configs/config.yaml") -Raw -Encoding UTF8
    $m = [regex]::Match($cfg, '(?m)^database:\s*\r?\n\s*driver:\s*(\w+)\s*\r?\n\s*dsn:\s*"?([^"\r\n]+)"?')
    if (-not $m.Success) { Fail "无法从 configs/config.yaml 解析 database.dsn" }
    $Dsn = $m.Groups[2].Value.Trim()
}
if ($Dsn -notmatch 'tcp\(') { Fail "DSN 不是 MySQL 连接串：$Dsn" }

$commit = (git rev-parse --short HEAD).Trim()
$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$tag = "$commit-$stamp"
$binary = Join-Path $releaseDir "gateway-$tag.exe"
$runID = if ($env:GITHUB_RUN_ID) { "gh-$($env:GITHUB_RUN_ID)" } else { "local-$stamp" }

Write-Step "1/6 质量门禁（go vet + 单元测试）"
go vet ./... | Out-Host
if ($LASTEXITCODE -ne 0) { Fail "go vet 未通过，禁止部署" }
go test -count=1 ./internal/fleetlog/... | Out-Host
if ($LASTEXITCODE -ne 0) { Fail "单元测试未通过，禁止部署" }

Write-Step "2/6 构建制品 gateway-$tag.exe"
go build -o $binary ./cmd/gateway
if ($LASTEXITCODE -ne 0 -or -not (Test-Path $binary)) { Fail "构建失败" }

# 记录上一可用版本，供回滚使用
$previous = $null
if (Test-Path $currentFile) {
    $previous = Get-Content $currentFile -Raw -Encoding UTF8 | ConvertFrom-Json
}

Write-Step "3/6 停止旧版本"
if ($previous -and $previous.pid) {
    Stop-Process -Id $previous.pid -Force -ErrorAction SilentlyContinue
}
Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue |
    Select-Object -ExpandProperty OwningProcess -Unique |
    ForEach-Object {
        $procPath = (Get-Process -Id $_ -ErrorAction SilentlyContinue).Path
        if ($procPath -and $procPath.StartsWith($releaseDir)) { Stop-Process -Id $_ -Force -ErrorAction SilentlyContinue }
        else { Fail "端口 $Port 被非本脚本部署的进程占用（PID $_，$procPath），请先处理" }
    }
Start-Sleep -Seconds 1

Write-Step "4/6 启动新版本（端口 $Port）"
$env:FLEETLOG_DB_PATH = ""
$env:FLEETLOG_DB_DSN = $Dsn
$env:FLEETLOG_HTTP_PORT = "$Port"
$env:FLEETLOG_TIMEOUT_SECONDS = "$TimeoutSeconds"
$stdout = Join-Path $releaseDir "gateway-$tag.out.log"
$stderr = Join-Path $releaseDir "gateway-$tag.err.log"
$proc = Start-Process -FilePath $binary -WorkingDirectory $root -PassThru `
    -RedirectStandardOutput $stdout -RedirectStandardError $stderr

Write-Step "5/6 健康检查"
$base = "http://127.0.0.1:$Port"
$healthy = $false
for ($i = 1; $i -le 30; $i++) {
    Start-Sleep -Milliseconds 500
    try {
        $h = Invoke-RestMethod "$base/healthz" -TimeoutSec 2
        if ($h.status -eq "ok") { $healthy = $true; break }
    } catch { }
    if ($proc.HasExited) { break }
}
if (-not $healthy) {
    Get-Content $stderr -Encoding UTF8 -ErrorAction SilentlyContinue | Select-Object -Last 10 | Out-Host
    Write-Step "健康检查失败，回滚到上一版本"
    if ($previous -and $previous.binary -and (Test-Path $previous.binary)) {
        $env:FLEETLOG_DB_DSN = $previous.dsn
        Start-Process -FilePath $previous.binary -WorkingDirectory $root -PassThru | Out-Null
        Write-Step "已回滚到 $($previous.tag)"
    }
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Fail "部署失败：健康检查未通过"
}

Write-Step "6/6 业务冒烟"
$smoke = @()
function Add-Smoke($name, $ok, $detail) {
    $script:smoke += [pscustomobject]@{ name = $name; ok = $ok; detail = $detail }
}
try {
    $accounts = (Invoke-RestMethod "$base/api/v1/demo/accounts" -TimeoutSec 5).data.accounts
    Add-Smoke "预置账号接口" ($accounts.Count -eq 3) "账号数=$($accounts.Count)"
    $apps = Invoke-RestMethod "$base/api/v1/applications" -TimeoutSec 5
    Add-Smoke "申请列表接口" ($null -ne $apps.data) "返回申请条数=$($apps.data.Count)"
    $page = Invoke-WebRequest "$base/web/index.html" -UseBasicParsing -TimeoutSec 5
    Add-Smoke "业务页面可访问" ($page.StatusCode -eq 200) "HTTP $($page.StatusCode)"
    $latest = Invoke-RestMethod "$base/api/v1/campaigns/latest" -TimeoutSec 5
    Add-Smoke "活动总览接口" ($null -ne $latest) "status=$($latest.data.status)"
} catch {
    Add-Smoke "冒烟请求异常" $false $_.Exception.Message
}
$smoke | ForEach-Object { Write-Host ("  [{0}] {1} - {2}" -f $(if ($_.ok) { "PASS" } else { "FAIL" }), $_.name, $_.detail) }
$smokeFailed = ($smoke | Where-Object { -not $_.ok }).Count
if ($smokeFailed -gt 0) {
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Fail "冒烟未通过（$smokeFailed 项失败），已停止新版本，请执行 deploy/rollback.ps1 恢复"
}

# 写部署记录
$record = [pscustomobject]@{
    tag          = $tag
    commit       = $commit
    binary       = $binary
    pid          = $proc.Id
    port         = $Port
    url          = $base
    dsn          = $Dsn
    timeout      = $TimeoutSeconds
    environment  = $Environment
    pipeline_run = $runID
    deployed_at  = (Get-Date -Format "yyyy-MM-dd HH:mm:ss")
    smoke        = $smoke
}
$record | ConvertTo-Json -Depth 5 | Set-Content $currentFile -Encoding UTF8
$history = @()
if (Test-Path $historyFile) {
    $parsedHist = Get-Content $historyFile -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($parsedHist -is [System.Array]) { $history = @($parsedHist | Where-Object { $_ }) }
    elseif ($parsedHist) { $history = @($parsedHist) }
}
if ($previous) { $history += $previous }
$history | ConvertTo-Json -Depth 5 | Set-Content $historyFile -Encoding UTF8

$md = @()
$md += "# 部署记录 $tag"
$md += ""
$md += "| 项 | 值 |"
$md += "| --- | --- |"
$md += "| 提交 | ``$commit`` |"
$md += "| 版本标识 | ``$tag`` |"
$md += "| 流水线/运行标识 | ``$runID`` |"
$md += "| 环境 | $Environment（本机教学环境） |"
$md += "| 运行地址 | $base |"
$md += "| 端口 | $Port |"
$md += "| 超时阈值 | $TimeoutSeconds 秒 |"
$md += "| 数据库 | $($Dsn -replace ':[^:@]+@', ':***@') |"
$md += "| 部署时间 | $($record.deployed_at) |"
$md += "| 制品 | ``$binary`` |"
$md += ""
$md += "## 健康检查与冒烟"
$md += ""
$md += "| 检查 | 结果 | 说明 |"
$md += "| --- | --- | --- |"
$md += "| /healthz | PASS | status=ok |"
foreach ($s in $smoke) { $md += "| $($s.name) | $(if ($s.ok) { "PASS" } else { "FAIL" }) | $($s.detail) |" }
$md += ""
$md += "## 回滚方式"
$md += ""
$md += '```powershell'
$md += "powershell -ExecutionPolicy Bypass -File deploy/rollback.ps1"
$md += '```'
$md += ""
$md += "回滚脚本会读取 ``deploy/releases/history.json`` 中上一版本，停止当前进程并重新拉起，再执行健康检查。"
$md | Set-Content (Join-Path $evidenceDir "deploy-$tag.md") -Encoding UTF8

Write-Step "部署成功：$base（版本 $tag，PID $($proc.Id)）"
Write-Step "部署记录：docs/evidence/deploy-$tag.md"
