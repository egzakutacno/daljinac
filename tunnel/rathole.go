package tunnel

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

const ratholeDownloadURL = "https://github.com/rathole-org/rathole/releases/download/v0.5.0/rathole-x86_64-pc-windows-msvc.zip"
const ratholeServerAddr = "45.32.121.103:2333"
const ratholeToken = "83kFmP9qR2vL7xN4"

type RatholeTunnel struct {
	localPort   int
	serviceName string
	url         string
	stopCh      chan struct{}
	onConnected func(url string)
	binPath     string
	running     bool
	mu          sync.Mutex
	serverIP    string
	serverPort  int
}

func NewRathole(localPort int, shareName string, onConnected func(url string)) *RatholeTunnel {
	sanitized := strings.ToLower(shareName)
	sanitized = strings.NewReplacer("_", "-", ".", "-", " ", "-").Replace(sanitized)
	var clean strings.Builder
	for _, r := range sanitized {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			clean.WriteRune(r)
		}
	}
	sanitized = strings.Trim(clean.String(), "-")
	if len(sanitized) < 2 {
		sanitized = "machine"
	}
	log.Printf("[rathole] service name: '%s' (from hostname: '%s')", sanitized, shareName)
	return &RatholeTunnel{
		localPort:   localPort,
		serviceName: sanitized,
		stopCh:      make(chan struct{}),
		onConnected: onConnected,
		serverIP:    "45.32.121.103",
		serverPort:  2333,
	}
}

func (t *RatholeTunnel) download() error {
	tmpDir := filepath.Join(os.TempDir(), "daljinac-rathole")
	os.MkdirAll(tmpDir, 0755)
	t.binPath = filepath.Join(tmpDir, "rathole.exe")

	if _, err := os.Stat(t.binPath); err == nil {
		log.Printf("[rathole] binary already exists at %s", t.binPath)
		return nil
	}

	log.Printf("[rathole] downloading from %s", ratholeDownloadURL)
	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Get(ratholeDownloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}

	zipPath := filepath.Join(tmpDir, "rathole.zip")
	out, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("create zip: %w", err)
	}
	written, _ := io.Copy(out, resp.Body)
	out.Close()
	defer os.Remove(zipPath)
	log.Printf("[rathole] downloaded %d bytes", written)

	log.Printf("[rathole] extracting rathole.exe...")
	cmd := exec.Command("tar", "-xf", zipPath, "-C", tmpDir, "rathole.exe")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("extract: %w - %s", err, string(output))
	}

	os.Remove(t.binPath + ":Zone.Identifier")

	if _, err := os.Stat(t.binPath); err != nil {
		return fmt.Errorf("binary not found after extract: %w", err)
	}
	log.Printf("[rathole] downloaded and extracted OK to %s", t.binPath)
	return nil
}

func (t *RatholeTunnel) writeClientConfig(configPath string) error {
	cfg := fmt.Sprintf(`[client]
remote_addr = "%s"
default_token = "%s"

[client.services.%s]
type = "tcp"
local_addr = "127.0.0.1:%d"
`, ratholeServerAddr, ratholeToken, t.serviceName, t.localPort)
	return os.WriteFile(configPath, []byte(cfg), 0644)
}

func (t *RatholeTunnel) Start() {
	log.Printf("[rathole] Start() called")
	t.mu.Lock()
	t.running = true
	t.mu.Unlock()
	go t.Run()
}

func (t *RatholeTunnel) Run() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, true)
			log.Printf("[rathole] PANIC: %v\n%s", r, buf[:n])
		}
	}()

	log.Printf("[rathole] Run() starting")

	if err := t.download(); err != nil {
		log.Printf("[rathole] download error: %v", err)
		return
	}

	t.running = true
	delay := 2 * time.Second

	for {
		select {
		case <-t.stopCh:
			t.mu.Lock()
			t.running = false
			t.mu.Unlock()
			log.Printf("[rathole] stopped by signal")
			return
		default:
		}

		log.Printf("[rathole] connecting (delay=%v)...", delay)
		t.connect()

		t.mu.Lock()
		isRunning := t.running
		t.mu.Unlock()
		if !isRunning {
			return
		}

		select {
		case <-t.stopCh:
			t.mu.Lock()
			t.running = false
			t.mu.Unlock()
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, 60*time.Second)
	}
}

func (t *RatholeTunnel) connect() {
	tmpDir := filepath.Join(os.TempDir(), "daljinac-rathole")
	os.MkdirAll(tmpDir, 0755)
	configPath := filepath.Join(tmpDir, t.serviceName+".toml")
	if err := t.writeClientConfig(configPath); err != nil {
		log.Printf("[rathole] write config error: %v", err)
		return
	}

	log.Printf("[rathole] starting: rathole %s (service=%s -> 127.0.0.1:%d)", configPath, t.serviceName, t.localPort)
	cmd := exec.Command(t.binPath, configPath)
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[rathole] StdoutPipe error: %v", err)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Printf("[rathole] StderrPipe error: %v", err)
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("[rathole] start error: %v", err)
		return
	}
	log.Printf("[rathole] process started (pid=%d)", cmd.Process.Pid)

	t.mu.Lock()
	t.url = fmt.Sprintf("http://%s:%d", t.serverIP, t.getServicePort(t.serviceName))
	t.mu.Unlock()

	var wg sync.WaitGroup
	var stdoutBuf, stderrBuf bytes.Buffer
	wg.Add(2)
	go func() {
		io.Copy(&stdoutBuf, stdout)
		wg.Done()
	}()
	go func() {
		io.Copy(&stderrBuf, stderr)
		wg.Done()
	}()

	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()

	log.Printf("[rathole] connected, URL: %s", t.url)
	if t.onConnected != nil {
		t.onConnected(t.url)
	}

	select {
	case <-done:
		log.Printf("[rathole] process exited")
		wg.Wait()
		if out := strings.TrimSpace(stdoutBuf.String()); out != "" {
			log.Printf("[rathole stdout] %s", out)
		}
		if out := strings.TrimSpace(stderrBuf.String()); out != "" {
			log.Printf("[rathole stderr] %s", out)
		}
	case <-t.stopCh:
		cmd.Process.Kill()
		log.Printf("[rathole] process killed")
	}
}

func (t *RatholeTunnel) getServicePort(name string) int {
	portMap := map[string]int{
		"desktop-inj3o0l":   8081,
		"usermic-m3sii9l":   8082,
		"desktop-s43ukd6":   8083,
		"desktop-ba967g1":   8084,
		"sandokan":          8085,
	}
	if p, ok := portMap[name]; ok {
		return p
	}
	return 8081
}

func (t *RatholeTunnel) Stop() {
	t.mu.Lock()
	t.running = false
	t.mu.Unlock()
	close(t.stopCh)
}

func (t *RatholeTunnel) URL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.url
}

func (t *RatholeTunnel) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}


