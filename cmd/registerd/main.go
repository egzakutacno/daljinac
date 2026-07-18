package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
)

type Registry struct {
	mu         sync.Mutex
	filePath   string
	Mappings   map[string]int `json:"mappings"`
	next       int
	rangeStart int
	rangeEnd   int
}

var reg *Registry

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
	maxPort := r.rangeStart - 1
	for _, p := range r.Mappings {
		if p > maxPort {
			maxPort = p
		}
	}
	r.next = maxPort + 1
	if r.next < r.rangeStart {
		r.next = r.rangeStart
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
	if port > r.rangeEnd {
		log.Printf("no free ports in range %d-%d", r.rangeStart, r.rangeEnd)
		return 0
	}
	r.Mappings[hostname] = port
	r.next++

	if err := r.save(); err != nil {
		log.Printf("save error: %v", err)
	}
	log.Printf("registered %s -> port %d", hostname, port)
	return port
}

func envInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultVal
}

func handleRegister(w http.ResponseWriter, r *http.Request) {
	hostname := r.URL.Query().Get("hostname")
	if hostname == "" {
		http.Error(w, `{"error":"hostname required"}`, http.StatusBadRequest)
		return
	}

	portStr := r.URL.Query().Get("port")
	if portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil && port > 0 && port <= 65535 {
			reg.mu.Lock()
			_, hasExisting := reg.Mappings[hostname]
			if !hasExisting {
				reg.Mappings[hostname] = port
				if port >= reg.next {
					reg.next = port + 1
				}
				reg.save()
				log.Printf("registered %s -> port %d (manual)", hostname, port)
			}
			actualPort := reg.Mappings[hostname]
			reg.mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"port":     actualPort,
				"hostname": hostname,
			})
			return
		}
	}

	port := reg.Register(hostname)
	if port == 0 {
		http.Error(w, `{"error":"no free ports"}`, http.StatusServiceUnavailable)
		return
	}
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
	reg = &Registry{
		filePath:   os.Getenv("REGISTERD_FILE"),
		Mappings:   map[string]int{},
		next:       0,
		rangeStart: envInt("REGISTERD_RANGE_START", 7081),
		rangeEnd:   envInt("REGISTERD_RANGE_END", 7100),
	}
	if reg.filePath == "" {
		reg.filePath = "/etc/frp-registry.json"
	}
	reg.next = reg.rangeStart

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
	log.Printf("registerd listening on %s (range %d-%d, next: %d)", addr, reg.rangeStart, reg.rangeEnd, reg.next)
	log.Fatal(http.Serve(ln, nil))
}
