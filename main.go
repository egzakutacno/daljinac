package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"agent/server"
	"agent/tray"
	"agent/tunnel"
)

const maxLogSize = 1 * 1024 * 1024
const version = "2.6.41"
const originalExeName = "daljinac.exe"

var logFile *os.File

func exeDir() string {
	exe, _ := os.Executable()
	return filepath.Dir(exe)
}

func exeBase() string {
	return strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
}

func initLog() {
	logDir := exeDir()
	os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, exeBase()+".log")
	if fi, err := os.Stat(logPath); err == nil && fi.Size() > maxLogSize {
		os.Rename(logPath, logPath+".old")
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("WARN: cannot open log %s: %v", logPath, err)
		return
	}
	logFile = f
	log.SetOutput(io.MultiWriter(f, os.Stdout))
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Printf("=== %s v%s starting ===", exeBase(), version)
}

func syncLog() {
	if logFile != nil {
		logFile.Sync()
	}
}

func hideConsole() {
	if runtime.GOOS != "windows" {
		return
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getConsoleWindow := kernel32.NewProc("GetConsoleWindow")
	user32 := syscall.NewLazyDLL("user32.dll")
	showWindow := user32.NewProc("ShowWindow")
	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd != 0 {
		showWindow.Call(hwnd, 0) // SW_HIDE = 0
	}
}

func writeStartupMarker() {
	dir := exeDir()
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "started.txt"), []byte(time.Now().Format(time.RFC3339)+"\n"), 0644)
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			b := make([]byte, 4096)
			n := runtime.Stack(b, true)
			log.Printf("PANIC: %v\n%s", r, b[:n])
			syncLog()
		}
	}()
	writeStartupMarker()
	initLog()
	hideConsole()
	time.Sleep(2 * time.Second)

	port := flag.Int("port", 8081, "HTTP port")
	tag := flag.String("tag", "", "Machine tag")
	noTray := flag.Bool("notray", false, "No system tray")
	flag.Parse()

	args := flag.Args()
	if len(args) > 0 && args[0] == "-install" {
		doInstall()
		return
	}
	if len(args) > 0 && args[0] == "-remove" {
		doRemove()
		return
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Printf("Port %d in use — another instance running. Exiting.", *port)
		return
	}

	shutdown := make(chan struct{})
	hostname, _ := os.Hostname()
	log.Printf("Hostname: %s, Version: %s, Port: %d, Tray: %v", hostname, version, *port, !*noTray)

	srv := server.New(*tag, version)

	var t tunnel.Tunnel
	onConnect := func(url string) {
		srv.SetInfo(exeBase(), hostname, url)
	}

	// Start HTTP server — ln is already open (port check passed), so Serve cannot fail in startup
	go func() {
		defer func() { recover() }()
		if err := srv.StartWithListener(ln); err != nil {
			log.Printf("HTTP error: %v", err)
		}
	}()

	tr := tray.New(hostname, version)
	tr.OnUpdate = func() {
		log.Println("[main] update requested — removing tray icon")
		tr.RemoveIcon()
		if err := doUpdate(); err != nil {
			log.Printf("Update: %v", err)
		}
	}
	srv.SetOnUpdate(func() {
		log.Println("[main] update via HTTP API")
		tr.RemoveIcon()
		if err := doUpdate(); err != nil {
			log.Printf("Update: %v", err)
		}
	})

	tr.OnRestartTunnel = func() {
		log.Println("[main] restarting tunnel")
		if t != nil {
			t.Stop()
		}
	}
	tr.OnExit = func() {
		if t != nil {
			t.Stop()
		}
		srv.Stop()
		close(shutdown)
	}

	// Re-set info now that tray exists
	onConnect = func(url string) {
		srv.SetInfo(exeBase(), hostname, url)
		tr.SetURL(url)
		tr.SetRunning()
		tr.SetStatusIcon(tray.IconConnected)
		bakPath := filepath.Join(exeDir(), exeBase()+".exe.bak")
		os.Remove(bakPath)
	}
	srv.SetInfo("daljinac", hostname, "")

	if !*noTray {
		log.Println("[main] waiting for Explorer (3s)...")
		time.Sleep(3 * time.Second)
		log.Println("[main] starting tray goroutine")
		go tr.Run()
	} else {
		log.Println("Headless mode")
	}

	t = tunnel.NewSSH(*port, hostname, onConnect)
	t.Start()

	go func() {
		for {
			time.Sleep(5 * time.Minute)
			if t == nil {
				continue
			}
			since := time.Since(t.LastConnected())
			if since > 30*time.Minute {
				log.Printf("[watchdog] WARNING: tunnel not connected for %v (will keep retrying)", since)
				syncLog()
			}
		}
	}()

	if *noTray {
		select {}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-shutdown:
	case <-sig:
	}

	log.Println("Shutdown")
	if t != nil {
		t.Stop()
	}
	srv.Stop()
	tr.Stop()
}

