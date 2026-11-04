package main

import (
	"errors"
	"strings"
	"syscall"
	"unsafe"
)

var user32 = syscall.NewLazyDLL("user32.dll")
var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
var procGetWindowText = user32.NewProc("GetWindowTextW")
var procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
var procOpenProcess = kernel32.NewProc("OpenProcess")
var procQueryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
var procCloseHandle = kernel32.NewProc("CloseHandle")

func foregroundWindow() (string, string, error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return "", "", errors.New("没有活动窗口")
	}
	buf := make([]uint16, 512)
	n, _, _ := procGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	title := syscall.UTF16ToString(buf[:n])
	var pid uint32
	_, _, _ = procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	h, _, _ := procOpenProcess.Call(0x1000, 0, uintptr(pid))
	if h == 0 {
		return "", title, nil
	}
	defer procCloseHandle.Call(h)
	name := make([]uint16, 1024)
	size := uint32(len(name))
	ok, _, _ := procQueryFullProcessImageName.Call(h, 0, uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return "", title, nil
	}
	path := syscall.UTF16ToString(name[:size])
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		path = path[i+1:]
	}
	return path, title, nil
}
