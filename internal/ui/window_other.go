//go:build !windows

package ui

import "fyne.io/fyne/v2"

func isWindowMaximized(_ fyne.Window) bool {
	return false
}

func restoreMaximizedWindow(_ fyne.Window) {}
