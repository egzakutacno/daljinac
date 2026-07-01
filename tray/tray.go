package tray

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	registerClassExW  = user32.NewProc("RegisterClassExW")
	createWindowExW   = user32.NewProc("CreateWindowExW")
	defWindowProcW    = user32.NewProc("DefWindowProcW")
	destroyWindow     = user32.NewProc("DestroyWindow")
	postQuitMessage   = user32.NewProc("PostQuitMessage")
	getMessageW       = user32.NewProc("GetMessageW")
	translateMessage  = user32.NewProc("TranslateMessage")
	dispatchMessageW  = user32.NewProc("DispatchMessageW")
	shellNotifyIconW  = shell32.NewProc("Shell_NotifyIconW")
	loadIconW         = user32.NewProc("LoadIconW")
	createPopupMenu   = user32.NewProc("CreatePopupMenu")
	appendMenuW       = user32.NewProc("AppendMenuW")
	trackPopupMenu    = user32.NewProc("TrackPopupMenu")
	destroyMenu       = user32.NewProc("DestroyMenu")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	getCursorPos      = user32.NewProc("GetCursorPos")
	postMessageW      = user32.NewProc("PostMessageW")
	openClipboard     = user32.NewProc("OpenClipboard")
	emptyClipboard    = user32.NewProc("EmptyClipboard")
	setClipboardData  = user32.NewProc("SetClipboardData")
	closeClipboard    = user32.NewProc("CloseClipboard")
	globalAlloc       = kernel32.NewProc("GlobalAlloc")
	globalLock        = kernel32.NewProc("GlobalLock")
	globalUnlock      = kernel32.NewProc("GlobalUnlock")
	rtlMoveMemory     = kernel32.NewProc("RtlMoveMemory")
	getModuleHandleW  = kernel32.NewProc("GetModuleHandleW")
)

const (
	WM_DESTROY = 2
	WM_COMMAND = 0x0111
	WM_TIMER   = 0x0113
	WM_APP     = 0x8000

	NIM_ADD     = 0
	NIM_MODIFY  = 1
	NIM_DELETE  = 2
	NIM_SETVERSION = 4

	NIF_MESSAGE = 1
	NIF_ICON    = 2
	NIF_TIP     = 4
	NIF_INFO    = 0x10

	MF_STRING    = 0
	MF_SEPARATOR = 0x0800
	MF_DISABLED  = 0x0002
	MF_GRAYED    = 0x0001

	TPM_RIGHTALIGN  = 0x0008
	TPM_BOTTOMALIGN = 0x0020

	NIIF_INFO  = 1
	NIIF_ERROR = 3

	GMEM_MOVEABLE  = 0x0002
	CF_UNICODETEXT = 13
	WM_NULL        = 0

	IDI_APPLICATION = 32512
	IDI_INFORMATION = 32517
	IDI_ERROR       = 32513
	IDI_SHIELD      = 32518
)

