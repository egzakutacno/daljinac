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

func initLog() {
	logDir := filepath.Join(os.Getenv("ProgramData"), "daljinac")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		logDir = "C:\\daljinac"
		os.MkdirAll(logDir, 0755)
	}
	logFile := filepath.Join(logDir, "daljinac.log")
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("WARN: cannot open log %s: %v", logFile, err)
		return
	}
	log.SetOutput(io.MultiWriter(f, os.Stdout))
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Printf("=== daljinac v%s starting ===", version)
}

const version = "2.6.10"

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
	marker := "C:\\daljinac\\started.txt"
	os.MkdirAll("C:\\daljinac", 0755)
	os.WriteFile(marker, []byte(time.Now().Format(time.RFC3339)+"\n"), 0644)
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			b := make([]byte, 4096)
			n := runtime.Stack(b, true)
			log.Printf("PANIC: %v\n%s", r, b[:n])
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

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Printf("Port %d in use — another instance running. Exiting.", *port)
		return
	}
	ln.Close()

	shutdown := make(chan struct{})
	hostname, _ := os.Hostname()

	srv := server.New(*tag, version)
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

	var t tunnel.Tunnel

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

	onConnect := func(url string) {
		srv.SetInfo("daljinac", hostname, url)
		tr.SetURL(url)
		tr.SetRunning()
		tr.SetStatusIcon(tray.IconConnected)
	}

	if !*noTray {
		log.Println("[main] waiting for Explorer (3s)...")
		time.Sleep(3 * time.Second)
		log.Println("[main] starting tray goroutine")
		go tr.Run()
	} else {
		log.Println("Headless mode")
	}

	go func() {
		defer func() { recover() }()
		addr := fmt.Sprintf(":%d", *port)
		if err := srv.Start(addr); err != nil {
			log.Printf("HTTP error: %v", err)
		}
	}()

	t = tunnel.NewSSH(*port, hostname, onConnect)
	t.Start()

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
	dir := filepath.Dir(exe)
	taskXml := filepath.Join(dir, "task.xml")
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Author>daljinac</Author></RegistrationInfo>
  <Triggers>
    <LogonTrigger><Enabled>true</Enabled><Delay>PT10S</Delay></LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author"><RunLevel>HighestAvailable</RunLevel></Principal>
  </Principals>
  <Settings>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowStartIfOnBatteries>true</AllowStartIfOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RestartOnFailure><Interval>PT1M</Interval><Count>3</Count></RestartOnFailure>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>`, exe, dir)
	os.WriteFile(taskXml, []byte(xml), 0644)
	exec.Command("schtasks", "/create", "/tn", "Daljinac", "/xml", taskXml, "/f").Run()
	os.Remove(taskXml)
	exec.Command("schtasks", "/run", "/tn", "Daljinac").Run()
	log.Println("Installed (scheduled task)")
}

func doRemove() {
	exec.Command("taskkill", "/f", "/im", filepath.Base(os.Args[0])).Run()
	exec.Command("schtasks", "/delete", "/tn", "Daljinac", "/f").Run()
	log.Println("Removed")
}

const updateURL = "https://github.com/egzakutacno/daljinac/releases/latest/download/daljinac.exe"

func doUpdate() error {
	tmpDir := filepath.Join(os.TempDir(), "daljinac-update")
	os.MkdirAll(tmpDir, 0755)

	newExe := filepath.Join(tmpDir, "daljinac.exe")
	log.Printf("Downloading %s", updateURL)
	resp, err := http.Get(updateURL)
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
	fullCmd := fmt.Sprintf(`"%s" %s`, current, strings.Join(os.Args[1:], " "))
	os.WriteFile(argsFile, []byte(fullCmd), 0644)

	logFile := filepath.Join(tmpDir, "update.log")
	bat := filepath.Join(tmpDir, "up.bat")
	taskXml := filepath.Join(tmpDir, "task.xml")
	os.WriteFile(taskXml, []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Author>daljinac</Author></RegistrationInfo>
  <Triggers>
    <LogonTrigger><Enabled>true</Enabled><Delay>PT10S</Delay></LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author"><RunLevel>HighestAvailable</RunLevel></Principal>
  </Principals>
  <Settings>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowStartIfOnBatteries>true</AllowStartIfOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RestartOnFailure><Interval>PT1M</Interval><Count>3</Count></RestartOnFailure>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>`, current, filepath.Dir(current))), 0644)
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
taskkill /f /im daljinac.exe >> %%LOG%% 2>&1
timeout /t 2 /nobreak > nul
echo %%date%% %%time%% [update] registering scheduled task >> %%LOG%%
schtasks /create /tn Daljinac /xml "%s" /f >> %%LOG%% 2>&1
schtasks /run /tn Daljinac >> %%LOG%% 2>&1
echo %%date%% %%time%% [update] done, cleaning up >> %%LOG%%
del "%s"
del "%%~f0"
`, logFile, argsFile, newExe, current, taskXml, taskXml)
	os.WriteFile(bat, []byte(batch), 0644)

	log.Printf("Update batch: %s", bat)
	log.Printf("Update log: %s", logFile)

	shell32 := syscall.NewLazyDLL("shell32.dll")
	se := shell32.NewProc("ShellExecuteW")
	ret, _, _ := se.Call(0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("runas"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("cmd"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("/C \""+bat+"\""))),
		0, 5)
	log.Printf("ShellExecuteW ret=%d", ret)

	log.Println("Update launched, exiting")
	os.Exit(0)
	return nil
}
