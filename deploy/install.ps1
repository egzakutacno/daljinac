# Daljinac — One-Line Installer (Windows)
# Koristi se preko Cloudflare Tunnel URL-a sa RPi servera.
#
# One-liner (kopiraj u PowerShell):
# powershell -c "$u='https://TUNNEL_URL'; irm $u/install.ps1 | iex"

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$ServerUrl = if ($u) { $u } elseif ($env:DALJINAC_SERVER) { $env:DALJINAC_SERVER } else { $null }
if (-not $ServerUrl) {
    Write-Host "ERROR: Set `$u or `$env:DALJINAC_SERVER" -ForegroundColor Red
    Write-Host 'Usage: $u="https://TUNNEL_URL"; irm https://raw.githubusercontent.com/egzakutacno/remote-exec/main/daljinac/install.ps1 | iex' -ForegroundColor Yellow
    exit 1
}

$InstallDir = if ($env:DALJINAC_DIR) { $env:DALJINAC_DIR } else { "$env:USERPROFILE\daljinac-agent" }
$MachineName = if ($env:DALJINAC_NAME) { $env:DALJINAC_NAME } else { $env:COMPUTERNAME }

Write-Host "`n  Daljinac — Remote Agent Installer`n" -ForegroundColor Cyan
Write-Host "  Server:    $ServerUrl" -ForegroundColor Gray
Write-Host "  Machine:   $MachineName" -ForegroundColor Gray
Write-Host "  Install:   $InstallDir`n" -ForegroundColor Gray

try {
    Write-Host "[1/3] Downloading daljinac.exe..." -ForegroundColor Yellow
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $exePath = "$InstallDir\daljinac.exe"
    Invoke-WebRequest -Uri "$ServerUrl/daljinac.exe" -OutFile $exePath -UseBasicParsing
    if (-not (Test-Path $exePath)) { throw "Download failed" }
    Write-Host "       daljinac.exe $((Get-Item $exePath).Length) bytes" -ForegroundColor Gray

    Write-Host "[2/3] Installing..." -ForegroundColor Yellow
    $result = & $exePath -install -server $ServerUrl 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Host "ERROR: Install failed" -ForegroundColor Red
        Write-Host $result -ForegroundColor Gray
        exit 1
    }
    Write-Host $result -ForegroundColor Gray

    Write-Host "[3/3] Starting agent..." -ForegroundColor Yellow
    Start-ScheduledTask -TaskName "Daljinac-*" -ErrorAction SilentlyContinue

    Write-Host "`n  DONE." -ForegroundColor Green
    Write-Host "  Logs: Get-Content `$env:TEMP\daljinac.log" -ForegroundColor Gray
    Write-Host "  Config: $InstallDir\agent.json" -ForegroundColor Gray
    Write-Host "  Remove: daljinac.exe -remove" -ForegroundColor Gray
    Write-Host "`n  Agent is now running in the background." -ForegroundColor Cyan
    Write-Host "  Check system tray for the Daljinac icon.`n" -ForegroundColor Cyan
} catch {
    Write-Host "`nFAILED: $_" -ForegroundColor Red
    Write-Host $_.ScriptStackTrace -ForegroundColor Gray
    exit 1
}
