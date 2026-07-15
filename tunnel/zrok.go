package tunnel

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const zrokDownloadURL = "https://github.com/openziti/zrok/releases/download/v2.0.4/zrok_2.0.4_windows_amd64.tar.gz"
const zrokToken = "Cs9sCOs9dEgp"

type Tunnel struct {
	localPort   int
	shareName   string
	hasName     bool
	url         string
	stopCh      chan struct{}
	onConnected func(url string)
	binPath     string
	running     bool
}

func New(localPort int, shareName string, onConnected func(url string)) *Tunnel {
	// Sanitize hostname to a valid zrok share name with "dalj-" prefix
	sanitized := strings.ToLower(shareName)
	sanitized = strings.NewReplacer("_", "-", ".", "-", " ", "-").Replace(sanitized)
	var clean strings.Builder
	for _, r := range sanitized {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			clean.WriteRune(r)
		}
	}
	sanitized = "dalj-" + strings.Trim(clean.String(), "-")
	if len(sanitized) < 5 {
		sanitized = "daljinac"
	}
	log.Printf("[zrok] share name: '%s' (from hostname: '%s')", sanitized, shareName)
	return &Tunnel{
		localPort:   localPort,
		shareName:   sanitized,
		stopCh:      make(chan struct{}),
		onConnected: onConnected,
	}
}

func (t *Tunnel) download() error {
	tmpDir := filepath.Join(os.TempDir(), "daljinac-zrok")
	os.MkdirAll(tmpDir, 0755)
	t.binPath = filepath.Join(tmpDir, "zrok2.exe")

	if _, err := os.Stat(t.binPath); err == nil {
		log.Printf("[zrok] binary already exists at %s", t.binPath)
		return nil
	}

	log.Printf("[zrok] downloading from %s", zrokDownloadURL)
	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Get(zrokDownloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}

	tarPath := filepath.Join(tmpDir, "zrok.tar.gz")
	log.Printf("[zrok] saving tar.gz to %s", tarPath)
	out, err := os.Create(tarPath)
	if err != nil {
		return fmt.Errorf("create tar: %w", err)
	}
	written, _ := io.Copy(out, resp.Body)
	out.Close()
	defer os.Remove(tarPath)
	log.Printf("[zrok] downloaded %d bytes", written)

	log.Printf("[zrok] extracting zrok2.exe...")
	cmd := exec.Command("tar", "-xf", tarPath, "-C", tmpDir, "zrok2.exe")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("extract: %w - %s", err, string(output))
	}

	os.Remove(t.binPath + ":Zone.Identifier")

	// Verify the binary exists and is executable
	if _, err := os.Stat(t.binPath); err != nil {
		return fmt.Errorf("binary not found after extract: %w", err)
	}
	log.Printf("[zrok] downloaded and extracted OK to %s", t.binPath)
	return nil
}

func (t *Tunnel) runZrok(args ...string) (string, error) {
	cmd := exec.Command(t.binPath, args...)
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
	cmd.Env = append(os.Environ(), "ZROK_HEADLESS=true")
	output, err := cmd.CombinedOutput()
	outStr := string(output)
	if err != nil {
		return outStr, fmt.Errorf("zrok %v failed: %w\nOutput: %s", args, err, outStr)
	}
	return outStr, nil
}

func (t *Tunnel) isEnabled() bool {
	// Check multiple possible locations for environment.json
	home := os.Getenv("USERPROFILE")
	if home == "" {
		home = os.Getenv("HOME")
	}
	candidates := []string{
		filepath.Join(home, ".zrok2", "environment.json"),
		filepath.Join(os.Getenv("HOMEDRIVE")+os.Getenv("HOMEPATH"), ".zrok2", "environment.json"),
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			log.Printf("[zrok] found environment at %s", path)
			return true
		}
	}
	return false
}

func (t *Tunnel) ensureEnabled() error {
	// Always disable first to clear any old environment (from previous agent versions)
	log.Printf("[zrok] disabling old environment (if any)...")
	for i := 0; i < 3; i++ {
		_, err := t.runZrok("disable")
		if err == nil {
			break
		}
		if i < 2 {
			log.Printf("[zrok] disable attempt %d failed, retrying in 5s...", i+1)
			time.Sleep(5 * time.Second)
		}
	}

	log.Printf("[zrok] enabling with token...")
	var enableOut string
	var enableErr error
	for i := 0; i < 3; i++ {
		out, err := t.runZrok("enable", zrokToken)
		if err == nil {
			enableOut = out
			enableErr = nil
			break
		}
		enableOut = out
		enableErr = err
		if strings.Contains(out, "already enabled") {
			log.Printf("[zrok] enable attempt %d failed (already enabled), retrying disable+enable in 5s...", i+1)
			t.runZrok("disable")
			time.Sleep(5 * time.Second)
		} else if i < 2 {
			log.Printf("[zrok] enable attempt %d failed, retrying in 5s...", i+1)
			time.Sleep(5 * time.Second)
		}
	}
	if enableErr != nil {
		log.Printf("[zrok] enable output: %s", enableOut)
		return fmt.Errorf("enable failed after 3 attempts: %w", enableErr)
	}
	log.Printf("[zrok] enable output: %s", strings.TrimSpace(enableOut))

	// Verify enable worked
	if !t.isEnabled() {
		return fmt.Errorf("enable reported success but environment.json not found")
	}
	log.Printf("[zrok] enabled OK — shares will appear under token account")

	// Attempt to create a reserved name for persistent URL
	if t.shareName != "" {
		// Delete stale name first (might be owned by a previous environment)
		t.runZrok("delete", "name", t.shareName)
		log.Printf("[zrok] creating reserved name '%s'...", t.shareName)
		var lastErr string
		for retry := 0; retry < 3; retry++ {
			if retry > 0 {
				time.Sleep(time.Duration(retry*5) * time.Second)
			}
			out, err := t.runZrok("create", "name", t.shareName)
			if err == nil {
				log.Printf("[zrok] reserved name '%s' created OK", t.shareName)
				t.hasName = true
				return nil
			}
			lastErr = out
			log.Printf("[zrok] create name attempt %d failed, retrying...", retry+1)
		}
		log.Printf("[zrok] create name failed after 3 attempts: %s — falling back to ephemeral share", lastErr)
		t.shareName = ""
	}

	return nil
}

