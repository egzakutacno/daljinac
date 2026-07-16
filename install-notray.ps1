$notray = $true
$ErrorActionPreference = "SilentlyContinue"
$ProgressPreference = "SilentlyContinue"

$Dir = "C:\daljinac"
$Exe = "$Dir\daljinac.exe"
$URL = "https://github.com/egzakutacno/daljinac/releases/latest/download/daljinac.exe"
$Args = if ($notray) { "-notray" } else { "" }

Write-Host "[1/3] Downloading..."
mkdir $Dir -Force | Out-Null
Invoke-WebRequest $URL -OutFile "$Exe.new" -UseBasicParsing
Write-Host "       $((Get-Item "$Exe.new").Length) bytes"

Write-Host "[1b/3] Replacing old binary..."
Get-Process daljinac -ErrorAction SilentlyContinue | Stop-Process -Force
Move-Item -Force "$Exe.new" $Exe

Write-Host "[2/3] Installing scheduled task..."
schtasks /delete /tn Daljinac /f 2>$null

$action  = New-ScheduledTaskAction -Execute $Exe -Argument $Args -WorkingDirectory $Dir
$trigger = New-ScheduledTaskTrigger -AtLogon
$settings = New-ScheduledTaskSettingsSet
$settings.DisallowStartIfOnBatteries = $false
$settings.StopIfGoingOnBatteries = $false
$settings.StartWhenAvailable = $true
$settings.AllowStartIfOnBatteries = $true
$settings.RestartCount = 3
$settings.RestartInterval = "PT1M"
$settings.ExecutionTimeLimit = "PT0S"
$principal = New-ScheduledTaskPrincipal -UserId (whoami) -LogonType Interactive -RunLevel Highest
Register-ScheduledTask -TaskName Daljinac -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null

Write-Host "[3/3] Starting..."
$cmd = if ($Args -ne "") { "$Exe $Args" } else { $Exe }
([wmiclass]'Win32_Process').Create($cmd) | Out-Null

Write-Host ""
Write-Host "DONE." -ForegroundColor Green
if ($notray) { Write-Host "  Mode: no-tray (background)" }
