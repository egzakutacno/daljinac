package server

import (
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	shell32             = syscall.NewLazyDLL("shell32.dll")
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	gdi32               = syscall.NewLazyDLL("gdi32.dll")

	getModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	createWindowExW     = user32.NewProc("CreateWindowExW")
	defWindowProcW      = user32.NewProc("DefWindowProcW")
	registerClassExW    = user32.NewProc("RegisterClassExW")
	destroyWindow       = user32.NewProc("DestroyWindow")
	postQuitMessage     = user32.NewProc("PostQuitMessage")
	getMessageW         = user32.NewProc("GetMessageW")
	translateMessage    = user32.NewProc("TranslateMessage")
	dispatchMessageW    = user32.NewProc("DispatchMessageW")
	shellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	loadIconW           = user32.NewProc("LoadIconW")
	createPopupMenu     = user32.NewProc("CreatePopupMenu")
	appendMenuW         = user32.NewProc("AppendMenuW")
	trackPopupMenu      = user32.NewProc("TrackPopupMenu")
	destroyMenu         = user32.NewProc("DestroyMenu")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	getCursorPos        = user32.NewProc("GetCursorPos")
	postMessageW        = user32.NewProc("PostMessageW")
	openClipboard       = user32.NewProc("OpenClipboard")
	emptyClipboard      = user32.NewProc("EmptyClipboard")
	setClipboardData    = user32.NewProc("SetClipboardData")
	closeClipboard      = user32.NewProc("CloseClipboard")
	globalAlloc         = kernel32.NewProc("GlobalAlloc")
	globalLock          = kernel32.NewProc("GlobalLock")
	globalUnlock        = kernel32.NewProc("GlobalUnlock")
	rtlMoveMemory       = kernel32.NewProc("RtlMoveMemory")
)

const (
	WM_DESTROY       = 2
	WM_COMMAND       = 0x0111
	WM_APP           = 0x8000
	NIM_ADD          = 0
	NIM_MODIFY       = 1
	NIM_DELETE       = 2
	NIF_MESSAGE      = 1
	NIF_ICON         = 2
	NIF_TIP          = 4
	NIF_INFO         = 0x10

	MF_STRING        = 0
	MF_SEPARATOR     = 0x0800
	MF_DISABLED      = 0x0002
	MF_GRAYED        = 0x0001

	TPM_RIGHTALIGN   = 0x0008
	TPM_BOTTOMALIGN  = 0x0020

	NIIF_NONE        = 0
	NIIF_INFO        = 1
	NIIF_WARNING     = 2
	NIIF_ERROR       = 3
	NIIF_USER        = 4

	GMEM_MOVEABLE    = 0x0002

	CF_UNICODETEXT   = 13

	WM_NULL          = 0

	IDI_APPLICATION  = 32512
	IDI_WARNING      = 32515
	IDI_INFORMATION  = 32517
	IDI_ERROR        = 32513
	IDI_SHIELD       = 32518
)

const (
	IDM_START = 1000 + iota
	IDM_STOP
	IDM_RESTART_TUNNEL
	IDM_COPY_URL
	IDM_SEPARATOR_1
	IDM_INSTALL
	IDM_REMOVE
	IDM_SEPARATOR_2
	IDM_EXIT
)

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type POINT struct {
	X, Y int32
}

type NOTIFYICONDATAW struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         syscall.GUID
	HBalloonIcon     uintptr
}

type Tray struct {
	server    *Server
	hwnd      uintptr
	nid       NOTIFYICONDATAW
	wg        sync.WaitGroup
	stop      chan struct{}
	url       string
	tag       string
	mu        sync.Mutex
	hIcons    [4]uintptr

	startFunc         func()
	stopFunc          func()
	restartTunnelFunc func()
}

func NewTray(srv *Server, tag string) *Tray {
	return &Tray{
		server: srv,
		stop:   make(chan struct{}),
		tag:    tag,
	}
}

func (t *Tray) SetStartFunc(fn func())       { t.startFunc = fn }
func (t *Tray) SetStopFunc(fn func())        { t.stopFunc = fn }
func (t *Tray) SetRestartTunnelFunc(fn func()) { t.restartTunnelFunc = fn }

func (t *Tray) loadIcons() {
	t.hIcons[0] = t.loadSysIcon(IDI_SHIELD)       // stopped
	t.hIcons[1] = t.loadSysIcon(IDI_INFORMATION)   // starting
	t.hIcons[2] = t.loadSysIcon(IDI_APPLICATION)   // running
	t.hIcons[3] = t.loadSysIcon(IDI_ERROR)         // error
}

func (t *Tray) loadSysIcon(id int) uintptr {
	ret, _, _ := loadIconW.Call(0, uintptr(id))
	return ret
}

func (t *Tray) SetURL(url string) {
	t.mu.Lock()
	t.url = url
	t.mu.Unlock()
}

