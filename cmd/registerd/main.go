package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
)

type Registry struct {
	mu       sync.Mutex
	filePath string
	Mappings map[string]int `json:"mappings"`
	next     int
}

var reg = &Registry{
	filePath: "/etc/frp-registry.json",
	Mappings: map[string]int{},
	next:     7081,
}

func (r *Registry) load() error {
	data, err := os.ReadFile(r.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var raw struct {
		Mappings map[string]int `json:"mappings"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Mappings = raw.Mappings
	if r.Mappings == nil {
		r.Mappings = map[string]int{}
	}
	maxPort := 7080
	for _, p := range r.Mappings {
		if p > maxPort {
			maxPort = p
		}
	}
	r.next = maxPort + 1
	if r.next < 7081 {
		r.next = 7081
	}
	return nil
}

func (r *Registry) save() error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, r.filePath)
}

func (r *Registry) Register(hostname string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	if port, ok := r.Mappings[hostname]; ok {
		return port
	}

	port := r.next
	r.Mappings[hostname] = port
	r.next++

	if err := r.save(); err != nil {
		log.Printf("save error: %v", err)
	}
	log.Printf("registered %s -> port %d", hostname, port)
	return port
}

func handleRegister(w http.ResponseWriter, r *http.Request) {
	hostname := r.URL.Query().Get("hostname")
	if hostname == "" {
		http.Error(w, `{"error":"hostname required"}`, http.StatusBadRequest)
		return
	}

	port := reg.Register(hostname)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"port":     port,
		"hostname": hostname,
	})
}

func handleList(w http.ResponseWriter, r *http.Request) {
	reg.mu.Lock()
	defer reg.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reg.Mappings)
}

func main() {
	if err := reg.load(); err != nil {
		log.Printf("load registry: %v", err)
	}

	http.HandleFunc("/register", handleRegister)
	http.HandleFunc("/list", handleList)

	port := os.Getenv("REGISTERD_PORT")
	if port == "" {
		port = "7080"
	}
	addr := ":" + port
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen %s: %v", addr, err)
	}
	log.Printf("registerd listening on %s (next port: %d)", addr, reg.next)
	log.Fatal(http.Serve(ln, nil))
}
