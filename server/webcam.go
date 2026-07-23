package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var webcamPS = func(cameraIdx int, outPath string) error {
	psCmd := fmt.Sprintf(`
try {
    $dm = New-Object -ComObject WIA.DeviceManager
    $di = $null
    $idx = 0
    foreach ($d in $dm.DeviceInfos) {
        if ($d.Type -eq 1) {
            if ($idx -eq %d) { $di = $d; break }
            $idx++
        }
    }
    if (-not $di) { Write-Error "no camera at index %d"; exit 1 }
    $dev = $di.Connect()
    $pic = $dev.ExecuteCommand("{AF933CAC-ACAD-11D2-A093-00C04F72DC3C}")
    $img = $pic.Transfer()
    $img.SaveFile('%s')
    Write-Output "ok"
} catch {
    Write-Error $_.Exception.Message
    exit 1
}
`, cameraIdx, cameraIdx, strings.ReplaceAll(outPath, "'", "''"))

	result := ExecutePS(psCmd)
	if result.ExitCode != 0 {
		return fmt.Errorf("webcam capture failed (exit %d): %s", result.ExitCode, result.Stderr)
	}
	return nil
}

func CaptureWebcam(cameraIdx int) ([]byte, error) {
	tmpDir := os.TempDir()
	outPath := filepath.Join(tmpDir, fmt.Sprintf("agent-webcam-%d.jpg", time.Now().UnixNano()))
	defer os.Remove(outPath)

	if err := webcamPS(cameraIdx, outPath); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("read webcam image: %w", err)
	}

	return data, nil
}
