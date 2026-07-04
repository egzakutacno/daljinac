package tray

import (
	"encoding/binary"
	"log"
	"syscall"
	"unsafe"
)

var (
	gdi32          = syscall.NewLazyDLL("gdi32.dll")
	createDIBitmap = gdi32.NewProc("CreateDIBSection")
)

type BITMAPINFO struct {
	Header BITMAPINFOHEADER
	Colors [4]byte
}

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

func makeICO(r, g, b byte) []byte {
	w, h := 16, 16
	xorSize := w * h * 4
	andSize := (w * h) / 8

	bih := make([]byte, 40)
	binary.LittleEndian.PutUint32(bih[0:], 40)
	binary.LittleEndian.PutUint32(bih[4:], uint32(w))
	binary.LittleEndian.PutUint32(bih[8:], uint32(h*2))
	binary.LittleEndian.PutUint16(bih[12:], 1)
	binary.LittleEndian.PutUint16(bih[14:], 32)
	binary.LittleEndian.PutUint32(bih[16:], 0)

	xor := make([]byte, xorSize)
	cx, cy := w/2, h/2
	radius2 := (w/2 - 1) * (w/2 - 1)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x-cx, y-cy
			off := (y*w + x) * 4
			if dx*dx+dy*dy <= radius2 {
				xor[off+0] = b
				xor[off+1] = g
				xor[off+2] = r
				xor[off+3] = 255
			}
		}
	}

	andMask := make([]byte, andSize)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy > radius2 {
				byteIdx := y*(w/8) + x/8
				bitIdx := uint(7 - (x % 8))
				andMask[byteIdx] |= (1 << bitIdx)
			}
		}
	}

	headerSize := 6 + 16
	totalSize := headerSize + len(bih) + len(xor) + len(andMask)
	ico := make([]byte, totalSize)

	binary.LittleEndian.PutUint16(ico[0:], 0)
	binary.LittleEndian.PutUint16(ico[2:], 1)
	binary.LittleEndian.PutUint16(ico[4:], 1)

	off := 6
	ico[off+0] = byte(w)
	ico[off+1] = byte(h)
	ico[off+2] = 0
	ico[off+3] = 0
	binary.LittleEndian.PutUint16(ico[off+4:], 1)
	binary.LittleEndian.PutUint16(ico[off+6:], 32)
	imgSize := uint32(len(bih) + len(xor) + len(andMask))
	binary.LittleEndian.PutUint32(ico[off+8:], imgSize)
	binary.LittleEndian.PutUint32(ico[off+12:], uint32(headerSize))

	copy(ico[headerSize:], bih)
	copy(ico[headerSize+40:], xor)
	copy(ico[headerSize+40+xorSize:], andMask)

	return ico
}

func createColorIcon(r, g, b byte) uintptr {
	icoData := makeICO(r, g, b)
	h, _, err := createIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&icoData[0])),
		uintptr(len(icoData)),
		1,
		0x00030000,
		0, 0, 0,
	)
	if h == 0 {
		log.Printf("[tray] createColorIcon(%d,%d,%d) failed (err=%d)", r, g, b, err)
	}
	return h
}

func destroyIcon(hicon uintptr) {
	if hicon != 0 {
		destroyIconProc.Call(hicon)
	}
}
