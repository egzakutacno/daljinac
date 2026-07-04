package tray

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	registerClassExW   = user32.NewProc("RegisterClassExW")
	createWindowExW    = user32.NewProc("CreateWindowExW")
	defWindowProcW     = user32.NewProc("DefWindowProcW")
	destroyWindow      = user32.NewProc("DestroyWindow")
	postQuitMessage    = user32.NewProc("PostQuitMessage")
	getMessageW        = user32.NewProc("GetMessageW")
	translateMessage   = user32.NewProc("TranslateMessage")
	dispatchMessageW   = user32.NewProc("DispatchMessageW")
	shellNotifyIconW   = shell32.NewProc("Shell_NotifyIconW")
	loadIconW          = user32.NewProc("LoadIconW")
	createPopupMenu    = user32.NewProc("CreatePopupMenu")
	appendMenuW        = user32.NewProc("AppendMenuW")
	trackPopupMenu     = user32.NewProc("TrackPopupMenu")
	destroyMenu        = user32.NewProc("DestroyMenu")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	getCursorPos       = user32.NewProc("GetCursorPos")
	postMessageW       = user32.NewProc("PostMessageW")
	openClipboard      = user32.NewProc("OpenClipboard")
	emptyClipboard     = user32.NewProc("EmptyClipboard")
	setClipboardData   = user32.NewProc("SetClipboardData")
	closeClipboard     = user32.NewProc("CloseClipboard")
	globalAlloc        = kernel32.NewProc("GlobalAlloc")
	globalLock         = kernel32.NewProc("GlobalLock")
	globalUnlock       = kernel32.NewProc("GlobalUnlock")
	rtlMoveMemory      = kernel32.NewProc("RtlMoveMemory")
	getModuleHandleW   = kernel32.NewProc("GetModuleHandleW")
	getLastError       = kernel32.NewProc("GetLastError")
)

const (
	WM_DESTROY    = 2
	WM_COMMAND    = 0x0111
	WM_APP        = 0x8000
	NIM_ADD       = 0
	NIM_MODIFY    = 1
	NIM_DELETE    = 2
	NIF_MESSAGE   = 1
	NIF_ICON      = 2
	NIF_TIP       = 4
	MF_STRING     = 0
	MF_SEPARATOR  = 0x0800
	MF_DISABLED   = 0x0002
	MF_GRAYED     = 0x0001
	GMEM_MOVEABLE = 0x0002
	CF_UNICODETEXT = 13
	WM_NULL       = 0
	IDI_APPLICATION = 32512
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

type Tray struct {
	hwnd     uintptr
	nid      NOTIFYICONDATAW
	hostname string
	version  string
	url      string
	running  bool
	mu       sync.RWMutex

	OnCopyURL       func()
	OnUpdate        func()
	OnRestartTunnel func()
	OnExit          func()
}

func New(hostname, version string) *Tray {
	return &Tray{hostname: hostname, version: version}
}

func (t *Tray) SetURL(u string) {
	t.mu.Lock()
	t.url = u
	t.mu.Unlock()
	t.updateTip()
}

func (t *Tray) SetRunning() {
	t.mu.Lock()
	t.running = true
	t.mu.Unlock()
	t.updateTip()
}

func (t *Tray) updateTip() {
	t.mu.RLock()
	s := fmt.Sprintf("Daljinac — %s", t.hostname)
	if t.url != "" {
		s += " [connected]"
	}
	h := t.hIcon()
	t.mu.RUnlock()

	t.nid.UFlags = NIF_ICON | NIF_TIP
	t.nid.HIcon = h
	copy(t.nid.SzTip[:], syscall.StringToUTF16(s))
	shellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&t.nid)))
}

func (t *Tray) hIcon() uintptr {
	if t.running {
		h, _, _ := loadIconW.Call(0, uintptr(IDI_APPLICATION))
		return h
	}
	h, _, _ := loadIconW.Call(0, uintptr(32512))
	return h
}

func (t *Tray) Run() {
	if runtime.GOOS != "windows" {
		log.Println("[tray] skipping: not windows")
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	log.Println("[tray] starting")

	hInstance, _, _ := getModuleHandleW.Call(0)
	className := syscall.StringToUTF16Ptr(fmt.Sprintf("DaljinacTray_%d", os.Getpid()))
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
	reg, _, _ := registerClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if reg == 0 {
		errCode, _, _ := getLastError.Call()
		log.Printf("[tray] RegisterClassExW failed (err=%d)", errCode)
		return
	}
	log.Println("[tray] class registered OK")

	hwnd, _, _ := createWindowExW.Call(0, uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, 0, hInstance, 0)
	if hwnd == 0 {
		errCode, _, _ := getLastError.Call()
		log.Printf("[tray] CreateWindowExW failed (err=%d)", errCode)
		return
	}
	t.hwnd = hwnd
	log.Println("[tray] window created OK")

	hIcon, _, _ := loadIconW.Call(0, uintptr(IDI_APPLICATION))
	t.nid = NOTIFYICONDATAW{
		HWnd:             hwnd,
		UID:              1,
		UFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
		UCallbackMessage: WM_APP + 1,
		HIcon:            hIcon,
	}
	t.nid.CbSize = uint32(unsafe.Sizeof(t.nid))
	copy(t.nid.SzTip[:], syscall.StringToUTF16(fmt.Sprintf("Daljinac v%s — %s", t.version, t.hostname)))
	add, _, _ := shellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&t.nid)))
	errCode, _, _ := getLastError.Call()
	log.Printf("[tray] icon added (ret=%d, err=%d)", add, errCode)

	var msg struct {
		HWnd    uintptr
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		PtX     int32
		PtY     int32
	}
	log.Println("[tray] entering message pump")
	for {
		ret, _, _ := getMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if ret == 0 {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}

	log.Println("[tray] message pump exited, cleaning up")
	shellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&t.nid)))
	destroyWindow.Call(hwnd)
}