func (t *Tray) hostLabel() string {
	hostname := "PC"
	if t.server != nil && t.server.info != nil && t.server.info.Hostname != "" {
		hostname = t.server.info.Hostname
	}
	label := "Agent " + hostname
	if t.tag != "" {
		label += " [" + t.tag + "]"
	}
	return label
}

func (t *Tray) SetStatus(st AgentStatus) {
	t.mu.Lock()
	t.mu.Unlock()
	t.updateTrayIcon(st)
}

func (t *Tray) updateTrayIcon(st AgentStatus) {
	label := t.hostLabel()
	statusText := st.String()
	tip := label + " — " + statusText

	iconIdx := int(st)
	if iconIdx < 0 || iconIdx >= len(t.hIcons) {
		iconIdx = 0
	}

	t.nid.UFlags = NIF_ICON | NIF_TIP | NIF_MESSAGE
	t.nid.HIcon = t.hIcons[iconIdx]
	copy(t.nid.SzTip[:], syscall.StringToUTF16(tip))

	if t.hwnd != 0 {
		shellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&t.nid)))
	}
}

func (t *Tray) showBalloon(title, text string, iconType uint32) {
	t.nid.UFlags = NIF_INFO
	t.nid.DwInfoFlags = iconType
	t.nid.UVersion = 3
	copy(t.nid.SzInfoTitle[:], syscall.StringToUTF16(title))
	copy(t.nid.SzInfo[:], syscall.StringToUTF16(text))

	if t.hwnd != 0 {
		shellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&t.nid)))
	}

	t.nid.UFlags = NIF_ICON | NIF_TIP | NIF_MESSAGE
	t.updateTrayIcon(AgentStatus(t.server.status.Load()))
}

func (t *Tray) Run() {
	if runtime.GOOS != "windows" {
		return
	}
	t.wg.Add(1)
	go t.runLoop()
}

func (t *Tray) runLoop() {
	defer t.wg.Done()

	hInstance, _, _ := getModuleHandleW.Call(0)
	className := syscall.StringToUTF16Ptr("AgentClassV2")

	cb := syscall.NewCallback(func(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
		return t.wndProc(hwnd, msg, wParam, lParam)
	})

	wc := WNDCLASSEXW{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
		LpfnWndProc:   cb,
		HInstance:     hInstance,
		HbrBackground: 6,
		LpszClassName: className,
	}
	registerClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, _ := createWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0, 0, 0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		return
	}
	t.hwnd = hwnd

	t.loadIcons()

	label := t.hostLabel()
	t.nid = NOTIFYICONDATAW{
		HWnd:             hwnd,
		UID:              1,
		UFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
		UCallbackMessage: WM_APP + 1,
	}
	copy(t.nid.SzTip[:], syscall.StringToUTF16(label))
	t.nid.CbSize = uint32(unsafe.Sizeof(t.nid))
	t.nid.HIcon = t.hIcons[0]
	shellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&t.nid)))

	var m struct {
		HWnd    uintptr
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		PtX     int32
		PtY     int32
	}
	for {
		ret, _, _ := getMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if ret == 0 {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&m)))
		dispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	shellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&t.nid)))
	destroyWindow.Call(hwnd)
}

func (t *Tray) wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_DESTROY:
		postQuitMessage.Call(0)
		return 0
	case WM_COMMAND:
		t.handleMenuCommand(int(wParam) & 0xFFFF)
		return 0
	case WM_APP + 1:
		switch lParam {
		case 0x0204: // WM_RBUTTONUP
			t.showContextMenu()
		case 0x0201: // WM_LBUTTONUP
			t.openURL()
		case 0x0403: // NIN_BALLOONUSERCLICK (0x403)
			t.openURL()
		}
		return 0
	}
	ret, _, _ := defWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}

func (t *Tray) hostname() string {
	if t.server != nil && t.server.info != nil && t.server.info.Hostname != "" {
		return t.server.info.Hostname
	}
	return "PC"
}

func (t *Tray) showContextMenu() {
	hMenu, _, _ := createPopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer destroyMenu.Call(hMenu)

	ptr := func(s string) uintptr {
		return uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(s)))
	}

	st := AgentStatus(t.server.status.Load())
	label := t.hostname()
	if t.tag != "" {
		label += " [" + t.tag + "]"
	}
	statusItem := "Agent " + label + " — " + st.String()
	appendMenuW.Call(hMenu, MF_DISABLED|MF_GRAYED, IDM_START, ptr(statusItem))
	appendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)

	if st == StatusRunning {
		appendMenuW.Call(hMenu, MF_STRING, IDM_STOP, ptr("Stop Agent"))
		appendMenuW.Call(hMenu, MF_STRING, IDM_RESTART_TUNNEL, ptr("Restart Tunnel"))
		appendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
		appendMenuW.Call(hMenu, MF_STRING, IDM_COPY_URL, ptr("Copy URL"))
	} else {
		appendMenuW.Call(hMenu, MF_STRING, IDM_START, ptr("Start Agent"))
	}

	appendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
	appendMenuW.Call(hMenu, MF_STRING, IDM_INSTALL, ptr("Install Auto-Start"))
	appendMenuW.Call(hMenu, MF_STRING, IDM_REMOVE, ptr("Remove Auto-Start"))
	appendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
	appendMenuW.Call(hMenu, MF_STRING, IDM_EXIT, ptr("Exit"))

	var p POINT
	getCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	setForegroundWindow.Call(t.hwnd)
	trackPopupMenu.Call(hMenu, 0, uintptr(p.X), uintptr(p.Y), 0, t.hwnd, 0)
	postMessageW.Call(t.hwnd, WM_NULL, 0, 0)
}

