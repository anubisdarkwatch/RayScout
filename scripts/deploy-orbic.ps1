param(
    [switch]$InstallInitScript
)

$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent $PSScriptRoot
$BinPath  = Join-Path $RepoRoot "dist\rayscout-linux-armv7"

if (-not (Test-Path $BinPath)) {
    throw "Binary not found at $BinPath. Run .\scripts\build-orbic.ps1 first."
}

Write-Host "Checking ADB device..." -ForegroundColor Cyan
adb devices

Write-Host "Creating /data/rayscout on Orbic RC400L..." -ForegroundColor Cyan
adb shell "mkdir -p /data/rayscout/data"

Write-Host "Stopping existing rayscout process if running..." -ForegroundColor Cyan
adb shell "/etc/init.d/rayscout_daemon stop 2>/dev/null || killall rayscout 2>/dev/null || true"

Write-Host "Pushing binary, rules, and default config..." -ForegroundColor Cyan
adb push $BinPath /data/rayscout/rayscout
adb push (Join-Path $RepoRoot "rules.json") /data/rayscout/rules.json
adb shell "test -f /data/rayscout/config.json" 2>$null
if ($LASTEXITCODE -ne 0) {
    adb push (Join-Path $RepoRoot "config.example.json") /data/rayscout/config.json
}
adb shell "chmod +x /data/rayscout/rayscout"

if ($InstallInitScript) {
    Write-Host "Installing SysV boot script (/etc/init.d/rayscout_daemon)..." -ForegroundColor Cyan
    adb shell "mount -o remount,rw /"
    adb push (Join-Path $RepoRoot "scripts\rayscout_daemon") /etc/init.d/rayscout_daemon
    adb shell "chmod 755 /etc/init.d/rayscout_daemon && ln -sf /etc/init.d/rayscout_daemon /etc/rc5.d/S99rayscout"
    adb shell "mount -o remount,ro /"
    adb shell "/etc/init.d/rayscout_daemon start"
} else {
    Write-Host "Starting rayscout in background (non-persistent test run)..." -ForegroundColor Cyan
    adb shell "nohup /data/rayscout/rayscout -config /data/rayscout/config.json >/data/rayscout/rayscout.log 2>&1 &"
}

Write-Host "`nDone! Connect to your Orbic Wi-Fi and open: http://192.168.1.1:8090" -ForegroundColor Green
Write-Host "Rayhunter remains untouched at: http://192.168.1.1:8080" -ForegroundColor Green
