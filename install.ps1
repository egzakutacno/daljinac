Write-Host "Daljinac Installer" -ForegroundColor Cyan

$Dir = "C:\daljinac"
$Exe = "$Dir\daljinac.exe"
$URL = "https://github.com/egzakutacno/daljinac/releases/latest/download/daljinac.exe"

Write-Host "[1/3] Downloading daljinac.exe from GitHub..."
mkdir $Dir -Force | Out-Null
taskkill /f /im daljinac.exe 2>$null
taskkill /f /im zrok2.exe 2>$null
Invoke-WebRequest $URL -OutFile $Exe -UseBasicParsing
Write-Host "       $((Get-Item $Exe).Length) bytes"

Write-Host "[2/3] Installing scheduled task..."
schtasks /delete /tn Daljinac /f 2>$null
schtasks /create /tn Daljinac /tr "`"$Exe`"" /sc ONLOGON /rl HIGHEST /f

Write-Host "[3/3] Starting agent..."
schtasks /run /tn Daljinac

Write-Host ""
Write-Host "DONE." -ForegroundColor Green
Write-Host "  Tray: look for Daljinac icon"
Write-Host "  Remove: $Exe -remove"
