package server

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func CaptureScreen() ([]byte, error) {
	tmpDir := os.TempDir()
	outPath := filepath.Join(tmpDir, "daljinac-screenshot.png")

	psCmd := fmt.Sprintf(`
Add-Type -AssemblyName System.Drawing,System.Windows.Forms
$bmp = New-Object System.Drawing.Bitmap ([System.Windows.Forms.Screen]::PrimaryScreen.Bounds.Width), ([System.Windows.Forms.Screen]::PrimaryScreen.Bounds.Height)
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen(0, 0, 0, 0, $bmp.Size)
$bmp.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)
$g.Dispose()
$bmp.Dispose()
`, strings.ReplaceAll(outPath, "'", "''"))

	result := ExecutePS(psCmd)
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("screenshot failed: %s", result.Stderr)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("read screenshot: %w", err)
	}

	os.Remove(outPath)
	return data, nil
}

func CaptureScreenBase64() (string, error) {
	data, err := CaptureScreen()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
