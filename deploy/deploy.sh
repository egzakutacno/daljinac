#!/bin/bash
set -e

cd "$(dirname "$0")/.."

EXE="daljinac.exe"
PS1="deploy/install.ps1"

echo "=== Daljinac Build & Deploy ==="
echo ""

echo "[1/3] Building for Windows..."
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w -H=windowsgui" -o "$EXE" .
echo "       $EXE $(du -h "$EXE" | cut -f1)"

echo "[2/3] Getting tunnel URL..."
TUNNEL_URL=$(ssh pi "journalctl -u remote-exec-tunnel --no-pager -n 100 2>/dev/null" 2>/dev/null | grep -oP 'https://[^\s]+\.trycloudflare\.com' | tail -1)
if [ -z "$TUNNEL_URL" ]; then
    echo "       WARNING: Could not get tunnel URL from pi."
    echo "       Set it manually: export TUNNEL_URL='https://xxx.trycloudflare.com'"
    TUNNEL_URL="${TUNNEL_URL:-UNKNOWN}"
fi
echo "       $TUNNEL_URL"

echo "[3/3] Deploying to RPi server..."
if [ "$TUNNEL_URL" != "UNKNOWN" ]; then
    scp "$EXE" "pi:/opt/remote-exec/static/daljinac.exe" 2>/dev/null || echo "       scp daljinac.exe failed"
    scp "$PS1" "pi:/opt/remote-exec/static/install.ps1" 2>/dev/null || echo "       scp install.ps1 failed"
    echo "       Files copied to RPi."
else
    echo "       Skipping scp (no tunnel URL)."
fi

echo ""
echo "=== DONE ==="
echo ""
echo "One-liner for Windows:"
echo ""
echo "powershell -c \"\$u='$TUNNEL_URL'; irm \$u/install.ps1 | iex\""
echo ""
echo "Build only (no deploy):"
echo "  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='-s -w -H=windowsgui' -o daljinac.exe ."
echo ""
echo "Manual install on Windows (if no tunnel):"
echo "  daljinac.exe -install -server https://TUNNEL_URL"
