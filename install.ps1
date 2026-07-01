Write-Host "Daljinac Installer" -ForegroundColor Cyan

# Discover RPi URL from GitHub Gist
Write-Host "[1/3] Discovering RPi server..."
$gist = "https://gist.githubusercontent.com/egzakutacno/0c3de11a3381ae878b09626b306d04d1/raw/tunnel-url.txt"
try {
    $ServerUrl = (Invoke-WebRequest $gist -UseBasicParsing).Content.Trim()
    if ($ServerUrl -notmatch '^https://') { throw "Invalid URL" }
} catch {
    Write-Host "ERROR: Can't discover RPi URL from Gist. Set manually:" -ForegroundColor Red
    Write-Host "  `$u='https://TUNNEL_URL'; irm ... | iex" -ForegroundColor Yellow
    exit 1
}
Write-Host "       RPi: $ServerUrl"

$Dir = "C:\daljinac"
$Exe = "$Dir\daljinac.exe"

Write-Host "[2/3] Downloading daljinac.exe..."
mkdir $Dir -Force | Out-Null
taskkill /f /im daljinac.exe 2>$null
taskkill /f /im cloudflared.exe 2>$null
Invoke-WebRequest "$ServerUrl/daljinac.exe" -OutFile $Exe -UseBasicParsing
Write-Host "       $((Get-Item $Exe).Length) bytes"

Write-Host "[3/3] Installing + starting..."
schtasks /delete /tn Daljinac /f 2>$null
schtasks /create /tn Daljinac /tr "`"$Exe`"" /sc ONLOGON /f | Out-Null
Start-Process "$Exe"

Write-Host ""
Write-Host "DONE." -ForegroundColor Green
Write-Host "  Tray: look for Daljinac icon"
Write-Host "  Remove: $Exe -remove"