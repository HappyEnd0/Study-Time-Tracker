package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"syscall"
	"unsafe"
)

var gdi32 = syscall.NewLazyDLL("gdi32.dll")
var pGetWindowRect = user32.NewProc("GetWindowRect")
var pGetWindowDC = user32.NewProc("GetWindowDC")
var pReleaseDC = user32.NewProc("ReleaseDC")
var pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
var pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
var pSelectObject = gdi32.NewProc("SelectObject")
var pBitBlt = gdi32.NewProc("BitBlt")
var pGetDIBits = gdi32.NewProc("GetDIBits")
var pDeleteObject = gdi32.NewProc("DeleteObject")
var pDeleteDC = gdi32.NewProc("DeleteDC")

type nativeRect struct{ Left, Top, Right, Bottom int32 }
type bitmapHeader struct {
	Size                         uint32
	Width, Height                int32
	Planes, BitCount             uint16
	Compression, SizeImage       int32
	XPelsPerMeter, YPelsPerMeter int32
	ClrUsed, ClrImportant        uint32
}
type bitmapInfo struct {
	Header bitmapHeader
	Colors [1]uint32
}

func captureActiveWindowJPEG() ([]byte, error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return nil, errors.New("no foreground window")
	}
	var r nativeRect
	ok, _, _ := pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ok == 0 {
		return nil, errors.New("GetWindowRect failed")
	}
	w, h := int(r.Right-r.Left), int(r.Bottom-r.Top)
	if w < 1 || h < 1 || w > 6000 || h > 6000 {
		return nil, errors.New("invalid capture dimensions")
	}
	src, _, _ := pGetWindowDC.Call(hwnd)
	if src == 0 {
		return nil, errors.New("GetWindowDC failed")
	}
	defer pReleaseDC.Call(hwnd, src)
	dc, _, _ := pCreateCompatibleDC.Call(src)
	if dc == 0 {
		return nil, errors.New("CreateCompatibleDC failed")
	}
	defer pDeleteDC.Call(dc)
	bmp, _, _ := pCreateCompatibleBitmap.Call(src, uintptr(w), uintptr(h))
	if bmp == 0 {
		return nil, errors.New("CreateCompatibleBitmap failed")
	}
	defer pDeleteObject.Call(bmp)
	old, _, _ := pSelectObject.Call(dc, bmp)
	defer pSelectObject.Call(dc, old)
	const srcCopy = 0x00CC0020
	ok, _, _ = pBitBlt.Call(dc, 0, 0, uintptr(w), uintptr(h), src, 0, 0, srcCopy)
	if ok == 0 {
		return nil, errors.New("BitBlt failed")
	}
	info := bitmapInfo{Header: bitmapHeader{Size: uint32(unsafe.Sizeof(bitmapHeader{})), Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32, Compression: 0}}
	pixels := make([]byte, w*h*4)
	lines, _, _ := pGetDIBits.Call(dc, bmp, 0, uintptr(h), uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&info)), 0)
	if lines == 0 {
		return nil, errors.New("GetDIBits failed")
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			img.SetRGBA(x, y, color.RGBA{R: pixels[i+2], G: pixels[i+1], B: pixels[i], A: 255})
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 55}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