const (
	IDM_COPY_URL = 2000 + iota
	IDM_SEP1
	IDM_UPDATE
	IDM_SEP2
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

type POINT struct{ X, Y int32 }

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

type Status int

const (
	StatusStopped  Status = 0
	StatusStarting Status = 1
	StatusRunning  Status = 2
	StatusError    Status = 3
)

func (s Status) String() string {
	switch s {
	case StatusStopped:
		return "Stopped"
	case StatusStarting:
		return "Starting"
	case StatusRunning:
		return "Running"
	case StatusError:
		return "Error"
	default:
		return "Unknown"
	}
}

type Tray struct {
	hwnd       uintptr
	nid        NOTIFYICONDATAW
	hostname   string
	url        string
	status     Status
	mu         sync.RWMutex
	stopCh     chan struct{}
	doneCh     chan struct{}
	cmdCh      chan trayCmd
	hIcons     [4]uintptr

	OnCopyURL func()
	OnUpdate  func()
	OnExit    func()
}

type trayCmd int

const (
	cmdRefresh trayCmd = iota
	cmdStop
)

func New(hostname string) *Tray {
	return &Tray{
		hostname: hostname,
		status:   StatusStopped,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		cmdCh:    make(chan trayCmd, 10),
	}
}

func (t *Tray) SetURL(url string) {
	t.mu.Lock()
	t.url = url
	t.mu.Unlock()
	t.cmdCh <- cmdRefresh
}

func (t *Tray) SetStatus(s Status) {
	t.mu.Lock()
	t.status = s
	t.mu.Unlock()
	t.cmdCh <- cmdRefresh
}

func (t *Tray) Run() {
	if runtime.GOOS != "windows" {
		return
	}
	go t.runLoop()
}

func (t *Tray) StopCh() <-chan struct{} {
	return t.stopCh
}

func (t *Tray) Stop() {
	close(t.stopCh)
	if t.hwnd != 0 {
		postQuitMessage.Call(0)
	}
	<-t.doneCh
}

func (t *Tray) runLoop() {
	defer close(t.doneCh)

	hInstance, _, _ := getModuleHandleW.Call(0)
	className := syscall.StringToUTF16Ptr("DaljinacTray")

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
	ret, _, _ := registerClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if ret == 0 {
		return
	}

	hwnd, _, _ := createWindowExW.Call(0, uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, hInstance, 0)
	if hwnd == 0 {
		return
	}
	t.hwnd = hwnd

	t.loadIcons()

	tip := t.label()
	t.nid = NOTIFYICONDATAW{
		HWnd:             hwnd,
		UID:              1,
		UFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
		UCallbackMessage: WM_APP + 1,
		HIcon:            t.hIcons[StatusStopped],
	}
	t.nid.CbSize = uint32(unsafe.Sizeof(t.nid))
	copy(t.nid.SzTip[:], syscall.StringToUTF16(tip))

	shellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&t.nid)))

	var v uint32 = 3
	t.nid.UVersion = v
	shellNotifyIconW.Call(NIM_SETVERSION, uintptr(unsafe.Pointer(&t.nid)))

	t.setupTimer()

	go t.cmdProcessor()

	var msg struct {
		HWnd    uintptr
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		PtX     int32
		PtY     int32
	}
	for {
		ret, _, _ := getMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if ret == 0 || ret == ^uintptr(0) {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}

	shellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&t.nid)))
	destroyWindow.Call(hwnd)
}

func (t *Tray) setupTimer() {
	// Set a timer to periodically refresh the tooltip
	user32.NewProc("SetTimer").Call(t.hwnd, 1, 5000, 0)
}

func (t *Tray) cmdProcessor() {
	for {
		select {
		case cmd := <-t.cmdCh:
			switch cmd {
			case cmdRefresh:
				t.refreshUI()
			case cmdStop:
				postQuitMessage.Call(0)
				return
			}
		case <-t.stopCh:
			return
		}
	}
}

