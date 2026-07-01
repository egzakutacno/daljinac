package websocket

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"daljinac/server"

	"github.com/gorilla/websocket"
)

type Task struct {
	TaskID  string `json:"task_id"`
	Action  string `json:"action"`
	Payload string `json:"payload"`
	Timeout int    `json:"timeout"`
}

type TaskResult struct {
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
	Output   string `json:"output"`
	Error    string `json:"error"`
	ExitCode int    `json:"exit_code"`
}

type OnStatusChange func(status string)

type Client struct {
	serverURL      string
	machineID      string
	apiKey         string
	exec           *server.Executor
	conn           *websocket.Conn
	connMu         sync.Mutex
	stopCh         chan struct{}
	doneCh         chan struct{}
	onStatusChange OnStatusChange
	reconnectDelay time.Duration
	consecutiveErr int
}

func New(serverURL, machineID, apiKey string, exec *server.Executor) *Client {
	return &Client{
		serverURL: serverURL,
		machineID: machineID,
		apiKey:    apiKey,
		exec:      exec,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

func (c *Client) SetOnStatusChange(cb OnStatusChange) {
	c.onStatusChange = cb
}

func (c *Client) ConnectAndLoop() {
	defer close(c.doneCh)
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, true)
			log.Printf("[ws] PANIC: %v\n%s", r, buf[:n])
		}
	}()

	c.reconnectDelay = time.Duration(1) * time.Second

	for {
		select {
		case <-c.stopCh:
			log.Println("[ws] Stop requested")
			c.disconnect()
			return
		default:
		}

		if c.onStatusChange != nil {
			c.onStatusChange("connecting")
		}

		if err := c.connect(); err != nil {
			log.Printf("[ws] Connect failed: %v", err)
			c.consecutiveErr++
			if c.consecutiveErr >= 5 {
				log.Println("[ws] 5 consecutive errors, attempting re-registration")
				c.reregister()
				c.consecutiveErr = 0
			}
			c.waitReconnect()
			continue
		}

		c.consecutiveErr = 0
		c.reconnectDelay = time.Duration(1) * time.Second

		if c.onStatusChange != nil {
			c.onStatusChange("connected")
		}

		log.Println("[ws] Connected, reading messages")
		c.readLoop()
	}
}

func (c *Client) connect() error {
	url := strings.NewReplacer(
		"https://", "wss://",
		"http://", "ws://",
	).Replace(c.serverURL)

	wsURL := fmt.Sprintf("%s/api/v1/agent/ws/%s?api_key=%s", strings.TrimRight(url, "/"), c.machineID, c.apiKey)

	log.Printf("[ws] Dialing %s", wsURL)

	dialer := &websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()

	return nil
}

func (c *Client) readLoop() {
	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		conn := c.getConn()
		if conn == nil {
			return
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[ws] Read error: %v", err)
			c.disconnect()
			return
		}

		var task Task
		if err := json.Unmarshal(message, &task); err != nil {
			log.Printf("[ws] Invalid message: %v - body: %s", err, string(message))
			continue
		}

		log.Printf("[ws] Received task: %s action=%s", task.TaskID, task.Action)
		result := c.executeTask(&task)
		c.sendResult(conn, result)
	}
}

