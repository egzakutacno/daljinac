package tunnel

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const sshServerAddr = "31.220.74.109:22"
const sshUser = "root"

var sshPrivateKey = []byte(`-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACDEOKSdcPLC7/4lRCLKU6A8FNOhwZOkFtrbmwkf4/PiiwAAAJgkTFgrJExY
KwAAAAtzc2gtZWQyNTUxOQAAACDEOKSdcPLC7/4lRCLKU6A8FNOhwZOkFtrbmwkf4/Piiw
AAAEBaPhEkmrJCX9MumPrFGE+Mc38sTXitvivzo4DHhp1SXcQ4pJ1w8sLv/iVEIspToDwU
06HBk6QW2tubCR/j8+KLAAAADmRhbGppbmFjQGFnZW50AQIDBAUGBw==
-----END OPENSSH PRIVATE KEY-----`)

type SSHTunnel struct {
	localPort     int
	serviceName   string
	url           string
	stopCh        chan struct{}
	onConnected   func(url string)
	running       bool
	mu            sync.Mutex
	remotePort    int
	client        *ssh.Client
	lastConnected time.Time
}

func (t *SSHTunnel) LastConnected() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastConnected
}

func NewSSH(localPort int, shareName string, onConnected func(url string)) *SSHTunnel {
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
	log.Printf("[ssh] service name: '%s' (from hostname: '%s')", sanitized, shareName)
	return &SSHTunnel{
		localPort:   localPort,
		serviceName: sanitized,
		stopCh:      make(chan struct{}),
		onConnected: onConnected,
		remotePort:  getSSHPort(sanitized),
	}
}

func getSSHPort(name string) int {
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

func (t *SSHTunnel) writeKey() (string, error) {
	keyDir := filepath.Join(os.Getenv("ProgramData"), "daljinac", ".ssh")
	os.MkdirAll(keyDir, 0700)
	keyPath := filepath.Join(keyDir, "id_daljinac")
	if _, err := os.Stat(keyPath); err == nil {
		return keyPath, nil
	}
	if err := os.WriteFile(keyPath, sshPrivateKey, 0600); err != nil {
		return "", fmt.Errorf("write key: %w", err)
	}
	return keyPath, nil
}

func (t *SSHTunnel) Start() {
	log.Printf("[ssh] Start() called")
	t.mu.Lock()
	t.running = true
	t.mu.Unlock()
	go t.Run()
}

func (t *SSHTunnel) Run() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, true)
			log.Printf("[ssh] PANIC: %v\n%s", r, buf[:n])
		}
	}()

	log.Printf("[ssh] Run() starting")
	t.running = true
	delay := 2 * time.Second

	for {
		select {
		case <-t.stopCh:
			t.mu.Lock()
			t.running = false
			t.mu.Unlock()
			log.Printf("[ssh] stopped by signal")
			return
		default:
		}

		log.Printf("[ssh] connecting (remotePort=%d, delay=%v)...", t.remotePort, delay)
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

func (t *SSHTunnel) connect() {
	signer, err := ssh.ParsePrivateKey(sshPrivateKey)
	if err != nil {
		log.Printf("[ssh] parse key error: %v", err)
		return
	}

	config := &ssh.ClientConfig{
		User:            sshUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	client, err := ssh.Dial("tcp", sshServerAddr, config)
	if err != nil {
		log.Printf("[ssh] dial error: %v", err)
		return
	}
	log.Printf("[ssh] connected to %s", sshServerAddr)

	t.mu.Lock()
	t.client = client
	t.mu.Unlock()

	t.mu.Lock()
	t.url = fmt.Sprintf("http://31.220.74.109:%d", t.remotePort)
	t.mu.Unlock()
	log.Printf("[ssh] URL: %s", t.url)
	t.mu.Lock()
	t.lastConnected = time.Now()
	t.mu.Unlock()
	if t.onConnected != nil {
		t.onConnected(t.url)
	}

	listener, err := client.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", t.remotePort))
	if err != nil {
		log.Printf("[ssh] listen error on remote port %d: %v", t.remotePort, err)
		client.Close()
		return
	}
	log.Printf("[ssh] listening on 0.0.0.0:%d (forwarding -> 127.0.0.1:%d)", t.remotePort, t.localPort)

	// Keepalive: refresh lastConnected every 30s while tunnel is active
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-t.stopCh:
				return
			case <-ticker.C:
				t.mu.Lock()
				t.lastConnected = time.Now()
				t.mu.Unlock()
			}
		}
	}()

	acceptCh := make(chan net.Conn)
	go func() {
		for {
			remoteConn, err := listener.Accept()
			if err != nil {
				select {
				case <-t.stopCh:
				default:
					log.Printf("[ssh] accept error: %v", err)
				}
				close(acceptCh)
				return
			}
			acceptCh <- remoteConn
		}
	}()

	for {
		select {
		case <-t.stopCh:
			listener.Close()
			client.Close()
			log.Printf("[ssh] tunnel stopped")
			return
		case remoteConn, ok := <-acceptCh:
			if !ok {
				listener.Close()
				client.Close()
				log.Printf("[ssh] listener closed, reconnecting...")
				return
			}
			go t.handleConnection(remoteConn)
		}
	}
}

func (t *SSHTunnel) handleConnection(remoteConn net.Conn) {
	defer remoteConn.Close()

	localConn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", t.localPort), 10*time.Second)
	if err != nil {
		log.Printf("[ssh] dial local %d error: %v", t.localPort, err)
		return
	}
	defer localConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		cp(remoteConn, localConn)
		wg.Done()
	}()
	go func() {
		cp(localConn, remoteConn)
		wg.Done()
	}()
	wg.Wait()
}

func cp(dst net.Conn, src net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			_, werr := dst.Write(buf[:n])
			if werr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
}

func (t *SSHTunnel) Stop() {
	t.mu.Lock()
	t.running = false
	if t.client != nil {
		t.client.Close()
	}
	t.mu.Unlock()
	close(t.stopCh)
}

func (t *SSHTunnel) URL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.url
}

func (t *SSHTunnel) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}
