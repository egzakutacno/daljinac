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

$xml = @"
<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Author>daljinac</Author>
    <Description>Daljinac remote agent</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <Delay>PT10S</Delay>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowStartIfOnBatteries>true</AllowStartIfOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>$Exe</Command>
      <Arguments>$Args</Arguments>
      <WorkingDirectory>$Dir</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
"@

$taskXml = "$Dir\task.xml"
[System.IO.File]::WriteAllText($taskXml, $xml, [System.Text.Encoding]::Unicode)
schtasks /create /tn Daljinac /xml "$taskXml" /f
Remove-Item $taskXml -Force

Write-Host "[3/3] Starting..."
$cmd = if ($Args -ne "") { "$Exe $Args" } else { $Exe }
([wmiclass]'Win32_Process').Create($cmd) | Out-Null

Write-Host ""
Write-Host "DONE." -ForegroundColor Green
if ($notray) { Write-Host "  Mode: no-tray (background)" }
