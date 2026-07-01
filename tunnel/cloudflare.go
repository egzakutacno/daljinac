package tunnel

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"syscall"
	"time"
)

const cfDownloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe"

type Tunnel struct {
	localPort   int
	cfURL       string
	stopCh      chan struct{}
	onConnected func(url string)
	binPath     string
	running     bool
}

func New(localPort int, onConnected func(url string)) *Tunnel {
	return &Tunnel{
		localPort:   localPort,
		stopCh:      make(chan struct{}),
		onConnected: onConnected,
	}
}

func (t *Tunnel) download() error {
	tmpDir := filepath.Join(os.TempDir(), "daljinac-cf")
	os.MkdirAll(tmpDir, 0755)
	t.binPath = filepath.Join(tmpDir, "cloudflared.exe")

	if _, err := os.Stat(t.binPath); err == nil {
		return nil
	}

	log.Printf("Downloading cloudflared...")
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(cfDownloadURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.OpenFile(t.binPath, os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return fmt.Errorf("write: %w", err)
	}
	out.Close()

	// Remove Mark of the Web
	os.Remove(t.binPath + ":Zone.Identifier")

	log.Printf("cloudflared downloaded to %s", t.binPath)
	return nil
}

func (t *Tunnel) Start() {
	exec.Command("taskkill", "/f", "/im", "cloudflared.exe").Run()
	go t.Run()
}

func (t *Tunnel) Run() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, true)
			log.Printf("PANIC in tunnel: %v\n%s", r, buf[:n])
		}
	}()

	if err := t.download(); err != nil {
		log.Printf("Failed to download cloudflared: %v", err)
		return
	}

	t.running = true
	delay := 2 * time.Second

	for {
		select {
		case <-t.stopCh:
			t.running = false
			return
		default:
		}

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
		"tunnel",
		"--url", fmt.Sprintf("http://127.0.0.1:%d", t.localPort),
		"--no-autoupdate",
	}

	cmd := exec.Command(t.binPath, args...)
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("StdoutPipe: %v", err)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Printf("StderrPipe: %v", err)
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("cloudflared start: %v", err)
		return
	}

	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()

	urlFound := make(chan string, 1)
	go parseOutput(stdout, urlFound)
	go parseOutput(stderr, urlFound)

	// Wait for URL or process exit
	select {
	case url := <-urlFound:
		if url != "" {
			t.cfURL = url
			log.Printf("Cloudflare URL: %s", url)
			if t.onConnected != nil {
				t.onConnected(url)
			}
		}
	case <-done:
		log.Println("cloudflared exited before URL")
		return
	case <-t.stopCh:
		cmd.Process.Kill()
		return
	}

	// Keep running until stop or cloudflared exits
	select {
	case <-done:
		log.Println("cloudflared exited")
	case <-t.stopCh:
		cmd.Process.Kill()
		log.Println("cloudflared stopped")
	}
}

var urlRe = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

func parseOutput(r io.Reader, urlCh chan<- string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if m := urlRe.FindString(line); m != "" {
			select {
			case urlCh <- m:
			default:
			}
			return
		}
	}
}

func (t *Tunnel) Stop() {
	t.running = false
	close(t.stopCh)
}

func (t *Tunnel) URL() string {
	return t.cfURL
}

func (t *Tunnel) IsRunning() bool {
	return t.running
}