func doInstall() {
	exe, _ := os.Executable()
	base := exeBase()
	taskName := base
	watchName := base + "Watch"
	dir := exeDir()
	os.MkdirAll(dir, 0755)
	vbsPath := filepath.Join(dir, "watchdog.vbs")
	vbs := fmt.Sprintf("CreateObject(\"WScript.Shell\").Run \"schtasks /run /tn %s\", 0, False\n", taskName)
	os.WriteFile(vbsPath, []byte(vbs), 0644)
	exec.Command("schtasks", "/delete", "/tn", taskName, "/f").Run()
	exec.Command("schtasks", "/delete", "/tn", watchName, "/f").Run()
	exec.Command("schtasks", "/create", "/tn", taskName, "/tr", exe, "/sc", "ONLOGON", "/rl", "HIGHEST", "/f").Run()
	exec.Command("schtasks", "/create", "/tn", watchName, "/tr", fmt.Sprintf("wscript.exe //B %s", vbsPath), "/sc", "MINUTE", "/mo", "5", "/f").Run()
	exec.Command("cmd", "/c", "start", "", "/min", exe).Run()
	log.Println("Installed (scheduled task + watchdog)")
}

func doRemove() {
	exeName := filepath.Base(os.Args[0])
	base := exeBase()
	exec.Command("taskkill", "/f", "/im", exeName).Run()
	exec.Command("schtasks", "/delete", "/tn", base, "/f").Run()
	exec.Command("schtasks", "/delete", "/tn", base+"Watch", "/f").Run()
	os.Remove(filepath.Join(exeDir(), "watchdog.vbs"))
	log.Println("Removed")
}

func updateURL() string {
	return "https://github.com/egzakutacno/daljinac/releases/latest/download/daljinac.exe"
}

func doUpdate() error {
	base := exeBase()

	tmpDir := filepath.Join(os.TempDir(), base+"-update")
	os.MkdirAll(tmpDir, 0755)

	dlURL := updateURL()
	newExe := filepath.Join(tmpDir, originalExeName)
	log.Printf("Downloading %s", dlURL)
	resp, err := http.Get(dlURL)
	if err != nil {
		log.Printf("download failed: %v, retrying once...", err)
		time.Sleep(5 * time.Second)
		resp, err = http.Get(dlURL)
	}
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	out, err := os.Create(newExe)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	n, err := io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		return fmt.Errorf("download incomplete (%d bytes): %w", n, err)
	}
	if n < 1024*1024 {
		return fmt.Errorf("downloaded binary too small: %d bytes", n)
	}
	header := make([]byte, 2)
	f, err := os.Open(newExe)
	if err == nil {
		f.Read(header)
		f.Close()
	}
	if header[0] != 'M' || header[1] != 'Z' {
		return fmt.Errorf("downloaded file is not a valid PE executable")
	}

	current, _ := os.Executable()
	args := strings.Join(os.Args[1:], " ")

	vbsContent := fmt.Sprintf(`On Error Resume Next
Set ws = CreateObject("WScript.Shell")
Set fso = CreateObject("Scripting.FileSystemObject")
WScript.Sleep 3000
fso.CopyFile "%s", "%s", True
If Err.Number = 0 Then
  ws.Run """%s"" %s", 0, False
End If
`, newExe, current, current, args)
	vbsPath := filepath.Join(tmpDir, "up.vbs")
	os.WriteFile(vbsPath, []byte(vbsContent), 0644)

	log.Printf("Update VBS: %s", vbsPath)
	log.Printf("Update URL: %s (saving as %s -> %s)", dlURL, newExe, current)

	shell32 := syscall.NewLazyDLL("shell32.dll")
	se := shell32.NewProc("ShellExecuteW")
	se.Call(0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("open"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("wscript.exe"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("/B \""+vbsPath+"\""))),
		0, 0)

	log.Println("Update launched, exiting")
	os.Exit(0)
	return nil
}
