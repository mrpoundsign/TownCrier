//go:build windows

package ui

import (
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
)

var (
	procIsZoomed      = user32.NewProc("IsZoomed")
	procShowWindow    = user32.NewProc("ShowWindow")
	procGetClientRect = user32.NewProc("GetClientRect")
)

const (
	swNormal   uintptr = 1
	swMaximize uintptr = 3
)

type winRect struct {
	Left, Top, Right, Bottom int32
}

func getWin32Handle(w fyne.Window) uintptr {
	if w == nil {
		return 0
	}
	nw, ok := w.(driver.NativeWindow)
	if !ok {
		return 0
	}
	var hwnd uintptr
	nw.RunNative(func(ctx any) {
		if winCtx, ok := ctx.(driver.WindowsWindowContext); ok {
			hwnd = winCtx.HWND
		}
	})
	return hwnd
}

func isWindowMaximized(w fyne.Window) bool {
	hwnd := getWin32Handle(w)
	if hwnd == 0 {
		return false
	}
	ret, _, _ := procIsZoomed.Call(hwnd)
	return ret != 0
}

func restoreMaximizedWindow(w fyne.Window) {
	hwnd := getWin32Handle(w)
	if hwnd == 0 {
		return
	}
	// Reset and re-maximize so Win32 snaps the window back to the monitor work area
	// after GLFW's RescaleContext SetSize call, and sync Fyne's canvas to the client rect.
	_, _, _ = procShowWindow.Call(hwnd, swNormal)
	_, _, _ = procShowWindow.Call(hwnd, swMaximize)

	var rc winRect
	if ret, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc))); ret != 0 {
		clientW := float32(rc.Right - rc.Left)
		clientH := float32(rc.Bottom - rc.Top)
		sc := w.Canvas().Scale()
		if clientW > 0 && clientH > 0 && sc > 0 {
			if cr, ok := w.Canvas().(interface{ Resize(fyne.Size) }); ok {
				cr.Resize(fyne.NewSize(clientW/sc, clientH/sc))
			}
		}
	}
}
