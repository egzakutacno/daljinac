package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
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
	logFile := filepath.Join(os.TempDir(), "daljinac.log")
	f, _ := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if f != nil {
		log.SetOutput(io.MultiWriter(f, os.Stdout))
		log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	}
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			b := make([]byte, 4096)
			n := runtime.Stack(b, true)
			log.Printf("PANIC: %v\n%s", r, b[:n])
		}
	}()
	initLog()
	exec.Command("taskkill", "/f", "/im", "daljinac.exe").Run()
	exec.Command("taskkill", "/f", "/im", "cloudflared.exe").Run()
	time.Sleep(500 * time.Millisecond)

	apiKey := flag.String("key", "sk-remctrl-8f3a1b9c", "API key")
	port := flag.Int("port", 8081, "HTTP port")
	tag := flag.String("tag", "", "Machine tag")
	rpiURL := flag.String("rpi-url", "", "Publish tunnel URL to RPi")
	noTray := flag.Bool("notray", false, "No system tray")
	flag.Parse()

	if v := os.Getenv("AGENT_KEY"); v != "" {
		*apiKey = v
	}
	if *rpiURL == "" {
		if v := os.Getenv("RPI_URL"); v != "" {
			*rpiURL = v
		}
	}

	args := flag.Args()
	if len(args) > 0 && args[0] == "-install" {
		doInstall()
		return
	}
	if len(args) > 0 && args[0] == "-remove" {
		doRemove()
		return
	}

	shutdown := make(chan struct{})
	hostname, _ := os.Hostname()

	srv := server.New(*apiKey, *tag)
	tr := tray.New(hostname)
	tr.OnUpdate = func() {
		if err := doUpdate(); err != nil {
			log.Printf("Update: %v", err)
		}
	}

	var t *tunnel.Tunnel
	tr.OnExit = func() {
		if t != nil {
			t.Stop()
		}
		srv.Stop()
		close(shutdown)
	}

	seenID := ""

	publish := func(turl string) {
		if turl == "" {
			return
		}
		rpi := *rpiURL
		if rpi == "" {
			rpi = discoverRPiURL()
		}
		if rpi == "" {
			return
		}

		if seenID == "" {
			ak := rpiGenKey()
			mid := rpiMid(hostname)
			if id := rpiRegister(rpi, mid, ak, hostname); id != "" {
				seenID = id
			}
		}
		if seenID != "" {
			rpiPublish(rpi, seenID, turl)
		}
	}

	onConnect := func(url string) {
		srv.SetInfo("daljinac", hostname, url)
		tr.SetURL(url)
		tr.SetRunning()
		publish(url)
	}

	if !*noTray {
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

	t = tunnel.New(*port, onConnect)
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
	name := "Daljinac"
	tr := fmt.Sprintf(`"%s"`, exe)

	exec.Command("schtasks", "/create",
		"/tn", name, "/tr", tr,
		"/sc", "ONLOGON", "/f",
	).Run()
	exec.Command("schtasks", "/run", "/tn", name).Run()
	log.Println("Installed (scheduled task)")
}

func doRemove() {
	exec.Command("taskkill", "/f", "/im", filepath.Base(os.Args[0])).Run()
	exec.Command("schtasks", "/delete", "/tn", "Daljinac", "/f").Run()
	log.Println("Removed")
}

func rpiMid(hostname string) string {
	h := strings.ToLower(hostname)
	if len(h) > 15 {
		h = h[:15]
	}
	h = strings.NewReplacer(".", "-", " ", "-", "_", "-").Replace(h)
	b := make([]byte, 2)
	rand.Read(b)
	return fmt.Sprintf("%s-%x", h, b)
}

func rpiGenKey() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func rpiRegister(rpi, mid, ak, hostname string) string {
	b, _ := json.Marshal(map[string]string{
		"name": hostname, "api_key": ak,
		"hostname": hostname, "metadata": `{"type":"daljinac"}`,
	})
	resp, err := http.Post(strings.TrimRight(rpi, "/")+"/api/v1/agent/register",
		"application/json", bytes.NewReader(b))
	if err != nil {
		log.Printf("register: %v", err)
		return ""
	}
	defer resp.Body.Close()
	var r struct{ MachineID string `json:"machine_id"` }
	json.NewDecoder(resp.Body).Decode(&r)
	if r.MachineID != "" {
		return r.MachineID
	}
	return ""
}

func rpiPublish(rpi, mid, url string) {
	b, _ := json.Marshal(map[string]string{"machine_id": mid, "tunnel_url": url})
	resp, err := http.Post(strings.TrimRight(rpi, "/")+"/api/v1/agent/url",
		"application/json", bytes.NewReader(b))
	if err != nil {
		log.Printf("publish: %v", err)
		return
	}
	resp.Body.Close()
}

func discoverRPiURL() string {
	resp, err := http.Get(gistRaw)
	if err != nil {
		log.Printf("gist: %v", err)
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	url := strings.TrimSpace(string(b))
	if url == "" || !strings.HasPrefix(url, "https://") {
		return ""
	}
	return url
}

const updateURL = "https://github.com/egzakutacno/daljinac/releases/latest/download/daljinac.exe"

const gistAPI = "https://api.github.com/gists/0c3de11a3381ae878b09626b306d04d1"
const gistRaw = "https://gist.githubusercontent.com/egzakutacno/0c3de11a3381ae878b09626b306d04d1/raw/tunnel-url.txt"

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

	bat := filepath.Join(tmpDir, "up.bat")
	batch := fmt.Sprintf(`@echo off
set /p CMD=<"%s"
timeout /t 3 /nobreak > nul
taskkill /f /im daljinac.exe > nul 2>&1
copy /y "%s" "%s" > nul 2>&1
schtasks /create /tn Daljinac /tr "%%CMD%%" /sc ONLOGON /f > nul 2>&1
schtasks /run /tn Daljinac > nul 2>&1
del "%s"
del "%%~f0"
`, argsFile, newExe, current, argsFile)
	os.WriteFile(bat, []byte(batch), 0644)

	log.Println("Launching UAC update...")
	shell32 := syscall.NewLazyDLL("shell32.dll")
	se := shell32.NewProc("ShellExecuteW")
	se.Call(0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("runas"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("cmd"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("/C \""+bat+"\""))),
		0, 5)

	log.Println("Update launched, exiting")
	os.Exit(0)
	return nil
}
