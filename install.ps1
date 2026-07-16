Write-Host "Daljinac Installer" -ForegroundColor Cyan

$Dir = "C:\daljinac"
$Exe = "$Dir\daljinac.exe"
$URL = "https://github.com/egzakutacno/daljinac/releases/latest/download/daljinac.exe"

Write-Host "[1/3] Downloading daljinac.exe from GitHub..."
mkdir $Dir -Force | Out-Null
Invoke-WebRequest $URL -OutFile "$Exe.new" -UseBasicParsing
Write-Host "       $((Get-Item "$Exe.new").Length) bytes"

Write-Host "[1b/3] Replacing old binary..."
taskkill /f /im daljinac.exe 2>$null
taskkill /f /im frpc.exe 2>$null
Move-Item -Force "$Exe.new" $Exe

Write-Host "[2/3] Installing scheduled task..."
schtasks /delete /tn Daljinac /f 2>$null

$action  = New-ScheduledTaskAction -Execute $Exe
$trigger = New-ScheduledTaskTrigger -AtLogon
$settings = New-ScheduledTaskSettingsSet
$principal = New-ScheduledTaskPrincipal -UserId (whoami) -LogonType Interactive -RunLevel Highest
Register-ScheduledTask -TaskName Daljinac -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null

Write-Host "[3/3] Starting agent..."
schtasks /run /tn Daljinac

Write-Host ""
Write-Host "DONE." -ForegroundColor Green
Write-Host "  Tray: look for Daljinac icon"
Write-Host "  Remove: $Exe -remove"
