param(
    [switch]$IncludeWindows
)

$ErrorActionPreference = "Stop"
$RepoRoot   = Split-Path -Parent $PSScriptRoot
$HubDir     = Join-Path $RepoRoot "orbic-hub"
$DistDir    = Join-Path $RepoRoot "dist"
$PayloadDir = Join-Path $HubDir "cmd\installer\payload"

New-Item -ItemType Directory -Force -Path $DistDir | Out-Null
New-Item -ItemType Directory -Force -Path $PayloadDir | Out-Null

Push-Location $HubDir
try {
    Write-Host "[1/4] Running Go unit tests..." -ForegroundColor Cyan
    go test -v ./internal/... ./cmd/rayscout
    if ($LASTEXITCODE -ne 0) { throw "go test failed with exit code $LASTEXITCODE" }

    Write-Host "[2/4] Cross-compiling static ARMv7 binary for Orbic RC400L..." -ForegroundColor Cyan
    $env:CGO_ENABLED = "0"
    $env:GOOS        = "linux"
    $env:GOARCH      = "arm"
    $env:GOARM       = "7"
    $OutOrbic = Join-Path $DistDir "rayscout-linux-armv7"
    go build -trimpath -ldflags="-s -w" -o $OutOrbic ./cmd/rayscout
    if ($LASTEXITCODE -ne 0) { throw "Orbic ARMv7 build failed with exit code $LASTEXITCODE" }
    Write-Host "Built: $OutOrbic" -ForegroundColor Green

    Write-Host "[3/4] Bundling one-click Windows installer (RayScout-Installer.exe)..." -ForegroundColor Cyan
    Copy-Item -Force $OutOrbic (Join-Path $PayloadDir "rayscout-linux-armv7")
    Copy-Item -Force (Join-Path $RepoRoot "rules.json") (Join-Path $PayloadDir "rules.json")
    Copy-Item -Force (Join-Path $RepoRoot "config.example.json") (Join-Path $PayloadDir "config.example.json")
    Copy-Item -Force (Join-Path $RepoRoot "scripts\rayscout_daemon") (Join-Path $PayloadDir "rayscout_daemon")

    $env:GOOS   = "windows"
    $env:GOARCH = "amd64"
    Remove-Item Env:GOARM -ErrorAction SilentlyContinue
    $OutInstaller = Join-Path $DistDir "RayScout-Installer.exe"
    go build -trimpath -ldflags="-s -w" -o $OutInstaller ./cmd/installer
    if ($LASTEXITCODE -ne 0) { throw "Installer build failed with exit code $LASTEXITCODE" }
    Write-Host "Built: $OutInstaller" -ForegroundColor Green

    if ($IncludeWindows) {
        Write-Host "[4/4] Building local Windows demo binary (rayscout.exe)..." -ForegroundColor Cyan
        $OutWin = Join-Path $DistDir "rayscout.exe"
        go build -trimpath -ldflags="-s -w" -o $OutWin ./cmd/rayscout
        if ($LASTEXITCODE -ne 0) { throw "Windows demo build failed with exit code $LASTEXITCODE" }
        Write-Host "Built: $OutWin" -ForegroundColor Green
    }
} finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:GOARM, Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}