func (t *Tray) RemoveIcon() {
	if t.hwnd != 0 {
		ret, _, _ := shellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&t.nid)))
		log.Printf("[tray] RemoveIcon: NIM_DELETE ret=%d", ret)
	}
}

func (t *Tray) Stop() {
	if t.hwnd != 0 {
		postMessageW.Call(t.hwnd, WM_DESTROY, 0, 0)
	}
}

func (t *Tray) wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_DESTROY:
		postQuitMessage.Call(0)
		return 0
	case WM_COMMAND:
		t.handleCmd(int(wParam) & 0xFFFF)
		return 0
	case WM_APP + 1:
		if lParam == 0x0204 {
			t.showMenu()
		} else if lParam == 0x0201 {
			t.mu.RLock()
			u := t.url
			t.mu.RUnlock()
			if u != "" {
				shell32dll := syscall.NewLazyDLL("shell32.dll")
				se := shell32dll.NewProc("ShellExecuteW")
				se.Call(0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("open"))),
					uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(u))), 0, 0, 5)
			}
		}
		return 0
	}
	ret, _, _ := defWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}

func (t *Tray) showMenu() {
	hMenu, _, _ := createPopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer destroyMenu.Call(hMenu)

	ptr := func(s string) uintptr {
		return uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(s)))
	}

	t.mu.RLock()
	hasURL := t.url != ""
	isRunning := t.running
	t.mu.RUnlock()

	statusStr := "Starting..."
	if isRunning {
		if hasURL {
			statusStr = "Connected"
		} else {
			statusStr = "Running"
		}
	}
	label := fmt.Sprintf("Daljinac v%s — %s — %s", t.version, t.hostname, statusStr)
	appendMenuW.Call(hMenu, MF_DISABLED|MF_GRAYED, 0, ptr(label))
	appendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)

	if isRunning {
		appendMenuW.Call(hMenu, MF_STRING, 1004, ptr("Restart Tunnel"))
	} else {
		appendMenuW.Call(hMenu, MF_DISABLED|MF_GRAYED, 1004, ptr("Restart Tunnel"))
	}
	if hasURL {
		appendMenuW.Call(hMenu, MF_STRING, 1001, ptr("Copy URL"))
	} else {
		appendMenuW.Call(hMenu, MF_DISABLED|MF_GRAYED, 1001, ptr("Copy URL"))
	}
	appendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
	appendMenuW.Call(hMenu, MF_STRING, 1002, ptr("Check for Updates"))
	appendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
	appendMenuW.Call(hMenu, MF_STRING, 1003, ptr("Exit"))

	var p POINT
	getCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	setForegroundWindow.Call(t.hwnd)
	trackPopupMenu.Call(hMenu, 0, uintptr(p.X), uintptr(p.Y), 0, t.hwnd, 0)
	postMessageW.Call(t.hwnd, WM_NULL, 0, 0)
}

func (t *Tray) handleCmd(cmd int) {
	switch cmd {
	case 1001:
		t.mu.RLock()
		u := t.url
		t.mu.RUnlock()
		if u == "" {
			return
		}
		utf16 := syscall.StringToUTF16(u)
		openClipboard.Call(t.hwnd)
		emptyClipboard.Call()
		hMem, _, _ := globalAlloc.Call(GMEM_MOVEABLE, uintptr(len(utf16)*2))
		if hMem != 0 {
			pMem, _, _ := globalLock.Call(hMem)
			if pMem != 0 {
				rtlMoveMemory.Call(pMem, uintptr(unsafe.Pointer(&utf16[0])), uintptr(len(utf16)*2))
				globalUnlock.Call(hMem)
				setClipboardData.Call(CF_UNICODETEXT, hMem)
			}
		}
		closeClipboard.Call()
	case 1002:
		if t.OnUpdate != nil {
			go t.OnUpdate()
		}
	case 1004:
		if t.OnRestartTunnel != nil {
			log.Println("[tray] restarting tunnel")
			go t.OnRestartTunnel()
		}
	case 1003:
		if t.OnExit != nil {
			go t.OnExit()
		} else {
			postQuitMessage.Call(0)
		}
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