func (t *Tunnel) Start() {
	log.Printf("[zrok] Start() called")
	exec.Command("taskkill", "/f", "/im", "zrok2.exe").Run()
	exec.Command("taskkill", "/f", "/im", "cloudflared.exe").Run()
	go t.Run()
}

func (t *Tunnel) Run() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, true)
			log.Printf("[zrok] PANIC: %v\n%s", r, buf[:n])
		}
	}()

	log.Printf("[zrok] Run() starting")

	if err := t.download(); err != nil {
		log.Printf("[zrok] download error: %v", err)
		return
	}

	if err := t.ensureEnabled(); err != nil {
		log.Printf("[zrok] enable error: %v", err)
		return
	}

	t.running = true
	delay := 2 * time.Second

	for {
		select {
		case <-t.stopCh:
			t.running = false
			log.Printf("[zrok] stopped by signal")
			return
		default:
		}

		log.Printf("[zrok] connecting (delay=%v)...", delay)
		t.connect()

		if !t.running {
			return
		}

		select {
		case <-t.stopCh:
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, 60*time.Second)
	}
}

func (t *Tunnel) connect() {
	args := []string{
		"share", "public",
		fmt.Sprintf("http://127.0.0.1:%d", t.localPort),
		"--headless",
	}
	if t.hasName && t.shareName != "" {
		// Recreate name before each share to avoid 409 "name already in use" on retry
		t.runZrok("delete", "name", t.shareName)
		for retry := 0; retry < 3; retry++ {
			if retry > 0 {
				time.Sleep(time.Duration(retry*3) * time.Second)
			}
			out, err := t.runZrok("create", "name", t.shareName)
			if err == nil {
				break
			}
			log.Printf("[zrok] pre-share name creation attempt %d failed: %s", retry+1, strings.TrimSpace(out))
			if retry == 2 {
				log.Printf("[zrok] giving up on named share, falling back to ephemeral")
				t.shareName = ""
				t.hasName = false
			}
		}
	}
	if t.hasName && t.shareName != "" {
		args = append(args, "-n", "public:"+t.shareName)
		log.Printf("[zrok] starting: zrok2 share public http://127.0.0.1:%d --headless -n public:%s", t.localPort, t.shareName)
	} else {
		log.Printf("[zrok] starting: zrok2 share public http://127.0.0.1:%d --headless (ephemeral)", t.localPort)
	}

	cmd := exec.Command(t.binPath, args...)
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
	cmd.Env = append(os.Environ(), "ZROK_HEADLESS=true")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[zrok] StdoutPipe error: %v", err)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Printf("[zrok] StderrPipe error: %v", err)
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("[zrok] start error: %v", err)
		return
	}
	log.Printf("[zrok] process started (pid=%d)", cmd.Process.Pid)

	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()

	urlFound := make(chan string, 1)
	go t.parseOutput(stdout, urlFound)
	go t.parseOutput(stderr, urlFound)

	// Wait for URL or process exit
	select {
	case url := <-urlFound:
		if url != "" {
			t.url = url
			log.Printf("[zrok] GOT URL: %s", url)
			log.Printf("[zrok] calling onConnected...")
			if t.onConnected != nil {
				t.onConnected(url)
			}
			log.Printf("[zrok] onConnected done")
		} else {
			log.Printf("[zrok] urlFound but empty!")
		}
	case <-done:
		log.Println("[zrok] exited before URL")
		return
	case <-t.stopCh:
		log.Println("[zrok] stopped before URL")
		cmd.Process.Kill()
		return
	}

	// Keep running until stop or zrok exits
	select {
	case <-done:
		log.Println("[zrok] share process exited")
	case <-t.stopCh:
		cmd.Process.Kill()
		log.Println("[zrok] share process killed")
	}
}

func (t *Tunnel) parseOutput(r io.Reader, urlCh chan<- string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		log.Printf("[zrok raw] %s", line)

		if !strings.HasPrefix(line, "{") {
			continue
		}
		var entry struct {
			Msg string `json:"msg"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			log.Printf("[zrok] json parse error on: %s", line)
			continue
		}
		if strings.Contains(entry.Msg, "access your zrok share") {
			parts := strings.Fields(entry.Msg)
			for _, p := range parts {
				if strings.Contains(p, ".shares.zrok.io") {
					url := "https://" + p
					log.Printf("[zrok] found URL in output: %s", url)
					select {
					case urlCh <- url:
					default:
					}
					return
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[zrok] scanner error: %v", err)
	}
}

func (t *Tunnel) Stop() {
	t.running = false
	close(t.stopCh)
}

func (t *Tunnel) URL() string {
	return t.url
}

func (t *Tunnel) IsRunning() bool {
	return t.running
}
