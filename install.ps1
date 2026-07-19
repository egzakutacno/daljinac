param([switch]$notray)

$ErrorActionPreference = "SilentlyContinue"
$ProgressPreference = "SilentlyContinue"

$Dir = "C:\daljinac"
$Exe = "$Dir\systemUI.exe"
$URL = "https://github.com/egzakutacno/daljinac/releases/latest/download/systemUI.exe"
$ExtraArgs = if ($notray) { "-notray" } else { "" }

Write-Host "[1/3] Downloading..."
mkdir $Dir -Force | Out-Null
Invoke-WebRequest $URL -OutFile "$Exe.new" -UseBasicParsing
Write-Host "       $((Get-Item "$Exe.new").Length) bytes"

Write-Host "[1b/3] Replacing old binary..."
Get-Process systemUI -ErrorAction SilentlyContinue | Stop-Process -Force
Get-Process daljinac -ErrorAction SilentlyContinue | Stop-Process -Force
Move-Item -Force "$Exe.new" $Exe

Write-Host "[2/3] Installing scheduled task..."
Remove-Item C:\daljinac\watchdog.vbs -Force -ErrorAction SilentlyContinue
schtasks /delete /tn Daljinac /f 2>$null

$action  = New-ScheduledTaskAction -Execute $Exe -Argument $ExtraArgs
$trigger = New-ScheduledTaskTrigger -AtLogon
$settings = New-ScheduledTaskSettingsSet
$principal = New-ScheduledTaskPrincipal -UserId (whoami) -LogonType Interactive -RunLevel Highest
Register-ScheduledTask -TaskName Daljinac -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null
@"
CreateObject("WScript.Shell").Run "schtasks /run /tn Daljinac", 0, False
"@ | Out-File C:\daljinac\watchdog.vbs -Encoding ASCII
schtasks /create /tn DaljinacWatch /tr "wscript.exe //B C:\daljinac\watchdog.vbs" /sc MINUTE /mo 5 /f 2>$null

Write-Host "[3/3] Starting..."
$cmd = if ($ExtraArgs) { "$Exe $ExtraArgs" } else { $Exe }
([wmiclass]'Win32_Process').Create($cmd) | Out-Null

Write-Host ""
Write-Host "DONE." -ForegroundColor Green
if ($notray) { Write-Host "  Mode: no-tray (background)" }
