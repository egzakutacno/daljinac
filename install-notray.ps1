param([switch]$notray)

$ErrorActionPreference = "SilentlyContinue"
$ProgressPreference = "SilentlyContinue"

$Dir = "C:\daljinac"
$Exe = "$Dir\daljinac.exe"
$URL = "https://github.com/egzakutacno/daljinac/releases/latest/download/daljinac.exe"
$ExtraArgs = "-notray"

Write-Host "[1/3] Downloading..."
mkdir $Dir -Force | Out-Null
Invoke-WebRequest $URL -OutFile "$Exe.new" -UseBasicParsing
Write-Host "       $((Get-Item "$Exe.new").Length) bytes"

Write-Host "[1b/3] Replacing old binary..."
Get-Process daljinac -ErrorAction SilentlyContinue | Stop-Process -Force
Move-Item -Force "$Exe.new" $Exe

Write-Host "[2/3] Installing scheduled task..."
schtasks /delete /tn Daljinac /f 2>$null
schtasks /create /tn Daljinac /tr "`"$Exe`" $ExtraArgs" /sc ONLOGON /rl HIGHEST /f 2>$null

Write-Host "[2b/3] Adding startup folder shortcut..."
$WshShell = New-Object -ComObject WScript.Shell
$Shortcut = $WshShell.CreateShortcut("$env:APPDATA\Microsoft\Windows\Start Menu\Programs\Startup\Daljinac.lnk")
$Shortcut.TargetPath = $Exe
$Shortcut.Arguments = $ExtraArgs
$Shortcut.WorkingDirectory = $Dir
$Shortcut.Save()

Write-Host "[3/3] Starting..."
([wmiclass]'Win32_Process').Create("$Exe $ExtraArgs") | Out-Null

Write-Host ""
Write-Host "DONE." -ForegroundColor Green
Write-Host "  Mode: no-tray (background)"
Write-Host "  Startup: scheduled task + startup folder"
