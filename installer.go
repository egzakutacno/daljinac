package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

func Install(exePath, serverURL string) error {
	log.Println("[install] Starting installation")

	agentDir, err := agentDir()
	if err != nil {
		return fmt.Errorf("agent dir: %w", err)
	}

	log.Printf("[install] Agent directory: %s", agentDir)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", agentDir, err)
	}

	destExe := filepath.Join(agentDir, "daljinac.exe")
	log.Printf("[install] Copying self to %s", destExe)

	if err := copyFile(exePath, destExe); err != nil {
		return fmt.Errorf("copy exe: %w", err)
	}

	cfgPath := filepath.Join(agentDir, "agent.json")
	log.Printf("[install] Generating config: %s", cfgPath)

	cfg := DefaultConfig()
	cfg.ServerURL = serverURL
	cfg.APIKey = generateAPIKey()
	cfg.MachineID = ""

	hostname, _ := os.Hostname()
	if hostname != "" {
		cfg.Name = hostname
	} else {
		cfg.Name = "windows-pc"
	}

	log.Printf("[install] Registering with server: %s", serverURL)
	machineID, err := registerOnServer(serverURL, cfg.APIKey, cfg.Name)
	if err != nil {
		log.Printf("[install] Registration warning: %v (continuing)", err)
	}
	cfg.MachineID = machineID

	if err := WriteConfig(cfgPath, cfg); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	if err := createScheduledTask(cfg.MachineID, destExe); err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	log.Println("[install] Starting agent via scheduled task")
	runScheduledTask(cfg.MachineID)

	log.Println("[install] Installation complete")
	return nil
}

func Remove() {
	log.Println("[remove] Starting removal")

	exe, _ := os.Executable()
	base := filepath.Base(exe)
	exec.Command("taskkill", "/f", "/im", base).Run()

	exec.Command("schtasks", "/delete", "/tn", "Daljinac-*", "/f").Run()

	agentDir, err := agentDir()
	if err == nil {
		log.Printf("[remove] Removing directory: %s", agentDir)
		os.RemoveAll(agentDir)
	}

	log.Println("[remove] Removal complete")
}

func createScheduledTask(machineID, exePath string) error {
	taskName := taskName(machineID)

	args := []string{
		"/create",
		"/tn", taskName,
		"/tr", exePath,
		"/sc", "ONLOGON",
		"/f",
	}

	username := osUsername()
	if username != "" {
		args = append(args, "/ru", username)
	}

	log.Printf("[install] Creating scheduled task: %s", taskName)
	cmd := exec.Command("schtasks", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks create: %v — %s", err, string(out))
	}

	return nil
}

func runScheduledTask(machineID string) {
	taskName := taskName(machineID)
	exec.Command("schtasks", "/run", "/tn", taskName).Run()
}

func taskName(machineID string) string {
	if machineID == "" {
		return "Daljinac"
	}
	return "Daljinac-" + sanitizeName(machineID)
}

func agentDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "daljinac-agent"), nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0755)
}

func registerOnServer(serverURL, apiKey, name string) (string, error) {
	url := mustTrimSuffix(serverURL, "/") + "/api/v1/agent/register"

	hostname, _ := os.Hostname()
	if name == "" {
		name = hostname
	}

	body := map[string]string{
		"name":     name,
		"api_key":  apiKey,
		"hostname": hostname,
		"metadata": "{}",
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	client := &http.Client{}
	resp, err := client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("HTTP error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return "", fmt.Errorf("server returned %d", resp.StatusCode)
	}

	var result struct {
		MachineID string `json:"machine_id"`
		APIKey    string `json:"api_key"`
		Status    string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	log.Printf("[install] Registered as machine_id=%s", result.MachineID)
	return result.MachineID, nil
}

func osUsername() string {
	advapi32 := syscall.NewLazyDLL("advapi32.dll")
	getUserNameW := advapi32.NewProc("GetUserNameW")
	buf := make([]uint16, 256)
	var size uint32 = 256
	ret, _, _ := getUserNameW.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ret == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:size])
}

func mustTrimSuffix(s, suffix string) string {
	if suffix == "/" {
		s = strings.TrimRight(s, "/")
	}
	return s
}
