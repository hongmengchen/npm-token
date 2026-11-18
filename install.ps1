# npm-token 安装脚本（Windows）
#
#   irm https://raw.githubusercontent.com/hongmengchen/npm-token/main/install.ps1 | iex
#
# 可选参数：
#   -Version v0.1.0   装指定版本（默认最新）
#   -BinDir C:\path   装到指定目录（默认 %LOCALAPPDATA%\npm-token）
param(
    [string]$Version = "",
    [string]$BinDir = ""
)

$ErrorActionPreference = "Stop"
try {
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    $OutputEncoding = [System.Text.Encoding]::UTF8
} catch { }

$repo = "hongmengchen/npm-token"
$bin = "npm-token"

function Fail($msg) {
    Write-Host "错误: $msg" -ForegroundColor Red
    exit 1
}

switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { $arch = "amd64" }
    "ARM64" { $arch = "arm64" }
    "x86" { Fail "不支持 32 位 Windows" }
    default { Fail "不支持的 CPU 架构: $($env:PROCESSOR_ARCHITECTURE)" }
}

if ($Version) {
    $tag = $Version.TrimStart("v")
    $base = "https://github.com/$repo/releases/download/v$tag"
    $label = "v$tag"
} else {
    $base = "https://github.com/$repo/releases/latest/download"
    $label = "最新版"
}

$asset = "${bin}_windows_${arch}.exe"
$tmp = Join-Path $env:TEMP ("npm-token-" + [guid]::NewGuid().ToString("N") + ".exe")

Write-Host "下载 $bin ($label, windows $arch)…"
try {
    Invoke-WebRequest -Uri "$base/$asset" -OutFile $tmp -UseBasicParsing
} catch {
    Fail "下载失败: $base/$asset`n$($_.Exception.Message)"
}

# 有 checksums.txt 就校验
$sums = "$tmp.sha256"
try {
    Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $sums -UseBasicParsing
    $line = Select-String -Path $sums -Pattern ("\s+" + [regex]::Escape($asset) + "$") | Select-Object -First 1
    if ($line) {
        $want = ($line.Line -split "\s+")[0]
        $got = (Get-FileHash -Algorithm SHA256 -Path $tmp).Hash
        if ($got -ne $want.ToUpper()) {
            Remove-Item -Force $tmp -ErrorAction SilentlyContinue
            Fail "校验失败：下载到的文件与 checksums.txt 不一致"
        }
    }
} catch { }
Remove-Item -Force $sums -ErrorAction SilentlyContinue

if (-not $BinDir) {
    $BinDir = Join-Path $env:LOCALAPPDATA "npm-token"
}
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null

$target = Join-Path $BinDir "$bin.exe"
try {
    Move-Item -Force -Path $tmp -Destination $target
} catch {
    Remove-Item -Force $tmp -ErrorAction SilentlyContinue
    Fail "写入 $target 失败（程序是否正在运行？）`n$($_.Exception.Message)"
}

Write-Host "已安装: $target"

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (($userPath -split ";") -notcontains $BinDir) {
    $newPath = if ([string]::IsNullOrEmpty($userPath)) { $BinDir } else { $userPath.TrimEnd(";") + ";" + $BinDir }
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    Write-Host "已把 $BinDir 加到用户 PATH（新开终端生效）"
}
$env:Path = $env:Path.TrimEnd(";") + ";" + $BinDir

try { & $target version } catch { }

Write-Host "`n运行 $bin 打开 TUI。" -ForegroundColor Green
