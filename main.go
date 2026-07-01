package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"daljinac/server"
	"daljinac/tray"
	"daljinac/websocket"
)

func initLog() {
	logDir := os.TempDir()
	logFile := filepath.Join(logDir, "daljinac.log")
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

	apiKey := flag.String("key", DefaultAPIKey, "Local HTTP API key")
	configPath := flag.String("config", "", "Path to agent.json")
	installFlag := flag.Bool("install", false, "Install as scheduled task")
	removeFlag := flag.Bool("remove", false, "Remove scheduled task")
	serverFlag := flag.String("server", "", "Server URL (for -install)")
	noTray := flag.Bool("notray", false, "Run without system tray")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("Daljinac v%s\n", Version)
		return
	}

	if v := os.Getenv("AGENT_KEY"); v != "" {
		*apiKey = v
	}

	if *installFlag {
		exePath, err := os.Executable()
		if err != nil {
			log.Fatalf("Cannot get executable: %v", err)
		}
		serverURL := *serverFlag
		if serverURL == "" {
			log.Fatalf("-server flag is required for -install")
		}
		if err := Install(exePath, serverURL); err != nil {
			log.Fatalf("Install failed: %v", err)
		}
		return
	}

	if *removeFlag {
		Remove()
		return
	}

	if *configPath == "" {
		exePath, _ := os.Executable()
		exeDir := filepath.Dir(exePath)
		defaultConfig := filepath.Join(exeDir, "agent.json")
		if _, err := os.Stat(defaultConfig); err == nil {
			*configPath = defaultConfig
		} else {
			agentDir, _ := agentDir()
			defaultConfig = filepath.Join(agentDir, "agent.json")
			if _, err := os.Stat(defaultConfig); err == nil {
				*configPath = defaultConfig
			}
		}
	}

	if *configPath == "" {
		log.Fatalf("No agent.json found. Use -config flag to specify path.")
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Load config: %v", err)
	}

	hostname, _ := os.Hostname()
	log.Printf("Daljinac v%s starting (machine=%s, hostname=%s)", Version, cfg.MachineID, hostname)

	if cfg.MachineID == "" {
		log.Println("No machine_id, registering with server")
		machineID, err := registerOnServer(cfg.ServerURL, cfg.APIKey, cfg.Name)
		if err != nil {
			log.Printf("Registration failed: %v (will retry via WebSocket reconnect)", err)
		} else {
			cfg.MachineID = machineID
			if err := WriteConfig(*configPath, cfg); err != nil {
				log.Printf("Failed to save updated config: %v", err)
			}
		}
	}

	log.Printf("Starting HTTP API on port %d (key=%s...)", cfg.APIPort, (*apiKey)[:8])
	srv := server.New(*apiKey, Version, cfg.ServerURL, cfg.MachineID)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("PANIC in HTTP server: %v", r)
			}
		}()
		addr := fmt.Sprintf("127.0.0.1:%d", cfg.APIPort)
		if err := srv.Start(addr); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	executor := srv.Executor()
	wsClient := websocket.New(cfg.ServerURL, cfg.MachineID, cfg.APIKey, executor)
	wsClient.SetOnStatusChange(func(status string) {
		switch status {
		case "connecting":
			srv.SetOnStatusChange(func(s server.AgentStatus) {})
		case "connected":
			log.Println("WebSocket connected")
		}
	})
	go wsClient.ConnectAndLoop()

	var tr *tray.Tray
	if !*noTray {
		tr = tray.New(hostname)
		tr.SetStatus(tray.StatusStarting)
		tr.SetURL(cfg.ServerURL)
		tr.OnCopyURL = func() {}
		tr.OnUpdate = func() {
			updateURL := cfg.UpdateURL
			if updateURL == "" {
				updateURL = cfg.ServerURL + "/agent.exe"
			}
			CheckAndUpdate(updateURL)
		}
		tr.OnExit = func() {
			log.Println("Exit requested from tray")
			wsClient.Close()
			srv.Stop()
			tr.Stop()
			os.Exit(0)
		}
		tr.Run()

		go func() {
			<-wsClient.Done()
			tr.SetStatus(tray.StatusError)
		}()
	}

	log.Println("Agent running")
	if *noTray {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("Signal received, shutting down")
		wsClient.Close()
		srv.Stop()
	} else {
		select {
		case <-tr.StopCh():
		case sig := <-func() chan os.Signal {
			s := make(chan os.Signal, 1)
			signal.Notify(s, syscall.SIGINT, syscall.SIGTERM)
			return s
		}():
			log.Printf("Signal %v received", sig)
		}
		wsClient.Close()
		srv.Stop()
		if tr != nil {
			tr.Stop()
		}
	}

	log.Println("Shutdown complete")
}
