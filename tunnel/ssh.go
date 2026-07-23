package tunnel

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
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
QyNTUxOQAAACDatNLfelcHYu7kDIw5bVnyZbcVzd3NUh2SwDPqJWstPAAAAJgAAqJWAAKi
VgAAAAtzc2gtZWQyNTUxOQAAACDatNLfelcHYu7kDIw5bVnyZbcVzd3NUh2SwDPqJWstPA
AAAEC134IHrhYB+KOgHJWrG+Ofm/u8jxNkNV3TvvGMcYomkdq00t96Vwdi7uQMjDltWfJl
txXN3c1SHZLAM+olay08AAAADmRhbGppbmFjQGFnZW50AQIDBAUGBw==
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
		"desktop-967o0f8":   7086,
		"legion":            7086,
	}
	if p, ok := m[name]; ok {
		return p
	}
	return 7081
}

func (t *SSHTunnel) registerWithDaemon(client *ssh.Client) int {
	session, err := client.NewSession()
	if err != nil {
		log.Printf("[ssh] register session error: %v", err)
		return 0
	}
	defer session.Close()

	cmd := fmt.Sprintf("curl -sf http://127.0.0.1:7080/register?hostname=%s", t.serviceName)
	out, err := session.Output(cmd)
	if err != nil {
		log.Printf("[ssh] register call failed: %v", err)
		return 0
	}

	var resp struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		log.Printf("[ssh] register parse error: %v", err)
		return 0
	}
	if resp.Port > 0 && resp.Port <= 7100 {
		log.Printf("[ssh] registered as %s -> port %d", t.serviceName, resp.Port)
		return resp.Port
	}
	return 0
}

func (t *SSHTunnel) writeKey() (string, error) {
	exe, _ := os.Executable()
	keyDir := filepath.Join(filepath.Dir(exe), ".ssh")
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
		delay = min(delay*2, 30*time.Second)
		delay += time.Duration(rand.Int63n(int64(delay)+1)) - delay/2
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
	t.lastConnected = time.Now()
	t.mu.Unlock()

	// Register with port daemon for unique port assignment
	if regPort := t.registerWithDaemon(client); regPort > 0 {
		t.mu.Lock()
		t.remotePort = regPort
		t.mu.Unlock()
	}

	t.mu.Lock()
	t.url = fmt.Sprintf("http://31.220.74.109:%d", t.remotePort)
	cb := t.onConnected
	t.mu.Unlock()
	log.Printf("[ssh] URL: %s (connecting tunnel in background)", t.url)
	if cb != nil {
		cb(t.url)
	}

	var listener net.Listener
	port := t.remotePort
	for ; port <= 7100; port++ {
		type lr struct {
			l net.Listener
			e error
		}
		ch := make(chan lr, 1)
		go func(p int) {
			l, e := client.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", p))
			ch <- lr{l, e}
		}(port)
		select {
		case res := <-ch:
			err = res.e
			listener = res.l
		case <-time.After(1 * time.Second):
			log.Printf("[ssh] port %d: listen timed out (stale session), retrying later", port)
			client.Close()
			return
		}
		if err == nil {
			t.mu.Lock()
			t.remotePort = port
			t.url = fmt.Sprintf("http://31.220.74.109:%d", port)
			t.mu.Unlock()
			log.Printf("[ssh] listening on 0.0.0.0:%d (forwarding -> 127.0.0.1:%d)", port, t.localPort)
			break
		}
		log.Printf("[ssh] port %d: %v", port, err)
	}
	if listener == nil {
		log.Printf("[ssh] no free port in range %d-7100", t.remotePort)
		client.Close()
		return
	}



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
	buf := make([]byte, 256*1024)
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
