$ServerUrl = if ($u) { $u } else { $null }
if (-not $ServerUrl) { Write-Host "ERROR: Set `$u before running" -ForegroundColor Red; exit 1 }

$Dir = "C:\daljinac"
$Exe = "$Dir\daljinac.exe"

Write-Host "Daljinac Installer" -ForegroundColor Cyan
Write-Host "  Server: $ServerUrl"
Write-Host "  Dir:    $Dir`n"

mkdir $Dir -Force | Out-Null
taskkill /f /im daljinac.exe 2>$null
taskkill /f /im cloudflared.exe 2>$null

Write-Host "[1/2] Downloading..."
Invoke-WebRequest "$ServerUrl/daljinac.exe" -OutFile $Exe -UseBasicParsing
Write-Host "       $((Get-Item $Exe).Length) bytes"

Write-Host "[2/2] Installing + starting..."
schtasks /delete /tn Daljinac /f 2>$null
schtasks /create /tn Daljinac /tr "`"$Exe`" --rpi-url $ServerUrl" /sc ONLOGON /f | Out-Null
Start-Process "$Exe" -ArgumentList "--rpi-url","$ServerUrl"

Write-Host ""
Write-Host "DONE." -ForegroundColor Green
Write-Host "  Tray: look for Daljinac icon in system tray"
Write-Host "  Remove: $Exe -remove"
