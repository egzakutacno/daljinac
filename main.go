package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"agent/server"
	"agent/tunnel"
)

func initLog() {
	logFile := filepath.Join(os.TempDir(), "daljinac.log")
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(f, os.Stdout))
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, true)
			log.Printf("!!! FATAL PANIC: %v\nStack:\n%s", r, buf[:n])
		}
	}()
	initLog()

	apiKey := flag.String("key", "sk-remctrl-8f3a1b9c", "API key for HTTP API")
	port := flag.Int("port", 8080, "HTTP API port")
	tag := flag.String("tag", "", "Machine tag")
	noTray := flag.Bool("notray", false, "Run without system tray")
	flag.Parse()

	if v := os.Getenv("AGENT_KEY"); v != "" {
		*apiKey = v
	}

	args := flag.Args()
	if len(args) > 0 {
		switch args[0] {
		case "-install":
			install()
			return
		case "-remove":
			remove()
			return
		}
	}

	log.Printf("Daljinac starting (tag=%q, port=%d)", *tag, *port)

	srv := server.New(*apiKey, *tag)
	tr := server.NewTray(srv, *tag)

	var t *tunnel.Tunnel
	onConnected := func(url string) {
		hostname, _ := os.Hostname()
		srv.SetInfo("daljinac", hostname, url)
		tr.SetURL(url)
		tr.SetStatus(server.StatusRunning)
	}

	startAgent := func() {
		log.Println("Starting agent...")
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("PANIC in HTTP server: %v", r)
				}
			}()
			addr := fmt.Sprintf(":%d", *port)
			if err := srv.Start(addr); err != nil {
				log.Printf("Server error: %v", err)
			}
		}()

		t = tunnel.New(*port, onConnected)
		t.Start()
	}

	stopAgent := func() {
		log.Println("Stopping agent...")
		if t != nil {
			t.Stop()
		}
		srv.Stop()
		tr.SetStatus(server.StatusStopped)
	}

	restartTunnel := func() {
		log.Println("Restarting tunnel...")
		if t != nil {
			t.Stop()
		}
		t = tunnel.New(*port, onConnected)
		t.Start()
	}

	tr.SetStartFunc(startAgent)
	tr.SetStopFunc(stopAgent)
	tr.SetRestartTunnelFunc(restartTunnel)

	if !*noTray {
		tr.Run()
	} else {
		log.Println("Running headless (no tray)")
	}

	startAgent()

	if *noTray {
		select {}
	}

	select {
	case <-tr.StopCh():
	case <-func() chan os.Signal {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		return sig
	}():
	}

	log.Println("Shutting down...")
	stopAgent()
	if !*noTray {
		tr.Stop()
	}
}

func install() {
	exe, _ := os.Executable()
	name := "Daljinac"

	psCmd := fmt.Sprintf(
		`powershell -WindowStyle Hidden -Command Start-Process -FilePath '%s' -ArgumentList '-start' -WindowStyle Hidden`,
		exe,
	)

	exec.Command("schtasks", "/create",
		"/tn", name, "/tr", psCmd,
		"/sc", "ONLOGON", "/ru", os.Getenv("USERNAME"), "/f",
	).Run()

	exec.Command("schtasks", "/run", "/tn", name).Run()
}

func remove() {
	exec.Command("taskkill", "/f", "/im", filepath.Base(os.Args[0])).Run()
	exec.Command("schtasks", "/delete", "/tn", "Daljinac", "/f").Run()
}
