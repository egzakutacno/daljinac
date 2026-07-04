package tray

import (
	"log"
	"syscall"
	"unsafe"
)

var (
	gdi32          = syscall.NewLazyDLL("gdi32.dll")
	createDIBitmap = gdi32.NewProc("CreateDIBSection")
	createBitmap   = gdi32.NewProc("CreateBitmap")
	createCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	deleteDC       = gdi32.NewProc("DeleteDC")
	deleteObject   = gdi32.NewProc("DeleteObject")
	selectObject   = gdi32.NewProc("SelectObject")
)

type BITMAPINFOHEADER struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type BITMAPINFO struct {
	Header BITMAPINFOHEADER
	Colors [4]byte
}

type ICONINFO struct {
	FIcon    uint32
	XHotspot uint32
	YHotspot uint32
	HbmMask  uintptr
	HbmColor uintptr
}

func createColorIcon(r, g, b byte) uintptr {
	w, h := 16, 16
	dc, _, _ := createCompatibleDC.Call(0)
	if dc == 0 {
		log.Printf("[tray] createColorIcon: CreateCompatibleDC failed")
		return 0
	}
	defer deleteDC.Call(dc)

	bmi := BITMAPINFO{
		Header: BITMAPINFOHEADER{
			Size:     40,
			Width:    int32(w),
			Height:   int32(-h),
			Planes:   1,
			BitCount: 32,
		},
	}

	var bits uintptr
	hbmp, _, _ := createDIBitmap.Call(
		dc,
		uintptr(unsafe.Pointer(&bmi)),
		0,
		uintptr(unsafe.Pointer(&bits)),
		0, 0,
	)
	if hbmp == 0 {
		log.Printf("[tray] createColorIcon: CreateDIBSection failed")
		return 0
	}
	defer deleteObject.Call(hbmp)

	if bits == 0 {
		log.Printf("[tray] createColorIcon: got nil bits")
		return 0
	}

	pixels := (*[16 * 16 * 4]byte)(unsafe.Pointer(bits))[:16*16*4]
	cx, cy := w/2, h/2
	radius2 := (w/2 - 1) * (w/2 - 1)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x-cx, y-cy
			off := (y*w + x) * 4
			if dx*dx+dy*dy <= radius2 {
				pixels[off+0] = b
				pixels[off+1] = g
				pixels[off+2] = r
				pixels[off+3] = 255
			}
		}
	}

	mask, _, _ := createBitmap.Call(uintptr(w), uintptr(h), 1, 1, 0)
	if mask == 0 {
		log.Printf("[tray] createColorIcon: CreateBitmap mask failed")
		return 0
	}
	defer deleteObject.Call(mask)

	ii := ICONINFO{FIcon: 1, HbmMask: mask, HbmColor: hbmp}
	hicon, _, _ := createIconIndirectProc.Call(uintptr(unsafe.Pointer(&ii)))
	if hicon == 0 {
		log.Printf("[tray] createColorIcon: CreateIconIndirect failed")
		return 0
	}
	log.Printf("[tray] createColorIcon(%d,%d,%d) = %#x", r, g, b, hicon)
	return hicon
}

func destroyIcon(hicon uintptr) {
	if hicon != 0 {
		destroyIconProc.Call(hicon)
	}
}
