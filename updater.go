package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func CheckAndUpdate(updateURL string) {
	if updateURL == "" {
		log.Println("[update] No update URL configured")
		return
	}

	log.Printf("[update] Checking for updates from %s", updateURL)

	tmpDir := os.TempDir()
	newExe := filepath.Join(tmpDir, "daljinac-update.exe")
	batPath := filepath.Join(tmpDir, "daljinac-update.bat")

	currentExe, err := os.Executable()
	if err != nil {
		log.Printf("[update] Cannot get current executable: %v", err)
		return
	}

	log.Printf("[update] Downloading new version...")
	if err := downloadFile(updateURL, newExe); err != nil {
		log.Printf("[update] Download failed: %v", err)
		return
	}

	escaped := func(s string) string { return `"` + s + `"` }

	batch := fmt.Sprintf(`@echo off
echo Updating Daljinac...
timeout /t 3 /nobreak > nul
taskkill /f /im daljinac.exe > nul 2>&1
timeout /t 2 /nobreak > nul
copy /y %s %s > nul
del %s > nul
start "" %s
del "%%~f0" > nul
`, escaped(newExe), escaped(currentExe), escaped(newExe), escaped(currentExe))

	if err := os.WriteFile(batPath, []byte(batch), 0644); err != nil {
		log.Printf("[update] Cannot create batch file: %v", err)
		return
	}

	log.Printf("[update] Launching update batch with UAC elevation")
	shell32 := syscall.NewLazyDLL("shell32.dll")
	shellExecuteW := shell32.NewProc("ShellExecuteW")
	shellExecuteW.Call(0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("runas"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("cmd"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("/C"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(batPath))),
		0,
		5)

	log.Println("[update] Update initiated, exiting")
	os.Exit(0)
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("copy: %w", err)
	}

	if strings.HasSuffix(strings.ToLower(dest), ".exe") {
		unblockFile(dest)
	}

	return nil
}

func unblockFile(path string) {
	// Remove the "Mark of the Web" alternate data stream
	zoneStream := path + ":Zone.Identifier"
	_ = os.Remove(zoneStream)
}