func (t *Tray) refreshUI() {
	t.mu.RLock()
	status := t.status
	tip := t.label() + " — " + status.String()
	t.mu.RUnlock()

	iconIdx := int(status)
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

func (t *Tray) loadIcons() {
	t.hIcons[StatusStopped], _, _ = loadIconW.Call(0, uintptr(IDI_SHIELD))
	t.hIcons[StatusStarting], _, _ = loadIconW.Call(0, uintptr(IDI_INFORMATION))
	t.hIcons[StatusRunning], _, _ = loadIconW.Call(0, uintptr(IDI_APPLICATION))
	t.hIcons[StatusError], _, _ = loadIconW.Call(0, uintptr(IDI_ERROR))
}

func (t *Tray) label() string {
	return "Daljinac — " + t.hostname
}

func (t *Tray) wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_DESTROY:
		return 0

	case WM_TIMER:
		if wParam == 1 {
			t.mu.RLock()
			status := t.status
			tip := t.label() + " — " + status.String()
			t.mu.RUnlock()
			iconIdx := int(status)
			if iconIdx < 0 || iconIdx >= len(t.hIcons) {
				iconIdx = 0
			}
			t.nid.UFlags = NIF_ICON | NIF_TIP
			t.nid.HIcon = t.hIcons[iconIdx]
			copy(t.nid.SzTip[:], syscall.StringToUTF16(tip))
			shellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&t.nid)))
		}
		return 0

	case WM_APP + 1:
		switch lParam {
		case 0x0204: // WM_RBUTTONUP
			t.showContextMenu()
		case 0x0201: // WM_LBUTTONUP
			t.mu.RLock()
			url := t.url
			t.mu.RUnlock()
			if url != "" {
				t.openURL(url)
			}
		case 0x0400: // NIN_BALLOONUSERCLICK
			t.mu.RLock()
			url := t.url
			t.mu.RUnlock()
			if url != "" {
				t.openURL(url)
			}
		}
		return 0

	case WM_COMMAND:
		t.handleMenuCommand(int(wParam) & 0xFFFF)
		return 0
	}

	ret, _, _ := defWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
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

	t.mu.RLock()
	status := t.status
	url := t.url
	t.mu.RUnlock()

	label := t.label() + " — " + status.String()
	appendMenuW.Call(hMenu, MF_DISABLED|MF_GRAYED, 0, ptr(label))
	appendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)

	if url != "" && status == StatusRunning {
		appendMenuW.Call(hMenu, MF_STRING, IDM_COPY_URL, ptr("Copy URL"))
	} else {
		appendMenuW.Call(hMenu, MF_DISABLED|MF_GRAYED, IDM_COPY_URL, ptr("Copy URL"))
	}
	appendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)

	appendMenuW.Call(hMenu, MF_STRING, IDM_UPDATE, ptr("Check for Updates"))
	appendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
	appendMenuW.Call(hMenu, MF_STRING, IDM_EXIT, ptr("Exit"))

	var p POINT
	getCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	setForegroundWindow.Call(t.hwnd)
	trackPopupMenu.Call(hMenu, TPM_RIGHTALIGN|TPM_BOTTOMALIGN, uintptr(p.X), uintptr(p.Y), 0, t.hwnd, 0)
	postMessageW.Call(t.hwnd, WM_NULL, 0, 0)
}

func (t *Tray) handleMenuCommand(cmd int) {
	switch cmd {
	case IDM_COPY_URL:
		t.mu.RLock()
		url := t.url
		t.mu.RUnlock()
		if url != "" {
			t.copyURL(url)
		}
	case IDM_UPDATE:
		if t.OnUpdate != nil {
			go t.OnUpdate()
		}
	case IDM_EXIT:
		if t.OnExit != nil {
			go t.OnExit()
		} else {
			postQuitMessage.Call(0)
		}
	}
}

func (t *Tray) openURL(url string) {
	shell32dll := syscall.NewLazyDLL("shell32.dll")
	shellExecuteW := shell32dll.NewProc("ShellExecuteW")
	shellExecuteW.Call(0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("open"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(url))),
		0, 0, 5)
}

func (t *Tray) copyURL(url string) {
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

func (t *Tray) showBalloon(title, text string, iconType uint32) {
	t.nid.UFlags = NIF_INFO
	t.nid.DwInfoFlags = iconType
	t.nid.UVersion = 3
	copy(t.nid.SzInfoTitle[:], syscall.StringToUTF16(title))
	copy(t.nid.SzInfo[:], syscall.StringToUTF16(text))

	if t.hwnd != 0 {
		shellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&t.nid)))
	}
}

func GetExecutablePath() (string, error) {
	buf := make([]uint16, 1024)
	ret, _, _ := kernel32.NewProc("GetModuleFileNameW").Call(0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if ret == 0 {
		return "", fmt.Errorf("GetModuleFileNameW failed")
	}
	return syscall.UTF16ToString(buf[:ret]), nil
}

func OSUserame() string {
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
