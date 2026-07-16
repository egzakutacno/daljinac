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
	os.MkdirAll(logDir, 0755)
	logFile := filepath.Join(logDir, "daljinac.log")
	f, _ := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if f != nil {
		log.SetOutput(io.MultiWriter(f, os.Stdout))
		log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
		log.Printf("=== daljinac v%s starting ===", version)
	}
}

const version = "2.4.0-notray"

func main() {
	defer func() {
		if r := recover(); r != nil {
			b := make([]byte, 4096)
			n := runtime.Stack(b, true)
			log.Printf("PANIC: %v\n%s", r, b[:n])
		}
	}()
	initLog()
	exec.Command("taskkill", "/f", "/im", "zrok2.exe").Run()
	exec.Command("taskkill", "/f", "/im", "cloudflared.exe").Run()
	time.Sleep(200 * time.Millisecond)

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

	tr.OnRestartTunnel = func() {
		log.Println("[main] restarting zrok tunnel")
		exec.Command("taskkill", "/f", "/im", "zrok2.exe").Run()
		exec.Command("taskkill", "/f", "/im", "cloudflared.exe").Run()
	}

	var t tunnel.Tunnel
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

	t = tunnel.NewRathole(*port, hostname, onConnect)
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
	ps := fmt.Sprintf(`
$action = New-ScheduledTaskAction -Execute '%s' -Argument '-notray'
$trigger = New-ScheduledTaskTrigger -AtLogon
$settings = New-ScheduledTaskSettingsSet
$principal = New-ScheduledTaskPrincipal -UserId (whoami) -LogonType Interactive -RunLevel Highest
Register-ScheduledTask -TaskName Daljinac -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null
$t = Get-ScheduledTask Daljinac
$t.Settings.DisallowStartIfOnBatteries = $$false
$t.Settings.StopIfGoingOnBatteries = $$false
Set-ScheduledTask $t | Out-Null
`, exe)
	exec.Command("powershell", "-NoProfile", "-Command", ps).Run()
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
schtasks /create /tn Daljinac /tr "%%CMD%%" /sc ONLOGON /rl HIGHEST /f >> %%LOG%% 2>&1
echo %%date%% %%time%% [update] fixing battery settings >> %%LOG%%
powershell -NoProfile -Command "$t=Get-ScheduledTask Daljinac; $t.Settings.DisallowStartIfOnBatteries=$false; $t.Settings.StopIfGoingOnBatteries=$false; Set-ScheduledTask $t" >> %%LOG%% 2>&1
schtasks /run /tn Daljinac >> %%LOG%% 2>&1
echo %%date%% %%time%% [update] done, cleaning up >> %%LOG%%
del "%s"
del "%%~f0"
`, logFile, argsFile, newExe, current, argsFile)
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