func (c *Client) executeTask(task *Task) *TaskResult {
	var result server.ExecResult

	timeout := time.Duration(task.Timeout) * time.Second
	if timeout <= 0 || timeout > 5*time.Minute {
		timeout = 60 * time.Second
	}

	done := make(chan server.ExecResult, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- server.ExecResult{Stderr: fmt.Sprintf("panic: %v", r), ExitCode: 1}
			}
		}()
		switch task.Action {
		case "ping":
			done <- server.ExecResult{Stdout: "pong", ExitCode: 0}
		case "run_powershell":
			done <- c.exec.ExecutePS(task.Payload)
		case "run_cmd":
			done <- c.exec.ExecuteCMD(task.Payload)
		case "restart_service":
			done <- c.exec.ExecutePS(fmt.Sprintf("Restart-Service -Name '%s' -Force", task.Payload))
		case "file_read":
			data, err := os.ReadFile(task.Payload)
			if err != nil {
				done <- server.ExecResult{Stderr: err.Error(), ExitCode: 1}
			} else {
				s := string(data)
				if len(s) > 50000 {
					s = s[:50000] + "\n...truncated"
				}
				done <- server.ExecResult{Stdout: s, ExitCode: 0}
			}
		case "file_write":
			var fw struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(task.Payload), &fw); err != nil {
				done <- server.ExecResult{Stderr: err.Error(), ExitCode: 1}
			} else {
				if err := os.MkdirAll(filepath.Dir(fw.Path), 0755); err != nil {
					done <- server.ExecResult{Stderr: err.Error(), ExitCode: 1}
				} else if err := os.WriteFile(fw.Path, []byte(fw.Content), 0644); err != nil {
					done <- server.ExecResult{Stderr: err.Error(), ExitCode: 1}
				} else {
					done <- server.ExecResult{Stdout: "ok", ExitCode: 0}
				}
			}
		case "file_delete":
			if err := os.Remove(task.Payload); err != nil {
				done <- server.ExecResult{Stderr: err.Error(), ExitCode: 1}
			} else {
				done <- server.ExecResult{Stdout: "deleted", ExitCode: 0}
			}
		case "install_package":
			result := c.exec.ExecuteCMD(fmt.Sprintf("winget install %s", task.Payload))
			if result.ExitCode != 0 {
				result = c.exec.ExecutePS(fmt.Sprintf("winget install %s", task.Payload))
			}
			done <- result
		case "screenshot":
			data, err := server.CaptureScreen()
			if err != nil {
				done <- server.ExecResult{Stderr: err.Error(), ExitCode: 1}
			} else {
				b64 := base64.StdEncoding.EncodeToString(data)
				if len(b64) > 50000 {
					b64 = b64[:50000]
				}
				done <- server.ExecResult{Stdout: b64, ExitCode: 0}
			}
		case "kill":
			os.Exit(0)
		default:
			done <- server.ExecResult{Stderr: fmt.Sprintf("unknown action: %s", task.Action), ExitCode: 1}
		}
	}()

	select {
	case result = <-done:
	case <-time.After(timeout):
		result = server.ExecResult{Stderr: "task timed out", ExitCode: 1}
	case <-c.stopCh:
		result = server.ExecResult{Stderr: "agent shutting down", ExitCode: 1}
	}

	return &TaskResult{
		TaskID:   task.TaskID,
		Status:   statusFromResult(result),
		Output:   truncate(result.Stdout, 50000),
		Error:    truncate(result.Stderr, 50000),
		ExitCode: result.ExitCode,
	}
}

func (c *Client) sendResult(conn *websocket.Conn, result *TaskResult) {
	data, err := json.Marshal(result)
	if err != nil {
		log.Printf("[ws] Marshal result error: %v", err)
		return
	}

	c.connMu.Lock()
	defer c.connMu.Unlock()

	deadline := time.Now().Add(30 * time.Second)
	conn.SetWriteDeadline(deadline)

	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("[ws] Write result error: %v", err)
	}
}

func (c *Client) reregister() {
	url := strings.TrimRight(c.serverURL, "/") + "/api/v1/agent/register"
	body := map[string]string{
		"name":     "",
		"api_key":  c.apiKey,
		"hostname": "",
	}
	hostname, _ := os.Hostname()
	if body["name"] == "" {
		body["name"] = hostname
	}
	if body["hostname"] == "" {
		body["hostname"] = hostname
	}

	data, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		log.Printf("[ws] Re-register HTTP error: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode == 200 || resp.StatusCode == 201 {
		log.Println("[ws] Re-registration successful")
	} else {
		log.Printf("[ws] Re-registration returned %d", resp.StatusCode)
	}
}

func (c *Client) waitReconnect() {
	select {
	case <-c.stopCh:
		return
	case <-time.After(c.reconnectDelay):
	}
	c.reconnectDelay *= 2
	if c.reconnectDelay > time.Duration(30)*time.Second {
		c.reconnectDelay = time.Duration(30) * time.Second
	}
	log.Printf("[ws] Reconnecting in %v", c.reconnectDelay)
}

func (c *Client) getConn() *websocket.Conn {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	return c.conn
}

func (c *Client) disconnect() {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

func (c *Client) Done() <-chan struct{} {
	return c.doneCh
}

func (c *Client) Close() {
	close(c.stopCh)
	c.disconnect()
	select {
	case <-c.doneCh:
	case <-time.After(10 * time.Second):
	}
}

func statusFromResult(r server.ExecResult) string {
	if r.ExitCode == 0 {
		return "success"
	}
	return "error"
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}
