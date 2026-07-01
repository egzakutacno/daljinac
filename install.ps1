# Daljinac — One-Line Installer (Windows)
# Run as Administrator in PowerShell

$ServerUrl = if ($u) { $u } else { $null }
if (-not $ServerUrl) { Write-Host "Usage: `$u='URL'; irm ... | iex" -ForegroundColor Red; exit 1 }

$Dir = "C:\daljinac"
$Exe = "$Dir\daljinac.exe"

Write-Host "Daljinac Installer" -ForegroundColor Cyan
Write-Host "  Server: $ServerUrl" -ForegroundColor Gray
Write-Host "  Install: $Dir" -ForegroundColor Gray
Write-Host ""

mkdir $Dir -Force | Out-Null
taskkill /f /im daljinac.exe 2>$null | Out-Null

Write-Host "[1/2] Downloading daljinac.exe..."
Invoke-WebRequest "$ServerUrl/daljinac.exe" -OutFile $Exe -UseBasicParsing
Write-Host "       $((Get-Item $Exe).Length) bytes"

Write-Host "[2/2] Installing auto-start..."
& $Exe -install
Start-Sleep 2
Start-Process -FilePath $Exe -WindowStyle Hidden

Write-Host ""
Write-Host "DONE. Agent is running." -ForegroundColor Green
Write-Host "  Tray: check ^ arrow near clock for Daljinac icon"
Write-Host "  Remove: $Exe -remove"