func (t *Tray) handleMenuCommand(cmd int) {
	switch cmd {
	case IDM_START:
		if t.startFunc != nil {
			t.startFunc()
		}
	case IDM_STOP:
		if t.stopFunc != nil {
			t.stopFunc()
		}
	case IDM_RESTART_TUNNEL:
		if t.restartTunnelFunc != nil {
			t.restartTunnelFunc()
		}
	case IDM_COPY_URL:
		t.copyURL()
	case IDM_INSTALL:
		t.installService()
	case IDM_REMOVE:
		t.removeService()
	case IDM_EXIT:
		postQuitMessage.Call(0)
	}
}

func (t *Tray) openURL() {
	t.mu.Lock()
	url := t.url
	t.mu.Unlock()
	if url == "" {
		return
	}
	var shell32dll = syscall.NewLazyDLL("shell32.dll")
	var shellExecuteW = shell32dll.NewProc("ShellExecuteW")
	shellExecuteW.Call(0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("open"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(url))), 0, 0, 5)
}

func (t *Tray) copyURL() {
	t.mu.Lock()
	url := t.url
	t.mu.Unlock()
	if url == "" {
		t.showBalloon("No URL", "Agent tunnel is not connected yet", NIIF_INFO)
		return
	}

	utf16 := syscall.StringToUTF16(url)
	size := len(utf16) * 2

	openClipboard.Call(t.hwnd)
	emptyClipboard.Call()

	hMem, _, _ := globalAlloc.Call(GMEM_MOVEABLE, uintptr(size))
	if hMem != 0 {
		pMem, _, _ := globalLock.Call(hMem)
		if pMem != 0 {
			rtlMoveMemory.Call(pMem, uintptr(unsafe.Pointer(&utf16[0])), uintptr(size))
			globalUnlock.Call(hMem)
			setClipboardData.Call(CF_UNICODETEXT, hMem)
		}
	}
	closeClipboard.Call()

	t.showBalloon("URL Copied", url, NIIF_INFO)
}

func (t *Tray) installService() {
	exe, err := getExecutable()
	if err != nil {
		return
	}
	name := "RemoteAgentV2"
	psCmd := fmt.Sprintf(
		`powershell -WindowStyle Hidden -Command Start-Process -FilePath '%s' -ArgumentList '-start' -WindowStyle Hidden`,
		exe,
	)
	exec.Command("schtasks", "/create",
		"/tn", name, "/tr", psCmd,
		"/sc", "ONLOGON", "/ru", osUsername(), "/f",
	).Run()
	exec.Command("schtasks", "/run", "/tn", name).Run()
	t.showBalloon("Auto-Start Installed", "Agent will start automatically on next login", NIIF_INFO)
}

func (t *Tray) removeService() {
	exec.Command("taskkill", "/f", "/im", "agent.exe").Run()
	exec.Command("schtasks", "/delete", "/tn", "RemoteAgentV2", "/f").Run()
	t.showBalloon("Auto-Start Removed", "Agent removed from startup", NIIF_INFO)
}

var trayMu sync.Mutex
var cachedExe string

func getExecutable() (string, error) {
	trayMu.Lock()
	defer trayMu.Unlock()
	if cachedExe != "" {
		return cachedExe, nil
	}
	buf := make([]uint16, 1024)
	ret, _, _ := kernel32.NewProc("GetModuleFileNameW").Call(0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if ret == 0 {
		return "", fmt.Errorf("GetModuleFileNameW failed")
	}
	cachedExe = syscall.UTF16ToString(buf[:ret])
	return cachedExe, nil
}

func osUsername() string {
	advapi32 := syscall.NewLazyDLL("advapi32.dll")
	getUserNameW := advapi32.NewProc("GetUserNameW")
	buf := make([]uint16, 256)
	var size uint32 = 256
	ret, _, _ := getUserNameW.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ret == 0 {
		return "USERNAME"
	}
	return syscall.UTF16ToString(buf[:size])
}

func (t *Tray) Stop() {
	postQuitMessage.Call(0)
	t.wg.Wait()
}

func (t *Tray) StopCh() <-chan struct{} {
	return t.stop
}
